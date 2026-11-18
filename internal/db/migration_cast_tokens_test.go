package db

import "testing"

// TestMigration0048CastTokens (the file is numbered 0047 until the orchestrator renumbers it): a v46
// database keeps its users, the table starts empty, and the checks refuse a short hash, an unknown
// kind and an expiry that is not after the creation.
func TestMigration0048CastTokens(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 46, dir)
	seed7(t, prev)
	users := countRows(t, prev.Reader(), "users")
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	if v := st.SchemaVersion(); v != SchemaVersion || v < 47 {
		t.Fatalf("schema version = %d, want %d", v, SchemaVersion)
	}
	if got := countRows(t, st.Reader(), "users"); got != users || got == 0 {
		t.Errorf("users = %d, want %d", got, users)
	}
	if got := countRows(t, st.Reader(), "cast_tokens"); got != 0 {
		t.Errorf("cast_tokens rows = %d, want 0", got)
	}
	w := st.Writer()
	if _, err := w.Exec(`INSERT INTO users(id, username, role, created_at) VALUES ('us_c', 'Cy', 'member', 1)`); err != nil {
		t.Fatal(err)
	}
	const ins = `INSERT INTO cast_tokens (token_hash, user_id, kind, target_id, created_at, expires_at) VALUES `
	for _, q := range []string{
		ins + `(randomblob(32), 'us_c', 'book', 'b1', 1, 2)`,
		ins + `(randomblob(32), 'us_c', 'episode', 'e1', 1, 2)`,
		ins + `(randomblob(32), 'us_c', 'station', 's1', 1, 2)`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for name, q := range map[string]string{
		"31-byte hash": ins + `(randomblob(31), 'us_c', 'book', 'b1', 1, 2)`,
		"unknown kind": ins + `(randomblob(32), 'us_c', 'file', 'b1', 1, 2)`,
		"expired":      ins + `(randomblob(32), 'us_c', 'book', 'b1', 5, 5)`,
		"no user":      ins + `(randomblob(32), 'us_zz', 'book', 'b1', 1, 2)`,
	} {
		if _, err := w.Exec(q); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
	if _, err := w.Exec(`DELETE FROM users WHERE id = 'us_c'`); err != nil {
		t.Fatal(err)
	}
	if got := countRows(t, st.Reader(), "cast_tokens"); got != 0 {
		t.Errorf("cast_tokens after user delete = %d, want 0", got)
	}
}
