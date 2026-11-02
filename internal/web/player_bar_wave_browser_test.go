//go:build dev

package web

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The desktop bar's waveform (PB2, docs/design/tendshelf-player-bar): bars, hover, click, drag, keys,
// words for a screen reader, loading, the book hairline, themes and navigation. The fixture is the
// player's: one book of six 30 s chapters in two 90 s files; the stored position is 100 s (chapter 4,
// file 2). make player runs these.

const pbwWave = `.ts-wave--bar`

// pbwOpen is fullpOpen with an evaluate-on-new-document script that shortens the peaks retry waits.
func pbwOpen(t *testing.T, e *playerEnv, w, h int, reduced bool, retryScale float64) *cdp.Tab {
	t.Helper()
	b := fullpBrowsers[t.Name()]
	if b == nil {
		b = chromium(t)
		fullpBrowsers[t.Name()] = b
	}
	e.store(t, 100_000, "fullpdevice01", "Other device", 0, false)
	tab := b.NewTab(w, h, false, reduced)
	if retryScale > 0 {
		src := fmt.Sprintf(`window.tsWave = { debug: { retryScale: %g } }`, retryScale)
		if err := tab.Call("Page.enable", map[string]any{}, nil); err != nil {
			t.Fatal(err)
		}
		if err := tab.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": src}, nil); err != nil {
			t.Fatal(err)
		}
	}
	tab.Navigate(e.URL + "/libraries")
	tab.WaitFor(`window.tsPlayer && window.tsPlayer.state().bookId !== "" && !document.getElementById("player").hidden`, 10*time.Second)
	return tab
}

// pbwWithPeaks stores peaks for both files.
func pbwWithPeaks(t *testing.T, e *playerEnv) {
	t.Helper()
	peaksAttach(t, e.a)
	wavedrawWrite(t, e, e.files[0])
	wavedrawWrite(t, e, e.files[1])
}

// pbwMove moves the mouse (no button) to x, y.
func pbwMove(t *testing.T, tab *cdp.Tab, x, y float64) {
	t.Helper()
	if err := tab.Call("Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": x, "y": y}, nil); err != nil {
		t.Fatal(err)
	}
}

// pbwSeek pauses at ms (inside the fixture) and waits for the wave to show it.
func pbwSeek(t *testing.T, tab *cdp.Tab, ms int64) {
	t.Helper()
	tab.Eval(`window.tsPlayer.pause()`, nil)
	wavedrawSeek(t, tab, ms)
}

type pbwBars struct {
	N, Layers int
	Pitch     []float64
	Stroke    string
	Width     float64
	Heights   []float64
	Loading   bool
	Animation string
}

func pbwRead(tab *cdp.Tab) pbwBars {
	var r pbwBars
	tab.Eval(`(() => {
	  const w = document.querySelector(".ts-wave--bar");
	  const rest = w.querySelector(".ts-wave__rest");
	  const d = rest ? rest.querySelector("path").getAttribute("d") : "";
	  const xs = [...d.matchAll(/M([\d.]+) ([\d.]+)V([\d.]+)/g)];
	  return {N: xs.length, Layers: w.querySelectorAll(":scope > svg").length,
	    Pitch: xs.slice(1).map((m, i) => +m[1] - +xs[i][1]),
	    Stroke: rest ? getComputedStyle(rest.querySelector("path")).strokeWidth : "",
	    Width: w.clientWidth, Heights: xs.map((m) => +m[3] - +m[2]),
	    Loading: w.classList.contains("ts-wave--loading"),
	    Animation: rest ? getComputedStyle(rest).animationName : ""};
	})()`, &r)
	return r
}

