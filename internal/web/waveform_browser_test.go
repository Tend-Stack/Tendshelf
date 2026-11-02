//go:build dev

package web

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The waveform under the chapter scrubber (W2): inline SVG bars, decorative, the played part in the
// cover accent by CSS alone. The P1c fixture has two 90 s files and six chapters of 30 s. Only the
// first file gets a peaks file, so chapters 1-3 draw and chapters 4-6 meet a 404 and draw nothing.
// make player runs this.

const (
	wavedrawFull = `#player-full [data-ts-wave]`
	wavedrawBar  = `#player [data-ts-wave="bar"]`

	// The bar's played colour is --wave-played of the theme (tokens.css), as the browser reports it.
	wavedrawBarDark     = "rgb(226, 112, 58)"
	wavedrawBarLight    = "rgb(162, 68, 26)"
	wavedrawBarContrast = "rgb(255, 154, 102)"
)

// wavedrawWrite stores a 180-bin peaks file (90 s of 500 ms bins) for f, in the on-disk format.
func wavedrawWrite(t *testing.T, e *playerEnv, f library.AudioFile) {
	t.Helper()
	values := make([]byte, 180)
	for i := range values {
		values[i] = byte(20 + (i*7)%200)
	}
	dir := mediajobs.PeaksDir(e.a.dir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	b := media.Peaks{BinMs: media.PeaksBinMs, Values: values}.Encode()
	if err := os.WriteFile(filepath.Join(dir, f.QuickHash+"-v1.peaks"), b, 0o640); err != nil {
		t.Fatal(err)
	}
}

type wavedrawRead struct {
	Svgs, Bars   int
	Hidden       bool
	D, PlayedD   string
	Muted, Fill  string
	MutedOpacity string
	Clip         string
	Width        float64
	Display      string
}

func wavedrawState(tab *cdp.Tab, sel string) wavedrawRead {
	var r wavedrawRead
	tab.Eval(`(() => {
	  const w = document.querySelector(`+cdp.Quote(sel)+`);
	  const m = w.querySelector(".ts-wave__muted, .ts-wave__rest"), p = w.querySelector(".ts-wave__played");
	  const bar = w.dataset.tsWave === "bar";
	  const paint = (s) => bar ? getComputedStyle(s.querySelector("path")).stroke : getComputedStyle(s).fill;
	  const d = (s) => s ? s.querySelector("path").getAttribute("d") : "";
	  return {Svgs: w.querySelectorAll("svg").length, Bars: (d(m).match(/M/g) || []).length,
	    Hidden: w.getAttribute("aria-hidden") === "true" || [...w.querySelectorAll("svg")].every((s) => s.getAttribute("aria-hidden") === "true"), D: d(m), PlayedD: d(p),
	    Muted: m ? paint(m) : "", Fill: p ? paint(p) : "",
	    MutedOpacity: m ? getComputedStyle(m).opacity : "", Clip: p ? getComputedStyle(p).clipPath : "",
	    Width: w.getBoundingClientRect().width, Display: getComputedStyle(w).display};
	})()`, &r)
	return r
}

// wavedrawSeek moves the position and waits for it.
func wavedrawSeek(t *testing.T, tab *cdp.Tab, ms int64) {
	t.Helper()
	tab.Eval(fmt.Sprintf(`window.tsPlayer.seek(%d)`, ms), nil)
	tab.WaitFor(fmt.Sprintf(`Math.abs(window.tsPlayer.state().positionMs - %d) < 1000`, ms), 2*time.Second)
	time.Sleep(200 * time.Millisecond)
}

// wavedrawClipFraction reads how much of the played picture is shown from its clip-path (an inset
// whose right side is the hidden part).
func wavedrawClipFraction(t *testing.T, r wavedrawRead) float64 {
	t.Helper()
	var top, right, bottom, left string
	text := strings.NewReplacer("inset(", "", ")", "").Replace(r.Clip)
	if _, err := fmt.Sscanf(text, "%s %s %s %s", &top, &right, &bottom, &left); err != nil || r.Width == 0 {
		t.Fatalf("clip-path %q, width %v: %v", r.Clip, r.Width, err)
	}
	var hidden float64
	if strings.HasSuffix(right, "%") {
		fmt.Sscanf(right, "%f%%", &hidden)
		return 1 - hidden/100
	}
	fmt.Sscanf(right, "%fpx", &hidden)
	return 1 - hidden/r.Width
}

func wavedrawCheckDrawn(t *testing.T, what string, tab *cdp.Tab, sel string) wavedrawRead {
	t.Helper()
	tab.WaitFor(`document.querySelector(`+cdp.Quote(sel)+`).querySelectorAll("svg").length > 0 && !document.querySelector(`+cdp.Quote(sel)+`).classList.contains("ts-wave--loading")`, 5*time.Second)
	r := wavedrawState(tab, sel)
	layers := 2
	if sel == wavedrawBar {
		layers = 4 // rest, played, ahead, behind
	}
	want := int(r.Width) / 4
	if want < 8 {
		want = 8
	}
	if r.Svgs != layers || r.Bars != want || !r.Hidden || r.D != r.PlayedD || r.Display == "none" {
		t.Errorf("%s: %+v (want %d svgs, %d bars, aria-hidden, equal paths)", what, r, layers, want)
	}
	wantOpacity := "0.5" // the full player's rest layer is a faded line; the bar's is its own colour, token --wave-rest
	if sel == wavedrawBar {
		wantOpacity = "1"
	}
	if r.Fill == r.Muted || r.MutedOpacity != wantOpacity {
		t.Errorf("%s: played %q and muted %q (opacity %s) must differ", what, r.Fill, r.Muted, r.MutedOpacity)
	}
	return r
}

func TestWaveformBrowser(t *testing.T) {
	e := playerApp(t)
	peaksAttach(t, e.a)
	wavedrawWrite(t, e, e.files[0])

	for _, vp := range []struct{ w, h int }{{390, 844}, {1440, 900}} {
		tab := fullpOpen(t, e, vp.w, vp.h, false)
		tab.Eval(`document.documentElement.setAttribute("data-theme", "dark")`, nil)
		fullpOpenPlayer(t, tab)
		// On a desktop the side panel hides its chapter slider (only the book ruler shows there), so the
		// waveform and the click are on the bar's compact scrubber.
		sels := []string{wavedrawFull}
		sel0 := wavedrawFull
		slider := `#player-full [data-ts-range]`
		if vp.w >= 1280 {
			sels = []string{wavedrawBar}
			sel0 = wavedrawBar
			slider = `#player .ts-wave--bar .ts-wave__input`
		}

		// The stored position is in chapter 4 (file 2, no peaks yet): a 404 draws nothing, and the
		// scrubber still works: a click at 90 % of the line lands at 90 % of chapter 4.
		time.Sleep(500 * time.Millisecond)
		for _, sel := range sels {
			// The bar draws flat bars (pulsing) while the values are on their way; the full player draws nothing.
			want := 0
			if sel == wavedrawBar {
				want = 4
			}
			if r := wavedrawState(tab, sel); r.Svgs != want {
				t.Errorf("%dpx %s: %d svgs drawn while /peaks/ answers 404, want %d", vp.w, sel, r.Svgs, want)
			}
		}
		box := chapscrubMeasure(tab, slider)
		fullpMouse(t, tab, "mousePressed", box.L+0.9*box.W, box.Y)
		fullpMouse(t, tab, "mouseReleased", box.L+0.9*box.W, box.Y)
		tab.WaitFor(`Math.abs(window.tsPlayer.state().positionMs - 117000) < 1000`, 2*time.Second)

		// Chapter 3 (60-90 s, in file 1): drawn at the width's bar count.
		wavedrawSeek(t, tab, 75_000)
		chapter3 := map[string]wavedrawRead{}
		for _, sel := range sels {
			r := wavedrawCheckDrawn(t, fmt.Sprintf("%dpx %s", vp.w, sel), tab, sel)
			// The full player uses the cover's dark accent #9ccbc4; the bar's scrubber its own fill colour.
			want := "rgb(156, 203, 196)"
			if sel == wavedrawBar {
				want = wavedrawBarDark
			}
			if r.Fill != want {
				t.Errorf("%dpx %s: played fill %q, want %q (the scrubber's own colour)", vp.w, sel, r.Fill, want)
			}
			if f := wavedrawClipFraction(t, r); f < 0.45 || f > 0.55 {
				t.Errorf("%dpx %s: played part %.2f of the width in mid-chapter, want about 0.5", vp.w, sel, f)
			}
			chapter3[sel] = r
		}

		// The side panel: the desktop shows the book ruler and no slider; a phone keeps the slider.
		var panel struct{ Slider, Ruler string }
		tab.Eval(`(() => {
		  const show = (s) => getComputedStyle(document.querySelector(s)).display;
		  const wave = document.querySelector(`+cdp.Quote(sel0)+`);
		  return {Slider: show('#player-full [data-ts-scrub="full"]'), Ruler: show("#player-full [data-ts-ruler]")};
		})()`, &panel)
		if vp.w >= 1280 {
			if panel.Slider != "none" || panel.Ruler == "none" {
				t.Errorf("%dpx: side panel slider display %q, ruler %q; want the slider hidden and the ruler shown", vp.w, panel.Slider, panel.Ruler)
			}
		} else if panel.Slider == "none" {
			t.Errorf("%dpx: the full player's slider is hidden", vp.w)
		}

		// The position moves inside the chapter: the clip follows, the bars do not change.
		wavedrawSeek(t, tab, 84_000)
		r := wavedrawState(tab, sel0)
		if f := wavedrawClipFraction(t, r); f < 0.75 || f > 0.85 || r.D != chapter3[sel0].D {
			t.Errorf("%dpx: after a seek inside the chapter: played %.2f (want about 0.8), paths equal %v", vp.w, f, r.D == chapter3[sel0].D)
		}

		// Another chapter of the same file redraws.
		wavedrawSeek(t, tab, 10_000)
		tab.WaitFor(`document.querySelector(`+cdp.Quote(sel0)+`).querySelector("path").getAttribute("d") !== `+cdp.Quote(chapter3[sel0].D), 3*time.Second)

		// Themes switch by CSS alone: no new fetch, same bars; contrast uses ink with no accent.
		var before, after int
		tab.Eval(`performance.getEntriesByType("resource").filter((e) => e.name.includes("/peaks/")).length`, &before)
		d1 := wavedrawState(tab, sel0).D
		for _, theme := range []string{"light", "sepia", "contrast", "dark"} {
			tab.Eval(`document.documentElement.setAttribute("data-theme", `+cdp.Quote(theme)+`)`, nil)
			time.Sleep(100 * time.Millisecond)
			if theme == "light" {
				// The full player uses the cover's light accent; the bar's scrubber its own fill colour.
				want := "rgb(44, 102, 96)"
				if sel0 == wavedrawBar {
					want = wavedrawBarLight
				}
				if got := wavedrawState(tab, sel0).Fill; got != want {
					t.Errorf("%dpx light: played fill %q, want %q", vp.w, got, want)
				}
			}
			if theme == "contrast" {
				want := "rgb(255, 255, 255)"
				if sel0 == wavedrawBar {
					want = wavedrawBarContrast
				}
				if got := wavedrawState(tab, sel0).Fill; got != want {
					t.Errorf("%dpx contrast: played fill %q, want %q", vp.w, got, want)
				}
			}
		}
		tab.Eval(`performance.getEntriesByType("resource").filter((e) => e.name.includes("/peaks/")).length`, &after)
		if before != after || wavedrawState(tab, sel0).D != d1 {
			t.Errorf("%dpx: a theme change fetched %d new peaks or redrew", vp.w, after-before)
		}
		tab.Eval(`window.tsPlayer.pause()`, nil)
	}

	// Pure helpers: a chapter that spans two files joins them; a flat chapter stays level.
	tab := fullpOpen(t, e, 390, 844, false)
	var spans struct{ N, Zeros, Equal int }
	tab.Eval(`(() => {
	  const h = window.tsWave.debug.heights;
	  const a = { offset: 0, binMs: 500, bins: new Uint8Array(20).fill(200) };
	  const b = { offset: 10000, binMs: 500, bins: new Uint8Array(20).fill(100) };
	  const two = h([a, b], 5000, 15000, 10);
	  const flat = h([a], 0, 10000, 8);
	  return { N: two.length, Zeros: two.filter((v) => v === 0).length, Equal: flat.every((v) => v === flat[0]) ? 1 : 0 };
	})()`, &spans)
	if spans.N != 10 || spans.Zeros != 0 || spans.Equal != 1 {
		t.Errorf("heights: %+v", spans)
	}
	var bad bool
	tab.Eval(`window.tsWave.debug.parse(new Uint8Array(32).buffer) === null && window.tsWave.debug.parse(new TextEncoder().encode("TSPK").buffer) === null`, &bad)
	if !bad {
		t.Error("parse accepted a bad peaks file")
	}
}
