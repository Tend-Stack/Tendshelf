package web

import (
	"html"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

// Tests for the Library filter header (decision 0008, reworked): the Filter menu with a nested
// disclosure per kind, the Show and Sort chips, the Category list, and the retired filter sheet.

// libFilterPage is a Library page with its entities unescaped, so hrefs read as addresses.
func libFilterPage(t *testing.T, a *testApp, query string) string {
	t.Helper()
	rec := browseGet(a, "/libraries"+query, false, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d", query, rec.Code)
	}
	return html.UnescapeString(rec.Body.String())
}

var libFilterOpenMenu = regexp.MustCompile(`<details[^>]* open[ >]`)

// libFilterRegion cuts the <details> element that starts at id, with the details nested in it.
func libFilterRegion(t *testing.T, body, id string) string {
	t.Helper()
	i := strings.Index(body, `id="`+id+`"`)
	if i < 0 {
		t.Fatalf("no %s", id)
	}
	depth, pos := 1, i
	for depth > 0 {
		open, shut := strings.Index(body[pos:], "<details"), strings.Index(body[pos:], "</details>")
		if shut < 0 {
			t.Fatalf("%s is not closed", id)
		}
		if open >= 0 && open < shut {
			depth++
			pos += open + len("<details")
			continue
		}
		depth--
		pos += shut + len("</details>")
	}
	return body[i:pos]
}

func TestLibFilterQueryHrefAdd(t *testing.T) {
	if got := (browseQuery{Sort: library.SortAdded, Add: "author"}).href("/libraries", nil); got != "/libraries" {
		t.Errorf("a nil mod carried add: %q", got)
	}
	if got := (browseQuery{Sort: library.SortAdded, Add: "author", Title: "fog"}).href("/libraries", func(q *browseQuery) { q.Cursor = "c1" }); got != "/libraries?cursor=c1&title=fog" {
		t.Errorf("a paging mod carried add or dropped the title: %q", got)
	}
	got := (browseQuery{Sort: library.SortAdded, Title: "fog"}).href("/libraries", func(q *browseQuery) { q.Add = "author" })
	if got != "/libraries?add=author&title=fog" {
		t.Errorf("a mod that sets add: %q", got)
	}
	for in, want := range map[string]string{"author": "author", "category": "category", "bogus": "", "": ""} {
		r, _ := http.NewRequest(http.MethodGet, "/libraries?add="+in, nil)
		if q := parseBrowseQuery(r); q.Add != want {
			t.Errorf("add=%q parsed as %q, want %q", in, q.Add, want)
		}
	}
	// The typed title rides on every address the header builds.
	r, _ := http.NewRequest(http.MethodGet, "/libraries?title=fog&sort=title", nil)
	q := parseBrowseQuery(r)
	if got := q.href("/libraries", func(q *browseQuery) { q.Length = "short" }); got != "/libraries?length=short&sort=title&title=fog" {
		t.Errorf("the header link drops the title: %q", got)
	}
}

func TestLibFilterKinds(t *testing.T) {
	a, ids := sweepFilterApp(t)
	body := libFilterPage(t, a, "")
	menu := libFilterRegion(t, body, "lib-menu-filter")
	for _, k := range []string{"author", "narrator", "series", "length"} {
		if !strings.Contains(menu, `<details id="lib-kind-`+k+`"`) {
			t.Errorf("the Filter menu lacks the %s kind", k)
		}
	}
	if strings.Contains(menu, `id="lib-kind-category"`) || strings.Contains(menu, `id="lib-kind-format"`) {
		t.Error("the Filter menu offers Category or Format without genres or ebooks")
	}
	// One menu, no separate kind chips, no empty chips.
	for _, not := range []string{`id="lib-menu-author"`, `id="lib-kinds"`, `aria-label="Filters"`, "<select", "More filters", "sheet=filters", "ts-lib-sort", "ts-lib-match", "Clear filters", "books match"} {
		if strings.Contains(body, not) {
			t.Errorf("the default header contains %q", not)
		}
	}
	// The long lists are not in the page: each kind loads its own on first open, or links to add=.
	if strings.Contains(menu, "ts-chipmenu__name\">Una Uno") || !strings.Contains(menu, `hx-get="/hx/library-facets/author"`) || !strings.Contains(menu, `hx-trigger="toggle from:#lib-kind-author once"`) {
		t.Error("the Author list is in the page, or does not load on first open")
	}
	if !strings.Contains(menu, `<noscript><a class="ts-chipmenu__item" href="/libraries?add=author">`) {
		t.Error("the no-JavaScript link to the Author list is missing")
	}
	// Length is short: it rides in the page.
	if !strings.Contains(menu, "Under 5 hours") {
		t.Error("the Length choices are not in the Filter menu")
	}
	list := libFilterPage(t, a, "?view=list&sort=title")
	if !strings.Contains(list, `href="/libraries?add=author&sort=title&view=list"`) || !strings.Contains(list, `hx-get="/hx/library-facets/author?sort=title&view=list"`) {
		t.Error("a kind link loses the view or the sort")
	}
	// The typed title survives (the links carry it).
	typed := libFilterPage(t, a, "?title=fog&author="+ids[library.RoleAuthor])
	if !strings.Contains(libFilterRegion(t, typed, "lib-menu-filter"), `hx-get="/hx/library-facets/narrator?author=`+ids[library.RoleAuthor]+`&title=fog"`) {
		t.Error("the Filter menu drops the typed title")
	}
	if !strings.Contains(libFilterRegion(t, typed, "lib-menu-show"), `href="/libraries?author=`+ids[library.RoleAuthor]+`&show=unfinished&title=fog"`) {
		t.Error("the Show menu drops the typed title")
	}
}

func TestLibFilterAddKind(t *testing.T) {
	a, ids := sweepFilterApp(t)
	author := ids[library.RoleAuthor]
	body := libFilterPage(t, a, "?add=author")
	menu := libFilterRegion(t, body, "lib-menu-filter")
	if !strings.Contains(body, `<summary class="ts-chip" autofocus>`) || !strings.Contains(menu, "Any author") || !strings.Contains(menu, "Una Uno") {
		t.Error("add=author does not open the Filter menu with the Author list and the focus on it")
	}
	if !strings.Contains(body, `<details id="lib-kind-author" class="ts-chipmenu__kind" name="lib-filter-kind" open>`) {
		t.Error("the Author kind is not open")
	}
	if strings.Contains(libFilterRegion(t, body, "lib-kind-narrator"), "hx-get") == false {
		t.Error("another kind lost its lazy load")
	}
	// add= appears only in the Filter menu's own links.
	if got, in := len(regexp.MustCompile(`[?&]add=`).FindAllString(body, -1)), len(regexp.MustCompile(`[?&]add=`).FindAllString(menu, -1)); got != in || in == 0 {
		t.Errorf("add= appears %d times, %d inside the Filter menu", got, in)
	}

	chosen := libFilterPage(t, a, "?author="+author)
	if !strings.Contains(chosen, `<details id="lib-kind-author" class="ts-chipmenu__kind is-on" name="lib-filter-kind">`) || !strings.Contains(chosen, `<span class="ts-chipmenu__chosen">Una Uno</span>`) {
		t.Error("the chosen Author kind is not on or does not name the choice")
	}
	if cleared := libFilterPage(t, a, "?length=short"); strings.Contains(cleared, `chosen">Una Uno`) {
		t.Error("the author stays after Any author")
	}
	for _, q := range []string{"?add=bogus"} {
		if page := libFilterPage(t, a, q); libFilterOpenMenu.MatchString(page) {
			t.Errorf("%s opens a menu", q)
		}
	}
}

func TestLibFilterFacetFragment(t *testing.T) {
	a, ids := sweepFilterApp(t)
	author := ids[library.RoleAuthor]
	rec := browseGet(a, "/hx/library-facets/narrator?author="+author, true, nil)
	body := html.UnescapeString(rec.Body.String())
	if rec.Code != http.StatusOK || strings.Contains(body, "<html") || !strings.Contains(body, "Any narrator") || !strings.Contains(body, "Nel Nox") || !strings.Contains(body, "Ola Oro") {
		t.Errorf("narrator fragment: %d %q", rec.Code, body)
	}
	if !strings.Contains(body, `href="/libraries?author=`+author+`&narrator=`) {
		t.Error("a narrator choice does not keep the author")
	}
	if rec := browseGet(a, "/hx/library-facets/length", true, nil); rec.Code != http.StatusNotFound {
		t.Errorf("length fragment: %d, want 404", rec.Code)
	}
	if rec := browseGet(a, "/hx/library-facets/lib_nobody/author", true, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown library: %d, want 404", rec.Code)
	}
}

func TestLibFilterShowSort(t *testing.T) {
	a, ids := sweepFilterApp(t)
	body := libFilterPage(t, a, "")
	show := libFilterRegion(t, body, "lib-menu-show")
	for _, want := range []string{">Show</span>", ">Show</p>", ">Any<", ">Unfinished<", ">Not started<", ">Finished<"} {
		if !strings.Contains(show, want) {
			t.Errorf("the Show menu lacks %q", want)
		}
	}
	if strings.Contains(show, "Sort by") || strings.Contains(show, "sort=") {
		t.Error("the Show menu still carries the sort")
	}
	sortMenu := libFilterRegion(t, body, "lib-menu-sort")
	for _, want := range []string{">Sort: Recently added</span>", `href="/libraries?sort=title"`, `href="/libraries?sort=author"`, `href="/libraries?sort=duration"`,
		`href="/libraries" aria-current="true"><span class="ts-chipmenu__name">Recently added`, ">Length<"} {
		if !strings.Contains(sortMenu, want) {
			t.Errorf("the Sort menu lacks %q", want)
		}
	}
	if strings.Contains(sortMenu, `class="ts-chipmenu is-on`) {
		t.Error("the Sort chip is styled as a filter")
	}

	sorted := libFilterPage(t, a, "?sort=title&show=unfinished")
	show = libFilterRegion(t, sorted, "lib-menu-show")
	for _, want := range []string{">Show: Unfinished</span>", `id="lib-menu-show" class="ts-chipmenu is-on"`, `href="/libraries?sort=title"`} {
		if !strings.Contains(show, want) {
			t.Errorf("the Show menu lacks %q", want)
		}
	}
	if !strings.Contains(libFilterRegion(t, sorted, "lib-menu-sort"), ">Sort: Title</span>") || !strings.Contains(sorted, `name="sort" value="title"`) {
		t.Error("the Sort chip or the title form lost the order")
	}
	// Sorting alone is not a filter: no row, no count change.
	if only := libFilterPage(t, a, "?sort=author"); strings.Contains(only, "ts-lib-active") || strings.Contains(only, `id="lib-menu-show" class="ts-chipmenu is-on"`) {
		t.Error("a sort alone counts as a filter")
	}
	author := ids[library.RoleAuthor]
	from := libFilterPage(t, a, "?author="+author)
	if !strings.Contains(libFilterRegion(t, from, "lib-menu-sort"), `href="/libraries?author=`+author+`&sort=title"`) {
		t.Error("the Title sort drops the author")
	}
}

func TestLibFilterCategoryMenu(t *testing.T) {
	a := browseOwnerApp(t)
	f := browseLibrary(t, a, "Audiobooks")
	add := func(title string, genres ...string) {
		librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: title, Genres: genres})
	}
	add("Cold Case", "Mystery", "Thriller")
	add("Locked Room", "Mystery")
	add("Love Letters", "Romance")
	add("Dragon Year", "Fantasy")
	add("Six Of Six", "g1", "g2", "g3", "g4", "g5", "g6")

	menu := libFilterRegion(t, libFilterPage(t, a, "?add=category"), "lib-kind-category")
	for _, want := range []string{`href="/libraries?genre=Mystery"`, `ts-chipmenu__count ts-num">2<`, "Any category"} {
		if !strings.Contains(menu, want) {
			t.Errorf("the Category menu lacks %q", want)
		}
	}
	two := libFilterPage(t, a, "?genre=Mystery&genre=Thriller&add=category")
	if !strings.Contains(two, `<span class="ts-chipmenu__chosen">Mystery +1</span>`) {
		t.Error("the Category chip does not name the first genre and the rest")
	}
	if !strings.Contains(libFilterRegion(t, two, "lib-kind-category"), `href="/libraries?genre=Thriller" aria-current="true"`) {
		t.Error("choosing Mystery again should drop it and keep Thriller")
	}
	five := libFilterRegion(t, libFilterPage(t, a, "?genre=g1&genre=g2&genre=g3&genre=g4&genre=g5&add=category"), "lib-kind-category")
	if !regexp.MustCompile(`<span class="ts-chipmenu__item is-disabled" aria-disabled="true"><span class="ts-chipmenu__name">g6<`).MatchString(five) || strings.Contains(five, `genre=g6"`) {
		t.Error("at five genres an unchosen genre is not a disabled, link-less line")
	}
	if !strings.Contains(libFilterRegion(t, libFilterPage(t, a, "?sort=title&genre=Mystery&add=category"), "lib-kind-category"), `href="/libraries?sort=title"`) {
		t.Error("Any category should keep the sort")
	}
}

