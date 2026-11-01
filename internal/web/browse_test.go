package web

import (
	"bytes"
	"context"
	"fmt"
	"html"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
)

// browseGet does a GET as the owner (or as the cookie's person) and returns the recorder.
func browseGet(a *testApp, path string, htmx bool, ck *http.Cookie) *httptest.ResponseRecorder {
	if ck == nil {
		ck = a.session
	}
	return a.do(http.MethodGet, path, nil, func(r *http.Request) {
		if htmx {
			r.Header.Set("HX-Request", "true")
		}
	}, ck)
}

// browseOwnerApp returns an app with its owner signed in.
func browseOwnerApp(t *testing.T) *testApp {
	t.Helper()
	a := newTestApp(t)
	a.owner(t)
	return a
}

var browseLibCounter int

// browseLibrary creates a library with one folder.
func browseLibrary(t *testing.T, a *testApp, name string) library.Folder {
	t.Helper()
	browseLibCounter++
	_, f := librarytest.NewLibrary(t, a.lib, name, fmt.Sprintf("/browse-%d", browseLibCounter))
	return f
}

var browseBookHref = regexp.MustCompile(`href="/books/([^"]+)"`)

// browseTitles lists the titles of the cards on a page in order.
func browseTitles(body string) []string {
	var out []string
	for _, m := range regexp.MustCompile(`class="ts-(?:card__title|row__title)[^"]*">([^<]*)<`).FindAllStringSubmatch(body, -1) {
		out = append(out, html.UnescapeString(m[1]))
	}
	return out
}

func browseIDs(body string) []string {
	var out []string
	for _, m := range browseBookHref.FindAllStringSubmatch(body, -1) {
		out = append(out, m[1])
	}
	return out
}

// browseMore returns the fragment address of the sentinel, or "".
func browseMore(body string) string {
	m := regexp.MustCompile(`hx-get="([^"]*/hx/libraries/[^"]*)"`).FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return html.UnescapeString(m[1])
}

func browseAddMany(t *testing.T, a *testApp, f library.Folder, n int) []library.Book {
	t.Helper()
	var out []library.Book
	for i := 1; i <= n; i++ {
		out = append(out, librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{
			Title: fmt.Sprintf("Book %03d", i), Authors: []string{"Imogen Hale"}, DurationMs: int64(i) * 600_000, AddedAt: int64(i) * 1000,
		}))
	}
	return out
}

