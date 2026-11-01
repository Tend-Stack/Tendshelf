package web

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// bookMain returns the <main> element of a page: the part the pages own, without the navigation.
func bookMain(body string) string {
	i := strings.Index(body, "<main")
	j := strings.Index(body, "</main>")
	if i < 0 || j < i {
		return body
	}
	return body[i:j]
}

// bookSeedChapters gives a book n chapters of 100 s each; chapter 3 has no title.
func bookSeedChapters(t *testing.T, a *testApp, id string, n int) {
	t.Helper()
	var chs []library.Chapter
	for i := 0; i < n; i++ {
		c := library.Chapter{StartMs: int64(i) * 100_000, EndMs: int64(i+1) * 100_000, Title: fmt.Sprintf("Part %d", i+1), Source: library.ChapterFile}
		if i == 2 {
			c.Title = ""
		}
		if i == 0 {
			c.EndMs = c.StartMs + 2_890_000 // 48:10
		}
		chs = append(chs, c)
	}
	if err := a.lib.Update(context.Background(), func(tx *library.Tx) error { return tx.ReplaceChapters(id, chs) }); err != nil {
		t.Fatal(err)
	}
}

// bookSetDetails fills the columns BookSpec has no field for.
func bookSetDetails(t *testing.T, a *testApp, id string, edit func(*library.Book)) library.Book {
	t.Helper()
	var out library.Book
	err := a.lib.Update(context.Background(), func(tx *library.Tx) error {
		b, err := a.lib.Book(context.Background(), id)
		if err != nil {
			return err
		}
		edit(&b)
		out = b
		return tx.UpdateBook(b)
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func bookApp(t *testing.T) (*testApp, library.Folder) {
	t.Helper()
	a := browseOwnerApp(t)
	return a, browseLibrary(t, a, "Audiobooks")
}

func TestClockText(t *testing.T) {
	for _, c := range []struct {
		ms   int64
		want string
	}{{0, "0:00"}, {-5, "0:00"}, {999, "0:00"}, {9_000, "0:09"}, {2_890_000, "48:10"}, {3_723_000, "1:02:03"}, {36_000_000, "10:00:00"}} {
		if got := clockText(c.ms); got != c.want {
			t.Errorf("clockText(%d) = %q, want %q", c.ms, got, c.want)
		}
	}
}

func TestHoursMinutesText(t *testing.T) {
	for _, c := range []struct {
		ms   int64
		want string
	}{{0, ""}, {-1, ""}, {10_000, "1 m"}, {48 * 60_000, "48 m"}, {5 * 3_600_000, "5 h 0 m"}, {(17*60 + 32) * 60_000, "17 h 32 m"},
		{1_234 * 3_600_000, "1,234 h 0 m"}, {59*60_000 + 31_000, "1 h 0 m"}} {
		if got := hoursMinutesText(c.ms); got != c.want {
			t.Errorf("hoursMinutesText(%d) = %q, want %q", c.ms, got, c.want)
		}
	}
}

func TestBookIntro(t *testing.T) {
	short := "A short description."
	if i, r := bookIntro(short); i != short || r != "" {
		t.Errorf("short: %q %q", i, r)
	}
	long := strings.Repeat("word ", 100) // 500 runes
	i, r := bookIntro(long)
	if len([]rune(i)) > bookIntroRunes || strings.HasSuffix(i, " ") || strings.TrimSpace(i+" "+r) != strings.TrimSpace(long) {
		t.Errorf("long: intro %d runes, rest %d", len([]rune(i)), len([]rune(r)))
	}
	if strings.Contains(i, "wor ") || !strings.HasSuffix(i, "word") {
		t.Errorf("intro is not cut at a word boundary: %q", i[len(i)-8:])
	}
	one := strings.Repeat("x", 400)
	if i, r := bookIntro(one); len([]rune(i)) != bookIntroRunes || len([]rune(r)) != 80 {
		t.Errorf("one long word: %d, %d", len([]rune(i)), len([]rune(r)))
	}
}

func TestBookPageTabs(t *testing.T) {
	a, f := bookApp(t)
	b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Tabbed", Authors: []string{"Ann Author"}, Description: "About this one.", DurationMs: 3_600_000})
	for _, c := range []struct{ path, tab string }{
		{"/books/" + b.ID, "about"}, {"/books/" + b.ID + "/about", "about"}, {"/books/" + b.ID + "/chapters", "chapters"}, {"/books/" + b.ID + "/bookmarks", "bookmarks"}, {"/books/" + b.ID + "/details", "details"},
	} {
		rec := browseGet(a, c.path, false, nil)
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", c.path, rec.Code)
		}
		if !strings.Contains(body, `id="book-panel"`) || !strings.Contains(body, `aria-labelledby="book-tab-`+c.tab+`"`) {
			t.Errorf("%s: panel id or label wrong", c.path)
		}
		current := regexp.MustCompile(`<a id="book-tab-(\w+)"[^>]*aria-current="page"`).FindAllStringSubmatch(body, -1)
		if len(current) != 1 || current[0][1] != c.tab {
			t.Errorf("%s: current tab %v, want %s", c.path, current, c.tab)
		}
		for _, want := range []string{`<nav id="book-tabs" class="ts-tabs" aria-label="Book">`, `hx-target="#book-panel"`, `hx-select="#book-panel"`, `hx-push-url="true"`,
			`href="/books/` + b.ID + `"`, `href="/books/` + b.ID + `/chapters"`, `href="/books/` + b.ID + `/details"`} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: missing %s", c.path, want)
			}
		}
		if strings.Count(body, "<h1") != 1 || !strings.Contains(body, "<title>Tabbed · Tendshelf</title>") {
			t.Errorf("%s: h1 or title wrong", c.path)
		}
	}
	for _, path := range []string{"/books/" + b.ID + "/notes", "/books/" + b.ID + "/x/y", "/books/" + b.ID + "/"} {
		if rec := browseGet(a, path, false, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", path, rec.Code)
		}
	}
}

