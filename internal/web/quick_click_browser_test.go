//go:build dev

package web

import (
	"testing"
	"time"
)

// TestQuickClickBrowser: a click that lands while the previous page change is still animating is not
// lost. During a view transition Chromium sends the click to <html> (whatever CSS says), so it went
// nowhere; shell.js ends the transition and replays it on the element under the pointer (three
// packages on 2026-11-02 had to wait out the animation in their tests).
func TestQuickClickBrowser(t *testing.T) {
	e := playerApp(t)
	tab := chromium(t).NewTab(1440, 900, false, false)
	tab.Navigate(e.URL + "/")
	tab.WaitFor(`document.querySelector(".ts-home") !== null && document.getAnimations().length === 0`, 5*time.Second)
	time.Sleep(300 * time.Millisecond)

	// Go to Library, then click Search as soon as the change has started animating.
	swapstylesClick(t, tab, `a[data-nav="/libraries"]`)
	tab.WaitFor(`location.pathname === "/libraries" && document.getAnimations().length > 0`, 5*time.Second)
	var r struct{ X, Y float64 }
	tab.Eval(`(() => { const e = [...document.querySelectorAll('a[data-nav="/search"]')].find(e => e.getBoundingClientRect().width > 0);
	  const r = e.getBoundingClientRect(); return {X: r.x + r.width / 2, Y: r.y + r.height / 2}; })()`, &r)
	var running bool
	tab.Eval(`document.getAnimations().length > 0`, &running)
	if !running {
		t.Skip("the page change finished before the click could land; nothing to prove on this machine")
	}
	playerClickAt(t, tab, r.X, r.Y)
	swapstylesPath(t, tab, "/search", `document.querySelector(".ts-sf") !== null`)

	// The same quick click landing on a nav icon (SVG, which has no click()) still navigates.
	tab.WaitFor(`document.getAnimations().length === 0 && !document.activeViewTransition`, 5*time.Second)
	swapstylesClick(t, tab, `a[data-nav="/libraries"]`)
	tab.WaitFor(`location.pathname === "/libraries" && document.getAnimations().length > 0`, 5*time.Second)
	tab.Eval(`(() => { const e = [...document.querySelectorAll('a[data-nav="/search"] svg')].find(e => e.getBoundingClientRect().width > 0);
	  const r = e.getBoundingClientRect(); return {X: r.x + r.width / 2, Y: r.y + r.height / 2}; })()`, &r)
	tab.Eval(`document.getAnimations().length > 0`, &running)
	if !running {
		t.Skip("the page change finished before the icon click could land; nothing to prove on this machine")
	}
	playerClickAt(t, tab, r.X, r.Y)
	swapstylesPath(t, tab, "/search", `document.querySelector(".ts-sf") !== null`)
}
