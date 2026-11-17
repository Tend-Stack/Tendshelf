package db

import (
	"strings"
	"testing"
)

// TestMigrationCustomProvidersFromPrevious: a v46 database keeps its users and gains an empty table.
func TestMigrationCustomProvidersFromPrevious(t *testing.T) {
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
	if got := countRows(t, st.Reader(), "custom_providers"); got != 0 {
		t.Errorf("custom_providers rows = %d, want 0", got)
	}
}

// TestMigrationCustomProvidersFromEmpty: the CHECKs refuse a bad scheme, a long name and a name that
// differs only in case.
func TestMigrationCustomProvidersFromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	w := st.Writer()
	ins := `INSERT INTO custom_providers(id, name, base_url, created_at) VALUES (?, ?, ?, 1)`
	if _, err := w.Exec(ins, "cp_1", "Mine", "https://books.example.org"); err != nil {
		t.Fatalf("valid row: %v", err)
	}
	for name, args := range map[string][]any{
		"ftp scheme":     {"cp_2", "Other", "ftp://x"},
		"long name":      {"cp_3", strings.Repeat("é", 41), "https://b.example.org"},
		"duplicate name": {"cp_4", "mine", "https://c.example.org"},
	} {
		if _, err := w.Exec(ins, args...); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}
}
