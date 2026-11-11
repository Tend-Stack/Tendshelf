//go:build dev

package web

import (
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// Up next rows glide to their new places when one moves or goes (now-panel.js).

// uqmRecord notes every row's left plus top on each frame, 60 frames from now, keyed by the row's id.
const uqmRecord = `(() => {
  window.__uqm = []; window.__uqmStop = false;
  const tick = () => {
    const tops = {};
    for (const li of document.querySelectorAll(".ts-upnext__item")) { const b = li.getBoundingClientRect(); tops[li.id] = b.left + b.top; }
    window.__uqm.push(tops);
    if (!window.__uqmStop) requestAnimationFrame(tick);
  };
  requestAnimationFrame(tick);
})()`

func uqmTab(t *testing.T, w int, reduced bool) (*cdp.Tab, []string) {
	t.Helper()
	e := nowPanelEnv(t)
	for _, title := range []string{"Salt Road", "Harbour Dawn", "Harbour Night"} {
		page, err := e.a.lib.ListBooks(t.Context(), bookAllScope, library.ListQuery{Title: title, Limit: 5})
		if err != nil || len(page.Books) == 0 {
			t.Fatalf("book %q: %v", title, err)
		}
		if _, err := e.a.lib.QueueAdd(t.Context(), e.ownerID, page.Books[0].ID, false); err != nil {
			t.Fatal(err)
		}
	}
	tab := fullpOpen(t, e, w, 900, reduced)
	tab.Navigate(e.URL + "/queue")
	tab.WaitFor(`document.querySelectorAll(".ts-upnext__item").length === 3`, 5*time.Second)
	var ids []string
	tab.Eval(`[...document.querySelectorAll(".ts-upnext__item")].map((li) => li.id)`, &ids)
	return tab, ids
}

func uqmMoveFirstDown(t *testing.T, tab *cdp.Tab, ids []string) (frames []map[string]float64) {
	t.Helper()
	tab.Eval(uqmRecord, nil)
	playerClickSel(t, tab, `.ts-upnext__item:nth-child(1) form[action$="/move"] button`)
	time.Sleep(900 * time.Millisecond)
	tab.Eval(`window.__uqmStop = true`, nil)
	time.Sleep(100 * time.Millisecond)
	tab.Eval(`window.__uqm`, &frames)
	tab.WaitFor(`document.querySelectorAll(".ts-upnext__item")[1].id === `+cdp.Quote(ids[0]), 3*time.Second)
	return frames
}

func TestUpNextRowsGlide(t *testing.T) {
	for _, w := range []int{390, 1440} {
		tab, ids := uqmTab(t, w, false)
		frames := uqmMoveFirstDown(t, tab, ids)
		first := frames[0][ids[0]]
		last := frames[len(frames)-1][ids[0]]
		if last <= first+1 {
			t.Fatalf("%d px: the moved row ended at %v from %v, want lower", w, last, first)
		}
		if got := frames[len(frames)-1][ids[1]]; got >= last {
			t.Errorf("%d px: the second row ends at %v, want it above the moved one (%v)", w, got, last)
		}
		mid := map[int]bool{}
		for _, f := range frames {
			if v, ok := f[ids[0]]; ok && v > first+1 && v < last-1 {
				mid[int(v)] = true
			}
		}
		if len(mid) < 3 {
			t.Errorf("%d px: the moved row passed through %d intermediate positions, want a glide of at least 3", w, len(mid))
		}
	}
}

func TestUpNextRowsReducedMotionStepOnce(t *testing.T) {
	tab, ids := uqmTab(t, 390, true)
	frames := uqmMoveFirstDown(t, tab, ids)
	first := frames[0][ids[0]]
	seen := map[int]bool{}
	for _, f := range frames {
		if v, ok := f[ids[0]]; ok {
			seen[int(v)] = true
		}
	}
	if len(seen) != 2 {
		t.Errorf("reduced motion: the moved row took %d positions (from %v), want 2 (before, after)", len(seen), first)
	}
}
