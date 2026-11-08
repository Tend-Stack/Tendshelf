//go:build dev

package web

import (
	"fmt"
	"math"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The Shelf view in Chromium (decision 0034): thickness, targets, rows and planks, the fragment that
// loads on scroll, focus, hover and reduced motion, and Home's shelf drawing the same spines.

// shelfviewApp serves a library of 1 h, 10 h and 40 h books, more than one page of them (the page holds 48).
func shelfviewApp(t *testing.T) string {
	t.Helper()
	a := browseOwnerApp(t)
	f := browseLibrary(t, a, "Audiobooks")
	hours := []int64{1, 10, 40}
	for i := 1; i <= 130; i++ {
		librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{
			Title: fmt.Sprintf("Shelf Book %03d", i), Authors: []string{"Imogen Hale"},
			DurationMs: hours[i%3] * hourMs, AddedAt: int64(i) * 1000,
		})
	}
	srv := httptest.NewServer(a.signedIn(t))
	t.Cleanup(srv.Close)
	return srv.URL
}

// shelfviewProbe measures the Library's shelf and returns what the checks compare. Sizes in rem follow
// pages-spines.css: width = 44 px + step * the increment of the breakpoint; one zone is the tallest book plus room.
const shelfviewProbe = `(() => {
  const vw = window.innerWidth;
  const inc = vw >= 1280 ? 6.4 : vw >= 768 ? 5.6 : 4.8;
  const hmin = vw >= 1280 ? 10.5 : vw >= 768 ? 10 : 9.25;
  const hinc = vw >= 1280 ? 9 : 8;
  const zone = hmin * 16 + 4 * hinc + 8;
  const tile = zone + 8 + 28;
  const ul = document.querySelector("ul#library-grid");
  const box = ul.getBoundingClientRect();
  const slots = Array.from(ul.querySelectorAll(":scope > li.ts-shelf__slot"));
  const out = { slots: slots.length, badWidth: [], small: [], badBottom: [], tiny: [], rows: 0, nested: ul.querySelectorAll("ul").length,
    overflow: document.documentElement.scrollWidth - document.documentElement.clientWidth, userSelect: getComputedStyle(ul).userSelect,
    zone, tile, height: box.height, maxBottom: 0, more: ul.querySelector(":scope > li.ts-shelf__more") ? 1 : 0 };
  for (const li of slots) {
    const r = li.getBoundingClientRect();
    const step = Number((li.className.match(/ts-spine--w(\d+)/) || [])[1]);
    if (Math.abs(r.width - (44 + (step - 1) * inc)) > 0.5) out.badWidth.push([li.textContent.trim().slice(0, 20), step, r.width]);
    const a = li.querySelector("a.ts-spine").getBoundingClientRect();
    if (a.width < 43.99 || a.height < 43.99) out.small.push([a.width, a.height]);
    const rel = r.bottom - box.top - zone;
    const k = Math.round(rel / tile);
    if (Math.abs(rel - k * tile) > 1) out.badBottom.push([rel, tile]);
    out.maxBottom = Math.max(out.maxBottom, k);
    for (const el of li.querySelectorAll(".ts-spine__title, .ts-spine__author")) {
      if (parseFloat(getComputedStyle(el).fontSize) < 13) out.tiny.push(el.className);
    }
  }
  out.rows = out.maxBottom + 1;
  return out;
})()`

type shelfviewState struct {
	Slots     int         `json:"slots"`
	BadWidth  [][]any     `json:"badWidth"`
	Small     [][]float64 `json:"small"`
	BadBottom [][]float64 `json:"badBottom"`
	Tiny      []string    `json:"tiny"`
	Rows      int         `json:"rows"`
	Nested    int         `json:"nested"`
	Overflow  float64     `json:"overflow"`
	UserSel   string      `json:"userSelect"`
	Zone      float64     `json:"zone"`
	Tile      float64     `json:"tile"`
	Height    float64     `json:"height"`
	More      int         `json:"more"`
}

