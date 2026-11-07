//go:build dev

package web

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/upload"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// usersProbeJS is adminProbeJS with the touch-target rule fitted to checkboxes and radios: the
// control counts through its label or switch row, as a finger would hit it.
const usersProbeJS = `(() => {
  const small = [];
  for (const el of document.querySelectorAll("main input:not([type=hidden]), main button, main a.ts-btn, main select, body.ts-frame button")) {
    const box = el.closest("label, .ts-switch-row") || el;
    const r = box.getBoundingClientRect();
    if (r.width > 0 && r.height < 43.5) small.push((el.id || el.name || el.className || el.tagName) + ":" + Math.round(r.height));
  }
  const labels = [...document.querySelectorAll("main input:not([type=hidden]), main select")].filter(i => !i.closest("label") && !i.getAttribute("aria-label") && !document.querySelector('label[for="' + i.id + '"]')).map(i => i.id || i.name);
  return {over: document.documentElement.scrollWidth > innerWidth, theme: document.documentElement.dataset.theme || "",
    small, unlabelled: labels, h1: document.querySelectorAll("h1").length};
})()`

// usersBoardSeed fills the app with the board's roster: Daniel (owner, the signed-in person), Maya,
// Rosa, Lena and Tomas, listening time, and for Rosa a shelf, an ended library grant and a book
// grant with an end date. It returns Rosa's user id.
func usersBoardSeed(t *testing.T, a *testApp) string {
	t.Helper()
	ctx := context.Background()
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := a.st.Writer().Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	ownerID := sweepUserID(t, a, a.owner(t))
	exec(`UPDATE users SET display_name = 'Daniel Reyes' WHERE id = ?`, ownerID)
	ids := map[string]string{
		"Maya Chen":   sweepUserID(t, a, a.as(t, users.RoleMember)),
		"Rosa Reyes":  sweepUserID(t, a, a.as(t, users.RoleGuest)),
		"Lena Reyes":  sweepUserID(t, a, a.as(t, users.RoleMember)),
		"Tomás Reyes": sweepUserID(t, a, a.as(t, users.RoleGuest)),
	}
	for name, id := range ids {
		exec(`UPDATE users SET display_name = ? WHERE id = ?`, name, id)
	}

	l, f := librarytest.NewLibrary(t, a.lib, "Kids", adminAudioDir(t, true))
	var bookIDs []string
	for _, title := range []string{"Saltmarsh", "The Salt Road Letters", "Weather for Beginners", "Lantern Street"} {
		b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: title, Files: 1, DurationMs: 3_600_000, AddedAt: 5, Authors: []string{"Imogen Hale"}})
		bookIDs = append(bookIDs, b.ID)
	}
	sh, err := a.lib.CreateShelf(ctx, ownerID, "For Mamá")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range bookIDs[:2] {
		if err := a.lib.AddToShelf(ctx, ownerID, sh.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	nowMs := now.UnixMilli()
	end := now.AddDate(0, 0, 20).UnixMilli()
	exec(`INSERT INTO grants(id, user_id, scope, shelf_id, can_download, created_at, updated_at) VALUES ('gv1', ?, 'shelf', ?, 1, 1, 1)`, ids["Rosa Reyes"], sh.ID)
	// An ended grant: it ran out three days ago (a real date, so the date field shows one).
	ended := now.AddDate(0, 0, -3).UnixMilli()
	exec(`INSERT INTO grants(id, user_id, scope, library_id, expires_at, created_at, updated_at) VALUES ('gv2', ?, 'library', ?, ?, 1, 1)`, ids["Rosa Reyes"], l.ID, ended)
	exec(`INSERT INTO grants(id, user_id, scope, book_id, can_read, expires_at, created_at, updated_at) VALUES ('gv3', ?, 'book', ?, 1, ?, 3, 3)`, ids["Rosa Reyes"], bookIDs[3], end)
	exec(`INSERT INTO grants(id, user_id, scope, created_at, updated_at) VALUES ('gv4', ?, 'all', 1, 1)`, ids["Maya Chen"])
	exec(`INSERT INTO grants(id, user_id, scope, created_at, updated_at) VALUES ('gv5', ?, 'all', 1, 1)`, ids["Lena Reyes"])
	exec(`INSERT INTO grants(id, user_id, scope, library_id, created_at, updated_at) VALUES ('gv6', ?, 'library', ?, 1, 1)`, ids["Tomás Reyes"], l.ID)

	// Listening: hours land inside this month; the end time sets "last active".
	monthStart := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location()).UnixMilli() + 1000
	session := func(id, user string, hours, endedAt int64) {
		exec(`INSERT INTO listening_sessions(id, user_id, book_id, device_id, started_at, ended_at, start_ms, end_ms, listened_ms, speed_pct)
			VALUES (?, ?, ?, 'dev', ?, ?, 0, 1, ?, 100)`, id, user, bookIDs[0], min(monthStart, endedAt-1000), endedAt, hours*3_600_000)
	}
	day := int64(24 * 3_600_000)
	session("lv1", ownerID, 31, nowMs-200_000)
	session("lv2", ids["Maya Chen"], 24, nowMs-2*3_600_000)
	session("lv3", ids["Rosa Reyes"], 9, nowMs-5_000)
	session("lv4", ids["Lena Reyes"], 6, nowMs-3*day)
	session("lv5", ids["Tomás Reyes"], 2, nowMs-9*day)
	return ids["Rosa Reyes"]
}

