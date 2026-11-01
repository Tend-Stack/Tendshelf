//go:build dev

package web

import (
	"context"
	"fmt"
	"io"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

// bookVisualCover gives a book a solid-colour cover from the board palette.
func bookVisualCover(t *testing.T, a *testApp, b library.Book, i int) {
	t.Helper()
	tint := browsePalette[i%len(browsePalette)]
	sha, rel := browseWriteCover(t, a.dir, fmt.Sprintf("book-cover-%d", i), tint)
	cover := library.Cover{BookID: b.ID, Source: library.CoverFolder, Mime: "image/png", Width: 96, Height: 96, SHA256: sha, Path: rel, Tint: tint}
	if err := a.lib.Update(context.Background(), func(tx *library.Tx) error {
		if err := tx.PutCover(&cover); err != nil {
			return err
		}
		return tx.SetBookCover(b.ID, cover.ID)
	}); err != nil {
		t.Fatal(err)
	}
}

// bookVisualSeed builds the Harbour Lights shelf of the boards: three books with chapters, a long
// description and details, plus other books by the same narrator and author. It returns the ids
// the pages need.
func bookVisualSeed(t *testing.T, a *testApp) (bookID, authorID, narratorID, seriesID string) {
	t.Helper()
	f := browseLibrary(t, a, "Audiobooks")
	desc := "On a storm-worn island off the northern coast, Nell Avery inherits her father's lighthouse and the one lamp he never let her light. " +
		"As winter closes in, letters hidden in the keeper's log begin to tell a different story about the night her mother left, and about the stranger who keeps rowing out from the mainland. " +
		"Nothing on the island is what it was, and the lamp has begun to answer."
	var first library.Book
	for i, title := range []string{"The Lighthouse Keeper's Daughter", "The Fog Bell", "Winter Signal"} {
		b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{
			Title: title, Authors: []string{"Imogen Hale"}, Narrators: []string{"Theo Marsh"}, Series: "Harbour Lights", Sequence: fmt.Sprint(i + 1),
			Description: desc, Genres: []string{"Mystery", "Literary"}, DurationMs: (17*60 + 32) * 60_000, PublishedYear: 2019, AddedAt: int64(100 - i), Files: 3,
		})
		bookVisualCover(t, a, b, i)
		if i == 0 {
			first = bookSetDetails(t, a, b.ID, func(b *library.Book) { b.Publisher, b.Language, b.Abridged = "Harrow & Finch", "en", false })
			bookSeedChapters(t, a, b.ID, 19)
		}
	}
	others := []string{"The Quiet Mechanic", "A Field Guide to Small Winters", "The Orchard at Low Tide", "Notes from the Lower Field", "Salt and Ember", "Nine Bridges Home", "Saltmarsh"}
	for i, title := range others {
		author := []string{"Jonah Feld", "Mara Lindqvist", "Sana Iqbal", "Abel Osei"}[i%4]
		if i < 3 {
			author = "Imogen Hale"
		}
		b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: title, Authors: []string{author}, Narrators: []string{"Theo Marsh"}, DurationMs: int64(5+i) * 3_600_000, AddedAt: int64(50 - i)})
		bookVisualCover(t, a, b, i+3)
	}
	sc := bookAllScope
	ctx := context.Background()
	card, err := a.lib.BookDetail(ctx, sc, first.ID)
	if err != nil {
		t.Fatal(err)
	}
	return first.ID, card.Card.Authors[0].ID, card.Card.Narrators[0].ID, card.Series[0].ID
}

// bookProbeJS is browseProbeJS plus what these pages promise: one h1, a labelled field, no inline
// style, and a visible heading order that never skips a level.
const bookProbeJS = `(() => {
  const r = ` + browseProbeJS + `;
  const levels = [...document.querySelectorAll("main h1, main h2, main h3")].map(h => +h.tagName[1]);
  r.skip = levels.some((l, i) => i > 0 && l - levels[i - 1] > 1);
  r.styled = document.querySelectorAll("[style]").length;
  return r;
})()`

type bookProbeResult struct {
	browseProbe
	Skip   bool `json:"skip"`
	Styled int  `json:"styled"`
}

