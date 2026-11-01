package library_test

import (
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

func TestPersonDuration(t *testing.T) {
	r, _ := newRepo(t)
	_, f := librarytest.NewLibrary(t, r, "Main", "/audio")
	a := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "One", Authors: []string{"Ann Author"}, Narrators: []string{"Nan Narrator"}, DurationMs: 1000})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Two", Authors: []string{"Ann Author", "Bo Bee"}, Narrators: []string{"Nan Narrator"}, DurationMs: 2000})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Gone", Authors: []string{"Ann Author"}, DurationMs: 4000, MissingSince: 1})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Refused", Authors: []string{"Ann Author"}, DurationMs: 8000, Refused: library.RefusedDRM})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Other", Authors: []string{"Bo Bee"}, Narrators: []string{"Ann Author"}, DurationMs: 16000})

	people, err := r.BookPeople(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	var ann, nan string
	for _, p := range people {
		switch p.Name {
		case "Ann Author":
			ann = p.PersonID
		case "Nan Narrator":
			nan = p.PersonID
		}
	}
	for _, c := range []struct {
		name, id, role string
		sc             library.Scope
		want           int64
	}{
		{"author role, missing and refused left out", ann, library.RoleAuthor, allScope, 3000},
		{"narrator role of the same person", ann, library.RoleNarrator, allScope, 16000},
		{"narrator", nan, library.RoleNarrator, allScope, 3000},
		{"no author role", nan, library.RoleAuthor, allScope, 0},
		{"scope none", ann, library.RoleAuthor, noneScope, 0},
		{"unknown person", "ps_none", library.RoleAuthor, allScope, 0},
	} {
		got, err := r.PersonDuration(ctx, c.sc, c.id, c.role)
		if err != nil || got != c.want {
			t.Errorf("%s: %d, %v; want %d", c.name, got, err, c.want)
		}
	}
	if _, err := r.PersonDuration(ctx, allScope, ann, "editor"); err == nil {
		t.Error("an unknown role is accepted")
	}
}

func TestSeriesSequences(t *testing.T) {
	r, _ := newRepo(t)
	_, f := librarytest.NewLibrary(t, r, "Main", "/audio")
	a := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "A", Series: "Saga", Sequence: "1"})
	b := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "B", Series: "Saga", Sequence: "0.5"})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Gone", Series: "Saga", Sequence: "3", MissingSince: 1})
	links, err := r.BookSeries(ctx, a.ID)
	if err != nil || len(links) != 1 {
		t.Fatalf("%v %v", links, err)
	}
	got, err := r.SeriesSequences(ctx, allScope, links[0].SeriesID)
	if err != nil || len(got) != 2 || got[a.ID] != "1" || got[b.ID] != "0.5" {
		t.Errorf("got %v, %v", got, err)
	}
	if got, err := r.SeriesSequences(ctx, noneScope, links[0].SeriesID); err != nil || len(got) != 0 {
		t.Errorf("scope none: %v, %v", got, err)
	}
}
