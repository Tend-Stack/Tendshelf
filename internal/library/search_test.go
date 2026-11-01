package library_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

func searchIDs(t *testing.T, r *library.Repo, q string) []string {
	t.Helper()
	res, err := r.Search(ctx, allScope, q, 0)
	if err != nil {
		t.Fatalf("Search(%q): %v", q, err)
	}
	return res.IDs
}

func wantIDs(t *testing.T, r *library.Repo, q string, want ...string) {
	t.Helper()
	got := searchIDs(t, r, q)
	if len(got) != len(want) {
		t.Fatalf("Search(%q) = %v, want %v", q, got, want)
	}
	have := map[string]bool{}
	for _, id := range got {
		have[id] = true
	}
	for _, id := range want {
		if !have[id] {
			t.Fatalf("Search(%q) = %v, want %v", q, got, want)
		}
	}
}

func searchEnv(t *testing.T) (*library.Repo, *db.Store, library.Folder) {
	t.Helper()
	r, st := newRepo(t)
	_, f := librarytest.NewLibrary(t, r, "Books", "/audio")
	return r, st, f
}

func TestSearchTriggersInsertUpdate(t *testing.T) {
	r, _, f := searchEnv(t)
	b := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "The Lighthouse Keeper"})
	wantIDs(t, r, "lighthouse", b.ID)
	b.Title = "A Walk In Spring"
	if err := r.Update(ctx, func(tx *library.Tx) error { return tx.UpdateBook(b) }); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, r, "lighthouse")
	wantIDs(t, r, "spring", b.ID)
	b.Description = "Sailors and storms"
	if err := r.Update(ctx, func(tx *library.Tx) error { return tx.UpdateBook(b) }); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, r, "storms", b.ID)
}

func TestSearchTriggersPeople(t *testing.T) {
	r, st, f := searchEnv(t)
	b := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Plain"})
	wantIDs(t, r, "ana")
	err := r.Update(ctx, func(tx *library.Tx) error {
		a, err := tx.EnsurePerson("Ana Maria", "")
		if err != nil {
			return err
		}
		n, err := tx.EnsurePerson("Bruno Voice", "")
		if err != nil {
			return err
		}
		if err := tx.SetBookPeople(b.ID, library.RoleAuthor, []string{a}); err != nil {
			return err
		}
		return tx.SetBookPeople(b.ID, library.RoleNarrator, []string{n})
	})
	if err != nil {
		t.Fatal(err)
	}
	wantIDs(t, r, "maria", b.ID)
	wantIDs(t, r, "voice", b.ID)
	if _, err := st.Writer().Exec(`UPDATE people SET name = 'Clara Oliveira' WHERE name = 'Ana Maria'`); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, r, "maria")
	wantIDs(t, r, "oliveira", b.ID)
	// The narrator column is searched too, and removing the link drops the term.
	if err := r.Update(ctx, func(tx *library.Tx) error { return tx.SetBookPeople(b.ID, library.RoleNarrator, nil) }); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, r, "voice")
	wantIDs(t, r, "clara", b.ID)
}

func TestSearchTriggersSeries(t *testing.T) {
	r, st, f := searchEnv(t)
	b := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Plain", Series: "Saga Of Ash", Sequence: "1"})
	wantIDs(t, r, "ash", b.ID)
	if _, err := st.Writer().Exec(`UPDATE series SET name = 'Saga Of Pine'`); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, r, "ash")
	wantIDs(t, r, "pine", b.ID)
	if err := r.Update(ctx, func(tx *library.Tx) error { return tx.SetBookSeries(b.ID, nil) }); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, r, "pine")
}

func docsizeCount(t *testing.T, st *db.Store) int {
	t.Helper()
	var n int
	if err := st.Reader().QueryRow(`SELECT count(*) FROM books_fts_docsize`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func count(t *testing.T, st *db.Store, table string) int {
	t.Helper()
	var n int
	if err := st.Reader().QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSearchDeleteBook(t *testing.T) {
	r, st, f := searchEnv(t)
	b := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Gone Soon", Authors: []string{"Ana"}, Series: "Saga"})
	keep := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Stays"})
	if _, err := st.Writer().Exec(`DELETE FROM books WHERE id = ?`, b.ID); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, r, "gone")
	wantIDs(t, r, "stays", keep.ID)
	if n := count(t, st, "books_fts_ids"); n != 1 {
		t.Fatalf("ids = %d", n)
	}
	if n := docsizeCount(t, st); n != 1 {
		t.Fatalf("fts rows = %d", n)
	}
}

func TestSearchDeleteFolderCascade(t *testing.T) {
	r, st, f := searchEnv(t)
	other, err := r.AddFolder(ctx, f.LibraryID, "/other")
	if err != nil {
		t.Fatal(err)
	}
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Cascade One", Authors: []string{"Ana"}})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Cascade Two", Series: "Saga"})
	keep := librarytest.AddBook(t, r, other, librarytest.BookSpec{Title: "Cascade Three"})
	if _, err := st.Writer().Exec(`DELETE FROM library_folders WHERE id = ?`, f.ID); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, r, "cascade", keep.ID)
	if n := count(t, st, "books_fts_ids"); n != 1 {
		t.Fatalf("ids = %d", n)
	}
	if n := docsizeCount(t, st); n != 1 {
		t.Fatalf("fts rows = %d", n)
	}
}

