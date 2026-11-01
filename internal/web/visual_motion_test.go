//go:build dev

package web

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

var (
	runOnce sync.Once
	runID   string
)

// visualDir returns .local/tests/visual/<run>/<engine>/ (git-ignored), one run id per test process.
func visualDir(t *testing.T, engine string) string {
	t.Helper()
	runOnce.Do(func() { runID = time.Now().UTC().Format("20060102T150405Z") })
	dir := filepath.Join("..", "..", ".local", "tests", "visual", runID, engine)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func chromium(t *testing.T) *cdp.Browser {
	t.Helper()
	path := os.Getenv("TENDSHELF_TEST_BROWSER")
	if path == "" {
		t.Skip("TENDSHELF_TEST_BROWSER not set")
	}
	return cdp.Start(t, path)
}

func appServer(t *testing.T) string {
	t.Helper()
	srv := httptest.NewServer(NewHandler(Options{Version: "visual"}))
	t.Cleanup(srv.Close)
	return srv.URL
}

var themes = []string{"dark", "light", "sepia", "contrast"}

// shellRectsJS returns the rects of the shell parts a page swap must not move.
const shellRectsJS = `(() => {
  const out = {};
  for (const id of ["nav-tabbar", "nav-rail", "nav-side", "player", "audio"]) {
    const el = document.getElementById(id);
    const r = el ? el.getBoundingClientRect() : null;
    out[id] = r ? [r.x, r.y, r.width, r.height] : null;
  }
  return out;
})()`

// TestVisualKitchenSink captures the kitchen sink in every theme at phone and desktop width, plus
// and the toasts, and fails on horizontal overflow.
func TestVisualKitchenSink(t *testing.T) {
	b := chromium(t)
	base := appServer(t)
	out := visualDir(t, "chromium")
	for _, vp := range []struct {
		w, h   int
		mobile bool
	}{{390, 844, true}, {1440, 900, false}} {
		for _, theme := range themes {
			t.Run(fmt.Sprintf("%s-%d", theme, vp.w), func(t *testing.T) {
				tab := b.NewTab(vp.w, vp.h, vp.mobile, false)
				tab.Navigate(base + "/dev/kitchen-sink?theme=" + theme)
				var m struct {
					Over  bool   `json:"over"`
					Theme string `json:"theme"`
					Bar   string `json:"bar"`
				}
				tab.Eval(`({over: document.documentElement.scrollWidth > innerWidth,
					theme: document.documentElement.dataset.theme,
					bar: getComputedStyle(document.querySelector('#ks-bar .ts-bar__fill')).width})`, &m)
				if m.Over {
					t.Errorf("horizontal overflow at %d px in theme %s", vp.w, theme)
				}
				if m.Theme != theme {
					t.Errorf("data-theme = %q, want %q", m.Theme, theme)
				}
				if m.Bar == "0px" {
					t.Error("progress fill is 0 wide: shell.js did not apply data-ts-value")
				}
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("kitchen-%s-%d.png", theme, vp.w)))
				if theme != "dark" {
					return
				}
				// Toasts, then the sheet.
				for _, kind := range []string{"ok", "hint", "err"} {
					tab.Eval(fmt.Sprintf(`document.querySelector('[hx-post$="kind=%s"]').click()`, kind), nil)
				}
				tab.WaitFor(`document.querySelectorAll('#toasts .ts-toast').length === 3`, 5*time.Second)
				time.Sleep(400 * time.Millisecond)
				tab.Screenshot(filepath.Join(out, fmt.Sprintf("toasts-%d.png", vp.w)))
			})
		}
	}
	t.Logf("screenshots in %s", out)
}

const sheetProbeJS = `(() => {
  const s = document.querySelector("#sheet .ts-sheet");
  if (!s) return null;
  const r = s.getBoundingClientRect();
  const hit = (y) => { const e = document.elementFromPoint(innerWidth / 2, y); return {inside: Boolean(e && e.closest(".ts-sheet")), what: e ? (e.id || e.className || e.tagName) : ""}; };
  return {bottom: r.bottom, h: innerHeight, over: document.documentElement.scrollWidth > innerWidth,
    centre: hit(r.top + r.height / 2), edge: hit(innerHeight - 8), focused: document.activeElement === s,
    outsideMain: !document.getElementById("main").contains(s)};
})()`