func TestHomeNewOnShelf(t *testing.T) {
	a := browseOwnerApp(t)
	f := browseLibrary(t, a, "Audiobooks")
	books := browseAddMany(t, a, f, 15)

	rec := browseGet(a, "/", false, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := rec.Body.String()
	if got := strings.Count(body, `role="listitem"`); got != 12 {
		t.Fatalf("%d spines, want 12", got)
	}
	// Newest first: books 15 down to 4.
	ids := browseIDs(body)
	if len(ids) != 12 {
		t.Fatalf("%d book links, want 12", len(ids))
	}
	for i, id := range ids {
		if want := books[14-i].ID; id != want {
			t.Fatalf("spine %d is %s, want %s", i, id, want)
		}
	}
	for _, want := range []string{
		`aria-label="Book 015 by Imogen Hale"`, `aria-label="Book 004 by Imogen Hale"`,
		`id="row-recent"`, `New on the shelf`, `href="/libraries?sort=added"`, `See all`, `Welcome back, Ana`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, not := range []string{`Book 003`, `Book 001`, `Your shelf is empty`} {
		if strings.Contains(body, not) {
			t.Errorf("page has %q", not)
		}
	}
	if !strings.Contains(body, `<title>Home · Tendshelf</title>`) || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("title or cache header wrong")
	}
}

func TestHomeEmptyStates(t *testing.T) {
	a := browseOwnerApp(t)
	owner := browseGet(a, "/", false, nil).Body.String()
	for _, want := range []string{"Your shelf is empty", "Point Tendshelf at a folder of audiobooks and they&#39;ll appear here.", "Add a folder", `href="/admin/libraries/folders/new"`} {
		if !strings.Contains(owner, want) {
			t.Errorf("owner first-run lacks %q", want)
		}
	}
	if strings.Contains(owner, "New on the shelf") {
		t.Error("an empty shelf shows the New row")
	}

	// Books exist, but the member may not open any of them.
	f := browseLibrary(t, a, "Hidden")
	browseAddMany(t, a, f, 3)
	member := a.as(t, users.RoleMember)
	body := browseGet(a, "/", false, member).Body.String()
	for _, want := range []string{"Nothing shared yet", "When someone shares a book with you, it shows up here."} {
		if !strings.Contains(body, want) {
			t.Errorf("member lacks %q", want)
		}
	}
	for _, not := range []string{"Add a folder", "/admin/libraries", "Book 001", "New on the shelf", "/hx/jobs/"} {
		if strings.Contains(body, not) {
			t.Errorf("member page has %q", not)
		}
	}
}

func TestHomeScanBanner(t *testing.T) {
	a := browseOwnerApp(t)
	f := browseLibrary(t, a, "Audiobooks")
	browseAddMany(t, a, f, 3)
	job := a.adminEnqueueScan(t, f.LibraryID)
	for _, path := range []string{"/", "/libraries", "/libraries/" + f.LibraryID} {
		body := browseGet(a, path, false, nil).Body.String()
		if !strings.Contains(body, `hx-get="/hx/jobs/`+job+`"`) || !strings.Contains(body, `hx-trigger="every 2s"`) {
			t.Errorf("%s: no scan poller for job %s", path, job)
		}
	}
	member := a.as(t, users.RoleMember)
	for _, path := range []string{"/", "/libraries"} {
		if body := browseGet(a, path, false, member).Body.String(); strings.Contains(body, "/hx/jobs/") {
			t.Errorf("%s: member sees the scan banner", path)
		}
	}
	// A scan with no books yet shows the banner, not the empty state.
	b := browseOwnerApp(t)
	f2 := browseLibrary(t, b, "Fresh")
	b.adminEnqueueScan(t, f2.LibraryID)
	body := browseGet(b, "/", false, nil).Body.String()
	if !strings.Contains(body, "/hx/jobs/") || strings.Contains(body, "Your shelf is empty") {
		t.Error("scanning first-run should show the banner and no empty state")
	}
}

func TestHomeGreeting(t *testing.T) {
	for _, c := range []struct{ name, want string }{
		{"Ana Reyes", "Welcome back, Ana"}, {"  Maya ", "Welcome back, Maya"}, {"", "Welcome back"}, {"   ", "Welcome back"},
	} {
		if got := browseGreetingFor(c.name); got != c.want {
			t.Errorf("browseGreetingFor(%q) = %q, want %q", c.name, got, c.want)
		}
	}
	a := browseOwnerApp(t)
	if body := browseGet(a, "/", false, nil).Body.String(); !strings.Contains(body, ">Welcome back, Ana<") {
		t.Error("owner greeting is not the first name")
	}
	if _, err := a.st.Writer().Exec(`UPDATE users SET display_name = ''`); err != nil {
		t.Fatal(err)
	}
	if body := browseGet(a, "/", false, nil).Body.String(); !strings.Contains(body, ">Welcome back<") {
		t.Error("a person without a name gets a bare greeting")
	}
}

func TestLibraryAccess(t *testing.T) {
	a := browseOwnerApp(t)
	f := browseLibrary(t, a, "Audiobooks")
	browseAddMany(t, a, f, 3)

	rec := browseGet(a, "/libraries", false, nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, ">3 books · 1 folder · 1 hour<") || !strings.Contains(body, `<title>Library · Tendshelf</title>`) {
		t.Fatalf("owner: %d, count line missing", rec.Code)
	}
	if rec := browseGet(a, "/libraries/"+f.LibraryID, false, nil); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), ">Audiobooks<") {
		t.Errorf("owner /libraries/{id}: %d", rec.Code)
	}
	if rec := browseGet(a, "/libraries/lib_unknown", false, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown library: %d, want 404", rec.Code)
	}

	member := a.as(t, users.RoleMember)
	mrec := browseGet(a, "/libraries", false, member)
	mbody := mrec.Body.String()
	if mrec.Code != http.StatusOK || !strings.Contains(mbody, "Nothing shared yet") || !strings.Contains(mbody, ">0 books · 0 hours<") || strings.Contains(mbody, "Book 001") {
		t.Errorf("member: %d, want the empty state and zero count", mrec.Code)
	}
	if rec := browseGet(a, "/libraries/"+f.LibraryID, false, member); rec.Code != http.StatusNotFound {
		t.Errorf("member on a library they cannot see: %d, want 404", rec.Code)
	}
	if rec := browseGet(a, "/hx/libraries/"+f.LibraryID+"/items", true, member); rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("member fragment: %d, want a 404 toast", rec.Code)
	}

	// Anonymous: a redirect to sign in that returns here; an htmx request gets 204 with HX-Redirect.
	anon := a.do(http.MethodGet, "/libraries?view=list", nil, nil)
	if anon.Code != http.StatusSeeOther || anon.Header().Get("Location") != "/login?next=%2Flibraries%3Fview%3Dlist" {
		t.Errorf("anonymous: %d %q", anon.Code, anon.Header().Get("Location"))
	}
	hx := a.do(http.MethodGet, "/libraries?view=list", nil, func(r *http.Request) { r.Header.Set("HX-Request", "true") })
	if hx.Code != http.StatusNoContent || hx.Header().Get("HX-Redirect") == "" {
		t.Errorf("anonymous htmx: %d %q", hx.Code, hx.Header().Get("HX-Redirect"))
	}
}

