package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// Tests for the P1b-9a Library filters, the results region, the header line, the setup cover wall
// and the visual matrix. Helpers here start with "sweep".

const sweepHourMs = int64(3_600_000)

// sweepFilterApp seeds one library: two authors, two narrators, two series, three lengths.
func sweepFilterApp(t *testing.T) (a *testApp, ids map[string]string) {
	t.Helper()
	a = browseOwnerApp(t)
	f := browseLibrary(t, a, "Audiobooks")
	add := func(title, author, narrator, series string, hours int64) library.Book {
		return librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: title, Authors: []string{author}, Narrators: []string{narrator},
			Series: series, DurationMs: hours * sweepHourMs, AddedAt: hours})
	}
	b1 := add("Fog Bell", "Una Uno", "Nel Nox", "Saga", 3)
	add("Foghorn Days", "Una Uno", "Ola Oro", "Saga", 8)
	add("Quiet Mechanic", "Bo Bee", "Nel Nox", "", 20)
	add("Salt and Ember", "Bo Bee", "Ola Oro", "Other", 4)
	ids = map[string]string{"library": f.LibraryID}
	people, _ := a.lib.BookPeople(context.Background(), b1.ID)
	for _, p := range people {
		ids[p.Role] = p.PersonID
	}
	series, _ := a.lib.BookSeries(context.Background(), b1.ID)
	ids["series"] = series[0].SeriesID
	return a, ids
}

func sweepList(t *testing.T, a *testApp, query string) (titles []string, body string) {
	t.Helper()
	rec := browseGet(a, "/libraries"+query, false, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("%s: status %d", query, rec.Code)
	}
	body = html.UnescapeString(rec.Body.String())
	return browseTitles(body), body
}

func TestLibraryFiltersInURL(t *testing.T) {
	a, ids := sweepFilterApp(t)
	author, narrator, series := ids[library.RoleAuthor], ids[library.RoleNarrator], ids["series"]
	cases := []struct {
		name, query string
		want        string
	}{
		{"author", "?author=" + author, "Fog Bell,Foghorn Days"},
		{"narrator", "?narrator=" + narrator, "Fog Bell,Quiet Mechanic"},
		{"series", "?series=" + series, "Fog Bell,Foghorn Days"},
		{"short", "?length=short", "Fog Bell,Salt and Ember"},
		{"medium", "?length=medium", "Foghorn Days"},
		{"long", "?length=long", "Quiet Mechanic"},
		{"title", "?title=FOG", "Fog Bell,Foghorn Days"},
		{"title literal percent", "?title=%25", ""},
		{"combined", "?author=" + author + "&narrator=" + narrator + "&length=short&title=fog", "Fog Bell"},
		{"unknown author", "?author=ps_nobody", ""},
		{"unknown length is ignored", "?length=huge", "Fog Bell,Foghorn Days,Quiet Mechanic,Salt and Ember"},
	}
	for _, c := range cases {
		titles, body := sweepList(t, a, c.query+"&sort=title")
		if got := strings.Join(titles, ","); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
		if c.name == "unknown length is ignored" {
			if strings.Contains(body, " books match") {
				t.Errorf("%s: an ignored filter shows a match line", c.name)
			}
			continue
		}
		n := len(titles)
		want := fmt.Sprintf(">%d books match<", n)
		if n == 1 {
			want = ">1 book matches<"
		}
		if !strings.Contains(body, want) || !strings.Contains(body, ">Clear filters<") {
			t.Errorf("%s: lacks %q or Clear filters", c.name, want)
		}
	}

	// No filter: no match line, no Clear filters in the results.
	_, plain := sweepList(t, a, "")
	if strings.Contains(plain, " books match") || strings.Contains(plain, ">Clear filters<") {
		t.Error("match line without a filter")
	}

	// Clear filters drops every filter and keeps the sort and the view.
	_, body := sweepList(t, a, "?sort=title&view=list&author="+author+"&length=short&title=fog&genre=Mystery")
	if !regexp.MustCompile(`<a class="ts-btn ts-btn--quiet" href="/libraries\?sort=title&view=list">Clear filters</a>`).MatchString(body) {
		t.Errorf("Clear filters does not keep the sort and the view")
	}

	// The chips name the choice; every option is an address that keeps the other filters.
	_, body = sweepList(t, a, "?author="+author+"&length=short&sort=title")
	flat := strings.Join(strings.Fields(body), " ")
	for _, want := range []string{"Author: Una Uno<", "Length: Under 5 hours<", "Narrator<svg", "Series<svg", ">Any author<", ">Any length<",
		`href="/libraries?length=short&sort=title"`,                        // Any author
		`href="/libraries?author=` + author + `&sort=title"`,               // Any length
		`href="/libraries?author=` + author + `&length=medium&sort=title"`, // another length
		`class="ts-chipmenu is-on"`, `<details`, `<summary class="ts-chip">`} {
		if !strings.Contains(flat, want) {
			t.Errorf("chips lack %q", want)
		}
	}
	// The narrator menu follows the author filter: Una Uno's two books have two narrators, 1 each.
	_, nb := sweepList(t, a, "?author="+author)
	if !strings.Contains(nb, `class="ts-chipmenu__name">Nel Nox<`) || !strings.Contains(nb, `class="ts-chipmenu__name">Ola Oro<`) {
		t.Error("narrator menu lacks its people")
	}
	// A menu lists its own choices ignoring its own filter, so the other author stays listed.
	if !strings.Contains(body, `class="ts-chipmenu__name">Bo Bee<`) {
		t.Error("the author menu hides the other authors")
	}
	// The three length edges.
	for _, want := range []string{"Under 5 hours", "5 to 15 hours", "Over 15 hours"} {
		if !strings.Contains(body, want) {
			t.Errorf("length menu lacks %q", want)
		}
	}
}

