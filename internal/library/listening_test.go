package library_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

const listenOwner, listenMember = "us_a", "us_b"

// listenSeed returns a repo with an owner and a member, a progress service, and a folder to add books to.
func listenSeed(t *testing.T) (*library.Repo, *db.Store, *progress.Service, library.Folder) {
	t.Helper()
	r, st := newRepo(t)
	shelfUser(t, st, listenOwner)
	shelfUser(t, st, listenMember)
	_, f := librarytest.NewLibrary(t, r, "Main", "/audio")
	return r, st, progress.New(st, nil), f
}

// listenSave stores progress for a book. at is a number of seconds that orders saves; larger is newer.
func listenSave(t *testing.T, svc *progress.Service, user, book string, pos int64, at int64, finished bool) {
	t.Helper()
	base := time.Now().UnixMilli() - 1_000_000
	if _, err := svc.Save(ctx, progress.Update{UserID: user, BookID: book, DeviceID: "device-test-1", ClientUpdatedAt: base + at*1000,
		PositionMs: pos, DurationMs: 3_600_000, Finished: finished}); err != nil {
		t.Fatal(err)
	}
}

// listenScope hides the given books from an otherwise open scope.
func listenScope(hidden ...string) library.Scope {
	if len(hidden) == 0 {
		return allScope
	}
	sql := ""
	var args []any
	for i, id := range hidden {
		if i > 0 {
			sql += " AND "
		}
		sql += "b.id <> ?"
		args = append(args, id)
	}
	return library.Scope{Books: share.Filter{SQL: sql, Args: args}, Libraries: share.Filter{SQL: "1=1"}}
}

func listenTitles(l []library.Listening) []string {
	var out []string
	for _, x := range l {
		out = append(out, x.Book.Title)
	}
	return out
}

func TestListeningContinueOrderAndScope(t *testing.T) {
	r, _, svc, f := listenSeed(t)
	add := func(title string, s librarytest.BookSpec) library.Book {
		s.Title, s.DurationMs = title, 3_600_000
		return librarytest.AddBook(t, r, f, s)
	}
	b0, b1, b2 := add("B0", librarytest.BookSpec{}), add("B1", librarytest.BookSpec{}), add("B2", librarytest.BookSpec{})
	done := add("Done", librarytest.BookSpec{})
	refused := add("Refused", librarytest.BookSpec{Refused: library.RefusedDRM})
	missing := add("Missing", librarytest.BookSpec{MissingSince: 5})
	untouched := add("Untouched", librarytest.BookSpec{})
	_ = untouched
	listenSave(t, svc, listenOwner, b0.ID, 1000, 1, false)
	listenSave(t, svc, listenOwner, b1.ID, 2000, 3, false)
	listenSave(t, svc, listenOwner, b2.ID, 3000, 2, false)
	listenSave(t, svc, listenOwner, done.ID, 3_600_000, 5, true)
	listenSave(t, svc, listenOwner, refused.ID, 1, 6, false)
	listenSave(t, svc, listenOwner, missing.ID, 1, 7, false)
	listenSave(t, svc, listenMember, b0.ID, 9, 8, false)

	got, err := r.ContinueListening(ctx, allScope, listenOwner, 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"B1", "B2", "B0"}; !reflect.DeepEqual(listenTitles(got), want) {
		t.Fatalf("titles = %v, want %v", listenTitles(got), want)
	}
	if got[0].PositionMs != 2000 || got[0].DurationMs != 3_600_000 || got[0].Book.ID != b1.ID || got[0].UpdatedAt == 0 {
		t.Fatalf("hero = %+v", got[0])
	}
	if mine, _ := r.ContinueListening(ctx, allScope, listenMember, 10); len(mine) != 1 || mine[0].PositionMs != 9 {
		t.Fatalf("member rows = %+v", mine)
	}
	if none, err := r.ContinueListening(ctx, noneScope, listenOwner, 10); err != nil || len(none) != 0 {
		t.Fatalf("member scope (0=1) = %+v, %v", none, err)
	}
	if hid, _ := r.ContinueListening(ctx, listenScope(b1.ID), listenOwner, 10); !reflect.DeepEqual(listenTitles(hid), []string{"B2", "B0"}) {
		t.Fatalf("hidden book listed: %v", listenTitles(hid))
	}
	for limit, want := range map[int]int{-3: 1, 0: 1, 1: 1, 2: 2, 100: 3} {
		if l, _ := r.ContinueListening(ctx, allScope, listenOwner, limit); len(l) != want {
			t.Errorf("limit %d: %d rows, want %d", limit, len(l), want)
		}
	}
	// "Mark as not started" (unfinished, position 0) stays off the shelf: no hero, no mini-player.
	if _, err := svc.SetFinished(ctx, listenOwner, done.ID, false, 3_600_000); err != nil {
		t.Fatal(err)
	}
	if l, _ := r.ContinueListening(ctx, allScope, listenOwner, 10); len(l) != 3 || l[0].Book.ID == done.ID {
		t.Fatalf("after mark as not started: %v", listenTitles(l))
	}
}