func TestLibraryTwoLibraries(t *testing.T) {
	a := browseOwnerApp(t)
	f1, f2 := browseLibrary(t, a, "Fiction"), browseLibrary(t, a, "Podcasts")
	librarytest.AddBook(t, a.lib, f1, librarytest.BookSpec{Title: "In Fiction"})
	librarytest.AddBook(t, a.lib, f2, librarytest.BookSpec{Title: "In Podcasts"})

	all := browseGet(a, "/libraries", false, nil).Body.String()
	for _, want := range []string{`aria-label="Libraries"`, `href="/libraries/` + f1.LibraryID + `"`, "Fiction", "Podcasts", "In Fiction", "In Podcasts", ">2 books · 2 folders · 0 hours<"} {
		if !strings.Contains(all, want) {
			t.Errorf("all libraries lacks %q", want)
		}
	}
	one := browseGet(a, "/libraries/"+f1.LibraryID, false, nil).Body.String()
	if !strings.Contains(one, "In Fiction") || strings.Contains(one, "In Podcasts") || !strings.Contains(one, ">1 book · 1 folder · 0 hours<") {
		t.Error("one library shows the other's books or the wrong count")
	}
	// A single library needs no chips.
	b := browseOwnerApp(t)
	browseLibrary(t, b, "Only")
	if body := browseGet(b, "/libraries", false, nil).Body.String(); strings.Contains(body, `aria-label="Libraries"`) {
		t.Error("library chips with one library")
	}
}

