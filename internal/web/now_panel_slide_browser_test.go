//go:build dev

package web

import (
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The Now playing side panel eases in and out on wide screens (player.css, side-panel media query).

// npSlideRecord samples the dialog and the page on every frame, 150 frames from now.
const npSlideRecord = `(() => {
  const d = document.getElementById("player-full");
  const m = document.querySelector(".ts-main");
  window.__np = [];
  const tick = () => {
    const cs = getComputedStyle(d);
    window.__np.push({Shown: cs.display !== "none", Left: d.getBoundingClientRect().left,
      Opacity: parseFloat(cs.opacity), Open: d.open,
      Wide: document.documentElement.scrollWidth > innerWidth, Main: m.clientWidth - parseFloat(getComputedStyle(m).paddingRight)});
    if (window.__np.length < 150) requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
})()`

type npSlideSample struct {
	Shown   bool
	Left    float64
	Opacity float64
	Open    bool
	Wide    bool
	Main    float64
}

func npSlideFrames(tab *cdp.Tab) (f []npSlideSample) {
	time.Sleep(900 * time.Millisecond)
	tab.Eval(`window.__np`, &f)
	return f
}

// npSlideTab opens the Library at 1440 x 900 with the book loaded and the player closed.
func npSlideTab(t *testing.T, reduced bool) *cdp.Tab {
	t.Helper()
	return fullpOpen(t, playerApp(t), 1440, 900, reduced)
}

// TestNowPanelSlidesAtEdges: the slide also holds at the narrowest width that has the side panel (1280 px) and
// on a full HD screen (1920 px): the panel fades and moves in, never past its place, with no sideways scrollbar,
// and is gone after it slid out.
func TestNowPanelSlidesAtEdges(t *testing.T) {
	for _, w := range []int{1280, 1920} {
		tab := fullpOpen(t, playerApp(t), w, 900, false)
		tab.Eval(npSlideRecord, nil)
		playerClickSel(t, tab, "[data-ts-open]")
		var left, op []float64
		for _, f := range npSlideFrames(tab) {
			if f.Wide {
				t.Fatalf("%d px: a horizontal scrollbar appeared while the panel slid in", w)
			}
			if f.Open {
				left, op = append(left, f.Left), append(op, f.Opacity)
			}
		}
		if len(left) < 3 {
			t.Fatalf("%d px: only %d frames with the panel open", w, len(left))
		}
		if npSlideDistinct(left) < 3 || npSlideDistinct(op) < 3 {
			t.Errorf("%d px: the panel took %d places and %d opacities, want a slide and a fade of at least 3", w, npSlideDistinct(left), npSlideDistinct(op))
		}
		end := left[len(left)-1]
		for _, l := range left {
			if l < end-0.01 {
				t.Errorf("%d px: the panel went past its place: left %v, ends at %v", w, l, end)
			}
		}
		if op[len(op)-1] != 1 {
			t.Errorf("%d px: ends at opacity %v, want 1", w, op[len(op)-1])
		}
		if right := end + 400; right > float64(w)+0.5 {
			t.Errorf("%d px: the panel ends at left %v, so its 25rem reaches past the window", w, end)
		}

		tab.Eval(npSlideRecord, nil)
		playerClickSel(t, tab, "#player-full [data-ts-close-player]")
		var mid int
		var last npSlideSample
		for _, f := range npSlideFrames(tab) {
			if f.Wide {
				t.Fatalf("%d px: a horizontal scrollbar appeared while the panel slid out", w)
			}
			if !f.Open && f.Shown && f.Opacity > 0 && f.Opacity < 1 {
				mid++
			}
			last = f
		}
		if mid < 3 {
			t.Errorf("%d px: only %d frames of the panel on its way out, want at least 3", w, mid)
		}
		if last.Shown {
			t.Errorf("%d px: the panel is still displayed after it slid out", w)
		}
	}
}

func npSlideDistinct(vals []float64) int {
	seen := map[int]bool{}
	for _, v := range vals {
		seen[int(v*100)] = true
	}
	return len(seen)
}

func TestNowPanelSlidesIn(t *testing.T) {
	tab := npSlideTab(t, false)
	tab.Eval(npSlideRecord, nil)
	playerClickSel(t, tab, "[data-ts-open]")
	var left, op []float64
	for _, f := range npSlideFrames(tab) {
		if f.Wide {
			t.Fatal("a horizontal scrollbar appeared while the panel slid in")
		}
		if f.Open {
			left, op = append(left, f.Left), append(op, f.Opacity)
		}
	}
	if len(left) < 3 {
		t.Fatalf("only %d frames with the panel open", len(left))
	}
	if n := npSlideDistinct(left); n < 3 {
		t.Errorf("the panel's left edge took %d distinct values, want a slide of at least 3", n)
	}
	if n := npSlideDistinct(op); n < 3 {
		t.Errorf("the panel's opacity took %d distinct values, want a fade of at least 3", n)
	}
	for i := 1; i < len(left); i++ {
		if left[i] > left[i-1]+0.01 {
			t.Errorf("frame %d: the panel moved right (%v then %v) while coming in", i, left[i-1], left[i])
		}
	}
	if op[len(op)-1] != 1 {
		t.Errorf("ends at opacity %v, want 1", op[len(op)-1])
	}
	// The final place is the lowest left edge; it never goes past it.
	end := left[len(left)-1]
	for _, l := range left {
		if l < end-0.01 {
			t.Errorf("the panel went past its place: left %v, ends at %v", l, end)
		}
	}
}

func TestNowPanelSlidesOut(t *testing.T) {
	tab := npSlideTab(t, false)
	playerClickSel(t, tab, "[data-ts-open]")
	time.Sleep(600 * time.Millisecond)
	tab.Eval(npSlideRecord, nil)
	playerClickSel(t, tab, "#player-full [data-ts-close-player]")
	var mid int
	var last npSlideSample
	for _, f := range npSlideFrames(tab) {
		if f.Wide {
			t.Fatal("a horizontal scrollbar appeared while the panel slid out")
		}
		if !f.Open && f.Shown && f.Opacity > 0 && f.Opacity < 1 {
			mid++
		}
		last = f
	}
	if mid < 3 {
		t.Errorf("only %d frames of the panel on its way out, want at least 3", mid)
	}
	if last.Shown {
		t.Error("the panel is still displayed after it slid out")
	}
}

func TestNowPanelSlideReducedMotion(t *testing.T) {
	tab := npSlideTab(t, true)
	tab.Eval(npSlideRecord, nil)
	playerClickSel(t, tab, "[data-ts-open]")
	var opened bool
	var left float64
	for _, f := range npSlideFrames(tab) {
		if !f.Open {
			continue
		}
		if !opened {
			opened, left = true, f.Left
		}
		if f.Opacity != 1 || f.Left != left {
			t.Fatalf("reduced motion: the panel is at %v (opacity %v), want %v and 1 from its first frame", f.Left, f.Opacity, left)
		}
	}
	if !opened {
		t.Fatal("the panel never opened")
	}
	tab.Eval(npSlideRecord, nil)
	playerClickSel(t, tab, "#player-full [data-ts-close-player]")
	for _, f := range npSlideFrames(tab) {
		if !f.Open && f.Shown {
			t.Fatal("reduced motion: the panel stays displayed after it closed")
		}
	}
}

// The page behind the panel steps once (its content width, inside the padding); the library grid must not re-wrap on every frame.
func TestNowPanelContentStepsOnce(t *testing.T) {
	tab := npSlideTab(t, false)
	tab.Eval(npSlideRecord, nil)
	playerClickSel(t, tab, "[data-ts-open]")
	widths := map[int]bool{}
	for _, f := range npSlideFrames(tab) {
		widths[int(f.Main*10)] = true
	}
	if len(widths) != 2 {
		t.Errorf(".ts-main took %d widths across the open, want 2 (before, after)", len(widths))
	}
}
