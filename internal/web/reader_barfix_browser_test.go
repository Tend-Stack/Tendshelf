//go:build dev

package web

import (
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// cfMouse sends one mouse event to the page.
func cfMouse(t *testing.T, tab *cdp.Tab, typ string, x, y float64) {
	t.Helper()
	p := map[string]any{"type": typ, "x": x, "y": y, "button": "none"}
	if typ != "mouseMoved" {
		p["button"], p["clickCount"] = "left", 1
	}
	if err := tab.Call("Input.dispatchMouseEvent", p, nil); err != nil {
		t.Fatal(err)
	}
}

// TestReaderBarKeepsAndReturns: a mouse click in the text never hides the bar; with hide-after-a-moment on,
// the hidden bar leaves no gap in the scroll flow, the words stay put, and the mouse at the top, a key or the
// handle bring it back; a finger in the middle still toggles it and the handle answers there too.
func TestReaderBarKeepsAndReturns(t *testing.T) {
	b := chromium(t)
	chrome := `document.querySelector(".ts-readerpage").dataset.chrome`
	handle := `(() => { const h = document.querySelector("[data-show-bar]"), r = h.getBoundingClientRect(); return {shown: getComputedStyle(h).display !== "none", w: r.width, h: r.height, label: h.getAttribute("aria-label")}; })()`
	type handleInfo struct {
		Shown bool
		W, H  float64
		Label string
	}

	t.Run("mouse-never-hides", func(t *testing.T) {
		e := ofrApp(t, cfBook(), nil)
		tab := cfOpen(t, b, e, 1440, 900, false)
		cfMouse(t, tab, "mousePressed", 720, 450)
		cfMouse(t, tab, "mouseReleased", 720, 450)
		time.Sleep(400 * time.Millisecond)
		var state string
		tab.Eval(`String(`+chrome+`)`, &state)
		if state == "off" {
			t.Error("a mouse click in the text hid the bar")
		}
	})

	t.Run("autohide-scroll-1440", func(t *testing.T) {
		e := ofrApp(t, cfBook(), nil)
		cfSet(t, e, "auto_hide", "1")
		tab := cfOpen(t, b, e, 1440, 900, false)
		tab.Eval(`document.getElementById("reader").scrollTop = 120`, nil)
		time.Sleep(300 * time.Millisecond)
		sentence := `document.querySelector(".ts-s").getBoundingClientRect().top`
		var barH, top0, top1, rootTop float64
		tab.Eval(`document.querySelector(".ts-readerbar").getBoundingClientRect().height`, &barH)
		tab.Eval(sentence, &top0)
		tab.WaitFor(chrome+` === "off"`, 8*time.Second)
		time.Sleep(300 * time.Millisecond)
		tab.Eval(sentence, &top1)
		tab.Eval(`document.getElementById("reader").getBoundingClientRect().top`, &rootTop)
		if rootTop > 1 {
			t.Errorf("the hidden bar still takes room: the text starts at %.0f px", rootTop)
		}
		if d := top1 - top0; d > 1 || d < -1 {
			t.Errorf("the words moved by %.1f px when the bar hid (bar %.0f px)", d, barH)
		}
		var h handleInfo
		tab.Eval(handle, &h)
		if !h.Shown || h.W < 44 || h.H < 44 || h.Label != "Show the bar" {
			t.Errorf("the handle: %+v", h)
		}
		if p := rfixShot("scroll-bar-hidden-desktop-1440.png"); p != "" {
			tab.Screenshot(p)
		}
		// The mouse at the top brings it back.
		cfMouse(t, tab, "mouseMoved", 700, 440)
		cfMouse(t, tab, "mouseMoved", 700, 10)
		tab.WaitFor(chrome+` === "on"`, 3*time.Second)
		tab.Eval(`document.activeElement.blur()`, nil)
		tab.WaitFor(chrome+` === "off"`, 8*time.Second)
		// A key (Tab) does too.
		pgKey(t, tab, "Tab", "Tab", 9, "", 0)
		tab.WaitFor(chrome+` === "on"`, 3*time.Second)
		tab.Eval(`document.activeElement.blur()`, nil)
		tab.WaitFor(chrome+` === "off"`, 8*time.Second)
		// The handle, by the keyboard route: focus it and press Enter.
		tab.Eval(`document.querySelector("[data-show-bar]").focus()`, nil)
		pgKey(t, tab, "Enter", "Enter", 13, "\r", 0)
		tab.WaitFor(chrome+` === "on"`, 3*time.Second)
	})

	t.Run("touch-toggles-390", func(t *testing.T) {
		e := ofrApp(t, cfBook(), nil)
		tab := cfOpen(t, b, e, 390, 844, true)
		pgTouch(t, tab, 195, 422, 195, 422)
		tab.WaitFor(chrome+` === "off"`, 3*time.Second)
		var h handleInfo
		tab.Eval(handle, &h)
		if !h.Shown || h.W < 44 || h.H < 44 {
			t.Errorf("the handle at 390: %+v", h)
		}
		if p := rfixShot("bar-hidden-handle-mobile-390.png"); p != "" {
			tab.Screenshot(p)
		}
		pgTouch(t, tab, 195, 22, 195, 22)
		tab.WaitFor(chrome+` === "on"`, 3*time.Second)
	})
}
