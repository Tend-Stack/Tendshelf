//go:build dev

package web

import (
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// swapPlayerWatchHome watches Home through a progress save (every 10 s while playing), after which the
// hero is fetched again: through the refresh the bar must never leave and the new hero says Pause.
func swapPlayerWatchHome(t *testing.T, tab *cdp.Tab, where string) {
	t.Helper()
	tab.Eval(`(() => {
	  const probe = {bad: [], done: false};
	  window.homeProbe = probe;
	  const started = performance.now();
	  const tick = () => {
	    const p = document.getElementById("player");
	    const b = p.getBoundingClientRect();
	    const btn = document.querySelector("#home-hero [data-ts-play]");
	    const shown = !p.classList.contains("is-suppressed") && !p.inert && b.height > 0;
	    const word = btn ? btn.textContent.trim() : "";
	    if (window.tsPlayer.state().playing && (!shown || (btn && !word.startsWith("Pause") && word !== "Show player"))) probe.bad.push([Math.round(performance.now() - started), shown, word]);
	    if (performance.now() - started < 13000) setTimeout(tick, 50); else probe.done = true;
	  };
	  tick();
	})()`, nil)
	tab.WaitFor(`window.homeProbe.done`, 20*time.Second)
	var p struct{ Bad [][]any }
	tab.Eval(`window.homeProbe`, &p)
	if len(p.Bad) > 0 {
		t.Errorf("%s: while the book plays Home showed the bar hidden or the hero not on Pause: %v", where, p.Bad)
	}
}

// TestPlayerBrowserHomeHeroRefreshWhilePlaying refreshes the hero over and over (progress saves) right
// through the swap to Home and after it: no sample may show the bar hidden or Resume on the hero.
func TestPlayerBrowserHomeHeroRefreshWhilePlaying(t *testing.T) {
	e := playerApp(t)
	tab := swapPlayerStart(t, e, 1440, 900, true)
	persistClickNav(t, tab, `a[data-nav="/libraries"]`, "/libraries")
	tab.Eval(`(() => {
	  const probe = {bad: [], done: false, swaps: 0};
	  window.homeProbe = probe;
	  const started = performance.now();
	  const tick = () => {
	    const p = document.getElementById("player");
	    const btn = document.querySelector("#home-hero [data-ts-play]");
	    const shown = !p.classList.contains("is-suppressed") && !p.inert && p.getBoundingClientRect().height > 0;
	    const word = btn ? btn.textContent.trim() : "";
	    if (window.tsPlayer.state().playing && (!shown || (btn && !word.startsWith("Pause") && word !== "Show player"))) probe.bad.push([Math.round(performance.now() - started), shown, word]);
	    if (performance.now() - started < 4000) setTimeout(tick, 16); else probe.done = true;
	  };
	  tick();
	  let n = 0;
	  const refresh = setInterval(() => { document.body.dispatchEvent(new Event("progress-saved")); if (++n > 30) clearInterval(refresh); }, 100);
	})()`, nil)
	swapPlayerClickNow(t, tab, `a[data-nav="/"]`)
	tab.WaitFor(`window.homeProbe.done`, 10*time.Second)
	var p struct{ Bad [][]any }
	tab.Eval(`window.homeProbe`, &p)
	if len(p.Bad) > 0 {
		t.Errorf("Home showed the bar hidden or the hero on Resume while the book played: %v", p.Bad)
	}
}

// TestPlayerBrowserHomeBackWhilePlaying goes Home (paused, Resume on the hero), on to the Library, plays
// there from the bar and comes Back: the restored Home must show Pause and the bar.
func TestPlayerBrowserHomeBackWhilePlaying(t *testing.T) {
	e := playerApp(t)
	e.store(t, 10_000, "swapplayerdev", "First device", time.Now().UnixMilli()-5000, false)
	tab := playBtnOpenPage(t, chromium(t), e.URL+"/", 1440, 900)
	tab.WaitFor(`window.tsPlayer.state().bookId !== ""`, 8*time.Second)
	persistClickNav(t, tab, `a[data-nav="/libraries"]`, "/libraries")
	tab.WaitFor(persistBarJS, 5*time.Second)
	playerClickSel(t, tab, "#player [data-ts-toggle]")
	tab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().positionMs > 12000`, 10*time.Second)
	tab.WaitFor(`document.getAnimations().length === 0`, 3*time.Second)
	tab.Eval(`history.back()`, nil)
	tab.WaitFor(`location.pathname === "/" && document.querySelector("#home-hero [data-ts-play]") !== null`, 8*time.Second)
	time.Sleep(800 * time.Millisecond)
	persistExpectPausing(t, tab, "#home-hero [data-ts-play]", "Home after Back")
	swapPlayerBarInView(t, tab, "Home after Back")
}

var _ = cdp.Quote
