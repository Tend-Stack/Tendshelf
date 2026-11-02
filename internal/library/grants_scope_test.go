package library_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// TestGrantScopeHidesEverything runs every scoped read with the real member scope, the first
// production filter with arguments, and checks that nothing the grant does not cover leaks: not an
// id, a title, a name, a count or a duration.
func TestGrantScopeHidesEverything(t *testing.T) {
	r, st := newRepo(t)
	lv, fv := librarytest.NewLibrary(t, r, "Open Library", "/open")
	lh, fh := librarytest.NewLibrary(t, r, "Closed Library", "/closed")
	visible := librarytest.AddBook(t, r, fv, librarytest.BookSpec{Title: "Visible Tale", Authors: []string{"Vera Ink"},
		Narrators: []string{"Nora Voice"}, Series: "Open Saga", Sequence: "1", Genres: []string{"Fantasy"}, DurationMs: 1000})
	// Same library as the granted book: a book grant must not open its neighbours.
	sibling := librarytest.AddBook(t, r, fv, librarytest.BookSpec{Title: "Hiddenword Quill", Authors: []string{"Hiddenauthor Penn"},
		Narrators: []string{"Hiddennarrator Echo"}, Series: "Hiddenseries Saga", Sequence: "1", Genres: []string{"Hiddengenre"}, DurationMs: 5000})
	elsewhere := librarytest.AddBook(t, r, fh, librarytest.BookSpec{Title: "Hiddenword Lantern", Authors: []string{"Hiddenauthor Penn"},
		Genres: []string{"Hiddengenre"}, DurationMs: 7000})
	refused := librarytest.AddBook(t, r, fh, librarytest.BookSpec{Title: "Hiddenword Locked", Refused: "drm"})
	missing := librarytest.AddBook(t, r, fh, librarytest.BookSpec{Title: "Hiddenword Gone", MissingSince: 5})
	if err := r.RebuildSearch(ctx); err != nil {
		t.Fatal(err)
	}

	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := st.Writer().Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	exec(`INSERT INTO users(id, username, role, created_at) VALUES ('us_own', 'own', 'owner', 1), ('us_mem', 'mem', 'member', 1)`)
	exec(`INSERT INTO grants(id, user_id, scope, book_id, created_at, updated_at) VALUES ('gr_1', 'us_mem', 'book', ?, 1, 1)`, visible.ID)
	for _, b := range []library.Book{visible, sibling, elsewhere} {
		exec(`INSERT INTO progress(user_id, book_id, position_ms, duration_ms, client_updated_at, server_updated_at, device_id)
			VALUES ('us_mem', ?, 100, 1000, 1, 1, 'dev_test_1')`, b.ID)
	}
	exec(`INSERT INTO shelves(id, owner_id, name, created_at, updated_at) VALUES ('sh_1', 'us_mem', 'Mine', 1, 1)`)
	for i, b := range []library.Book{visible, sibling, elsewhere} {
		exec(`INSERT INTO shelf_items(shelf_id, book_id, idx, added_at) VALUES ('sh_1', ?, ?, 1)`, b.ID, i)
	}

	checker := share.New(st)
	member := users.User{ID: "us_mem", Role: users.RoleMember}
	owner := users.User{ID: "us_own", Role: users.RoleOwner}
	scopeOf := func(u users.User) library.Scope {
		return library.Scope{Books: checker.BookFilter(u, "b"), Libraries: checker.LibraryFilter(u, "l")}
	}
	sc, full := scopeOf(member), scopeOf(owner)

	// What must never appear for the member: ids, titles, names.
	hidden := []string{sibling.ID, elsewhere.ID, refused.ID, missing.ID, lh.ID, "Hiddenword", "Hiddenauthor", "Hiddennarrator",
		"Hiddenseries", "Hiddengenre", "Closed Library", "/closed", "Hiddenword Quill"}
	sawHidden := false // the same calls must show them to the owner, or the test proves nothing
	check := func(name string, got any, err error) {
		t.Helper()
		if err != nil {
			t.Errorf("%s: %v", name, err)
			return
		}
		text := fmt.Sprintf("%+v", got)
		for _, h := range hidden {
			if strings.Contains(text, h) {
				t.Errorf("%s leaked %q: %s", name, h, text)
			}
		}
	}
	checkAs := func(name string, call func(library.Scope) (any, error)) {
		t.Helper()
		got, err := call(sc)
		check(name, got, err)
		if all, err := call(full); err == nil {
			text := fmt.Sprintf("%+v", all)
			for _, h := range hidden {
				if strings.Contains(text, h) {
					sawHidden = true
				}
			}
		}
	}
	wrap := func(v any, err error) (any, error) { return v, err }

	checkAs("FolderSummaries", func(s library.Scope) (any, error) { return wrap(r.FolderSummaries(ctx, s)) })
	checkAs("Problems", func(s library.Scope) (any, error) { return wrap(r.Problems(ctx, s, 10)) })
	checkAs("Overview", func(s library.Scope) (any, error) { return wrap(r.Overview(ctx, s)) })
	checkAs("ScanProgress", func(s library.Scope) (any, error) { return wrap(r.ScanProgress(ctx, s, lh.ID)) })
	checkAs("ContinueListening", func(s library.Scope) (any, error) { return wrap(r.ContinueListening(ctx, s, "us_mem", 10)) })
	checkAs("SeriesInProgress", func(s library.Scope) (any, error) { return wrap(r.SeriesInProgress(ctx, s, "us_mem", 10)) })
	checkAs("BecauseYouListened", func(s library.Scope) (any, error) {
		b, _, err := r.BecauseYouListened(ctx, s, "us_mem", 10)
		return b, err
	})
	checkAs("Shelves", func(s library.Scope) (any, error) { return wrap(r.Shelves(ctx, s, "us_mem")) })
	checkAs("ShelfCounts", func(s library.Scope) (any, error) { return wrap(r.ShelfCounts(ctx, s, "us_mem")) })
	checkAs("ShelfBooks", func(s library.Scope) (any, error) { return wrap(r.ShelfBooks(ctx, s, "us_mem", "sh_1", "", 20)) })
	checkAs("ListBooks", func(s library.Scope) (any, error) { return wrap(r.ListBooks(ctx, s, library.ListQuery{})) })
	checkAs("ListBooks in the closed library", func(s library.Scope) (any, error) {
		return wrap(r.ListBooks(ctx, s, library.ListQuery{LibraryID: lh.ID}))
	})
	checkAs("CardsByID", func(s library.Scope) (any, error) {
		return wrap(r.CardsByID(ctx, s, []string{visible.ID, sibling.ID, elsewhere.ID}))
	})
	checkAs("Genres", func(s library.Scope) (any, error) { return wrap(r.Genres(ctx, s, "")) })
	checkAs("VisibleLibraries", func(s library.Scope) (any, error) { return wrap(r.VisibleLibraries(ctx, s)) })
	checkAs("PeopleFacet", func(s library.Scope) (any, error) {
		return wrap(r.PeopleFacet(ctx, s, library.ListQuery{}, library.RoleAuthor, 0))
	})
	checkAs("PeopleFacet narrators", func(s library.Scope) (any, error) {
		return wrap(r.PeopleFacet(ctx, s, library.ListQuery{}, library.RoleNarrator, 0))
	})
	checkAs("SeriesFacet", func(s library.Scope) (any, error) { return wrap(r.SeriesFacet(ctx, s, library.ListQuery{}, 0)) })
	checkAs("Totals", func(s library.Scope) (any, error) { return wrap(r.Totals(ctx, s, "")) })
	checkAs("Search", func(s library.Scope) (any, error) { return wrap(r.Search(ctx, s, "Hiddenword", 20)) })
	checkAs("Search for a typo", func(s library.Scope) (any, error) { return wrap(r.Search(ctx, s, "Hiddenwrd", 20)) })
	checkAs("Suggest", func(s library.Scope) (any, error) { return wrap(r.Suggest(ctx, s, "Hidden", 20)) })
	if !sawHidden {
		t.Fatal("the owner never saw a hidden word: the test would pass on an empty library")
	}

	// Details of hidden things: not found, or nothing of them in the answer.
	detail, err := r.BookDetail(ctx, full, visible.ID)
	if err != nil {
		t.Fatal(err)
	}
	fullSibling, err := r.BookDetail(ctx, full, sibling.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.BookDetail(ctx, sc, sibling.ID); err == nil {
		t.Error("BookDetail opened a book in the granted library that the grant does not cover")
	}
	if _, err := r.BookDetail(ctx, sc, elsewhere.ID); err == nil {
		t.Error("BookDetail opened a book in a hidden library")
	}
	if d, err := r.BookDetail(ctx, sc, visible.ID); err != nil || d.Card.Title != "Visible Tale" {
		t.Errorf("BookDetail of the granted book = %q, %v", d.Card.Title, err)
	}
	hiddenAuthor, hiddenSeries := fullSibling.Card.Authors[0].ID, fullSibling.Card.Series.ID
	visibleAuthor, visibleSeries := detail.Card.Authors[0].ID, detail.Card.Series.ID
	if p, err := r.Person(ctx, sc, hiddenAuthor); err == nil {
		t.Errorf("Person of a hidden author = %+v", p)
	}
	if s, err := r.SeriesInfo(ctx, sc, hiddenSeries); err == nil {
		t.Errorf("SeriesInfo of a hidden series = %+v", s)
	}
	if p, err := r.Person(ctx, sc, visibleAuthor); err != nil || p.AuthorOf != 1 {
		t.Errorf("Person of the visible author = %+v, %v", p, err)
	}
	if s, err := r.SeriesInfo(ctx, sc, visibleSeries); err != nil || len(s.Books) != 1 {
		t.Errorf("SeriesInfo of the visible series = %+v, %v", s, err)
	}
	if d, err := r.PersonDuration(ctx, sc, hiddenAuthor, library.RoleAuthor); err != nil || d != 0 {
		t.Errorf("PersonDuration of a hidden author = %d, %v", d, err)
	}
	if d, err := r.PersonDuration(ctx, sc, visibleAuthor, library.RoleAuthor); err != nil || d != 1000 {
		t.Errorf("PersonDuration of the visible author = %d, %v", d, err)
	}
	if m, err := r.SeriesSequences(ctx, sc, hiddenSeries); err != nil || len(m) != 0 {
		t.Errorf("SeriesSequences of a hidden series = %v, %v", m, err)
	}
	if m, err := r.SeriesSequences(ctx, sc, visibleSeries); err != nil || len(m) != 1 {
		t.Errorf("SeriesSequences of the visible series = %v, %v", m, err)
	}

	// Counts and durations are the visible book's alone.
	if o, err := r.Overview(ctx, sc); err != nil || o.Books != 1 || o.DurationMs != 1000 || o.Problems != 0 {
		t.Errorf("Overview = %+v, %v", o, err)
	}
	if tot, err := r.Totals(ctx, sc, ""); err != nil || tot.Books != 1 || tot.DurationMs != 1000 || tot.Folders != 1 {
		t.Errorf("Totals = %+v, %v", tot, err)
	}
	if tot, err := r.Totals(ctx, sc, lh.ID); err != nil || tot.Books != 0 || tot.Folders != 0 {
		t.Errorf("Totals of the closed library = %+v, %v", tot, err)
	}
	if libs, err := r.VisibleLibraries(ctx, sc); err != nil || len(libs) != 1 || libs[0].ID != lv.ID || libs[0].Books != 1 {
		t.Errorf("VisibleLibraries = %+v, %v", libs, err)
	}
	if page, err := r.ListBooks(ctx, sc, library.ListQuery{}); err != nil || page.Total != 1 || len(page.Books) != 1 || page.Books[0].ID != visible.ID {
		t.Errorf("ListBooks = %+v, %v", page, err)
	}
	if shelves, err := r.Shelves(ctx, sc, "us_mem"); err != nil || len(shelves) != 1 || shelves[0].Books != 1 {
		t.Errorf("Shelves = %+v, %v", shelves, err)
	}
	if res, err := r.Search(ctx, sc, "Hiddenword", 20); err != nil || res.Total != 0 || len(res.IDs) != 0 {
		t.Errorf("Search for a hidden word = %+v, %v", res, err)
	}
	if res, err := r.Search(ctx, sc, "Visible", 20); err != nil || res.Total != 1 || len(res.IDs) != 1 || res.IDs[0] != visible.ID {
		t.Errorf("Search for the visible word = %+v, %v", res, err)
	}
	if got, err := r.ContinueListening(ctx, sc, "us_mem", 10); err != nil || len(got) != 1 || got[0].Book.ID != visible.ID {
		t.Errorf("ContinueListening = %+v, %v", got, err)
	}
}
