//go:build dev

package web

import (
	"testing"
	"time"
)

// TestFullPlayerBrowserSleepCardOnHome: on Home, with the bar showing (the book plays, so the hero
// button is a Pause control), a sleep fade must show its card with a +10 min button that is on
// screen, at least 44 px, and does not cover the hero's button.
func TestFullPlayerBrowserSleepCardOnHome(t *testing.T) {
	for _, size := range []struct {
		name string
		w, h int
	}{{"390", 390, 800}, {"1440", 1440, 900}} {
		t.Run(size.name, func(t *testing.T) {
			e := playerApp(t)
			e.store(t, 10_000, "sweepfirstdev", "First device", time.Now().UnixMilli()-5000, false)
			tab := playBtnOpenPage(t, chromium(t), e.URL+"/", size.w, size.h)
			tab.WaitFor(`window.tsPlayer.state().bookId !== ""`, 8*time.Second)

			// Play from the hero, then scroll past it so the bar is back, and start a 15-minute timer.
			playerClickSel(t, tab, "#home-hero [data-ts-play]")
			tab.WaitFor(`window.tsPlayer.state().playing`, 10*time.Second)
			tab.Eval(`(() => { const d = document.createElement("div"); d.style.height = "3000px"; document.getElementById("main").append(d); window.scrollTo(0, 2500); })()`, nil)
			tab.WaitFor(`!document.getElementById("player").classList.contains("is-suppressed")`, 5*time.Second)
			fullpOpenPlayer(t, tab)
			sleepChoose(t, tab, "15")
			playerClickSel(t, tab, "[data-ts-close-player]")
			tab.WaitFor(`!document.getElementById("player-full").open`, 3*time.Second)

			// With the bar showing, the card sits above it and does not cover it.
			tab.Eval(`window.tsPlayerFull.debug.advanceSleep(15 * 60000 - 9000)`, nil)
			var above bool
			tab.Eval(`(() => { const c = document.querySelector('[data-ts-sleep-card="bar"]').getBoundingClientRect(), m = document.getElementById("mini-player").getBoundingClientRect();
			  return c.height > 0 && c.bottom <= m.top; })()`, &above)
			if !above {
				t.Error("the sleep card is not above the mini-player")
			}
			playerClickSel(t, tab, `[data-ts-sleep-card="bar"] [data-ts-sleep-plus]`)

			// Back at the top the book is still playing, so the bar stays beside the hero's Pause
			// button; the fade must show without covering either.
			tab.Eval(`window.scrollTo(0, 0)`, nil)
			time.Sleep(400 * time.Millisecond) // the observer's report
			tab.WaitFor(`!document.getElementById("player").classList.contains("is-suppressed") && window.tsPlayer.state().playing`, 5*time.Second)
			tab.Eval(`window.tsPlayerFull.debug.advanceSleep(window.tsPlayerFull.debug.sleep().leftMs - 9000)`, nil)
			time.Sleep(400 * time.Millisecond) // the slot's slide

			var got struct {
				Shown, Inside, Big, ResumeFree, PlusFree bool
				Text                                     string
			}
			tab.Eval(`(() => {
			  const card = document.querySelector('[data-ts-sleep-card="bar"]'), plus = card.querySelector("[data-ts-sleep-plus]");
			  const play = document.querySelector("#home-hero [data-ts-play]");
			  const at = (el) => { const r = el.getBoundingClientRect(); return document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2); };
			  const b = plus.getBoundingClientRect(), p = play.getBoundingClientRect();
			  return {Shown: !card.hidden && card.getBoundingClientRect().height > 0, Text: card.querySelector("[data-ts-sleep-count]").textContent,
			    Inside: b.top >= 0 && b.left >= 0 && b.bottom <= innerHeight && b.right <= innerWidth,
			    Big: b.width >= 44 && b.height >= 44, ResumeFree: play.contains(at(play)), PlusFree: plus.contains(at(plus)),
			    Overlap: !(b.right < p.left || b.left > p.right || b.bottom < p.top || b.top > p.bottom)};
			})()`, &got)
			if !got.Shown || !got.Inside || !got.Big || !got.PlusFree {
				t.Errorf("sleep card on Home: %+v", got)
			}
			if !got.ResumeFree {
				t.Error("the sleep card covers the hero's Resume")
			}

			// +10 min answers it: the card goes.
			playerClickSel(t, tab, `[data-ts-sleep-card="bar"] [data-ts-sleep-plus]`)
			var hidden bool
			tab.Eval(`document.querySelector('[data-ts-sleep-card="bar"]').hidden`, &hidden)
			if !hidden {
				t.Error("the card stays after +10 min")
			}
		})
	}
}
