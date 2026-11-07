package db

import "testing"

// TestMigrationSignInLinksFromPrevious: a v34 database keeps its users and sessions and gains an
// empty sign_in_links table with both indexes.
func TestMigrationSignInLinksFromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 34, dir)
	seed7(t, prev)
	users := countRows(t, prev.Reader(), "users")
	sessions := countRows(t, prev.Reader(), "sessions")
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	if v := st.SchemaVersion(); v != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", v, SchemaVersion)
	}
	if got := countRows(t, st.Reader(), "users"); got != users || got == 0 {
		t.Errorf("user rows = %d, want %d", got, users)
	}
	if got := countRows(t, st.Reader(), "sessions"); got != sessions {
		t.Errorf("session rows = %d, want %d", got, sessions)
	}
	if got := countRows(t, st.Reader(), "sign_in_links"); got != 0 {
		t.Errorf("sign_in_links rows = %d, want 0", got)
	}
	for _, ix := range []string{"sign_in_links_open", "sign_in_links_session"} {
		if n := indexCount(t, st, ix); n != 1 {
			t.Errorf("%s indexes = %d, want 1", ix, n)
		}
	}
}

// TestMigrationSignInLinksFromEmpty: a new database has both indexes, allows one open link per
// person, and clears session_hash when the session goes.
func TestMigrationSignInLinksFromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	for _, ix := range []string{"sign_in_links_open", "sign_in_links_session"} {
		if n := indexCount(t, st, ix); n != 1 {
			t.Errorf("%s indexes = %d, want 1", ix, n)
		}
	}
	w := st.Writer()
	for _, q := range []string{
		`INSERT INTO users(id, username, role, created_at) VALUES ('us_1', 'Ana', 'owner', 1)`,
		`INSERT INTO sessions(token_hash, user_id, created_at, expires_at, last_seen_at) VALUES (zeroblob(32), 'us_1', 1, 2, 1)`,
		`INSERT INTO sign_in_links (id, token_hash, user_id, created_at, expires_at) VALUES ('l1', randomblob(32), 'us_1', 1, 2)`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := w.Exec(`INSERT INTO sign_in_links (id, token_hash, user_id, created_at, expires_at) VALUES ('l2', randomblob(32), 'us_1', 1, 2)`); err == nil {
		t.Error("a second open link for one person was accepted")
	}
	if _, err := w.Exec(`INSERT INTO sign_in_links (id, token_hash, user_id, created_at, expires_at) VALUES ('l3', x'00', 'us_1', 1, 2)`); err == nil {
		t.Error("a short token hash was accepted")
	}
	if _, err := w.Exec(`UPDATE sign_in_links SET used_at = 1, session_hash = zeroblob(32) WHERE id = 'l1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(`DELETE FROM sessions`); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := st.Reader().QueryRow(`SELECT count(*) FROM sign_in_links WHERE id = 'l1' AND session_hash IS NULL`).Scan(&n); err != nil || n != 1 {
		t.Errorf("link with cleared session_hash = %d, %v", n, err)
	}
}
