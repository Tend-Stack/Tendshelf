package db

import "testing"

// TestMigrationListeningRecentFromPrevious: a v29 database keeps its sessions and gains the index.
func TestMigrationListeningRecentFromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 29, dir)
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
	if n := indexCount(t, st, "listening_sessions_recent"); n != 1 {
		t.Errorf("listening_sessions_recent indexes = %d, want 1", n)
	}
}

// TestMigrationListeningRecentFromEmpty: a new database has the index.
func TestMigrationListeningRecentFromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	if n := indexCount(t, st, "listening_sessions_recent"); n != 1 {
		t.Errorf("listening_sessions_recent indexes = %d, want 1", n)
	}
}

func indexCount(t *testing.T, st *Store, name string) int {
	t.Helper()
	var n int
	if err := st.Reader().QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}
