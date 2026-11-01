package library_test

import (
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// browseSeed adds books with equal added_at values and titles that tie under NOCASE, so every
// ordering has to fall back to the id.
func browseSeed(t *testing.T) (*library.Repo, library.Library, []library.Book) {
	t.Helper()
	r, _ := newRepo(t)
	lib, f := librarytest.NewLibrary(t, r, "Main", "/audio")
	specs := []librarytest.BookSpec{
		{Title: "banana", Authors: []string{"Zed Zoe"}, AddedAt: 100, DurationMs: 500},
		{Title: "Apple", Authors: []string{"Amy Ash"}, AddedAt: 100, DurationMs: 500},
		{Title: "apple", Authors: []string{"Amy Ash"}, AddedAt: 100, DurationMs: 900},
		{Title: "Cherry", AddedAt: 200, DurationMs: 100},
		{Title: "Date", SortTitle: "Aardvark Date", Authors: []string{"Bo Bee"}, AddedAt: 300, DurationMs: 900},
		{Title: "Elder", Authors: []string{"Bo Bee"}, AddedAt: 300, DurationMs: 100},
		{Title: "Fig", Authors: []string{"amy ash"}, AddedAt: 50, DurationMs: 500},
		{Title: "Grape", AddedAt: 50, DurationMs: 500},
		{Title: "Honey", Authors: []string{"Cy Cox"}, AddedAt: 400, DurationMs: 700},
	}
	var books []library.Book
	for _, s := range specs {
		books = append(books, librarytest.AddBook(t, r, f, s))
	}
	return r, lib, books
}

// order is the expected order of ids for sort s, worked out in Go from the seeded books.
func order(t *testing.T, r *library.Repo, books []library.Book, s library.Sort) []string {
	t.Helper()
	type row struct {
		id, tk, ak string
		added, dur int64
	}
	var rows []row
	for _, b := range books {
		tk := b.SortTitle
		if tk == "" {
			tk = b.Title
		}
		ak := "\U0010FFFF"
		people, err := r.BookPeople(ctx, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range people {
			if p.Role == library.RoleAuthor {
				ak = p.Name
				break
			}
		}
		rows = append(rows, row{b.ID, strings.ToLower(tk), strings.ToLower(ak), b.AddedAt, b.DurationMs})
	}
	sort.Slice(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		switch s {
		case library.SortTitle:
			if a.tk != b.tk {
				return a.tk < b.tk
			}
			return a.id < b.id
		case library.SortAuthor:
			if a.ak != b.ak {
				return a.ak < b.ak
			}
			if a.tk != b.tk {
				return a.tk < b.tk
			}
			return a.id < b.id
		case library.SortDuration:
			if a.dur != b.dur {
				return a.dur > b.dur
			}
			return a.id > b.id
		}
		if a.added != b.added {
			return a.added > b.added
		}
		return a.id > b.id
	})
	ids := make([]string, len(rows))
	for i, x := range rows {
		ids[i] = x.id
	}
	return ids
}

func cardIDs(cs []library.BookCard) []string {
	ids := make([]string, len(cs))
	for i, c := range cs {
		ids[i] = c.ID
	}
	return ids
}

var allSorts = []library.Sort{library.SortAdded, library.SortTitle, library.SortAuthor, library.SortDuration}

func TestListBooksSorts(t *testing.T) {
	r, _, books := browseSeed(t)
	for _, s := range allSorts {
		t.Run(string(s), func(t *testing.T) {
			p, err := r.ListBooks(ctx, allScope, library.ListQuery{Sort: s})
			if err != nil {
				t.Fatal(err)
			}
			if got, want := cardIDs(p.Books), order(t, r, books, s); !reflect.DeepEqual(got, want) {
				t.Errorf("order = %v\nwant    %v", got, want)
			}
			if p.Total != len(books) || p.Next != "" {
				t.Errorf("Total %d Next %q", p.Total, p.Next)
			}
		})
	}
	if library.ParseSort("nope") != library.SortAdded || library.ParseSort("") != library.SortAdded || library.ParseSort("title") != library.SortTitle {
		t.Error("ParseSort")
	}
}

func TestListBooksCursorWalk(t *testing.T) {
	r, _, books := browseSeed(t)
	for _, s := range allSorts {
		t.Run(string(s), func(t *testing.T) {
			var got []string
			cursor := ""
			for pages := 0; ; pages++ {
				if pages > 10 {
					t.Fatal("too many pages")
				}
				p, err := r.ListBooks(ctx, allScope, library.ListQuery{Sort: s, Limit: 3, Cursor: cursor})
				if err != nil {
					t.Fatal(err)
				}
				if p.Total != len(books) {
					t.Errorf("page %d Total = %d", pages, p.Total)
				}
				got = append(got, cardIDs(p.Books)...)
				if p.Next == "" {
					if len(p.Books) == 0 {
						t.Error("empty last page")
					}
					break
				}
				if len(p.Books) != 3 {
					t.Errorf("page %d has %d books before a Next", pages, len(p.Books))
				}
				cursor = p.Next
			}
			if want := order(t, r, books, s); !reflect.DeepEqual(got, want) {
				t.Errorf("walk = %v\nwant   %v", got, want)
			}
		})
	}
}

func TestListBooksCursorInvalid(t *testing.T) {
	r, _, _ := browseSeed(t)
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	p, err := r.ListBooks(ctx, allScope, library.ListQuery{Sort: library.SortAdded, Limit: 2})
	if err != nil || p.Next == "" {
		t.Fatalf("setup: %v %q", err, p.Next)
	}
	for _, c := range []struct{ name, sort, cursor string }{
		{"garbage", "added", "!!!not base64!!!"},
		{"bad json", "added", enc("{nope")},
		{"other sort", "title", p.Next},
		{"empty id", "added", enc(`{"s":"added","n":1,"i":""}`)},
		{"no id", "added", enc(`{"s":"added","n":1}`)},
		{"missing key", "added", enc(`{"s":"added","i":"x"}`)},
		{"title without t", "title", enc(`{"s":"title","i":"x"}`)},
		{"author without a", "author", enc(`{"s":"author","t":"x","i":"x"}`)},
		{"json null", "added", enc(`null`)},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := r.ListBooks(ctx, allScope, library.ListQuery{Sort: library.Sort(c.sort), Cursor: c.cursor})
			if !errors.Is(err, library.ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
}

func TestListBooksFilters(t *testing.T) {
	r, lib, _ := browseSeed(t)
	_, f2 := librarytest.NewLibrary(t, r, "Other", "/other")
	a := librarytest.AddBook(t, r, f2, librarytest.BookSpec{Title: "A1", Authors: []string{"Una Uno"}, Narrators: []string{"Nel Nox"},
		Series: "Saga", Sequence: "1", Genres: []string{"Fantasy", "Travel"}})
	librarytest.AddBook(t, r, f2, librarytest.BookSpec{Title: "B1", Authors: []string{"Una Uno"}, Genres: []string{"fantasy"}})
	librarytest.AddBook(t, r, f2, librarytest.BookSpec{Title: "C1", Genres: []string{"Travel"}})

	titles := func(q library.ListQuery) (string, int) {
		p, err := r.ListBooks(ctx, allScope, q)
		if err != nil {
			t.Fatal(err)
		}
		var ts []string
		for _, c := range p.Books {
			ts = append(ts, c.Title)
		}
		sort.Strings(ts)
		if p.Total != len(p.Books) {
			t.Errorf("Total %d != %d books for %+v", p.Total, len(p.Books), q)
		}
		return strings.Join(ts, ","), p.Total
	}
	if got, _ := titles(library.ListQuery{LibraryID: f2.LibraryID}); got != "A1,B1,C1" {
		t.Errorf("library = %q", got)
	}
	if got, n := titles(library.ListQuery{LibraryID: lib.ID}); n != 9 {
		t.Errorf("main library = %q (%d)", got, n)
	}
	if got, _ := titles(library.ListQuery{Genres: []string{"fantasy"}}); got != "A1,B1" {
		t.Errorf("one genre = %q", got)
	}
	if got, _ := titles(library.ListQuery{Genres: []string{"Fantasy", "travel"}}); got != "A1" {
		t.Errorf("two genres = %q", got)
	}
	people, _ := r.BookPeople(ctx, a.ID)
	var author, narrator string
	for _, p := range people {
		if p.Role == library.RoleAuthor {
			author = p.PersonID
		} else {
			narrator = p.PersonID
		}
	}
	if got, _ := titles(library.ListQuery{AuthorID: author}); got != "A1,B1" {
		t.Errorf("author = %q", got)
	}
	if got, _ := titles(library.ListQuery{NarratorID: narrator}); got != "A1" {
		t.Errorf("narrator = %q", got)
	}
	if got, _ := titles(library.ListQuery{AuthorID: narrator}); got != "" {
		t.Errorf("narrator as author = %q", got)
	}
	series, _ := r.BookSeries(ctx, a.ID)
	if got, _ := titles(library.ListQuery{SeriesID: series[0].SeriesID}); got != "A1" {
		t.Errorf("series = %q", got)
	}
	if got, _ := titles(library.ListQuery{AuthorID: author, Genres: []string{"travel"}}); got != "A1" {
		t.Errorf("author+genre = %q", got)
	}
}

func TestListBooksHidesRefusedAndMissing(t *testing.T) {
	r, _ := newRepo(t)
	_, f := librarytest.NewLibrary(t, r, "Main", "/audio")
	ok := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Fine", Genres: []string{"Drama"}})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Locked", Refused: library.RefusedDRM, Genres: []string{"Drama"}})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Gone", MissingSince: 5, Genres: []string{"Drama"}})
	p, err := r.ListBooks(ctx, allScope, library.ListQuery{})
	if err != nil || p.Total != 1 || len(p.Books) != 1 || p.Books[0].ID != ok.ID {
		t.Fatalf("list = %+v, %v", p, err)
	}
	g, err := r.Genres(ctx, allScope, "")
	if err != nil || len(g) != 1 || g[0].Count != 1 {
		t.Errorf("genres = %+v, %v", g, err)
	}
	libs, err := r.VisibleLibraries(ctx, allScope)
	if err != nil || len(libs) != 1 || libs[0].Books != 1 {
		t.Errorf("libraries = %+v, %v", libs, err)
	}
}

func TestListBooksScopeDenies(t *testing.T) {
	r, _, _ := browseSeed(t)
	p, err := r.ListBooks(ctx, noneScope, library.ListQuery{})
	if err != nil || len(p.Books) != 0 || p.Total != 0 || p.Next != "" {
		t.Fatalf("page = %+v, %v", p, err)
	}
	if g, err := r.Genres(ctx, noneScope, ""); err != nil || len(g) != 0 {
		t.Errorf("genres = %+v, %v", g, err)
	}
	if l, err := r.VisibleLibraries(ctx, noneScope); err != nil || len(l) != 0 {
		t.Errorf("libraries = %+v, %v", l, err)
	}
}

// A scope with arguments: placeholders and arguments must stay paired in order.
func TestListBooksScopeArgs(t *testing.T) {
	r, lib, books := browseSeed(t)
	sc := library.Scope{
		Books:     share.Filter{SQL: "b.id <> ? AND b.id <> ?", Args: []any{books[0].ID, books[1].ID}},
		Libraries: share.Filter{SQL: "l.id = ?", Args: []any{lib.ID}},
	}
	p, err := r.ListBooks(ctx, sc, library.ListQuery{LibraryID: lib.ID})
	if err != nil || p.Total != len(books)-2 {
		t.Fatalf("page = %d %v", p.Total, err)
	}
	libs, err := r.VisibleLibraries(ctx, sc)
	if err != nil || len(libs) != 1 || libs[0].Books != len(books)-2 {
		t.Errorf("libraries = %+v, %v", libs, err)
	}
}

func TestListBooksTotalMatchesFilters(t *testing.T) {
	r, _ := newRepo(t)
	_, f := librarytest.NewLibrary(t, r, "Main", "/audio")
	for i := 0; i < 7; i++ {
		g := "Even"
		if i%2 == 1 {
			g = "Odd"
		}
		librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: fmt.Sprintf("Book %d", i), Genres: []string{g}})
	}
	p, err := r.ListBooks(ctx, allScope, library.ListQuery{Genres: []string{"Odd"}, Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if p.Total != 3 || len(p.Books) != 2 || p.Next == "" {
		t.Errorf("odd page = %d total, %d books, next %q", p.Total, len(p.Books), p.Next)
	}
	g, err := r.Genres(ctx, allScope, "")
	if err != nil || len(g) != 2 || g[0] != (library.Facet{Value: "Even", Count: 4}) || g[1] != (library.Facet{Value: "Odd", Count: 3}) {
		t.Errorf("facets = %+v, %v", g, err)
	}
}

func TestListBooksLimit(t *testing.T) {
	r, _, books := browseSeed(t)
	p, err := r.ListBooks(ctx, allScope, library.ListQuery{Limit: -4})
	if err != nil || len(p.Books) != 1 || p.Next == "" {
		t.Errorf("negative limit: %d books, %q, %v", len(p.Books), p.Next, err)
	}
	p, err = r.ListBooks(ctx, allScope, library.ListQuery{Limit: 100000})
	if err != nil || len(p.Books) != len(books) {
		t.Errorf("huge limit: %d books, %v", len(p.Books), err)
	}
}

func TestCardsHydration(t *testing.T) {
	r, _ := newRepo(t)
	_, f := librarytest.NewLibrary(t, r, "Main", "/audio")
	b := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Full", Subtitle: "Sub", Authors: []string{"Zed", "Amy"},
		Narrators: []string{"Nel"}, Series: "Saga", Sequence: "2", DurationMs: 4000, PublishedYear: 2019})
	empty := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "", Authors: []string{"Amy"}})
	c := newCover(b.ID, 'c')
	changes(t, r, func(tx *library.Tx) error {
		if err := tx.PutCover(&c); err != nil {
			return err
		}
		return tx.SetBookCover(b.ID, c.ID)
	})
	cards, err := r.CardsByID(ctx, allScope, []string{b.ID, empty.ID})
	if err != nil || len(cards) != 2 {
		t.Fatalf("cards = %+v, %v", cards, err)
	}
	full := cards[0]
	if full.Title != "Full" || full.Subtitle != "Sub" || full.DurationMs != 4000 || full.PublishedYear != 2019 || full.LibraryID != f.LibraryID {
		t.Errorf("card = %+v", full)
	}
	if len(full.Authors) != 2 || full.Authors[0].Name != "Zed" || full.Authors[1].Name != "Amy" || len(full.Narrators) != 1 || full.Narrators[0].Name != "Nel" {
		t.Errorf("people = %+v / %+v", full.Authors, full.Narrators)
	}
	if full.Series == nil || full.Series.Name != "Saga" || full.Series.Sequence != "2" {
		t.Errorf("series = %+v", full.Series)
	}
	want := &library.CoverRef{SHA: c.SHA256, Mime: "image/jpeg", Tint: "#2c6660", Width: 600, Height: 600,
		Accent: map[string]string{"dark": "#9ac9c2", "light": "#35615c"}}
	if !reflect.DeepEqual(full.Cover, want) {
		t.Errorf("cover = %+v, want %+v", full.Cover, want)
	}
	// An empty title falls back to the folder name; no series and no cover stay nil.
	if !strings.HasPrefix(cards[1].Title, "book-") || cards[1].Series != nil || cards[1].Cover != nil {
		t.Errorf("fallback card = %+v", cards[1])
	}
	// A cover without accent colours has a nil map.
	c2 := newCover(empty.ID, 'd')
	c2.Accent = nil
	changes(t, r, func(tx *library.Tx) error {
		if err := tx.PutCover(&c2); err != nil {
			return err
		}
		return tx.SetBookCover(empty.ID, c2.ID)
	})
	cards, _ = r.CardsByID(ctx, allScope, []string{empty.ID})
	if len(cards) != 1 || cards[0].Cover == nil || cards[0].Cover.Accent != nil {
		t.Errorf("no-accent card = %+v", cards)
	}
}

