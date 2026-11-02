//go:build dev

package web

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The desktop player bar's browser tests (PB1, docs/design/tendshelf-player-bar), run by make player.
// The fixture is the player's (player_fixture_test.go): one book, six chapters of 30 s.

// TestPlayerBarBrowserCapture screenshots the page with the bar at phone, tablet and desktop widths. With
// TENDSHELF_PB_DIR set the PNGs go there, which is how a package proves that phone and tablet captures are
// unchanged before and after a change; otherwise they go to the run's output directory.
func TestPlayerBarBrowserCapture(t *testing.T) {
	e := playerApp(t)
	out := os.Getenv("TENDSHELF_PB_DIR")
	if out == "" {
		out = filepath.Join(playerOut(t), "bar")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, size := range [][2]int{{390, 844}, {768, 1024}, {1024, 768}, {1280, 800}, {1440, 900}, {1920, 1080}} {
		w, h := size[0], size[1]
		themesToCheck := []string{"dark", "light"}
		if w >= 1280 {
			themesToCheck = []string{"dark", "light", "sepia", "contrast"}
		}
		tab := fullpOpen(t, e, w, h, true)
		for _, theme := range themesToCheck {
			tab.Eval(`document.documentElement.setAttribute("data-theme", `+cdp.Quote(theme)+`)`, nil)
			time.Sleep(700 * time.Millisecond)
			tab.Screenshot(filepath.Join(out, fmt.Sprintf("bar-%s-%d.png", theme, w)))
		}
	}
}

// barRect is one element's box in CSS px.
type barRect struct{ L, R, T, B, W, H float64 }

func barBox(tab *cdp.Tab, sel string) barRect {
	var r barRect
	tab.Eval(`(() => { const r = document.querySelector(`+cdp.Quote(sel)+`).getBoundingClientRect(); return {L: r.left, R: r.right, T: r.top, B: r.bottom, W: r.width, H: r.height}; })()`, &r)
	return r
}

func barNear(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if got < want-tol || got > want+tol {
		t.Errorf("%s: %.1f, want %.1f +- %.1f", what, got, want, tol)
	}
}

// TestPlayerBarBrowserGeometry: at 1280, 1440 and 1920 the waveform column starts one column gap after the
// transport and ends one column gap before the tools, so no empty band is left in the bar; the bar is 88 px
// tall; the waveform is at least 320 px wide; the waveform and the times do not move while the clock ticks.
func TestPlayerBarBrowserGeometry(t *testing.T) {
	e := playerApp(t)
	for _, w := range []int{1280, 1440, 1920} {
		tab := fullpOpen(t, e, w, 900, true)
		time.Sleep(500 * time.Millisecond)
		var gap float64
		tab.Eval(`parseFloat(getComputedStyle(document.getElementById("mini-player")).columnGap)`, &gap)
		transport, seek, tools, wave, bar := barBox(tab, ".ts-pbar__transport"), barBox(tab, ".ts-pbar__seek"), barBox(tab, ".ts-pbar__tools"), barBox(tab, ".ts-wave--bar"), barBox(tab, "#mini-player")
		name := fmt.Sprintf("%dpx", w)
		barNear(t, name+" seek left = transport right + gap", seek.L, transport.R+gap, 1)
		barNear(t, name+" seek right = tools left - gap", seek.R, tools.L-gap, 1)
		barNear(t, name+" wave fills its column", wave.W, seek.W, 1)
		barNear(t, name+" bar height", bar.H, 88, 1)
		if wave.W < 320 {
			t.Errorf("%s: wave %.0f px wide, want at least 320", name, wave.W)
		}
		wantGap := 24.0
		if w < 1366 {
			wantGap = 16
		}
		barNear(t, name+" column gap", gap, wantGap, 0.5)

		// Three ticks: the wave's box and the three time cells keep their size and place.
		cells := func() []barRect {
			out := []barRect{barBox(tab, ".ts-wave--bar")}
			for _, c := range []string{"elapsed", "chapter", "left"} {
				out = append(out, barBox(tab, `[data-pbar="`+c+`"]`))
			}
			return out
		}
		before := cells()
		playerClickSel(t, tab, "[data-ts-toggle]")
		tab.WaitFor(`window.tsPlayer.state().playing`, 5*time.Second)
		time.Sleep(3200 * time.Millisecond)
		tab.Eval(`window.tsPlayer.pause()`, nil)
		for i, r := range cells() {
			if r != before[i] {
				t.Errorf("%s: box %d moved or changed size over three ticks: %+v -> %+v", name, i, before[i], r)
			}
		}
	}
}

// TestPlayerBarBrowserTools: with the side panel open there is no second Speed or Sleep control and no
// panel bar; the bar's Chapters opens the panel with the chapters sheet; Bookmark posts a bookmark; the
// expand button toggles the panel and says so.
func TestPlayerBarBrowserTools(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 1440, 900, true)
	tab.Eval(`(() => { window.barBookmarks = 0; const f = window.fetch;
	  window.fetch = (u, o) => { if (String(u).includes("/bookmarks") && o && o.method === "POST") window.barBookmarks++; return f(u, o); }; })()`, nil)

	// Expand toggles.
	playerClickSel(t, tab, "[data-ts-bar-expand]")
	tab.WaitFor(fullpDialog+`.open`, 3*time.Second)
	var s struct {
		Expanded, Label string
		PanelBar        string
		Speed, Sleep    int
	}
	read := func() {
		tab.Eval(`(() => { const vis = (el) => el.getClientRects().length > 0 && getComputedStyle(el).visibility !== "hidden";
		  const b = document.querySelector("[data-ts-bar-expand]");
		  return {Expanded: b.getAttribute("aria-expanded"), Label: b.getAttribute("aria-label"),
		    PanelBar: getComputedStyle(document.querySelector("#player-full .ts-player__bar")).display,
		    Speed: [...document.querySelectorAll("[data-ts-bar-speed], [data-ts-open-speed]")].filter(vis).length,
		    Sleep: [...document.querySelectorAll("[data-ts-bar-sleep], [data-ts-open-sleep]")].filter(vis).length}; })()`, &s)
	}
	read()
	if s.Expanded != "true" || s.Label != "Close player" || s.PanelBar != "none" || s.Speed != 1 || s.Sleep != 1 {
		t.Errorf("panel open: %+v, want expanded true, label Close player, panel bar hidden, one Speed and one Sleep", s)
	}
	playerClickSel(t, tab, "[data-ts-bar-expand]")
	tab.WaitFor(`!`+fullpDialog+`.open`, 3*time.Second)
	read()
	if s.Expanded != "false" || !strings.HasPrefix(s.Label, "Open player: Harbour Lamp") {
		t.Errorf("panel closed: expanded %q, label %q", s.Expanded, s.Label)
	}

	// Chapters opens the panel with the chapters sheet.
	playerClickSel(t, tab, "[data-ts-bar-chapters]")
	tab.WaitFor(fullpDialog+`.open && document.querySelector("#player-full [data-ts-chapter-list]").getClientRects().length > 0`, 3*time.Second)

	// Bookmark posts one bookmark.
	playerClickSel(t, tab, "[data-ts-bar-bookmark]")
	tab.WaitFor(`window.barBookmarks === 1`, 3*time.Second)
}