func TestListeningSeriesNext(t *testing.T) {
	type st struct {
		book int // 1-based
		pos  int64
		fin  bool
	}
	cases := []struct {
		name     string
		saves    []st
		hide     int // 1-based, 0 = none
		wantNext int // 0 = series absent
		started  bool
		done     int
		total    int
	}{
		{"nothing touched", nil, 0, 0, false, 0, 0},
		{"book 2 in progress", []st{{2, 5000, false}}, 0, 2, true, 0, 4},
		{"book 2 finished", []st{{2, 100, true}}, 0, 3, false, 1, 4},
		{"all finished", []st{{1, 1, true}, {2, 1, true}, {3, 1, true}, {4, 1, true}}, 0, 0, false, 0, 0},
		{"book 3 finished, 1 untouched", []st{{3, 1, true}}, 0, 4, false, 1, 4},
		{"hidden book skipped", []st{{2, 1, true}}, 3, 4, false, 1, 3},
		{"book 4 finished is the end", []st{{4, 1, true}}, 0, 0, false, 0, 0},
		{"book 1 finished, 2 in progress", []st{{1, 1, true}, {2, 777, false}}, 0, 2, true, 1, 4},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, _, svc, f := listenSeed(t)
			var books []library.Book
			for i := 1; i <= 4; i++ {
				books = append(books, librarytest.AddBook(t, r, f, librarytest.BookSpec{
					Title: fmt.Sprintf("S%d", i), Series: "Saga", Sequence: fmt.Sprint(i), DurationMs: 3_600_000}))
			}
			for i, s := range c.saves {
				listenSave(t, svc, listenOwner, books[s.book-1].ID, s.pos, int64(i+1), s.fin)
			}
			sc := allScope
			if c.hide > 0 {
				sc = listenScope(books[c.hide-1].ID)
			}
			got, err := r.SeriesInProgress(ctx, sc, listenOwner, 10)
			if err != nil {
				t.Fatal(err)
			}
			if c.wantNext == 0 {
				if len(got) != 0 {
					t.Fatalf("got %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("got %d series", len(got))
			}
			g := got[0]
			if g.Next.ID != books[c.wantNext-1].ID || g.Started != c.started || g.Done != c.done || g.Total != c.total ||
				g.Series.Name != "Saga" || g.Series.Sequence != fmt.Sprint(c.wantNext) {
				t.Fatalf("got %+v, want next %d started %v done %d total %d", g, c.wantNext, c.started, c.done, c.total)
			}
			// NextIndex counts the visible books: a hidden book before Next does not take a place.
			wantIndex := c.wantNext
			if c.hide > 0 && c.hide < c.wantNext {
				wantIndex--
			}
			if g.NextIndex != wantIndex {
				t.Fatalf("NextIndex = %d, want %d", g.NextIndex, wantIndex)
			}
			if c.started && g.PositionMs == 0 {
				t.Fatal("started series has no position")
			}
			// Another person's progress never makes a series appear.
			if other, _ := r.SeriesInProgress(ctx, sc, listenMember, 10); len(other) != 0 {
				t.Fatalf("member sees %+v", other)
			}
		})
	}
}

