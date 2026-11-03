//go:build dev

package web

import (
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// navKey dispatches one real key press (down then up) to the focused element.
func navKey(t *testing.T, tab *cdp.Tab, key, code string, vk int, text string) {
	t.Helper()
	for _, typ := range []string{"keyDown", "keyUp"} {
		p := map[string]any{"type": typ, "key": key, "code": code, "windowsVirtualKeyCode": vk}
		if typ == "keyDown" {
			p["text"] = text
		}
		if err := tab.Call("Input.dispatchKeyEvent", p, nil); err != nil {
			t.Fatal(err)
		}
	}
}

// TestNavRailBrowser drives the sidebar's collapse toggle at 1440 px while a book plays.
func TestNavRailBrowser(t *testing.T) {
	e := playerApp(t)
	b := chromium(t)
	tab := playerOpen(t, b, e.URL, 1440, 900)
	e.playerInject(tab, e.book.ID, 0, 0, "")
	playerClickSel(t, tab, "#play-link")
	tab.WaitFor(`window.tsPlayer.state().playing`, 10*time.Second)

	width := func() float64 {
		var w float64
		tab.Eval(`document.getElementById("nav-side").getBoundingClientRect().width`, &w)
		return w
	}
	mainLeft := func() float64 {
		var x float64
		tab.Eval(`document.getElementById("main").getBoundingClientRect().left`, &x)
		return x
	}
	mode := func() string {
		var m string
		tab.Eval(`document.getElementById("nav-side").dataset.nav`, &m)
		return m
	}
	var railToken float64
	tab.Eval(`(() => { const d = document.createElement("div"); d.style.width = "var(--size-side-rail)"; document.body.append(d); const w = d.getBoundingClientRect().width; d.remove(); return w; })()`, &railToken)
	fullW, fullLeft := width(), mainLeft()
	if mode() != "full" || fullW <= railToken {
		t.Fatalf("start: mode %s, width %.0f, rail token %.0f", mode(), fullW, railToken)
	}

	// The toggle stays fully visible above the player bar while a book plays.
	var toggleBottom, barTop float64
	tab.Eval(`document.getElementById("nav-toggle").getBoundingClientRect().bottom`, &toggleBottom)
	tab.Eval(`document.getElementById("player").getBoundingClientRect().top`, &barTop)
	if toggleBottom >= barTop {
		t.Errorf("#nav-toggle bottom %.0f is not above the player bar top %.0f", toggleBottom, barTop)
	}

	// Click: the sidebar becomes the rail, #main moves left, the audio keeps playing.
	before := playerStateOf(tab)
	playerClickSel(t, tab, "#nav-toggle")
	tab.WaitFor(`document.getElementById("nav-side").dataset.nav === "rail"`, 5*time.Second)
	if w := width(); w != railToken {
		t.Errorf("rail width %.0f, want the token %.0f", w, railToken)
	}
	if l := mainLeft(); l >= fullLeft {
		t.Errorf("#main left %.0f did not move left of %.0f", l, fullLeft)
	}
	time.Sleep(600 * time.Millisecond)
	if s := playerStateOf(tab); !s.Playing || s.PositionMs <= before.PositionMs {
		t.Errorf("toggle stopped the audio: before %+v, after %+v", before, s)
	}

	// A boosted navigation keeps the rail and the audio.
	playerClickSel(t, tab, `#nav-side a[data-nav="/search"]`)
	tab.WaitFor(`location.pathname === "/search"`, 5*time.Second)
	if mode() != "rail" || width() != railToken {
		t.Errorf("after boosted navigation: mode %s, width %.0f", mode(), width())
	}
	if s := playerStateOf(tab); !s.Playing {
		t.Errorf("boosted navigation stopped the audio: %+v", s)
	}

	// A reload renders the saved state.
	tab.Navigate(e.URL + "/libraries")
	tab.WaitFor(`window.tsPlayer !== undefined`, 10*time.Second)
	if mode() != "rail" || width() != railToken {
		t.Errorf("after reload: mode %s, width %.0f", mode(), width())
	}

	// Keyboard: focus the toggle, Enter expands, the swap keeps the focus, Space collapses again.
	tab.Eval(`document.getElementById("nav-toggle").focus()`, nil)
	navKey(t, tab, "Enter", "Enter", 13, "\r")
	tab.WaitFor(`document.getElementById("nav-side").dataset.nav === "full"`, 5*time.Second)
	var focused string
	tab.Eval(`document.activeElement && document.activeElement.id`, &focused)
	if focused != "nav-toggle" {
		t.Errorf("focus after Enter on %q, want nav-toggle", focused)
	}
	var ring string
	tab.Eval(`(() => { const el = document.getElementById("nav-toggle"); return el.matches(":focus-visible") ? getComputedStyle(el).boxShadow : "not focus-visible"; })()`, &ring)
	if ring == "" || ring == "none" || ring == "not focus-visible" {
		t.Errorf("toggle focus ring = %q", ring)
	}
	var h struct{ W, H float64 }
	tab.Eval(`(() => { const r = document.getElementById("nav-toggle").getBoundingClientRect(); return {W: r.width, H: r.height}; })()`, &h)
	if h.W < 44 || h.H < 44 {
		t.Errorf("toggle is %.0f x %.0f, want at least 44", h.W, h.H)
	}
	navKey(t, tab, " ", "Space", 32, " ")
	tab.WaitFor(`document.getElementById("nav-side").dataset.nav === "rail"`, 5*time.Second)
}
