package db

import "testing"

// TestMigrationRadioHLSFromPrevious: a v41 database keeps its stations and they read as 'icy'.
func TestMigrationRadioHLSFromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 41, dir)
	if _, err := prev.Writer().Exec(`INSERT INTO radio_stations(id, stream_url, name, created_at, updated_at) VALUES ('rs_1', 'https://a.example/live', 'One', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	if v := st.SchemaVersion(); v != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", v, SchemaVersion)
	}
	var name, format string
	if err := st.Reader().QueryRow(`SELECT name, format FROM radio_stations WHERE id = 'rs_1'`).Scan(&name, &format); err != nil {
		t.Fatal(err)
	}
	if name != "One" || format != "icy" {
		t.Errorf("row = %q %q, want One icy", name, format)
	}
}

// TestMigrationRadioHLSFromEmpty: a new database accepts 'hls' and refuses any other format.
func TestMigrationRadioHLSFromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	w := st.Writer()
	if _, err := w.Exec(`INSERT INTO radio_stations(id, stream_url, name, format, created_at, updated_at) VALUES ('rs_1', 'https://a.example/a.m3u8', 'A', 'hls', 1, 1)`); err != nil {
		t.Fatalf("hls refused: %v", err)
	}
	if _, err := w.Exec(`INSERT INTO radio_stations(id, stream_url, name, format, created_at, updated_at) VALUES ('rs_2', 'https://a.example/b', 'B', 'dash', 1, 1)`); err == nil {
		t.Error("format 'dash' was accepted")
	}
	var f string
	if err := st.Reader().QueryRow(`SELECT format FROM radio_stations WHERE id='rs_1'`).Scan(&f); err != nil || f != "hls" {
		t.Errorf("format = %q, %v", f, err)
	}
}