func TestLibFilterHeaderNoJS(t *testing.T) {
	a, _ := sweepFilterApp(t)
	raw := browseGet(a, "/libraries?add=author", false, nil).Body.String()
	body := html.UnescapeString(raw)
	for _, id := range []string{"lib-menu-filter", "lib-menu-show", "lib-menu-sort", "lib-kind-author"} {
		menu := libFilterRegion(t, body, id)
		if !strings.Contains(menu, `<a class="ts-chipmenu__item" href="/libraries`) {
			t.Errorf("%s has no plain link items", id)
		}
	}
	if !strings.Contains(raw, `<form class="ts-filter-form" method="get" action="/libraries">`) ||
		!regexp.MustCompile(`<noscript>\s*<button type="submit"[^>]*>Show books</button>\s*</noscript>`).MatchString(raw) {
		t.Error("the title form is not a GET form with the no-script submit")
	}
	for _, banned := range []string{"hx-on", "js:", `style="`} {
		if strings.Contains(raw, banned) {
			t.Errorf("the header contains %q", banned)
		}
	}
	retired := browseGet(a, "/libraries?sheet=filters", false, nil)
	rb := retired.Body.String()
	if retired.Code != http.StatusOK || strings.Contains(rb, "Filter and sort") || !regexp.MustCompile(`id="sheet"[^>]*>\s*</div>`).MatchString(rb) {
		t.Error("?sheet=filters is not an ignored parameter")
	}
}

