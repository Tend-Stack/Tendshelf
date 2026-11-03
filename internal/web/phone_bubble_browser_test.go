//go:build dev

package web

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

// The phone mini-player is a round bubble (operator request, 2026-11-03; docs/design/screens.md): a circle at
// the bottom right above the tab bar, the cover inside a progress ring of the book, the play button on its
// lower-left edge. Run by make player (TestPhoneBubble*). phoneOpen emulates reduced motion, so the
// behaviour test doubles as the "no animation" check.

// TestPhoneBubbleGeometry checks the circle, its place and its targets at 360, 390 and 430 px and with Larger text.
func TestPhoneBubbleGeometry(t *testing.T) {
	e := playerApp(t)
	for _, c := range []struct {
		w, h  int
		large bool
	}{{360, 740, false}, {390, 844, false}, {430, 932, false}, {390, 844, true}} {
		name := fmt.Sprintf("%dx%d large=%v", c.w, c.h, c.large)
		tab := phoneOpen(t, e, c.w, c.h, c.large)
		var r struct {
			Mini, Toggle, Open, Tab [4]float64
			Radius                  string
			CW                      float64
			HitToggle, HitCover     bool
			Meta, Letter            string
		}
		tab.Eval(`(() => {
		  const box = (el) => { const r = el.getBoundingClientRect(); return [r.left, r.top, r.right, r.bottom]; };
		  const mini = document.getElementById("mini-player"), toggle = document.querySelector("[data-ts-toggle]"), open = document.querySelector("[data-ts-open]");
		  const t = box(toggle), o = box(open);
		  return {Mini: box(mini), Toggle: t, Open: o, Tab: box(document.getElementById("nav-tabbar")),
		    Radius: getComputedStyle(mini).borderRadius, CW: document.documentElement.clientWidth,
		    HitToggle: toggle.contains(document.elementFromPoint((t[0] + t[2]) / 2, (t[1] + t[3]) / 2)),
		    HitCover: !!document.elementFromPoint(o[2] - 12, o[1] + 12).closest("[data-ts-open]"),
		    Meta: document.querySelector("[data-ts-meta]").textContent, Letter: open.dataset.letter};
		})()`, &r)
		w, h := r.Mini[2]-r.Mini[0], r.Mini[3]-r.Mini[1]
		if d := w - h; d > 1 || d < -1 || w < 63 {
			t.Errorf("%s: bubble %.0fx%.0f, want a circle of at least 64 px", name, w, h)
		}
		if r.Radius != "50%" {
			t.Errorf("%s: border radius %q", name, r.Radius)
		}
		if r.Mini[2] > r.CW-8 || r.Mini[2] < r.CW-24 || r.Toggle[0] < 0 || r.Mini[1] < 0 {
			t.Errorf("%s: bubble %v toggle %v not at the right inside width %.0f", name, r.Mini, r.Toggle, r.CW)
		}
		if r.Mini[3] > r.Tab[1] || r.Toggle[3] > r.Tab[1] {
			t.Errorf("%s: bubble %v / toggle %v reach the tab bar %v", name, r.Mini, r.Toggle, r.Tab)
		}
		if tw, th := r.Toggle[2]-r.Toggle[0], r.Toggle[3]-r.Toggle[1]; tw < 43.5 || th < 43.5 {
			t.Errorf("%s: play button %.0fx%.0f, want at least 44", name, tw, th)
		}
		if !r.HitToggle || !r.HitCover {
			t.Errorf("%s: hit test toggle %v cover %v", name, r.HitToggle, r.HitCover)
		}
		if r.Meta != "Chapter 4 · 1 min left" || r.Letter == "" {
			t.Errorf("%s: hidden time text %q, letter %q", name, r.Meta, r.Letter)
		}
	}
}