func shelfviewCheck(t *testing.T, w int, s shelfviewState) {
	t.Helper()
	if s.Slots < 48 {
		t.Errorf("%d px: %d spines", w, s.Slots)
	}
	if len(s.BadWidth) > 0 {
		t.Errorf("%d px: widths differ from the step table: %v", w, s.BadWidth[:min(3, len(s.BadWidth))])
	}
	if len(s.Small) > 0 {
		t.Errorf("%d px: links under 44 x 44: %v", w, s.Small[:min(3, len(s.Small))])
	}
	if len(s.BadBottom) > 0 {
		t.Errorf("%d px: slots off the row rule: %v", w, s.BadBottom[:min(3, len(s.BadBottom))])
	}
	if len(s.Tiny) > 0 {
		t.Errorf("%d px: text under 13 px: %v", w, s.Tiny[:1])
	}
	if s.Overflow > 0 {
		t.Errorf("%d px: the page scrolls sideways by %.0f px", w, s.Overflow)
	}
	if s.UserSel != "none" {
		t.Errorf("%d px: user-select %q", w, s.UserSel)
	}
	// The list ends in a plank: rows of one zone, a plank and air between, and the last plank in the padding.
	rows := s.Rows + s.More // the sentinel stands in a row of its own
	if want := float64(rows)*s.Zone + float64(rows-1)*36 + 8; math.Abs(s.Height-want) > 1.5 {
		t.Errorf("%d px: the list is %.1f px high, want %.1f for %d rows", w, s.Height, want, rows)
	}
}

func TestShelfViewBrowser(t *testing.T) {
	base := shelfviewApp(t)
	b := chromium(t)
	for _, w := range []int{390, 768, 1024, 1440} {
		tab := b.NewTab(w, 900, w < 768, false)
		tab.Navigate(base + "/libraries?view=shelf")
		tab.WaitFor(`document.querySelectorAll("ul#library-grid > li.ts-shelf__slot").length >= 48`, 10*time.Second)
		var s shelfviewState
		tab.Eval(shelfviewProbe, &s)
		t.Logf("%d px: %d spines in %d rows", w, s.Slots, s.Rows)
		shelfviewCheck(t, w, s)

		// The next page joins the same list: no nested list, the row rule holds, and the sentinel goes.
		tab.Eval(`document.querySelector("li.ts-shelf__more").scrollIntoView()`, nil)
		tab.WaitFor(`document.querySelectorAll("ul#library-grid > li.ts-shelf__slot").length > 48`, 10*time.Second)
		tab.WaitFor(`document.querySelectorAll("ul#library-grid > li.ts-shelf__slot").length >= 96`, 10*time.Second)
		tab.Eval(shelfviewProbe, &s)
		if s.Nested != 0 {
			t.Errorf("%d px: the loaded page nests %d lists", w, s.Nested)
		}
		shelfviewCheck(t, w, s)
		tab.ScreenshotFull(filepath.Join(visualDir(t, "chromium"), fmt.Sprintf("shelf-loadmore-%d.png", w)))
		if s.Slots <= 48 {
			t.Errorf("%d px: scrolling loaded nothing", w)
		}
		tab.Close()
	}

	// Zoomed to 200 % and beyond: no sideways scroll.
	for _, w := range []int{195, 160} {
		tab := b.NewTab(w, 900, true, false)
		tab.Navigate(base + "/libraries?view=shelf")
		tab.WaitFor(`document.querySelectorAll("ul#library-grid > li.ts-shelf__slot").length >= 48`, 10*time.Second)
		var over float64
		tab.Eval(`document.documentElement.scrollWidth - document.documentElement.clientWidth`, &over)
		if over > 0 {
			t.Errorf("%d px: the page scrolls sideways by %.0f px", w, over)
		}
		tab.Close()
	}
}

// shelfviewFocusHover Tabs to the first spine and hovers it; it returns the book's box-shadow after the
// Tab and its transform after the hover and after the Tab.
func shelfviewFocusHover(t *testing.T, tab *cdp.Tab) (shadow, hoverTransform, focusTransform string) {
	t.Helper()
	tab.Eval(`document.body.focus()`, nil)
	reached := false
	for i := 0; i < 80 && !reached; i++ {
		if err := a11yTab(tab); err != nil {
			t.Fatal(err)
		}
		tab.Eval(`document.activeElement && document.activeElement.matches("a.ts-spine")`, &reached)
	}
	if !reached {
		t.Fatal("Tab never reached a spine")
	}
	tab.WaitFor(`getComputedStyle(document.activeElement).boxShadow !== "none"`, 3*time.Second)
	tab.Eval(`getComputedStyle(document.activeElement).boxShadow`, &shadow)
	time.Sleep(300 * time.Millisecond)
	tab.Eval(`getComputedStyle(document.activeElement.querySelector(".ts-spine__book")).transform`, &focusTransform)
	// Hover a different spine, so focus does not decide the answer.
	var x, y float64
	tab.Eval(`(() => { const r = document.querySelectorAll("a.ts-spine")[3].getBoundingClientRect(); return r.left + r.width / 2; })()`, &x)
	tab.Eval(`(() => { const r = document.querySelectorAll("a.ts-spine")[3].getBoundingClientRect(); return r.top + r.height / 2; })()`, &y)
	if err := tab.Call("Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": x, "y": y}, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	tab.Eval(`getComputedStyle(document.querySelectorAll("a.ts-spine")[3].querySelector(".ts-spine__book")).transform`, &hoverTransform)
	return shadow, hoverTransform, focusTransform
}

