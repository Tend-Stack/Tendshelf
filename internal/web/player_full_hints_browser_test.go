//go:build dev

package web

import (
	"fmt"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// TestFullPlayerBrowserHints records the StatePlayerHints board (the rewind hint and the sleep
// timer's fade) at 390, 768, 1024 and 1440 px: hints-<theme>-<width>.png under
// .local/tests/player/<run-id>/. It also checks the card and its +10 min button: at least 44 px,
// inside the viewport, no overflow, and the hint inside the player's own toast region on a phone.
func TestFullPlayerBrowserHints(t *testing.T) {
	e := playerApp(t)
	out := playerOut(t)
	themesToCheck := themes
	if !visualFull() {
		themesToCheck = []string{"dark", "light"}
	}
	for _, size := range [][2]int{{390, 844}, {768, 1024}, {1024, 768}, {1440, 900}} {
		w, h := size[0], size[1]
		tab := fullpOpen(t, e, w, h, true)
		fullpOpenPlayer(t, tab)
		// The sleep timer first (the sound is not playing yet), then the break rule and the play click.
		sleepChoose(t, tab, "15")
		tab.Eval(`window.tsPlayer.debug.setPausedAt(Date.now() - 61000)`, nil)
		// From 1280 px the side panel has no transport of its own (the bar holds play/pause), so the
		// play click goes to the bar there.
		toggle := "[data-ts-full-toggle]"
		if w >= 1280 {
			toggle = "#player [data-ts-toggle]"
		}
		playerClickSel(t, tab, toggle)
		tab.WaitFor(`window.tsPlayer.state().playing`, 5*time.Second)
		tab.Eval(`window.tsPlayerFull.debug.advanceSleep(15 * 60000 - 8000)`, nil)
		tab.WaitFor(`document.querySelector('#player-full [data-ts-sleep-card="full"]') && !document.querySelector('#player-full [data-ts-sleep-card="full"]').hidden`, 3*time.Second)
		for _, theme := range themesToCheck {
			tab.Eval(`document.documentElement.setAttribute("data-theme", `+cdp.Quote(theme)+`)`, nil)
			// the rewind hint leaves after 1.6 s; show it again for each capture
			tab.Eval(`document.querySelectorAll(".ts-toast--hint").forEach((x) => x.remove());
			  window.tsPlayer.toast("hint", document.getElementById("player").dataset.tsMsgRewound)`, nil)
			time.Sleep(250 * time.Millisecond)
			var r struct {
				Over         bool
				CardW, CardH float64
				PlusW, PlusH float64
				Inside       bool
				Hint         string
				HintInPlayer bool
				Text         string
				ToggleBg     string
				CoverBg      string
			}
			tab.Eval(`(() => {
			  const card = document.querySelector('#player-full [data-ts-sleep-card="full"]'), plus = card.querySelector("[data-ts-sleep-plus]");
			  const c = card.getBoundingClientRect(), p = plus.getBoundingClientRect();
			  const hint = [...document.querySelectorAll(".ts-toast--hint")].pop();
			  // The pointer is still on the play button after the click: it must keep its cover-coloured circle.
			  const toggle = document.querySelector("[data-ts-full-toggle]"), probe = document.createElement("span");
			  probe.style.setProperty("background-color", "var(--cover-accent)");
			  toggle.parentNode.append(probe);
			  const toggleBg = getComputedStyle(toggle).backgroundColor, coverBg = getComputedStyle(probe).backgroundColor;
			  probe.remove();
			  return {Over: document.documentElement.scrollWidth > innerWidth || document.getElementById("player-full").scrollWidth > document.getElementById("player-full").clientWidth,
			    CardW: c.width, CardH: c.height, PlusW: p.width, PlusH: p.height,
			    Inside: c.left >= 0 && c.right <= innerWidth && c.top >= 0 && c.bottom <= innerHeight && p.right <= innerWidth && p.bottom <= innerHeight,
			    Hint: hint ? hint.textContent : "", HintInPlayer: hint ? !!hint.closest("[data-ts-full-toasts]") : false, Text: card.textContent, ToggleBg: toggleBg, CoverBg: coverBg};
			})()`, &r)
			name := fmt.Sprintf("%s/hints-%s-%d", out, theme, w)
			if r.Over {
				t.Errorf("%s: horizontal overflow", name)
			}
			if r.PlusW < 43.5 || r.PlusH < 43.5 || r.CardH < 43.5 {
				t.Errorf("%s: +10 min %.0fx%.0f, card %.0fx%.0f; controls need 44 px", name, r.PlusW, r.PlusH, r.CardW, r.CardH)
			}
			if !r.Inside {
				t.Errorf("%s: the sleep card is outside the viewport", name)
			}
			if r.ToggleBg != r.CoverBg {
				t.Errorf("%s: play button background %s under the pointer, want the cover accent %s", name, r.ToggleBg, r.CoverBg)
			}
			if r.Hint != msgRewound {
				t.Errorf("%s: hint %q, want %q", name, r.Hint, msgRewound)
			}
			if w < 1280 && !r.HintInPlayer {
				t.Errorf("%s: the hint is not in the player's toast region", name)
			}
			if theme == "dark" || theme == "light" || visualFull() {
				tab.Screenshot(name + ".png")
			}
		}
	}
}
