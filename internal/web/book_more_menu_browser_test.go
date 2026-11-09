//go:build dev

package web

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
)

const bookMoreSeriesName = "A Court of Thorns and Roses and the Very Long Chronicle of Everything That Followed"

// bookMoreProbeJS measures the three-dot button, its neighbours and the panel (the menu must be open for the panel).
const bookMoreProbeJS = `(() => {
  const r = (el) => { const b = el.getBoundingClientRect(); return {x: b.x, y: b.y, w: b.width, h: b.height, r: b.right, b: b.bottom}; };
  const btn = document.querySelector(".ts-moremenu > summary");
  const shelf = document.querySelector(".ts-book__shelf");
  const body = document.querySelector(".ts-moremenu__body");
  const items = [...document.querySelectorAll(".ts-moremenu__body a")].map((a) => a.getBoundingClientRect().height);
  const cells = [...document.querySelectorAll(".ts-book__acts > *")].filter((c) => c.offsetParent).map((c) => c.getBoundingClientRect().width);
  return {btn: r(btn), shelf: r(shelf), open: document.querySelector(".ts-moremenu").open,
    panel: body && body.offsetParent ? r(body) : null, items, cells, vw: innerWidth, vh: innerHeight,
    overflow: document.documentElement.scrollWidth > innerWidth};
})()`

// bookMoreSeriesJS measures the series line above the title and the chip row.
const bookMoreSeriesJS = `(() => {
  const p = document.querySelector(".ts-book__overline");
  if (!p || !p.offsetParent) return {shown: false};
  const name = p.querySelector(".ts-book__ovname");
  const keep = p.querySelector(".ts-book__ovkeep");
  const pr = p.getBoundingClientRect(), kr = keep.getBoundingClientRect();
  const tops = [...document.querySelectorAll(".ts-book__chips > li")].map((li) => Math.round(li.getBoundingClientRect().top));
  return {shown: true, cut: name.scrollWidth > name.clientWidth, keepWhole: keep.scrollWidth <= keep.clientWidth + 1,
    keepInside: kr.left >= pr.left - 0.5 && kr.right <= pr.right + 0.5, keepText: keep.textContent,
    oneLine: p.getBoundingClientRect().height < 2 * parseFloat(getComputedStyle(p).lineHeight), fits: p.scrollWidth <= p.clientWidth + 1,
    title: p.querySelector("a").title, chipRows: new Set(tops).size, chips: tops.length};
})()`

type bookMoreProbe struct {
	Btn, Shelf struct{ X, Y, W, H, R, B float64 }
	Open       bool
	Panel      *struct{ X, Y, W, H, R, B float64 }
	Items      []float64
	Cells      []float64
	VW, VH     float64
	Overflow   bool
}

type bookMoreSeries struct {
	Shown, Cut, KeepWhole, KeepInside, OneLine, Fits bool
	KeepText, Title                                  string
	ChipRows, Chips                                  int
}