func TestCardsByIDOrderAndScope(t *testing.T) {
	r, _, books := browseSeed(t)
	_, f := librarytest.NewLibrary(t, r, "Second", "/second")
	refused := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "R", Refused: library.RefusedDRM})
	missing := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "M", MissingSince: 3})
	in := []string{books[3].ID, "bk_unknown", refused.ID, books[0].ID, missing.ID, books[3].ID, books[7].ID}
	cards, err := r.CardsByID(ctx, allScope, in)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cardIDs(cards), []string{books[3].ID, books[0].ID, books[7].ID}; !reflect.DeepEqual(got, want) {
		t.Errorf("ids = %v, want %v", got, want)
	}
	if cards, err := r.CardsByID(ctx, noneScope, in); err != nil || len(cards) != 0 {
		t.Errorf("denied = %+v, %v", cards, err)
	}
	if cards, err := r.CardsByID(ctx, allScope, nil); err != nil || len(cards) != 0 {
		t.Errorf("empty = %+v, %v", cards, err)
	}
	ids := make([]string, 201)
	for i := range ids {
		ids[i] = fmt.Sprintf("bk_%d", i)
	}
	if _, err := r.CardsByID(ctx, allScope, ids); !errors.Is(err, library.ErrInvalid) {
		t.Errorf("201 ids: %v", err)
	}
	if _, err := r.CardsByID(ctx, allScope, ids[:200]); err != nil {
		t.Errorf("200 ids: %v", err)
	}
}

