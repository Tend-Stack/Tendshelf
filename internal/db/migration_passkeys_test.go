package db

import "testing"

// TestMigrationPasskeysFromPrevious: a v43 database keeps its users and sessions, the new handle
// column is NULL for everyone, and the two passkey tables start empty.
func TestMigrationPasskeysFromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 43, dir)
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
	var withHandle int
	if err := st.Reader().QueryRow(`SELECT count(*) FROM users WHERE passkey_handle IS NOT NULL`).Scan(&withHandle); err != nil || withHandle != 0 {
		t.Errorf("users with a handle = %d (%v), want 0", withHandle, err)
	}
	for _, table := range []string{"passkeys", "passkey_ceremonies"} {
		if got := countRows(t, st.Reader(), table); got != 0 {
			t.Errorf("%s rows = %d, want 0", table, got)
		}
	}
	for _, ix := range []string{"users_passkey_handle", "passkeys_user", "passkey_ceremonies_expires"} {
		if n := indexCount(t, st, ix); n != 1 {
			t.Errorf("%s indexes = %d, want 1", ix, n)
		}
	}
}

// TestMigrationPasskeysFromEmpty: a new database enforces the checks and uniqueness, and rows go
// with their user or session.
func TestMigrationPasskeysFromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	w := st.Writer()
	for _, q := range []string{
		`INSERT INTO users(id, username, role, created_at, passkey_handle) VALUES ('us_1', 'Ana', 'owner', 1, randomblob(32))`,
		`INSERT INTO users(id, username, role, created_at) VALUES ('us_2', 'Ben', 'member', 1)`,
		`INSERT INTO sessions(token_hash, user_id, created_at, expires_at, last_seen_at) VALUES (zeroblob(32), 'us_1', 1, 2, 1)`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	pk := `INSERT INTO passkeys(id, user_id, credential_id, rp_id, public_key, alg, sign_count, backup_eligible, backed_up, name, created_at) VALUES `
	cer := `INSERT INTO passkey_ceremonies(challenge_hash, purpose, rp_id, origin, user_id, session_hash, created_at, expires_at) VALUES `
	if _, err := w.Exec(pk + `('pk_1', 'us_1', zeroblob(16), 'a.example', zeroblob(32), -7, 0, 0, 0, 'Phone', 1)`); err != nil {
		t.Fatalf("valid passkey: %v", err)
	}
	for name, q := range map[string]string{
		"same handle twice":     `UPDATE users SET passkey_handle = (SELECT passkey_handle FROM users WHERE id = 'us_1') WHERE id = 'us_2'`,
		"short handle":          `UPDATE users SET passkey_handle = x'00' WHERE id = 'us_2'`,
		"same credential twice": pk + `('pk_2', 'us_2', zeroblob(16), 'a.example', zeroblob(32), -7, 0, 0, 0, 'Dup', 1)`,
		"short credential":      pk + `('pk_3', 'us_2', zeroblob(15), 'a.example', zeroblob(32), -7, 0, 0, 0, 'Short', 1)`,
		"unknown algorithm":     pk + `('pk_4', 'us_2', randomblob(16), 'a.example', zeroblob(32), -36, 0, 0, 0, 'Alg', 1)`,
		"negative counter":      pk + `('pk_5', 'us_2', randomblob(16), 'a.example', zeroblob(32), -7, -1, 0, 0, 'Count', 1)`,
		"empty name":            pk + `('pk_6', 'us_2', randomblob(16), 'a.example', zeroblob(32), -7, 0, 0, 0, '', 1)`,
		"backup flag 2":         pk + `('pk_7', 'us_2', randomblob(16), 'a.example', zeroblob(32), -7, 0, 2, 0, 'BE', 1)`,
		"register without user": cer + `(randomblob(32), 'register', 'a.example', 'https://a.example', NULL, NULL, 1, 2)`,
		"signin with session":   cer + `(randomblob(32), 'signin', 'a.example', 'https://a.example', 'us_1', zeroblob(32), 1, 2)`,
		"short challenge hash":  cer + `(x'00', 'signin', 'a.example', 'https://a.example', NULL, NULL, 1, 2)`,
		"unknown ceremony kind": cer + `(randomblob(32), 'other', 'a.example', 'https://a.example', NULL, NULL, 1, 2)`,
	} {
		if _, err := w.Exec(q); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := w.Exec(cer + `(randomblob(32), 'register', 'a.example', 'https://a.example', 'us_1', zeroblob(32), 1, 2)`); err != nil {
		t.Fatalf("valid register row: %v", err)
	}
	if _, err := w.Exec(cer + `(randomblob(32), 'signin', 'a.example', 'https://a.example', NULL, NULL, 1, 2)`); err != nil {
		t.Fatalf("valid signin row: %v", err)
	}
	if _, err := w.Exec(`DELETE FROM sessions`); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, st.Reader(), "passkey_ceremonies"); got != 1 {
		t.Errorf("ceremonies after the session went = %d, want 1 (the sign-in one)", got)
	}
	if _, err := w.Exec(`DELETE FROM users WHERE id = 'us_1'`); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, st.Reader(), "passkeys"); got != 0 {
		t.Errorf("passkeys after the user went = %d, want 0", got)
	}
}
