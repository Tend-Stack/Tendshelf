package db

import (
	"context"
	"testing"
	"time"
)

// TestMigration0014Uploads: a v13 database keeps its rows, the new column is NULL and the upload
// tables start empty.
func TestMigration0014Uploads(t *testing.T) {
	ms := embedded(t)
	dir := t.TempDir()
	prev, err := open(context.Background(), dir, openConfig{migrations: ms[:13], supported: 13, now: time.Now})
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
	for _, tbl := range []string{"uploads", "upload_files", "upload_paths"} {
		if n := countRows(t, st.Reader(), tbl); n != 0 {
			t.Errorf("%s has %d rows", tbl, n)
		}
	}
	var n int
	if err := st.Reader().QueryRow(`SELECT count(*) FROM libraries WHERE upload_folder_id IS NULL`).Scan(&n); err != nil || n != 1 {
		t.Errorf("upload_folder_id not NULL on the old row: %d, %v", n, err)
	}
}

// TestMigration0014FromEmpty: a new database has the tables and the column.
func TestMigration0014FromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	for _, tbl := range []string{"uploads", "upload_files", "upload_paths"} {
		if n := countRows(t, st.Reader(), tbl); n != 0 {
			t.Errorf("%s has %d rows", tbl, n)
		}
	}
	if _, err := st.Reader().Exec(`SELECT upload_folder_id FROM libraries`); err != nil {
		t.Errorf("column missing: %v", err)
	}
}

// TestMigration0014FolderSetNull: removing the chosen folder turns uploads off for the library.
func TestMigration0014FolderSetNull(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	seed3(t, st)
	w := st.Writer()
	var folder string
	if err := st.Reader().QueryRow(`SELECT id FROM library_folders LIMIT 1`).Scan(&folder); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(`UPDATE libraries SET upload_folder_id = ?`, folder); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(`DELETE FROM library_folders WHERE id = ?`, folder); err != nil {
		t.Skipf("fixture folder has restricting dependents: %v", err)
	}
	var n int
	if err := st.Reader().QueryRow(`SELECT count(*) FROM libraries WHERE upload_folder_id IS NULL`).Scan(&n); err != nil || n != 1 {
		t.Errorf("upload_folder_id not cleared: %d, %v", n, err)
	}
	if _, err := w.Exec(`UPDATE libraries SET upload_folder_id = 'lf_nope'`); err == nil {
		t.Error("unknown folder accepted")
	}
}

// TestMigration0014LedgerIndex: a removed path can be planned again; two live rows for one path cannot.
func TestMigration0014LedgerIndex(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	w := st.Writer()
	ins := func(id, state string) error {
		_, err := w.Exec(`INSERT INTO upload_paths(id, upload_id, library_folder_id, path, kind, state, created_at, updated_at)
VALUES (?, 'upl_1', 'lf_1', '/lib/A', 'dir', ?, 1, 1)`, id, state)
		return err
	}
	if err := ins("upp_1", "removed"); err != nil {
		t.Fatal(err)
	}
	if err := ins("upp_2", "planned"); err != nil {
		t.Fatalf("a removed path cannot be planned again: %v", err)
	}
	if err := ins("upp_3", "present"); err == nil {
		t.Error("two live rows for one path were accepted")
	}
	if err := ins("upp_4", "kept"); err != nil {
		t.Errorf("kept rows are not live: %v", err)
	}
	if err := ins("upp_5", "bogus"); err == nil {
		t.Error("bad state accepted")
	}
}
