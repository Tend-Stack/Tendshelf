package library_test

import (
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

// Tests for the P1b-9a library filters: title, length, the people and series facets, and totals.

const sweepHour = int64(3_600_000)

func sweepTitles(t *testing.T, r *library.Repo, q library.ListQuery) string {
	t.Helper()
	p, err := r.ListBooks(ctx, allScope, q)
	if err != nil {
		t.Fatal(err)
	}
	var ts []string
	for _, c := range p.Books {
		ts = append(ts, c.Title)
	}
	if p.Total != len(p.Books) {
		t.Errorf("Total %d != %d books for %+v", p.Total, len(p.Books), q)
	}
	return strings.Join(sweepSorted(ts), ",")
}

func sweepSorted(in []string) []string {
	out := append([]string(nil), in...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

func TestListBooksTitleFilter(t *testing.T) {
	r, _ := newRepo(t)
	_, f := librarytest.NewLibrary(t, r, "Main", "/audio")
	for _, title := range []string{"The Fog Bell", "fog of war", "100% Pure", "Snake_case", "Snakexcase", "Back\\slash", "Ünder"} {
		librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: title})
	}
	cases := []struct {
		title, want string
	}{
		{"fog", "The Fog Bell,fog of war"},
		{"FOG B", "The Fog Bell"},
		{"  fog  ", "The Fog Bell,fog of war"},
		{"%", "100% Pure"},
		{"_", "Snake_case"},
		{"e_c", "Snake_case"},
		{`\`, `Back\slash`},
		{"100%", "100% Pure"},
		{"zzz", ""},
		{"", "100% Pure,Back\\slash,Snake_case,Snakexcase,The Fog Bell,fog of war,Ünder"},
	}
	for _, c := range cases {
		if got := sweepTitles(t, r, library.ListQuery{Title: c.title}); got != c.want {
			t.Errorf("Title %q = %q, want %q", c.title, got, c.want)
		}
	}
	// A title longer than 100 runes is cut, not refused.
	long := strings.Repeat("a", 150)
	if _, err := r.ListBooks(ctx, allScope, library.ListQuery{Title: long}); err != nil {
		t.Errorf("long title: %v", err)
	}
}

func TestListBooksLength(t *testing.T) {
	r, _ := newRepo(t)
	_, f := librarytest.NewLibrary(t, r, "Main", "/audio")
	for _, s := range []struct {
		title string
		ms    int64
	}{{"a-just-under-5h", 5*sweepHour - 1}, {"b-exactly-5h", 5 * sweepHour}, {"c-just-under-15h", 15*sweepHour - 1},
		{"d-exactly-15h", 15 * sweepHour}, {"e-30h", 30 * sweepHour}} {
		librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: s.title, DurationMs: s.ms})
	}
	cases := []struct {
		name     string
		min, max int64
		want     string
	}{
		{"short", 0, 5 * sweepHour, "a-just-under-5h"},
		{"medium", 5 * sweepHour, 15 * sweepHour, "b-exactly-5h,c-just-under-15h"},
		{"long", 15 * sweepHour, 0, "d-exactly-15h,e-30h"},
		{"open", 0, 0, "a-just-under-5h,b-exactly-5h,c-just-under-15h,d-exactly-15h,e-30h"},
	}
	for _, c := range cases {
		if got := sweepTitles(t, r, library.ListQuery{MinDurationMs: c.min, MaxDurationMs: c.max}); got != c.want {
			t.Errorf("%s = %q, want %q", c.name, got, c.want)
		}
	}
}

func sweepFacetSeed(t *testing.T) (*library.Repo, map[string]string) {
	t.Helper()
	r, _ := newRepo(t)
	_, f := librarytest.NewLibrary(t, r, "Main", "/audio")
	add := func(s librarytest.BookSpec) library.Book { return librarytest.AddBook(t, r, f, s) }
	a1 := add(librarytest.BookSpec{Title: "A1", Authors: []string{"Una Uno"}, Narrators: []string{"Nel Nox"}, Series: "Saga", Sequence: "1", Genres: []string{"Fantasy"}})
	add(librarytest.BookSpec{Title: "A2", Authors: []string{"Una Uno"}, Narrators: []string{"Nel Nox"}, Series: "Saga", Sequence: "2", Genres: []string{"Fantasy"}})
	add(librarytest.BookSpec{Title: "B1", Authors: []string{"Bo Bee"}, Narrators: []string{"Nel Nox", "Ola Oro"}, Series: "Other", Genres: []string{"Travel"}})
	add(librarytest.BookSpec{Title: "C1", Authors: []string{"Cy Cox"}})
	add(librarytest.BookSpec{Title: "Hidden", Authors: []string{"Hal Hid"}, Series: "Ghost", Refused: "drm"})
	ids := map[string]string{}
	people, _ := r.BookPeople(ctx, a1.ID)
	for _, p := range people {
		ids[p.Role] = p.PersonID
	}
	series, _ := r.BookSeries(ctx, a1.ID)
	ids["series"] = series[0].SeriesID
	return r, ids
}

func sweepNames(fs []library.NamedFacet) string {
	var out []string
	for _, f := range fs {
		out = append(out, f.Name+":"+string(rune('0'+f.Count)))
	}
	return strings.Join(out, ",")
}

func TestPeopleFacetIgnoresOwnFilter(t *testing.T) {
	r, ids := sweepFacetSeed(t)
	// No filter: count desc then name; the refused book's author is not listed.
	got, err := r.PeopleFacet(ctx, allScope, library.ListQuery{}, library.RoleAuthor, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Una Uno:2,Bo Bee:1,Cy Cox:1"; sweepNames(got) != want {
		t.Errorf("authors = %q, want %q", sweepNames(got), want)
	}
	// Choosing an author does not narrow the author facet, but the narrator facet follows it.
	q := library.ListQuery{AuthorID: ids[library.RoleAuthor]}
	got, _ = r.PeopleFacet(ctx, allScope, q, library.RoleAuthor, 0)
	if want := "Una Uno:2,Bo Bee:1,Cy Cox:1"; sweepNames(got) != want {
		t.Errorf("authors with author chosen = %q, want %q", sweepNames(got), want)
	}
	got, _ = r.PeopleFacet(ctx, allScope, q, library.RoleNarrator, 0)
	if want := "Nel Nox:2"; sweepNames(got) != want {
		t.Errorf("narrators with author chosen = %q, want %q", sweepNames(got), want)
	}
	// Another filter (genre) applies to the facet.
	got, _ = r.PeopleFacet(ctx, allScope, library.ListQuery{Genres: []string{"travel"}}, library.RoleAuthor, 0)
	if want := "Bo Bee:1"; sweepNames(got) != want {
		t.Errorf("authors in travel = %q, want %q", sweepNames(got), want)
	}
	// The limit applies; a chosen person outside it is still named.
	got, _ = r.PeopleFacet(ctx, allScope, library.ListQuery{AuthorID: ids[library.RoleAuthor], Genres: []string{"travel"}}, library.RoleAuthor, 1)
	if want := "Bo Bee:1,Una Uno:0"; sweepNames(got) != want {
		t.Errorf("limit 1 with chosen = %q, want %q", sweepNames(got), want)
	}
	if _, err := r.PeopleFacet(ctx, allScope, library.ListQuery{}, "editor", 0); err == nil {
		t.Error("unknown role accepted")
	}
	if got, err := r.PeopleFacet(ctx, noneScope, library.ListQuery{AuthorID: ids[library.RoleAuthor]}, library.RoleAuthor, 0); err != nil || len(got) != 0 {
		t.Errorf("none scope = %v, %v", got, err)
	}
}

func TestSeriesFacet(t *testing.T) {
	r, ids := sweepFacetSeed(t)
	got, err := r.SeriesFacet(ctx, allScope, library.ListQuery{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Saga:2,Other:1"; sweepNames(got) != want {
		t.Errorf("series = %q, want %q", sweepNames(got), want)
	}
	// Its own filter is ignored; the genre filter is not.
	got, _ = r.SeriesFacet(ctx, allScope, library.ListQuery{SeriesID: ids["series"], Genres: []string{"travel"}}, 0)
	if want := "Other:1,Saga:0"; sweepNames(got) != want {
		t.Errorf("series with own filter = %q, want %q", sweepNames(got), want)
	}
	if got, err := r.SeriesFacet(ctx, noneScope, library.ListQuery{SeriesID: ids["series"]}, 0); err != nil || len(got) != 0 {
		t.Errorf("none scope = %v, %v", got, err)
	}
}

func TestTotalsScoped(t *testing.T) {
	r, _ := newRepo(t)
	_, f1 := librarytest.NewLibrary(t, r, "Main", "/audio")
	l2, f2 := librarytest.NewLibrary(t, r, "Other", "/other")
	if _, err := r.AddFolder(ctx, l2.ID, "/other2"); err != nil {
		t.Fatal(err)
	}
	librarytest.AddBook(t, r, f1, librarytest.BookSpec{Title: "One", DurationMs: 2 * sweepHour})
	librarytest.AddBook(t, r, f2, librarytest.BookSpec{Title: "Two", DurationMs: 3 * sweepHour})
	librarytest.AddBook(t, r, f2, librarytest.BookSpec{Title: "Gone", DurationMs: 9 * sweepHour, MissingSince: 5})
	librarytest.AddBook(t, r, f2, librarytest.BookSpec{Title: "Refused", DurationMs: 9 * sweepHour, Refused: "drm"})

	got, err := r.Totals(ctx, allScope, "")
	if err != nil {
		t.Fatal(err)
	}
	if want := (library.Totals{Books: 2, DurationMs: 5 * sweepHour, Folders: 3}); got != want {
		t.Errorf("all = %+v, want %+v", got, want)
	}
	got, _ = r.Totals(ctx, allScope, l2.ID)
	if want := (library.Totals{Books: 1, DurationMs: 3 * sweepHour, Folders: 2}); got != want {
		t.Errorf("one library = %+v, want %+v", got, want)
	}
	got, err = r.Totals(ctx, noneScope, "")
	if err != nil || got != (library.Totals{}) {
		t.Errorf("none scope = %+v, %v", got, err)
	}
}