func TestGenresFacet(t *testing.T) {
	r, _ := newRepo(t)
	_, f1 := librarytest.NewLibrary(t, r, "One", "/one")
	_, f2 := librarytest.NewLibrary(t, r, "Two", "/two")
	librarytest.AddBook(t, r, f1, librarytest.BookSpec{Title: "a", Genres: []string{"Drama", "Crime"}})
	librarytest.AddBook(t, r, f1, librarytest.BookSpec{Title: "b", Genres: []string{"drama"}})
	librarytest.AddBook(t, r, f2, librarytest.BookSpec{Title: "c", Genres: []string{"Crime", "Zen"}})
	librarytest.AddBook(t, r, f2, librarytest.BookSpec{Title: "d", Genres: []string{"Alpha"}})
	vals := func(libID string) []string {
		g, err := r.Genres(ctx, allScope, libID)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, x := range g {
			out = append(out, fmt.Sprintf("%s=%d", strings.ToLower(x.Value), x.Count))
		}
		return out
	}
	if got, want := vals(""), []string{"crime=2", "drama=2", "alpha=1", "zen=1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("all = %v, want %v", got, want)
	}
	if got, want := vals(f2.LibraryID), []string{"alpha=1", "crime=1", "zen=1"}; !reflect.DeepEqual(got, want) {
		t.Errorf("library two = %v, want %v", got, want)
	}
}

