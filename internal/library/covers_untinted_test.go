package library_test

import (
	"slices"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

// TestBooksWithUntintedCover: only a present book whose current cover came from the scan and has no
// tint is listed; a tinted cover, an upload and a book of another library are not.
func TestBooksWithUntintedCover(t *testing.T) {
	r, _ := newRepo(t)
	lib, f := fixture(t, r)
	plain := addBook(t, r, lib, f, "/audio/Plain")
	embedded := addBook(t, r, lib, f, "/audio/Embedded")
	tinted := addBook(t, r, lib, f, "/audio/Tinted")
	upload := addBook(t, r, lib, f, "/audio/Upload")
	addBook(t, r, lib, f, "/audio/NoCover") // no cover at all

	for i, c := range []struct {
		book   library.Book
		source string
		tint   string
	}{
		{plain, library.CoverFolder, ""},
		{embedded, library.CoverEmbedded, ""},
		{tinted, library.CoverFolder, "#2c6660"},
		{upload, library.CoverUpload, ""},
	} {
		cv := newCover(c.book.ID, byte('a'+i))
		cv.Source, cv.Tint, cv.Accent = c.source, c.tint, nil
		changes(t, r, func(tx *library.Tx) error {
			if err := tx.PutCover(&cv); err != nil {
				return err
			}
			return tx.SetBookCover(c.book.ID, cv.ID)
		})
	}

	got, err := r.BooksWithUntintedCover(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{plain.ID, embedded.ID}
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Errorf("BooksWithUntintedCover = %v, want %v", got, want)
	}
	if got, err := r.BooksWithUntintedCover(ctx, "lb_other"); err != nil || len(got) != 0 {
		t.Errorf("another library: %v, %v", got, err)
	}
}
