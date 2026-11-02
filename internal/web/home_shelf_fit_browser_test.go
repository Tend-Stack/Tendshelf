//go:build dev

package web

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The Home shelf in Chromium: the server sends the 40 newest books and shell.js shows only the
// spines that fit whole on one row (make visual runs it with the other browser tests).

// shelffitApp serves Home with 45 books of every spine width, signed in as the owner.
func shelffitApp(t *testing.T) string {
	t.Helper()
	a := browseOwnerApp(t)
	f := browseLibrary(t, a, "Audiobooks")
	hours := []int64{3, 9, 20} // a short, a medium and a long book: the three spine widths
	for i := 1; i <= 45; i++ {
		librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{
			Title: fmt.Sprintf("Book %03d", i), Authors: []string{"Imogen Hale"},
			DurationMs: hours[i%3] * hourMs, AddedAt: int64(i) * 1000,
		})
	}
	srv := httptest.NewServer(a.signedIn(t))
	t.Cleanup(srv.Close)
	return srv.URL
}

// shelffitState is what the shelf looks like right now.
type shelffitState struct {
	Spines       int     `json:"spines"`
	Visible      int     `json:"visible"`
	ScrollWidth  float64 `json:"scrollWidth"`
	ClientWidth  float64 `json:"clientWidth"`
	Outside      int     `json:"outside"`      // visible spines not fully inside the shelf
	HiddenFits   bool    `json:"hiddenFits"`   // the first hidden spine would have fitted
	HiddenInTabs int     `json:"hiddenInTabs"` // hidden spines still rendered (reachable by keyboard)
}

const shelffitProbe = `(() => {
  const shelf = document.querySelector("#row-recent .ts-shelf");
  const spines = Array.from(shelf.children);
  const box = shelf.getBoundingClientRect();
  const style = getComputedStyle(shelf);
  const gap = parseFloat(style.columnGap) || 0;
  const room = shelf.clientWidth - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight);
  const visible = spines.filter((s) => !s.hidden);
  const hidden = spines.filter((s) => s.hidden);
  let used = 0;
  visible.forEach((s, i) => { used += s.getBoundingClientRect().width + (i > 0 ? gap : 0); });
  let hiddenFits = false;
  if (hidden.length > 0) {
    hiddenFits = used + gap + parseFloat(getComputedStyle(hidden[0]).width) <= room;
  }
  return {
    spines: spines.length,
    visible: visible.length,
    scrollWidth: shelf.scrollWidth,
    clientWidth: shelf.clientWidth,
    outside: visible.filter((s) => {
      const r = s.getBoundingClientRect();
      return r.left < box.left - 0.01 || r.right > box.right + 0.01;
    }).length,
    hiddenFits,
    hiddenInTabs: hidden.filter((s) => s.getBoundingClientRect().width > 0).length,
  };
})()`

// shelffitSettled waits until the fitter has hidden something, then reads the shelf.
func shelffitSettled(t *testing.T, tab *cdp.Tab) shelffitState {
	t.Helper()
	tab.WaitFor(`document.querySelector("#row-recent .ts-shelf > [hidden]") !== null`, 10*time.Second)
	var s shelffitState
	tab.Eval(shelffitProbe, &s)
	return s
}

func shelffitCheck(t *testing.T, w int, s shelffitState) {
	t.Helper()
	if s.Spines != 40 {
		t.Errorf("%d px: the server sent %d spines, want 40", w, s.Spines)
	}
	if s.Visible < 1 || s.Visible >= s.Spines {
		t.Errorf("%d px: %d of %d spines visible", w, s.Visible, s.Spines)
	}
	if s.ScrollWidth > s.ClientWidth {
		t.Errorf("%d px: the shelf scrolls sideways (scrollWidth %.0f > clientWidth %.0f)", w, s.ScrollWidth, s.ClientWidth)
	}
	if s.Outside != 0 {
		t.Errorf("%d px: %d visible spines are not fully inside the shelf", w, s.Outside)
	}
	if s.HiddenFits {
		t.Errorf("%d px: the first hidden spine would have fitted", w)
	}
	if s.HiddenInTabs != 0 {
		t.Errorf("%d px: %d hidden spines still take up space", w, s.HiddenInTabs)
	}
}

func TestHomeShelfFitBrowser(t *testing.T) {
	base := shelffitApp(t)
	b := chromium(t)
	visible := map[int]int{}
	for _, w := range []int{390, 768, 1024, 1440, 1920} {
		tab := b.NewTab(w, 900, w < 768, false)
		tab.Navigate(base + "/")
		s := shelffitSettled(t, tab)
		t.Logf("%d px: %d of %d spines, shelf %.0f px", w, s.Visible, s.Spines, s.ClientWidth)
		shelffitCheck(t, w, s)
		visible[w] = s.Visible
	}
	if visible[1920] <= visible[390] {
		t.Errorf("%d spines at 1920 px, %d at 390 px: the wide window should show more", visible[1920], visible[390])
	}

	// A window that shrinks re-fits without a reload.
	tab := b.NewTab(1440, 900, false, false)
	tab.Navigate(base + "/")
	if s := shelffitSettled(t, tab); s.Visible != visible[1440] {
		t.Fatalf("1440 px: %d spines, want %d", s.Visible, visible[1440])
	}
	tab.Eval(`window.__shelfBefore = 1`, nil)
	if err := tab.Call("Emulation.setDeviceMetricsOverride", map[string]any{"width": 390, "height": 900, "deviceScaleFactor": 1, "mobile": true}, nil); err != nil {
		t.Fatal(err)
	}
	tab.WaitFor(fmt.Sprintf(`document.querySelectorAll("#row-recent .ts-shelf > :not([hidden])").length === %d`, visible[390]), 10*time.Second)
	var s shelffitState
	tab.Eval(shelffitProbe, &s)
	shelffitCheck(t, 390, s)
	var same bool
	tab.Eval(`window.__shelfBefore === 1`, &same)
	if !same {
		t.Errorf("the page reloaded instead of re-fitting")
	}
}
