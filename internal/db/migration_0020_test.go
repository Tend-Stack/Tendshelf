package db

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// openUpTo opens dir with the first n migrations only. Tests of an older migration use it because later ones
// (0020 drops the alignment tables) change what they check.
func openUpTo(t *testing.T, n int, dir string) *Store {
	t.Helper()
	st, err := open(context.Background(), dir, openConfig{migrations: embedded(t)[:n], supported: n, now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func tableExists(t *testing.T, st *Store, name string) bool {
	t.Helper()
	var n int
	if err := st.Reader().QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = ?`, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

// TestMigration0020FromPrevious: a v19 database with a timed highlight and align jobs keeps what is the person's
// (quote, note, colour, offsets), loses the audio times and the three alignment tables, loses the align jobs
// (nothing is left to run them) and keeps every other job.
func TestMigration0020FromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 19, dir)
	seed7(t, prev)
	for _, q := range []string{
		ebookFileSQL("ef_1", "library", "/audio/a/a.epub", "", "ready", ""),
		alSeedEdition,
		alComponent,
		alInsert("al_1", "done"),
		hlInsert("hl_1", ", edition_id, start_ms, end_ms, alignment_id, note, colour",
			", 'ed_1', 1000, 2500, 'al_1', 'a thought', 'green'"),
		hlInsert("hl_2", "", ""),
		`INSERT INTO jobs(id, kind, state, created_at, updated_at) VALUES ('jb_q', 'align.book', 'queued', 1, 1)`,
		`INSERT INTO jobs(id, kind, state, lease_until, attempts, created_at, updated_at) VALUES ('jb_r', 'align.book', 'running', 9999999999999, 1, 1, 1)`,
		`INSERT INTO jobs(id, kind, state, created_at, updated_at) VALUES ('jb_c', 'align.check', 'done', 1, 1)`,
		`INSERT INTO jobs(id, kind, state, created_at, updated_at) VALUES ('jb_i', 'ebook.ingest', 'queued', 1, 1)`,
	} {
		if _, err := prev.Writer().Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	prev.Close()

	st := openUpTo(t, 20, dir)
	defer st.Close()
	if v := st.SchemaVersion(); v != 20 {
		t.Fatalf("schema version = %d, want 20", v)
	}
	var quote, note, colour, sid string
	var off int
	var ed sql.NullString
	err := st.Reader().QueryRow(`SELECT quote, note, colour, start_sid, end_off, edition_id FROM highlights WHERE id = 'hl_1'`).Scan(&quote, &note, &colour, &sid, &off, &ed)
	if err != nil || quote != "Hello" || note != "a thought" || colour != "green" || sid != "c1.p1.s0" || off != 5 || ed.String != "ed_1" {
		t.Fatalf("highlight after = %q %q %q %q %d %v, %v", quote, note, colour, sid, off, ed, err)
	}
	if n := countRows(t, st.Reader(), "highlights"); n != 2 {
		t.Fatalf("highlights = %d, want 2", n)
	}
	cols, err := st.Reader().Query(`SELECT name FROM pragma_table_info('highlights')`)
	if err != nil {
		t.Fatal(err)
	}
	for cols.Next() {
		var name string
		if err := cols.Scan(&name); err != nil {
			t.Fatal(err)
		}
		if name == "start_ms" || name == "end_ms" || name == "alignment_id" {
			t.Errorf("highlights still has column %s", name)
		}
	}
	cols.Close()
	for _, tbl := range []string{"alignments", "alignment_chapters", "runtime_components"} {
		if tableExists(t, st, tbl) {
			t.Errorf("table %s still exists", tbl)
		}
	}
	var left []string
	rows, err := st.Reader().Query(`SELECT id FROM jobs ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		left = append(left, id)
	}
	rows.Close()
	if strings.Join(left, ",") != "jb_1,jb_i" {
		t.Errorf("jobs left = %v, want the seeded job and the ebook.ingest job", left)
	}
	fk, err := st.Reader().Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	if fk.Next() {
		t.Error("foreign_key_check reports a violation")
	}
	fk.Close()
	// The rebuilt table keeps its rules: a forgotten edition leaves the mark, a deleted user removes it.
	if _, err := st.Writer().Exec(`DELETE FROM editions`); err != nil {
		t.Fatal(err)
	}
	if err := st.Reader().QueryRow(`SELECT quote, edition_id FROM highlights WHERE id = 'hl_1'`).Scan(&quote, &ed); err != nil || quote != "Hello" || ed.Valid {
		t.Errorf("highlight after edition delete = %q %v, %v", quote, ed, err)
	}
	if _, err := st.Writer().Exec(`DELETE FROM users WHERE id = 'us_1'`); err != nil {
		t.Fatal(err)
	}
	if countRows(t, st.Reader(), "highlights") != 0 {
		t.Error("highlights must go with the user")
	}
}

// TestMigration0020FromEmpty: a fresh database has the highlights table with its checks and keys, and no alignment tables.
func TestMigration0020FromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	w := st.Writer()
	for _, q := range []string{
		`INSERT INTO users(id, username, role, created_at) VALUES ('us_1', 'Ana', 'owner', 1)`,
		`INSERT INTO libraries(id, name, created_at) VALUES ('lib_1', 'Books', 1)`,
		`INSERT INTO library_folders(id, library_id, path, created_at) VALUES ('lf_1', 'lib_1', '/audio', 1)`,
		`INSERT INTO books(id, library_id, folder_id, root_path, title, added_at, updated_at) VALUES ('bk_1', 'lib_1', 'lf_1', '/audio/a', 'A', 1, 1)`,
		hlInsert("hl_1", ", client_id", ", 'c1'"),
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	bad := []struct{ name, q string }{
		{"colour red", hlInsert("hl_x", ", colour", ", 'red'")},
		{"33 char prefix", hlInsert("hl_x", ", quote_prefix", ", '"+strings.Repeat("p", 33)+"'")},
		{"chapter 0", strings.Replace(hlInsert("hl_x", "", ""), "'bk_1', 1,", "'bk_1', 0,", 1)},
		{"same client id", hlInsert("hl_y", ", client_id", ", 'c1'")},
		{"short sid", strings.Replace(hlInsert("hl_x", "", ""), "'c1.p1.s0', 0,", "'c1.p', 0,", 1)},
		{"audio times are gone", hlInsert("hl_x", ", start_ms, end_ms", ", 1, 2")},
	}
	for _, c := range bad {
		if _, err := w.Exec(c.q); err == nil {
			t.Errorf("%s must fail", c.name)
		}
	}
	for _, tbl := range []string{"alignments", "alignment_chapters", "runtime_components"} {
		if tableExists(t, st, tbl) {
			t.Errorf("table %s exists on a fresh database", tbl)
		}
	}
}
