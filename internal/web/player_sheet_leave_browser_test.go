//go:build dev

package web

import (
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// A sheet inside the full player sinks when it closes (the same is-leaving rules as the #sheet sheets).

const psxWrap = `document.querySelector("#player-full [data-ts-chapters-sheet]")`
const psxCloseX = `#player-full [data-ts-chapters-sheet] .ts-iconbtn[data-ts-sheet-close]`
const psxBody = `document.querySelector("#player-full [data-ts-full-body]")`

// psxRecord samples the sheet on every frame, 60 frames from now.
const psxRecord = `(() => {
  const wrap = ` + psxWrap + `;
  const sheet = wrap.querySelector(".ts-sheet");
  window.__psx = [];
  const tick = () => {
    window.__psx.push({Hidden: wrap.hidden, Top: sheet.getBoundingClientRect().top, Inert: ` + psxBody + `.inert});
    if (window.__psx.length < 60) requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
})()`

type psxSample struct {
	Hidden bool
	Top    float64
	Inert  bool
}

// psxOpenSheet opens the player and its chapters sheet and waits for the rise to finish.
func psxOpenSheet(t *testing.T, reduced bool) (*cdp.Tab, float64) {
	t.Helper()
	tab := fullpOpen(t, playerApp(t), 390, 800, reduced)
	fullpOpenPlayer(t, tab)
	playerClickSel(t, tab, "[data-ts-open-chapters]")
	tab.WaitFor(`!`+psxWrap+`.hidden`, 2*time.Second)
	time.Sleep(500 * time.Millisecond)
	var top float64
	tab.Eval(psxWrap+`.querySelector(".ts-sheet").getBoundingClientRect().top`, &top)
	return tab, top
}

func psxFrames(tab *cdp.Tab) (f []psxSample) {
	time.Sleep(900 * time.Millisecond)
	tab.Eval(`window.__psx`, &f)
	return f
}

func TestPlayerSheetSinksOnClose(t *testing.T) {
	tab, open := psxOpenSheet(t, false)
	tab.Eval(psxRecord, nil)
	playerClickSel(t, tab, psxCloseX)
	frames := psxFrames(tab)
	mid := map[int]bool{}
	for _, f := range frames {
		if !f.Hidden && f.Top > open+1 {
			mid[int(f.Top)] = true
		}
	}
	if len(mid) < 3 {
		t.Errorf("only %d distinct positions while the sheet was still visible and below its place, want a sink of at least 3", len(mid))
	}
	last := frames[len(frames)-1]
	if !last.Hidden || last.Inert {
		t.Errorf("after the sink: %+v, want the sheet hidden and the player body live", last)
	}
	var focusBack bool
	tab.Eval(`document.activeElement === document.querySelector("[data-ts-open-chapters]")`, &focusBack)
	if !focusBack {
		t.Error("focus did not return to the Chapters item")
	}
}

func TestPlayerSheetReopenDuringLeave(t *testing.T) {
	tab, open := psxOpenSheet(t, false)
	tab.Eval(`document.querySelector(`+"`"+psxCloseX+"`"+`).click(); setTimeout(() => document.querySelector("[data-ts-open-chapters]").click(), 60)`, nil)
	time.Sleep(700 * time.Millisecond)
	var st struct {
		Hidden  bool
		Leaving int
		Top     float64
		Inert   bool
	}
	tab.Eval(`({Hidden: `+psxWrap+`.hidden, Leaving: `+psxWrap+`.querySelectorAll(".is-leaving").length, Top: `+psxWrap+`.querySelector(".ts-sheet").getBoundingClientRect().top, Inert: `+psxBody+`.inert})`, &st)
	if st.Hidden || st.Leaving != 0 || !st.Inert || st.Top > open+1 || st.Top < open-1 {
		t.Errorf("reopened during the leave: %+v, want shown at %v, not leaving, body inert", st, open)
	}
}

func TestPlayerSheetCloseWithPlayerLeavesNothing(t *testing.T) {
	tab, _ := psxOpenSheet(t, false)
	tab.Eval(`document.querySelector(`+"`"+psxCloseX+"`"+`).click(); setTimeout(() => document.querySelector("#player-full [data-ts-close-player]").click(), 60)`, nil)
	time.Sleep(900 * time.Millisecond)
	var st struct {
		Hidden  bool
		Leaving int
		Inert   bool
	}
	tab.Eval(`({Hidden: `+psxWrap+`.hidden, Leaving: `+psxWrap+`.querySelectorAll(".is-leaving").length, Inert: `+psxWrap+`.inert || `+psxBody+`.inert})`, &st)
	if !st.Hidden || st.Leaving != 0 || st.Inert {
		t.Errorf("after closing the player mid-leave: %+v, want hidden, nothing leaving, nothing inert", st)
	}
}

func TestPlayerSheetReducedMotion(t *testing.T) {
	tab, open := psxOpenSheet(t, true)
	tab.Eval(psxRecord, nil)
	playerClickSel(t, tab, psxCloseX)
	frames := psxFrames(tab)
	for _, f := range frames {
		if !f.Hidden && (f.Top < open-1 || f.Top > open+1) {
			t.Fatalf("reduced motion: the sheet moved to %v (rest %v) before it hid", f.Top, open)
		}
	}
	if last := frames[len(frames)-1]; !last.Hidden {
		t.Error("reduced motion: the sheet stayed visible")
	}
}
