package library_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

func TestPlayShowFilter(t *testing.T) {
	r, _, svc, f := listenSeed(t)
	add := func(title string, s librarytest.BookSpec) library.Book {
		s.Title, s.DurationMs, s.Authors = title, 3_600_000, []string{"Ann"}
		return librarytest.AddBook(t, r, f, s)
	}
	add("Fresh", librarytest.BookSpec{})
	started, zero := add("Started", librarytest.BookSpec{}), add("Zero", librarytest.BookSpec{})
	done, other := add("Done", librarytest.BookSpec{Genres: []string{"Mystery"}}), add("Other", librarytest.BookSpec{})
	listenSave(t, svc, listenOwner, started.ID, 5000, 1, false)
	listenSave(t, svc, listenOwner, zero.ID, 0, 2, false)
	listenSave(t, svc, listenOwner, done.ID, 3_600_000, 3, true)
	listenSave(t, svc, listenMember, other.ID, 7000, 4, false) // someone else's progress never counts for the owner

	titles := func(sc library.Scope, q library.ListQuery) []string {
		t.Helper()
		q.Sort = library.SortTitle
		p, err := r.ListBooks(ctx, sc, q)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, b := range p.Books {
			out = append(out, b.Title)
		}
		return out
	}
	for _, c := range []struct {
		show, user string
		want       []string
	}{
		{"", listenOwner, []string{"Done", "Fresh", "Other", "Started", "Zero"}},
		{library.ShowUnfinished, listenOwner, []string{"Started"}},
		{library.ShowNotStarted, listenOwner, []string{"Fresh", "Other", "Zero"}},
		{library.ShowFinished, listenOwner, []string{"Done"}},
		{library.ShowUnfinished, listenMember, []string{"Other"}},
	} {
		if got := titles(allScope, library.ListQuery{Show: c.show, UserID: c.user}); !reflect.DeepEqual(got, c.want) {
			t.Errorf("show %q for %s = %v, want %v", c.show, c.user, got, c.want)
		}
	}
	// The other filters keep working with it, and a hidden book never shows.
	if got := titles(allScope, library.ListQuery{Show: library.ShowFinished, UserID: listenOwner, Genres: []string{"Mystery"}}); !reflect.DeepEqual(got, []string{"Done"}) {
		t.Errorf("show + genre = %v", got)
	}
	if got := titles(allScope, library.ListQuery{Show: library.ShowNotStarted, UserID: listenOwner, Genres: []string{"Mystery"}}); len(got) != 0 {
		t.Errorf("show not started + genre = %v", got)
	}
	if got := titles(listenScope(zero.ID), library.ListQuery{Show: library.ShowNotStarted, UserID: listenOwner}); !reflect.DeepEqual(got, []string{"Fresh", "Other"}) {
		t.Errorf("hidden book in a Show list: %v", got)
	}
	// Facets keep the filter too: Ann wrote all five, but only one is unfinished.
	fac, err := r.PeopleFacet(ctx, allScope, library.ListQuery{Show: library.ShowUnfinished, UserID: listenOwner}, library.RoleAuthor, 10)
	if err != nil || len(fac) != 1 || fac[0].Count != 1 {
		t.Errorf("author facet under Show = %+v, %v", fac, err)
	}
	// No user, or an unknown value, is refused.
	for _, q := range []library.ListQuery{{Show: library.ShowFinished}, {Show: "all", UserID: listenOwner}} {
		if _, err := r.ListBooks(ctx, allScope, q); !errors.Is(err, library.ErrInvalid) {
			t.Errorf("%+v: err = %v, want ErrInvalid", q, err)
		}
		if _, err := r.PeopleFacet(ctx, allScope, q, library.RoleAuthor, 10); !errors.Is(err, library.ErrInvalid) {
			t.Errorf("facet %+v: err = %v, want ErrInvalid", q, err)
		}
		if _, err := r.SeriesFacet(ctx, allScope, q, 10); !errors.Is(err, library.ErrInvalid) {
			t.Errorf("series facet %+v: err = %v, want ErrInvalid", q, err)
		}
	}
}
