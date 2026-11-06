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
// that the root scrollbar starts below the 44 px title bar: in the right-hand lane every pixel above the bar's
// bottom is the bar's colour, and the thumb's first pixel is at or below it. Opt-in like the other WebKit tests:
// scripts/isolated-display.sh with TENDSHELF_TEST_WEBKIT_DRIVER=/usr/bin/WebKitWebDriver. Shots go to
// .local/tests/desktop-bar/.
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
			// Make the page long enough to scroll (CSSOM, not a style attribute).
			s.Exec(`document.getElementById("main").style.minHeight = "3000px";`, nil)
			var m struct {
				Bar, Gutter, Host float64
			}
			s.Eval(`({bar: document.querySelector("[data-wbar]").getBoundingClientRect().bottom,
				gutter: innerWidth - document.documentElement.clientWidth,
				host: document.getElementById("desktop-bar").getBoundingClientRect().width})`, &m)
			t.Logf("bar host width %.0f", m.Host)
			if m.Gutter < 1 {
				t.Fatalf("no scrollbar lane (gutter %.0f px)", m.Gutter)
			}
			var dim struct{ H, Inner, Client float64 }
			s.Eval(`({h: document.documentElement.scrollHeight, inner: innerHeight, client: document.documentElement.clientWidth})`, &dim)
			t.Logf("scrollHeight %.0f, innerHeight %.0f, clientWidth %.0f, gutter %.0f", dim.H, dim.Inner, dim.Client, m.Gutter)
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
				x = img.Bounds().Dx() - int(m.Gutter)/2 - 1
				bg = img.At(x, 2)
				// The bar's last row is its hairline: the lane above it must be the bar's colour, and the thumb
				// must start below it.
				first = -1
				for y := 0; y < int(m.Bar)-1; y++ {
					if img.At(x, y) != bg {
						t.Errorf("lane pixel at y=%d is not the bar's colour", y)
					}
				}
				for y := int(m.Bar); y < img.Bounds().Dy(); y++ {
					if img.At(x, y) != bg {
						first = y
						break
					}
				}
			}
			if img.At(x, int(m.Bar)-1) != img.At(500, int(m.Bar)-1) {
				t.Errorf("the bar's hairline does not run across the scrollbar lane")
			}
			t.Logf("bar bottom %.0f, lane x=%d, thumb starts at y=%d", m.Bar, x, first)
			if first < int(m.Bar) {
				t.Errorf("the scrollbar thumb starts at y=%d, above the bar's bottom %.0f", first, m.Bar)
			}
		})
	}
}