type sheetProbe struct {
	Bottom float64 `json:"bottom"`
	H      float64 `json:"h"`
	Over   bool    `json:"over"`
	Centre struct {
		Inside bool   `json:"inside"`
		What   string `json:"what"`
	} `json:"centre"`
	Edge struct {
		Inside bool   `json:"inside"`
		What   string `json:"what"`
	} `json:"edge"`
	Focused     bool `json:"focused"`
	OutsideMain bool `json:"outsideMain"`
}

func checkSheet(t *testing.T, name string, p sheetProbe) {
	t.Helper()
	t.Logf("%s: sheet bottom %.0f of %.0f; centre hits %q, bottom edge hits %q", name, p.Bottom, p.H, p.Centre.What, p.Edge.What)
	if !p.Centre.Inside || !p.Edge.Inside {
		t.Errorf("%s: the sheet is covered (centre %q, bottom edge %q)", name, p.Centre.What, p.Edge.What)
	}
	if p.Bottom < p.H-1 || p.Bottom > p.H+1 {
		t.Errorf("%s: sheet bottom = %.0f, want the viewport bottom %.0f", name, p.Bottom, p.H)
	}
	if p.Over {
		t.Errorf("%s: horizontal overflow with the sheet open", name)
	}
	if !p.OutsideMain {
		t.Errorf("%s: the sheet is inside #main", name)
	}
}

// TestVisualSheet: a sheet swapped into #sheet (and one on a canonical ?sheet=1 page) covers the tab
// bar at phone width, takes focus, closes with its exit motion and hands focus back to its opener.
func TestVisualSheet(t *testing.T) {
	b := chromium(t)
	base := appServer(t)
	out := visualDir(t, "chromium")
	for _, vp := range []struct {
		w, h   int
		mobile bool
	}{{390, 844, true}, {1440, 900, false}} {
		t.Run(fmt.Sprint(vp.w), func(t *testing.T) {
			tab := b.NewTab(vp.w, vp.h, vp.mobile, false)
			tab.Navigate(base + "/dev/kitchen-sink")
			tab.Eval(`document.querySelector('#ks-sheet a').click()`, nil)
			tab.WaitFor(`document.querySelector('#sheet .ts-sheet')`, 5*time.Second)
			time.Sleep(400 * time.Millisecond)
			tab.Screenshot(filepath.Join(out, fmt.Sprintf("sheet-%d.png", vp.w)))
			var p sheetProbe
			tab.Eval(sheetProbeJS, &p)
			checkSheet(t, "swapped", p)
			if !p.Focused {
				t.Error("the sheet did not take focus")
			}
			var loc string
			tab.Eval(`location.pathname + location.search`, &loc)
			if loc != "/dev/kitchen-sink" {
				t.Errorf("opening the sheet changed the URL to %q", loc)
			}
			tab.Eval(`document.querySelector('#sheet [data-ts-close][aria-label="Close"]').click()`, nil)
			tab.WaitFor(`!document.querySelector('#sheet .ts-sheet-layer')`, 5*time.Second)
			var after struct {
				Empty   bool `json:"empty"`
				Opener  bool `json:"opener"`
				Display string
			}
			tab.Eval(`({empty: document.getElementById("sheet").childElementCount === 0,
				opener: document.activeElement === document.querySelector("#ks-sheet a"),
				Display: getComputedStyle(document.getElementById("sheet")).display})`, &after)
			if !after.Empty || after.Display != "none" {
				t.Errorf("after closing, #sheet empty = %v, display %q", after.Empty, after.Display)
			}
			if !after.Opener {
				t.Error("focus did not return to the opener")
			}
			// Escape closes too.
			tab.Eval(`document.querySelector('#ks-sheet a').click()`, nil)
			tab.WaitFor(`document.querySelector('#sheet .ts-sheet')`, 5*time.Second)
			tab.Eval(`document.dispatchEvent(new KeyboardEvent("keydown", {key: "Escape", bubbles: true}))`, nil)
			tab.WaitFor(`!document.querySelector('#sheet .ts-sheet-layer')`, 5*time.Second)
			// The canonical URL, as without JavaScript.
			tab.Navigate(base + "/dev/kitchen-sink?sheet=1")
			time.Sleep(400 * time.Millisecond)
			tab.Eval(sheetProbeJS, &p)
			checkSheet(t, "canonical", p)
		})
	}
}

