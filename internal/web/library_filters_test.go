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

// Tests for the Library filter header (decision 0008): the Filter menu, the kinds row opened by add=,
// the Show menu with its Sort by group, the Category menu, and the retired filter sheet.

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

// libFilterRegion cuts the <details> element that starts at id.
func libFilterRegion(t *testing.T, body, id string) string {
	t.Helper()
	i := strings.Index(body, `id="`+id+`"`)
	if i < 0 {
		t.Fatalf("no %s", id)
	}
	j := strings.Index(body[i:], "</details>")
	if j < 0 {
		t.Fatalf("%s is not closed", id)
	}
	return body[i : i+j]
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
		if !strings.Contains(menu, `href="/libraries?add=`+k+`"`) {
			t.Errorf("the Filter menu lacks the %s kind", k)
		}
	}
	if strings.Contains(menu, "add=category") {
		t.Error("the Filter menu offers Category without genres")
	}
	for _, not := range []string{`id="lib-menu-author"`, `aria-label="Filters"`, "<select", "More filters", "sheet=filters", "ts-lib-sort"} {
		if strings.Contains(body, not) {
			t.Errorf("the default header contains %q", not)
		}
	}
	list := libFilterPage(t, a, "?view=list&sort=title")
	if !strings.Contains(list, `href="/libraries?add=author&sort=title&view=list"`) {
		t.Error("a kind link loses the view or the sort")
	}
	// The typed title survives choosing a kind (the old header dropped it).
	typed := libFilterPage(t, a, "?title=fog&author="+ids[library.RoleAuthor])
	if !strings.Contains(libFilterRegion(t, typed, "lib-menu-filter"), `href="/libraries?add=narrator&author=`+ids[library.RoleAuthor]+`&title=fog"`) {
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
	if !strings.Contains(body, `<summary class="ts-chip" autofocus>`) || !strings.Contains(libFilterRegion(t, body, "lib-menu-author"), "Any author") {
		t.Error("add=author does not open the Author menu with the focus on it")
	}
	if !strings.Contains(body, `id="lib-menu-author" class="ts-chipmenu" data-ts-chipmenu open>`) {
		t.Error("the Author details is not open")
	}
	if n := len(libFilterOpenMenu.FindAllString(body, -1)); n != 1 {
		t.Errorf("%d open menus, want 1", n)
	}
	// add= appears only in the Filter menu's own links.
	menu := libFilterRegion(t, body, "lib-menu-filter")
	if got, in := len(regexp.MustCompile(`[?&]add=`).FindAllString(body, -1)), len(regexp.MustCompile(`[?&]add=`).FindAllString(menu, -1)); got != in || in == 0 {
		t.Errorf("add= appears %d times, %d inside the Filter menu", got, in)
	}

	chosen := libFilterPage(t, a, "?add=author&author="+author)
	if got := strings.Count(chosen, `id="lib-menu-author"`); got != 1 {
		t.Errorf("%d Author menus, want 1", got)
	}
	if !strings.Contains(chosen, `id="lib-menu-author" class="ts-chipmenu is-on" data-ts-chipmenu open>`) {
		t.Error("the chosen, requested Author menu is not open and on")
	}

	any := libFilterPage(t, a, "?author="+author+"&length=short")
	if !strings.Contains(any, `href="/libraries?length=short" aria-current`) && !strings.Contains(any, `href="/libraries?length=short"`) {
		t.Fatal("no Any author link")
	}
	if cleared := libFilterPage(t, a, "?length=short"); strings.Contains(cleared, `id="lib-menu-author"`) {
		t.Error("the Author chip stays after Any author")
	}

	for _, q := range []string{"?add=bogus", "?add=category"} {
		page := libFilterPage(t, a, q)
		if libFilterOpenMenu.MatchString(page) {
			t.Errorf("%s opens a menu", q)
		}
	}
	// A kind chip near the right edge opens towards the left: the third chip onward.
	three := libFilterPage(t, a, "?author="+author+"&length=short&narrator="+ids[library.RoleNarrator])
	if !strings.Contains(three, `class="ts-chipmenu is-on ts-chipmenu--end"`) {
		t.Error("the third kind chip does not open towards the left")
	}
}

