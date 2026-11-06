package db

import "testing"

// TestMigrationAppKeysFromPrevious: a v25 database keeps its device rows and hashes through the rebuild.
func TestMigrationAppKeysFromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 25, dir)
	seed7(t, prev)
	before := countRows(t, prev.Reader(), "devices")
	var hash []byte
	if err := prev.Reader().QueryRow(`SELECT token_hash FROM devices WHERE id = 'dv_1'`).Scan(&hash); err != nil || len(hash) != 32 {
		t.Fatalf("seed device hash = %d bytes, %v", len(hash), err)
	}
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	if v := st.SchemaVersion(); v != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", v, SchemaVersion)
	}
	if n := countRows(t, st.Reader(), "devices"); n != before || n == 0 {
		t.Fatalf("devices = %d, was %d", n, before)
	}
	var kept []byte
	var name, kind string
	if err := st.Reader().QueryRow(`SELECT token_hash, name, kind FROM devices WHERE id = 'dv_1'`).Scan(&kept, &name, &kind); err != nil {
		t.Fatal(err)
	}
	if string(kept) != string(hash) || name != "Phone" || kind != "mobile" {
		t.Errorf("device changed: %q %q", name, kind)
	}
	var idx int
	if err := st.Reader().QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name = 'devices_user'`).Scan(&idx); err != nil || idx != 1 {
		t.Errorf("devices_user index = %d, %v", idx, err)
	}
}

// TestMigrationAppKeysFromEmpty: the two new kinds insert, an unknown kind still fails.
func TestMigrationAppKeysFromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	w := st.Writer()
	if _, err := w.Exec(`INSERT INTO users(id, username, display_name, password_hash, role, created_at) VALUES ('us_k', 'kay', 'Kay', 'x', 'member', 1)`); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"web", "pwa", "desktop", "mobile", "opds", "koreader"} {
		if _, err := w.Exec(`INSERT INTO devices(id, user_id, name, kind, token_hash, created_at) VALUES (?, 'us_k', 'N', ?, randomblob(32), 1)`, "dv_"+k, k); err != nil {
			t.Errorf("kind %s refused: %v", k, err)
		}
	}
	if _, err := w.Exec(`INSERT INTO devices(id, user_id, name, kind, token_hash, created_at) VALUES ('dv_bad', 'us_k', 'N', 'toaster', randomblob(32), 1)`); err == nil {
		t.Error("kind toaster was accepted")
	}
}
