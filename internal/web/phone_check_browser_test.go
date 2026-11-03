//go:build dev

package web

import (
	"fmt"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// Phone-check regressions (the operator's real-phone pass): the bottom tab bar stays at the bottom while the
// page scrolls, the full player fits the visible height without scrolling, and the bottom sheets close by
// dragging them down. Run by make player (TestPhoneCheck*). The tab is a mobile one with touch events, the
// closest the headless browser comes to a phone; the collapsing address bar of a real phone is not
// reproducible here (the viewport does not resize mid-scroll), so the tab bar test checks the cause
// instead: nothing between the bar and the viewport may create a containing block, and the bar must sit at
// the bottom of the viewport on every frame of a touch scroll.

// phoneOpen is fullpOpen with a mobile, touch tab at any size (fullpOpen only marks tabs under 768 px mobile).
func phoneOpen(t *testing.T, e *playerEnv, w, h int, large bool) *cdp.Tab {
	t.Helper()
	b := fullpBrowsers[t.Name()]
	if b == nil {
		b = chromium(t)
		fullpBrowsers[t.Name()] = b
	}
	e.store(t, 100_000, "fullpdevice01", "Other device", 0, false)
	tab := b.NewTab(w, h, true, true)
	if err := tab.Call("Emulation.setTouchEmulationEnabled", map[string]any{"enabled": true, "maxTouchPoints": 5}, nil); err != nil {
		t.Fatal(err)
	}
	if large {
		tab.Navigate(e.URL + "/libraries")
		tab.Eval(`document.documentElement.dataset.textSize = "large"`, nil)
	}
	tab.Navigate(e.URL + "/libraries")
	if large {
		tab.Eval(`document.documentElement.dataset.textSize = "large"`, nil)
	}
	tab.WaitFor(`window.tsPlayer && window.tsPlayer.state().bookId !== "" && !document.getElementById("player").hidden`, 10*time.Second)
	return tab
}

func touchEvent(t *testing.T, tab *cdp.Tab, typ string, x, y float64) {
	t.Helper()
	points := []map[string]any{{"x": x, "y": y}}
	if typ == "touchEnd" {
		points = []map[string]any{}
	}
	if err := tab.Call("Input.dispatchTouchEvent", map[string]any{"type": typ, "touchPoints": points}, nil); err != nil {
		t.Fatal(err)
	}
}

// touchDrag presses at (x, y0) and moves in 12 steps, one frame apart, to y1 before lifting.
func touchDrag(t *testing.T, tab *cdp.Tab, x, y0, y1 float64) {
	t.Helper()
	touchEvent(t, tab, "touchStart", x, y0)
	for i := 1; i <= 12; i++ {
		touchEvent(t, tab, "touchMove", x, y0+(y1-y0)*float64(i)/12)
		time.Sleep(16 * time.Millisecond)
	}
	touchEvent(t, tab, "touchEnd", x, y1)
	time.Sleep(50 * time.Millisecond)
}

// TestPhoneCheckTabBar: on a tall page, scrolling by touch leaves the tab bar and the mini player at the
// bottom of the viewport on every frame, and none of their ancestors makes a containing block for fixed
// positioning (transform, filter, perspective, contain, will-change, backdrop-filter).
func TestPhoneCheckTabBar(t *testing.T) {
	e := playerApp(t)
	tab := phoneOpen(t, e, 390, 844, false)
	tab.Eval(`(() => { const d = document.createElement("div"); d.style.height = "6000px"; document.getElementById("main").append(d); })()`, nil)
	var bad []string
	tab.Eval(`(() => {
	  const out = [];
	  for (const sel of ["#nav-tabbar", ".ts-player-slot"]) {
	    for (let el = document.querySelector(sel).parentElement; el; el = el.parentElement) {
	      const s = getComputedStyle(el);
	      const props = [["transform", s.transform, "none"], ["filter", s.filter, "none"], ["perspective", s.perspective, "none"], ["contain", s.contain, "none"],
	        ["backdropFilter", s.backdropFilter, "none"], ["willChange", s.willChange, "auto"], ["translate", s.translate, "none"], ["rotate", s.rotate, "none"], ["scale", s.scale, "none"]];
	      for (const [n, v, none] of props) if (v !== none && v !== "" && v !== undefined) out.push(sel + " < " + el.tagName + (el.id ? "#" + el.id : "") + " " + n + "=" + v);
	      if (el === document.documentElement) break;
	    }
	  }
	  return out;
	})()`, &bad)
	for _, b := range bad {
		t.Errorf("containing block: %s", b)
	}
	tab.Eval(`window.__frames = []; (function sample() {
	  const bar = document.getElementById("nav-tabbar").getBoundingClientRect();
	  const mini = document.querySelector(".ts-player-slot").getBoundingClientRect();
	  window.__frames.push({ y: scrollY, bar: bar.bottom, mini: mini.bottom, h: window.innerHeight });
	  requestAnimationFrame(sample);
	})()`, nil)
	touchDrag(t, tab, 200, 600, 100)
	touchDrag(t, tab, 200, 600, 100)
	var frames []struct{ Y, Bar, Mini, H float64 }
	tab.Eval(`window.__frames`, &frames)
	moved := false
	for _, f := range frames {
		if f.Y > 0 {
			moved = true
		}
		if f.Bar != f.H {
			t.Fatalf("tab bar bottom %.1f, viewport %.1f at scroll %.0f", f.Bar, f.H, f.Y)
		}
		if f.Mini >= f.H {
			t.Fatalf("mini player bottom %.1f overlaps the tab bar (viewport %.1f)", f.Mini, f.H)
		}
	}
	if !moved || len(frames) < 10 {
		t.Fatalf("the touch scroll did not move the page (%d frames, scrolled=%v)", len(frames), moved)
	}
	// the address bar collapsing is a taller viewport: the bars follow it at once
	if err := tab.Call("Emulation.setDeviceMetricsOverride", map[string]any{"width": 390, "height": 900, "deviceScaleFactor": 1, "mobile": true}, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	var after struct{ Bar, Mini, H float64 }
	tab.Eval(`({ Bar: document.getElementById("nav-tabbar").getBoundingClientRect().bottom, Mini: document.querySelector(".ts-player-slot").getBoundingClientRect().bottom, H: window.innerHeight })`, &after)
	if after.Bar != after.H || after.Mini >= after.H {
		t.Errorf("after the viewport grew: tab bar bottom %.1f, mini player bottom %.1f, viewport %.1f", after.Bar, after.Mini, after.H)
	}
}

// TestPhoneCheckPlayerFits: the full player's content (down to the Speed, Sleep, Chapters and Bookmark bar)
// fits the visible height without scrolling on common phones and in landscape, and with larger text at 390x844.
func TestPhoneCheckPlayerFits(t *testing.T) {
	e := playerApp(t)
	cases := []struct {
		w, h  int
		large bool
	}{{360, 640, false}, {375, 667, false}, {390, 844, false}, {412, 915, false}, {430, 932, false}, {844, 390, false}, {390, 844, true}, {768, 1024, false}, {1024, 768, false}}
	for _, c := range cases {
		name := fmt.Sprintf("%dx%d large=%v", c.w, c.h, c.large)
		tab := phoneOpen(t, e, c.w, c.h, c.large)
		fullpOpenPlayer(t, tab)
		var m struct {
			Scroll, Client, BarBottom, BarTop, Inner, Cover, Transport, Scrub, TitleTop, TitleBottom float64
			Title                                                                                    string
		}
		tab.Eval(`(() => {
		  const body = document.querySelector("[data-ts-full-body]");
		  const r = (s) => document.querySelector("#player-full " + s).getBoundingClientRect();
		  const cover = r(".ts-player__cover");
		  return { Scroll: body.scrollHeight, Client: body.clientHeight, BarBottom: r(".ts-player__bar").bottom, BarTop: r(".ts-player__bar").top,
		    Inner: window.innerHeight, Cover: cover.height, Transport: r(".ts-player__transport").bottom, Scrub: r(".ts-scrub__times").bottom,
		    TitleTop: r(".ts-player__title").top, TitleBottom: r(".ts-player__title").bottom, Title: getComputedStyle(document.querySelector("#player-full .ts-player__title")).display };
		})()`, &m)
		t.Logf("%s: scroll %.0f client %.0f bar %.0f-%.0f inner %.0f cover %.0f title %.0f-%.0f", name, m.Scroll, m.Client, m.BarTop, m.BarBottom, m.Inner, m.Cover, m.TitleTop, m.TitleBottom)
		if m.Scroll != m.Client {
			t.Errorf("%s: the player scrolls (scrollHeight %.0f, clientHeight %.0f)", name, m.Scroll, m.Client)
		}
		if m.BarBottom > m.Inner || m.Scrub > m.BarTop || m.Transport > m.BarTop {
			t.Errorf("%s: settings bar bottom %.0f (viewport %.0f), scrubber times end %.0f, transport end %.0f, bar top %.0f", name, m.BarBottom, m.Inner, m.Scrub, m.Transport, m.BarTop)
		}
		if m.Title == "none" || m.TitleBottom-m.TitleTop < 26 {
			t.Errorf("%s: the title is not shown", name)
		}
	}
}

// TestPhoneCheckSheetDrag: a downward touch drag on the chapters sheet's handle closes it; a short drag
// snaps back; a drag on the list scrolled down scrolls the list; a drag on the list at its top closes it.
func TestPhoneCheckSheetDrag(t *testing.T) {
	e := playerApp(t)
	tab := phoneOpen(t, e, 390, 844, false)
	fullpOpenPlayer(t, tab)
	open := func() {
		t.Helper()
		playerClickSel(t, tab, "[data-ts-open-chapters]")
		tab.WaitFor(`!document.querySelector("[data-ts-chapters-sheet]").hidden`, 3*time.Second)
		time.Sleep(450 * time.Millisecond)
	}
	isOpen := func() bool {
		var v bool
		tab.Eval(`!document.querySelector("[data-ts-chapters-sheet]").hidden`, &v)
		return v
	}
	centre := func(sel string) (float64, float64) {
		var p struct{ X, Y float64 }
		tab.Eval(`(() => { const r = document.querySelector("[data-ts-chapters-sheet] `+sel+`").getBoundingClientRect(); return { X: r.left + r.width / 2, Y: r.top + r.height / 2 }; })()`, &p)
		return p.X, p.Y
	}
	// the root and the sheet's scroller must not chain a pull to the browser's refresh
	var guard struct{ Root, List string }
	tab.Eval(`({ Root: getComputedStyle(document.documentElement).overscrollBehaviorY, List: getComputedStyle(document.querySelector("[data-ts-chapter-list]")).overscrollBehaviorY })`, &guard)
	open()
	tab.Eval(`({ Root: getComputedStyle(document.documentElement).overscrollBehaviorY, List: getComputedStyle(document.querySelector("[data-ts-chapter-list]")).overscrollBehaviorY })`, &guard)
	if guard.Root != "none" || guard.List != "contain" {
		t.Errorf("overscroll-behavior-y with a sheet open: root %q (want none), list %q (want contain)", guard.Root, guard.List)
	}

	x, y := centre(".ts-sheet__grab")
	touchDrag(t, tab, x, y, y+20)
	if !isOpen() {
		t.Fatal("a 20 px drag closed the sheet; it should snap back")
	}
	var off float64
	time.Sleep(400 * time.Millisecond)
	tab.Eval(`document.querySelector("[data-ts-chapters-sheet] .ts-sheet").getBoundingClientRect().bottom - window.innerHeight`, &off)
	if off > 1 {
		t.Errorf("the sheet did not snap back: %.1f px below the viewport bottom", off)
	}
	touchDrag(t, tab, x, y, y+120)
	time.Sleep(500 * time.Millisecond)
	if isOpen() {
		t.Fatal("a 120 px drag on the handle did not close the chapters sheet")
	}

	// the header closes it too
	open()
	hx, hy := centre(".ts-sheet__title")
	touchDrag(t, tab, hx, hy, hy+120)
	time.Sleep(500 * time.Millisecond)
	if isOpen() {
		t.Fatal("a 120 px drag on the header did not close the chapters sheet")
	}

	// the list: make it long enough to scroll, scroll it down, then drag
	open()
	tab.Eval(`(() => { const list = document.querySelector("[data-ts-chapter-list]"); for (let i = 0; i < 20; i++) list.append(list.firstElementChild.cloneNode(true)); })()`, nil)
	lx, ly := centre("[data-ts-chapter-list]")
	var top float64
	touchDrag(t, tab, lx, ly, ly-150) // finger up: the list scrolls down
	tab.Eval(`document.querySelector("[data-ts-chapter-list]").scrollTop`, &top)
	if top <= 0 {
		t.Fatalf("a drag up did not scroll the list (scrollTop %.0f)", top)
	}
	if !isOpen() {
		t.Fatal("scrolling the list closed the sheet")
	}
	touchDrag(t, tab, lx, ly, ly+60) // finger down, list not at its top: it scrolls back, the sheet stays
	if !isOpen() {
		t.Fatal("a drag on a scrolled list closed the sheet")
	}
	tab.Eval(`document.querySelector("[data-ts-chapter-list]").scrollTop = 0`, nil)
	touchDrag(t, tab, lx, ly, ly+150)
	time.Sleep(500 * time.Millisecond)
	if isOpen() {
		t.Fatal("a 150 px drag on the list at its top did not close the sheet")
	}
}

// TestPhoneCheckSavedToastLeaves: the "Note saved." toast the server sends out of band goes away on its own
// (it had no timer: only toasts made in script were dismissed).
func TestPhoneCheckSavedToastLeaves(t *testing.T) {
	e := playerApp(t)
	tab := phoneOpen(t, e, 390, 844, false)
	notekeepOpenSheet(t, tab)
	playerClickSel(t, tab, `#sheet form[action$="/note"] button[type="submit"]`)
	const toast = `[...document.querySelectorAll("#toasts .ts-toast")].filter((t) => t.textContent.includes("Note saved."))`
	tab.WaitFor(toast+`.length === 1`, 5*time.Second)
	var role string
	tab.Eval(toast+`[0].getAttribute("role")`, &role)
	if role != "status" {
		t.Errorf("toast role %q, want status", role)
	}
	time.Sleep(1500 * time.Millisecond)
	tab.WaitFor(toast+`.length === 0`, 8*time.Second)
}