// TestPlayerBarBrowserVolume: the volume button opens a pop with the slider; Escape, a press outside and
// focus leaving close it, and Escape returns the focus to the button.
func TestPlayerBarBrowserVolume(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 1440, 900, true)
	state := func() (expanded string, hidden bool, focusOnButton bool) {
		var s struct {
			Expanded string
			Hidden   bool
			Focus    bool
		}
		tab.Eval(`({Expanded: document.querySelector("[data-ts-bar-volume]").getAttribute("aria-expanded"), Hidden: document.getElementById("ts-volume-pop").hidden,
		  Focus: document.activeElement === document.querySelector("[data-ts-bar-volume]")})`, &s)
		return s.Expanded, s.Hidden, s.Focus
	}
	open := func() {
		t.Helper()
		playerClickSel(t, tab, "[data-ts-bar-volume]")
		if ex, hidden, _ := state(); ex != "true" || hidden {
			t.Fatalf("after a click: expanded %q, pop hidden %v", ex, hidden)
		}
	}
	open()
	fullpKey(t, tab, "Escape", 27)
	if ex, hidden, focus := state(); ex != "false" || !hidden || !focus {
		t.Errorf("after Escape: expanded %q, hidden %v, focus on the button %v", ex, hidden, focus)
	}
	open()
	playerClickAt(t, tab, 700, 300) // the page, outside the pop
	if ex, hidden, _ := state(); ex != "false" || !hidden {
		t.Errorf("after a press outside: expanded %q, hidden %v", ex, hidden)
	}
	open()
	playerClickSel(t, tab, "[data-ts-bar-volume]")
	if ex, hidden, _ := state(); ex != "false" || !hidden {
		t.Errorf("after a second click on the button: expanded %q, hidden %v", ex, hidden)
	}
	open()
	tab.Eval(`document.querySelector("[data-ts-bar-expand]").focus()`, nil)
	if ex, hidden, _ := state(); ex != "false" || !hidden {
		t.Errorf("after focus left: expanded %q, hidden %v", ex, hidden)
	}
}