func TestSearchIndexConsistent(t *testing.T) {
	r, st, f := searchEnv(t)
	var books []library.Book
	for i := 0; i < 6; i++ {
		books = append(books, librarytest.AddBook(t, r, f, librarytest.BookSpec{
			Title: fmt.Sprintf("Book %d", i), Authors: []string{"Ana", "Zed"}, Narrators: []string{"Voice"}, Series: "Saga"}))
	}
	b := books[0]
	b.Title = "Renamed"
	if err := r.Update(ctx, func(tx *library.Tx) error { return tx.UpdateBook(b) }); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`DELETE FROM books WHERE id = '` + books[1].ID + `'`,
		`UPDATE people SET name = 'Zeta' WHERE name = 'Zed'`,
		`DELETE FROM book_people WHERE book_id = '` + books[2].ID + `'`,
		`DELETE FROM series`,
	} {
		if _, err := st.Writer().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	nb, ni, nd := count(t, st, "books"), count(t, st, "books_fts_ids"), docsizeCount(t, st)
	if nb != 5 || ni != nb || nd != nb {
		t.Fatalf("books %d, ids %d, fts %d", nb, ni, nd)
	}
	wantIDs(t, r, "saga")
}

func TestSearchTermsSanitised(t *testing.T) {
	r, _, f := searchEnv(t)
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Near Title Test", Authors: []string{"Ana"}})
	twenty := strings.TrimSpace(strings.Repeat("word ", 20))
	for _, c := range []struct {
		in   string
		want []string
	}{
		{`"`, nil},
		{`*`, nil},
		{`NEAR(a b)`, []string{"near", "a", "b"}},
		{`title:x`, []string{"title", "x"}},
		{`-x`, []string{"x"}},
		{`a OR b`, []string{"a", "or", "b"}},
		{`^x`, []string{"x"}},
		{`''`, nil},
		{`   `, nil},
		{"\U0001F600\U0001F4DA", nil},
		{twenty, []string{"word"}},
		{"Tomás  O'Brien", []string{"tomás", "o", "brien"}},
		{"one two three four five six seven eight nine ten", []string{"one", "two", "three", "four", "five", "six", "seven", "eight"}},
		{strings.Repeat("x", 100), []string{strings.Repeat("x", 64)}},
	} {
		if got := library.SearchTerms(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("SearchTerms(%q) = %q, want %q", c.in, got, c.want)
		}
		if _, err := r.Search(ctx, allScope, c.in, 0); err != nil {
			t.Errorf("Search(%q): %v", c.in, err)
		}
		if _, err := r.Suggest(ctx, allScope, c.in, 0); err != nil {
			t.Errorf("Suggest(%q): %v", c.in, err)
		}
	}
	if got := twentyWords(); len(library.SearchTerms(got)) != library.SearchMaxTerms {
		t.Errorf("20 distinct words give %d terms", len(library.SearchTerms(got)))
	}
	// No terms: an empty result, not an error.
	if res, err := r.Search(ctx, allScope, `"*`, 0); err != nil || len(res.IDs) != 0 || res.Total != 0 {
		t.Errorf("no-term search = %+v, %v", res, err)
	}
	// Operators in the input are plain words and never match as syntax.
	wantIDs(t, r, `title:near`, bookIDByTitle(t, r, "Near Title Test"))
}

func twentyWords() string {
	var w []string
	for i := 0; i < 20; i++ {
		w = append(w, fmt.Sprintf("w%d", i))
	}
	return strings.Join(w, " ")
}

// bookIDByTitle returns the id of the book titled title.
func bookIDByTitle(t *testing.T, r *library.Repo, title string) string {
	t.Helper()
	res, err := r.Search(ctx, allScope, title, 5)
	if err != nil || len(res.IDs) == 0 {
		t.Fatalf("find %q: %v %v", title, res, err)
	}
	return res.IDs[0]
}