func TestLibFilterShowSort(t *testing.T) {
	a, ids := sweepFilterApp(t)
	body := libFilterPage(t, a, "")
	show := libFilterRegion(t, body, "lib-menu-show")
	for _, want := range []string{">Show</span>", ">Show</p>", ">Sort by</p>", `href="/libraries?sort=title"`, `href="/libraries?sort=author"`, `href="/libraries?sort=duration"`,
		`href="/libraries" aria-current="true"><span class="ts-chipmenu__name">Recently added`} {
		if !strings.Contains(show, want) {
			t.Errorf("the Show menu lacks %q", want)
		}
	}
	if got := strings.Count(show, `class="ts-chipmenu__group"`); got != 2 {
		t.Errorf("%d groups, want Show and Sort by", got)
	}

	sorted := libFilterPage(t, a, "?sort=title&show=unfinished")
	show = libFilterRegion(t, sorted, "lib-menu-show")
	for _, want := range []string{"Show: Unfinished · By title</span>", `id="lib-menu-show" class="ts-chipmenu is-on"`, `href="/libraries?show=unfinished"><span class="ts-chipmenu__name">Recently added`} {
		if !strings.Contains(show, want) {
			t.Errorf("the Show menu lacks %q", want)
		}
	}
	if !strings.Contains(sorted, `name="sort" value="title"`) {
		t.Error("the title form drops the order")
	}
	// Sorting alone is not a filter.
	if only := libFilterPage(t, a, "?sort=author"); strings.Contains(only, `id="lib-menu-show" class="ts-chipmenu is-on"`) || !strings.Contains(only, "Show · By author</span>") {
		t.Error("a sort alone turns the Show chip on, or the chip lacks the order")
	}
	author := ids[library.RoleAuthor]
	from := libFilterPage(t, a, "?author="+author)
	if !strings.Contains(libFilterRegion(t, from, "lib-menu-show"), `href="/libraries?author=`+author+`&sort=title"`) {
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

	menu := libFilterRegion(t, libFilterPage(t, a, "?add=category"), "lib-menu-category")
	for _, want := range []string{`href="/libraries?genre=Mystery"`, `ts-chipmenu__count ts-num">2<`, "Any category"} {
		if !strings.Contains(menu, want) {
			t.Errorf("the Category menu lacks %q", want)
		}
	}
	two := libFilterPage(t, a, "?genre=Mystery&genre=Thriller")
	if !strings.Contains(two, "Category: Mystery +1</span>") {
		t.Error("the Category chip does not name the first genre and the rest")
	}
	if !strings.Contains(libFilterRegion(t, two, "lib-menu-category"), `href="/libraries?genre=Thriller" aria-current="true"`) {
		t.Error("choosing Mystery again should drop it and keep Thriller")
	}
	five := libFilterRegion(t, libFilterPage(t, a, "?genre=g1&genre=g2&genre=g3&genre=g4&genre=g5"), "lib-menu-category")
	if !regexp.MustCompile(`<span class="ts-chipmenu__item is-disabled" aria-disabled="true"><span class="ts-chipmenu__name">g6<`).MatchString(five) || strings.Contains(five, `genre=g6"`) {
		t.Error("at five genres an unchosen genre is not a disabled, link-less line")
	}
	if !strings.Contains(libFilterRegion(t, libFilterPage(t, a, "?sort=title&genre=Mystery"), "lib-menu-category"), `href="/libraries?sort=title"`) {
		t.Error("Any category should keep the sort")
	}
}

func TestLibFilterHeaderNoJS(t *testing.T) {
	a, _ := sweepFilterApp(t)
	raw := browseGet(a, "/libraries?add=author", false, nil).Body.String()
	body := html.UnescapeString(raw)
	for _, id := range []string{"lib-menu-filter", "lib-menu-show", "lib-menu-author"} {
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
