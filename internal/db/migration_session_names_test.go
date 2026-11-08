package db

import (
	"strings"
	"testing"
)

// TestMigrationSessionNamesFromPrevious: a v36 database keeps its sessions, each with an empty name.
func TestMigrationSessionNamesFromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 36, dir)
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
	var named int
	if err := st.Reader().QueryRow(`SELECT count(*) FROM sessions WHERE name <> ''`).Scan(&named); err != nil || named != 0 {
		t.Errorf("named sessions = %d (err %v), want 0", named, err)
	}
}

// TestMigrationSessionNamesFromEmpty: a new database has the column.
func TestMigrationSessionNamesFromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	if _, err := st.Reader().Exec(`SELECT name FROM sessions LIMIT 1`); err != nil {
		t.Errorf("sessions.name: %v", err)
	}
}

// TestMigrationSessionNamesChecks: a name longer than 64 characters is refused by the table.
func TestMigrationSessionNamesChecks(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	if _, err := st.Writer().Exec(`INSERT INTO users(id, username, role, created_at) VALUES ('us_1', 'Ana', 'owner', 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Writer().Exec(`INSERT INTO sessions (token_hash, user_id, created_at, expires_at, last_seen_at, user_agent, name)
		VALUES (randomblob(32), 'us_1', 1, 2, 1, '', ?)`, strings.Repeat("a", 65)); err == nil {
		t.Error("a 65-character name was accepted")
	}
}
