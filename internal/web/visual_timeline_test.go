//go:build dev

package web

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// TestTimelineVisual captures /me/listening/history (all books, and one book) at the matrix of
// visualMatrix and asserts the page has no sideways scroll, one h1 and no target under 44 px.
// The sessions are the listening dashboard's seeded ones, so the captures repeat from run to run.
func TestTimelineVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	w := listeningVisualApp(t)
	base := themedServer(t, w.a.signedIn(t))
	for _, view := range []struct{ name, path string }{
		{"all", "/me/listening/history"},
		{"book", "/me/listening/history?book=" + w.ownerBook},
	} {
		for _, sh := range visualMatrix(false) {
			t.Run(fmt.Sprintf("%s-%s-%d", view.name, sh.Theme, sh.Width), func(t *testing.T) {
				cur, err := w.a.srv.users.LoadSettings(t.Context(), w.ownerID)
				if err != nil {
					t.Fatal(err)
				}
				cur.Theme = sh.Theme
				if err := w.a.srv.users.SaveSettings(t.Context(), w.ownerID, cur); err != nil {
					t.Fatal(err)
				}
				tab := b.NewTab(sh.Width, 900, sh.Width == 390, false)
				if err := tab.Call("Emulation.setTimezoneOverride", map[string]any{"timezoneId": "UTC"}, nil); err != nil {
					t.Fatal(err)
				}
				tab.Navigate(base + view.path + sepFor(view.path) + "theme=" + sh.Theme)
				tab.WaitFor(`document.querySelector(".ts-tl__row") !== null || document.querySelector(".ts-tl") !== null`, 5*time.Second)
				tab.Eval(`document.fonts.ready.then(() => true)`, nil)
				time.Sleep(300 * time.Millisecond)
				var m browseProbe
				tab.Eval(browseProbeJS, &m)
				if m.Theme != sh.Theme {
					t.Fatalf("data-theme = %q, want %q", m.Theme, sh.Theme)
				}
				if m.Over {
					t.Errorf("horizontal overflow")
				}
				if m.H1 != 1 {
					t.Errorf("%d h1, want 1", m.H1)
				}
				for _, s := range m.Small {
					t.Errorf("target under 44 px: %s", s)
				}
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("timeline-%s-%s-%d.png", view.name, sh.Theme, sh.Width)))
			})
		}
	}
}