func TestVisibleLibraries(t *testing.T) {
	r, _ := newRepo(t)
	_, fb := librarytest.NewLibrary(t, r, "banana", "/b")
	_, fa := librarytest.NewLibrary(t, r, "Apple", "/a")
	librarytest.AddBook(t, r, fb, librarytest.BookSpec{Title: "x"})
	librarytest.AddBook(t, r, fb, librarytest.BookSpec{Title: "y"})
	librarytest.AddBook(t, r, fb, librarytest.BookSpec{Title: "z", Refused: library.RefusedDamaged})
	libs, err := r.VisibleLibraries(ctx, allScope)
	if err != nil || len(libs) != 2 {
		t.Fatalf("libraries = %+v, %v", libs, err)
	}
	if libs[0].Name != "Apple" || libs[0].Books != 0 || libs[0].ID != fa.LibraryID || libs[1].Name != "banana" || libs[1].Books != 2 {
		t.Errorf("libraries = %+v", libs)
	}
	only := library.Scope{Books: share.Filter{SQL: "1=1"}, Libraries: share.Filter{SQL: "l.id = ?", Args: []any{fb.LibraryID}}}
	libs, err = r.VisibleLibraries(ctx, only)
	if err != nil || len(libs) != 1 || libs[0].Name != "banana" {
		t.Errorf("scoped = %+v, %v", libs, err)
	}
}