// The title field is a separate form from the kind links: its query keeps what the links carry.
func TestLibFilterTitleFormCarries(t *testing.T) {
	a, ids := sweepFilterApp(t)
	q := url.Values{"author": {ids[library.RoleAuthor]}, "length": {"short"}, "show": {"unfinished"}, "sort": {"title"}, "title": {"fog"}}
	body := libFilterPage(t, a, "?"+q.Encode())
	for _, want := range []string{`name="author" value="` + ids[library.RoleAuthor] + `"`, `name="length" value="short"`, `name="show" value="unfinished"`, `name="sort" value="title"`, `value="fog"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the title form lacks %q", want)
		}
	}
}

// TestLibraryFilterRow checks the active filter row: one chip per filter in use, each a link that
// drops only that filter, "Clear all" that keeps the sort and the view, no row while unfiltered, and
// values written safely.
func TestLibraryFilterRow(t *testing.T) {
	a, ids := sweepFilterApp(t)
	author, narrator, series := ids[library.RoleAuthor], ids[library.RoleNarrator], ids["series"]
	base := url.Values{"sort": {"title"}, "view": {"list"}, "author": {author}, "narrator": {narrator}, "series": {series},
		"genre": {"Mystery", "Thriller"}, "length": {"short"}, "format": {"text"}, "show": {"unfinished"}, "title": {"fog"}}
	_, body := sweepList(t, a, "?"+base.Encode())
	body = html.UnescapeString(body)
	i := strings.Index(body, `class="ts-lib-active"`)
	if i < 0 {
		t.Fatal("no active filter row")
	}
	row := body[i : i+strings.Index(body[i:], "Clear all</a>")+len("Clear all</a>")]
	chips := regexp.MustCompile(`<a class="ts-chip ts-lib-fchip" href="([^"]+)" aria-label="([^"]+)">`).FindAllStringSubmatch(row, -1)
	// Author, Series, Narrator, two categories, Length, Format, Show, Title.
	if len(chips) != 9 {
		t.Fatalf("%d chips, want 9: %s", len(chips), row)
	}
	// Each chip drops exactly its own filter; every other parameter stays.
	drops := map[string]func(url.Values){
		"Remove filter: Author":            func(v url.Values) { v.Del("author") },
		"Remove filter: Series":            func(v url.Values) { v.Del("series") },
		"Remove filter: Narrator":          func(v url.Values) { v.Del("narrator") },
		"Remove filter: Length":            func(v url.Values) { v.Del("length") },
		"Remove filter: Format":            func(v url.Values) { v.Del("format") },
		"Remove filter: Show":              func(v url.Values) { v.Del("show") },
		"Remove filter: Title":             func(v url.Values) { v.Del("title") },
		"Remove filter: Category Mystery":  func(v url.Values) { v["genre"] = []string{"Thriller"} },
		"Remove filter: Category Thriller": func(v url.Values) { v["genre"] = []string{"Mystery"} },
	}
	seen := 0
	for _, c := range chips {
		var fn func(url.Values)
		for prefix, f := range drops {
			if strings.HasPrefix(c[2], prefix) && (strings.HasPrefix(prefix, "Remove filter: Category ") || !strings.HasPrefix(c[2], "Remove filter: Category")) {
				fn = f
			}
		}
		if fn == nil {
			t.Errorf("unexpected chip %q", c[2])
			continue
		}
		want := url.Values{}
		for k, v := range base {
			want[k] = append([]string(nil), v...)
		}
		fn(want)
		u, err := url.Parse(c[1])
		if err != nil || u.Path != "/libraries" || u.Query().Encode() != want.Encode() {
			t.Errorf("%q links to %q, want the query %q", c[2], c[1], want.Encode())
		}
		seen++
	}
	if seen != 9 {
		t.Errorf("%d chips checked", seen)
	}
	for _, want := range []string{`aria-label="Remove filter: Author Una Uno"`, `aria-label="Remove filter: Title “fog”"`, `aria-label="Remove filter: Format Ebooks"`, `aria-label="Remove filter: Show Unfinished"`} {
		if !strings.Contains(row, want) {
			t.Errorf("the row lacks %q", want)
		}
	}
	// Clear all keeps the sort and the view and nothing else.
	if !strings.Contains(row, `href="/libraries?sort=title&view=list">Clear all</a>`) && !strings.Contains(row, `href="/libraries?view=list&sort=title">Clear all</a>`) {
		t.Errorf("Clear all does not keep the sort and the view: %s", row[len(row)-120:])
	}
	// The row comes right after the chips and before the results.
	if strings.Index(body, `class="ts-lib-active"`) < strings.Index(body, `id="library-results"`) {
		t.Error("the filter row is outside the results region")
	}

	// Sort and view alone are not filters.
	for _, q := range []string{"", "?sort=title&view=list"} {
		if plain := libFilterPage(t, a, q); strings.Contains(plain, "ts-lib-active") || strings.Contains(plain, "Clear all") {
			t.Errorf("%q: a row without a filter", q)
		}
	}

	// A typed title is written safely, in the chip and in its link.
	raw := browseGet(a, "/libraries?title="+url.QueryEscape(`<b>"x"&y`), false, nil).Body.String()
	if strings.Contains(raw, "<b>") || !strings.Contains(raw, "&lt;b&gt;") {
		t.Error("a typed title is not escaped in the filter row")
	}
	if !strings.Contains(raw, `href="/libraries"`) {
		t.Error("removing the only filter does not return to the plain list")
	}
}