// TestPlayerBarBrowserWaveBars: floor(width / 4) bars, 2 px wide every 4 px at every desktop width and
// after a resize, never stretched; the quietest tenth of the chapter is drawn at 20 % of the height.
func TestPlayerBarBrowserWaveBars(t *testing.T) {
	e := playerApp(t)
	pbwWithPeaks(t, e)
	for _, w := range []int{1280, 1440, 1920, 2560} {
		tab := pbwOpen(t, e, w, 900, true, 0)
		pbwSeek(t, tab, 10_000)
		tab.WaitFor(`document.querySelectorAll(".ts-wave--bar > svg").length === 4 && !document.querySelector(".ts-wave--bar").classList.contains("ts-wave--loading")`, 5*time.Second)
		check := func(what string) {
			t.Helper()
			r := pbwRead(tab)
			if want := int(r.Width) / 4; r.N != want || r.Layers != 4 || r.Stroke != "2px" {
				t.Errorf("%s: %d bars of %s in %d layers on %.0f px, want %d", what, r.N, r.Stroke, r.Layers, r.Width, int(r.Width)/4)
			}
			for _, p := range r.Pitch {
				if p != 4 {
					t.Errorf("%s: bar pitch %v, want 4", what, p)
					break
				}
			}
			min, max := 1e9, 0.0
			for _, h := range r.Heights {
				if h < min {
					min = h
				}
				if h > max {
					max = h
				}
			}
			// bar height h (path length) is h * (36 - 4) for the chapter-relative value; the quietest sits at 20 %.
			if max < 28 || min > 0.35*32+1 {
				t.Errorf("%s: heights %.1f..%.1f px, want the quietest tenth near 20 %% and the loudest full", what, min, max)
			}
		}
		check(fmt.Sprintf("%dpx", w))
		// A resize redraws without stretching.
		if err := tab.Call("Emulation.setDeviceMetricsOverride", map[string]any{"width": w + 160, "height": 900, "deviceScaleFactor": 1, "mobile": false}, nil); err != nil {
			t.Fatal(err)
		}
		tab.WaitFor(fmt.Sprintf(`document.querySelector(".ts-wave--bar").querySelector(".ts-wave__rest path").getAttribute("d").split("M").length - 1 === Math.floor(document.querySelector(".ts-wave--bar").clientWidth / 4)`), 3*time.Second)
		time.Sleep(200 * time.Millisecond)
		check(fmt.Sprintf("%dpx resized to %d", w, w+160))
	}
}

// TestPlayerBarBrowserWaveThemes: the layers take their colours from the tokens in all four themes.
func TestPlayerBarBrowserWaveThemes(t *testing.T) {
	e := playerApp(t)
	pbwWithPeaks(t, e)
	tab := pbwOpen(t, e, 1440, 900, true, 0)
	pbwSeek(t, tab, 10_000)
	tab.WaitFor(`document.querySelectorAll(".ts-wave--bar > svg").length === 4`, 5*time.Second)
	for _, theme := range []string{"dark", "light", "sepia", "contrast"} {
		tab.Eval(`document.documentElement.setAttribute("data-theme", `+cdp.Quote(theme)+`)`, nil)
		time.Sleep(150 * time.Millisecond)
		var got struct{ Rest, Played, Ahead, WantRest, WantPlayed, WantAhead string }
		tab.Eval(`(() => {
		  const token = (name) => { const s = document.createElement("span"); s.style.color = "var(" + name + ")"; document.body.append(s); const c = getComputedStyle(s).color; s.remove(); return c; };
		  const stroke = (cls) => getComputedStyle(document.querySelector(".ts-wave--bar ." + cls + " path")).stroke;
		  return {Rest: stroke("ts-wave__rest"), Played: stroke("ts-wave__played"), Ahead: stroke("ts-wave__ahead"),
		    WantRest: token("--wave-rest"), WantPlayed: token("--wave-played"), WantAhead: token("--wave-ahead")};
		})()`, &got)
		if got.Rest != got.WantRest || got.Played != got.WantPlayed || got.Ahead != got.WantAhead || got.Rest == got.Played {
			t.Errorf("%s: %+v", theme, got)
		}
	}
}

var (
	pbwAhead = regexp.MustCompile(`^\d+:\d\d \d+( min \d+)? ?s? ahead$`)
	pbwBack  = regexp.MustCompile(`^\d+:\d\d \d+( min \d+)? ?s? back$`)
)