func TestBookDetail(t *testing.T) {
	r, _ := newRepo(t)
	_, f := librarytest.NewLibrary(t, r, "Main", "/audio")
	b := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Parts", Authors: []string{"Amy"}, Series: "Saga", Sequence: "3",
		Genres: []string{"Drama", "Crime"}, Files: 3, MissingSince: 7})
	changes(t, r, func(tx *library.Tx) error {
		files, err := tx.AudioFiles(b.ID)
		if err != nil {
			return err
		}
		if err := tx.SetBookLabels(b.ID, library.LabelTag, []string{"cosy"}); err != nil {
			return err
		}
		return tx.SetAudioFileMissing(files[1].ID, 9)
	})
	d, err := r.BookDetail(ctx, allScope, b.ID) // the book itself is missing: still has a page
	if err != nil {
		t.Fatal(err)
	}
	if d.Parts != 3 || d.MissingParts != 1 || d.Book.ID != b.ID || d.Card.Title != "Parts" || len(d.Card.Authors) != 1 {
		t.Errorf("detail = %+v", d)
	}
	if len(d.Series) != 1 || d.Series[0].Name != "Saga" || d.Series[0].Sequence != "3" {
		t.Errorf("series = %+v", d.Series)
	}
	if !reflect.DeepEqual(d.Genres, []string{"Crime", "Drama"}) || !reflect.DeepEqual(d.Tags, []string{"cosy"}) {
		t.Errorf("labels = %v / %v", d.Genres, d.Tags)
	}
	if _, err := r.BookDetail(ctx, noneScope, b.ID); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("denied = %v, want ErrNotFound", err)
	}
	if _, err := r.BookDetail(ctx, allScope, "bk_unknown"); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("unknown = %v, want ErrNotFound", err)
	}
}

