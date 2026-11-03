//go:build dev

package web

import (
	"fmt"
	"testing"
	"time"
)

// TestNavSteadyBrowser: the sidebar, its rail mode and the tablet rail sit in the same place on every page,
// whatever stylesheets the page loads (operator, 2026-11-03: the items moved 12 px between Home and
// Users & invites, because the setup screens' .ts-brand rule reached the nav's brand).
func TestNavSteadyBrowser(t *testing.T) {
	e := playerApp(t)
	b := chromium(t)
	for _, c := range []struct {
		w    int
		rail bool
		nav  string
	}{{1440, false, "#nav-side"}, {1440, true, "#nav-side"}, {1024, false, "#nav-rail"}} {
		t.Run(fmt.Sprintf("%d-rail-%v", c.w, c.rail), func(t *testing.T) {
			tab := playerOpen(t, b, e.URL, c.w, 900)
			if c.rail {
				playerClickSel(t, tab, "#nav-toggle")
				tab.WaitFor(`document.getElementById("nav-side").dataset.nav === "rail"`, 5*time.Second)
			}
			var home []float64
			for _, path := range []string{"/", "/libraries", "/admin/users", "/admin/libraries", "/admin/email", "/me", "/me/settings", "/search", "/shelves"} {
				tab.Navigate(e.URL + path)
				tab.WaitFor(`document.readyState === "complete"`, 5*time.Second)
				var tops []float64
				tab.Eval(`[...document.querySelectorAll("`+c.nav+` a, `+c.nav+` button, `+c.nav+` .ts-brand")].filter((x) => x.offsetParent).map((x) => Math.round(x.getBoundingClientRect().top * 10) / 10)`, &tops)
				if home == nil {
					home = tops
					continue
				}
				if len(tops) != len(home) {
					t.Errorf("%s: %d nav items, Home has %d", path, len(tops), len(home))
					continue
				}
				for i := range tops {
					if tops[i] != home[i] {
						t.Errorf("%s: nav item %d at %.1f px, on Home at %.1f px", path, i, tops[i], home[i])
						break
					}
				}
			}
		})
	}
}
