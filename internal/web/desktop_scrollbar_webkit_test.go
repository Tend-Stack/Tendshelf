//go:build dev

package web

import (
	"image"
	"image/color"
	_ "image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/testutil/webdriver"
)

// TestDeskBarScrollbarWebKit runs the desktop shell's page in WebKitGTK (the desktop's real engine) and checks
// that #main is the scroller (decision 0011 addendum, 2026-11-15): the document has no scrollbar and does not
// scroll, #main has its own 14 px lane that starts under the 44 px title bar, and the thumb is painted in it.
// Opt-in like the other WebKit tests: scripts/isolated-display.sh with
// TENDSHELF_TEST_WEBKIT_DRIVER=/usr/bin/WebKitWebDriver. Shots go to .local/tests/desktop-bar/.
func TestDeskBarScrollbarWebKit(t *testing.T) {
	driver := os.Getenv("TENDSHELF_TEST_WEBKIT_DRIVER")
	if driver == "" {
		t.Skip("TENDSHELF_TEST_WEBKIT_DRIVER not set")
	}
	browser := os.Getenv("TENDSHELF_TEST_WEBKIT_BROWSER")
	if browser == "" {
		browser = defaultMiniBrowser
	}
	app := newTestApp(t)
	app.owner(t)
	base := themedServer(t, app.signedIn(t))
	out := filepath.Join("..", "..", ".local", "tests", "desktop-bar")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, theme := range []string{"dark", "light", "sepia", "contrast"} {
		t.Run(theme, func(t *testing.T) {
			// One fresh browser per theme, so no state carries over between themes.
			s := webdriver.Start(t, driver, browser)
			s.SetViewport(1024, 800)
			s.Navigate(base + "/?shell=desktop")
			s.Navigate(base + "/?theme=" + theme)
			// Make the page long enough to scroll (a constructed stylesheet, not a style attribute).
			s.Exec(`const sheet = new CSSStyleSheet(); sheet.replaceSync('#main::after { content: ""; display: block; height: 3000px; }'); document.adoptedStyleSheets = [sheet];`, nil)
			var m struct {
				Bar, Gutter, Right, RootGutter, RootScroll, RootRange float64
			}
			s.Eval(`({bar: document.querySelector("[data-wbar]").getBoundingClientRect().bottom,
				gutter: document.getElementById("main").offsetWidth - document.getElementById("main").clientWidth,
				right: document.getElementById("main").getBoundingClientRect().right,
				rootGutter: innerWidth - document.documentElement.clientWidth,
				rootScroll: document.scrollingElement.scrollTop,
				rootRange: document.scrollingElement.scrollHeight - document.scrollingElement.clientHeight})`, &m)
			if m.RootGutter != 0 || m.RootRange != 0 || m.RootScroll != 0 {
				t.Errorf("the document must not scroll: gutter %.0f, range %.0f, top %.0f", m.RootGutter, m.RootRange, m.RootScroll)
			}
			if m.Gutter < 1 {
				t.Fatalf("#main has no scrollbar lane (gutter %.0f px)", m.Gutter)
			}
			shot := filepath.Join(out, "scrollbar-webkit-"+theme+".png")
			var img image.Image
			var x, first int
			var bg color.Color
			// A shot taken before WebKit has painted the scrollbar shows no thumb at all; take it again then.
			for try := 0; try < 5 && first <= 0; try++ {
				s.ExecAsync(`const done = arguments[arguments.length - 1];
					requestAnimationFrame(() => requestAnimationFrame(() => setTimeout(done, 300)));`, nil)
				s.Screenshot(shot)
				f, err := os.Open(shot)
				if err != nil {
					t.Fatal(err)
				}
				img, _, err = image.Decode(f)
				f.Close()
				if err != nil {
					t.Fatal(err)
				}
				x = int(m.Right) - int(m.Gutter)/2 - 1
				bg = img.At(x, int(m.Bar)+1)
				first = -1
				for y := int(m.Bar); y < img.Bounds().Dy(); y++ {
					if img.At(x, y) != bg {
						first = y
						break
					}
				}
			}
			t.Logf("bar bottom %.0f, lane x=%d, thumb starts at y=%d", m.Bar, x, first)
			if first < int(m.Bar) {
				t.Errorf("no scrollbar thumb painted in #main's lane (first=%d)", first)
			}
		})
	}
}
