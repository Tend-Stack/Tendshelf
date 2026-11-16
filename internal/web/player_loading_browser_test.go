//go:build dev

package web

import (
	"strconv"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The player says it is loading (js/player.js setLoading and paintLoading, css/player.css): from the press of Play
// until the engine reports sound, a quiet ring replaces the icon of that item's play controls and veils the full
// player's cover, but only after 250 ms and for at least 400 ms.

// plHold stands between the player and the audio element: play() is accepted but nothing starts until
// window.__hold.release(), so the test decides how long the engine takes. paused follows what the player asked.
const plHold = `(() => {
  const a = document.getElementById("audio");
  const realPlay = a.play.bind(a), realPause = a.pause.bind(a);
  let held = true, paused = true;
  Object.defineProperty(a, "paused", { configurable: true, get: () => paused });
  a.play = () => { paused = false; a.dispatchEvent(new Event("play")); return held ? Promise.resolve() : realPlay(); };
  a.pause = () => { paused = true; realPause(); a.dispatchEvent(new Event("pause")); };
  window.__hold = { release: () => { held = false; if (!paused) realPlay().catch(() => {}); } };
})()`

// plSeen counts the moments any control showed the busy state, so a start that never shows it is provable.
const plSeen = `(() => {
  window.__busySeen = 0;
  new MutationObserver(() => { if (document.querySelector("[data-ts-loading]")) window.__busySeen++; })
    .observe(document.body, { subtree: true, attributes: true, attributeFilter: ["data-ts-loading"] });
})()`

type plBox struct{ X, Y, W, H float64 }

func plRect(tab *cdp.Tab, sel string) plBox {
	var r plBox
	tab.Eval(`(() => { const r = document.querySelector(`+cdp.Quote(sel)+`).getBoundingClientRect(); return {X: r.x + scrollX, Y: r.y + scrollY, W: r.width, H: r.height}; })()`, &r)
	return r
}

func plBusy(tab *cdp.Tab, sel string) (busy, ring bool) {
	var r struct{ Busy, Ring bool }
	tab.Eval(`(() => { const el = document.querySelector(`+cdp.Quote(sel)+`); return {Busy: el.getAttribute("aria-busy") === "true" && el.hasAttribute("data-ts-loading"), Ring: !!el.querySelector(".ts-loadring")}; })()`, &r)
	return r.Busy, r.Ring
}

const plBookButton = `a[data-ts-play], button[data-ts-play]`

func TestPlayerLoadingBookPage(t *testing.T) {
	e := playerApp(t)
	tab := playerOpen(t, chromium(t), e.URL, 390, 800)
	tab.Navigate(e.URL + "/books/" + e.book.ID)
	tab.WaitFor(`window.tsPlayer !== undefined && document.querySelector(`+cdp.Quote(plBookButton)+`) !== null`, 10*time.Second)
	tab.Eval(plHold, nil)
	tab.Eval(plSeen, nil)
	tab.Eval(`document.body.insertAdjacentHTML("beforeend", '<a href="/x" id="other-play" data-ts-play="bk_other">Other</a>')`, nil)
	before := plRect(tab, plBookButton)

	playerClickSel(t, tab, plBookButton)
	var early bool
	tab.Eval(`window.tsPlayer.state().loading && !document.querySelector("[data-ts-loading]")`, &early)
	if !early {
		t.Error("right after the press the state should be loading and nothing visible yet")
	}
	tab.WaitFor(`document.querySelector(`+cdp.Quote(plBookButton)+`).getAttribute("aria-busy") === "true"`, 3*time.Second)
	if busy, ring := plBusy(tab, plBookButton); !busy || !ring {
		t.Errorf("page button: busy %v ring %v", busy, ring)
	}
	if busy, _ := plBusy(tab, "[data-ts-toggle]"); !busy {
		t.Error("the bar's toggle is not busy")
	}
	if busy, _ := plBusy(tab, "#other-play"); busy {
		t.Error("another book's button shows the busy state")
	}
	if after := plRect(tab, plBookButton); after != before {
		t.Errorf("the page button moved or resized: %+v -> %+v", before, after)
	}

	// Sound arrives: the state clears at once, the picture after it has been up for 400 ms.
	tab.Eval(`window.__hold.release()`, nil)
	tab.WaitFor(`window.tsPlayer.state().playing && !window.tsPlayer.state().loading`, 8*time.Second)
	tab.WaitFor(`!document.querySelector("[data-ts-loading]")`, 3*time.Second)

	// (d) a wait in the middle of playing shows it again, and sound ends it.
	tab.Eval(`document.getElementById("audio").dispatchEvent(new Event("waiting"))`, nil)
	tab.WaitFor(`document.querySelector(`+cdp.Quote(plBookButton)+`).getAttribute("aria-busy") === "true"`, 3*time.Second)
	if after := plRect(tab, plBookButton); after != before {
		t.Errorf("mid-play: the page button moved or resized: %+v -> %+v", before, after)
	}
	tab.Eval(`document.getElementById("audio").dispatchEvent(new Event("playing"))`, nil)
	tab.WaitFor(`!window.tsPlayer.state().loading && !document.querySelector("[data-ts-loading]")`, 3*time.Second)
}

func TestPlayerLoadingFastStartShowsNothing(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 800, false) // the book is loaded already, so only the engine is waited for
	tab.Eval(plHold, nil)
	tab.Eval(plSeen, nil)
	playerClickSel(t, tab, "[data-ts-toggle]")
	time.Sleep(60 * time.Millisecond)
	tab.Eval(`window.__hold.release()`, nil)
	tab.WaitFor(`window.tsPlayer.state().playing && !window.tsPlayer.state().loading`, 8*time.Second)
	time.Sleep(700 * time.Millisecond)
	var seen int
	tab.Eval(`window.__busySeen`, &seen)
	if seen != 0 {
		t.Errorf("a start under 250 ms showed the busy state %d times", seen)
	}
}

