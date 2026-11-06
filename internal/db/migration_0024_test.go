package db

import "testing"

const bookSettingsInsert = `INSERT INTO book_settings(user_id, book_id, speed, updated_at) VALUES (?, ?, ?, ?)`

// TestMigration0024FromPrevious: a v23 database keeps its rows and gains an empty book_settings.
func TestMigration0024FromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 23, dir)
	seed7(t, prev)
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	if v := st.SchemaVersion(); v != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", v, SchemaVersion)
	}
	for _, tbl := range []string{"libraries", "books", "progress"} {
		if countRows(t, st.Reader(), tbl) != 1 {
			t.Errorf("%s lost its row", tbl)
		}
	}
	if n := countRows(t, st.Reader(), "book_settings"); n != 0 {
		t.Errorf("book_settings has %d rows", n)
	}
	if _, err := st.Writer().Exec(bookSettingsInsert, "us_1", "bk_1", 1.5, 1); err != nil {
		t.Fatal(err)
	}
}

// TestMigration0024FromEmpty: one row per (person, book), a speed in range, and rows leave with the book.
func TestMigration0024FromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	seed7(t, st)
	w := st.Writer()
	if _, err := w.Exec(bookSettingsInsert, "us_1", "bk_1", 1.25, 1); err != nil {
		t.Fatal(err)
	}
	for name, args := range map[string][]any{
		"duplicate (user, book)": {"us_1", "bk_1", 1.5, 1},
		"unknown book":           {"us_1", "bk_nope", 1.5, 1},
		"unknown user":           {"us_nope", "bk_1", 1.5, 1},
		"speed too low":          {"us_1", "bk_1", 0.4, 1},
		"speed too high":         {"us_1", "bk_1", 3.1, 1},
		"updated_at = 0":         {"us_1", "bk_1", 1.5, 0},
	} {
		if _, err := w.Exec(bookSettingsInsert, args...); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := w.Exec(`DELETE FROM books WHERE id = 'bk_1'`); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st.Reader(), "book_settings"); n != 0 {
		t.Errorf("the book's speed stayed: %d rows", n)
	}
}
