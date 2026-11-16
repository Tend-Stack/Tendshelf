package db

import "testing"

// TestMigrationRemovalsFromPrevious: a v44 database keeps its users and uploads, the uploads gain
// add_anyway = 0 and no duplicate columns, and the two removal tables start empty.
func TestMigrationRemovalsFromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 44, dir)
	seed7(t, prev)
	if _, err := prev.Writer().Exec(`INSERT INTO uploads(id, library_id, folder_id, created_by, label, state, bytes_total, book_path, created_at, updated_at) VALUES ('up_1', 'lib_1', 'lf_1', 'us_1', 'A', 'placed', 1, '/audio/A', 1, 5)`); err != nil {
		t.Fatal(err)
	}
	users := countRows(t, prev.Reader(), "users")
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	if v := st.SchemaVersion(); v != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", v, SchemaVersion)
	}
	if got := countRows(t, st.Reader(), "users"); got != users || got == 0 {
		t.Errorf("users = %d, want %d", got, users)
	}
	var anyway int
	var dupBook, dupTier *string
	if err := st.Reader().QueryRow(`SELECT add_anyway, dup_book_id, dup_tier FROM uploads WHERE id = 'up_1'`).Scan(&anyway, &dupBook, &dupTier); err != nil {
		t.Fatalf("upload lost: %v", err)
	}
	if anyway != 0 || dupBook != nil || dupTier != nil {
		t.Errorf("upload columns = %d %v %v, want 0 NULL NULL", anyway, dupBook, dupTier)
	}
	for _, table := range []string{"removals", "removal_paths"} {
		if got := countRows(t, st.Reader(), table); got != 0 {
			t.Errorf("%s rows = %d, want 0", table, got)
		}
	}
	for _, ix := range []string{"removals_active_book", "removals_library"} {
		if n := indexCount(t, st, ix); n != 1 {
			t.Errorf("%s indexes = %d, want 1", ix, n)
		}
	}
}

// TestMigrationRemovalsFromEmpty: the checks hold, one removal per book may be active, and paths go with
// their removal.
func TestMigrationRemovalsFromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	w := st.Writer()
	for _, q := range []string{
		`INSERT INTO libraries(id, name, created_at) VALUES ('lib_1', 'Books', 1)`,
		`INSERT INTO library_folders(id, library_id, path, created_at) VALUES ('lf_1', 'lib_1', '/audio', 1)`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	hash := "'0000000000000000000000000000000000000000000000000000000000000000'"
	rm := `INSERT INTO removals(id, library_id, folder_id, book_id, title, root_path, mode, state, plan_hash, created_at, updated_at) VALUES `
	if _, err := w.Exec(rm + `('rmv_1', 'lib_1', 'lf_1', 'bk_1', 'A', '/audio/A', 'files', 'planned', ` + hash + `, 1, 1)`); err != nil {
		t.Fatalf("valid removal: %v", err)
	}
	for name, q := range map[string]string{
		"second active for a book": rm + `('rmv_2', 'lib_1', 'lf_1', 'bk_1', 'A', '/audio/A', 'files', 'moving', ` + hash + `, 1, 1)`,
		"unknown mode":             rm + `('rmv_3', 'lib_1', 'lf_1', 'bk_2', 'A', '/audio/A', 'delete', 'held', ` + hash + `, 1, 1)`,
		"unknown state":            rm + `('rmv_4', 'lib_1', 'lf_1', 'bk_2', 'A', '/audio/A', 'files', 'gone', ` + hash + `, 1, 1)`,
		"short hash":               rm + `('rmv_5', 'lib_1', 'lf_1', 'bk_2', 'A', '/audio/A', 'files', 'held', 'abc', 1, 1)`,
		"path kind":                `INSERT INTO removal_paths(removal_id, idx, kind, path, state) VALUES ('rmv_1', 0, 'link', '/audio/A/x', 'planned')`,
		"path state":               `INSERT INTO removal_paths(removal_id, idx, kind, path, state) VALUES ('rmv_1', 0, 'file', '/audio/A/x', 'moved')`,
		"add_anyway 2":             `INSERT INTO uploads(id, library_id, folder_id, created_by, label, state, bytes_total, add_anyway, created_at, updated_at) VALUES ('up_1', 'lib_1', 'lf_1', 'u', 'A', 'failed', 1, 2, 1, 1)`,
		"dup tier":                 `INSERT INTO uploads(id, library_id, folder_id, created_by, label, state, bytes_total, dup_tier, created_at, updated_at) VALUES ('up_2', 'lib_1', 'lf_1', 'u', 'A', 'failed', 1, 'maybe', 1, 1)`,
	} {
		if _, err := w.Exec(q); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	// A finished removal does not block a new one for the same book.
	if _, err := w.Exec(`UPDATE removals SET state = 'held' WHERE id = 'rmv_1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(rm + `('rmv_6', 'lib_1', 'lf_1', 'bk_1', 'A', '/audio/A', 'files', 'planned', ` + hash + `, 1, 1)`); err != nil {
		t.Fatalf("a new removal after a held one: %v", err)
	}
	if _, err := w.Exec(`INSERT INTO removal_paths(removal_id, idx, kind, path, state) VALUES ('rmv_1', 0, 'file', '/audio/A/x', 'planned')`); err != nil {
		t.Fatalf("valid path: %v", err)
	}
	if _, err := w.Exec(`DELETE FROM removals WHERE id = 'rmv_1'`); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, st.Reader(), "removal_paths"); got != 0 {
		t.Errorf("paths after their removal went = %d, want 0", got)
	}
}