func TestPlayerLoadingPressCancels(t *testing.T) {
	e := playerApp(t)
	tab := playerOpen(t, chromium(t), e.URL, 390, 800)
	tab.Navigate(e.URL + "/books/" + e.book.ID)
	tab.WaitFor(`window.tsPlayer !== undefined && document.querySelector(`+cdp.Quote(plBookButton)+`) !== null`, 10*time.Second)
	tab.Eval(plHold, nil)
	playerClickSel(t, tab, plBookButton)
	tab.WaitFor(`document.querySelector(`+cdp.Quote(plBookButton)+`).getAttribute("aria-busy") === "true"`, 3*time.Second)
	playerClickSel(t, tab, plBookButton) // the busy button is still a button: pressing it stops the start
	tab.WaitFor(`!window.tsPlayer.state().loading && !window.tsPlayer.state().playing`, 3*time.Second)
	tab.WaitFor(`!document.querySelector("[data-ts-loading]")`, 3*time.Second) // after the 400 ms the ring has been up
	var busy bool
	tab.Eval(`!!document.querySelector("[aria-busy=true]")`, &busy)
	if busy {
		t.Error("aria-busy is still set after the start was cancelled")
	}
}

// plFullHold opens the full player of a paused item, holds the engine and presses the large button.
func plFullHold(t *testing.T, tab *cdp.Tab) plBox {
	t.Helper()
	return plHoldVia(t, tab, "[data-ts-full-toggle]")
}

// plHoldVia presses the control sel with the engine held and waits for its busy state.
func plHoldVia(t *testing.T, tab *cdp.Tab, sel string) plBox {
	t.Helper()
	tab.Eval(plHold, nil)
	box := plRect(tab, sel)
	playerClickSel(t, tab, sel)
	tab.WaitFor(`document.querySelector(`+cdp.Quote(sel)+`).getAttribute("aria-busy") === "true"`, 3*time.Second)
	return box
}

func TestPlayerLoadingFullPlayerCover(t *testing.T) {
	for _, size := range []struct{ w, h int }{{390, 900}, {1440, 900}} {
		size := size
		t.Run(strconv.Itoa(size.w), func(t *testing.T) {
			e := playerApp(t)
			tab := fullpOpen(t, e, size.w, size.h, false)
			fullpOpenPlayer(t, tab)
			tab.Eval(`window.tsPlayer.pause()`, nil)
			// the phone's transport has the large button; the wide layout's Now playing panel leaves the bar's
			toggle := "[data-ts-full-toggle]"
			if size.w >= 1280 {
				toggle = "[data-ts-toggle]"
			}
			box := plHoldVia(t, tab, toggle)
			if after := plRect(tab, toggle); after != box {
				t.Errorf("%d: the play button moved or resized: %+v -> %+v", size.w, box, after)
			}
			if _, ring := plBusy(tab, toggle); !ring {
				t.Errorf("%d: the play button has no ring", size.w)
			}
			var cover struct {
				Veil    bool
				Opacity string
				Ring    string
			}
			tab.Eval(`(() => { const c = document.querySelector("#player-full .ts-player__cover"); return {
		  Veil: document.getElementById("player-full").hasAttribute("data-ts-loading-item"),
		  Opacity: getComputedStyle(c.querySelector("img")).opacity, Ring: getComputedStyle(c, "::after").animationName }; })()`, &cover)
			if !cover.Veil || cover.Opacity != "0.7" || cover.Ring != "ts-spin" {
				t.Errorf("%d: cover veil %+v", size.w, cover)
			}
			tab.Eval(`window.__hold.release()`, nil)
			tab.WaitFor(`!window.tsPlayer.state().loading`, 8*time.Second)
			tab.WaitFor(`!document.getElementById("player-full").hasAttribute("data-ts-loading-item")`, 3*time.Second)
			tab.Eval(`window.tsPlayer.pause()`, nil)
		})
	}
}

