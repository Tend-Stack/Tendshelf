//go:build dev

package web

import (
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// Regression test for the stylesheets of swapped pages (needs TENDSHELF_TEST_BROWSER). htmx drops
// the <head> of a boosted answer, so a page's own stylesheet must travel inside the swapped content.
// Every step here is a real click, and every assertion is a computed style that only the stylesheet
// of the page just reached sets.

// swapstylesClick clicks the first VISIBLE element matching sel: the same link sits in the sidebar,
// the rail and the tab bar, and only one of them shows at a given width.
func swapstylesClick(t *testing.T, tab *cdp.Tab, sel string) {
	t.Helper()
	// Let the previous swap finish (its view transition and history snapshot): a click that lands
	// while it runs is dropped by htmx, which a person's slower click never meets.
	tab.WaitFor(`document.getAnimations().length === 0`, 3*time.Second)
	time.Sleep(300 * time.Millisecond)
	tab.WaitFor(`[...document.querySelectorAll(`+cdp.Quote(sel)+`)].some(e => e.getBoundingClientRect().width > 0)`, 5*time.Second)
	var r struct{ X, Y float64 }
	tab.Eval(`(() => { const e = [...document.querySelectorAll(`+cdp.Quote(sel)+`)].find(e => e.getBoundingClientRect().width > 0);
	  e.scrollIntoView({block: "center"}); const r = e.getBoundingClientRect(); return {X: r.x + r.width / 2, Y: r.y + r.height / 2}; })()`, &r)
	playerClickAt(t, tab, r.X, r.Y)
}

// swapstylesHas waits until the first element matching sel has the computed value for prop. A
// stylesheet that arrived with the swap applies a moment after the content, so this polls.
func swapstylesHas(t *testing.T, tab *cdp.Tab, sel, prop, want string) {
	t.Helper()
	tab.WaitFor(`(() => { const e = document.querySelector(`+cdp.Quote(sel)+`);
	  return e !== null && getComputedStyle(e).`+prop+` === `+cdp.Quote(want)+`; })()`, 5*time.Second)
}

func swapstylesPath(t *testing.T, tab *cdp.Tab, path, ready string) {
	t.Helper()
	cond := `location.pathname === ` + cdp.Quote(path) + ` && ` + ready
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		var ok bool
		if tab.TryEval(`Boolean(`+cond+`)`, &ok) == nil && ok {
			return
		}
		if time.Now().After(deadline) {
			var diag string
			tab.TryEval(`JSON.stringify({path: location.pathname, main: document.getElementById("main").innerHTML.slice(0, 300)})`, &diag)
			t.Fatalf("never reached %s (%s): %s", path, ready, diag)
		}
	}
}

func TestSwapStylesBrowser(t *testing.T) {
	e := playerApp(t)
	for _, vp := range []struct {
		name string
		w, h int
	}{{"desktop-1440", 1440, 900}, {"mobile-390", 390, 844}} {
		t.Run(vp.name, func(t *testing.T) {
			b := chromium(t) // a new browser per viewport: empty cache, so a missing link cannot hide
			tab := b.NewTab(vp.w, vp.h, vp.w < 768, false)
			tab.Navigate(e.URL + "/")
			tab.WaitFor(`document.querySelector(".ts-home") !== null`, 5*time.Second)

			// Search: the chips are spaced and not underlined (pages-search.css).
			swapstylesClick(t, tab, `a[data-nav="/search"]`)
			swapstylesPath(t, tab, "/search", `document.querySelector(".ts-sf") !== null`)
			swapstylesHas(t, tab, ".ts-sf", "rowGap", "12px")
			swapstylesHas(t, tab, ".ts-sf .ts-chip", "textDecorationLine", "none")

			// Profile (pages-me.css). The phone has no Shelves tab: its way to the shelves is the row
			// on the Profile page. The desktop sidebar has the link itself.
			swapstylesClick(t, tab, `a[data-nav="/me"]`)
			swapstylesPath(t, tab, "/me", `document.querySelector(".ts-me") !== null`)
			swapstylesHas(t, tab, ".ts-me", "display", "flex")

			// Shelves: the form is a column with a gap (pages-shelves.css).
			if vp.w < 768 {
				swapstylesClick(t, tab, `a.ts-me__row[href="/shelves"]`)
			} else {
				swapstylesClick(t, tab, `a[data-nav="/shelves"]`)
			}
			swapstylesPath(t, tab, "/shelves", `document.querySelector(".ts-shelf-form") !== null`)
			swapstylesHas(t, tab, ".ts-shelf-form", "rowGap", "12px")

			// Library (pages-library.css), then a book (pages-book.css).
			swapstylesClick(t, tab, `a[data-nav="/libraries"]`)
			swapstylesPath(t, tab, "/libraries", `document.querySelector(".ts-library") !== null`)
			swapstylesHas(t, tab, ".ts-library", "rowGap", "12px")
			swapstylesClick(t, tab, `a[href="/books/`+e.book.ID+`"]`)
			swapstylesPath(t, tab, "/books/"+e.book.ID, `document.querySelector(".ts-book") !== null`)
			swapstylesHas(t, tab, ".ts-book", "rowGap", "16px")

			// The Bookmarks tab swaps only #book-panel: pages-bookmarks.css must already be there.
			swapstylesClick(t, tab, `#book-tab-bookmarks`)
			tab.WaitFor(`document.querySelector(".ts-bookmarks") !== null`, 5*time.Second)
			swapstylesHas(t, tab, ".ts-bookmarks", "rowGap", "12px")

			// Back to Home: a page keeps its look going the other way too.
			swapstylesClick(t, tab, `a[data-nav="/"]`)
			swapstylesPath(t, tab, "/", `document.querySelector(".ts-home") !== null`)
			swapstylesHas(t, tab, ".ts-home", "display", "flex")
		})
	}
}

