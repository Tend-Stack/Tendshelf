package web

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// railSection returns the markup of one rail (series or narrated) of a book page, or "".
func railSection(main, titleID string) string {
	i := strings.Index(main, `aria-labelledby="`+titleID+`"`)
	if i < 0 {
		return ""
	}
	j := strings.Index(main[i:], "</section>")
	if j < 0 {
		return main[i:]
	}
	return main[i : i+j]
}

// railTiles lists the tiles of a rail as "title|state" pairs.
func railTiles(section string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`class="ts-card__title[^"]*">([^<]*)</span>\s*(?:<span class="ts-card__author[^"]*">([^<]*)</span>)?`).FindAllStringSubmatch(section, -1) {
		out = append(out, m[1]+"|"+m[2])
	}
	return out
}

func TestBookRails(t *testing.T) {
	a, f := bookApp(t)
	one := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Saga One", Authors: []string{"Ann Author"}, Narrators: []string{"Nan Narrator"}, Series: "The Saga", Sequence: "1", AddedAt: 1})
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Saga Two", Authors: []string{"Ann Author"}, Narrators: []string{"Nan Narrator"}, Series: "The Saga", Sequence: "2", AddedAt: 2})
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Saga Gone", Series: "The Saga", Sequence: "3", MissingSince: 1})
	for i := 0; i < 8; i++ {
		librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: fmt.Sprintf("Other %d", i), Narrators: []string{"Nan Narrator"}, AddedAt: int64(10 + i)})
	}
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Unrelated Book", Narrators: []string{"Zed Zoe"}})

	for _, tab := range []string{"", "/chapters", "/details", "/bookmarks"} {
		main := bookMain(browseGet(a, "/books/"+one.ID+tab, false, nil).Body.String())
		series := railSection(main, "book-series-title")
		if series == "" {
			t.Fatalf("%q: no series rail", tab)
		}
		if !strings.Contains(series, `href="/series/`) || !strings.Contains(series, ">More in The Saga<") {
			t.Errorf("%q: series heading is not a link to the series page", tab)
		}
		tiles := railTiles(series)
		if len(tiles) != 2 || tiles[0] != "Book 1|This book" || tiles[1] != "Book 2 · Saga Two|Next in series" {
			t.Errorf("%q: series tiles %q", tab, tiles)
		}
		if strings.Contains(main, "Saga Gone") {
			t.Errorf("%q: the missing book is listed", tab)
		}
		if strings.Count(series, `aria-current="page"`) != 1 || !strings.Contains(series, `aria-current="page"><span class="ts-card__cover">`) {
			t.Errorf("%q: the open book's tile is not the one aria-current", tab)
		}
		if strings.Contains(series, "data-ts-play") {
			t.Errorf("%q: a tile is a play control", tab)
		}
		narrated := railTiles(railSection(main, "book-narrated-title"))
		if len(narrated) != 3 {
			t.Errorf("%q: %d cards in the narrator rail, want 3", tab, len(narrated))
		}
		for _, c := range narrated {
			if strings.HasPrefix(c, "Saga One|") || strings.HasPrefix(c, "Unrelated Book|") {
				t.Errorf("%q: narrator rail lists %q", tab, c)
			}
		}
		if !strings.Contains(main, ">Also narrated by Nan Narrator<") || !strings.Contains(main, `href="/narrators/`) || !strings.Contains(main, `href="/authors/`) {
			t.Errorf("%q: narrator rail title or people links missing", tab)
		}
	}
	main := bookMain(browseGet(a, "/books/"+one.ID, false, nil).Body.String())
	if !strings.Contains(main, "The Saga · Book 1") {
		t.Error(`series chip "The Saga · Book 1" missing`)
	}
	// Book 2 is not started and is the book right after this one; Start series would point at this
	// book itself, so it is left out.
	if strings.Contains(main, "Start series") {
		t.Error("Start series shown on the first unfinished book")
	}
}