func TestListeningSeriesOrderAndScope(t *testing.T) {
	r, _, svc, f := listenSeed(t)
	mk := func(series string, n int) []library.Book {
		var out []library.Book
		for i := 1; i <= n; i++ {
			out = append(out, librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: fmt.Sprintf("%s%d", series, i), Series: series, Sequence: fmt.Sprint(i), DurationMs: 3_600_000}))
		}
		return out
	}
	x, y, z := mk("X", 3), mk("Y", 3), mk("Z", 3)
	listenSave(t, svc, listenOwner, x[0].ID, 1, 1, true)
	listenSave(t, svc, listenOwner, y[1].ID, 500, 3, false)
	listenSave(t, svc, listenOwner, z[0].ID, 1, 2, true)
	got, err := r.SeriesInProgress(ctx, allScope, listenOwner, 10)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, g := range got {
		names = append(names, g.Series.Name+":"+g.Next.Title)
	}
	if want := []string{"Y:Y2", "Z:Z2", "X:X2"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("got %v, want %v", names, want)
	}
	if one, _ := r.SeriesInProgress(ctx, allScope, listenOwner, 1); len(one) != 1 || one[0].Series.Name != "Y" {
		t.Fatalf("limit 1 = %+v", one)
	}
	if none, err := r.SeriesInProgress(ctx, noneScope, listenOwner, 10); err != nil || len(none) != 0 {
		t.Fatalf("none scope = %+v, %v", none, err)
	}
	// The only touched book of Y is hidden: the series disappears.
	if hid, _ := r.SeriesInProgress(ctx, listenScope(y[1].ID), listenOwner, 10); len(hid) != 2 {
		t.Fatalf("with the touched book hidden: %d series", len(hid))
	}
}

