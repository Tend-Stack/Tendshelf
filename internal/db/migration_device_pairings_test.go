package db

import "testing"

// TestMigrationDevicePairingsFromPrevious: a v32 database keeps its sessions and gains an empty
// device_pairings table with its index.
func TestMigrationDevicePairingsFromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 32, dir)
	seed7(t, prev)
	before := countRows(t, prev.Reader(), "sessions")
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	if v := st.SchemaVersion(); v != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", v, SchemaVersion)
	}
	if got := countRows(t, st.Reader(), "sessions"); got != before || got == 0 {
		t.Errorf("session rows = %d, want %d", got, before)
	}
	if got := countRows(t, st.Reader(), "device_pairings"); got != 0 {
		t.Errorf("device_pairings rows = %d, want 0", got)
	}
	if n := indexCount(t, st, "device_pairings_owner"); n != 1 {
		t.Errorf("device_pairings_owner indexes = %d, want 1", n)
	}
}

// TestMigrationDevicePairingsFromEmpty: a new database has the table and the index.
func TestMigrationDevicePairingsFromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	if n := indexCount(t, st, "device_pairings_owner"); n != 1 {
		t.Errorf("device_pairings_owner indexes = %d, want 1", n)
	}
}

// TestMigrationDevicePairingsChecks: the table refuses a wrong hash length, an unknown state and a
// match code that is not four characters, and a pairing goes with its session.
func TestMigrationDevicePairingsChecks(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	w := st.Writer()
	for _, q := range []string{
		`INSERT INTO users(id, username, role, created_at) VALUES ('us_1', 'Ana', 'owner', 1)`,
		`INSERT INTO sessions(token_hash, user_id, created_at, expires_at, last_seen_at) VALUES (zeroblob(32), 'us_1', 1, 2, 1)`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	bad := map[string]string{
		"short token hash": `INSERT INTO device_pairings (id, token_hash, owner_session, created_at, expires_at) VALUES ('p1', x'00', zeroblob(32), 1, 2)`,
		"unknown state":    `INSERT INTO device_pairings (id, token_hash, owner_session, state, created_at, expires_at) VALUES ('p2', randomblob(32), zeroblob(32), 'done', 1, 2)`,
		"short match":      `INSERT INTO device_pairings (id, token_hash, owner_session, match_code, created_at, expires_at) VALUES ('p3', randomblob(32), zeroblob(32), '12', 1, 2)`,
		"short claim":      `INSERT INTO device_pairings (id, token_hash, owner_session, claim_hash, created_at, expires_at) VALUES ('p4', randomblob(32), zeroblob(32), x'01', 1, 2)`,
		"no such session":  `INSERT INTO device_pairings (id, token_hash, owner_session, created_at, expires_at) VALUES ('p5', randomblob(32), randomblob(32), 1, 2)`,
	}
	for name, q := range bad {
		if _, err := w.Exec(q); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := w.Exec(`INSERT INTO device_pairings (id, token_hash, owner_session, created_at, expires_at) VALUES ('ok', randomblob(32), zeroblob(32), 1, 2)`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(`DELETE FROM sessions`); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, st.Reader(), "device_pairings"); got != 0 {
		t.Errorf("pairings after the session went = %d, want 0", got)
	}
}
