//go:build dev

package web

import (
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// Swipe down to minimise the full player with a finger (touch pointers), at 390 x 844, in Chromium
// (make player). Real Input.dispatchTouchEvent input, in steps spaced 30 ms apart so a drag is slow
// enough not to count as a flick.

// swipeTouch drags one finger from (x0,y0) to (x1,y1) in ten steps and lifts it.
func swipeTouch(t *testing.T, tab *cdp.Tab, x0, y0, x1, y1 float64) {
	t.Helper()
	send := func(typ string, x, y float64) {
		points := []map[string]any{{"x": x, "y": y}}
		if typ == "touchEnd" {
			points = []map[string]any{}
		}
		if err := tab.Call("Input.dispatchTouchEvent", map[string]any{"type": typ, "touchPoints": points}, nil); err != nil {
			t.Fatal(err)
		}
	}
	send("touchStart", x0, y0)
	for i := 1; i <= 10; i++ {
		f := float64(i) / 10
		time.Sleep(30 * time.Millisecond)
		send("touchMove", x0+(x1-x0)*f, y0+(y1-y0)*f)
	}
	send("touchEnd", x1, y1)
}

// swipeCentre is the middle of the first element matching sel.
func swipeCentre(tab *cdp.Tab, sel string) (x, y float64) {
	var p struct{ X, Y float64 }
	tab.Eval(`(() => { const r = document.querySelector(`+cdp.Quote(sel)+`).getBoundingClientRect(); return {X: r.x + r.width / 2, Y: r.y + r.height / 2}; })()`, &p)
	return p.X, p.Y
}

// swipeOpenPlaying opens the full player and starts playback.
func swipeOpenPlaying(t *testing.T, reduced bool) *cdp.Tab {
	t.Helper()
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 844, reduced)
	fullpOpenPlayer(t, tab)
	playerClickSel(t, tab, "#player-full [data-ts-full-toggle]")
	tab.WaitFor(`window.tsPlayer.state().playing`, 5*time.Second)
	return tab
}

// swipeSettled waits out the spring-back and reports whether the player is open and where it sits.
func swipeSettled(tab *cdp.Tab) (open bool, drag string) {
	time.Sleep(600 * time.Millisecond)
	tab.Eval(fullpDialog+`.open`, &open)
	tab.Eval(`getComputedStyle(`+fullpDialog+`).getPropertyValue("--ts-drag").trim()`, &drag)
	return open, drag
}

// swipeKeepsPlaying checks the position moved on since before and the audio was never paused.
func swipeKeepsPlaying(t *testing.T, tab *cdp.Tab, before int64) {
	t.Helper()
	time.Sleep(1200 * time.Millisecond)
	s := playerStateOf(tab)
	if !s.Playing || s.PositionMs <= before {
		t.Errorf("playback: playing %v, position %d ms (was %d); closing must not touch the audio", s.Playing, s.PositionMs, before)
	}
}

func TestPlayerSwipeClosesPastQuarter(t *testing.T) {
	tab := swipeOpenPlaying(t, false)
	before := playerStateOf(tab).PositionMs
	x, y := swipeCentre(tab, "#player-full .ts-player__title")
	swipeTouch(t, tab, x, y, x, y+0.4*844)
	tab.WaitFor(`!`+fullpDialog+`.open`, 3*time.Second)
	swipeKeepsPlaying(t, tab, before)
}

func TestPlayerSwipeShortSpringsBack(t *testing.T) {
	tab := swipeOpenPlaying(t, false)
	before := playerStateOf(tab).PositionMs
	x, y := swipeCentre(tab, "#player-full .ts-player__title")
	swipeTouch(t, tab, x, y, x, y+0.15*844)
	if open, drag := swipeSettled(tab); !open || (drag != "0px" && drag != "") {
		t.Errorf("a 15 %% drag: open %v, drag %q; it should spring back", open, drag)
	}
	swipeKeepsPlaying(t, tab, before)
}

func TestPlayerSwipeFollowsAndFades(t *testing.T) {
	tab := swipeOpenPlaying(t, false)
	x, y := swipeCentre(tab, "#player-full .ts-player__title")
	// Hold the finger down part way: the player has moved and faded.
	for _, p := range []struct {
		typ string
		y   float64
	}{{"touchStart", y}, {"touchMove", y + 20}, {"touchMove", y + 150}} {
		tab.Call("Input.dispatchTouchEvent", map[string]any{"type": p.typ, "touchPoints": []map[string]any{{"x": x, "y": p.y}}}, nil)
		time.Sleep(40 * time.Millisecond)
	}
	var s struct{ Moved, Opacity float64 }
	tab.Eval(`(() => { const d = `+fullpDialog+`; return {Moved: new DOMMatrix(getComputedStyle(d).transform).m42, Opacity: Number(getComputedStyle(d).opacity)}; })()`, &s)
	tab.Call("Input.dispatchTouchEvent", map[string]any{"type": "touchEnd", "touchPoints": []map[string]any{}}, nil)
	if s.Moved < 100 || s.Opacity >= 1 {
		t.Errorf("while dragging: moved %.0f px, opacity %.2f; it should follow the finger and fade", s.Moved, s.Opacity)
	}
}