func TestBookPageAccess(t *testing.T) {
	a, f := bookApp(t)
	b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Private One", Authors: []string{"Ann Author"}})
	for _, path := range []string{"/books/" + b.ID, "/books/" + b.ID + "/chapters", "/books/" + b.ID + "/details"} {
		if rec := browseGet(a, path, false, nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Private One") {
			t.Fatalf("owner %s: %d", path, rec.Code)
		}
	}
	for _, role := range []users.Role{users.RoleMember, users.RoleGuest} {
		ck := a.as(t, role)
		unknown := browseGet(a, "/books/bk_unknown", false, ck)
		if unknown.Code != http.StatusNotFound {
			t.Fatalf("%s unknown id: %d", role, unknown.Code)
		}
		for _, path := range []string{"/books/" + b.ID, "/books/" + b.ID + "/chapters", "/books/" + b.ID + "/details"} {
			rec := browseGet(a, path, false, ck)
			if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "Private One") {
				t.Errorf("%s %s: %d, want 404 without the title", role, path, rec.Code)
			}
			if rec.Body.String() != unknown.Body.String() {
				t.Errorf("%s %s: the 404 differs from an unknown id's", role, path)
			}
		}
		if rec := browseGet(a, "/books/"+b.ID, true, ck); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "<html") {
			t.Errorf("%s htmx: %d, want a 404 toast", role, rec.Code)
		}
	}
	anon := a.do(http.MethodGet, "/books/"+b.ID, nil, nil)
	if anon.Code != http.StatusSeeOther || anon.Header().Get("Location") != "/login?next=%2Fbooks%2F"+b.ID {
		t.Errorf("anonymous: %d %q", anon.Code, anon.Header().Get("Location"))
	}
	hx := a.do(http.MethodGet, "/books/"+b.ID, nil, func(r *http.Request) { r.Header.Set("HX-Request", "true") })
	if hx.Code != http.StatusNoContent {
		t.Errorf("anonymous htmx: %d, want 204", hx.Code)
	}
}

// TestBookPageNoPlayControls: a book with no playable file draws no play button (no dead control).
// The only button left is "Mark as finished"; downloads and sharing are not on the page yet.
func TestBookPageNoPlayControls(t *testing.T) {
	a, f := bookApp(t)
	b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Quiet", Authors: []string{"Ann Author"}, Narrators: []string{"Nan Narrator"}, Series: "Saga", Sequence: "1", Description: "d"})
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Next", Authors: []string{"Ann Author"}, Narrators: []string{"Nan Narrator"}, Series: "Saga", Sequence: "2"})
	bookSeedChapters(t, a, b.ID, 3)
	for _, tab := range []string{"", "/chapters", "/details"} {
		main := bookMain(browseGet(a, "/books/"+b.ID+tab, false, nil).Body.String())
		for _, not := range []string{"Resume", "Start listening", "Listen again", "Download", "Share", "disabled", "data-ts-play="} {
			if strings.Contains(main, not) {
				t.Errorf("%q on the book page contains %q", tab, not)
			}
		}
		if n := strings.Count(main, "<button"); n != 1 || !strings.Contains(main, "Mark as finished") {
			t.Errorf("%q on the book page has %d buttons, want only Mark as finished", tab, n)
		}
	}
}

