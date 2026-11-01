package librarytest_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

func TestSeedRoundTrip(t *testing.T) {
	ctx := context.Background()
	st, err := db.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	r := library.New(st)
	_, f := librarytest.NewLibrary(t, r, "Books", "/audio")

	b := librarytest.AddBook(t, r, f, librarytest.BookSpec{
		Title: "The Long Way", Authors: []string{"Ana Reyes", "Bo Lin"}, Narrators: []string{"Cy Dee"},
		Series: "Roads", Sequence: "2", Genres: []string{"Fantasy", "Travel"}, DurationMs: 5000, Files: 2,
		PublishedYear: 2020, MissingSince: 99,
	})
	got, err := r.Book(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "The Long Way" || got.SortTitle != "The Long Way" || got.MissingSince != 99 || got.PublishedYear != 2020 {
		t.Errorf("book = %+v", got)
	}
	people, err := r.BookPeople(ctx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, p := range people {
		names = append(names, p.Role+":"+p.Name)
	}
	if want := []string{"author:Ana Reyes", "author:Bo Lin", "narrator:Cy Dee"}; !reflect.DeepEqual(names, want) {
		t.Errorf("people = %v, want %v", names, want)
	}
	series, err := r.BookSeries(ctx, b.ID)
	if err != nil || len(series) != 1 || series[0].Name != "Roads" || series[0].Sequence != "2" {
		t.Errorf("series = %+v, %v", series, err)
	}
	labels, err := r.BookLabels(ctx, b.ID)
	if err != nil || len(labels[library.LabelGenre]) != 2 {
		t.Errorf("labels = %v, %v", labels, err)
	}
	files, err := r.AudioFiles(ctx, b.ID)
	if err != nil || len(files) != 2 || files[1].Size != 1000 || string(files[0].ProbeJSON) != "{}" {
		t.Errorf("files = %+v, %v", files, err)
	}

	u := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Fresh", Unsettled: true, Refused: library.RefusedDRM})
	ids, err := r.UnsettledAudioFileIDs(ctx, f.LibraryID)
	if err != nil || len(ids) != 1 {
		t.Errorf("unsettled = %v, %v", ids, err)
	}
	if got, _ := r.Book(ctx, u.ID); got.RefusedReason != library.RefusedDRM {
		t.Errorf("refused = %q", got.RefusedReason)
	}
}