func TestListeningBecause(t *testing.T) {
	r, _, svc, f := listenSeed(t)
	add := func(title string, s librarytest.BookSpec) library.Book {
		s.Title, s.DurationMs = title, 3_600_000
		return librarytest.AddBook(t, r, f, s)
	}
	seed := add("Seed", librarytest.BookSpec{Authors: []string{"Ann"}, Narrators: []string{"Nat"}, Genres: []string{"Fantasy"}, Series: "Saga", Sequence: "1", AddedAt: 1})
	c1 := add("ByAuthor", librarytest.BookSpec{Authors: []string{"Ann"}, AddedAt: 2})
	c2 := add("ByNarrator", librarytest.BookSpec{Authors: []string{"Zed"}, Narrators: []string{"Nat"}, AddedAt: 3})
	c3 := add("ByGenre", librarytest.BookSpec{Authors: []string{"Zed"}, Genres: []string{"fantasy"}, AddedAt: 4})
	_ = add("SameSeries", librarytest.BookSpec{Authors: []string{"Ann"}, Series: "Saga", Sequence: "2", AddedAt: 5})
	_ = add("Unrelated", librarytest.BookSpec{Authors: []string{"Qux"}, Genres: []string{"Cooking"}, AddedAt: 7})
	started := add("Started2", librarytest.BookSpec{Authors: []string{"Ann"}, AddedAt: 8})
	recent := add("RecentInProgress", librarytest.BookSpec{Authors: []string{"Other"}})
	listenSave(t, svc, listenOwner, started.ID, 1000, 1, false)
	listenSave(t, svc, listenOwner, seed.ID, 3_600_000, 2, true)
	listenSave(t, svc, listenOwner, recent.ID, 1_200_000, 9, false) // newer, but a finished book is preferred

	titles := func(b library.Because) []string {
		var out []string
		for _, c := range b.Books {
			out = append(out, c.Title)
		}
		return out
	}
	got, ok, err := r.BecauseYouListened(ctx, allScope, listenOwner, 10)
	if err != nil || !ok {
		t.Fatalf("ok=%v err=%v", ok, err)
	}
	if got.Seed.ID != seed.ID {
		t.Fatalf("seed = %s, want the finished book", got.Seed.Title)
	}
	if want := []string{"ByAuthor", "ByNarrator", "ByGenre"}; !reflect.DeepEqual(titles(got), want) {
		t.Fatalf("books = %v, want %v (author > narrator > genre; seed series and started books out)", titles(got), want)
	}
	// Scoped: a hidden candidate is gone; a hidden seed falls back to the in-progress book.
	if g, ok, _ := r.BecauseYouListened(ctx, listenScope(c1.ID), listenOwner, 10); !ok || !reflect.DeepEqual(titles(g), []string{"ByNarrator", "ByGenre"}) {
		t.Fatalf("hidden candidate: %v", titles(g))
	}
	if g, ok, _ := r.BecauseYouListened(ctx, listenScope(seed.ID), listenOwner, 10); ok && g.Seed.ID == seed.ID {
		t.Fatal("hidden seed used")
	}
	if _, ok, err := r.BecauseYouListened(ctx, noneScope, listenOwner, 10); ok || err != nil {
		t.Fatalf("none scope: ok=%v err=%v", ok, err)
	}
	if g, ok, _ := r.BecauseYouListened(ctx, allScope, listenOwner, 2); !ok || len(g.Books) != 2 {
		t.Fatalf("limit 2 = %v", titles(g))
	}
	if _, ok, _ := r.BecauseYouListened(ctx, allScope, listenMember, 10); ok {
		t.Fatal("a person without progress got a shelf")
	}
	_ = c2
	_ = c3

	// An in-progress book past 10 minutes seeds the shelf when nothing is finished; under 10 minutes it does not.
	r2, _, svc2, f2 := listenSeed(t)
	a := librarytest.AddBook(t, r2, f2, librarytest.BookSpec{Title: "A", Authors: []string{"Ann"}, DurationMs: 3_600_000})
	librarytest.AddBook(t, r2, f2, librarytest.BookSpec{Title: "Other", Authors: []string{"Ann"}, DurationMs: 3_600_000})
	listenSave(t, svc2, listenOwner, a.ID, 599_999, 1, false)
	if _, ok, _ := r2.BecauseYouListened(ctx, allScope, listenOwner, 10); ok {
		t.Fatal("under 10 minutes seeded the shelf")
	}
	listenSave(t, svc2, listenOwner, a.ID, 600_000, 2, false)
	if g, ok, _ := r2.BecauseYouListened(ctx, allScope, listenOwner, 10); !ok || g.Seed.Title != "A" || len(g.Books) != 1 || g.Books[0].Title != "Other" {
		t.Fatalf("10 minutes in: ok=%v %+v", ok, g)
	}
}

func TestListeningChapterAt(t *testing.T) {
	r, _, _, f := listenSeed(t)
	b := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "C", DurationMs: 3000})
	bare := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Bare", DurationMs: 3000})
	err := r.Update(ctx, func(tx *library.Tx) error {
		return tx.ReplaceChapters(b.ID, []library.Chapter{
			{StartMs: 100, EndMs: 1000, Title: "One", Source: "file"},
			{StartMs: 1000, EndMs: 2000, Title: "Two", Source: "file"},
			{StartMs: 2000, EndMs: 3000, Title: "Three", Source: "file"},
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name string
		pos  int64
		want string
	}{
		{"before the first", 0, "One"}, {"exact start", 1000, "Two"}, {"inside", 1500, "Two"},
		{"first start", 100, "One"}, {"after the last", 99_999, "Three"},
	} {
		ch, n, ok, err := r.ChapterAt(ctx, b.ID, c.pos)
		if err != nil || !ok || n != 3 || ch.Title != c.want {
			t.Errorf("%s: %+v n=%d ok=%v err=%v, want %s", c.name, ch, n, ok, err, c.want)
		}
	}
	if _, n, ok, err := r.ChapterAt(ctx, bare.ID, 5); ok || n != 0 || err != nil {
		t.Fatalf("no chapters: n=%d ok=%v err=%v", n, ok, err)
	}
}