func TestPersonNeedsVisibleBook(t *testing.T) {
	r, _ := newRepo(t)
	_, f := librarytest.NewLibrary(t, r, "Main", "/audio")
	ghost := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Gone", Authors: []string{"Ghost"}, MissingSince: 4})
	both := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "One", Authors: []string{"Amy"}, Narrators: []string{"Amy"}})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Two", Authors: []string{"Amy"}})
	idOf := func(b library.Book, role string) string {
		people, _ := r.BookPeople(ctx, b.ID)
		for _, p := range people {
			if p.Role == role {
				return p.PersonID
			}
		}
		t.Fatalf("no %s on %s", role, b.Title)
		return ""
	}
	if _, err := r.Person(ctx, allScope, idOf(ghost, library.RoleAuthor)); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("ghost = %v, want ErrNotFound", err)
	}
	amy := idOf(both, library.RoleAuthor)
	p, err := r.Person(ctx, allScope, amy)
	if err != nil || p.Name != "Amy" || p.AuthorOf != 2 || p.NarratorOf != 1 {
		t.Errorf("amy = %+v, %v", p, err)
	}
	if _, err := r.Person(ctx, noneScope, amy); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("denied = %v", err)
	}
	if _, err := r.Person(ctx, allScope, "pe_unknown"); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("unknown = %v", err)
	}
}

func TestSeriesOrder(t *testing.T) {
	r, _ := newRepo(t)
	_, f := librarytest.NewLibrary(t, r, "Main", "/audio")
	var anyBook library.Book
	for _, s := range []struct{ title, seq string }{
		{"Ten", "10"}, {"Prequel", "Prequel"}, {"Two", "2"}, {"Blank", ""}, {"One-and-half", "1.5"}, {"One", "1"}, {"Zed unnumbered", "x"},
	} {
		anyBook = librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: s.title, Series: "Saga", Sequence: s.seq})
	}
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Hidden", Series: "Saga", Sequence: "0", Refused: library.RefusedDRM})
	series, err := r.BookSeries(ctx, anyBook.ID)
	if err != nil || len(series) != 1 {
		t.Fatal(series, err)
	}
	sp, err := r.SeriesInfo(ctx, allScope, series[0].SeriesID)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, c := range sp.Books {
		got = append(got, c.Title)
	}
	want := []string{"One", "One-and-half", "Two", "Ten", "Blank", "Prequel", "Zed unnumbered"}
	if !reflect.DeepEqual(got, want) || sp.Name != "Saga" {
		t.Errorf("series = %q %v\nwant %v", sp.Name, got, want)
	}
	if _, err := r.SeriesInfo(ctx, noneScope, series[0].SeriesID); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("denied = %v", err)
	}
	if _, err := r.SeriesInfo(ctx, allScope, "se_unknown"); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("unknown = %v", err)
	}
}