// TestVisualUsersPages captures the Users list, a person's page with grants, the page with book
// search results (the add-grant form) and both confirm pages. The three Users boards are copied
// beside the captures for a side-by-side look; the editor and confirm pages have no board.
func TestVisualUsersPages(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	app := newTestApp(t)
	rosa := usersBoardSeed(t, app)
	deviceFor(t, app, rosa, "Living room speaker", auth.KindMobile)
	deviceFor(t, app, rosa, "Reading tablet", auth.KindPWA)
	// The list sorts by creation time; two devices made a millisecond apart would swap places between runs.
	if _, err := app.st.Writer().Exec(`UPDATE devices SET created_at = created_at - CASE name WHEN 'Living room speaker' THEN 2000 ELSE 1000 END WHERE user_id = ?`, rosa); err != nil {
		t.Fatal(err)
	}
	// A contributor with two books they added, a folder of their own and a finished remove-all
	// job that kept one book (decision 0029). The folder is a fixed string so no temp path shows.
	ines := sweepUserID(t, app, app.as(t, users.RoleMember))
	var folderID, libID string
	if err := app.st.Reader().QueryRow(`SELECT id, library_id FROM library_folders ORDER BY id LIMIT 1`).Scan(&folderID, &libID); err != nil {
		t.Fatal(err)
	}
	rows, err := app.st.Reader().Query(`SELECT root_path, title FROM books ORDER BY title LIMIT 2`)
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for n := 0; rows.Next(); n++ {
		var root, title string
		if err := rows.Scan(&root, &title); err != nil {
			t.Fatal(err)
		}
		kept = append(kept, title)
		if _, err := app.st.Writer().Exec(`INSERT INTO upload_books(root_path, upload_id, library_id, added_by, created_at) VALUES (?, ?, ?, ?, 1)`,
			root, fmt.Sprintf("up_vis%d", n), libID, ines); err != nil {
			t.Fatal(err)
		}
	}
	_ = rows.Close()
	for q, args := range map[string][]any{
		`UPDATE users SET display_name = 'Ines Moreno', adds_books = 1 WHERE id = ?`:                                                 {ines},
		`INSERT INTO contributor_folders(folder_id, user_id, path, created_at) VALUES (?, ?, '/audiobooks/Added by Ines Moreno', 1)`: {folderID, ines},
	} {
		if _, err := app.st.Writer().Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	if err := app.runner.Register(jobs.Kind{Name: upload.KindRemoveAll, Concurrency: 1, MaxAttempts: 1,
		Handler: func(context.Context, *jobs.Task) (any, error) { return nil, nil }}); err != nil {
		t.Fatal(err)
	}
	jobID, err := app.runner.Enqueue(context.Background(), upload.KindRemoveAll, upload.RemoveAllPayload{UserID: ines, By: "x"}, jobs.EnqueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	result := `{"removed":3,"kept":[{"book_id":"b1","title":"` + kept[0] + `","reason":"changed"},{"book_id":"b2","title":"` + kept[1] + `","reason":"not_theirs"}]}`
	if _, err := app.st.Writer().Exec(`UPDATE jobs SET state = 'done', result_json = ? WHERE id = ?`, result, jobID); err != nil {
		t.Fatal(err)
	}
	base := themedServer(t, app.signedIn(t))

	for _, name := range []string{"UsersPage", "UsersTablet", "UsersPhone"} {
		src, err := os.Open(filepath.Join("..", "..", "docs", "design", "tendshelf-handoff", "screens", "admin", name+".png"))
		if err != nil {
			continue
		}
		if dst, err := os.Create(filepath.Join(out, "users-board-"+name+".png")); err == nil {
			_, _ = io.Copy(dst, src)
			_ = dst.Close()
		}
		_ = src.Close()
	}

	pgs := []struct{ name, path string }{
		{"list", "/admin/users"},
		{"person", "/admin/users/" + rosa},
		{"person-search", "/admin/users/" + rosa + "?q=Salt"},
		{"confirm-stop", "/admin/users/" + rosa + "/stop"},
		{"confirm-disable", "/admin/users/" + rosa + "/disable"},
		{"contributor", "/admin/users/" + ines},
		{"confirm-remove-all", "/admin/users/" + ines + "/books/remove"},
		{"remove-all-done", "/admin/users/" + ines + "/books/remove?job=" + jobID},
	}
	for _, pg := range pgs {
		for _, sh := range visualMatrix(true) {
			theme, w := sh.Theme, sh.Width
			t.Run(fmt.Sprintf("%s-%s-%d", pg.name, theme, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w == 390, false)
				sep := "?"
				if strings.Contains(pg.path, "?") {
					sep = "&"
				}
				tab.Navigate(base + pg.path + sep + "theme=" + theme)
				var m struct {
					Over       bool     `json:"over"`
					Theme      string   `json:"theme"`
					Small      []string `json:"small"`
					Unlabelled []string `json:"unlabelled"`
					H1         int      `json:"h1"`
				}
				tab.Eval(usersProbeJS, &m)
				if m.Over {
					t.Errorf("horizontal overflow")
				}
				if m.Theme != theme {
					t.Errorf("data-theme = %q, want %q", m.Theme, theme)
				}
				if len(m.Small) > 0 {
					t.Errorf("targets under 44 px: %v", m.Small)
				}
				if len(m.Unlabelled) > 0 {
					t.Errorf("inputs without a label: %v", m.Unlabelled)
				}
				if m.H1 != 1 {
					t.Errorf("%d h1 elements", m.H1)
				}
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("users-%s-%s-%d.png", pg.name, theme, w)))
			})
		}
	}
	t.Logf("screenshots in %s", out)
}
