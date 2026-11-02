//go:build dev

package web

import (
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// Two regressions of the page swap with a book playing (make player): the pages must not paint over
// the side panel while the swap's view transition runs, and the bar must show on Home.

// swapPlayerStart opens the book page at w x h, plays from its button and opens the full player when
// open is set (on desktop widths it is the right-hand side panel).
func swapPlayerStart(t *testing.T, e *playerEnv, w, h int, open bool) *cdp.Tab {
	t.Helper()
	e.store(t, 10_000, "swapplayerdev", "First device", time.Now().UnixMilli()-5000, false)
	tab := playBtnOpenPage(t, chromium(t), e.URL+"/books/"+e.book.ID, w, h)
	tab.WaitFor(`window.tsPlayer.state().bookId !== ""`, 8*time.Second)
	playerClickSel(t, tab, "#book-play [data-ts-play]")
	tab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().positionMs > 11000`, 10*time.Second)
	tab.WaitFor(persistBarJS, 5*time.Second)
	if open {
		fullpOpenPlayer(t, tab)
	}
	return tab
}

// swapPlayerBarInView asserts the bar shows and lies fully inside the viewport.
func swapPlayerBarInView(t *testing.T, tab *cdp.Tab, where string) {
	t.Helper()
	var r struct {
		OK       bool
		Top, Bot float64
		InnerH   float64
		Suppress bool
		Inert    bool
		Hidden   bool
	}
	tab.Eval(`(() => { const p = document.getElementById("player"); const b = p.getBoundingClientRect();
	  return {OK: b.height > 0, Top: b.top, Bot: b.bottom, InnerH: innerHeight, Suppress: p.classList.contains("is-suppressed"), Inert: p.inert, Hidden: p.hidden}; })()`, &r)
	if !r.OK || r.Suppress || r.Inert || r.Hidden || r.Top < 0 || r.Bot > r.InnerH+0.5 {
		t.Errorf("%s: the bar is not showing while the book plays: %+v", where, r)
	}
}

func TestPlayerBrowserHomeBarWhilePlaying(t *testing.T) {
	for _, c := range []struct {
		name string
		w, h int
		open bool
	}{{"1440-panel", 1440, 900, true}, {"1440-closed", 1440, 900, false}, {"1920-panel", 1920, 1240, true}} {
		t.Run(c.name, func(t *testing.T) {
			e := playerApp(t)
			tab := swapPlayerStart(t, e, c.w, c.h, c.open)
			persistClickNav(t, tab, `a[data-nav="/libraries"]`, "/libraries")
			persistClickNav(t, tab, `a[data-nav="/"]`, "/")
			tab.WaitFor(`document.querySelector("#home-hero [data-ts-play]") !== null`, 8*time.Second)
			time.Sleep(600 * time.Millisecond)
			persistExpectPausing(t, tab, "#home-hero [data-ts-play]", "Home hero")
			swapPlayerBarInView(t, tab, "after the boosted swap to Home")
			swapPlayerWatchHome(t, tab, c.name)
		})
	}
	t.Run("reload-on-home", func(t *testing.T) {
		e := playerApp(t)
		e.store(t, 10_000, "swapplayerdev", "First device", time.Now().UnixMilli()-5000, false)
		tab := playBtnOpenPage(t, chromium(t), e.URL+"/", 1440, 900)
		tab.WaitFor(`window.tsPlayer.state().bookId !== ""`, 8*time.Second)
		playerClickSel(t, tab, "#home-hero [data-ts-play]")
		tab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().positionMs > 12000`, 10*time.Second)
		persistExpectPausing(t, tab, "#home-hero [data-ts-play]", "Home hero")
		swapPlayerBarInView(t, tab, "Home played in place")
		swapPlayerWatchHome(t, tab, "reload")
	})
}
