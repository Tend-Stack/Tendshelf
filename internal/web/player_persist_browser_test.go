//go:build dev

package web

import (
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The bar stays on every page while the book plays, and the page play buttons of that book are
// Pause controls in step with it; paused, one Resume per screen applies again (make player).

// persistBarJS is true when the bar is laid out, not suppressed and not inert.
const persistBarJS = `(() => { const p = document.getElementById("player"); const b = p.getBoundingClientRect();
  return !p.hidden && !p.classList.contains("is-suppressed") && !p.inert && b.width > 0 && b.height > 0; })()`

// persistBarVisible asserts the bar shows (and may be reached) right now.
func persistBarVisible(t *testing.T, tab *cdp.Tab, where string) {
	t.Helper()
	var ok bool
	tab.Eval(persistBarJS, &ok)
	if !ok {
		t.Errorf("%s: the bar is not visible while the book plays", where)
	}
}

type persistButtonState struct {
	Label, Aria         string
	PlayIcon, PauseIcon bool
}

// persistButton reads a play button: its label, aria-label and which icon is laid out.
func persistButton(tab *cdp.Tab, sel string) persistButtonState {
	var s persistButtonState
	tab.Eval(`(() => { const el = document.querySelector(`+cdp.Quote(sel)+`);
	  const shown = (n) => n !== null && n.getBoundingClientRect().width > 0;
	  const label = el.querySelector("[data-ts-play-label]");
	  return {Label: label ? label.textContent.trim() : "", Aria: el.getAttribute("aria-label") || "",
	    PlayIcon: shown(el.querySelector(":scope > svg")), PauseIcon: shown(el.querySelector("[data-ts-pause-icon] svg"))}; })()`, &s)
	return s
}

// persistExpectPausing asserts the button is a Pause control: word, pause icon, no play icon.
func persistExpectPausing(t *testing.T, tab *cdp.Tab, sel, where string) {
	t.Helper()
	tab.WaitFor(`document.querySelector(`+cdp.Quote(sel)+`).dataset.tsPausing === "1"`, 5*time.Second)
	s := persistButton(tab, sel)
	// The hero's button of the playing book shows the player (NP2): its word, and no play or pause icon.
	var shows bool
	tab.Eval(`document.querySelector(`+cdp.Quote(sel)+`).hasAttribute("data-ts-show-player")`, &shows)
	if shows {
		if s.Label != "Show player" || s.PauseIcon || s.PlayIcon {
			t.Errorf("%s: the hero button while playing is %+v, want Show player and no icon", where, s)
		}
		return
	}
	if s.Label != "Pause" || !s.PauseIcon || s.PlayIcon {
		t.Errorf("%s: the button while playing is %+v", where, s)
	}
}

// persistClickNav clicks a real link and waits for the htmx swap to finish.
func persistClickNav(t *testing.T, tab *cdp.Tab, sel, path string) {
	t.Helper()
	swapstylesClick(t, tab, sel)
	tab.WaitFor(`location.pathname === `+cdp.Quote(path)+` && document.getElementById("main") !== null`, 8*time.Second)
}

func TestPlayerBrowserPersistentWhilePlaying(t *testing.T) {
	for _, size := range []struct {
		name string
		w, h int
	}{{"390", 390, 800}, {"1440", 1440, 900}} {
		t.Run(size.name, func(t *testing.T) {
			e := playerApp(t)
			e.store(t, 10_000, "persistfirstdev", "First device", time.Now().UnixMilli()-5000, false)
			tab := playBtnOpenPage(t, chromium(t), e.URL+"/", size.w, size.h)
			tab.WaitFor(`window.tsPlayer.state().bookId !== ""`, 8*time.Second)
			hero := "#home-hero [data-ts-play]"
			tab.WaitFor(`document.getElementById("player").classList.contains("is-suppressed")`, 5*time.Second)
			if s := persistButton(tab, hero); s.Label != "Resume" || !s.PlayIcon || s.PauseIcon {
				t.Fatalf("the paused hero button is %+v", s)
			}

			// Playing from the hero: the bar stays, the hero button is Pause.
			playerClickSel(t, tab, hero)
			tab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().positionMs > 12000`, 10*time.Second)
			tab.WaitFor(persistBarJS, 5*time.Second)
			persistExpectPausing(t, tab, hero, "Home")
			persistBarVisible(t, tab, "Home")

			// A real click to the Library and on to the book page: the bar stays all the way.
			persistClickNav(t, tab, `a[data-nav="/libraries"]`, "/libraries")
			tab.WaitFor(persistBarJS, 5*time.Second)
			if st := playerStateOf(tab); !st.Playing {
				t.Errorf("the book stopped on the way to the Library: %+v", st)
			}
			persistClickNav(t, tab, `a[href="/books/`+e.book.ID+`"]`, "/books/"+e.book.ID)
			bookBtn := "#book-play [data-ts-play]"
			tab.WaitFor(`document.querySelector(`+cdp.Quote(bookBtn)+`) !== null`, 5*time.Second)
			persistExpectPausing(t, tab, bookBtn, "book page")
			persistBarVisible(t, tab, "book page")

			// Pause with the page button: audio pauses (no restart, no seek), the button gets its
			// own words back and the bar steps aside for it.
			before := playerStateOf(tab).PositionMs
			tab.WaitFor(`document.getAnimations().length === 0`, 3*time.Second) // the page swap has settled
			time.Sleep(300 * time.Millisecond)
			playerClickSel(t, tab, bookBtn)
			tab.WaitFor(`!window.tsPlayer.state().playing`, 5*time.Second)
			tab.WaitFor(`document.querySelector(`+cdp.Quote(bookBtn)+`).dataset.tsPausing !== "1"`, 5*time.Second)
			s := persistButton(tab, bookBtn)
			if s.Label == "" || s.Label == "Pause" || !s.PlayIcon || s.PauseIcon {
				t.Errorf("the paused book page button is %+v", s)
			}
			after := playerStateOf(tab)
			if after.Playing || after.PositionMs < before || after.PositionMs > before+4000 {
				t.Errorf("pausing from the page moved the book: before %d, after %+v", before, after)
			}
			tab.WaitFor(`document.getElementById("player").classList.contains("is-suppressed") && document.getElementById("player").inert`, 5*time.Second)

			// Scrolled away from the button the bar returns.
			tab.Eval(`(() => { const d = document.createElement("div"); d.style.height = "3000px"; document.getElementById("main").append(d); window.scrollTo(0, 2500); })()`, nil)
			tab.WaitFor(`!document.getElementById("player").classList.contains("is-suppressed") && !document.getElementById("player").inert`, 5*time.Second)
		})
	}
}