// TestVisualBookPages captures the book (About, Chapters, Details), author, narrator, series and
// search pages: dark and light at four widths for the main four, the rest at 390 and 1440, and
// sepia and contrast at 390 and 1440. It fails on horizontal overflow, a target under 44 px, an
// unlabelled field, a page without one h1, a skipped heading level or an inline style. The board
// PNGs are copied beside the captures.
func TestVisualBookPages(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	app := newTestApp(t)
	app.owner(t)
	bookID, authorID, narratorID, seriesID := bookVisualSeed(t, app)
	base := themedServer(t, app.signedIn(t))

	for name, dir := range map[string]string{
		"BookPhone": "core", "BookPhoneLight": "core", "BookTablet": "core", "BookTabletLight": "core", "BookDesktop": "core", "BookDesktopLight": "core",
		"SeriesPhone": "browse", "SeriesTablet": "browse", "SeriesPage": "browse", "AuthorTablet": "browse", "AuthorPage": "browse", "NarratorPhone": "browse",
		"SearchPhone": "browse", "SearchTablet": "browse", "SearchPage": "browse", "StateNoResults": "states",
	} {
		src, err := os.Open(filepath.Join("..", "..", "docs", "design", "tendshelf-handoff", "screens", dir, name+".png"))
		if err != nil {
			continue
		}
		if dst, err := os.Create(filepath.Join(out, "book-board-"+name+".png")); err == nil {
			_, _ = io.Copy(dst, src)
			_ = dst.Close()
		}
		_ = src.Close()
	}

	type page struct{ name, path string }
	main4 := []page{{"book-about", "/books/" + bookID}, {"author", "/authors/" + authorID}, {"series", "/series/" + seriesID}, {"search", "/search?q=harbour"}}
	extra := []page{{"book-chapters", "/books/" + bookID + "/chapters"}, {"book-details", "/books/" + bookID + "/details"}, {"narrator", "/narrators/" + narratorID}, {"search-none", "/search?q=lighthuose"}}
	type job struct {
		pg     page
		theme  string
		widths []int
	}
	var jobs []job
	for _, p := range main4 {
		for _, th := range []string{"dark", "light"} {
			jobs = append(jobs, job{p, th, []int{390, 768, 1024, 1440}})
		}
		for _, th := range []string{"sepia", "contrast"} {
			jobs = append(jobs, job{p, th, []int{390, 1440}})
		}
	}
	for _, p := range extra {
		jobs = append(jobs, job{p, "dark", []int{390, 1440}})
	}
	for _, j := range jobs {
		for _, w := range j.widths {
			t.Run(fmt.Sprintf("%s-%s-%d", j.pg.name, j.theme, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w == 390, false)
				sep := "?"
				if strings.Contains(j.pg.path, "?") {
					sep = "&"
				}
				tab.Navigate(base + j.pg.path + sep + "theme=" + j.theme)
				var m bookProbeResult
				tab.Eval(bookProbeJS, &m)
				switch {
				case m.Over:
					t.Errorf("horizontal overflow")
				case m.Theme != j.theme:
					t.Errorf("data-theme = %q, want %q", m.Theme, j.theme)
				}
				if len(m.Small) > 0 {
					t.Errorf("targets under 44 px: %v", m.Small)
				}
				if len(m.Unlabelled) > 0 {
					t.Errorf("inputs without a label: %v", m.Unlabelled)
				}
				if m.H1 != 1 {
					t.Errorf("%d h1 elements", m.H1)
				}
				if m.Skip {
					t.Errorf("a heading level is skipped")
				}
				if m.Styled > 0 {
					t.Errorf("%d elements with a style attribute", m.Styled)
				}
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("book-%s-%s-%d.png", j.pg.name, j.theme, w)))
			})
		}
	}
}

// TestVisualBookTabsAndSearchLive checks the two htmx behaviours in a real browser: a tab swaps
// only the panel and moves aria-current (with the address pushed, and Back returning), and typing in
// the search field replaces #search-results, the segments and the clear button without moving focus.
func TestVisualBookTabsAndSearchLive(t *testing.T) {
	b := chromium(t)
	app := newTestApp(t)
	app.owner(t)
	bookID, _, _, _ := bookVisualSeed(t, app)
	srv := httptest.NewServer(app.signedIn(t))
	t.Cleanup(srv.Close)

	tab := b.NewTab(390, 844, true, false)
	tab.Navigate(srv.URL + "/books/" + bookID)
	tab.WaitFor(`window.htmx !== undefined`, 5*time.Second)
	tab.Eval(`document.getElementById("book-tab-chapters").click()`, nil)
	tab.WaitFor(`location.pathname.endsWith("/chapters") && document.querySelectorAll(".ts-chapter").length === 12`, 5*time.Second)
	var got struct {
		Current, Labelled string
		Hero              bool
	}
	tab.Eval(`({current: document.querySelector('#book-tabs [aria-current="page"]').id, labelled: document.getElementById("book-panel").getAttribute("aria-labelledby"),
		hero: Boolean(document.querySelector(".ts-book__cover"))})`, &got)
	if got.Current != "book-tab-chapters" || got.Labelled != "book-tab-chapters" || !got.Hero {
		t.Errorf("after the Chapters tab: %+v", got)
	}
	tab.Eval(`history.back()`, nil)
	tab.WaitFor(`!location.pathname.endsWith("/chapters") && document.querySelectorAll(".ts-chapter").length === 0 && document.querySelector('#book-tabs [aria-current="page"]').id === "book-tab-about"`, 5*time.Second)

	tab.Navigate(srv.URL + "/search")
	tab.WaitFor(`window.htmx !== undefined`, 5*time.Second)
	tab.Eval(`(() => { const i = document.getElementById("search-q"); i.focus(); i.value = "harbour"; i.dispatchEvent(new Event("input", {bubbles: true})); })()`, nil)
	tab.WaitFor(`document.querySelectorAll("#search-results .ts-row").length > 0 && location.search.includes("q=harbour")`, 5*time.Second)
	var live struct {
		Focus, Clear bool
		Seg          string
	}
	tab.Eval(`({focus: document.activeElement.id === "search-q", clear: Boolean(document.querySelector("#search-clear a")),
		seg: document.querySelector('#search-segments a[aria-current="true"]').getAttribute("href")})`, &live)
	if !live.Focus || !live.Clear || live.Seg != "/search?q=harbour" {
		t.Errorf("after typing: %+v", live)
	}
	tab.Screenshot(filepath.Join(visualDir(t, "chromium"), "book-search-live-390.png"))
}