// TestPlayerBarBrowserWaveHover: hovering previews ahead and behind with the tooltip, the hairline
// shows the chapter and the share of the book, and a click jumps.
func TestPlayerBarBrowserWaveHover(t *testing.T) {
	e := playerApp(t)
	pbwWithPeaks(t, e)
	for _, w := range []int{1280, 1920} {
		tab := pbwOpen(t, e, w, 900, true, 0)
		pbwSeek(t, tab, 10_000)
		tab.WaitFor(`document.querySelectorAll(".ts-wave--bar > svg").length === 4`, 5*time.Second)
		box := barBox(tab, pbwWave)
		y := box.T + box.H/2
		tipText := func() string {
			var s string
			tab.Eval(`(() => { const t = document.querySelector(".ts-wave__tip"); return t.querySelector("[data-ts-tip-time]").textContent + " " + t.querySelector("[data-ts-tip-rel]").textContent; })()`, &s)
			return s
		}
		pbwMove(t, tab, box.L+0.75*box.W, y)
		time.Sleep(300 * time.Millisecond)
		var s struct {
			Hover         bool
			Ahead, Behind float64
			Tip, Line, H  string
		}
		read := func() {
			tab.Eval(`(() => { const w = document.querySelector(".ts-wave--bar");
			  // Each layer shows the stretch between the needle and the pointer (--ts-pos, --ts-hover) once hovered.
			  const pos = parseFloat(w.style.getPropertyValue("--ts-pos")), hover = parseFloat(w.style.getPropertyValue("--ts-hover"));
			  const shown = (c) => { const e = getComputedStyle(w.querySelector(c)); const d = c.includes("ahead") ? hover - pos : pos - hover; return e.opacity === "1" && e.clipPath !== "none" ? Math.max(0, d) * w.clientWidth : 0; };
			  return {Hover: w.classList.contains("is-hover"), Ahead: shown(".ts-wave__ahead"), Behind: shown(".ts-wave__behind"),
			    Tip: getComputedStyle(w.querySelector(".ts-wave__tip")).opacity, Line: getComputedStyle(w.querySelector(".ts-wave__hoverline")).opacity,
			    H: w.style.getPropertyValue("--ts-hover")}; })()`, &s)
		}
		read()
		if text := tipText(); !s.Hover || s.Ahead < 100 || s.Behind != 0 || s.Tip != "1" || s.Line != "1" || !pbwAhead.MatchString(text) || !strings.HasPrefix(s.H, "0.7") {
			t.Errorf("%dpx hover ahead: %+v tip %q", w, s, tipText())
		}
		pbwMove(t, tab, box.L+0.05*box.W, y)
		time.Sleep(300 * time.Millisecond)
		read()
		if text := tipText(); s.Ahead != 0 || s.Behind < 10 || !pbwBack.MatchString(text) {
			t.Errorf("%dpx hover behind: %+v tip %q", w, s, tipText())
		}
		pbwMove(t, tab, 40, 300)
		time.Sleep(300 * time.Millisecond)
		read()
		if s.Hover || s.Tip != "0" {
			t.Errorf("%dpx after leaving: %+v", w, s)
		}

		// The book hairline: gaps at each chapter start, a tip with the chapter and the share of the book.
		var h struct {
			Gaps  int
			At    []string
			Tip   string
			Attrs int
		}
		tab.Eval(`(() => { const b = document.querySelector("[data-ts-bar-book]"); const gaps = [...b.querySelectorAll(":scope > i")];
		  return {Gaps: gaps.length, At: gaps.map((g) => g.style.getPropertyValue("--at")), Tip: "", Attrs: document.querySelectorAll("#player [style]:not(i):not(.ts-wave)").length}; })()`, &h)
		wantAt := []string{"16.67%", "33.33%", "50.00%", "66.67%", "83.33%"}
		if h.Gaps != 5 || strings.Join(h.At, " ") != strings.Join(wantAt, " ") {
			t.Errorf("%dpx hairline gaps %d at %v, want %v", w, h.Gaps, h.At, wantAt)
		}
		book := barBox(tab, "[data-ts-bar-book]")
		pbwMove(t, tab, book.L+0.62*book.W, book.T+1)
		time.Sleep(300 * time.Millisecond)
		tab.Eval(`document.querySelector("[data-ts-bar-booktip]").textContent`, &h.Tip)
		// The test browser reports no hovering pointer, and motion.css then keeps the tip hidden (a touch
		// screen keeps :hover after a tap); on a desktop it shows. So the CSS opacity is checked only there.
		var tipOpacity string
		tab.Eval(`matchMedia("(hover: hover)").matches ? getComputedStyle(document.querySelector("[data-ts-bar-booktip]")).opacity : "1"`, &tipOpacity)
		if h.Tip != "Chapter 4 of 6 · 62% of the book" || tipOpacity != "1" {
			t.Errorf("%dpx book tip %q (opacity %s)", w, h.Tip, tipOpacity)
		}
		// The gaps sit on the chapter starts of the hairline.
		var gapX []float64
		tab.Eval(`[...document.querySelectorAll("[data-ts-bar-book] > i")].map((g) => g.getBoundingClientRect().left)`, &gapX)
		for i, x := range gapX {
			barNear(t, fmt.Sprintf("%dpx gap %d", w, i), (x-book.L)/book.W, float64(i+1)/6, 0.01)
		}

		// A click jumps there: 50 % of chapter 1 is 15 s.
		pbwMove(t, tab, box.L+0.5*box.W, y)
		fullpMouse(t, tab, "mousePressed", box.L+0.5*box.W, y)
		fullpMouse(t, tab, "mouseReleased", box.L+0.5*box.W, y)
		tab.WaitFor(`Math.abs(window.tsPlayer.state().positionMs - 15000) < 400`, 2*time.Second)
	}
}

