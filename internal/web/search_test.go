package web

import (
	"fmt"
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// bookSearchApp seeds two Harbour Lights books, a loose book and 25 "Lantern" books.
func bookSearchApp(t *testing.T) *testApp {
	t.Helper()
	a, f := bookApp(t)
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "The Lighthouse Keeper's Daughter", Authors: []string{"Imogen Hale"}, Narrators: []string{"Theo Marsh"}, Series: "Harbour Lights", Sequence: "1", DurationMs: 3_600_000})
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "The Fog Bell", Authors: []string{"Imogen Hale"}, Narrators: []string{"Theo Marsh"}, Series: "Harbour Lights", Sequence: "2", DurationMs: 3_600_000})
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Salt and Ember", Authors: []string{"Ruth Okafor"}})
	for i := 0; i < 25; i++ {
		librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: fmt.Sprintf("Lantern Street %02d", i), Authors: []string{"Elias Varn"}})
	}
	return a
}

func bookSearchGet(a *testApp, q, only string, ck *http.Cookie) string {
	v := url.Values{"q": {q}}
	if only != "" {
		v.Set("only", only)
	}
	return browseGet(a, "/search?"+v.Encode(), false, ck).Body.String()
}

func TestSearchPage(t *testing.T) {
	a := bookSearchApp(t)

	body := bookSearchGet(a, "harbour", "", nil)
	main := bookMain(body)
	for _, want := range []string{`role="search"`, `<h1 class="ts-sr">Search</h1>`, `>2 books found<`, ">Books · 2<", ">Series · 1<", ">Harbour Lights<", ">The Fog Bell<",
		`href="/series/`, `aria-label="Clear search"`, `aria-label="Show only"`} {
		if !strings.Contains(main, want) {
			t.Errorf("harbour: missing %s", want)
		}
	}
	if strings.Contains(main, "Salt and Ember") || strings.Contains(main, ">People ·") {
		t.Error("harbour: an unrelated book or an empty people group is listed")
	}
	if !strings.Contains(body, `<title>Search · Tendshelf</title>`) {
		t.Error("title")
	}

	people := bookMain(bookSearchGet(a, "imogen", "", nil))
	if !strings.Contains(people, ">People · 1<") || !strings.Contains(people, ">Author<") || !strings.Contains(people, `href="/authors/`) || !strings.Contains(people, ">Imogen Hale<") {
		t.Error("imogen: the author is not listed under People")
	}
	nar := bookMain(bookSearchGet(a, "theo", "", nil))
	if !strings.Contains(nar, ">Narrator<") || !strings.Contains(nar, `href="/narrators/`) {
		t.Error("theo: the narrator is not listed")
	}

	// Only views keep one group and the query in every segment link.
	onlyBooks := bookMain(bookSearchGet(a, "harbour", "books", nil))
	if !strings.Contains(onlyBooks, ">Books · 2<") || strings.Contains(onlyBooks, ">Series ·") || !strings.Contains(onlyBooks, `<input type="hidden" name="only" value="books">`) {
		t.Error("only=books")
	}
	onlySeries := bookMain(bookSearchGet(a, "harbour", "series", nil))
	if strings.Contains(onlySeries, ">Books ·") || !strings.Contains(onlySeries, ">Series · 1<") || !strings.Contains(onlySeries, ">1 series found<") {
		t.Error("only=series")
	}
	onlyPeople := bookMain(bookSearchGet(a, "imogen", "people", nil))
	if !strings.Contains(onlyPeople, ">People · 1<") || strings.Contains(onlyPeople, ">Books ·") || !strings.Contains(onlyPeople, ">1 people found<") {
		t.Error("only=people")
	}
	if !strings.Contains(main, `href="/search?only=people&amp;q=harbour"`) || !regexp.MustCompile(`href="/search\?q=harbour" aria-current="true"`).MatchString(main) {
		t.Error("segments do not keep the query or mark the current one")
	}

	// 25 books: Everything shows 20 and a link to all of them; Books shows all 25.
	every := bookMain(bookSearchGet(a, "lantern", "", nil))
	if n := strings.Count(every, `class="ts-row__title`); n != 20 || !strings.Contains(every, ">Show all 25 books<") || !strings.Contains(every, `href="/search?only=books&amp;q=lantern"`) || !strings.Contains(every, ">25 books found<") {
		t.Errorf("everything: %d rows", n)
	}
	all := bookMain(bookSearchGet(a, "lantern", "books", nil))
	if n := strings.Count(all, `class="ts-row__title`); n != 25 || strings.Contains(all, "Show all") {
		t.Errorf("books: %d rows", n)
	}

	// Empty query: the field and the segments only.
	empty := bookMain(bookSearchGet(a, "", "", nil))
	if strings.Contains(empty, "ts-row") || strings.Contains(empty, "ts-empty") || strings.Contains(empty, `aria-label="Clear search"`) || !strings.Contains(empty, `id="search-results"`) {
		t.Error("empty query shows results")
	}

	// A long query is cut to 200 runes in the field.
	long := "lighthouse " + strings.Repeat("é", 400)
	field := regexp.MustCompile(`name="q" value="([^"]*)"`).FindStringSubmatch(bookSearchGet(a, long, "", nil))
	if field == nil || utf8.RuneCountInString(html.UnescapeString(field[1])) != 200 {
		t.Errorf("long query field: %v", field)
	}
}

