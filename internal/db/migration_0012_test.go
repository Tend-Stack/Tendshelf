package db

import (
	"context"
	"testing"
	"time"
)

// TestMigration0012AutoLookup: a v11 database keeps its rows and gains the empty lookup tables and a
// NULL auto_lookup_at; the marker table refuses an unknown outcome, goes with its book, and the
// usage table refuses an unknown provider and a negative count.
func TestMigration0012AutoLookup(t *testing.T) {
	ms := embedded(t)
	dir := t.TempDir()
	prev, err := open(context.Background(), dir, openConfig{migrations: ms[:11], supported: 11, now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	seed7(t, prev)
	prev.Close()
	st := mustOpen(t, dir)
	defer st.Close()
	for _, tbl := range []string{"libraries", "books", "metadata_values", "progress"} {
		if countRows(t, st.Reader(), tbl) != 1 {
			t.Errorf("%s lost its row", tbl)
		}
	}
	var at *int64
	if err := st.Reader().QueryRow(`SELECT auto_lookup_at FROM libraries`).Scan(&at); err != nil || at != nil {
		t.Errorf("auto_lookup_at = %v, %v; want NULL", at, err)
	}
	for _, tbl := range []string{"book_lookups", "online_usage"} {
		if n := countRows(t, st.Reader(), tbl); n != 0 {
			t.Errorf("%s has %d rows", tbl, n)
		}
	}
	w := st.Writer()
	if _, err := w.Exec(`UPDATE libraries SET auto_lookup_at = 0`); err == nil {
		t.Error("auto_lookup_at = 0 was accepted")
	}
	if _, err := w.Exec(`UPDATE libraries SET auto_lookup_at = 5`); err != nil {
		t.Errorf("auto_lookup_at = 5: %v", err)
	}
	if _, err := w.Exec(`INSERT INTO book_lookups(book_id, outcome, checked_at) VALUES ('bk_1', 'bogus', 1)`); err == nil {
		t.Error("an unknown outcome was accepted")
	}
	if _, err := w.Exec(`INSERT INTO book_lookups(book_id, outcome, checked_at) VALUES ('bk_1', 'matched', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(`INSERT INTO book_lookups(book_id, outcome, checked_at) VALUES ('bk_nope', 'matched', 1)`); err == nil {
		t.Error("a marker for an unknown book was accepted")
	}
	if _, err := w.Exec(`INSERT INTO online_usage VALUES (1, 'nowhere', 1)`); err == nil {
		t.Error("an unknown provider was accepted")
	}
	if _, err := w.Exec(`INSERT INTO online_usage VALUES (1, 'audnexus', -1)`); err == nil {
		t.Error("a negative count was accepted")
	}
	if _, err := w.Exec(`INSERT INTO online_usage VALUES (1, 'audnexus', 3)`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(`DELETE FROM books WHERE id = 'bk_1'`); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st.Reader(), "book_lookups"); n != 0 {
		t.Errorf("marker outlived its book (%d)", n)
	}
}

// TestMigration0012FromEmpty: a new database has the tables at the current version.
func TestMigration0012FromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	for _, tbl := range []string{"book_lookups", "online_usage"} {
		if n := countRows(t, st.Reader(), tbl); n != 0 {
			t.Errorf("%s has %d rows", tbl, n)
		}
	}
}