// TestPlayerBarBrowserWaveDrag: the needle and the times follow the pointer, the tip reads "{t} · chapter
// {n}", the audio jumps on release, dragging off the wave keeps working, and a cancelled drag does not seek.
func TestPlayerBarBrowserWaveDrag(t *testing.T) {
	e := playerApp(t)
	pbwWithPeaks(t, e)
	tab := pbwOpen(t, e, 1440, 900, true, 0)
	pbwSeek(t, tab, 10_000)
	tab.WaitFor(`document.querySelectorAll(".ts-wave--bar > svg").length === 4`, 5*time.Second)
	box := barBox(tab, pbwWave)
	y := box.T + box.H/2
	state := func() (s struct {
		Dragging bool
		Pos      string
		Tip, Rel string
		Elapsed  string
	}) {
		tab.Eval(`(() => { const w = document.querySelector(".ts-wave--bar");
		  return {Dragging: w.classList.contains("is-dragging"), Pos: w.style.getPropertyValue("--ts-pos"),
		    Tip: w.querySelector("[data-ts-tip-time]").textContent, Rel: w.querySelector("[data-ts-tip-rel]").textContent,
		    Elapsed: document.querySelector('[data-pbar="elapsed"]').textContent}; })()`, &s)
		return s
	}
	fullpMouse(t, tab, "mousePressed", box.L+0.2*box.W, y)
	fullpMouse(t, tab, "mouseMoved", box.L+0.6*box.W, y)
	s := state()
	if !s.Dragging || !strings.HasPrefix(s.Pos, "0.6") || s.Rel != "chapter 1" || (s.Tip != "0:18" && s.Tip != "0:17") || s.Elapsed != s.Tip {
		t.Errorf("dragging: %+v", s)
	}
	if p := fullpPos(tab); p < 9000 || p > 11000 {
		t.Errorf("the audio moved to %d ms before the release", p)
	}
	// Off the wave, far below it and past the right edge of the column: the drag follows.
	fullpMouse(t, tab, "mouseMoved", box.L+0.8*box.W, y+300)
	if s = state(); !s.Dragging || !strings.HasPrefix(s.Pos, "0.8") {
		t.Errorf("dragging off the wave: %+v", s)
	}
	fullpMouse(t, tab, "mouseReleased", box.L+0.8*box.W, y+300)
	tab.WaitFor(`Math.abs(window.tsPlayer.state().positionMs - 24000) < 500`, 2*time.Second)
	if s = state(); s.Dragging {
		t.Errorf("still dragging after the release: %+v", s)
	}

	// A cancelled drag restores the state and does not seek.
	before := fullpPos(tab)
	fullpMouse(t, tab, "mousePressed", box.L+0.1*box.W, y)
	fullpMouse(t, tab, "mouseMoved", box.L+0.3*box.W, y)
	tab.Eval(`document.querySelector(".ts-wave--bar").dispatchEvent(new PointerEvent("pointercancel", {bubbles: true}))`, nil)
	time.Sleep(300 * time.Millisecond)
	if s = state(); s.Dragging {
		t.Errorf("still dragging after a cancel: %+v", s)
	}
	if p := fullpPos(tab); p < before-500 || p > before+500 {
		t.Errorf("a cancelled drag moved the audio from %d to %d", before, p)
	}
	fullpMouse(t, tab, "mouseReleased", box.L+0.3*box.W, y)
}