// TestPhoneBubbleBehaviour: the play button toggles and relabels, the ring follows the position, the one-resume
// rule holds, the cover opens the full player, and reduced motion has no animation.
func TestPhoneBubbleBehaviour(t *testing.T) {
	e := playerApp(t)
	tab := phoneOpen(t, e, 390, 844, false)
	label := `document.querySelector("[data-ts-toggle]").getAttribute("aria-label")`
	var l string
	tab.Eval(label, &l)
	if l != "Play Harbour Lamp" {
		t.Fatalf("play label %q", l)
	}
	playerClickSel(t, tab, "[data-ts-toggle]")
	tab.WaitFor(`window.tsPlayer.state().playing && `+label+` === "Pause Harbour Lamp"`, 6*time.Second)
	playerClickSel(t, tab, "[data-ts-toggle]")
	tab.WaitFor(`!window.tsPlayer.state().playing && `+label+` === "Play Harbour Lamp"`, 5*time.Second)

	// The ring is a conic gradient driven by --ts-progress, which follows the book position.
	for _, frac := range []float64{0.25, 0.75} {
		tab.Eval(fmt.Sprintf(`window.tsPlayer.seek(window.tsPlayer.state().durationMs * %v)`, frac), nil)
		tab.WaitFor(fmt.Sprintf(`Math.abs(parseFloat(document.querySelector("[data-ts-mini-progress]").style.getPropertyValue("--ts-progress")) - %v) < 0.02`, frac), 4*time.Second)
	}
	var m struct{ Bg, Anim, Slot, Ring string }
	tab.Eval(`(() => { const s = getComputedStyle(document.querySelector("[data-ts-mini-progress]"));
	  return {Bg: s.backgroundImage, Anim: getComputedStyle(document.getElementById("mini-player")).animationName,
	    Slot: getComputedStyle(document.getElementById("player")).animationName, Ring: s.transitionDuration}; })()`, &m)
	if !strings.HasPrefix(m.Bg, "conic-gradient") {
		t.Errorf("ring background %q", m.Bg)
	}
	if m.Anim != "none" || m.Slot != "none" || m.Ring != "0s" {
		t.Errorf("reduced motion: %+v", m)
	}

	// Paused with the page's own Resume in view: hidden. Playing: shown.
	e.playerInject(tab, e.book.ID, 0, 0, "")
	tab.Eval(`document.dispatchEvent(new Event("htmx:after:settle"))`, nil)
	tab.WaitFor(`getComputedStyle(document.getElementById("player")).visibility === "hidden"`, 5*time.Second)
	tab.Eval(`window.tsPlayer.play()`, nil)
	tab.WaitFor(`window.tsPlayer.state().playing && getComputedStyle(document.getElementById("player")).visibility === "visible"`, 6*time.Second)
	tab.Eval(`window.tsPlayer.pause(); document.getElementById("play-link").remove(); document.dispatchEvent(new Event("htmx:after:settle"))`, nil)
	tab.WaitFor(`getComputedStyle(document.getElementById("player")).visibility === "visible"`, 5*time.Second)

	// Tapping the cover opens the full player.
	fullpOpenPlayer(t, tab)
}

// TestPhoneBubbleCaptures saves the bubble at 390 px, dark and light: Home while playing, Library paused
// (under .local/tests/player/<run>/bubble-*.png).
func TestPhoneBubbleCaptures(t *testing.T) {
	e := playerApp(t)
	out := playerOut(t)
	tab := phoneOpen(t, e, 390, 844, false)
	shoot := func(page string) {
		for _, theme := range []string{"dark", "light"} {
			tab.Eval(`document.documentElement.setAttribute("data-theme", "`+theme+`")`, nil)
			time.Sleep(200 * time.Millisecond)
			tab.Screenshot(fmt.Sprintf("%s/bubble-%s-%s-390.png", out, page, theme))
		}
	}
	shoot("library-paused")
	playerClickSel(t, tab, "[data-ts-toggle]")
	tab.WaitFor(`window.tsPlayer.state().playing`, 6*time.Second)
	playerClickSel(t, tab, `#nav-tabbar a[href="/"]`)
	tab.WaitFor(`location.pathname === "/" && !!document.querySelector("[data-ts-play]")`, 6*time.Second)
	tab.WaitFor(`getComputedStyle(document.getElementById("player")).visibility === "visible"`, 5*time.Second)
	shoot("home-playing")
}

// TestPhoneBubbleCoverOnEveryPage: the bubble's artwork stays inside its circle on pages that do not
// load the browse stylesheets (operator, 2026-11-03: on Downloads and Profile the cover showed as a
// small square on top of the bubble, because only pages-home.css made .ts-cover a block).
func TestPhoneBubbleCoverOnEveryPage(t *testing.T) {
	e := playerApp(t)
	tab := phoneOpen(t, e, 390, 844, false)
	for _, path := range []string{"/", "/me", "/me/downloads", "/me/settings", "/search"} {
		tab.Navigate(e.URL + path)
		tab.WaitFor(`document.readyState === "complete" && window.tsPlayer && window.tsPlayer.state().bookId !== "" && !document.getElementById("player").hidden`, 10*time.Second)
		var r struct {
			Display          string
			Cover, Open, Img [4]float64
		}
		tab.Eval(`(() => {
		  const box = (el) => { const r = el.getBoundingClientRect(); return [r.left, r.top, r.right, r.bottom]; };
		  const cover = document.querySelector(".ts-mini__cover");
		  return {Display: getComputedStyle(cover).display, Cover: box(cover), Open: box(document.querySelector("[data-ts-open]")), Img: box(cover.querySelector("img"))};
		})()`, &r)
		inside := func(in, out [4]float64) bool {
			return in[0] >= out[0]-0.5 && in[1] >= out[1]-0.5 && in[2] <= out[2]+0.5 && in[3] <= out[3]+0.5
		}
		if r.Display != "block" || !inside(r.Cover, r.Open) || !inside(r.Img, r.Cover) || r.Cover[2]-r.Cover[0] < 40 {
			t.Errorf("%s: cover display %s, cover %v, image %v, bubble button %v; want a block cover filling the bubble with the image inside it", path, r.Display, r.Cover, r.Img, r.Open)
		}
	}
}