func TestBookRailStates(t *testing.T) {
	a, f := bookApp(t)
	var b [5]library.Book
	for i := range b {
		b[i] = librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{
			Title: fmt.Sprintf("Tale %d", i+1), Series: "The Tales", Sequence: fmt.Sprint(i + 1), DurationMs: 100_000, AddedAt: int64(i + 1)})
	}
	uid := sweepUserID(t, a, a.owner(t))
	svc := progress.New(a.st, nil)
	ctx := context.Background()
	if _, err := svc.SetFinished(ctx, uid, b[0].ID, true, 100_000); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Save(ctx, progress.Update{UserID: uid, BookID: b[3].ID, PositionMs: 40_000, DurationMs: 100_000, ClientUpdatedAt: 1_700_000_000_000, DeviceID: "raildevice01"}); err != nil {
		t.Fatal(err)
	}
	main := bookMain(browseGet(a, "/books/"+b[1].ID, false, nil).Body.String())
	tiles := railTiles(railSection(main, "book-series-title"))
	want := []string{"Book 1 · Tale 1|Finished", "Book 2|This book", "Book 3 · Tale 3|Next in series", "Book 4 · Tale 4|40%", "Book 5 · Tale 5|Not started"}
	if strings.Join(tiles, ",") != strings.Join(want, ",") {
		t.Errorf("tiles %q, want %q", tiles, want)
	}
	// Book 1 is finished, so the first unfinished book is Book 2: the open one.
	if strings.Contains(main, "Start series") {
		t.Error("Start series shown on the first unfinished book")
	}
	main = bookMain(browseGet(a, "/books/"+b[3].ID, false, nil).Body.String())
	if !strings.Contains(main, `class="ts-btn ts-btn--quiet ts-book__start" href="/books/`+b[1].ID+`">Start series<`) {
		t.Error("Start series does not point at the first unfinished book")
	}
	if !strings.Contains(main, "The Tales · Book 4 of 5") {
		t.Error("overline lacks the place in the series")
	}
	// Nothing finished: Start series points at Book 1 from any other book.
	if _, err := svc.SetFinished(ctx, uid, b[0].ID, false, 100_000); err != nil {
		t.Fatal(err)
	}
	main = bookMain(browseGet(a, "/books/"+b[3].ID, false, nil).Body.String())
	if !strings.Contains(main, `ts-book__start" href="/books/`+b[0].ID+`">Start series<`) {
		t.Error("Start series does not point at Book 1")
	}
	main = bookMain(browseGet(a, "/books/"+b[0].ID, false, nil).Body.String())
	if strings.Contains(main, "Start series") {
		t.Error("Start series shown on Book 1, which is the first unfinished book")
	}
	// A finished book carries "finished in" in its overline.
	if _, err := svc.SetFinished(ctx, uid, b[0].ID, true, 100_000); err != nil {
		t.Fatal(err)
	}
	main = bookMain(browseGet(a, "/books/"+b[0].ID, false, nil).Body.String())
	if !regexp.MustCompile(`ts-book__overline">The Tales · Book 1 of 5 · finished in [A-Z][a-z]+( \d{4})?<`).MatchString(main) {
		t.Errorf("overline of a finished book: %s", regexp.MustCompile(`ts-book__overline">[^<]*`).FindString(main))
	}
}