func TestLibraryFiltersCarryToPaging(t *testing.T) {
	a := browseOwnerApp(t)
	f := browseLibrary(t, a, "Audiobooks")
	browseAddMany(t, a, f, 60)
	rec := browseGet(a, "/libraries?title=book+0&sort=title", false, nil)
	body := rec.Body.String()
	more := browseMore(body)
	if more == "" || !strings.Contains(more, "title=book+0") {
		t.Errorf("the next-page address drops filters: %q", more)
	}
	frag := browseGet(a, more, true, nil)
	if frag.Code != http.StatusOK || strings.Contains(frag.Body.String(), "<html") {
		t.Errorf("fragment: %d", frag.Code)
	}
	// A filtered page of more than 48 books: every book of the second page matches the filters.
	for _, title := range browseTitles(frag.Body.String()) {
		if !strings.Contains(strings.ToLower(title), "book 0") {
			t.Errorf("fragment shows %q", title)
		}
	}
}

func TestLibraryResultsRegion(t *testing.T) {
	a, _ := sweepFilterApp(t)
	rec := a.do(http.MethodGet, "/libraries?title=fog", nil, func(r *http.Request) {
		r.Header.Set("HX-Request", "true")
		r.Header.Set("HX-Target", "library-results")
	}, a.session)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "<html") || !strings.Contains(body, `id="library-results"`) {
		t.Fatalf("htmx request: %d, want a full page with #library-results", rec.Code)
	}
	// Results hold the match line, the grid and the sentinel; the field and the chips do not move.
	i := strings.Index(body, `id="library-results"`)
	results := body[i:]
	if !strings.Contains(results, "books match") || !strings.Contains(results, `id="library-grid"`) {
		t.Error("the results region lacks the count or the grid")
	}
	before := body[:i]
	if !strings.Contains(before, `id="fb-title"`) || strings.Contains(results, `id="fb-title"`) {
		t.Error("the title field must stay outside the swapped region")
	}
	// The bar form and the title field swap the region and push the address.
	for _, want := range []string{`hx-get="/libraries"`, `hx-target="#library-results"`, `hx-select="#library-results"`, `hx-swap="outerHTML"`,
		`hx-push-url="true"`, `hx-trigger="input changed delay:300ms, search"`, `hx-include="closest form"`, `hx-trigger="change target:select"`,
		`name="title"`, `value="fog"`, `<label class="ts-sr" for="fb-title">Filter by title</label>`, `placeholder="Filter this library by title"`} {
		if !strings.Contains(body, want) {
			t.Errorf("bar lacks %q", want)
		}
	}
	// "Show books" is a no-JavaScript fallback in the bar and a real button in the sheet.
	if !regexp.MustCompile(`<noscript>\s*<button type="submit"[^>]*>Show books</button>\s*</noscript>`).MatchString(body) {
		t.Error("the bar's Show books is not inside <noscript>")
	}
	sheet := browseGet(a, "/libraries?sheet=filters", false, nil).Body.String()
	k := strings.Index(sheet, `id="sheet"`)
	for _, want := range []string{`>Show books<`, `name="length"`, `Any length`, `name="title"`, `Filter by title`} {
		if !strings.Contains(sheet[k:], want) {
			t.Errorf("sheet lacks %q", want)
		}
	}
	if regexp.MustCompile(`<noscript>[^<]*<button`).MatchString(sheet[k:]) {
		t.Error("the sheet's Show books is inside <noscript>")
	}
	// A bad hx- attribute set: nothing the CSP or htmx 4 rules ban.
	for _, banned := range []string{"hx-on", `style="`, "js:"} {
		if strings.Contains(body, banned) {
			t.Errorf("the page contains %q", banned)
		}
	}
}