// TestVisualSwapNoShift: a boosted navigation runs one view transition, leaves the navigation,
// player slot and audio element exactly where they were, and shifts no layout; Back restores the page.
func TestVisualSwapNoShift(t *testing.T) {
	b := chromium(t)
	base := appServer(t)
	for _, vp := range []struct {
		w, h   int
		mobile bool
	}{{390, 844, true}, {1440, 900, false}} {
		t.Run(fmt.Sprint(vp.w), func(t *testing.T) {
			tab := b.NewTab(vp.w, vp.h, vp.mobile, false)
			tab.Navigate(base + "/dev/kitchen-sink")
			tab.Eval(`(() => {
				window.__cls = 0; window.__vt = 0; window.__settled = 0; window.__shifts = [];
				new PerformanceObserver((l) => { for (const e of l.getEntries()) if (!e.hadRecentInput) { window.__cls += e.value;
					window.__shifts.push(e.value + " @" + Math.round(e.startTime) + " " + e.sources.map((s) => (s.node && (s.node.id || s.node.className || s.node.tagName)) + " " + JSON.stringify(s.previousRect) + "->" + JSON.stringify(s.currentRect)).join("; ")); } })
					.observe({type: "layout-shift", buffered: false}); // shifts from the page load itself (font swap) are not the swap's
				document.addEventListener("htmx:before:viewTransition", () => window.__vt++);
				document.addEventListener("htmx:after:settle", () => window.__settled++);
			})()`, nil)
			var before, after, back map[string][]float64
			tab.Eval(shellRectsJS, &before)
			tab.Eval(`document.querySelector('[data-probe-nav]').click()`, nil)
			tab.WaitFor(`location.pathname === "/dev/kitchen-sink/b" && window.__settled >= 1 && document.querySelector('#main h1')?.textContent.includes('page B')`, 5*time.Second)
			time.Sleep(500 * time.Millisecond) // let the 250 ms transition finish and late shifts report
			tab.Eval(shellRectsJS, &after)
			var res struct {
				VT      int     `json:"vt"`
				CLS     float64 `json:"cls"`
				HasVT   bool    `json:"hasVT"`
				Entered bool    `json:"entered"`
			}
			tab.Eval(`({vt: window.__vt, cls: window.__cls, hasVT: Boolean(document.startViewTransition),
				entered: document.getElementById('main').classList.contains('ts-entering')})`, &res)
			t.Logf("width %d: view transitions %d, CLS %v, rects before %v", vp.w, res.VT, res.CLS, before)
			if res.VT != 1 {
				t.Errorf("htmx:before:viewTransition fired %d times, want 1", res.VT)
			}
			if res.CLS != 0 {
				var shifts []string
				tab.Eval(`window.__shifts`, &shifts)
				t.Logf("shifts: %v", shifts)
				t.Errorf("layout shift sum = %v, want 0", res.CLS)
			}
			if fmt.Sprint(before) != fmt.Sprint(after) {
				t.Errorf("shell rects moved on swap:\nbefore %v\nafter  %v", before, after)
			}
			tab.Eval(`history.back()`, nil)
			tab.WaitFor(`location.pathname === "/dev/kitchen-sink" && document.getElementById('ks-utilities')`, 5*time.Second)
			time.Sleep(300 * time.Millisecond)
			tab.Eval(shellRectsJS, &back)
			if fmt.Sprint(before) != fmt.Sprint(back) {
				t.Errorf("shell rects moved after Back:\nbefore %v\nback   %v", before, back)
			}
			var cls float64
			tab.Eval(`window.__cls`, &cls)
			if cls != 0 {
				t.Errorf("layout shift sum after Back = %v, want 0", cls)
			}
		})
	}
}

