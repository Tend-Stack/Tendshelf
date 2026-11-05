package db

import (
	"strings"
	"testing"
)

// TestMigration0021FromPrevious: a v20 database keeps its books, all of them audio, and gains the new tables empty.
func TestMigration0021FromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 20, dir)
	seed7(t, prev)
	if _, err := prev.Writer().Exec(ebookFileSQL("ef_1", "library", "/audio/a/a.epub", "", "ready", "")); err != nil {
		t.Fatal(err)
	}
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	if v := st.SchemaVersion(); v != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", v, SchemaVersion)
	}
	var kind string
	if err := st.Reader().QueryRow(`SELECT kind FROM books WHERE id = 'bk_1'`).Scan(&kind); err != nil || kind != "audio" {
		t.Fatalf("kind = %q, %v; want audio", kind, err)
	}
	var merged any
	if err := st.Reader().QueryRow(`SELECT merged_from FROM ebook_files WHERE id = 'ef_1'`).Scan(&merged); err != nil || merged != nil {
		t.Fatalf("merged_from = %v, %v; want NULL", merged, err)
	}
	for _, tbl := range []string{"merged_books", "reading_positions", "reading_sessions"} {
		if !tableExists(t, st, tbl) || countRows(t, st.Reader(), tbl) != 0 {
			t.Errorf("table %s missing or not empty", tbl)
		}
	}
	if countRows(t, st.Reader(), "progress") != 1 || countRows(t, st.Reader(), "shelf_items") != 1 {
		t.Error("existing progress or shelf rows were lost")
	}
	fk, err := st.Reader().Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if fk.Next() {
		t.Error("foreign_key_check reports a violation")
	}
	fk.Close()
}

// TestMigration0021FromEmpty: the checks and keys of the new tables hold on a fresh database.
func TestMigration0021FromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	w := st.Writer()
	for _, q := range []string{
		`INSERT INTO users(id, username, role, created_at) VALUES ('us_1', 'Ana', 'owner', 1)`,
		`INSERT INTO libraries(id, name, created_at) VALUES ('lib_1', 'Books', 1)`,
		`INSERT INTO library_folders(id, library_id, path, created_at) VALUES ('lf_1', 'lib_1', '/audio', 1)`,
		`INSERT INTO books(id, library_id, folder_id, root_path, title, added_at, updated_at, kind) VALUES ('bk_1', 'lib_1', 'lf_1', '/audio/a.epub', 'A', 1, 1, 'text')`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	pos := func(rest string) string {
		return `INSERT INTO reading_positions(user_id, book_id, chapter_idx, char_pos, percent_bp, client_updated_at, server_updated_at, device_id` + rest
	}
	good := pos(`) VALUES ('us_1', 'bk_1', 1, 0, 5000, 1, 1, 'dev_1')`)
	if _, err := w.Exec(good); err != nil {
		t.Fatal(err)
	}
	for name, q := range map[string]string{
		"bad kind":      `INSERT INTO books(id, library_id, folder_id, root_path, title, added_at, updated_at, kind) VALUES ('bk_2', 'lib_1', 'lf_1', '/audio/b', 'B', 1, 1, 'video')`,
		"percent_bp":    pos(`) VALUES ('us_1', 'bk_1', 1, 0, 10001, 1, 1, 'dev_2')`),
		"duplicate key": good,
		"chapter 0":     pos(`) VALUES ('us_1', 'bk_1', 0, 0, 1, 1, 1, 'dev_3')`),
		"short sid":     pos(`, sid) VALUES ('us_1', 'bk_1', 1, 0, 1, 1, 1, 'dev_3', 'abc')`),
		"long quote":    pos(`, quote) VALUES ('us_1', 'bk_1', 1, 0, 1, 1, 1, 'dev_3', '` + strings.Repeat("x", 65) + `')`),
	} {
		if _, err := w.Exec(q); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// The alias and a text book's rows go with the book.
	if _, err := w.Exec(`INSERT INTO merged_books(from_id, to_id, merged_at) VALUES ('bk_old', 'bk_1', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(`DELETE FROM books WHERE id = 'bk_1'`); err != nil {
		t.Fatal(err)
	}
	if countRows(t, st.Reader(), "reading_positions") != 0 || countRows(t, st.Reader(), "merged_books") != 0 {
		t.Error("positions and aliases must go with the book")
	}
}