func TestBookMissingAndRefusedBanners(t *testing.T) {
	a, f := bookApp(t)
	missing := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Gone Book", MissingSince: 1})
	drm := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Protected Book", Refused: library.RefusedDRM})
	dmg := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Damaged Book", Refused: library.RefusedDamaged})
	fine := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Fine Book"})
	for _, c := range []struct {
		id, want string
	}{
		{missing.ID, "This book&#39;s files are missing. It comes back when they do."},
		{drm.ID, "This file is copy-protected, so Tendshelf will not open it"},
		{dmg.ID, ""},
	} {
		rec := browseGet(a, "/books/"+c.id, false, nil)
		body := rec.Body.String()
		if rec.Code != http.StatusOK || !strings.Contains(body, `class="ts-banner" role="status"`) {
			t.Fatalf("%s: %d, banner missing", c.id, rec.Code)
		}
		if c.want != "" && !strings.Contains(body, c.want) {
			t.Errorf("%s: banner text %q not found", c.id, c.want)
		}
	}
	if body := browseGet(a, "/books/"+fine.ID, false, nil).Body.String(); strings.Contains(body, "ts-banner") {
		t.Error("a fine book has a banner")
	}
}

func TestBookDetailsFolderAdminOnly(t *testing.T) {
	a, f := bookApp(t)
	b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Detailed", Genres: []string{"Mystery", "Cosy & Calm"}, PublishedYear: 2019, Files: 2, DurationMs: 3_600_000})
	b = bookSetDetails(t, a, b.ID, func(b *library.Book) {
		b.Publisher, b.Language, b.ISBN, b.Abridged = "Harrow & Finch", "en", "978", true
	})
	d, err := a.lib.BookDetail(context.Background(), bookAllScope, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	has := func(rows []string, label string) bool {
		for _, r := range rows {
			if r == label {
				return true
			}
		}
		return false
	}
	labels := func(canManage bool) []string {
		var out []string
		for _, r := range bookDetailsView(d, canManage) {
			out = append(out, r.Label)
		}
		return out
	}
	if has(labels(false), "Folder") || !has(labels(true), "Folder") {
		t.Errorf("Folder row: member %v, admin %v", labels(false), labels(true))
	}
	for _, want := range []string{"Publisher", "Published", "Language", "ISBN", "Edition", "Genres", "Length", "Files", "Added"} {
		if !has(labels(false), want) {
			t.Errorf("row %s missing from %v", want, labels(false))
		}
	}
	for _, not := range []string{"ASIN", "Tags", "Chapters"} {
		if has(labels(false), not) {
			t.Errorf("empty value %s is shown", not)
		}
	}
	body := browseGet(a, "/books/"+b.ID+"/details", false, nil).Body.String()
	for _, want := range []string{"<dt>Folder</dt>", "Harrow &amp; Finch", "2 files", `href="/libraries?genre=Cosy+%26+Calm"`, "Abridged"} {
		if !strings.Contains(body, want) {
			t.Errorf("owner details page lacks %q", want)
		}
	}
}

// bookAllScope sees everything: the owner's scope in a test that reads the library directly.
var bookAllScope = library.Scope{Books: share.Filter{SQL: "1=1"}, Libraries: share.Filter{SQL: "1=1"}}

func TestBookAboutRails(t *testing.T) {
	a, f := bookApp(t)
	one := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Saga One", Authors: []string{"Ann Author"}, Narrators: []string{"Nan Narrator"}, Series: "The Saga", Sequence: "1", AddedAt: 1})
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Saga Two", Authors: []string{"Ann Author"}, Narrators: []string{"Nan Narrator"}, Series: "The Saga", Sequence: "2", AddedAt: 2})
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Saga Gone", Series: "The Saga", Sequence: "3", MissingSince: 1})
	for i := 0; i < 8; i++ {
		librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: fmt.Sprintf("Other %d", i), Narrators: []string{"Nan Narrator"}, AddedAt: int64(10 + i)})
	}
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Unrelated Book", Narrators: []string{"Zed Zoe"}})

	main := bookMain(browseGet(a, "/books/"+one.ID, false, nil).Body.String())
	if !strings.Contains(main, ">More in The Saga<") || strings.Count(main, `class="ts-book__seriesrow `)+strings.Count(main, `class="ts-book__seriesrow"`) != 2 {
		t.Errorf("series rows: %d, want 2 (the missing book is not listed)", strings.Count(main, `class="ts-book__seriesrow `)+strings.Count(main, `class="ts-book__seriesrow"`))
	}
	if !strings.Contains(main, `aria-current="true"><span class="ts-book__seq">Book 1</span><span class="ts-book__seqtitle">Saga One</span><span class="ts-book__here">This book</span>`) &&
		!strings.Contains(main, ">This book<") {
		t.Error(`the current book is not marked "This book"`)
	}
	if !strings.Contains(main, ">Book 2<") || !strings.Contains(main, `href="/books/`) || strings.Contains(main, "Saga Gone") {
		t.Error("series rows wrong")
	}
	if !strings.Contains(main, ">Also narrated by Nan Narrator<") {
		t.Error("narrator rail title missing")
	}
	cards := regexp.MustCompile(`class="ts-card__title[^"]*">([^<]*)<`).FindAllStringSubmatch(main, -1)
	if len(cards) != 6 {
		t.Errorf("%d cards in the narrator rail, want 6", len(cards))
	}
	for _, c := range cards {
		if html.UnescapeString(c[1]) == "Saga One" || c[1] == "Unrelated Book" {
			t.Errorf("rail lists %q", c[1])
		}
	}
	if !strings.Contains(main, `href="/series/`) || !strings.Contains(main, `href="/narrators/`) || !strings.Contains(main, `href="/authors/`) {
		t.Error("header links to the series and people missing")
	}
	if !strings.Contains(main, "The Saga · Book 1") {
		t.Error(`series line "The Saga · Book 1" missing`)
	}
}