func TestLibrarySortsAndView(t *testing.T) {
	a := browseOwnerApp(t)
	f := browseLibrary(t, a, "Audiobooks")
	for i, s := range []struct {
		title, author string
		ms            int64
	}{{"Charlie", "Zed Zimmer", 3 * 3_600_000}, {"Alpha", "Yan Young", 90 * 60_000}, {"Bravo", "Xi Xu", 3_600_000}} {
		librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: s.title, Authors: []string{s.author}, DurationMs: s.ms, AddedAt: int64(i + 1)})
	}
	// Durations: Charlie 3 h, Alpha 1 h 30 m, Bravo 1 h.
	for _, c := range []struct {
		query string
		want  []string
	}{
		{"", []string{"Bravo", "Alpha", "Charlie"}},
		{"?sort=added", []string{"Bravo", "Alpha", "Charlie"}},
		{"?sort=title", []string{"Alpha", "Bravo", "Charlie"}},
		{"?sort=author", []string{"Bravo", "Alpha", "Charlie"}},
		{"?sort=duration", []string{"Charlie", "Alpha", "Bravo"}},
		{"?sort=bogus", []string{"Bravo", "Alpha", "Charlie"}},
		{"?sort=title&view=list", []string{"Alpha", "Bravo", "Charlie"}},
	} {
		rec := browseGet(a, "/libraries"+c.query, false, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("%q: %d", c.query, rec.Code)
			continue
		}
		if got := browseTitles(rec.Body.String()); strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%q: order %v, want %v", c.query, got, c.want)
		}
	}

	// The sort and the view live in the addresses of the controls.
	body := html.UnescapeString(browseGet(a, "/libraries?sort=title", false, nil).Body.String())
	for _, want := range []string{`href="/libraries?sort=title"`, `href="/libraries?sort=title&view=list"`, ">Title<"} {
		if !strings.Contains(body, want) {
			t.Errorf("sort=title page lacks %q", want)
		}
	}
	list := html.UnescapeString(browseGet(a, "/libraries?view=list&sort=duration", false, nil).Body.String())
	for _, want := range []string{`class="ts-row"`, "Zed Zimmer · 3 h", "Yan Young · 1 h 30 m", `href="/libraries?sort=duration"`, ">Length<"} {
		if !strings.Contains(list, want) {
			t.Errorf("list view lacks %q", want)
		}
	}
	if strings.Contains(list, "ts-card__title") || !strings.Contains(list, "ts-lib-rows") {
		t.Error("list view still draws cards")
	}
	// An unknown view is the grid.
	if grid := browseGet(a, "/libraries?view=wide", false, nil).Body.String(); !strings.Contains(grid, "ts-grid--covers") || strings.Contains(grid, "ts-lib-rows") {
		t.Error("unknown view should fall back to the grid")
	}
}

func TestLibraryGenres(t *testing.T) {
	a := browseOwnerApp(t)
	f := browseLibrary(t, a, "Audiobooks")
	add := func(title string, genres ...string) {
		librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: title, Genres: genres})
	}
	add("Cold Case", "Mystery", "Thriller")
	add("Locked Room", "Mystery")
	add("Dead Letter", "Mystery")
	add("Love Letters", "Romance")
	add("Dragon Year", "Fantasy")
	add("Six Of Six", "g1", "g2", "g3", "g4", "g5", "g6")
	add("Five Of Six", "g1", "g2", "g3", "g4", "g5")

	body := html.UnescapeString(browseGet(a, "/libraries", false, nil).Body.String())
	// Top three genres by use: Mystery (3), then g1 and g2 (2 each, by name).
	for _, want := range []string{`href="/libraries?genre=Mystery"`, "More filters", `aria-label="Filters"`} {
		if !strings.Contains(body, want) {
			t.Errorf("genre chips lack %q", want)
		}
	}
	if strings.Contains(body, "Clear filters") {
		t.Error("Clear filters without a filter")
	}

	sel := html.UnescapeString(browseGet(a, "/libraries?genre=Mystery", false, nil).Body.String())
	if titles := browseTitles(sel); len(titles) != 3 || strings.Contains(strings.Join(titles, ","), "Love") {
		t.Errorf("genre=Mystery shows %v", titles)
	}
	// The chosen chip toggles off: its address drops the genre, and it says it is on.
	if !strings.Contains(sel, `aria-current="true"`) || !strings.Contains(sel, `href="/libraries"`) || !strings.Contains(sel, "Clear filters") || !strings.Contains(sel, ">3 books match<") {
		t.Error("selected genre chip, toggle address, Clear filters or count missing")
	}

	// AND filter: only the book with both.
	and := browseTitles(browseGet(a, "/libraries?genre=Mystery&genre=Thriller", false, nil).Body.String())
	if len(and) != 1 || and[0] != "Cold Case" {
		t.Errorf("Mystery AND Thriller shows %v", and)
	}

	// Six genres: the sixth is ignored, so the book with five also matches.
	six := browseTitles(browseGet(a, "/libraries?genre=g1&genre=g2&genre=g3&genre=g4&genre=g5&genre=g6", false, nil).Body.String())
	if len(six) != 2 {
		t.Errorf("six genres show %v, want both books (the sixth is ignored)", six)
	}
	long := browseGet(a, "/libraries?genre="+strings.Repeat("x", 101), false, nil)
	if long.Code != http.StatusOK || len(browseTitles(long.Body.String())) != 7 {
		t.Error("a genre over 100 runes should be ignored")
	}

	none := browseGet(a, "/libraries?genre=Mystery&genre=Romance", false, nil)
	nb := html.UnescapeString(none.Body.String())
	if none.Code != http.StatusOK || !strings.Contains(nb, "No books match these filters") || !strings.Contains(nb, ">Clear filters<") || !strings.Contains(nb, ">0 books match<") {
		t.Errorf("no-match state missing: %d", none.Code)
	}
	if strings.Contains(nb, "Your shelf is empty") || strings.Contains(nb, `id="library-grid"`) {
		t.Error("no-match shows the wrong state")
	}
}

