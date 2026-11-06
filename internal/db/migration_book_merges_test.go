package db

import "testing"

// TestMigrationBookMergesFromPrevious: a v26 database keeps its rows and gains the two empty tables.
func TestMigrationBookMergesFromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 26, dir)
	seed7(t, prev)
	before := countRows(t, prev.Reader(), "progress")
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	if v := st.SchemaVersion(); v != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", v, SchemaVersion)
	}
	if got := countRows(t, st.Reader(), "progress"); got != before || got == 0 {
		t.Errorf("progress rows = %d, want %d", got, before)
	}
	for _, tbl := range []string{"merged_folders", "book_not_duplicates"} {
		if n := countRows(t, st.Reader(), tbl); n != 0 {
			t.Errorf("%s has %d rows, want 0", tbl, n)
		}
	}
}

// TestMigrationBookMergesFromEmpty: the pair is stored in order, a folder is remembered once, and both
// tables follow the kept book when it is deleted.
func TestMigrationBookMergesFromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	w := st.Writer()
	for _, q := range []string{
		`INSERT INTO libraries(id, name, created_at) VALUES ('lib_a', 'A', 1)`,
		`INSERT INTO library_folders(id, library_id, path, created_at) VALUES ('fol_a', 'lib_a', '/a', 1)`,
		`INSERT INTO books(id, library_id, folder_id, root_path, added_at, updated_at) VALUES ('bk_1', 'lib_a', 'fol_a', '/a/1', 1, 1)`,
		`INSERT INTO books(id, library_id, folder_id, root_path, added_at, updated_at) VALUES ('bk_2', 'lib_a', 'fol_a', '/a/2', 1, 1)`,
		`INSERT INTO merged_folders(root_path, library_id, from_id, to_id, merged_at) VALUES ('/a/3', 'lib_a', 'bk_3', 'bk_1', 5)`,
		`INSERT INTO book_not_duplicates(a_id, b_id, created_at) VALUES ('bk_1', 'bk_2', 5)`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := w.Exec(`INSERT INTO book_not_duplicates(a_id, b_id, created_at) VALUES ('bk_2', 'bk_1', 5)`); err == nil {
		t.Error("a pair stored out of order was accepted")
	}
	if _, err := w.Exec(`INSERT INTO merged_folders(root_path, library_id, from_id, to_id, merged_at) VALUES ('/a/3', 'lib_a', 'bk_4', 'bk_2', 6)`); err == nil {
		t.Error("a folder remembered twice was accepted")
	}
	if _, err := w.Exec(`DELETE FROM books WHERE id = 'bk_1'`); err != nil {
		t.Fatal(err)
	}
	if n := countRows(t, st.Reader(), "merged_folders") + countRows(t, st.Reader(), "book_not_duplicates"); n != 0 {
		t.Errorf("rows left after the kept book was deleted: %d", n)
	}
}
