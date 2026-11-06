package db

import "testing"

// TestMigration0025FromPrevious: a v24 database keeps its libraries; each gains watch on, and a library
// that had no schedule gets six hours while one with its own interval keeps it.
func TestMigration0025FromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 24, dir)
	seed7(t, prev)
	if _, err := prev.Writer().Exec(`INSERT INTO libraries(id, name, scan_interval_min, created_at) VALUES ('lib_own', 'Own', 90, 1)`); err != nil {
		t.Fatal(err)
	}
	before := countRows(t, prev.Reader(), "libraries")
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	if v := st.SchemaVersion(); v != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", v, SchemaVersion)
	}
	var n, off, six, ninety int
	if err := st.Reader().QueryRow(`SELECT count(*), sum(watch = 0), sum(scan_interval_min = 360), sum(scan_interval_min = 90) FROM libraries`).Scan(&n, &off, &six, &ninety); err != nil {
		t.Fatal(err)
	}
	if n != before || off != 0 || ninety != 1 || six != n-1 {
		t.Errorf("libraries = %d (was %d), watch off = %d, six hours = %d, ninety = %d", n, before, off, six, ninety)
	}
	if countRows(t, st.Reader(), "progress") != 1 {
		t.Error("existing progress row was lost")
	}
}

// TestMigration0025FromEmpty: a new library row defaults to watching, and the watch flag is 0 or 1 only.
func TestMigration0025FromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	if _, err := st.Writer().Exec(`INSERT INTO libraries(id, name, created_at) VALUES ('lib_a', 'A', 1)`); err != nil {
		t.Fatal(err)
	}
	var w int
	if err := st.Reader().QueryRow(`SELECT watch FROM libraries WHERE id = 'lib_a'`).Scan(&w); err != nil || w != 1 {
		t.Fatalf("watch = %d, %v; want 1", w, err)
	}
	if _, err := st.Writer().Exec(`UPDATE libraries SET watch = 2`); err == nil {
		t.Error("watch = 2 was accepted")
	}
}