func TestChaptersShowAll(t *testing.T) {
	a, f := bookApp(t)
	b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Long Book"})
	bookSeedChapters(t, a, b.ID, 20)
	rows := func(path string) (int, string) {
		body := browseGet(a, path, false, nil).Body.String()
		return strings.Count(body, `<li class="ts-chapter">`), body
	}
	n, body := rows("/books/" + b.ID + "/chapters")
	if n != 12 || !strings.Contains(body, `href="/books/`+b.ID+`/chapters?all=1"`) || !strings.Contains(body, ">Show all 20 chapters<") {
		t.Errorf("first view: %d rows, show-all link missing", n)
	}
	if !strings.Contains(body, ">48:10<") || !strings.Contains(body, ">Chapter 3<") || !strings.Contains(body, ">1:40<") {
		t.Error("chapter clocks or the default title are wrong")
	}
	n, body = rows("/books/" + b.ID + "/chapters?all=1")
	if n != 20 || strings.Contains(body, "Show all") {
		t.Errorf("all view: %d rows", n)
	}
	short := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Short Book"})
	bookSeedChapters(t, a, short.ID, 5)
	if n, body := rows("/books/" + short.ID + "/chapters"); n != 5 || strings.Contains(body, "Show all") {
		t.Errorf("short book: %d rows", n)
	}
	none := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "No Chapters"})
	if body := browseGet(a, "/books/"+none.ID+"/chapters", false, nil).Body.String(); !strings.Contains(body, "This book has no chapters.") {
		t.Error("the empty chapter list says nothing")
	}
}

