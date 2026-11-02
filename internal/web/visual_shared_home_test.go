//go:build dev

package web

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

// asServer serves the app to a browser as the person who holds the cookie, with the ?theme= switch of
// themedServer. The cookie is read per request, so a test can change who is looking.
func asServer(t *testing.T, a *testApp, who func() *http.Cookie) string {
	t.Helper()
	return themedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ck := who()
		r.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
		a.h.ServeHTTP(w, r)
	}))
}

// sharedHomeSeed gives the owner the Home the boards show (a hero, two series) and a shelf of three
// books that the admin Daniel shares, so "Your series in progress" and "Shared with you" sit together.
func sharedHomeSeed(t *testing.T) (*testApp, string, *http.Cookie) {
	t.Helper()
	a := newTestApp(t)
	a.owner(t)
	f := browseSeed(t, a, 6)
	homeVisualSeed(t, a, f)
	daniel := a.as(t, users.RoleAdmin)
	streamExec(t, a, `UPDATE users SET display_name = 'Daniel' WHERE id = ?`, sweepUserID(t, a, daniel))
	rows, err := a.st.Reader().Query(`SELECT id FROM books ORDER BY title DESC LIMIT 3`)
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	_ = rows.Close()
	if len(ids) != 3 {
		t.Fatalf("seed gave %d books for the shelf, want 3", len(ids))
	}
	shelf := shelfMake(t, a, daniel, "Daniel picks")
	for _, id := range ids {
		shelfAdd(t, a, daniel, shelf, id)
	}
	if rec := shelfPost(t, a, daniel, "/shelves/"+shelf+"/share", url.Values{"shared": {"1"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("share: %d", rec.Code)
	}
	return a, shelf, daniel
}

// TestVisualHomeShared captures Home with "Shared with you" beside "Your series in progress" (the
// Home boards) and Rosa's Home, a guest who was given one shelf (RosaHome), in every theme and width
// of the matrix. It fails on overflow, small targets or a missing row.
func TestVisualHomeShared(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	for _, bd := range []struct{ name, dir string }{
		{"HomePhone", "core"}, {"HomeTablet", "core"}, {"HomeDesktop", "core"}, {"HomePhoneLight", "core"}, {"HomeDesktopLight", "core"}, {"RosaHome", "people"},
	} {
		if src, err := os.Open(filepath.Join("..", "..", "docs", "design", "tendshelf-handoff", "screens", bd.dir, bd.name+".png")); err == nil {
			if dst, err := os.Create(filepath.Join(out, "shared-board-"+bd.name+".png")); err == nil {
				_, _ = io.Copy(dst, src)
				_ = dst.Close()
			}
			_ = src.Close()
		}
	}

	member, memberShelf, danielCk := sharedHomeSeed(t)
	memberBase := themedServer(t, member.signedIn(t))
	danielBase := asServer(t, member, func() *http.Cookie { return danielCk })

	// Rosa: a guest whose only book is on the shelf Daniel gave her. Nothing else is in her library.
	guest := newTestApp(t)
	guest.owner(t)
	browseSeed(t, guest, 3)
	daniel := guest.as(t, users.RoleAdmin)
	rosa := guest.as(t, users.RoleGuest)
	streamExec(t, guest, `UPDATE users SET display_name = 'Daniel' WHERE id = ?`, sweepUserID(t, guest, daniel))
	var bookID string
	if err := guest.st.Reader().QueryRow(`SELECT id FROM books ORDER BY title LIMIT 1`).Scan(&bookID); err != nil {
		t.Fatal(err)
	}
	shelf := shelfMake(t, guest, daniel, "For Rosa")
	shelfAdd(t, guest, daniel, shelf, bookID)
	streamExec(t, guest, `INSERT INTO grants(id, user_id, scope, shelf_id, created_by, created_at, updated_at) VALUES ('gs1', ?, 'shelf', ?, ?, 1, 1)`,
		sweepUserID(t, guest, rosa), shelf, sweepUserID(t, guest, daniel))
	guestBase := asServer(t, guest, func() *http.Cookie { return rosa })

	for _, pg := range []struct{ name, base string }{{"home-shared", memberBase}, {"home-guest", guestBase}} {
		for _, sh := range visualMatrix(true) {
			theme, w := sh.Theme, sh.Width
			t.Run(fmt.Sprintf("%s-%s-%d", pg.name, theme, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w == 390, false)
				tab.Navigate(pg.base + "/?theme=" + theme)
				var m browseProbe
				tab.Eval(browseProbeJS, &m)
				if m.Over {
					t.Errorf("horizontal overflow")
				}
				if len(m.Small) > 0 {
					t.Errorf("targets under 44 px: %v", m.Small)
				}
				if m.H1 != 1 {
					t.Errorf("%d h1 elements", m.H1)
				}
				var rows int
				tab.Eval(`document.querySelectorAll("#row-shared .ts-card").length`, &rows)
				if rows == 0 {
					t.Errorf("no Shared with you cards")
				}
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("shared-%s-%s-%d.png", pg.name, theme, w)))
			})
		}
	}

	// The shelves list with its "Shared with you" section, the shelf read-only for the viewer, and the
	// same shelf for the sharer, with the "Stop sharing" button.
	for _, pg := range []struct{ name, base, path string }{
		{"shelves-shared", memberBase, "/shelves"},
		{"shelf-readonly", memberBase, "/shelves/" + memberShelf},
		{"shelf-owner", danielBase, "/shelves/" + memberShelf},
	} {
		for _, sh := range visualMatrix(false) {
			theme, w := sh.Theme, sh.Width
			t.Run(fmt.Sprintf("%s-%s-%d", pg.name, theme, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w == 390, false)
				tab.Navigate(pg.base + pg.path + "?theme=" + theme)
				var m browseProbe
				tab.Eval(browseProbeJS, &m)
				if m.Over {
					t.Errorf("horizontal overflow")
				}
				if len(m.Small) > 0 {
					t.Errorf("targets under 44 px: %v", m.Small)
				}
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("shared-%s-%s-%d.png", pg.name, theme, w)))
			})
		}
	}
	t.Logf("screenshots in %s", out)
}
