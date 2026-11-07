//go:build dev

package web

import (
	"fmt"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The scrubber is one chapter wide (decision 0005): a click or a drag anywhere on the line lands at
// that part of the current chapter, never on a chapter start the line used to snap to. The P1c
// fixture has six chapters of 30 s; chapter 3 runs from 60 s to 90 s. make player runs this.

const (
	chapscrubStart = 60_000
	chapscrubEnd   = 90_000
)

type chapscrubBox struct{ L, W, Y float64 }

// chapscrubMeasure measures the line the pointer works on: the range input of the scrubber at sel.
func chapscrubMeasure(tab *cdp.Tab, sel string) chapscrubBox {
	var box chapscrubBox
	tab.Eval(`(() => { const r = document.querySelector(`+cdp.Quote(sel)+`).getBoundingClientRect(); return {L: r.left, W: r.width, Y: r.top + r.height / 2}; })()`, &box)
	return box
}

// chapscrubInto parks the position inside chapter 3 and waits for it.
func chapscrubInto(t *testing.T, tab *cdp.Tab) {
	t.Helper()
	tab.Eval(`window.tsPlayer.seek(70000)`, nil)
	tab.WaitFor(`Math.abs(window.tsPlayer.state().positionMs - 70000) < 1000`, 2*time.Second)
	time.Sleep(150 * time.Millisecond)
}

// chapscrubWait waits until the position is within a second of want.
func chapscrubWait(t *testing.T, tab *cdp.Tab, what string, want int64) {
	t.Helper()
	tab.WaitFor(fmt.Sprintf(`Math.abs(window.tsPlayer.state().positionMs - %d) < 1000`, want), 2*time.Second)
	fullpNear(t, what, fullpPos(tab), want, 1000)
	if p := fullpPos(tab); p < chapscrubStart || p >= chapscrubEnd {
		t.Errorf("%s: %d ms left chapter 3", what, p)
	}
}

// chapscrubLine clicks at 25, 50 and 90 % of the line and drags the needle from 10 to 70 %.
func chapscrubLine(t *testing.T, tab *cdp.Tab, sel string) {
	t.Helper()
	chapscrubInto(t, tab)
	box := chapscrubMeasure(tab, sel)
	at := func(f float64) float64 { return box.L + f*box.W }
	for _, f := range []float64{0.25, 0.5, 0.9} {
		fullpMouse(t, tab, "mousePressed", at(f), box.Y)
		fullpMouse(t, tab, "mouseReleased", at(f), box.Y)
		chapscrubWait(t, tab, fmt.Sprintf("click at %.0f %%", f*100), chapscrubStart+int64(f*(chapscrubEnd-chapscrubStart)))
	}
	chapscrubInto(t, tab)
	fullpDrag(t, tab, at(0.1), box.Y, at(0.7), box.Y)
	chapscrubWait(t, tab, "drag from 10 % to 70 %", chapscrubStart+int64(0.7*(chapscrubEnd-chapscrubStart)))
}

func TestFullPlayerBrowserChapterScrub(t *testing.T) {
	e := playerApp(t)
	const full = `#player-full [data-ts-range]`

	// Phone: the full player's scrubber, keys, and the book ruler.
	phone := fullpOpen(t, e, 390, 844, false)
	fullpOpenPlayer(t, phone)
	chapscrubLine(t, phone, full)

	chapscrubInto(t, phone)
	phone.Eval(`document.querySelector("#player-full [data-ts-range]").focus()`, nil)
	fullpKey(t, phone, "Home", 36)
	chapscrubWait(t, phone, "Home", chapscrubStart)
	fullpKey(t, phone, "End", 35)
	phone.WaitFor(`window.tsPlayer.state().positionMs > 89000`, 2*time.Second)
	fullpNear(t, "End (the start of chapter 4)", fullpPos(phone), chapscrubEnd, 1000)
	var label string
	phone.Eval(`document.getElementById("ts-scrub-full").labels[0].textContent`, &label)
	if label != "Position in chapter" {
		t.Errorf("scrubber label %q", label)
	}

	// The book ruler is the chapter bar (chapter-bar.js): a slider over the six chapters with five ticks.
	var ruler struct {
		Hidden, Slider, Focusable bool
		Ticks, Max                int
	}
	phone.Eval(`(() => { const r = document.querySelector("#player-full [data-ts-ruler]");
	  return {Hidden: r.getAttribute("aria-hidden") === "true", Slider: r.getAttribute("role") === "slider", Focusable: r.tabIndex === 0,
	    Ticks: r.querySelectorAll(".ts-ruler__tick").length, Max: Number(r.getAttribute("aria-valuemax"))}; })()`, &ruler)
	if ruler.Hidden || !ruler.Slider || !ruler.Focusable || ruler.Ticks != 5 || ruler.Max != 6 {
		t.Errorf("book ruler: %+v", ruler)
	}

	// Desktop: the side panel shows only the book's chapter ruler (operator, 2026-11-02); the chapter
	// slider is the bar's compact one, which stays visible beside the open panel.
	wide := fullpOpen(t, e, 1440, 900, false)
	fullpOpenPlayer(t, wide)
	var hidden bool
	wide.Eval(`getComputedStyle(document.querySelector('#player-full [data-ts-scrub="full"]')).display === "none" &&
	  getComputedStyle(document.querySelector('#player-full [data-ts-ruler]')).display !== "none"`, &hidden)
	if !hidden {
		t.Error("desktop side panel: the chapter slider is shown or the book ruler is hidden")
	}
	chapscrubLine(t, wide, `#player .ts-wave--bar .ts-wave__input`)
}