// TestBookMoreMenuBrowser opens the three-dot menu on the book page at 360, 390, 1024 and 1440: the button is as tall
// as Add to shelf and at least 44 px square, the panel stays inside the screen, Escape closes it, Change cover opens
// its sheet and closes the menu. At 360 and 390 the long series line is cut but "Book 5 of 5" stays whole and the
// chips share one row. Screenshots go to TENDSHELF_MORE_SHOTS when it is set. Needs TENDSHELF_TEST_BROWSER.
func TestBookMoreMenuBrowser(t *testing.T) {
	b := chromium(t)
	shots := os.Getenv("TENDSHELF_MORE_SHOTS")
	for _, vp := range []struct {
		name   string
		w, h   int
		mobile bool
	}{{"w360", 360, 800, true}, {"w390", 390, 844, true}, {"w1024", 1024, 800, false}, {"w1440", 1440, 900, false}} {
		t.Run(vp.name, func(t *testing.T) {
			l := newCoverLab(t)
			l.start(t)
			l.a.srv.details = metadata.New(l.a.lib, l.a.runner, slog.New(slog.NewTextHandler(io.Discard, nil)))
			folder := browseLibrary(t, l.a, "Long series")
			long := librarytest.AddBook(t, l.a.lib, folder, librarytest.BookSpec{Title: "The Long Series Book", Authors: []string{"Ann Author"},
				Series: bookMoreSeriesName, Sequence: "5", DurationMs: 73_560_000, Files: 1, PublishedYear: 2023})
			base := themedServer(t, l.a.signedIn(t))
			tab := b.NewTab(vp.w, vp.h, vp.mobile, true)
			shot := func(name string) {
				if shots != "" {
					tab.Screenshot(filepath.Join(shots, name+"-"+vp.name+".png"))
				}
			}
			tab.Navigate(base + "/books/" + long.ID + "?theme=dark")
			tab.WaitFor(`window.htmx !== undefined && document.querySelector(".ts-moremenu") !== null`, 5*time.Second)
			shot("closed")
			// The tabs stay on one row: none is left alone on a new line (operator, 2026-11-09).
			var tabRows int
			tab.Eval(`new Set([...document.querySelectorAll("#book-tabs .ts-tabs__item")].filter((el) => el.offsetParent !== null).map((el) => Math.round(el.getBoundingClientRect().top))).size`, &tabRows)
			if tabRows != 1 {
				t.Errorf("the book's tabs take %d rows", tabRows)
			}
			var pr bookMoreProbe
			tab.Eval(bookMoreProbeJS, &pr)
			if pr.Btn.H < 43.5 || pr.Btn.W < 43.5 {
				t.Errorf("button is %.1f x %.1f, want at least 44 x 44", pr.Btn.W, pr.Btn.H)
			}
			if d := pr.Btn.H - pr.Shelf.H; d > 1 || d < -1 {
				t.Errorf("button height %.1f differs from Add to shelf %.1f", pr.Btn.H, pr.Shelf.H)
			}
			if vp.w < 768 {
				for _, c := range pr.Cells {
					if d := c - pr.Cells[0]; d > 1.5 || d < -1.5 {
						t.Errorf("phone action cells are uneven: %v", pr.Cells)
						break
					}
				}
			}
			if vp.w < 768 || vp.w >= 1280 {
				var se bookMoreSeries
				tab.Eval(bookMoreSeriesJS, &se)
				if !se.Shown || !se.Cut || !se.KeepWhole || !se.KeepInside || !se.OneLine || !se.Fits || se.KeepText != "· Book 5" || se.Title == "" {
					t.Errorf("series line: %+v", se)
				}
				if se.ChipRows != 1 {
					t.Errorf("chips are on %d rows (%d chips), want one", se.ChipRows, se.Chips)
				}
				if vp.w == 360 {
					shot("series")
				}
			}

			tab.Eval(`document.querySelector(".ts-moremenu > summary").click()`, nil)
			tab.WaitFor(`document.querySelector(".ts-moremenu").open`, 2*time.Second)
			tab.Eval(bookMoreProbeJS, &pr)
			if pr.Panel == nil {
				t.Fatal("the panel is not drawn")
			}
			if pr.Panel.X < 0 || pr.Panel.R > pr.VW || pr.Panel.Y < 0 || pr.Panel.B > pr.VH {
				t.Errorf("panel %+v leaves the %.0f x %.0f screen", *pr.Panel, pr.VW, pr.VH)
			}
			for _, h := range pr.Items {
				if h < 43.5 {
					t.Errorf("a menu item is %.1f px tall", h)
				}
			}
			if len(pr.Items) != 4 {
				t.Errorf("menu items = %d, want 4 (series, Share, cover, details)", len(pr.Items))
			}
			if pr.Overflow {
				t.Error("the open menu makes the page scroll sideways")
			}
			shot("open")
			if vp.name == "w390" {
				tab.Navigate(base + "/books/" + long.ID + "?theme=light")
				tab.WaitFor(`document.querySelector(".ts-moremenu") !== null`, 5*time.Second)
				tab.Eval(`document.querySelector(".ts-moremenu > summary").click()`, nil)
				tab.WaitFor(`document.querySelector(".ts-moremenu").open`, 2*time.Second)
				if shots != "" {
					tab.Screenshot(filepath.Join(shots, "open-light-"+vp.name+".png"))
				}
			}

			tab.Eval(`document.dispatchEvent(new KeyboardEvent("keydown", {key: "Escape", bubbles: true}))`, nil)
			tab.WaitFor(`!document.querySelector(".ts-moremenu").open`, 2*time.Second)
			var focused bool
			tab.Eval(`document.activeElement === document.querySelector(".ts-moremenu > summary")`, &focused)
			if !focused {
				t.Error("Escape did not put focus back on the button")
			}

			tab.Eval(`document.querySelector(".ts-moremenu > summary").click()`, nil)
			tab.Eval(`document.querySelector(".ts-moremenu a[aria-haspopup=dialog]").click()`, nil)
			tab.WaitFor(`document.querySelector("#sheet form.ts-cover-pick__upload") !== null`, 5*time.Second)
			var stillOpen bool
			tab.Eval(`document.querySelector(".ts-moremenu").open`, &stillOpen)
			if stillOpen {
				t.Error("the menu stays open behind the cover sheet")
			}
		})
	}
}

// TestBookMoreMenuNoSeriesBrowser: a book with no series draws no series line, and its chips share one row.
func TestBookMoreMenuNoSeriesBrowser(t *testing.T) {
	b := chromium(t)
	l := newCoverLab(t)
	l.start(t)
	base := themedServer(t, l.a.signedIn(t))
	for _, w := range []int{360, 390} {
		tab := b.NewTab(w, 800, true, true)
		tab.Navigate(base + "/books/" + l.book.ID + "?theme=dark")
		tab.WaitFor(`window.htmx !== undefined`, 5*time.Second)
		var se bookMoreSeries
		tab.Eval(bookMoreSeriesJS, &se)
		if se.Shown {
			t.Errorf("%d px: a book with no series shows a series line", w)
		}
		var rows int
		tab.Eval(`new Set([...document.querySelectorAll(".ts-book__chips > li")].map((li) => Math.round(li.getBoundingClientRect().top))).size`, &rows)
		if rows > 1 {
			t.Errorf("%d px: chips on %d rows", w, rows)
		}
	}
}
