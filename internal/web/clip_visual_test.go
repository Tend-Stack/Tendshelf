//go:build dev

package web

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// TestClipVisual captures the Bookmarks tab with two clips and a quote (the clip and kind chips, the kind and
// length under a row), the clip filter and the note sheet of a clip (kind radios, "Ends at") at the widths and
// themes of visualMatrix. The same checks as TestBookmarkVisual: no overflow, targets of 44 px, labelled fields,
// one h1, no skipped heading, no inline style.
func TestClipVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	old := bookmarkNow
	bookmarkNow = func() time.Time { return time.Date(2026, time.November, 5, 12, 0, 0, 0, time.Local) }
	t.Cleanup(func() { bookmarkNow = old })
	app := newTestApp(t)
	app.owner(t)
	bookID, _ := bookmarkVisualSeed(t, app)
	var owner string
	if err := app.st.Reader().QueryRow(`SELECT id FROM users WHERE role = 'owner'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	svc := progress.New(app.st, nil)
	list, err := svc.Bookmarks(context.Background(), owner, bookID, false) // newest first
	if err != nil || len(list) != 6 {
		t.Fatalf("seeded bookmarks = %d, %v", len(list), err)
	}
	clipOf := func(i int, lengthMs int64, kind string) string {
		end, k := list[i].PositionMs+lengthMs, kind
		if _, err := svc.UpdateBookmark(context.Background(), owner, list[i].ID, progress.BookmarkPatch{EndMs: &end, Kind: &k}); err != nil {
			t.Fatal(err)
		}
		return list[i].ID
	}
	openID := clipOf(0, 45_000, "quote")
	clipOf(2, 90_000, "favourite")
	k := "revisit"
	if _, err := svc.UpdateBookmark(context.Background(), owner, list[3].ID, progress.BookmarkPatch{Kind: &k}); err != nil {
		t.Fatal(err)
	}
	base := themedServer(t, app.signedIn(t))

	type page struct{ name, path, wait string }
	tabPath := "/books/" + bookID + "/bookmarks"
	main := []page{{"tab", tabPath, ""}}
	rest := []page{
		{"only", tabPath + "?only=clips", ""},
		{"sheet", tabPath + "?sheet=bookmark&bookmark=" + openID, ".ts-bookmark-sheet"},
	}
	for i, group := range [][]page{main, rest} {
		for _, p := range group {
			for _, sh := range visualMatrix(i == 0) {
				p, sh := p, sh
				t.Run(fmt.Sprintf("%s-%s-%d", p.name, sh.Theme, sh.Width), func(t *testing.T) {
					tab := b.NewTab(sh.Width, 900, sh.Width == 390, false)
					sep := "?"
					if strings.Contains(p.path, "?") {
						sep = "&"
					}
					tab.Navigate(base + p.path + sep + "theme=" + sh.Theme)
					if p.wait != "" {
						tab.WaitFor(`Boolean(document.querySelector("`+p.wait+`"))`, 5*time.Second)
						time.Sleep(450 * time.Millisecond)
					}
					var m bookProbeResult
					tab.Eval(bookProbeJS, &m)
					if m.Over {
						t.Errorf("horizontal overflow")
					}
					if m.Theme != sh.Theme {
						t.Errorf("data-theme = %q, want %q", m.Theme, sh.Theme)
					}
					if len(m.Small) > 0 {
						t.Errorf("targets under 44 px: %v", m.Small)
					}
					if len(m.Unlabelled) > 0 {
						t.Errorf("inputs without a label: %v", m.Unlabelled)
					}
					if m.H1 != 1 || m.Skip || m.Styled > 0 {
						t.Errorf("h1 %d, skipped heading %v, styled %d", m.H1, m.Skip, m.Styled)
					}
					tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("clips-%s-%s-%d.png", p.name, sh.Theme, sh.Width)))
				})
			}
		}
	}
}