// TestSwapStylesBrowserSheets opens sheets that a page does not link the styles of itself.
func TestSwapStylesBrowserSheets(t *testing.T) {
	e := playerApp(t)
	for _, vp := range []struct {
		name string
		w, h int
	}{{"desktop-1440", 1440, 900}, {"mobile-390", 390, 844}} {
		t.Run(vp.name, func(t *testing.T) {
			b := chromium(t)
			// The note sheet, opened from the toast of a bookmark made on Home (the full player's
			// "Add a note" link works on any page).
			e.store(t, 100_000, "swapstylesdev1", "Other device", 0, false)
			tab := b.NewTab(vp.w, vp.h, vp.w < 768, false)
			tab.Navigate(e.URL + "/")
			tab.WaitFor(`window.tsPlayer !== undefined && document.querySelector("#home-hero [data-ts-play]") !== null`, 10*time.Second)
			playerClickSel(t, tab, "#home-hero [data-ts-play]")
			tab.WaitFor(`window.tsPlayer.state().playing`, 5*time.Second)
			fullpKey(t, tab, "b", 66)
			tab.WaitFor(`document.querySelector("#toasts .ts-toast a") !== null`, 3*time.Second)
			if swapstylesLoaded(tab, "pages-bookmarks") {
				t.Fatal("Home already links pages-bookmarks.css: the test proves nothing")
			}
			swapstylesClick(t, tab, "#toasts .ts-toast a")
			tab.WaitFor(`document.querySelector("#sheet .ts-bookmark-sheet") !== null`, 5*time.Second)
			swapstylesHas(t, tab, ".ts-bookmark-sheet__form", "display", "flex")

			// The folder browser, opened from the Libraries page (an admin's path to Add a folder).
			tab = b.NewTab(vp.w, vp.h, vp.w < 768, false)
			tab.Navigate(e.URL + "/admin/libraries")
			swapstylesClick(t, tab, `a[href="/admin/libraries/folders/new"]`)
			swapstylesPath(t, tab, "/admin/libraries/folders/new", `document.querySelector("a[hx-target='#sheet']") !== null`)
			swapstylesClick(t, tab, `a[hx-target="#sheet"]`)
			tab.WaitFor(`document.querySelector("#sheet .ts-sheet--folders") !== null`, 5*time.Second)
			swapstylesHas(t, tab, ".ts-folders__body", "display", "flex")
		})
	}
}

// swapstylesLoaded reports whether a stylesheet whose address contains name is linked in the page.
func swapstylesLoaded(tab *cdp.Tab, name string) bool {
	var ok bool
	tab.Eval(`[...document.querySelectorAll('link[rel="stylesheet"]')].some(l => l.href.includes(`+cdp.Quote(name)+`))`, &ok)
	return ok
}