func TestLibraryHeaderLine(t *testing.T) {
	a := browseOwnerApp(t)
	f := browseLibrary(t, a, "Audiobooks")
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "One", DurationMs: 2 * sweepHourMs})
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Two", DurationMs: 3 * sweepHourMs})
	if _, err := a.lib.AddFolder(context.Background(), f.LibraryID, "/browse-extra"); err != nil {
		t.Fatal(err)
	}
	owner := browseGet(a, "/libraries", false, nil).Body.String()
	if !strings.Contains(owner, ">2 books · 2 folders · 5 hours<") {
		t.Errorf("owner header line: %s", regexp.MustCompile(`ts-lib-head__count[^>]*>[^<]*`).FindString(owner))
	}
	member := a.as(t, users.RoleMember)
	mb := browseGet(a, "/libraries", false, member).Body.String()
	if !strings.Contains(mb, ">0 books · 0 hours<") || strings.Contains(regexp.MustCompile(`ts-lib-head__count[^>]*>[^<]*`).FindString(mb), "folder") {
		t.Errorf("member header line shows folders or books: %s", regexp.MustCompile(`ts-lib-head__count[^>]*>[^<]*`).FindString(mb))
	}
	// The line counts the library, not the filter: a filter shows its own match line instead.
	filtered := browseGet(a, "/libraries?title=one", false, nil).Body.String()
	if !strings.Contains(filtered, ">2 books · 2 folders · 5 hours<") || !strings.Contains(filtered, ">1 book matches<") {
		t.Error("a filter changed the header line or lacks its match line")
	}
	// One of each reads in the singular.
	if got := browseHeaderLine(library.Totals{Books: 1, DurationMs: sweepHourMs, Folders: 1}, true); got != "1 book · 1 folder · 1 hour" {
		t.Errorf("singular line %q", got)
	}
	if got := browseHeaderLine(library.Totals{Books: 912, DurationMs: 8240 * sweepHourMs, Folders: 3}, true); got != "912 books · 3 folders · 8,240 hours" {
		t.Errorf("board line %q", got)
	}
}

