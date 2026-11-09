package db

import "testing"

// TestMigrationOIDCFromPrevious: a v38 database keeps its users and sessions and gains the two
// empty OpenID Connect tables with their indexes.
func TestMigrationOIDCFromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 38, dir)
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
	for _, table := range []string{"oidc_identities", "oidc_logins"} {
		if got := countRows(t, st.Reader(), table); got != 0 {
			t.Errorf("%s rows = %d, want 0", table, got)
		}
	}
	for _, ix := range []string{"oidc_identities_user", "oidc_logins_expires"} {
		if n := indexCount(t, st, ix); n != 1 {
			t.Errorf("%s indexes = %d, want 1", ix, n)
		}
	}
}

// TestMigrationOIDCFromEmpty: a new database enforces the uniqueness and the link-row CHECK, and
// the rows go with their user or session.
func TestMigrationOIDCFromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	w := st.Writer()
	nonce := "0123456789012345678901234567890123456789012"
	for _, q := range []string{
		`INSERT INTO users(id, username, role, created_at) VALUES ('us_1', 'Ana', 'owner', 1)`,
		`INSERT INTO users(id, username, role, created_at) VALUES ('us_2', 'Ben', 'member', 1)`,
		`INSERT INTO sessions(token_hash, user_id, created_at, expires_at, last_seen_at) VALUES (zeroblob(32), 'us_1', 1, 2, 1)`,
		`INSERT INTO oidc_identities(issuer, subject, user_id, linked_via, created_at) VALUES ('https://i', 's1', 'us_1', 'account', 1)`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	cols := `INSERT INTO oidc_logins(id, state_hash, purpose, issuer, nonce, verifier, user_id, session_hash, created_at, expires_at) VALUES `
	for name, q := range map[string]string{
		"same subject twice":       `INSERT INTO oidc_identities(issuer, subject, user_id, linked_via, created_at) VALUES ('https://i', 's1', 'us_2', 'account', 1)`,
		"two identities per user":  `INSERT INTO oidc_identities(issuer, subject, user_id, linked_via, created_at) VALUES ('https://i', 's2', 'us_1', 'account', 1)`,
		"bad linked_via":           `INSERT INTO oidc_identities(issuer, subject, user_id, linked_via, created_at) VALUES ('https://i', 's3', 'us_2', 'magic', 1)`,
		"link row without session": cols + `('l1', randomblob(32), 'link', 'https://i', '` + nonce + `', '` + nonce + `', NULL, NULL, 1, 2)`,
		"signin row with session":  cols + `('l2', randomblob(32), 'signin', 'https://i', '` + nonce + `', '` + nonce + `', 'us_1', zeroblob(32), 1, 2)`,
		"short state hash":         cols + `('l3', x'00', 'signin', 'https://i', '` + nonce + `', '` + nonce + `', NULL, NULL, 1, 2)`,
	} {
		if _, err := w.Exec(q); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := w.Exec(cols + `('l4', randomblob(32), 'link', 'https://i', '` + nonce + `', '` + nonce + `', 'us_1', zeroblob(32), 1, 2)`); err != nil {
		t.Fatalf("valid link row: %v", err)
	}
	if _, err := w.Exec(`DELETE FROM sessions`); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, st.Reader(), "oidc_logins"); got != 0 {
		t.Errorf("link rows after the session went = %d, want 0", got)
	}
	if _, err := w.Exec(`DELETE FROM users WHERE id = 'us_1'`); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, st.Reader(), "oidc_identities"); got != 0 {
		t.Errorf("identities after the user went = %d, want 0", got)
	}
}