// TestPlayerBarBrowserWaveKeys: Tab reaches the wave and shows the focus ring and the knob; the six keys
// move by 5 s, 30 s and to the chapter's ends; the real slider carries the words for a screen reader.
func TestPlayerBarBrowserWaveKeys(t *testing.T) {
	e := playerApp(t)
	pbwWithPeaks(t, e)
	tab := pbwOpen(t, e, 1440, 900, true, 0)
	pbwSeek(t, tab, 10_000)
	tab.WaitFor(`document.querySelectorAll(".ts-wave--bar > svg").length === 4`, 5*time.Second)
	tab.Eval(`document.querySelector("[data-ts-forward]").focus()`, nil)
	fullpKey(t, tab, "Tab", 9)
	time.Sleep(300 * time.Millisecond)
	var f struct {
		OnWave   bool
		Knob     string
		Ring     string
		Label    string
		Text     string
		Max, Val string
	}
	tab.Eval(`(() => { const i = document.querySelector(".ts-wave__input"); const w = document.querySelector(".ts-wave--bar");
	  return {OnWave: document.activeElement === i, Knob: getComputedStyle(w.querySelector(".ts-wave__needle"), "::after").opacity,
	    Ring: getComputedStyle(w).boxShadow, Label: i.getAttribute("aria-label"), Text: i.getAttribute("aria-valuetext"), Max: i.max, Val: i.value}; })()`, &f)
	if !f.OnWave || f.Knob != "1" || f.Ring == "none" || f.Label != "Position in chapter 1" || f.Max != "30" || f.Val != "10" {
		t.Errorf("after Tab: %+v", f)
	}
	if want := "10 seconds of 30 seconds, chapter 1, Chapter 1"; f.Text != want {
		t.Errorf("aria-valuetext %q, want %q", f.Text, want)
	}
	steps := []struct {
		key  string
		code int
		want int64
	}{
		{"ArrowRight", 39, 15_000}, {"ArrowLeft", 37, 10_000}, {"ArrowUp", 38, 15_000}, {"ArrowDown", 40, 10_000},
		{"PageUp", 33, 29_000}, {"PageDown", 34, 0}, {"End", 35, 29_000}, {"Home", 36, 0},
	}
	for _, s := range steps {
		fullpKey(t, tab, s.key, s.code)
		time.Sleep(250 * time.Millisecond)
		fullpNear(t, s.key, fullpPos(tab), s.want, 300)
	}
	// The slider's words follow the key at once, then hold for up to 10 s while the clock ticks.
	fullpKey(t, tab, "ArrowRight", 39)
	time.Sleep(250 * time.Millisecond)
	var text string
	tab.Eval(`document.querySelector(".ts-wave__input").getAttribute("aria-valuetext")`, &text)
	if text != "5 seconds of 30 seconds, chapter 1, Chapter 1" {
		t.Errorf("aria-valuetext after a key: %q", text)
	}
	tab.Eval(`window.tsPlayer.play()`, nil)
	tab.WaitFor(`window.tsPlayer.state().playing`, 3*time.Second)
	time.Sleep(3200 * time.Millisecond)
	var later string
	tab.Eval(`document.querySelector(".ts-wave__input").getAttribute("aria-valuetext")`, &later)
	tab.Eval(`window.tsPlayer.pause()`, nil)
	if later != text {
		t.Errorf("aria-valuetext changed within 10 s of a key: %q then %q", text, later)
	}
	// The times in words: elapsed, chapter name, time left; the bar's meta line.
	var times struct{ Elapsed, Chapter, Left, Meta string }
	tab.Eval(`({Elapsed: document.querySelector('[data-pbar="elapsed"]').textContent, Chapter: document.querySelector('[data-pbar="chapter"]').textContent,
	  Left: document.querySelector('[data-pbar="left"]').textContent, Meta: document.querySelector("[data-ts-bar-meta]").textContent})`, &times)
	if !regexp.MustCompile(`^0:\d\d$`).MatchString(times.Elapsed) || times.Chapter != "Chapter 1 · Chapter 1" || !regexp.MustCompile(`^\x{2212}0:\d\d$`).MatchString(times.Left) ||
		!strings.HasPrefix(times.Meta, "Imogen Hale · ") || !strings.HasSuffix(times.Meta, " left in book") {
		t.Errorf("times %+v", times)
	}
}

