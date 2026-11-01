//go:build dev

package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/testutil/webdriver"
)

// defaultMiniBrowser is the WebKitGTK 4.1 MiniBrowser that WebKitWebDriver (4.1) launches.
const defaultMiniBrowser = "/usr/lib/x86_64-linux-gnu/webkit2gtk-4.1/MiniBrowser"

// TestVisualWebKitKitchenSink runs the kitchen sink in WebKitGTK. Opt-in: set
// TENDSHELF_TEST_WEBKIT_DRIVER=/usr/bin/WebKitWebDriver (and optionally TENDSHELF_TEST_WEBKIT_BROWSER).
// MiniBrowser has no headless mode, so run it as `xvfb-run -a make visual-webkit` to keep the window
// off the desktop. The window cannot be narrower than 447 CSS px (a GTK minimum), so the phone
// layout is checked at 447, not 390. Reduced motion and layout shift cannot be read here; see the
// manual checks in docs/roadmap.md.
func TestVisualWebKitKitchenSink(t *testing.T) {
	driver := os.Getenv("TENDSHELF_TEST_WEBKIT_DRIVER")
	if driver == "" {
		t.Skip("TENDSHELF_TEST_WEBKIT_DRIVER not set")
	}
	browser := os.Getenv("TENDSHELF_TEST_WEBKIT_BROWSER")
	if browser == "" {
		browser = defaultMiniBrowser
	}
	base := appServer(t)
	out := visualDir(t, "webkit")
	s := webdriver.Start(t, driver, browser)

	var support struct {
		ViewTransitionName bool   `json:"viewTransitionName"`
		StartingStyle      string `json:"startingStyle"`
		UserAgent          string `json:"userAgent"`
	}
	s.Navigate(base + "/dev/kitchen-sink")
	s.Eval(`({viewTransitionName: CSS.supports("view-transition-name: none"),
		startingStyle: typeof CSSStartingStyleRule, userAgent: navigator.userAgent})`, &support)
	b, _ := json.MarshalIndent(support, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "support.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Logf("WebKit support: %s", b)

	for _, w := range []int{447, 1440} {
		s.SetViewport(w, 900)
		for _, theme := range themes {
			t.Run(fmt.Sprintf("%s-%d", theme, w), func(t *testing.T) {
				s.Navigate(base + "/dev/kitchen-sink?theme=" + theme)
				var m struct {
					Over   bool    `json:"over"`
					Height float64 `json:"height"`
					Inner  float64 `json:"inner"`
				}
				s.Eval(`({over: document.documentElement.scrollWidth > window.innerWidth,
					height: document.documentElement.scrollHeight, inner: window.innerHeight})`, &m)
				if m.Over {
					t.Errorf("horizontal overflow at %d px in theme %s", w, theme)
				}
				// WebDriver screenshots the viewport: scroll through the page, at most eight shots.
				for i := 0; i < 8 && float64(i)*m.Inner < m.Height; i++ {
					s.Exec(fmt.Sprintf("window.scrollTo(0, %d);", i*int(m.Inner)), nil)
					s.Screenshot(filepath.Join(out, fmt.Sprintf("kitchen-%s-%d-%d.png", theme, w, i+1)))
				}
				s.Exec("window.scrollTo(0, 0);", nil)
			})
		}
		t.Run(fmt.Sprintf("swap-%d", w), func(t *testing.T) {
			s.Navigate(base + "/dev/kitchen-sink")
			var res struct {
				Path   string               `json:"path"`
				Before map[string][]float64 `json:"before"`
				After  map[string][]float64 `json:"after"`
			}
			s.ExecAsync(`const done = arguments[arguments.length - 1];
				const rects = () => { const out = {};
					for (const id of ["nav-tabbar", "nav-rail", "nav-side", "player", "audio"]) {
						const el = document.getElementById(id);
						const r = el ? el.getBoundingClientRect() : null;
						out[id] = r ? [r.x, r.y, r.width, r.height] : null;
					}
					return out; };
				const before = rects();
				document.addEventListener("htmx:after:settle", () => setTimeout(() => done({path: location.pathname, before: before, after: rects()}), 500), {once: true});
				setTimeout(() => done({path: "timeout", before: before, after: rects()}), 8000);
				document.querySelector("[data-probe-nav]").click();`, &res)
			if res.Path != "/dev/kitchen-sink/b" {
				t.Fatalf("path after the swap = %q", res.Path)
			}
			if fmt.Sprint(res.Before) != fmt.Sprint(res.After) {
				t.Errorf("shell rects moved on swap:\nbefore %v\nafter  %v", res.Before, res.After)
			}
		})
	}
	t.Logf("screenshots in %s", out)
}