func TestLibraryItemsFragment(t *testing.T) {
	a := browseOwnerApp(t)
	f := browseLibrary(t, a, "Audiobooks")
	browseAddMany(t, a, f, 130)

	rec := browseGet(a, "/libraries?sort=title", false, nil)
	seen := map[string]bool{}
	var pages []int
	body := rec.Body.String()
	next := browseMore(body)
	ids := browseIDs(body)
	for _, id := range ids {
		seen[id] = true
	}
	pages = append(pages, len(ids))
	if !strings.HasPrefix(next, "/hx/libraries/items?") || !strings.Contains(next, "sort=title") || !strings.Contains(next, "cursor=") {
		t.Fatalf("first sentinel %q", next)
	}
	for n := 0; next != ""; n++ {
		if n > 5 {
			t.Fatal("the walk does not end")
		}
		frag := browseGet(a, next, true, nil)
		fb := frag.Body.String()
		if frag.Code != http.StatusOK || strings.Contains(fb, "<html") || strings.Contains(fb, `id="main"`) || strings.Contains(fb, `class="ts-library"`) {
			t.Fatalf("fragment %s: %d, not a bare fragment", next, frag.Code)
		}
		got := browseIDs(fb)
		pages = append(pages, len(got))
		for _, id := range got {
			if seen[id] {
				t.Fatalf("book %s appears twice", id)
			}
			seen[id] = true
		}
		next = browseMore(fb)
	}
	if len(seen) != 130 || fmt.Sprint(pages) != "[48 48 34]" {
		t.Errorf("%d books in pages %v, want 130 in [48 48 34]", len(seen), pages)
	}

	bad := browseGet(a, "/hx/libraries/items?cursor=garbage", true, nil)
	if bad.Code != http.StatusBadRequest || !strings.Contains(bad.Body.String(), "This list changed. Reload the page to see it.") || strings.Contains(bad.Body.String(), "<html") {
		t.Errorf("bad cursor: %d %q", bad.Code, bad.Body.String())
	}
	if rec := browseGet(a, "/libraries?cursor=garbage", false, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("page with a bad cursor: %d, want 400", rec.Code)
	}
	if rec := browseGet(a, "/hx/libraries/lib_nope/items", true, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown library fragment: %d", rec.Code)
	}
	// The per-library fragment works and stays in the library.
	one := browseGet(a, "/libraries/"+f.LibraryID, false, nil).Body.String()
	if !strings.HasPrefix(browseMore(one), "/hx/libraries/"+f.LibraryID+"/items?cursor=") {
		t.Errorf("library sentinel %q", browseMore(one))
	}
}

func TestLibraryNoJSPaging(t *testing.T) {
	a := browseOwnerApp(t)
	f := browseLibrary(t, a, "Audiobooks")
	browseAddMany(t, a, f, 60)

	first := html.UnescapeString(browseGet(a, "/libraries?sort=title", false, nil).Body.String())
	m := regexp.MustCompile(`<a class="ts-more" href="(/libraries\?[^"]*cursor=[^"]*)"`).FindStringSubmatch(first)
	if m == nil || !strings.Contains(first, "Show more") {
		t.Fatal("no Show more link with a cursor")
	}
	if strings.Contains(first, "Back to the start") {
		t.Error("the first page offers a way back to the start")
	}
	second := browseGet(a, m[1], false, nil)
	sb := html.UnescapeString(second.Body.String())
	if second.Code != http.StatusOK || !strings.Contains(sb, `<html`) || !strings.Contains(sb, "Back to the start") || !strings.Contains(sb, `class="ts-library"`) {
		t.Fatalf("cursor page: %d, not a full page with a way back", second.Code)
	}
	if got := browseTitles(sb); len(got) != 12 || got[0] != "Book 049" {
		t.Errorf("second page %d books starting %v", len(got), got[:1])
	}
	if strings.Contains(sb, "Show more") {
		t.Error("the last page offers Show more")
	}
	if !strings.Contains(sb, `>Back to the start<`) || !strings.Contains(sb, `href="/libraries?sort=title"`) {
		t.Error("Back to the start does not return to the first page")
	}
}