func TestPeoplePages(t *testing.T) {
	a, f := bookApp(t)
	for i := 0; i < 50; i++ {
		librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: fmt.Sprintf("Title %03d", i), Authors: []string{"Ann Author"}, DurationMs: 3_600_000})
	}
	voiced := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Voiced", Authors: []string{"Bo Bee"}, Narrators: []string{"Nan Narrator"}, DurationMs: 1_800_000})
	people, err := a.lib.BookPeople(context.Background(), voiced.ID)
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, p := range people {
		ids[p.Name] = p.PersonID
	}
	ann := ""
	if p, err := a.lib.Suggest(context.Background(), bookAllScope, "ann", 5); err == nil {
		for _, s := range p {
			if s.Kind == "author" {
				ann = s.ID
			}
		}
	}
	if ann == "" {
		t.Fatal("author not found")
	}

	rec := browseGet(a, "/authors/"+ann, false, nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, ">Author<") || !strings.Contains(body, ">Ann Author<") || !strings.Contains(body, "50 books in your library · 50 h 0 m") {
		t.Fatalf("author page: %d", rec.Code)
	}
	if n := strings.Count(body, `class="ts-card"`); n != 48 {
		t.Errorf("%d cards on the first page, want 48", n)
	}
	more := regexp.MustCompile(`href="(/authors/[^"]*\?cursor=[^"]+)"`).FindStringSubmatch(body)
	if more == nil {
		t.Fatal("no Show more link")
	}
	next := browseGet(a, html.UnescapeString(more[1]), false, nil).Body.String()
	if n := strings.Count(next, `class="ts-card"`); n != 2 || strings.Contains(next, ">Show more<") || !strings.Contains(next, ">Back to the start<") {
		t.Errorf("second page: %d cards", n)
	}
	if rec := browseGet(a, "/authors/"+ann+"?cursor=zzz", false, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("bad cursor: %d, want 400", rec.Code)
	}
	// The same id is no narrator; a narrator is no author.
	if rec := browseGet(a, "/narrators/"+ann, false, nil); rec.Code != http.StatusNotFound {
		t.Errorf("author on /narrators/: %d, want 404", rec.Code)
	}
	nan := ids["Nan Narrator"]
	if rec := browseGet(a, "/authors/"+nan, false, nil); rec.Code != http.StatusNotFound {
		t.Errorf("narrator on /authors/: %d, want 404", rec.Code)
	}
	nrec := browseGet(a, "/narrators/"+nan, false, nil)
	if nrec.Code != http.StatusOK || !strings.Contains(nrec.Body.String(), ">Narrator<") || !strings.Contains(nrec.Body.String(), "1 book in your library · 30 m") {
		t.Errorf("narrator page: %d", nrec.Code)
	}
	if rec := browseGet(a, "/authors/ps_unknown", false, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown person: %d", rec.Code)
	}
	member := a.as(t, users.RoleMember)
	unknown := browseGet(a, "/authors/ps_unknown", false, member)
	mrec := browseGet(a, "/authors/"+ann, false, member)
	if mrec.Code != http.StatusNotFound || mrec.Body.String() != unknown.Body.String() || strings.Contains(mrec.Body.String(), "Ann Author") {
		t.Errorf("member: %d, want the unknown-id 404", mrec.Code)
	}
	if rec := browseGet(a, "/narrators/"+nan, false, member); rec.Code != http.StatusNotFound {
		t.Errorf("member narrator: %d", rec.Code)
	}
}

func TestSeriesPage(t *testing.T) {
	a, f := bookApp(t)
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Third", Authors: []string{"Ann Author"}, Narrators: []string{"Nan Narrator"}, Series: "The Saga", Sequence: "3", DurationMs: 3_600_000})
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "First", Authors: []string{"Ann Author"}, Narrators: []string{"Nan Narrator", "Ola Voice"}, Series: "The Saga", Sequence: "1", DurationMs: 7_200_000})
	second := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Second", Authors: []string{"Bo Bee", "Cy Cox", "Di Dee"}, Series: "The Saga", Sequence: "2", DurationMs: 1_800_000})
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Hidden", Series: "The Saga", Sequence: "4", Refused: library.RefusedDRM})
	links, err := a.lib.BookSeries(context.Background(), second.ID)
	if err != nil || len(links) != 1 {
		t.Fatal(err)
	}
	path := "/series/" + links[0].SeriesID
	rec := browseGet(a, path, false, nil)
	body := bookMain(rec.Body.String())
	if rec.Code != http.StatusOK || !strings.Contains(body, "Series · 3 books") || !strings.Contains(body, ">The Saga<") {
		t.Fatalf("series page: %d", rec.Code)
	}
	if got := browseTitles(body); strings.Join(got, ",") != "First,Second,Third" {
		t.Errorf("order %v, want First, Second, Third", got)
	}
	for _, want := range []string{"Book 1 · 2 h", "Book 3 · 1 h", "3 h 30 m in all", ">Books in order<", "by ", "read by "} {
		if !strings.Contains(body, want) {
			t.Errorf("series page lacks %q", want)
		}
	}
	if strings.Count(body, `href="/authors/`) != 2 || strings.Count(body, `href="/narrators/`) != 2 || strings.Contains(body, "Di Dee") || strings.Contains(body, "Hidden") {
		t.Errorf("people links: %d authors, %d narrators", strings.Count(body, `href="/authors/`), strings.Count(body, `href="/narrators/`))
	}
	member := a.as(t, users.RoleMember)
	unknown := browseGet(a, "/series/sr_unknown", false, member)
	if rec := browseGet(a, path, false, member); rec.Code != http.StatusNotFound || rec.Body.String() != unknown.Body.String() {
		t.Errorf("member: %d", rec.Code)
	}
	if rec := browseGet(a, "/series/sr_unknown", false, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown series: %d", rec.Code)
	}
}
