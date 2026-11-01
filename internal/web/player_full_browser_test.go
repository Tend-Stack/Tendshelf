//go:build dev

package web

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The full player's browser tests (P1c-5a), run by make player. The fixture is the player's: one
// book of two 90 s files, six chapters of 30 s, a cover with a tint and teal accents; the stored
// place is 100 s, in chapter 4.

var fullpBrowsers = map[string]*cdp.Browser{} // one browser per test, shared by its tabs

// fullpOpen opens the home page at w x h with the book loaded and returns the tab.
func fullpOpen(t *testing.T, e *playerEnv, w, h int, reduced bool) *cdp.Tab {
	t.Helper()
	b := fullpBrowsers[t.Name()]
	if b == nil {
		b = chromium(t)
		fullpBrowsers[t.Name()] = b
	}
	e.store(t, 100_000, "fullpdevice01", "Other device", 0, false)
	tab := b.NewTab(w, h, w < 768, reduced)
	tab.Navigate(e.URL + "/")
	tab.WaitFor(`window.tsPlayer && window.tsPlayer.state().bookId !== "" && !document.getElementById("player").hidden`, 10*time.Second)
	return tab
}

const fullpDialog = `document.getElementById("player-full")`

// fullpOpenPlayer clicks the bar's open link and waits for the dialog.
func fullpOpenPlayer(t *testing.T, tab *cdp.Tab) {
	t.Helper()
	playerClickSel(t, tab, "[data-ts-open]")
	tab.WaitFor(fullpDialog+`.open`, 3*time.Second)
	time.Sleep(450 * time.Millisecond) // the view transition
}

