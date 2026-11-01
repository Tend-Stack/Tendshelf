package library_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

func newCover(bookID string, sha byte) library.Cover {
	return library.Cover{
		BookID: bookID, Source: library.CoverFolder, Mime: "image/jpeg", Width: 600, Height: 600,
		SHA256: hash(sha), Path: "covers/" + hash(sha) + ".jpg", Tint: "#2c6660",
		Accent: json.RawMessage(`{"dark":"#9ac9c2","light":"#35615c"}`),
	}
}

func changes(t *testing.T, r *library.Repo, fn func(*library.Tx) error) {
	t.Helper()
	if err := r.Update(ctx, fn); err != nil {
		t.Fatal(err)
	}
}

func TestCoverCRUD(t *testing.T) {
	r, st := newRepo(t)
	lib, f := fixture(t, r)
	book := addBook(t, r, lib, f, "/audio/One")
	other := addBook(t, r, lib, f, "/audio/Two")

	c := newCover(book.ID, 'a')
	changes(t, r, func(tx *library.Tx) error { return tx.PutCover(&c) })
	if !strings.HasPrefix(c.ID, "cv_") || c.CreatedAt == 0 {
		t.Fatalf("PutCover left ID %q, CreatedAt %d", c.ID, c.CreatedAt)
	}
	got, err := r.Cover(ctx, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Tint != "#2c6660" || string(got.Accent) != string(c.Accent) || got.Width != 600 || got.Source != library.CoverFolder {
		t.Errorf("Cover = %+v", got)
	}
	if list, err := r.Covers(ctx, book.ID); err != nil || len(list) != 1 {
		t.Fatalf("Covers = %v, %v", list, err)
	}
	if _, err := r.Cover(ctx, "cv_missing"); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("missing cover: %v", err)
	}

	// Same book and hash: an update keeps ID and CreatedAt.
	up := newCover(book.ID, 'a')
	up.Source, up.Tint, up.Accent = library.CoverEmbedded, "", nil
	changes(t, r, func(tx *library.Tx) error { return tx.PutCover(&up) })
	if up.ID != c.ID || up.CreatedAt != c.CreatedAt {
		t.Errorf("upsert changed identity: %+v vs %+v", up, c)
	}
	got, _ = r.Cover(ctx, c.ID)
	if got.Source != library.CoverEmbedded || got.Tint != "" || got.Accent != nil {
		t.Errorf("after update: %+v", got)
	}

	// The same picture on another book is its own row; CoversBySHA finds both.
	o := newCover(other.ID, 'a')
	changes(t, r, func(tx *library.Tx) error { return tx.PutCover(&o) })
	if o.ID == c.ID {
		t.Error("two books share one cover row")
	}
	if list, err := r.CoversBySHA(ctx, hash('a')); err != nil || len(list) != 2 {
		t.Errorf("CoversBySHA = %v, %v", list, err)
	}

	// Putting an identical row writes nothing.
	var before, after int64
	st.Writer().QueryRow(`SELECT total_changes()`).Scan(&before)
	same := newCover(other.ID, 'a')
	changes(t, r, func(tx *library.Tx) error { return tx.PutCover(&same) })
	st.Writer().QueryRow(`SELECT total_changes()`).Scan(&after)
	if after != before || same.ID != o.ID {
		t.Errorf("identical PutCover changed %d rows, ID %s vs %s", after-before, same.ID, o.ID)
	}

	// Cover selection; the other book's cover is refused; clearing works; updated_at stays.
	changes(t, r, func(tx *library.Tx) error { return tx.SetBookCover(book.ID, c.ID) })
	b, _ := r.Book(ctx, book.ID)
	if b.CoverID != c.ID || b.UpdatedAt != book.UpdatedAt {
		t.Errorf("book after SetBookCover: cover %q, updated %d (was %d)", b.CoverID, b.UpdatedAt, book.UpdatedAt)
	}
	err = r.Update(ctx, func(tx *library.Tx) error { return tx.SetBookCover(book.ID, o.ID) })
	if !errors.Is(err, library.ErrInvalid) {
		t.Errorf("another book's cover: %v", err)
	}
	err = r.Update(ctx, func(tx *library.Tx) error { return tx.SetBookCover("bk_nope", "") })
	if !errors.Is(err, library.ErrNotFound) {
		t.Errorf("missing book: %v", err)
	}

	// Deleting the cover in use clears the book's cover.
	changes(t, r, func(tx *library.Tx) error { return tx.DeleteCover(c.ID) })
	b, _ = r.Book(ctx, book.ID)
	if b.CoverID != "" {
		t.Errorf("cover_id = %q after delete", b.CoverID)
	}
	if err := r.Update(ctx, func(tx *library.Tx) error { return tx.DeleteCover(c.ID) }); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("second delete: %v", err)
	}
	changes(t, r, func(tx *library.Tx) error { return tx.SetBookCover(other.ID, o.ID) })
	changes(t, r, func(tx *library.Tx) error { return tx.SetBookCover(other.ID, "") })
	if b, _ = r.Book(ctx, other.ID); b.CoverID != "" {
		t.Errorf("cleared cover_id = %q", b.CoverID)
	}
}

func TestCoverValidation(t *testing.T) {
	r, _ := newRepo(t)
	lib, f := fixture(t, r)
	book := addBook(t, r, lib, f, "/audio/One")
	for name, mod := range map[string]func(*library.Cover){
		"no book":     func(c *library.Cover) { c.BookID = "" },
		"bad source":  func(c *library.Cover) { c.Source = "magic" },
		"bad mime":    func(c *library.Cover) { c.Mime = "image/gif" },
		"no size":     func(c *library.Cover) { c.Width = 0 },
		"short sha":   func(c *library.Cover) { c.SHA256 = "abc" },
		"no path":     func(c *library.Cover) { c.Path = "" },
		"upper tint":  func(c *library.Cover) { c.Tint = "#2C6660" },
		"short tint":  func(c *library.Cover) { c.Tint = "#fff" },
		"bad accents": func(c *library.Cover) { c.Accent = json.RawMessage(`{nope`) },
	} {
		c := newCover(book.ID, 'b')
		mod(&c)
		if err := r.Update(ctx, func(tx *library.Tx) error { return tx.PutCover(&c) }); !errors.Is(err, library.ErrInvalid) {
			t.Errorf("%s: err = %v, want ErrInvalid", name, err)
		}
	}
	c := newCover("bk_missing", 'b')
	if err := r.Update(ctx, func(tx *library.Tx) error { return tx.PutCover(&c) }); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("missing book: %v", err)
	}
}

func TestCoversCascadeWithBook(t *testing.T) {
	r, st := newRepo(t)
	lib, f := fixture(t, r)
	book := addBook(t, r, lib, f, "/audio/One")
	c := newCover(book.ID, 'c')
	changes(t, r, func(tx *library.Tx) error { return tx.PutCover(&c) })
	changes(t, r, func(tx *library.Tx) error { _, err := tx.DeleteEmptyBook(book.ID); return err })
	var n int
	if err := st.Reader().QueryRow(`SELECT count(*) FROM covers`).Scan(&n); err != nil || n != 0 {
		t.Errorf("covers left after the book went: %d, %v", n, err)
	}
}
