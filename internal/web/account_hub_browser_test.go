//go:build dev

package web

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// accountHubProbeJS reports where the two Profile menus sit once opened: each body must lie inside the
// viewport, every entry must be at least 44 px tall, and Escape must close the open menu.
const accountHubProbeJS = `(() => {
  const out = {menus: [], escape: true, vw: document.documentElement.clientWidth};
  for (const id of ["pf-menu-account", "pf-menu-settings"]) {
    const d = document.getElementById(id);
    d.open = true;
    const body = d.querySelector(".ts-chipmenu__body").getBoundingClientRect();
    const items = [...d.querySelectorAll(".ts-chipmenu__item")].map((a) => a.getBoundingClientRect().height);
    out.menus.push({id, left: body.left, right: body.right, minItem: Math.min(...items)});
    document.dispatchEvent(new KeyboardEvent("keydown", {key: "Escape", bubbles: true}));
    if (d.open) out.escape = false;
  }
  return out;
})()`

type accountHubProbe struct {
	Escape bool    `json:"escape"`
	VW     float64 `json:"vw"`
	Menus  []struct {
		ID      string  `json:"id"`
		Left    float64 `json:"left"`
		Right   float64 `json:"right"`
		MinItem float64 `json:"minItem"`
	} `json:"menus"`
}

// TestAccountHubCapture captures Profile and Home at four widths in dark and light into
// .local/tests/account-hub/, and with each Profile menu open at 390 and 1440. It checks the open menus
// stay on screen, their entries are 44 px tall and Escape closes them.
func TestAccountHubCapture(t *testing.T) {
	b := chromium(t)
	out := filepath.Join("..", "..", ".local", "tests", "account-hub")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	app := newTestApp(t)
	app.owner(t)
	base := themedServer(t, app.signedIn(t))
	for _, theme := range []string{"dark", "light"} {
		for _, w := range []int{390, 768, 1024, 1440} {
			for _, page := range []struct{ name, path string }{{"profile", "/me"}, {"home", "/"}} {
				t.Run(fmt.Sprintf("%s-%s-%d", page.name, theme, w), func(t *testing.T) {
					tab := b.NewTab(w, 900, w == 390, false)
					tab.Navigate(base + page.path + "?theme=" + theme)
					var m browseProbe
					tab.Eval(browseProbeJS, &m)
					if m.Over {
						t.Errorf("horizontal overflow")
					}
					if len(m.Small) > 0 {
						t.Errorf("targets under 44 px: %v", m.Small)
					}
					tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("%s-%s-%d.png", page.name, theme, w)))
					if page.name != "profile" || (w != 390 && w != 1440) {
						return
					}
					var p accountHubProbe
					tab.Eval(accountHubProbeJS, &p)
					for _, mn := range p.Menus {
						if mn.Left < 0 || mn.Right > p.VW {
							t.Errorf("%s leaves the screen: %.0f to %.0f of %.0f", mn.ID, mn.Left, mn.Right, p.VW)
						}
						if mn.MinItem < 44 {
							t.Errorf("%s has an entry %.0f px tall", mn.ID, mn.MinItem)
						}
					}
					if !p.Escape {
						t.Errorf("Escape did not close a menu")
					}
					for _, id := range []string{"pf-menu-account", "pf-menu-settings"} {
						tab.Eval(`(() => { document.getElementById("`+id+`").open = true; return 1; })()`, new(int))
						tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("profile-%s-%s-%d.png", id, theme, w)))
						tab.Eval(`(() => { document.getElementById("`+id+`").open = false; return 1; })()`, new(int))
					}
				})
			}
		}
	}
}