func fullpKey(t *testing.T, tab *cdp.Tab, key string, code int) {
	t.Helper()
	for _, typ := range []string{"rawKeyDown", "keyUp"} {
		if err := tab.Call("Input.dispatchKeyEvent", map[string]any{"type": typ, "key": key, "windowsVirtualKeyCode": code}, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func fullpMouse(t *testing.T, tab *cdp.Tab, typ string, x, y float64) {
	t.Helper()
	params := map[string]any{"type": typ, "x": x, "y": y, "button": "left", "clickCount": 1}
	if typ == "mouseMoved" {
		params["buttons"] = 1
	}
	if err := tab.Call("Input.dispatchMouseEvent", params, nil); err != nil {
		t.Fatal(err)
	}
}

// fullpDrag presses at (x0, y0), moves in steps to (x1, y1) and releases.
func fullpDrag(t *testing.T, tab *cdp.Tab, x0, y0, x1, y1 float64) {
	t.Helper()
	fullpMouse(t, tab, "mousePressed", x0, y0)
	for i := 1; i <= 10; i++ {
		f := float64(i) / 10
		fullpMouse(t, tab, "mouseMoved", x0+(x1-x0)*f, y0+(y1-y0)*f)
	}
	fullpMouse(t, tab, "mouseReleased", x1, y1)
}

func fullpPos(tab *cdp.Tab) int64 { return playerStateOf(tab).PositionMs }

func fullpNear(t *testing.T, what string, got, want, tol int64) {
	t.Helper()
	if got < want-tol || got > want+tol {
		t.Errorf("%s = %d ms, want %d ± %d", what, got, want, tol)
	}
}

func TestFullPlayerBrowserOpenClose(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 844, false)
	fullpOpenPlayer(t, tab)
	var s struct {
		Modal, FocusInside, MiniHidden bool
		Path                           string
	}
	read := func() {
		tab.Eval(`(() => { const d = `+fullpDialog+`; return {Modal: d.matches(":modal"), FocusInside: d.contains(document.activeElement),
		  MiniHidden: getComputedStyle(document.getElementById("player")).visibility === "hidden", Path: location.pathname}; })()`, &s)
	}
	read()
	if !s.Modal || !s.FocusInside || !s.MiniHidden || s.Path != "/" {
		t.Errorf("after opening at 390: %+v", s)
	}

	// Escape closes and focus returns to the link that opened it.
	fullpKey(t, tab, "Escape", 27)
	tab.WaitFor(`!`+fullpDialog+`.open`, 3*time.Second)
	var back bool
	tab.WaitFor(`document.activeElement === document.querySelector("[data-ts-open]")`, 2*time.Second)
	time.Sleep(450 * time.Millisecond) // the view transition; clicks wait for it
	tab.Eval(`getComputedStyle(document.getElementById("player")).visibility !== "hidden"`, &back)
	if !back {
		t.Error("the bar is still hidden after closing")
	}

	// Swipe down on the header: 20 % of the screen springs back, 50 % closes.
	fullpOpenPlayer(t, tab)
	var head struct{ X, Y float64 }
	tab.Eval(`(() => { const r = document.querySelector("#player-full .ts-player__head").getBoundingClientRect(); return {X: r.x + r.width / 2, Y: r.y + r.height / 2}; })()`, &head)
	fullpDrag(t, tab, head.X, head.Y, head.X, head.Y+0.2*844)
	time.Sleep(500 * time.Millisecond)
	var open bool
	var drag string
	tab.Eval(fullpDialog+`.open`, &open)
	tab.Eval(`getComputedStyle(`+fullpDialog+`).getPropertyValue("--ts-drag").trim()`, &drag)
	if !open || (drag != "0px" && drag != "") {
		t.Errorf("a 20 %% swipe: open %v, drag %q; it should snap back", open, drag)
	}
	fullpDrag(t, tab, head.X, head.Y, head.X, head.Y+0.5*844)
	tab.WaitFor(`!`+fullpDialog+`.open`, 3*time.Second)

	// From 1280 px the player is a non-modal side panel and the page under it keeps working.
	wide := fullpOpen(t, e, 1440, 900, false)
	fullpOpenPlayer(t, wide)
	var p struct {
		Modal  bool
		Left   float64
		Width  float64
		Bar    bool
		PageOK string
	}
	wide.Eval(`(() => { const d = `+fullpDialog+`; const r = d.getBoundingClientRect();
	  return {Modal: d.matches(":modal"), Left: r.left, Width: r.width, Bar: getComputedStyle(document.getElementById("player")).visibility !== "hidden", PageOK: ""}; })()`, &p)
	if p.Modal || p.Width != 400 || p.Left+p.Width < 1424 || !p.Bar {
		t.Errorf("side panel at 1440: %+v", p)
	}
	playerClickSel(t, wide, `.ts-nav-side a[href="/search"]`)
	wide.WaitFor(`location.pathname === "/search"`, 5*time.Second)
	var still bool
	wide.Eval(fullpDialog+`.open && window.tsPlayer.state().bookId !== ""`, &still)
	if !still {
		t.Error("the side panel closed on navigation")
	}
}

func TestFullPlayerBrowserAccent(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 844, false)
	var r struct{ Dark, Tint, MiniAccent, RootInline string }
	read := func(theme string) {
		tab.Eval(`document.documentElement.setAttribute("data-theme", `+cdp.Quote(theme)+`)`, nil)
		time.Sleep(200 * time.Millisecond)
		tab.Eval(`(() => { const d = `+fullpDialog+`; return {Dark: d.style.getPropertyValue("--cover-accent"), Tint: d.style.getPropertyValue("--cover-tint"),
		  MiniAccent: getComputedStyle(document.getElementById("player")).getPropertyValue("--cover-accent").trim(),
		  RootInline: document.documentElement.style.getPropertyValue("--cover-accent")}; })()`, &r)
	}
	read("dark")
	if r.Dark != "#9ccbc4" || r.Tint != "#1f4a52" {
		t.Errorf("dark: accent %q tint %q", r.Dark, r.Tint)
	}
	if r.MiniAccent == "#9ccbc4" || r.RootInline != "" {
		t.Errorf("the accent leaked out of the full player: mini %q, root %q", r.MiniAccent, r.RootInline)
	}
	// the mini-player's play button stays ember
	var ember bool
	tab.Eval(`(() => { const probe = document.createElement("span"); probe.className = "ts-iconbtn ts-iconbtn--filled"; document.body.append(probe);
	  const want = getComputedStyle(probe).backgroundColor; probe.remove(); return getComputedStyle(document.querySelector("[data-ts-toggle]")).backgroundColor === want; })()`, &ember)
	if !ember {
		t.Error("the mini-player play button is not ember")
	}
	read("light")
	if r.Dark != "#2c6660" {
		t.Errorf("light accent %q", r.Dark)
	}
	read("contrast")
	if r.Dark != "" || r.Tint != "" {
		t.Errorf("contrast sets %q / %q, want nothing", r.Dark, r.Tint)
	}
}

func TestFullPlayerBrowserScrubber(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 844, false)
	fullpOpenPlayer(t, tab)
	var ticks int
	tab.Eval(`document.querySelectorAll("#player-full .ts-scrub__tick").length`, &ticks)
	if ticks != 5 {
		t.Errorf("%d ticks, want 5 (six chapters)", ticks)
	}
	tab.Eval(`document.querySelector("#player-full [data-ts-range]").focus()`, nil)
	fullpNear(t, "start", fullpPos(tab), 100_000, 400)
	fullpKey(t, tab, "ArrowRight", 39)
	tab.WaitFor(`window.tsPlayer.state().positionMs > 125000`, 2*time.Second)
	fullpNear(t, "after ArrowRight", fullpPos(tab), 130_000, 400)
	fullpKey(t, tab, "ArrowLeft", 37)
	tab.WaitFor(`window.tsPlayer.state().positionMs < 120000`, 2*time.Second)
	fullpNear(t, "after ArrowLeft", fullpPos(tab), 115_000, 400)
	fullpKey(t, tab, "PageDown", 34)
	tab.WaitFor(`window.tsPlayer.state().positionMs >= 119000`, 2*time.Second)
	fullpNear(t, "after PageDown", fullpPos(tab), 120_000, 400)
	fullpKey(t, tab, "End", 35)
	tab.WaitFor(`window.tsPlayer.state().positionMs > 170000`, 2*time.Second)
	fullpNear(t, "after End", fullpPos(tab), 179_999, 1500)

	// A drag from the middle to 75 % of the ruler seeks there on release.
	var box struct{ L, W, Y float64 }
	tab.Eval(`(() => { const r = document.querySelector("#player-full [data-ts-range]").getBoundingClientRect(); return {L: r.left, W: r.width, Y: r.top + r.height / 2}; })()`, &box)
	fullpMouse(t, tab, "mousePressed", box.L+0.5*box.W, box.Y)
	for i := 1; i <= 5; i++ {
		fullpMouse(t, tab, "mouseMoved", box.L+(0.5+0.05*float64(i))*box.W, box.Y)
	}
	var mid struct {
		Dragging bool
		Bubble   string
		Fine     string
	}
	tab.Eval(`({Dragging: !!document.querySelector("#player-full .ts-scrub.is-dragging"), Bubble: document.querySelector("#player-full [data-ts-bubble]").textContent,
	  Fine: document.querySelector("#player-full [data-ts-full-fine]").textContent})`, &mid)
	if !mid.Dragging || !strings.HasPrefix(mid.Bubble, "Chapter 5 · Chapter 5 · ") || mid.Fine != "Drag up for fine control" {
		t.Errorf("while dragging: %+v", mid)
	}
	fullpMouse(t, tab, "mouseReleased", box.L+0.75*box.W, box.Y)
	tab.WaitFor(`window.tsPlayer.state().positionMs > 130000 && window.tsPlayer.state().positionMs < 140000`, 2*time.Second)
	fullpNear(t, "after the drag", fullpPos(tab), 135_000, 1000)

	// Fine control: the same horizontal travel 100 px above the bar moves a quarter as far.
	tab.Eval(`window.tsPlayer.seek(30000)`, nil)
	time.Sleep(200 * time.Millisecond)
	fullpMouse(t, tab, "mousePressed", box.L+0.2*box.W, box.Y)
	fullpMouse(t, tab, "mouseMoved", box.L+0.2*box.W, box.Y-100) // first away from the bar, then along it
	for i := 1; i <= 10; i++ {
		fullpMouse(t, tab, "mouseMoved", box.L+(0.2+0.03*float64(i))*box.W, box.Y-100)
	}
	fullpMouse(t, tab, "mouseReleased", box.L+0.5*box.W, box.Y-100)
	tab.WaitFor(`window.tsPlayer.state().positionMs > 30000`, 2*time.Second)
	// pressed at 20 % (36 s), moved 30 % of the width at a quarter rate: 36 s + 0.3 * 180 s / 4 = 49.5 s
	fullpNear(t, "after the fine drag", fullpPos(tab), 49_500, 1500)

	// aria-valuetext changes at most once per 10 s while playing.
	tab.Eval(`window.tsPlayer.seek(10000); window.__vt = 0; new MutationObserver((l) => { window.__vt += l.length; }).observe(
	  document.querySelector("#player-full [data-ts-range]"), {attributes: true, attributeFilter: ["aria-valuetext"]})`, nil)
	time.Sleep(200 * time.Millisecond)
	playerClickSel(t, tab, "[data-ts-full-toggle]")
	tab.WaitFor(`!document.querySelector("audio").paused`, 3*time.Second)
	time.Sleep(4500 * time.Millisecond)
	var changes int
	tab.Eval(`window.__vt`, &changes)
	if changes > 1 {
		t.Errorf("aria-valuetext was set %d times in 4.5 s of playing", changes)
	}
	var text string
	tab.Eval(`document.querySelector("#player-full [data-ts-range]").getAttribute("aria-valuetext")`, &text)
	if !strings.HasPrefix(text, "0 hours 0 minutes elapsed, chapter ") {
		t.Errorf("aria-valuetext %q", text)
	}
}

