//go:build dev

package web

import (
	"strings"
	"testing"
	"time"
)

// TestNowPanelLookupKeepsPlaying plays a book with no description, opens the full player's About
// tab, presses "Look up details online" and checks that the lookup sheet opens for that book while
// the same <audio> element keeps playing and no page load happens. Needs TENDSHELF_TEST_BROWSER.
func TestNowPanelLookupKeepsPlaying(t *testing.T) {
	cn := &compareNet{}
	e := playerAppMods(t, nil, compareMod(t, cn))
	tab := fullpOpen(t, e, 1440, 900, true)
	bmkeyPlay(t, tab, "[data-ts-toggle]")
	fullpOpenPlayer(t, tab)
	tab.WaitFor(`document.querySelector("#now-panel[data-book-id] .ts-np__empty a.ts-btn") !== null`, 5*time.Second)
	tab.Eval(`window.__tsMarker = 1; document.querySelector("audio").__tsSame = 1; window.__tsLoads = 0;
	  window.addEventListener("load", () => { window.__tsLoads++; })`, nil)
	var before float64
	tab.Eval(`document.querySelector("audio").currentTime`, &before)
	playerClickSel(t, tab, "#now-panel .ts-np__empty a.ts-btn")
	tab.WaitFor(`document.getElementById("sheet").children.length > 0`, 8*time.Second)
	var sheet string
	tab.Eval(`document.getElementById("sheet").textContent`, &sheet)
	if !strings.Contains(sheet, "Look up") {
		t.Errorf("the lookup sheet is not open: %q", sheet)
	}
	var r struct {
		Marker, Same, Loads int
		Path                string
		Time                float64
		Playing             bool
	}
	deadline := time.Now().Add(4 * time.Second)
	for {
		tab.Eval(`({ Marker: window.__tsMarker || 0, Same: document.querySelector("audio").__tsSame || 0, Loads: window.__tsLoads,
		  Path: location.pathname, Time: document.querySelector("audio").currentTime, Playing: window.tsPlayer.state().playing })`, &r)
		if r.Time > before || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if r.Marker != 1 || r.Same != 1 || r.Loads != 0 {
		t.Errorf("the page reloaded: marker %d, same audio %d, loads %d", r.Marker, r.Same, r.Loads)
	}
	if !r.Playing || r.Time <= before {
		t.Errorf("the book stopped: playing %v, time %.2f (was %.2f)", r.Playing, r.Time, before)
	}
	var top bool
	tab.Eval(`(() => { const s = document.querySelector("#sheet [role=dialog], #sheet dialog, #sheet > *"); const r = s.getBoundingClientRect();
	  const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2); return !!hit && s.contains(hit); })()`, &top)
	if !top {
		t.Error("the lookup sheet is behind the full player")
	}
}