func TestSearchDiacritics(t *testing.T) {
	r, _, f := searchEnv(t)
	b := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Cartas", Authors: []string{"Tomás Rivera"}})
	wantIDs(t, r, "tomas", b.ID)
	wantIDs(t, r, "Tomás", b.ID)
}

func TestSearchRanking(t *testing.T) {
	r, _, f := searchEnv(t)
	desc := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Other", Description: "a story about a harbour"})
	title := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "The Harbour"})
	got := searchIDs(t, r, "harbour")
	if !reflect.DeepEqual(got, []string{title.ID, desc.ID}) {
		t.Fatalf("order = %v, want title hit %s first", got, title.ID)
	}
}

func TestSearchScope(t *testing.T) {
	r, _, f := searchEnv(t)
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Lighthouse"})
	res, err := r.Search(ctx, noneScope, "lighthouse", 0)
	if err != nil || len(res.IDs) != 0 || res.Total != 0 || res.Suggestion != "" {
		t.Fatalf("0=1 scope = %+v, %v", res, err)
	}
	res, err = r.Search(ctx, noneScope, "lighthuose", 0)
	if err != nil || res.Suggestion != "" {
		t.Fatalf("0=1 scope suggestion = %+v, %v", res, err)
	}
	if res, err = r.Search(ctx, library.Scope{}, "lighthouse", 0); err != nil || len(res.IDs) != 0 {
		t.Fatalf("zero scope = %+v, %v", res, err)
	}
	sug, err := r.Suggest(ctx, noneScope, "light", 0)
	if err != nil || len(sug) != 0 {
		t.Fatalf("0=1 suggest = %v, %v", sug, err)
	}
	// Scope arguments are bound in order: a filter on library id.
	lib2, f2 := librarytest.NewLibrary(t, r, "Second", "/second")
	b2 := librarytest.AddBook(t, r, f2, librarytest.BookSpec{Title: "Lighthouse Two"})
	only := library.Scope{Books: share.Filter{SQL: "1=1"}, Libraries: share.Filter{SQL: "l.id = ?", Args: []any{lib2.ID}}}
	res, err = r.Search(ctx, only, "lighthouse", 0)
	if err != nil || !reflect.DeepEqual(res.IDs, []string{b2.ID}) || res.Total != 1 {
		t.Fatalf("library scope = %+v, %v", res, err)
	}
}

func TestSearchHidesRefusedAndMissing(t *testing.T) {
	r, _, f := searchEnv(t)
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Harbour Refused", Refused: "drm"})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Harbour Missing", MissingSince: 5})
	ok := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Harbour Visible"})
	res, err := r.Search(ctx, allScope, "harbour", 0)
	if err != nil || !reflect.DeepEqual(res.IDs, []string{ok.ID}) || res.Total != 1 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	sug, err := r.Suggest(ctx, allScope, "harbour", 0)
	if err != nil || len(sug) != 1 || sug[0].ID != ok.ID {
		t.Fatalf("suggest = %v, %v", sug, err)
	}
}

func TestSearchLimitAndTotal(t *testing.T) {
	r, _, f := searchEnv(t)
	for i := 0; i < 7; i++ {
		librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: fmt.Sprintf("Tide %d", i)})
	}
	res, err := r.Search(ctx, allScope, "tide", 3)
	if err != nil || len(res.IDs) != 3 || res.Total != 7 {
		t.Fatalf("result = %+v, %v", res, err)
	}
	res, _ = r.Search(ctx, allScope, "tide", -4)
	if len(res.IDs) != 1 {
		t.Fatalf("negative limit gave %d", len(res.IDs))
	}
}

func TestDidYouMean(t *testing.T) {
	r, _, f := searchEnv(t)
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "The Keeper Of The Lighthouse"})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Lighthouse Stories"})
	hidden := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Zeppelin Tales", MissingSince: 5})
	_ = hidden

	for _, c := range []struct{ q, want string }{
		{"lighthuose", "lighthouse"},
		{"keeper lighthuose", "keeper lighthouse"},
		{"lighthouse", ""},        // results exist
		{"keeper lighthouse", ""}, // results exist
		{"zeppelin", ""},          // only a hidden book has it
		{"zepelin", ""},           // near a word only a hidden book has
		{"xy", ""},                // too short to correct
		{"qqqqqqq", ""},           // nothing close
	} {
		res, err := r.Search(ctx, allScope, c.q, 0)
		if err != nil {
			t.Fatalf("Search(%q): %v", c.q, err)
		}
		if res.Suggestion != c.want {
			t.Errorf("Search(%q).Suggestion = %q, want %q (ids %v)", c.q, res.Suggestion, c.want, res.IDs)
		}
		if c.want != "" {
			again, _ := r.Search(ctx, allScope, res.Suggestion, 0)
			if len(again.IDs) == 0 {
				t.Errorf("suggestion %q finds nothing", res.Suggestion)
			}
		}
	}
	// A scope that hides every library never learns the word.
	res, err := r.Search(ctx, noneScope, "lighthuose", 0)
	if err != nil || res.Suggestion != "" {
		t.Fatalf("hidden scope = %+v, %v", res, err)
	}
}