func TestFullPlayerBrowserTimesAndChapters(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 844, false)
	fullpOpenPlayer(t, tab)
	texts := func() map[string]string {
		var m map[string]string
		tab.Eval(`Object.fromEntries(["chapter", "clock", "left", "elapsed", "book-left", "speed"].map((n) => [n, document.querySelector("#player-full [data-ts-full-" + n + "]").textContent]))`, &m)
		return m
	}
	// 100 s in: chapter 4 runs 90 s to 120 s; 80 s of the book are left.
	got := texts()
	want := map[string]string{"chapter": "Chapter 4 of 6", "clock": "0:10", "left": "0:20 left in chapter", "elapsed": "1:40", "book-left": "0 h 2 m left in book", "speed": "1.0×"}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("speed 1: %s = %q, want %q", k, got[k], v)
		}
	}
	// At 2x the book's time left halves: (180 s - 100 s) / 2 = 40 s, shown as 1 m.
	tab.Eval(`window.tsPlayer.setSpeed(2)`, nil)
	time.Sleep(300 * time.Millisecond)
	got = texts()
	if got["book-left"] != "0 h 1 m left in book" || got["speed"] != "2.0×" || got["left"] != "0:20 left in chapter" {
		t.Errorf("speed 2: %+v", got)
	}
	tab.Eval(`window.tsPlayer.setSpeed(1)`, nil)

	// The chapters sheet marks where you are and what is played.
	tab.Eval(`window.tsPlayer.seek(10000)`, nil)
	time.Sleep(200 * time.Millisecond)
	playerClickSel(t, tab, "[data-ts-open-chapters]")
	tab.WaitFor(`!document.querySelector("#player-full [data-ts-chapters-sheet]").hidden`, 2*time.Second)
	var rows struct {
		N       int
		Current []string
		Played  []string
		Inert   bool
	}
	read := func() {
		tab.Eval(`(() => { const rows = [...document.querySelectorAll("#player-full [data-ts-chapter-list] button")];
		  return {N: rows.length, Current: rows.filter((b) => b.getAttribute("aria-current") === "true").map((b) => b.getAttribute("aria-label")),
		  Played: rows.filter((b) => !b.querySelector("[hidden]") && b.getAttribute("aria-label").endsWith(", played")).map((b) => b.getAttribute("aria-label")),
		  Inert: document.querySelector("#player-full [data-ts-full-body]").inert}; })()`, &rows)
	}
	read()
	if rows.N != 6 || len(rows.Current) != 1 || rows.Current[0] != "Chapter 1" || len(rows.Played) != 0 || !rows.Inert {
		t.Errorf("sheet at 10 s: %+v", rows)
	}
	// Escape closes the sheet, not the player, and focus goes back to the Chapters item.
	fullpKey(t, tab, "Escape", 27)
	tab.WaitFor(`document.querySelector("#player-full [data-ts-chapters-sheet]").hidden`, 2*time.Second)
	var kept bool
	tab.Eval(fullpDialog+`.open && document.activeElement === document.querySelector("[data-ts-open-chapters]")`, &kept)
	if !kept {
		t.Error("Escape in the sheet closed the player or lost focus")
	}
	// At chapter 5 the first four are played; picking chapter 4 starts it.
	tab.Eval(`window.tsPlayer.seek(130000)`, nil)
	time.Sleep(200 * time.Millisecond)
	playerClickSel(t, tab, "[data-ts-open-chapters]")
	tab.WaitFor(`!document.querySelector("#player-full [data-ts-chapters-sheet]").hidden`, 2*time.Second)
	read()
	if len(rows.Current) != 1 || rows.Current[0] != "Chapter 5" || len(rows.Played) != 4 || rows.Played[3] != "Chapter 4, played" {
		t.Errorf("sheet at 130 s: %+v", rows)
	}
	playerClickSel(t, tab, `#player-full [data-ts-chapter-list] li:nth-child(4) button`)
	tab.WaitFor(`window.tsPlayer.state().positionMs >= 89500 && window.tsPlayer.state().positionMs < 95000`, 3*time.Second)
	var after struct {
		Hidden, Playing bool
	}
	tab.Eval(`({Hidden: document.querySelector("#player-full [data-ts-chapters-sheet]").hidden, Playing: window.tsPlayer.state().playing})`, &after)
	if !after.Hidden || !after.Playing {
		t.Errorf("after picking chapter 4: %+v", after)
	}
}