func TestPlayerLoadingReducedMotion(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 900, true)
	fullpOpenPlayer(t, tab)
	tab.Eval(`window.tsPlayer.pause()`, nil)
	plFullHold(t, tab)
	var r struct{ Anim, Dots, CoverAnim, CoverDots string }
	tab.Eval(`(() => { const ring = document.querySelector("[data-ts-full-toggle] .ts-loadring"), c = document.querySelector("#player-full .ts-player__cover");
	  return { Anim: getComputedStyle(ring).animationName, Dots: getComputedStyle(ring, "::before").content,
	    CoverAnim: getComputedStyle(c, "::after").animationName, CoverDots: getComputedStyle(c, "::after").content }; })()`, &r)
	if r.Anim != "none" || r.CoverAnim != "none" || len(r.Dots) < 4 || len(r.CoverDots) < 4 {
		t.Errorf("reduced motion should show static dots, got %+v", r)
	}
}

func TestPlayerLoadingRingContrast(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 900, false)
	fullpOpenPlayer(t, tab)
	tab.Eval(`window.tsPlayer.pause()`, nil)
	plFullHold(t, tab)
	for _, theme := range themes {
		tab.Eval(`document.documentElement.setAttribute("data-theme", `+cdp.Quote(theme)+`)`, nil)
		time.Sleep(150 * time.Millisecond)
		var r struct{ Ring, Ground, CoverRing, CoverGround string }
		tab.Eval(`(() => {
		  const ring = document.querySelector("[data-ts-full-toggle] .ts-loadring");
		  let ground = ring.parentElement;
		  while (ground && /rgba\(.*, 0\)$|transparent/.test(getComputedStyle(ground).backgroundColor)) ground = ground.parentElement;
		  const c = document.querySelector("#player-full .ts-player__cover");
		  return { Ring: getComputedStyle(ring).borderTopColor, Ground: getComputedStyle(ground).backgroundColor,
		    CoverRing: getComputedStyle(c, "::after").borderTopColor, CoverGround: getComputedStyle(c, "::before").backgroundColor };
		})()`, &r)
		want := 3.0 // a graphic
		if theme == "contrast" {
			want = 7
		}
		if got := pnrRatio(r.Ring, r.Ground); got < want {
			t.Errorf("%s: the button ring %.2f:1 on %s, want %.1f", theme, got, r.Ground, want)
		}
		if got := pnrRatio(r.CoverRing, r.CoverGround); got < want {
			t.Errorf("%s: the cover ring %.2f:1 on %s, want %.1f", theme, got, r.CoverGround, want)
		}
	}
}

func TestPlayerLoadingEpisodeAndStation(t *testing.T) {
	tab, w, _ := pkEpisode(t, false, 390, 900, "dark")
	_ = w
	box := plFullHold(t, tab)
	if after := plRect(tab, "[data-ts-full-toggle]"); after != box {
		t.Errorf("episode: the large button moved or resized: %+v -> %+v", box, after)
	}
	if busy, ring := plBusy(tab, "[data-ts-full-toggle]"); !busy || !ring {
		t.Errorf("episode: busy %v ring %v", busy, ring)
	}
	tab.Eval(`window.__hold.release()`, nil)
	tab.WaitFor(`window.tsPlayer.state().playing && !window.tsPlayer.state().loading`, 8*time.Second)
	tab.WaitFor(`!document.querySelector("[data-ts-loading]")`, 3*time.Second)

	stab, _, _ := pkStation(t, 390, 900, "dark")
	stab.Eval(`window.tsPlayer.pause()`, nil)
	stab.WaitFor(`!window.tsPlayer.state().playing`, 5*time.Second)
	plFullHold(t, stab)
	if busy, ring := plBusy(stab, "[data-ts-full-toggle]"); !busy || !ring {
		t.Errorf("station: busy %v ring %v", busy, ring)
	}
	stab.Eval(`window.__hold.release()`, nil)
	stab.WaitFor(`window.tsPlayer.state().playing && !window.tsPlayer.state().loading`, 10*time.Second)
	stab.WaitFor(`!document.querySelector("[data-ts-loading]")`, 3*time.Second)
}