// TestPlayerBarBrowserWaveLoading: while the server answers 404 with Retry-After the bar shows flat,
// pulsing bars with the chapter's words, still scrubs, and draws the real shape as soon as the values
// exist; reduced motion removes the pulse; a 404 without Retry-After stays flat and quiet.
func TestPlayerBarBrowserWaveLoading(t *testing.T) {
	e := playerApp(t)
	peaksAttach(t, e.a) // nothing built yet: 404 + Retry-After 30
	tab := pbwOpen(t, e, 1440, 900, false, 0.01)
	tab.WaitFor(`document.querySelectorAll(".ts-wave--bar > svg").length === 4`, 5*time.Second)
	r := pbwRead(tab)
	flat := true
	for _, h := range r.Heights {
		if h != r.Heights[0] {
			flat = false
		}
	}
	var chapter string
	tab.Eval(`document.querySelector('[data-pbar="chapter"]').textContent`, &chapter)
	if !r.Loading || !flat || r.Animation != "ts-wave-pulse" || chapter != "Drawing the waveform for chapter 4…" {
		t.Errorf("loading: loading %v flat %v animation %q chapter text %q", r.Loading, flat, r.Animation, chapter)
	}
	// It still scrubs: 50 % of chapter 4 is 105 s.
	box := barBox(tab, pbwWave)
	fullpMouse(t, tab, "mousePressed", box.L+0.5*box.W, box.T+box.H/2)
	fullpMouse(t, tab, "mouseReleased", box.L+0.5*box.W, box.T+box.H/2)
	tab.WaitFor(`Math.abs(window.tsPlayer.state().positionMs - 105000) < 600`, 2*time.Second)
	// The values appear late: the next retry draws them.
	wavedrawWrite(t, e, e.files[1])
	tab.WaitFor(`!document.querySelector(".ts-wave--bar").classList.contains("ts-wave--loading")`, 8*time.Second)
	time.Sleep(200 * time.Millisecond)
	r = pbwRead(tab)
	shaped := false
	for _, h := range r.Heights {
		if h != r.Heights[0] {
			shaped = true
		}
	}
	tab.Eval(`document.querySelector('[data-pbar="chapter"]').textContent`, &chapter)
	if r.Loading || !shaped || r.N != int(r.Width)/4 || chapter != "Chapter 4 · Chapter 4" {
		t.Errorf("filled in: loading %v shaped %v bars %d chapter text %q", r.Loading, shaped, r.N, chapter)
	}

	// Reduced motion: no pulse.
	e2 := playerApp(t)
	peaksAttach(t, e2.a)
	still := pbwOpen(t, e2, 1440, 900, true, 0.01)
	still.WaitFor(`document.querySelectorAll(".ts-wave--bar > svg").length === 4`, 5*time.Second)
	if r = pbwRead(still); !r.Loading || r.Animation != "none" {
		t.Errorf("reduced motion: loading %v animation %q, want loading without a pulse", r.Loading, r.Animation)
	}
}

