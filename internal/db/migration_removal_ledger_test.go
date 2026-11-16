package db

import "testing"

// TestMigrationRemovalLedgerFromPrevious: a v45 database keeps its removals, which gain source 'library',
// and the two ledger tables start empty.
func TestMigrationRemovalLedgerFromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 45, dir)
	seed7(t, prev)
	hash := "'0000000000000000000000000000000000000000000000000000000000000000'"
	if _, err := prev.Writer().Exec(`INSERT INTO removals(id, library_id, folder_id, book_id, title, root_path, mode, state, plan_hash, created_at, updated_at) VALUES ('rmv_1', 'lib_1', 'lf_1', 'bk_1', 'A', '/audio/A', 'files', 'held', ` + hash + `, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	users := countRows(t, prev.Reader(), "users")
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	if v := st.SchemaVersion(); v != SchemaVersion || v < 46 {
		t.Fatalf("schema version = %d, want %d", v, SchemaVersion)
	}
	if got := countRows(t, st.Reader(), "users"); got != users || got == 0 {
		t.Errorf("users = %d, want %d", got, users)
	}
	var source, state string
	if err := st.Reader().QueryRow(`SELECT source, state FROM removals WHERE id = 'rmv_1'`).Scan(&source, &state); err != nil {
		t.Fatalf("removal lost: %v", err)
	}
	if source != "library" || state != "held" {
		t.Errorf("removal = %q %q, want library held", source, state)
	}
	for _, table := range []string{"removal_ledger", "removal_upload_books"} {
		if got := countRows(t, st.Reader(), table); got != 0 {
			t.Errorf("%s rows = %d, want 0", table, got)
		}
	}
}

// TestMigrationRemovalLedgerFromEmpty: an unknown source is refused, and the ledger and owner rows go
// with their removal.
func TestMigrationRemovalLedgerFromEmpty(t *testing.T) {
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
	rm := `INSERT INTO removals(id, library_id, folder_id, book_id, title, root_path, mode, state, source, plan_hash, created_at, updated_at) VALUES `
	if _, err := w.Exec(rm + `('rmv_1', 'lib_1', 'lf_1', 'bk_1', 'A', '/audio/A', 'files', 'held', 'upload', ` + hash + `, 1, 1)`); err != nil {
		t.Fatalf("valid upload removal: %v", err)
	}
	if _, err := w.Exec(rm + `('rmv_2', 'lib_1', 'lf_1', 'bk_2', 'A', '/audio/A', 'files', 'held', 'trash', ` + hash + `, 1, 1)`); err == nil {
		t.Error("source 'trash' was accepted")
	}
	if _, err := w.Exec(`INSERT INTO removal_ledger(removal_id, upload_path_id) VALUES ('rmv_1', 'upp_1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(`INSERT INTO removal_upload_books(removal_id, root_path, upload_id, library_id, added_by, created_at) VALUES ('rmv_1', '/audio/A', 'up_1', 'lib_1', 'us_1', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(`DELETE FROM removals WHERE id = 'rmv_1'`); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"removal_ledger", "removal_upload_books"} {
		if got := countRows(t, st.Reader(), table); got != 0 {
			t.Errorf("%s rows after their removal went = %d, want 0", table, got)
		}
	}
}