func TestBookPlayRegion(t *testing.T) {
	e := playBtnEnv(t)
	page := func() string { return bookMain(playBtnGet(e, "/books/"+e.book.ID)) }
	region := func() string { return playBtnGet(e, "/hx/books/"+e.book.ID+"/play") }

	// Untouched: no ruler, but the shelf link, the one mark form and the queue menu are inside the region.
	body := page()
	reg := region()
	if strings.Contains(reg, "ts-ruler") || strings.Contains(reg, "ts-book__where") {
		t.Error("an unstarted book shows a ruler")
	}
	for _, want := range []string{`id="book-play"`, `aria-haspopup="dialog"`, `ts-book__shelf`, `ts-book__mark`, "Mark as finished"} {
		if !strings.Contains(reg, want) {
			t.Errorf("region lacks %q", want)
		}
	}
	if strings.Count(reg, "<form") != 3 || playBtnCount(body, e.book.ID) != 1 { // the mark form and the queue menu's two
		t.Errorf("region has %d forms, page has %d play buttons; want 3 and 1", strings.Count(reg, "<form"), playBtnCount(body, e.book.ID))
	}
	if strings.Contains(strings.Replace(body, reg, "", 1), `aria-haspopup="dialog"`) {
		t.Error("the shelf link also appears outside #book-play")
	}

	// In progress: the ruler with its fill, the long and the short where-lines.
	playBtnProgress(t, e, e.ownerID, e.book.ID, 90_000, 0, 1_700_000_000_000)
	body, reg = page(), region()
	for _, want := range []string{`class="ts-ruler"`, `data-ts-value="50.0"`, `aria-label="Book progress, chapter 2 of 3"`, "Chapter 2 · 1 min left in chapter · ", `ts-book__whereshort">Chapter 2 · 1 min left<`} {
		if !strings.Contains(reg, want) {
			t.Errorf("region lacks %q: %s", want, reg)
		}
	}
	if !strings.Contains(body, reg) {
		t.Error("/hx/books/{id}/play is not the region as the page draws it")
	}

	// Finished: no ruler.
	playBtnProgress(t, e, e.ownerID, e.book.ID, 180_000, 1_700_000_100_000, 1_700_000_100_000)
	if reg = region(); strings.Contains(reg, "ts-ruler") || !strings.Contains(reg, "Mark as not started") {
		t.Errorf("finished region: %s", reg)
	}
}

func TestBookPlayRegionNoPlay(t *testing.T) {
	a, f := bookApp(t)
	b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Locked Away", Refused: "drm"})
	main := bookMain(browseGet(a, "/books/"+b.ID, false, nil).Body.String())
	if strings.Contains(main, `id="book-play"`) || strings.Contains(main, "data-ts-play") {
		t.Error("a refused book has a play region")
	}
	if !strings.Contains(main, `<div class="ts-book__play"><div class="ts-book__acts"><a class="ts-btn ts-btn--secondary ts-btn--lg ts-book__shelf"`) {
		t.Error("the shelf link fallback is missing")
	}
	if rec := a.do(http.MethodGet, "/hx/books/"+b.ID+"/play", nil, nil, a.session); strings.Contains(rec.Body.String(), "ts-book__shelf") {
		t.Error("the fragment of a refused book draws a region")
	}
}

func TestBookTabHome(t *testing.T) {
	a, f := bookApp(t)
	b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Homey", Description: "About it."})
	bookSeedChapters(t, a, b.ID, 3)
	for _, c := range []struct{ path, home string }{
		{"/books/" + b.ID, "chapters"}, {"/books/" + b.ID + "/chapters", ""}, {"/books/" + b.ID + "/details", ""},
	} {
		body := browseGet(a, c.path, false, nil).Body.String()
		got := regexp.MustCompile(`<a id="book-tab-(\w+)" class="[^"]*is-home`).FindAllStringSubmatch(body, -1)
		switch {
		case c.home == "" && len(got) != 0, c.home != "" && (len(got) != 1 || got[0][1] != c.home):
			t.Errorf("%s: is-home on %v, want %q", c.path, got, c.home)
		}
		if !strings.Contains(body, "ts-book__tab--about") || !strings.Contains(body, "ts-book__tab--details") {
			t.Errorf("%s: tabs lack their id classes", c.path)
		}
		if strings.Contains(bookMain(body), " style=") {
			t.Errorf("%s: inline style in the book page", c.path)
		}
		panel := body[strings.Index(body, `id="book-panel"`):]
		if (c.path == "/books/"+b.ID) != strings.Contains(panel[:80], "is-about") {
			t.Errorf("%s: is-about on the panel is wrong", c.path)
		}
	}
	about := bookMain(browseGet(a, "/books/"+b.ID, false, nil).Body.String())
	if !strings.Contains(about, `ts-book__aboutchapters`) || strings.Count(about, `class="ts-chapter`) < 3 {
		t.Error("the About panel does not carry the chapter list")
	}
}