func TestLibraryFilterSheet(t *testing.T) {
	a := browseOwnerApp(t)
	f := browseLibrary(t, a, "Audiobooks")
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "One", Genres: []string{"Mystery", "Thriller"}})

	plain := browseGet(a, "/libraries", false, nil).Body.String()
	if !regexp.MustCompile(`id="sheet"[^>]*>\s*</div>`).MatchString(plain) || strings.Contains(plain, "Filter and sort") {
		t.Error("the sheet is open without ?sheet=filters")
	}
	// The chip that opens it loads the sheet into #sheet.
	if !strings.Contains(plain, `hx-get="/libraries?sheet=filters"`) || !strings.Contains(plain, `hx-target="#sheet"`) {
		t.Error("the sort chip does not open the sheet")
	}

	rec := browseGet(a, "/libraries?genre=Mystery&sheet=filters", false, nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	i := strings.Index(body, `id="sheet"`)
	if i < 0 || !strings.Contains(body[i:], "Filter and sort") || !strings.Contains(body[i:], `role="dialog"`) {
		t.Fatal("the sheet is not inside #sheet")
	}
	main := body[:i]
	if strings.Contains(main, "Filter and sort") {
		t.Error("the sheet is rendered inside #main")
	}
	sheet := body[i:]
	for _, want := range []string{
		`<form class="ts-filter-form" method="get" action="/libraries" hx-select-oob="#sheet">`, `Sort by`, `Recently added`, `Title`, `Author`, `Length`,
		`type="radio"`, `name="sort"`, `type="checkbox"`, `name="genre" value="Mystery" checked`, `name="genre" value="Thriller"`, `>Show books<`, `data-ts-close`,
	} {
		if !strings.Contains(sheet, want) {
			t.Errorf("sheet lacks %q", want)
		}
	}
	if strings.Contains(sheet, `value="Thriller" checked`) {
		t.Error("an unchosen genre is checked")
	}
	// The sheet form and the inline bar both are GET forms to this page; the bar carries the genres.
	if got := strings.Count(body, `<form class="ts-filter-form" method="get" action="/libraries"`); got != 2 {
		t.Errorf("%d filter forms, want the sheet's and the bar's", got)
	}
	if strings.Contains(body, "csrf") && strings.Contains(sheet, `name="_csrf"`) {
		t.Error("a GET form carries a CSRF field")
	}
}

func TestLibraryHidesRefusedAndMissing(t *testing.T) {
	a := browseOwnerApp(t)
	f := browseLibrary(t, a, "Audiobooks")
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Shown One", Genres: []string{"Visible"}})
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Shown Two", Genres: []string{"Visible"}})
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Protected Book", Refused: library.RefusedDRM, Genres: []string{"Secret"}})
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Gone Book", MissingSince: 1, Genres: []string{"Secret"}})

	for _, path := range []string{"/", "/libraries", "/libraries?view=list", "/libraries?sheet=filters", "/libraries?genre=Secret"} {
		body := browseGet(a, path, false, nil).Body.String()
		for _, not := range []string{"Protected Book", "Gone Book"} {
			if strings.Contains(body, not) {
				t.Errorf("%s lists %q", path, not)
			}
		}
		if path != "/libraries?genre=Secret" && strings.Contains(body, ">Secret<") {
			t.Errorf("%s offers the genre of hidden books", path)
		}
	}
	if body := browseGet(a, "/libraries", false, nil).Body.String(); !strings.Contains(body, ">2 books · 1 folder · 0 hours<") {
		t.Error("the count includes hidden books")
	}
}

