package db

import "testing"

// TestMigration0049CastTokensDevice: a v48 database keeps its cast token with device_id NULL; a station row may
// name a real device, a book row with a device and a row with an unknown device are refused, and deleting the
// device deletes its row (decision 0049).
func TestMigration0049CastTokensDevice(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 48, dir)
	seed7(t, prev)
	if _, err := prev.Writer().Exec(`INSERT INTO users(id, username, role, created_at) VALUES ('us_d', 'Di', 'member', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := prev.Writer().Exec(`INSERT INTO cast_tokens (token_hash, user_id, kind, target_id, created_at, expires_at)
		VALUES (randomblob(32), 'us_d', 'book', 'b1', 1, 2)`); err != nil {
		t.Fatal(err)
	}
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	if v := st.SchemaVersion(); v != SchemaVersion || v < 49 {
		t.Fatalf("schema version = %d, want %d", v, SchemaVersion)
	}
	var kept int
	if err := st.Reader().QueryRow(`SELECT count(*) FROM cast_tokens WHERE kind = 'book' AND target_id = 'b1' AND device_id IS NULL`).Scan(&kept); err != nil || kept != 1 {
		t.Fatalf("kept book row = %d, %v", kept, err)
	}
	w := st.Writer()
	if _, err := w.Exec(`INSERT INTO devices (id, user_id, name, kind, token_hash, created_at) VALUES ('dv_c9', 'us_d', 'Phone', 'mobile', randomblob(32), 1)`); err != nil {
		t.Fatal(err)
	}
	const ins = `INSERT INTO cast_tokens (token_hash, user_id, kind, target_id, device_id, created_at, expires_at) VALUES `
	if _, err := w.Exec(ins + `(randomblob(32), 'us_d', 'station', 's1', 'dv_c9', 1, 2)`); err != nil {
		t.Fatal(err)
	}
	for name, q := range map[string]string{
		"book with a device":  ins + `(randomblob(32), 'us_d', 'book', 'b1', 'dv_c9', 1, 2)`,
		"unknown device":      ins + `(randomblob(32), 'us_d', 'station', 's1', 'dv_zz', 1, 2)`,
		"episode with device": ins + `(randomblob(32), 'us_d', 'episode', 'e1', 'dv_c9', 1, 2)`,
	} {
		if _, err := w.Exec(q); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := w.Exec(`DELETE FROM devices WHERE id = 'dv_c9'`); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, st.Reader(), "cast_tokens"); got != 1 {
		t.Errorf("cast_tokens after device delete = %d, want 1 (the book row)", got)
	}
}

// TestMigration0049FromEmpty: a fresh database has the column and the partial index.
func TestMigration0049FromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	var n int
	if err := st.Reader().QueryRow(`SELECT count(*) FROM pragma_table_info('cast_tokens') WHERE name = 'device_id'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("device_id column = %d, %v", n, err)
	}
	if err := st.Reader().QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'index' AND name = 'cast_tokens_device'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("index = %d, %v", n, err)
	}
}
