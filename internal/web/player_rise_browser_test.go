//go:build dev

package web

import (
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The full player rises from the bottom when it opens and sinks when it closes (make player), at
// 390 x 844: the real dialog moves, opaque the whole way, and no view transition is used.

// riseSample is one animation frame of the dialog.
type riseSample struct {
	Open    bool
	Y       float64
	Opacity string
	Title   string
}

// riseRecord samples the dialog on every frame from now until stop() is called by the page.
const riseRecord = `(() => {
  window.__vtCalls = 0;
  if (document.startViewTransition) {
    const orig = document.startViewTransition.bind(document);
    document.startViewTransition = (...a) => { window.__vtCalls++; return orig(...a); };
  }
  const d = document.getElementById("player-full");
  window.__frames = [];
  const tick = () => {
    const cs = getComputedStyle(d);
    window.__frames.push({Open: d.open, Y: new DOMMatrix(cs.transform).m42, Opacity: cs.opacity,
      Title: d.querySelector("[data-ts-full-title]").textContent});
    if (window.__frames.length < 120) requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
})()`

func riseFrames(tab *cdp.Tab) (frames []riseSample) {
	time.Sleep(700 * time.Millisecond)
	tab.Eval(`window.__frames`, &frames)
	return frames
}

func TestPlayerRisesFromBelow(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 844, false)
	tab.Eval(riseRecord, nil)
	playerClickSel(t, tab, "[data-ts-open]")
	var open []riseSample
	for _, f := range riseFrames(tab) {
		if f.Open {
			open = append(open, f)
		}
	}
	if len(open) < 3 {
		t.Fatalf("only %d frames with the player open", len(open))
	}
	if open[0].Y < 0.5*844 {
		t.Errorf("first frame is at %v px, want well below its place (over %v)", open[0].Y, 0.5*844)
	}
	if open[0].Title == "" {
		t.Error("the title is empty on the first frame; content must be filled before the slide")
	}
	sawMiddle := false
	for _, f := range open {
		if f.Opacity != "1" {
			t.Errorf("opacity %q while rising, want 1", f.Opacity)
			break
		}
		if f.Y > 20 && f.Y < 824 {
			sawMiddle = true
		}
	}
	if !sawMiddle {
		t.Error("no frame between start and place: the player did not slide")
	}
	if last := open[len(open)-1]; last.Y != 0 {
		t.Errorf("ends at %v px, want 0", last.Y)
	}
	var vt int
	tab.Eval(`window.__vtCalls`, &vt)
	if vt != 0 {
		t.Errorf("document.startViewTransition called %d times, want 0", vt)
	}
}

func TestPlayerCloseButtonSlidesDown(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 844, false)
	fullpOpenPlayer(t, tab)
	tab.Eval(riseRecord, nil)
	playerClickSel(t, tab, "#player-full [data-ts-close-player]")
	frames := riseFrames(tab)
	sawMiddle, closed := false, false
	for _, f := range frames {
		if f.Open && f.Y > 20 && f.Y < 800 {
			sawMiddle = true
		}
		if !f.Open {
			closed = true
		}
	}
	if !sawMiddle || !closed {
		t.Errorf("closing: slid down %v, closed %v; want a slide, then closed", sawMiddle, closed)
	}
}

// A close during the rise, such as Android Back pressed at once, is honoured and not swallowed.
func TestPlayerCloseDuringRise(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 844, false)
	tab.Eval(`document.querySelector("[data-ts-open]").click(); document.querySelector("#player-full [data-ts-close-player]").click()`, nil)
	tab.WaitFor(`document.querySelector("dialog.ts-player[open]") === null`, 3*time.Second)
	tab.Eval(`document.querySelector("[data-ts-open]").click()`, nil)
	tab.WaitFor(`document.querySelector("dialog.ts-player[open]") !== null`, 3*time.Second)
	time.Sleep(600 * time.Millisecond)
	var y float64
	tab.Eval(`new DOMMatrix(getComputedStyle(document.getElementById("player-full")).transform).m42`, &y)
	if y != 0 {
		t.Errorf("the player opens again and rests %v px below its place", y)
	}
}

func TestPlayerReducedMotionIsInstant(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 844, true)
	tab.Eval(riseRecord, nil)
	playerClickSel(t, tab, "[data-ts-open]")
	for _, f := range riseFrames(tab) {
		if f.Open && f.Y != 0 {
			t.Fatalf("reduced motion: opened at %v px, want 0 on the first frame", f.Y)
		}
	}
	tab.Eval(riseRecord, nil)
	playerClickSel(t, tab, "#player-full [data-ts-close-player]")
	for _, f := range riseFrames(tab) {
		if f.Open && f.Y != 0 {
			t.Fatalf("reduced motion: closing moved to %v px", f.Y)
		}
	}
	var open bool
	tab.Eval(fullpDialog+`.open`, &open)
	if open {
		t.Error("reduced motion: still open after close")
	}
}