func TestSearchNoResultsDidYouMean(t *testing.T) {
	a := bookSearchApp(t)
	main := bookMain(bookSearchGet(a, "lighthuose", "", nil))
	for _, want := range []string{"Nothing for “lighthuose”", "Check the spelling, or try an author or narrator.", "Did you mean", `href="/search?q=lighthouse"`, ">lighthouse<", ">Clear search<", ">0 books found<"} {
		if !strings.Contains(main, want) {
			t.Errorf("misspelling: missing %q", want)
		}
	}
	nothing := bookMain(bookSearchGet(a, "zzzqqqxxx", "", nil))
	if !strings.Contains(nothing, "Nothing for “zzzqqqxxx”") || strings.Contains(nothing, "Did you mean") || !strings.Contains(nothing, ">Clear search<") {
		t.Error("nothing found: wrong empty state")
	}
	// Only views: the suggestion keeps the view; people never show an empty group.
	if body := bookMain(bookSearchGet(a, "lighthuose", "books", nil)); !strings.Contains(body, `href="/search?only=books&amp;q=lighthouse"`) {
		t.Error("the suggestion forgets the view")
	}
	if body := bookMain(bookSearchGet(a, "lantern", "people", nil)); !strings.Contains(body, "Nothing for “lantern”") {
		t.Error("people view with no people shows no empty state")
	}
	// The query is text: it is escaped in the title and the field.
	xss := bookSearchGet(a, `<script>alert(1)</script>`, "", nil)
	if strings.Contains(xss, "<script>alert") {
		t.Error("the query is not escaped")
	}
}

func TestSearchLiveFragment(t *testing.T) {
	a := bookSearchApp(t)
	rec := browseGet(a, "/search?q=harbour", true, nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "<html") || !strings.Contains(body, `id="search-results"`) || !strings.Contains(body, ">Books · 2<") {
		t.Fatalf("htmx request: %d, want a full page with #search-results", rec.Code)
	}
	input := regexp.MustCompile(`<input id="search-q"[^>]*>`).FindString(body)
	for _, want := range []string{`type="search"`, `name="q"`, `hx-get="/search"`, `hx-trigger="input changed delay:150ms, search"`, `hx-target="#search-results"`, `hx-select="#search-results"`,
		`hx-swap="outerHTML"`, `hx-replace-url="true"`, `hx-include="closest form"`, `hx-select-oob="#search-segments, #search-clear"`, `placeholder="Search books, authors, narrators and series"`} {
		if !strings.Contains(input, want) {
			t.Errorf("input lacks %s", want)
		}
	}
	if !strings.Contains(body, `<label class="ts-sr" for="search-q">Search</label>`) || !strings.Contains(body, `<form class="ts-sf__form" role="search" method="get" action="/search">`) {
		t.Error("label or form wrong")
	}
	if strings.Contains(body, "hx-on") || strings.Contains(body, "js:") {
		t.Error("banned htmx attributes")
	}
}

func TestSearchScope(t *testing.T) {
	a := bookSearchApp(t)
	member := a.as(t, users.RoleMember)
	for _, q := range []string{"harbour", "imogen", "theo", "lighthuose"} {
		body := bookMain(bookSearchGet(a, q, "", member))
		for _, not := range []string{"Lighthouse", "Fog Bell", "Imogen", "Theo", "Harbour Lights", "Did you mean", ">People ·", ">Series ·", ">Books ·"} {
			if strings.Contains(body, not) {
				t.Errorf("member %q sees %q", q, not)
			}
		}
		if !strings.Contains(body, ">0 books found<") || !strings.Contains(body, "Nothing for") {
			t.Errorf("member %q: not the plain no-results state", q)
		}
	}
	anon := a.do(http.MethodGet, "/search?q=harbour", nil, nil)
	if anon.Code != http.StatusSeeOther || anon.Header().Get("Location") != "/login?next=%2Fsearch%3Fq%3Dharbour" {
		t.Errorf("anonymous: %d %q", anon.Code, anon.Header().Get("Location"))
	}
}

func TestSearchInjection(t *testing.T) {
	a := bookSearchApp(t)
	for _, q := range []string{`"`, `*`, `NEAR(`, `title:x`, `a" OR "b`, `)(`, `-`, `AND`, `NOT x`, `^x`, "\x00", `'; DROP TABLE books; --`, `{title}:x`, `é*`} {
		for _, only := range []string{"", "books", "people", "series"} {
			rec := browseGet(a, "/search?"+url.Values{"q": {q}, "only": {only}}.Encode(), false, nil)
			if rec.Code != http.StatusOK {
				t.Errorf("q=%q only=%q: %d", q, only, rec.Code)
			}
		}
	}
	// The library is still there.
	if body := bookSearchGet(a, "harbour", "", nil); !strings.Contains(body, ">Books · 2<") {
		t.Error("search broke after the hostile queries")
	}
}

func TestSearchNoInlineStyles(t *testing.T) {
	a := bookSearchApp(t)
	styleAttr := regexp.MustCompile(`\sstyle\s*=`)
	for _, path := range []string{"/search", "/search?q=harbour", "/search?q=imogen&only=people", "/search?q=lighthuose", "/search?q=zzzz"} {
		body := browseGet(a, path, false, nil).Body.String()
		if styleAttr.MatchString(body) || strings.Contains(body, "<style") || strings.Contains(body, "<script>") {
			t.Errorf("%s has inline style or script", path)
		}
	}
}
