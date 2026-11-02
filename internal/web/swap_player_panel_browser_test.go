//go:build dev

package web

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The side panel during a page swap and while its own sheets open (make player).

// swapPanelShot is a screenshot of the viewport, cropped to box.
func swapPanelShot(t *testing.T, tab *cdp.Tab, box image.Rectangle, name string) image.Image {
	t.Helper()
	path := filepath.Join(t.TempDir(), name+".png")
	tab.Screenshot(path)
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		t.Fatal(err)
	}
	return img.(interface {
		SubImage(image.Rectangle) image.Image
	}).SubImage(box)
}

// swapPanelDiff is the share of pixels that differ clearly between two same-sized crops.
func swapPanelDiff(a, b image.Image) float64 {
	r := a.Bounds()
	differ := 0
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			ar, ag, ab, _ := a.At(x, y).RGBA()
			br, bg, bb, _ := b.At(x, y).RGBA()
			d := func(p, q uint32) int {
				if p > q {
					return int(p - q)
				}
				return int(q - p)
			}
			if d(ar, br)+d(ag, bg)+d(ab, bb) > 3*0x1800 {
				differ++
			}
		}
	}
	return float64(differ) / float64(r.Dx()*r.Dy())
}

// TestSwapPlayerBrowserSidePanelStays holds the Library to Home swap with the side panel open (the
// cross-fade is paused at its start, so the old page shows at full strength): the panel does not move
// and looks as it did before the click. An element hit-test cannot tell, it answers the root during
// any view transition, so the pixels are compared.
func TestSwapPlayerBrowserSidePanelStays(t *testing.T) {
	for _, c := range []struct {
		name string
		w, h int
	}{{"1440", 1440, 900}, {"1920", 1920, 1240}} {
		t.Run(c.name, func(t *testing.T) {
			e := playerApp(t)
			tab := swapPlayerStart(t, e, c.w, c.h, true)
			persistClickNav(t, tab, `a[data-nav="/libraries"]`, "/libraries")
			tab.WaitFor(`document.getAnimations().length === 0`, 3*time.Second)
			time.Sleep(500 * time.Millisecond)
			tab.Eval(`window.tsPlayer.pause()`, nil) // a frozen clock and waveform
			time.Sleep(300 * time.Millisecond)
			// An adopted sheet: the swap's head merge would drop a <style> element.
			tab.Eval(`(() => { const s = new CSSStyleSheet();
			  s.replaceSync("::view-transition-old(ts-main), ::view-transition-new(ts-main) { animation-duration: 30s !important; animation-play-state: paused !important; }");
			  document.adoptedStyleSheets = [...document.adoptedStyleSheets, s]; })()`, nil)
			var b struct{ X, Y, W, H float64 }
			tab.Eval(`(() => { const r = document.getElementById("player-full").getBoundingClientRect(); return {X: r.x, Y: r.y, W: r.width, H: r.height}; })()`, &b)
			box := image.Rect(int(b.X)+2, int(b.Y)+2, int(b.X+b.W)-2, int(b.Y+b.H)-2)
			before := swapPanelShot(t, tab, box, "before")
			swapPlayerClickNow(t, tab, `a[data-nav="/"]`)
			tab.WaitFor(`!!document.activeViewTransition && location.pathname === "/"`, 5*time.Second)
			time.Sleep(400 * time.Millisecond)
			during := swapPanelShot(t, tab, box, "during")
			var after struct{ X, Y, W, H float64 }
			tab.Eval(`(() => { const r = document.getElementById("player-full").getBoundingClientRect(); return {X: r.x, Y: r.y, W: r.width, H: r.height}; })()`, &after)
			if after != b {
				t.Errorf("the panel moved during the swap: %+v to %+v", b, after)
			}
			if d := swapPanelDiff(before, during); d > 0.01 {
				t.Errorf("%.1f%% of the panel's pixels changed while the pages swapped: they were painted over it", d*100)
			}
		})
	}
}

// TestSwapPlayerBrowserBarStays is the same for the bottom bar while the book plays with the panel
// closed: the swap's cross-fade must not hide the bar.
func TestSwapPlayerBrowserBarStays(t *testing.T) {
	e := playerApp(t)
	tab := swapPlayerStart(t, e, 1440, 900, false)
	persistClickNav(t, tab, `a[data-nav="/libraries"]`, "/libraries")
	tab.WaitFor(`document.getAnimations().length === 0`, 3*time.Second)
	time.Sleep(500 * time.Millisecond)
	tab.Eval(`(() => { const s = new CSSStyleSheet();
	  s.replaceSync("::view-transition-old(ts-main), ::view-transition-new(ts-main) { animation-duration: 30s !important; animation-play-state: paused !important; }");
	  document.adoptedStyleSheets = [...document.adoptedStyleSheets, s]; })()`, nil)
	var b struct{ X, Y, W, H float64 }
	tab.Eval(`(() => { const r = document.getElementById("player").getBoundingClientRect(); return {X: r.x, Y: r.y, W: r.width, H: r.height}; })()`, &b)
	box := image.Rect(int(b.X)+2, int(b.Y)+2, int(b.X+b.W)-2, int(b.Y+b.H)-2)
	before := swapPanelShot(t, tab, box, "before")
	swapPlayerClickNow(t, tab, `a[data-nav="/"]`)
	tab.WaitFor(`!!document.activeViewTransition && location.pathname === "/"`, 5*time.Second)
	time.Sleep(400 * time.Millisecond)
	during := swapPanelShot(t, tab, box, "during")
	if d := swapPanelDiff(before, during); d > 0.01 {
		t.Errorf("%.1f%% of the bar's pixels changed while the pages swapped: it was painted over", d*100)
	}
}

