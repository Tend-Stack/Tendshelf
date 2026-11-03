//go:build dev

package web

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

// prefsServerPage is one server page the window shows in its pane.
type prefsServerPage struct{ name, path string }

// TestVisualPrefsServer captures the server categories (Users, one person, Libraries, Add a folder
// with the folder browser sheet open, the lookups list and Email) inside the preferences window at
// 390, 768, 1024, 1440 and 1920 px. On every capture #main must not scroll sideways, and from 1024 px
// the category list stands beside the pane. Needs TENDSHELF_TEST_BROWSER.
func TestVisualPrefsServer(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	app := newTestApp(t, compareMod(t, &compareNet{}))
	rosa := usersBoardSeed(t, app)
	adminSeed(t, app)
	ctx := context.Background()
	var libID string
	if err := app.st.Reader().QueryRow(`SELECT id FROM libraries WHERE name = 'Audiobooks'`).Scan(&libID); err != nil {
		t.Fatal(err)
	}
	for i, title := range []string{"Slow Rivers", "Paper Boats"} {
		var id string
		if err := app.st.Reader().QueryRow(`SELECT id FROM books WHERE title = ?`, title).Scan(&id); err != nil {
			t.Fatal(err)
		}
		outcome := []string{library.LookupNeedsLook, library.LookupNoMatch}[i]
		err := app.lib.Update(ctx, func(tx *library.Tx) error {
			return tx.PutBookLookup(id, outcome, "audnexus", "", 1_730_000_000_000)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	base := themedServer(t, app.signedIn(t))
	root := fbTree(t)

	pgs := []prefsServerPage{
		{"users", "/admin/users"},
		{"user", "/admin/users/" + rosa},
		{"libraries", "/admin/libraries"},
		{"add-folder", addFolderURL + "?browse=" + url.QueryEscape(root)},
		{"lookups", "/admin/libraries/" + libID + "/lookups"},
		{"email", "/admin/email"},
	}
	for _, w := range []int{390, 768, 1024, 1440, 1920} {
		for _, pg := range pgs {
			t.Run(fmt.Sprintf("%s-%d", pg.name, w), func(t *testing.T) {
				tab := b.NewTab(w, 1000, w == 390, true)
				tab.Navigate(base + pg.path + prefsServerSep(pg.path) + "theme=dark")
				var m prefsLayout
				tab.Eval(prefsLayoutJS, &m)
				if m.Over || m.Doc {
					t.Errorf("horizontal overflow (main %v, page %v)", m.Over, m.Doc)
				}
				if !m.Pane {
					t.Error("the pane is not on screen")
				}
				if w >= 1024 && (!m.Nav || m.Back) {
					t.Errorf("two columns: nav %v back %v", m.Nav, m.Back)
				}
				if w < 1024 && m.Nav {
					t.Error("one column: the category list shows beside the pane")
				}
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("prefs-server-%s-%d.png", pg.name, w)))
			})
		}
	}
	t.Logf("captures in %s", out)
}

func prefsServerSep(p string) string {
	for _, c := range p {
		if c == '?' {
			return "&"
		}
	}
	return "?"
}

// TestVisualPrefsServerPanel captures Users and Libraries at 1440 with the Now playing panel open:
// the pane gets about 450 px, so the container queries pick the phone layout inside it.
func TestVisualPrefsServerPanel(t *testing.T) {
	out := visualDir(t, "chromium")
	e := nowPanelEnv(t)
	tab := fullpOpen(t, e, 1440, 1000, true)
	fullpOpenPlayer(t, tab)
	tab.WaitFor(`document.querySelector("#now-panel[data-book-id] .ts-np__desc") !== null`, 5*time.Second)
	for _, pg := range []prefsServerPage{{"users", "/admin/users"}, {"libraries", "/admin/libraries"}, {"email", "/admin/email"}} {
		// Follow a link the way the page does, through htmx, so the panel stays open.
		tab.Eval(fmt.Sprintf(`(() => { const old = document.getElementById("go"); if (old) old.remove(); const a = document.createElement("a"); a.id = "go"; a.href = %q; a.textContent = "go"; document.body.append(a); htmx.process(a); a.click(); })()`, pg.path), nil)
		tab.WaitFor(fmt.Sprintf(`location.pathname === %q && document.querySelector(".ts-prefs__pane h1") !== null`, pg.path), 8*time.Second)
		time.Sleep(400 * time.Millisecond)
		var m prefsLayout
		tab.Eval(prefsLayoutJS, &m)
		if m.Over {
			t.Errorf("%s: horizontal overflow beside the open panel", pg.name)
		}
		tab.Screenshot(filepath.Join(out, fmt.Sprintf("prefs-server-%s-panel-1440.png", pg.name)))
	}
}
