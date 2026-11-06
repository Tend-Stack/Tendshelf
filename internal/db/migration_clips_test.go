package db

import "testing"

// TestMigrationClipsFromPrevious: a v27 database keeps its bookmark and it becomes a plain bookmark with no end.
func TestMigrationClipsFromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 27, dir)
	seed7(t, prev)
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	if v := st.SchemaVersion(); v != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", v, SchemaVersion)
	}
	var pos, note, kind = int64(0), "", ""
	var end *int64
	if err := st.Reader().QueryRow(`SELECT position_ms, note, end_ms, kind FROM bookmarks WHERE id = 'bm_1'`).Scan(&pos, &note, &end, &kind); err != nil {
		t.Fatal(err)
	}
	if pos != 5 || note != "n" || end != nil || kind != "bookmark" {
		t.Errorf("bookmark = %d %q %v %q", pos, note, end, kind)
	}
}

// TestMigrationClipsFromEmpty: the end must be after the position, and the kind must be on the list.
func TestMigrationClipsFromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	w := st.Writer()
	for _, q := range []string{
		`INSERT INTO users(id, username, role, created_at) VALUES ('us_1', 'Ana', 'owner', 1)`,
		`INSERT INTO libraries(id, name, created_at) VALUES ('lib_a', 'A', 1)`,
		`INSERT INTO library_folders(id, library_id, path, created_at) VALUES ('fol_a', 'lib_a', '/a', 1)`,
		`INSERT INTO books(id, library_id, folder_id, root_path, added_at, updated_at) VALUES ('bk_1', 'lib_a', 'fol_a', '/a/1', 1, 1)`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	ins := `INSERT INTO bookmarks(id, user_id, book_id, position_ms, end_ms, kind, created_at, updated_at) VALUES (?, 'us_1', 'bk_1', 100, ?, ?, 1, 1)`
	for _, c := range []struct {
		id   string
		end  any
		kind string
		ok   bool
	}{
		{"a", nil, "bookmark", true},
		{"b", 101, "quote", true},
		{"c", 100, "bookmark", false},
		{"d", 50, "bookmark", false},
		{"e", nil, "loud", false},
	} {
		_, err := w.Exec(ins, c.id, c.end, c.kind)
		if (err == nil) != c.ok {
			t.Errorf("%s: err = %v, want ok %v", c.id, err, c.ok)
		}
	}
}
