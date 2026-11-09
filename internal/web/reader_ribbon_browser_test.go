//go:build dev

package web

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The page edges, the ribbon and the first-use hint (decision 0036). Needs TENDSHELF_TEST_BROWSER.

// prTouchDevice makes the window answer as a phone does: a coarse pointer that cannot hover.
func prTouchDevice(t *testing.T, tab *cdp.Tab) {
	t.Helper()
	if err := tab.Call("Emulation.setTouchEmulationEnabled", map[string]any{"enabled": true, "maxTouchPoints": 1}, nil); err != nil {
		t.Fatal(err)
	}
}

// prMouse sends one mouse event at (x, y).
func prMouse(t *testing.T, tab *cdp.Tab, typ string, x, y float64) {
	t.Helper()
	p := map[string]any{"type": typ, "x": x, "y": y, "button": "none", "pointerType": "mouse"}
	if typ == "mousePressed" || typ == "mouseReleased" {
		p["button"], p["clickCount"] = "left", 1
	}
	if err := tab.Call("Input.dispatchMouseEvent", p, nil); err != nil {
		t.Fatal(err)
	}
}

// prDragTo moves the mouse with the left button held.
func prDragTo(t *testing.T, tab *cdp.Tab, x, y float64) {
	t.Helper()
	p := map[string]any{"type": "mouseMoved", "x": x, "y": y, "button": "left", "buttons": 1, "pointerType": "mouse"}
	if err := tab.Call("Input.dispatchMouseEvent", p, nil); err != nil {
		t.Fatal(err)
	}
}

// prClick presses and releases the mouse at (x, y).
func prClick(t *testing.T, tab *cdp.Tab, x, y float64) {
	t.Helper()
	prMouse(t, tab, "mouseMoved", x, y)
	prMouse(t, tab, "mousePressed", x, y)
	prMouse(t, tab, "mouseReleased", x, y)
}

// prCentre is the middle of the first element matching sel, as the window sees it.
func prCentre(tab *cdp.Tab, sel string) (x, y float64) {
	var c [2]float64
	tab.Eval(fmt.Sprintf(`(() => { const r = document.querySelector(%q).getBoundingClientRect(); return [r.left + r.width / 2, r.top + r.height / 2]; })()`, sel), &c)
	return c[0], c[1]
}

// prRibbon reads the state of the ribbons that are shown: pressed flags in page order.
func prRibbons(tab *cdp.Tab) (pressed []bool) {
	tab.Eval(`[...document.querySelectorAll(".ts-ribbon:not([hidden])")].map(b => b.getAttribute("aria-pressed") === "true")`, &pressed)
	return pressed
}

// TestPageRibbonCapture takes the evidence pictures when TENDSHELF_RIBBON_OUT names a folder.
func TestPageRibbonCapture(t *testing.T) {
	out := os.Getenv("TENDSHELF_RIBBON_OUT")
	if out == "" {
		t.Skip("TENDSHELF_RIBBON_OUT not set")
	}
	b := chromium(t)
	l := rpBook(t, 3, 60)
	pgFlow(t, l, "pages")
	srv := brServe(t, l, l.owner)
	shot := func(name string, w, h int, theme, query string, prepare func(tab *cdp.Tab)) {
		t.Run(name, func(t *testing.T) {
			tab := b.NewTab(w, h, w == 390, true)
			if w == 390 {
				prTouchDevice(t, tab)
			}
			tab.Navigate(srv + l.readPath(query+"&theme="+theme))
			tab.WaitFor(`document.querySelector("#reader.ts-reader--pages") !== null && document.fonts.status === "loaded"`, 10*time.Second)
			time.Sleep(400 * time.Millisecond)
			if prepare != nil {
				prepare(tab)
				time.Sleep(400 * time.Millisecond)
			}
			tab.Screenshot(filepath.Join(out, name+".png"))
		})
	}
	press := func(page int) func(tab *cdp.Tab) {
		return func(tab *cdp.Tab) {
			if got := prRibbons(tab); len(got) > page && got[page] {
				return
			}
			x, y := prCentre(tab, fmt.Sprintf(`.ts-ribbon[data-page="%d"]`, page))
			prClick(t, tab, x, y)
			tab.WaitFor(`document.querySelector(".ts-ribbon[aria-pressed='true']") !== null`, 5*time.Second)
			prMouse(t, tab, "mouseMoved", 300, 500)
		}
	}
	hover := func(sel string) func(tab *cdp.Tab) {
		return func(tab *cdp.Tab) {
			x, y := prCentre(tab, sel)
			prMouse(t, tab, "mouseMoved", x, y)
		}
	}
	both := func(sel string) func(tab *cdp.Tab) {
		return func(tab *cdp.Tab) {
			press(0)(tab)
			hover(sel)(tab)
		}
	}
	_ = both
	shot("hint-390-dark", 390, 844, "dark", "ch=3", nil) // first: the hint shows once per device
	shot("edge-hover-2000-dark", 2000, 1100, "dark", "ch=3", hover(".ts-edge--next"))
	shot("edge-hover-prev-2000-dark", 2000, 1100, "dark", "ch=3&at=", hover(".ts-edge--prev"))
	shot("ribbon-unpressed-hover-2000-dark", 2000, 1100, "dark", "ch=3", hover(`.ts-ribbon[data-page="1"]`))
	shot("ribbon-unpressed-2000-dark", 2000, 1100, "dark", "ch=3", nil)
	shot("ribbon-pressed-left-2000-dark", 2000, 1100, "dark", "ch=3", press(0))
	shot("ribbon-pressed-right-2000-dark", 2000, 1100, "dark", "ch=3", press(1))
	shot("ribbon-pressed-390-dark", 390, 844, "dark", "ch=4", func(tab *cdp.Tab) {
		tab.Eval(`localStorage.setItem("ts.reader.hinted", "1")`, nil)
		x, y := prCentre(tab, `.ts-ribbon[data-page="0"]`)
		pgTouch(t, tab, x, y, x, y)
		tab.WaitFor(`document.querySelector(".ts-ribbon[aria-pressed='true']") !== null`, 5*time.Second)
	})
	for _, th := range []string{"light", "sepia", "contrast"} {
		shot("ribbon-pressed-1440-"+th, 1440, 900, th, "ch=5", func(tab *cdp.Tab) {
			press(0)(tab)
			x, y := prCentre(tab, ".ts-edge--next")
			prMouse(t, tab, "mouseMoved", x, y)
		})
	}
}