func TestShelfViewMotionBrowser(t *testing.T) {
	base := shelfviewApp(t)
	b := chromium(t)

	tab := b.NewTab(1440, 900, false, false)
	tab.Navigate(base + "/libraries?view=shelf")
	tab.WaitFor(`document.querySelectorAll("a.ts-spine").length >= 48`, 10*time.Second)
	shadow, hover, focus := shelfviewFocusHover(t, tab)
	if shadow == "none" || shadow == "" {
		t.Error("a focused spine has no ring")
	}
	var canHover bool
	tab.Eval(`matchMedia("(hover: hover)").matches`, &canHover)
	if !canHover {
		t.Log("headless Chromium reports (hover: hover) false: the hover rule is not exercised here")
		hover = "lifted"
	}
	if hover == "none" || focus == "none" {
		t.Errorf("motion on: hover transform %q, focus transform %q; the book should lift", hover, focus)
	}
	tab.Close()

	tab = b.NewTab(1440, 900, false, true)
	tab.Navigate(base + "/libraries?view=shelf")
	tab.WaitFor(`document.querySelectorAll("a.ts-spine").length >= 48`, 10*time.Second)
	shadow, hover, focus = shelfviewFocusHover(t, tab)
	if shadow == "none" || shadow == "" {
		t.Error("reduced motion: a focused spine has no ring")
	}
	if !canHover {
		hover = "none"
	}
	if hover != "none" || focus != "none" {
		t.Errorf("reduced motion: hover transform %q, focus transform %q, want none", hover, focus)
	}
	tab.Close()
}

// TestShelfViewHomeBrowser: Home's shelf is the same .ts-shelf and gives the same book the same width.
func TestShelfViewHomeBrowser(t *testing.T) {
	base := shelfviewApp(t)
	b := chromium(t)
	const widths = `(() => { const m = {}; for (const li of document.querySelectorAll(".ts-shelf > li.ts-shelf__slot")) { if (li.hidden) continue; m[li.querySelector("a").getAttribute("href")] = li.getBoundingClientRect().width; } return m; })()`
	for _, w := range []int{390, 1440} {
		home := b.NewTab(w, 900, w < 768, false)
		home.Navigate(base + "/")
		home.WaitFor(`document.querySelector("#row-recent .ts-shelf > [hidden]") !== null`, 10*time.Second)
		var onHome, onShelf map[string]float64
		home.Eval(widths, &onHome)
		var shelfClass bool
		home.Eval(`document.querySelector("#row-recent ul.ts-shelf.ts-shelf--scroll[data-ts-fit]") !== null && document.querySelector(".ts-shelf__plank") === null`, &shelfClass)
		if !shelfClass {
			t.Errorf("%d px: Home's shelf is not a ul.ts-shelf with data-ts-fit", w)
		}
		lib := b.NewTab(w, 900, w < 768, false)
		lib.Navigate(base + "/libraries?view=shelf&sort=added")
		lib.WaitFor(`document.querySelectorAll("ul#library-grid > li.ts-shelf__slot").length >= 48`, 10*time.Second)
		lib.Eval(widths, &onShelf)
		same := 0
		for href, hw := range onHome {
			if lw, ok := onShelf[href]; ok {
				same++
				if math.Abs(lw-hw) > 0.5 {
					t.Errorf("%d px: %s is %.1f px on Home and %.1f px on the Library", w, href, hw, lw)
				}
			}
		}
		if same == 0 {
			t.Errorf("%d px: no book is on both shelves", w)
		}
		home.Close()
		lib.Close()
	}
}