func TestBookBand(t *testing.T) {
	a, f := bookApp(t)
	tinted := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Teal Book"})
	bare := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Bare Book"})
	sha := strings.Repeat("ab", 32)
	cover := library.Cover{BookID: tinted.ID, Source: library.CoverFolder, Mime: "image/png", Width: 8, Height: 8, SHA256: sha, Path: "covers/x.png", Tint: "#1f4a52"}
	if err := a.lib.Update(context.Background(), func(tx *library.Tx) error {
		if err := tx.PutCover(&cover); err != nil {
			return err
		}
		return tx.SetBookCover(tinted.ID, cover.ID)
	}); err != nil {
		t.Fatal(err)
	}
	body := browseGet(a, "/books/"+tinted.ID, false, nil).Body.String()
	m := regexp.MustCompile(`<div class="ts-book__band ([^"]*)" data-ts-tint="#1f4a52" aria-hidden="true">`).FindStringSubmatch(body)
	if m == nil || !strings.Contains(m[1], "ts-band-dark-1") {
		t.Errorf("tinted cover band: %v", m)
	}
	if body = browseGet(a, "/books/"+bare.ID, false, nil).Body.String(); strings.Contains(body, "ts-book__band") {
		t.Error("a coverless book has a band")
	}
}

// TestBookGrantSeriesTiles: a person who holds a grant on two books of a three-book series sees two
// tiles, never the third book's title, id or place, and the page never counts hidden books.
func TestBookGrantSeriesTiles(t *testing.T) {
	for _, role := range []int{sweepMember, sweepGuest} {
		t.Run(sweepRoleNames[role], func(t *testing.T) {
			e := sweepSeed(t)
			f := library.Folder{ID: e.folderID, LibraryID: e.libID, Path: e.folderDir}
			mid := librarytest.AddBook(t, e.a.lib, f, librarytest.BookSpec{Title: "Hiddenmid Lantern", Series: "Sweepseries Saga", Sequence: "2", DurationMs: sweepHourMs, AddedAt: 6})
			b3 := librarytest.AddBook(t, e.a.lib, f, librarytest.BookSpec{Title: "Visiblethird Lantern", Series: "Sweepseries Saga", Sequence: "3", DurationMs: sweepHourMs, AddedAt: 7})
			for _, id := range []string{e.book.ID, b3.ID} {
				if _, err := e.a.st.Writer().Exec(`INSERT INTO grants(id, user_id, scope, book_id, can_download, can_read, created_at, updated_at)
					VALUES (?, ?, 'book', ?, 0, 0, 1, 1)`, "gr_tiles_"+id, e.uid[role], id); err != nil {
					t.Fatal(err)
				}
			}
			for _, path := range []string{"/books/" + e.book.ID, "/books/" + e.book.ID + "/chapters", "/books/" + e.book.ID + "/details", "/hx/books/" + e.book.ID + "/play"} {
				code, body := fetchAs(e, role, path)
				if code != http.StatusOK {
					t.Fatalf("GET %s: %d", path, code)
				}
				if strings.Contains(body, "Hiddenmid") || strings.Contains(body, mid.ID) {
					t.Errorf("GET %s leaks the hidden book", path)
				}
				if strings.HasPrefix(path, "/hx/") {
					continue
				}
				main := bookMain(body)
				if tiles := railTiles(railSection(main, "book-series-title")); len(tiles) != 2 {
					t.Errorf("GET %s: tiles %q, want 2", path, tiles)
				}
				if regexp.MustCompile(`Book \d+ of \d+`).MatchString(main) {
					t.Errorf("GET %s counts books it must not (%s)", path, regexp.MustCompile(`Book \d+ of \d+`).FindString(main))
				}
			}
			// The third book is the open one: Start series leads to the first visible book.
			_, body := fetchAs(e, role, "/books/"+b3.ID)
			if !strings.Contains(bookMain(body), `ts-book__start" href="/books/`+e.book.ID+`">Start series<`) {
				t.Error("Start series does not lead to the first visible book")
			}
			if strings.Contains(body, mid.ID) || strings.Contains(body, "Hiddenmid") {
				t.Error("the third book's page leaks the hidden book")
			}
		})
	}
}

func fetchAs(e *sweepEnv, role int, path string) (int, string) {
	rec := e.a.do(http.MethodGet, path, nil, nil, e.ck[role])
	return rec.Code, rec.Body.String()
}