func TestCardViewCoverURLs(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	c := library.BookCard{
		ID: "bk_1", Title: "Salt and Ember", Authors: []library.PersonRef{{Name: "Ruth Okafor"}, {Name: "Two"}, {Name: "Three"}},
		DurationMs: 63_120_000, Cover: &library.CoverRef{SHA: sha, Tint: "#8c3b24", Width: 800, Height: 800},
	}
	v := cardView(c)
	wantSrcset := "/covers/bk_1?w=160&v=01234567 160w, /covers/bk_1?w=320&v=01234567 320w, /covers/bk_1?w=640&v=01234567 640w"
	if v.CoverSrc != "/covers/bk_1?w=320&v=01234567" || v.CoverSrcset != wantSrcset {
		t.Errorf("cover address %q, srcset %q", v.CoverSrc, v.CoverSrcset)
	}
	if v.Href != "/books/bk_1" || v.Byline != "Ruth Okafor, Two and 1 more" || v.Duration != "17 h 32 m" ||
		v.AriaLabel != "Salt and Ember by Ruth Okafor, Two and 1 more" || v.Tint != "#8c3b24" || v.InkClass != "ts-ink-paper" {
		t.Errorf("card view %+v", v)
	}
	var buf bytes.Buffer
	if err := components.Cover(v, "fill").Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	img := buf.String()
	for _, want := range []string{`src="/covers/bk_1?w=320&amp;v=01234567"`, `srcset="`, ` 640w`, `sizes="`, `width="800"`, `height="800"`, `loading="lazy"`, `decoding="async"`, `alt=""`} {
		if !strings.Contains(img, want) {
			t.Errorf("cover image lacks %q: %s", want, img)
		}
	}
	if strings.Contains(img, "data-ts-tint") || strings.Contains(img, "ts-cover--blank") {
		t.Error("a book with a cover is drawn blank")
	}

	// No cover file: a blank cover that carries the title and, when the book has one, the tint.
	blank := cardView(library.BookCard{ID: "bk_2", Title: "Plain Title", Cover: &library.CoverRef{Tint: "#1f4a52"}})
	buf.Reset()
	if err := components.Cover(blank, "fill").Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{`ts-cover--blank`, `data-ts-tint="#1f4a52"`, `ts-ink-paper`, `Plain Title`, `aria-hidden="true"`} {
		if !strings.Contains(out, want) {
			t.Errorf("blank cover lacks %q: %s", want, out)
		}
	}
	if strings.Contains(out, "<img") {
		t.Error("a blank cover has an image")
	}
	bare := cardView(library.BookCard{ID: "bk_3", Title: "No Cover At All"})
	if bare.CoverSrc != "" || bare.Tint != "" || bare.AriaLabel != "No Cover At All" || bare.Byline != "" {
		t.Errorf("bare card %+v", bare)
	}
}

func TestSpineInk(t *testing.T) {
	for _, c := range []struct {
		tint, class string
		ok          bool
	}{
		{"#1f4a52", "ts-ink-paper", true},  // dark teal
		{"#8c3b24", "ts-ink-paper", true},  // rust
		{"#d9d4c7", "ts-ink-walnut", true}, // pale
		{"#efe3c8", "ts-ink-walnut", true}, // cream
		{"#c7894e", "", false},             // amber: walnut reaches only about 3:1, so the tint is not used
		{"#808080", "", false},             // mid grey: neither ink reaches 4.5:1
		{"", "", false},
		{"red", "", false},
	} {
		class, ok := spineInk(c.tint)
		if class != c.class || ok != c.ok {
			t.Errorf("spineInk(%q) = %q, %v; want %q, %v", c.tint, class, ok, c.class, c.ok)
		}
	}
	// A tint with no readable ink is not used by the card either.
	if v := cardView(library.BookCard{ID: "bk_1", Title: "T", Cover: &library.CoverRef{Tint: "#808080"}}); v.Tint != "" || v.InkClass != "" {
		t.Errorf("unreadable tint kept: %+v", v)
	}

	// The two inks are the brand tokens, so the contrast decided here is the contrast on screen.
	css, err := os.ReadFile("static/css/tokens.css")
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"--brand-paper": spinePaper, "--brand-walnut": spineWalnut} {
		m := regexp.MustCompile(regexp.QuoteMeta(name) + `:\s*(#[0-9a-fA-F]{6})`).FindSubmatch(css)
		if m == nil || strings.ToLower(string(m[1])) != want {
			t.Errorf("%s in tokens.css is %q, the Go constant is %q", name, m, want)
		}
	}
}

