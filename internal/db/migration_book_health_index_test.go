package db

import (
	"strings"
	"testing"
)

const healthFolderQuery = `SELECT 1 FROM books p WHERE p.folder_id = 'fo_1' AND p.missing_since IS NULL`

func planOf(t *testing.T, st *Store, q string) string {
	t.Helper()
	rows, err := st.Reader().Query(`EXPLAIN QUERY PLAN ` + q)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		out = append(out, detail)
	}
	return strings.Join(out, " | ")
}

// TestMigrationBookHealthIndexFromPrevious: a v31 database keeps its books and gains the folder index, which
// the "is any book of this folder present" question of Library health now uses.
func TestMigrationBookHealthIndexFromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 31, dir)
	seed7(t, prev)
	before := countRows(t, prev.Reader(), "books")
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	if v := st.SchemaVersion(); v != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", v, SchemaVersion)
	}
	if got := countRows(t, st.Reader(), "books"); got != before {
		t.Errorf("book rows = %d, want %d", got, before)
	}
	if n := indexCount(t, st, "books_folder_present"); n != 1 {
		t.Errorf("books_folder_present indexes = %d, want 1", n)
	}
	if plan := planOf(t, st, healthFolderQuery); !strings.Contains(plan, "books_folder_present") {
		t.Errorf("plan = %q, want it to use books_folder_present", plan)
	}
}

// TestMigrationBookHealthIndexFromEmpty: a new database has the index and uses it.
func TestMigrationBookHealthIndexFromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	if n := indexCount(t, st, "books_folder_present"); n != 1 {
		t.Errorf("books_folder_present indexes = %d, want 1", n)
	}
	if plan := planOf(t, st, healthFolderQuery); !strings.Contains(plan, "books_folder_present") {
		t.Errorf("plan = %q, want it to use books_folder_present", plan)
	}
}
