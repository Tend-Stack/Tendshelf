//go:build dev

package web

import (
	"testing"
	"time"
)

// A toast another module makes through window.tsPlayer.toast leaves by itself even inside the full
// player, whose toast region nothing else watches (the queued-bookmark toast stayed, operator 2026-11-04).
func TestPlayerToastLeavesInFullPlayer(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 844, false)
	fullpOpenPlayer(t, tab)
	var inPlayer bool
	tab.Eval(`(() => { const el = window.tsPlayer.toast("hint", "Toast that should leave"); el.id = "timed-toast";
	  return !!el.closest("[data-ts-full-toasts]"); })()`, &inPlayer)
	if !inPlayer {
		t.Fatal("the toast did not go into the full player's own region")
	}
	tab.WaitFor(`document.getElementById("timed-toast") === null`, 7*time.Second)

	// A tap dismisses one at once.
	tab.Eval(`(() => { const el = window.tsPlayer.toast("err", "Tap me"); el.id = "tap-toast"; el.click(); return true; })()`, nil)
	tab.WaitFor(`document.getElementById("tap-toast") === null`, 2*time.Second)
}
