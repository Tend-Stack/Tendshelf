//go:build dev

package web

import (
	"testing"
	"time"
)

// undoButton is the action on the "Jumped too far?" toast, in the full player's own toast region.
const undoButton = `#player-full [data-ts-full-toasts] .ts-toast__actions button`

// TestSmartRewindBrowserUndo: a chapter jump offers a way back, a small skip does not, and Go back
// returns to the place before the jump. The fixture is the full player's: six chapters of 30 s, at 100 s.
func TestSmartRewindBrowserUndo(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 844, false)
	fullpOpenPlayer(t, tab)
	tab.Eval(`document.querySelector("#player-full [data-ts-range]").focus()`, nil)

	// A skip of 30 s is no jump.
	fullpKey(t, tab, "ArrowRight", 39)
	tab.WaitFor(`window.tsPlayer.state().positionMs > 125000`, 2*time.Second)
	var n int
	tab.Eval(`document.querySelectorAll("#player-full [data-ts-full-toasts] .ts-toast__actions").length`, &n)
	if n != 0 {
		t.Fatalf("a 30 s skip offered an undo")
	}

	// Next chapter moves only some seconds, yet it is a chapter jump.
	before := fullpPos(tab)
	fullpKey(t, tab, "PageDown", 34)
	tab.WaitFor(`window.tsPlayer.state().positionMs >= 149000`, 2*time.Second)
	tab.WaitFor(`document.querySelector(`+"`"+undoButton+"`"+`) !== null`, 2*time.Second)
	var text string
	tab.Eval(`document.querySelector("#player-full [data-ts-full-toasts] .ts-toast").textContent`, &text)
	if want := msgJumpedFar + msgJumpedBack; text != want {
		t.Errorf("toast text %q, want %q", text, want)
	}
	playerClickSel(t, tab, undoButton)
	tab.WaitFor(`window.tsPlayer.state().positionMs < 135000`, 2*time.Second)
	fullpNear(t, "after Go back", fullpPos(tab), before, 600)
	tab.Eval(`document.querySelectorAll("#player-full [data-ts-full-toasts] .ts-toast__actions").length`, &n)
	if n != 0 {
		t.Errorf("the offer stayed after Go back")
	}

	// A seek of over a minute offers it too, and going back is not itself undoable.
	tab.Eval(`window.tsPlayer.seek(10000)`, nil)
	tab.WaitFor(`document.querySelector(`+"`"+undoButton+"`"+`) !== null`, 2*time.Second)
	playerClickSel(t, tab, undoButton)
	tab.WaitFor(`window.tsPlayer.state().positionMs > 100000`, 2*time.Second)
	time.Sleep(300 * time.Millisecond)
	tab.Eval(`document.querySelectorAll("#player-full [data-ts-full-toasts] .ts-toast__actions").length`, &n)
	if n != 0 {
		t.Errorf("going back offered another undo")
	}
}

// TestBookSpeedBrowserSheet: the speed sheet says it is for this book, and its button makes the
// speed the usual one.
func TestBookSpeedBrowserSheet(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 844, false)
	fullpOpenPlayer(t, tab)
	speedCountSettings(tab)
	playerClickSel(t, tab, "[data-ts-open-speed]")
	speedWaitSheet(tab, speedSheetSel)
	var note string
	tab.Eval(`document.querySelector("#player-full .ts-player__forbook p").textContent`, &note)
	if note != msgSpeedForBook {
		t.Errorf("note %q, want %q", note, msgSpeedForBook)
	}
	playerClickSel(t, tab, `[data-ts-speed-choice="1.5"]`)
	speedWaitSheetClosed(tab, speedSheetSel)
	time.Sleep(1400 * time.Millisecond)
	playerClickSel(t, tab, "[data-ts-open-speed]")
	speedWaitSheet(tab, speedSheetSel)
	playerClickSel(t, tab, "[data-ts-speed-usual]")
	time.Sleep(500 * time.Millisecond)
	calls := speedCalls(tab)
	if len(calls) != 2 || calls[0] != "speed=1.50" || calls[1] != "speed=1.50&make_default=1" {
		t.Errorf("saves = %v", calls)
	}
}