func TestFullPlayerBrowserLayout(t *testing.T) {
	e := playerApp(t)
	out := playerOut(t)
	themesToCheck := themes
	if !visualFull() {
		themesToCheck = []string{"dark", "light"}
	}
	for _, size := range [][2]int{{390, 844}, {768, 1024}, {1024, 768}, {1440, 900}} {
		w, h := size[0], size[1]
		tab := fullpOpen(t, e, w, h, true)
		fullpOpenPlayer(t, tab)
		for _, theme := range themesToCheck {
			tab.Eval(`document.documentElement.setAttribute("data-theme", `+cdp.Quote(theme)+`)`, nil)
			time.Sleep(200 * time.Millisecond)
			var r struct {
				Over   bool
				Small  []string
				Low    []string
				Title  string
				Toggle string
			}
			tab.Eval(`(() => {
			  const d = `+fullpDialog+`, small = [], low = [];
			  const box = (el) => el.getBoundingClientRect();
			  for (const el of d.querySelectorAll("button, a, summary, input")) {
			    if (el.type === "hidden" || !el.offsetParent || el.closest("[hidden]") || el.closest(".ts-player__menubody")) continue;
			    const r = box(el);
			    if (r.width < 43.5 || r.height < 43.5) small.push(el.className + ":" + Math.round(r.width) + "x" + Math.round(r.height));
			  }
			  if (innerWidth < 768) {
			    // one-handed: transport, scrubber and settings sit in the bottom 60 %
			    for (const sel of ["[data-ts-full-toggle]", "[data-ts-full-back]", "[data-ts-full-forward]", "[data-ts-full-prev]", "[data-ts-full-next]",
			      ".ts-scrub__input", "[data-ts-open-speed]", "[data-ts-open-chapters]"]) {
			      const el = [...d.querySelectorAll(sel)].find((x) => x.offsetParent);
			      if (box(el).top < innerHeight * 0.4) low.push(sel);
			    }
			  }
			  return {Over: document.documentElement.scrollWidth > innerWidth || d.scrollWidth > d.clientWidth, Small: small, Low: low,
			    Title: d.querySelector("[data-ts-full-title]").textContent, Toggle: d.querySelector("[data-ts-full-toggle]").getAttribute("aria-label")};
			})()`, &r)
			name := fmt.Sprintf("%s/full-%s-%d", out, theme, w)
			if r.Over {
				t.Errorf("%s: horizontal overflow", name)
			}
			if len(r.Small) > 0 {
				t.Errorf("%s: controls under 44 px: %v", name, r.Small)
			}
			if len(r.Low) > 0 {
				t.Errorf("%s: controls in the top 40%% of the phone: %v", name, r.Low)
			}
			if r.Title != "Harbour Lamp" || r.Toggle != "Play Harbour Lamp" {
				t.Errorf("%s: title %q, toggle label %q", name, r.Title, r.Toggle)
			}
			if theme == "dark" || theme == "light" || visualFull() {
				tab.Screenshot(name + ".png")
			}
		}
	}
}