func TestDidYouMeanPrefersCommonWord(t *testing.T) {
	r, _, f := searchEnv(t)
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Cargo Tales"})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Cargo Songs"})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Carbo Notes"})
	res, err := r.Search(ctx, allScope, "carga", 0)
	if err != nil || res.Suggestion != "cargo" {
		t.Fatalf("suggestion = %+v, %v", res, err)
	}
}

func TestSuggestKinds(t *testing.T) {
	r, _, f := searchEnv(t)
	b := librarytest.AddBook(t, r, f, librarytest.BookSpec{
		Title: "Sandy Tales", Authors: []string{"Sandra Quill", "Pat"}, Narrators: []string{"Sandro Reed", "Sandra Quill"},
		Series: "Sands Of Time"})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Unrelated", Authors: []string{"Hidden Sander"}, Refused: "drm"})
	got, err := r.Suggest(ctx, allScope, "sand", 0)
	if err != nil {
		t.Fatal(err)
	}
	var kinds, labels []string
	for _, s := range got {
		kinds = append(kinds, s.Kind)
		labels = append(labels, s.Label)
	}
	wantKinds := []string{"book", "author", "narrator", "narrator", "series"}
	wantLabels := []string{"Sandy Tales", "Sandra Quill", "Sandra Quill", "Sandro Reed", "Sands Of Time"}
	if !reflect.DeepEqual(kinds, wantKinds) || !reflect.DeepEqual(labels, wantLabels) {
		t.Fatalf("suggest = %v / %v", kinds, labels)
	}
	if got[0].ID != b.ID {
		t.Fatalf("book id = %s", got[0].ID)
	}
	// A word inside the name matches, not only the start of it.
	got, _ = r.Suggest(ctx, allScope, "quill", 0)
	if len(got) != 2 || got[0].Kind != "author" || got[1].Kind != "narrator" {
		t.Fatalf("word-start suggest = %v", got)
	}
	// Too short, and the limit holds.
	if got, _ = r.Suggest(ctx, allScope, "s", 0); len(got) != 0 {
		t.Fatalf("one rune gave %v", got)
	}
	if got, _ = r.Suggest(ctx, allScope, "sand", 2); len(got) != 2 {
		t.Fatalf("limit 2 gave %d", len(got))
	}
}

func TestSuggestLikeEscaped(t *testing.T) {
	r, _, f := searchEnv(t)
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Plain", Authors: []string{"100% Real", "Under_score", "Plain Name"}})
	for _, c := range []struct {
		q    string
		want int
	}{
		{"%", 0}, // no terms
		{"_", 0},
		{"100", 1},
		{"under_", 1}, // '_' separates terms, so this is the prefix "under"
		{"re%", 1},    // '%' is a separator too
	} {
		got, err := r.Suggest(ctx, allScope, c.q+"  ", 0)
		if err != nil {
			t.Fatalf("Suggest(%q): %v", c.q, err)
		}
		n := 0
		for _, s := range got {
			if s.Kind == "author" {
				n++
			}
		}
		if n != c.want {
			t.Errorf("Suggest(%q) authors = %d (%v), want %d", c.q, n, got, c.want)
		}
	}
}

func TestRebuildSearch(t *testing.T) {
	r, st, f := searchEnv(t)
	a := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "First Light", Authors: []string{"Ana"}})
	b := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Second Light", Series: "Saga"})
	// Break the index behind the triggers' back, then repair it.
	if _, err := st.Writer().Exec(`DELETE FROM books_fts`); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, r, "light")
	if err := r.RebuildSearch(ctx); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, r, "light", a.ID, b.ID)
	wantIDs(t, r, "saga", b.ID)
	wantIDs(t, r, "ana", a.ID)
	if nb, ni, nd := count(t, st, "books"), count(t, st, "books_fts_ids"), docsizeCount(t, st); nb != 2 || ni != 2 || nd != 2 {
		t.Fatalf("books %d, ids %d, fts %d", nb, ni, nd)
	}
	// The triggers still work on the rebuilt rows.
	a.Title = "Renamed"
	if err := r.Update(ctx, func(tx *library.Tx) error { return tx.UpdateBook(a) }); err != nil {
		t.Fatal(err)
	}
	wantIDs(t, r, "first")
	wantIDs(t, r, "renamed", a.ID)
}