func TestPlayerSwipeFlickCloses(t *testing.T) {
	tab := swipeOpenPlaying(t, false)
	x, y := swipeCentre(tab, "#player-full .ts-player__title")
	// 100 px in 40 ms is a flick although it is far short of a quarter of the screen. Each event carries its own time, so the
	// speed the page works out does not depend on how fast this machine delivers them (a loaded nightly runner on 2026-11-12
	// took long enough between them to turn the flick into a slow drag).
	began := float64(time.Now().UnixMilli()) / 1000
	send := func(typ string, y float64, afterMs int) {
		pts := []map[string]any{{"x": x, "y": y}}
		if typ == "touchEnd" {
			pts = []map[string]any{}
		}
		tab.Call("Input.dispatchTouchEvent", map[string]any{"type": typ, "touchPoints": pts, "timestamp": began + float64(afterMs)/1000}, nil)
	}
	send("touchStart", y, 0)
	for i := 1; i <= 4; i++ {
		time.Sleep(8 * time.Millisecond)
		send("touchMove", y+float64(i)*25, i*10)
	}
	send("touchEnd", y+100, 40)
	tab.WaitFor(`!`+fullpDialog+`.open`, 3*time.Second)
}

func TestPlayerSwipeFromProgressBarScrubs(t *testing.T) {
	tab := swipeOpenPlaying(t, false)
	x, y := swipeCentre(tab, "#player-full .ts-player__scrub .ts-ruler")
	swipeTouch(t, tab, x, y, x+80, y+0.4*844)
	if open, _ := swipeSettled(tab); !open {
		t.Error("a drag that starts on the progress bar closed the player")
	}
}

func TestPlayerSwipeFromButtonDoesNotClose(t *testing.T) {
	tab := swipeOpenPlaying(t, false)
	x, y := swipeCentre(tab, "#player-full [data-ts-open-speed]")
	swipeTouch(t, tab, x, y, x, y+0.4*844)
	if open, _ := swipeSettled(tab); !open {
		t.Error("a drag that starts on a button closed the player")
	}
}

func TestPlayerSwipeInChaptersSheetDoesNotClose(t *testing.T) {
	tab := swipeOpenPlaying(t, false)
	playerClickSel(t, tab, "[data-ts-open-chapters]")
	tab.WaitFor(`!document.querySelector("#player-full [data-ts-chapters-sheet]").hidden`, 2*time.Second)
	time.Sleep(400 * time.Millisecond)
	x, y := swipeCentre(tab, "#player-full [data-ts-chapter-list]")
	// Down then up again inside the list: it must neither close the player nor the sheet.
	swipeTouch(t, tab, x, y-40, x, y+20)
	var sheet bool
	if open, _ := swipeSettled(tab); !open {
		t.Error("a drag inside the open chapters sheet closed the player")
	}
	tab.Eval(`!document.querySelector("#player-full [data-ts-chapters-sheet]").hidden`, &sheet)
	if !sheet {
		t.Error("the chapters sheet closed by a drag in its list")
	}
}

func TestPlayerSwipeSidewaysDoesNothing(t *testing.T) {
	tab := swipeOpenPlaying(t, false)
	x, y := swipeCentre(tab, "#player-full .ts-player__title")
	swipeTouch(t, tab, x-100, y, x+100, y+60)
	if open, drag := swipeSettled(tab); !open || (drag != "0px" && drag != "") {
		t.Errorf("a sideways drag: open %v, drag %q", open, drag)
	}
}

func TestPlayerSwipeReducedMotionClosesWithoutTransform(t *testing.T) {
	tab := swipeOpenPlaying(t, true)
	before := playerStateOf(tab).PositionMs
	x, y := swipeCentre(tab, "#player-full .ts-player__title")
	// Watch every change of the transform while the finger is down.
	tab.Eval(`(() => { window.__moved = 0; const d = `+fullpDialog+`; new MutationObserver(() => { if (d.style.getPropertyValue("--ts-drag") !== "" && d.style.getPropertyValue("--ts-drag") !== "0px") window.__moved++; }).observe(d, {attributes: true, attributeFilter: ["style"]}); })()`, nil)
	swipeTouch(t, tab, x, y, x, y+0.4*844)
	tab.WaitFor(`!`+fullpDialog+`.open`, 3*time.Second)
	var moved int
	tab.Eval(`window.__moved`, &moved)
	if moved != 0 {
		t.Errorf("reduced motion set a drag transform %d times", moved)
	}
	swipeKeepsPlaying(t, tab, before)
}
