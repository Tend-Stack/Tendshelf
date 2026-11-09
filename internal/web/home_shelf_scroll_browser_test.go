//go:build dev

package web

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

// The Home shelf in Chromium: the 40 newest books on one row that scrolls sideways and runs to the
// edge of its space, however wide that is (make visual runs it with the other browser tests).

// shelfscrollApp serves Home with 45 books of every spine width, signed in as the owner.
func shelfscrollApp(t *testing.T) string {
	t.Helper()
	a := browseOwnerApp(t)
	f := browseLibrary(t, a, "Audiobooks")
	hours := []int64{3, 9, 20} // a short, a medium and a long book
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

// shelfscrollProbe scrolls the shelf to its end and back, and reports what it found.
const shelfscrollProbe = `(() => {
  const shelf = document.querySelector("#row-recent .ts-shelf");
  const spines = Array.from(shelf.children);
  const right = shelf.getBoundingClientRect().right;
  const gap = right - Math.max(...spines.map((s) => s.getBoundingClientRect().left).filter((x) => x < right));
  const reachesEdge = spines.some((s) => {
    const r = s.getBoundingClientRect();
    return r.left < right && r.right >= right;
  });
  shelf.scrollLeft = shelf.scrollWidth;
  const last = spines[spines.length - 1].getBoundingClientRect();
  const lastShown = last.right <= shelf.getBoundingClientRect().right + 1;
  shelf.scrollLeft = 0;
  return {
    spines: spines.length,
    hidden: spines.filter((s) => s.hidden).length,
    scrolls: shelf.scrollWidth > shelf.clientWidth,
    reachesEdge,
    lastShown,
    pageOverflow: document.documentElement.scrollWidth > innerWidth,
    gap,
  };
})()`

type shelfscrollState struct {
	Spines       int     `json:"spines"`
	Hidden       int     `json:"hidden"`
	Scrolls      bool    `json:"scrolls"`
	ReachesEdge  bool    `json:"reachesEdge"`
	LastShown    bool    `json:"lastShown"`
	PageOverflow bool    `json:"pageOverflow"`
	Gap          float64 `json:"gap"`
}

func TestHomeShelfScrollBrowser(t *testing.T) {
	base := shelfscrollApp(t)
	b := chromium(t)
	for _, w := range []int{390, 768, 1024, 1440, 1920} {
		tab := b.NewTab(w, 900, w < 768, false)
		tab.Navigate(base + "/")
		tab.WaitFor(`document.querySelector("#row-recent .ts-shelf > li") !== null`, 10*time.Second)
		check := func(when string) {
			t.Helper()
			var s shelfscrollState
			tab.Eval(shelfscrollProbe, &s)
			if s.Spines != 40 || s.Hidden != 0 {
				t.Errorf("%d px, %s: %d spines, %d hidden; want 40 and 0", w, when, s.Spines, s.Hidden)
			}
			if !s.Scrolls {
				t.Errorf("%d px, %s: the shelf does not scroll sideways", w, when)
			}
			if !s.ReachesEdge {
				t.Errorf("%d px, %s: empty plank at the end of the row (%.0f px past the last book's start)", w, when, s.Gap)
			}
			if !s.LastShown {
				t.Errorf("%d px, %s: scrolling does not reach the last book", w, when)
			}
			if s.PageOverflow {
				t.Errorf("%d px, %s: the page itself scrolls sideways", w, when)
			}
		}
		check("full width")
		// The side panel takes room from the page the same way: .ts-main gains padding on the right.
		if w >= 1280 {
			tab.Eval(`document.querySelector(".ts-main").style.paddingRight = "28rem"`, nil)
			check("beside the panel")
		}
		tab.Close()
	}
}
