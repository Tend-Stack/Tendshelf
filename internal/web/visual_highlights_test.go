//go:build dev

package web

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestHighlightsVisual captures /me/highlights (everything, and a search with no match) at the matrix of
// visualMatrix and asserts no sideways scroll, one h1, no target under 44 px and the colour written by name.
func TestHighlightsVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	l := rdrSeed(t)
	long := strings.Repeat("The remarkably patient sentence went on and on without a single full stop in sight, ", 4)
	for i, c := range []string{"yellow", "green", "blue", "pink"} {
		note := ""
		if i%2 == 0 {
			note = "A note that says why this line matters to me, " + c + "."
		}
		quote := "A short line in " + c + "."
		if i == 3 {
			quote = long
		}
		ahlAddFor(t, l, l.ownerID, fmt.Sprintf("hl_v%d", i), quote, note, c, int64(1_700_000_000_000+i*86_400_000))
	}
	base := themedServer(t, l.a.signedIn(t))
	for _, view := range []struct{ name, path string }{
		{"all", "/me/highlights"},
		{"none", "/me/highlights?q=zzzz"},
	} {
		for _, sh := range visualMatrix(false) {
			t.Run(fmt.Sprintf("%s-%s-%d", view.name, sh.Theme, sh.Width), func(t *testing.T) {
				cur, err := l.a.srv.users.LoadSettings(t.Context(), l.ownerID)
				if err != nil {
					t.Fatal(err)
				}
				cur.Theme = sh.Theme
				if err := l.a.srv.users.SaveSettings(t.Context(), l.ownerID, cur); err != nil {
					t.Fatal(err)
				}
				tab := b.NewTab(sh.Width, 900, sh.Width == 390, false)
				tab.Navigate(base + view.path + sepFor(view.path) + "theme=" + sh.Theme)
				tab.WaitFor(`document.querySelector(".ts-hlist") !== null`, 5*time.Second)
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
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("highlights-%s-%s-%d.png", view.name, sh.Theme, sh.Width)))
			})
		}
	}
}