// swapSheetProbeJS records, every frame for 700 ms, the boxes of the panel's cover, title, elapsed
// clock and scrubber, the worst gap between frames, and any page view transition.
const swapSheetProbeJS = `(() => {
  const panel = document.getElementById("player-full");
  const parts = {cover: ".ts-player__cover", title: "[data-ts-full-title]", clock: "[data-ts-full-elapsed]", scrub: ".ts-scrub"};
  const rect = (el) => { const r = el.getBoundingClientRect(); return [r.x, r.y, r.width, r.height]; };
  const base = {panel: rect(panel)};
  for (const k in parts) base[k] = rect(panel.querySelector(parts[k]));
  const probe = {worstGap: 0, maxShift: {}, vt: 0, frames: 0, done: false};
  window.sheetProbe = probe;
  const shiftOf = (r, b) => Math.max(...r.map((v, i) => Math.abs(v - b[i])));
  let last = 0;
  const started = performance.now();
  const step = (now) => {
    if (last) probe.worstGap = Math.max(probe.worstGap, now - last);
    last = now;
    for (const k in parts) probe.maxShift[k] = Math.max(probe.maxShift[k] || 0, shiftOf(rect(panel.querySelector(parts[k])), base[k]));
    probe.maxShift.panel = Math.max(probe.maxShift.panel || 0, shiftOf(rect(panel), base.panel));
    if (document.activeViewTransition) probe.vt++;
    probe.frames++;
    if (now - started < 700) requestAnimationFrame(step); else probe.done = true;
  };
  requestAnimationFrame(step);
})()`

// TestSwapPlayerBrowserSheetsStay opens each of the panel's sheets with the CPU throttled 4x: the
// cover, title, clock, scrubber and panel stay put to 1 px, no page view transition runs and no frame
// takes 50 ms or more.
func TestSwapPlayerBrowserSheetsStay(t *testing.T) {
	for _, w := range []struct{ w, h int }{{1440, 900}, {1920, 1240}} {
		for _, sheet := range []struct{ name, open string }{
			{"chapters", "[data-ts-open-chapters]"},
			{"speed", "[data-ts-bar-speed]"},
			{"sleep", "[data-ts-bar-sleep]"},
		} {
			t.Run(fmt.Sprintf("%d-%s", w.w, sheet.name), func(t *testing.T) {
				e := playerApp(t)
				tab := swapPlayerStart(t, e, w.w, w.h, true)
				tab.Eval(`window.tsPlayer.pause()`, nil)
				time.Sleep(400 * time.Millisecond)
				if err := tab.Call("Emulation.setCPUThrottlingRate", map[string]any{"rate": 4}, nil); err != nil {
					t.Fatal(err)
				}
				tab.Eval(swapSheetProbeJS, nil)
				playerClickSelNoScroll(t, tab, sheet.open)
				tab.WaitFor(`window.sheetProbe.done`, 8*time.Second)
				var p struct {
					WorstGap float64
					MaxShift map[string]float64
					VT       int
				}
				tab.Eval(`window.sheetProbe`, &p)
				t.Logf("worst frame gap %.0f ms, shifts %v, view transitions %d", p.WorstGap, p.MaxShift, p.VT)
				for part, shift := range p.MaxShift {
					if shift > 1 {
						t.Errorf("%s moved %.1f px while the sheet opened", part, shift)
					}
				}
				if p.VT > 0 {
					t.Errorf("opening the sheet ran a page view transition")
				}
				if p.WorstGap >= 50 {
					t.Errorf("a frame took %.0f ms at 4x CPU", p.WorstGap)
				}
			})
		}
	}
}

// playerClickSelNoScroll clicks the first visible match without scrolling anything.
func playerClickSelNoScroll(t *testing.T, tab *cdp.Tab, sel string) {
	t.Helper()
	var r struct{ X, Y float64 }
	tab.Eval(`(() => { const e = [...document.querySelectorAll(`+cdp.Quote(sel)+`)].find(e => e.getBoundingClientRect().width > 0);
	  const r = e.getBoundingClientRect(); return {X: r.x + r.width / 2, Y: r.y + r.height / 2}; })()`, &r)
	playerClickAt(t, tab, r.X, r.Y)
}

// swapPlayerClickNow clicks at once, without waiting for the previous swap.
func swapPlayerClickNow(t *testing.T, tab *cdp.Tab, sel string) {
	t.Helper()
	playerClickSelNoScroll(t, tab, sel)
}