// sweepAddCover gives a book a cover row (no file is needed: only the page is checked).
func sweepAddCover(t *testing.T, a *testApp, b library.Book) {
	t.Helper()
	h := sha256.Sum256([]byte("cover-" + b.ID))
	sum := hex.EncodeToString(h[:])
	cover := library.Cover{BookID: b.ID, Source: library.CoverFolder, Mime: "image/png", Width: 96, Height: 96, SHA256: sum,
		Path: filepath.Join("covers", sum+".png"), Tint: "#1f4a52"}
	if err := a.lib.Update(context.Background(), func(tx *library.Tx) error {
		if err := tx.PutCover(&cover); err != nil {
			return err
		}
		return tx.SetBookCover(b.ID, cover.ID)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestSetupDoneWall(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	f := browseLibrary(t, a, "Audiobooks")

	// Books without covers: no wall.
	for i := 0; i < 3; i++ {
		librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: fmt.Sprintf("Plain %d", i), AddedAt: int64(i)})
	}
	if body := a.do(http.MethodGet, "/setup/done", nil, nil, owner).Body.String(); strings.Contains(body, "ts-wall") {
		t.Error("a wall without covers")
	}

	// 25 books with covers: the 18 newest, decorative, no links.
	var withCover []library.Book
	for i := 0; i < 25; i++ {
		b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: fmt.Sprintf("Covered %02d", i), AddedAt: int64(100 + i)})
		sweepAddCover(t, a, b)
		withCover = append(withCover, b)
	}
	body := a.do(http.MethodGet, "/setup/done", nil, nil, owner).Body.String()
	if got := strings.Count(body, `class="ts-wall__tile `); got != 18 {
		t.Fatalf("%d tiles, want 18", got)
	}
	i := strings.Index(body, `<div class="ts-wall"`)
	if i < 0 || !strings.HasPrefix(body[i:], `<div class="ts-wall" aria-hidden="true">`) {
		t.Fatal("the wall is not aria-hidden")
	}
	wall := body[i:]
	wall = wall[:strings.Index(wall, `<h1`)]
	if strings.Contains(wall, "<a ") || strings.Contains(wall, "href=") || strings.Contains(wall, "tabindex") {
		t.Error("the wall has links or focus stops")
	}
	if !strings.Contains(wall, `/covers/`+withCover[24].ID+`?`) || strings.Contains(wall, `/covers/`+withCover[0].ID+`?`) {
		t.Error("the wall is not the 18 newest")
	}
	// Above the headline, and the headline is still the only h1.
	if strings.Count(body, "<h1") != 1 || strings.Index(body, `<h1`) < i {
		t.Error("the wall must sit above the title")
	}
}

// TestVisualMatrixUsed: no visual test keeps its own theme-and-width loop; the matrix decides them.
func TestVisualMatrixUsed(t *testing.T) {
	files, err := filepath.Glob("visual*_test.go")
	if err != nil || len(files) == 0 {
		t.Fatalf("no visual tests found: %v", err)
	}
	own := regexp.MustCompile(`(?m)^\s*for\s+_?,?\s*\w*\s*:?=\s*range\s+\[\]\s*(?:string|int)\s*\{`)
	for _, f := range files {
		switch f {
		case "visual_matrix_test.go", "visual_motion_test.go", "visual_webkit_test.go", "visual_large_text_test.go", "visual_large_text_player_test.go", "visual_large_text_people_test.go", "visual_large_text_probe_test.go":
			continue // the matrix itself; motion tests; WebKit and the sink have their own fixed lists; the large-text tests (ltShots) have theirs
		}
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(raw)
		if strings.Contains(src, "visualMatrix(") == false && strings.Contains(src, "Theme") {
			t.Errorf("%s does not use visualMatrix", f)
		}
		for _, l := range strings.Split(src, "\n") {
			if own.MatchString(l) && (strings.Contains(l, `"dark"`) || strings.Contains(l, "768")) {
				t.Errorf("%s keeps its own matrix loop: %s", f, strings.TrimSpace(l))
			}
		}
		for _, bad := range []string{`[]string{"dark", "light"`, `[]int{390,`, `[]int{390 `, `themes := `, `widths := `} {
			if strings.Contains(src, bad) {
				t.Errorf("%s keeps its own theme or width list (%s)", f, bad)
			}
		}
	}
}