// TestPlayerBarBrowserWaveNoPeaks: a 404 without Retry-After (nothing will build the values) is flat bars,
// no pulse, the chapter's name, and the wave still scrubs.
func TestPlayerBarBrowserWaveNoPeaks(t *testing.T) {
	e := playerApp(t) // no peaks service: a bare 404
	tab := pbwOpen(t, e, 1440, 900, false, 0)
	tab.WaitFor(`document.querySelectorAll(".ts-wave--bar > svg").length === 4`, 5*time.Second)
	time.Sleep(500 * time.Millisecond)
	r := pbwRead(tab)
	var chapter string
	tab.Eval(`document.querySelector('[data-pbar="chapter"]').textContent`, &chapter)
	if r.Loading || r.Animation != "none" || chapter != "Chapter 4 · Chapter 4" || r.N != int(r.Width)/4 {
		t.Errorf("no peaks: %+v chapter text %q", r, chapter)
	}
	box := barBox(tab, pbwWave)
	fullpMouse(t, tab, "mousePressed", box.L+0.5*box.W, box.T+box.H/2)
	fullpMouse(t, tab, "mouseReleased", box.L+0.5*box.W, box.T+box.H/2)
	tab.WaitFor(`Math.abs(window.tsPlayer.state().positionMs - 105000) < 600`, 2*time.Second)
}

// TestPlayerBarBrowserWaveChapterChange: the chapter ending loads the next chapter's values: the drawing
// changes and the times name the next chapter.
func TestPlayerBarBrowserWaveChapterChange(t *testing.T) {
	e := playerApp(t)
	pbwWithPeaks(t, e)
	tab := pbwOpen(t, e, 1440, 900, true, 0)
	pbwSeek(t, tab, 10_000)
	tab.WaitFor(`document.querySelectorAll(".ts-wave--bar > svg").length === 4`, 5*time.Second)
	var d1, d2, chapter string
	tab.Eval(`document.querySelector(".ts-wave__rest path").getAttribute("d")`, &d1)
	pbwSeek(t, tab, 40_000)
	tab.WaitFor(`document.querySelector('[data-pbar="chapter"]').textContent === "Chapter 2 · Chapter 2"`, 3*time.Second)
	tab.Eval(`document.querySelector(".ts-wave__rest path").getAttribute("d")`, &d2)
	tab.Eval(`document.querySelector(".ts-wave--bar .ts-wave__input").getAttribute("aria-label")`, &chapter)
	if d1 == d2 || chapter != "Position in chapter 2" {
		t.Errorf("chapter 2: same drawing %v, aria-label %q", d1 == d2, chapter)
	}
}

// TestPlayerBarBrowserWaveNavigation: three boosted navigations leave the bar's node, its drawing and
// its visibility alone: nothing is repainted, nothing toggles hidden.
func TestPlayerBarBrowserWaveNavigation(t *testing.T) {
	e := playerApp(t)
	pbwWithPeaks(t, e)
	tab := pbwOpen(t, e, 1440, 900, true, 0)
	pbwSeek(t, tab, 10_000)
	tab.WaitFor(`document.querySelectorAll(".ts-wave--bar > svg").length === 4`, 5*time.Second)
	tab.Eval(`(() => { window.barNode = document.getElementById("player"); window.barSvg = document.querySelector(".ts-wave__rest"); window.barMut = {svg: 0, hidden: 0};
	  new MutationObserver((list) => { for (const m of list) {
	    if (m.type === "attributes" && m.attributeName === "hidden") window.barMut.hidden++;
	    if (m.type === "childList" && [...m.addedNodes, ...m.removedNodes].some((n) => n.nodeName.toLowerCase() === "svg")) window.barMut.svg++;
	  } }).observe(document.getElementById("player"), {subtree: true, childList: true, attributes: true, attributeFilter: ["hidden"]}); })()`, nil)
	for _, nav := range []string{"/libraries", "/", "/libraries"} {
		persistClickNav(t, tab, `a[data-nav="`+nav+`"]`, nav)
		time.Sleep(300 * time.Millisecond)
	}
	var s struct {
		Same, SameSvg bool
		Svg, Hidden   int
	}
	tab.Eval(`({Same: window.barNode === document.getElementById("player"), SameSvg: window.barSvg === document.querySelector(".ts-wave__rest"),
	  Svg: window.barMut.svg, Hidden: window.barMut.hidden})`, &s)
	if !s.Same || !s.SameSvg || s.Svg != 0 || s.Hidden != 0 {
		t.Errorf("after three navigations: %+v", s)
	}
}