func TestSpineSizes(t *testing.T) {
	for _, c := range []struct {
		ms   int64
		want string
	}{{0, "ts-spine--ws"}, {4 * 3_600_000, "ts-spine--ws"}, {5 * 3_600_000, "ts-spine--wm"}, {14 * 3_600_000, "ts-spine--wm"}, {15 * 3_600_000, "ts-spine--wl"}} {
		if got := spineWidth(c.ms); got != c.want {
			t.Errorf("spineWidth(%d) = %s, want %s", c.ms, got, c.want)
		}
	}
	seen := map[string]bool{}
	for _, id := range []string{"a", "b", "c", "d"} {
		seen[spineHeight("bk_"+id)] = true
	}
	if len(seen) != 3 || spineHeight("") != "ts-spine--h2" || spineHeight("bk_a") != spineHeight("bk_a") {
		t.Errorf("heights %v", seen)
	}
}

var (
	browseScripts    = regexp.MustCompile(`<script\b[^>]*>`)
	browseScriptsSrc = regexp.MustCompile(`<script\b[^>]*\bsrc=[^>]*>`)
)

func TestBrowseNoInlineStyles(t *testing.T) {
	a := browseOwnerApp(t)
	f := browseLibrary(t, a, "Audiobooks")
	b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Tinted", Authors: []string{"Imogen Hale"}, Genres: []string{"Mystery"}, DurationMs: 3_600_000})
	cover := library.Cover{BookID: b.ID, Source: library.CoverFolder, Mime: "image/png", Width: 800, Height: 800, SHA256: coverSHA, Path: "covers/" + coverSHA + ".jpg", Tint: "#1f4a52"}
	if err := a.lib.Update(context.Background(), func(tx *library.Tx) error {
		if err := tx.PutCover(&cover); err != nil {
			return err
		}
		return tx.SetBookCover(b.ID, cover.ID)
	}); err != nil {
		t.Fatal(err)
	}
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Blank"})
	a.adminEnqueueScan(t, f.LibraryID)

	for _, path := range []string{"/", "/libraries", "/libraries?view=list", "/libraries?sheet=filters&genre=Mystery", "/libraries?genre=Nope"} {
		body := browseGet(a, path, false, nil).Body.String()
		if regexp.MustCompile(`\sstyle\s*=`).MatchString(body) {
			t.Errorf("%s has an inline style attribute", path)
		}
		if len(browseScripts.FindAllString(body, -1)) != len(browseScriptsSrc.FindAllString(body, -1)) {
			t.Errorf("%s has an inline script", path)
		}
		if strings.Contains(body, " hx-on") || strings.Contains(body, `hx-trigger="`+"revealed[") {
			t.Errorf("%s uses a banned htmx attribute", path)
		}
	}
	if body := browseGet(a, "/", false, nil).Body.String(); !strings.Contains(body, `data-ts-tint="#1f4a52"`) {
		t.Error("the tint does not reach the page as data-ts-tint")
	}
}

func TestDurationText(t *testing.T) {
	const h, m = int64(3_600_000), int64(60_000)
	for _, c := range []struct {
		ms   int64
		want string
	}{
		{0, ""}, {-5, ""}, {1, "1 m"}, {29 * 1000, "1 m"}, {48 * m, "48 m"}, {59*m + 40_000, "1 h"}, {h, "1 h"}, {5 * h, "5 h"},
		{17*h + 32*m, "17 h 32 m"}, {17*h + 32*m + 29_000, "17 h 32 m"}, {17*h + 32*m + 31_000, "17 h 33 m"}, {100 * h, "100 h"},
	} {
		if got := durationText(c.ms); got != c.want {
			t.Errorf("durationText(%d) = %q, want %q", c.ms, got, c.want)
		}
	}
	for n, want := range map[int]string{0: "0 books", 1: "1 book", 2: "2 books", 1234: "1,234 books"} {
		if got := browseCount(n); got != want {
			t.Errorf("browseCount(%d) = %q, want %q", n, got, want)
		}
	}
}