// TestVisualReducedMotion: with prefers-reduced-motion the swap animates opacity only, in 150 ms or
// less, and a fresh toast transitions in 150 ms or less.
func TestVisualReducedMotion(t *testing.T) {
	b := chromium(t)
	base := appServer(t)
	tab := b.NewTab(390, 844, true, true)
	tab.Navigate(base + "/dev/kitchen-sink")
	var reduced bool
	tab.Eval(`matchMedia("(prefers-reduced-motion: reduce)").matches`, &reduced)
	if !reduced {
		t.Fatal("reduced motion emulation is off")
	}
	tab.Eval(`(() => {
		window.__anims = null; window.__settled = 0;
		document.addEventListener("htmx:after:settle", () => window.__settled++);
		document.addEventListener("htmx:before:viewTransition", () => {
			requestAnimationFrame(() => requestAnimationFrame(() => {
				window.__anims = document.getAnimations().map((a) => {
					const e = a.effect;
					const frames = e && e.getKeyframes ? e.getKeyframes() : [];
					const props = new Set();
					for (const f of frames) for (const k of Object.keys(f)) props.add(k);
					return {name: a.animationName || "", pseudo: e && e.pseudoElement || "", duration: e && e.getTiming().duration, props: [...props]};
				});
			}));
		});
	})()`, nil)
	tab.Eval(`document.querySelector('[data-probe-nav]').click()`, nil)
	tab.WaitFor(`window.__anims !== null && window.__settled >= 1`, 5*time.Second)
	var anims []struct {
		Name     string   `json:"name"`
		Pseudo   string   `json:"pseudo"`
		Duration float64  `json:"duration"`
		Props    []string `json:"props"`
	}
	tab.Eval(`window.__anims`, &anims)
	if len(anims) == 0 {
		t.Fatal("no animations sampled during the swap")
	}
	for _, a := range anims {
		t.Logf("animation %q on %q: %v ms, properties %v", a.Name, a.Pseudo, a.Duration, a.Props)
		if a.Duration > 150 {
			t.Errorf("%s %s lasts %v ms, want at most 150", a.Name, a.Pseudo, a.Duration)
		}
		for _, p := range a.Props {
			if p == "translate" || p == "transform" {
				t.Errorf("%s %s animates %s under reduced motion", a.Name, a.Pseudo, p)
			}
		}
	}
	tab.Navigate(base + "/dev/kitchen-sink")
	tab.Eval(`document.querySelector('[hx-post$="kind=ok"]').click()`, nil)
	tab.WaitFor(`document.querySelector('#toasts .ts-toast')`, 5*time.Second)
	var dur string
	tab.Eval(`getComputedStyle(document.querySelector('#toasts .ts-toast')).transitionDuration`, &dur)
	t.Logf("toast transition-duration = %s", dur)
	if dur != "0.15s" {
		t.Errorf("toast transition-duration = %q, want 0.15s", dur)
	}
	var spin string
	tab.Eval(`getComputedStyle(document.querySelector('.ts-spinner')).animationName`, &spin)
	if spin != "none" {
		t.Errorf("spinner animation = %q under reduced motion, want none", spin)
	}
}

// TestVisualProgressEases: a progress bar eases to its new value instead of jumping.
func TestVisualProgressEases(t *testing.T) {
	b := chromium(t)
	base := appServer(t)
	tab := b.NewTab(390, 844, true, false)
	tab.Navigate(base + "/dev/kitchen-sink")
	var w struct {
		Old, Bar float64
	}
	tab.Eval(`({Old: document.querySelector('#ks-bar .ts-bar__fill').getBoundingClientRect().width,
		Bar: document.querySelector('#ks-bar').getBoundingClientRect().width})`, &w)
	tab.Eval(`document.querySelector('[hx-get$="v=100"]').click()`, nil)
	tab.WaitFor(`document.querySelector('#ks-bar .ts-bar__fill').dataset.tsValue === "100"`, 5*time.Second)
	time.Sleep(300 * time.Millisecond)
	var mid float64
	tab.Eval(`document.querySelector('#ks-bar .ts-bar__fill').getBoundingClientRect().width`, &mid)
	time.Sleep(900 * time.Millisecond)
	var end float64
	tab.Eval(`document.querySelector('#ks-bar .ts-bar__fill').getBoundingClientRect().width`, &end)
	t.Logf("bar %.1f px: old %.1f, after 300 ms %.1f, final %.1f", w.Bar, w.Old, mid, end)
	if !(mid > w.Old && mid < end) {
		t.Errorf("width after 300 ms = %.1f, want strictly between %.1f and %.1f", mid, w.Old, end)
	}
	if end < w.Bar-1 {
		t.Errorf("final width %.1f, want the full bar %.1f", end, w.Bar)
	}
}