// TestPlayerBarBrowserWaveCapture records the bar with peaks at 1280, 1440 and 1920 in four themes, and
// hover, drag and loading, next to docs/design/tendshelf-player-bar/screens/PBar*.png (compared by eye,
// not pixel by pixel). With TENDSHELF_PB_DIR set the PNGs go there.
func TestPlayerBarBrowserWaveCapture(t *testing.T) {
	out := os.Getenv("TENDSHELF_PB_DIR")
	if out == "" {
		out = filepath.Join(playerOut(t), "player-bar")
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	e := playerApp(t)
	pbwWithPeaks(t, e)
	for _, w := range []int{1280, 1440, 1920} {
		tab := pbwOpen(t, e, w, 900, true, 0)
		pbwSeek(t, tab, 12_000)
		tab.WaitFor(`document.querySelectorAll(".ts-wave--bar > svg").length === 4`, 5*time.Second)
		box := barBox(tab, pbwWave)
		y := box.T + box.H/2
		for _, theme := range []string{"dark", "light", "sepia", "contrast"} {
			tab.Eval(`document.documentElement.setAttribute("data-theme", `+cdp.Quote(theme)+`)`, nil)
			pbwMove(t, tab, 40, 300)
			time.Sleep(400 * time.Millisecond)
			tab.Screenshot(filepath.Join(out, fmt.Sprintf("playing-%s-%d.png", theme, w)))
			if theme == "dark" || theme == "light" {
				pbwMove(t, tab, box.L+0.7*box.W, y)
				time.Sleep(400 * time.Millisecond)
				tab.Screenshot(filepath.Join(out, fmt.Sprintf("hover-%s-%d.png", theme, w)))
				fullpMouse(t, tab, "mousePressed", box.L+0.7*box.W, y)
				fullpMouse(t, tab, "mouseMoved", box.L+0.72*box.W, y)
				time.Sleep(300 * time.Millisecond)
				tab.Screenshot(filepath.Join(out, fmt.Sprintf("drag-%s-%d.png", theme, w)))
				tab.Eval(`document.querySelector(".ts-wave--bar").dispatchEvent(new PointerEvent("pointercancel", {bubbles: true}))`, nil)
				fullpMouse(t, tab, "mouseReleased", box.L+0.72*box.W, y)
			}
		}
	}
	// Loading: the peaks are not built and the server keeps saying so.
	e2 := playerApp(t)
	peaksAttach(t, e2.a)
	for _, w := range []int{1280, 1440, 1920} {
		tab := pbwOpen(t, e2, w, 900, false, 0)
		tab.WaitFor(`document.querySelector(".ts-wave--bar").classList.contains("ts-wave--loading")`, 5*time.Second)
		for _, theme := range []string{"dark", "light"} {
			tab.Eval(`document.documentElement.setAttribute("data-theme", `+cdp.Quote(theme)+`)`, nil)
			time.Sleep(400 * time.Millisecond)
			tab.Screenshot(filepath.Join(out, fmt.Sprintf("loading-%s-%d.png", theme, w)))
		}
	}
}
