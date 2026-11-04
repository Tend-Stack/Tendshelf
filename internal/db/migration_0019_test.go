package db

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// hlInsert inserts a valid highlight for us_1 on bk_1; extraCols and extraVals add or override nothing, they append columns.
func hlInsert(id, extraCols, extraVals string) string {
	return `INSERT INTO highlights(id, user_id, book_id, chapter_idx, start_sid, start_off, end_sid, end_off, quote, created_at, updated_at` + extraCols +
		`) VALUES ('` + id + `', 'us_1', 'bk_1', 1, 'c1.p1.s0', 0, 'c1.p1.s0', 5, 'Hello', 1, 1` + extraVals + `)`
}

// TestMigration0019Highlights: a v18 database keeps its bookmark; the new columns start NULL; forgetting an
// edition keeps the marks; deleting the user removes the highlight.
func TestMigration0019Highlights(t *testing.T) {
	ms := embedded(t)
	dir := t.TempDir()
	prev, err := open(context.Background(), dir, openConfig{migrations: ms[:18], supported: 18, now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	seed7(t, prev)
	for _, q := range []string{ebookFileSQL("ef_1", "library", "/audio/a/a.epub", "", "ready", ""), alSeedEdition} {
		if _, err := prev.Writer().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	prev.Close()

	st := openUpTo(t, 19, dir)
	defer st.Close()
	w := st.Writer()
	var note string
	var ed, sid sql.NullString
	if err := st.Reader().QueryRow(`SELECT note, edition_id, sid FROM bookmarks WHERE id = 'bm_1'`).Scan(&note, &ed, &sid); err != nil {
		t.Fatal(err)
	}
	if note != "n" || ed.Valid || sid.Valid {
		t.Fatalf("bookmark = %q %v %v", note, ed, sid)
	}
	for _, q := range []string{
		hlInsert("hl_1", ", edition_id", ", 'ed_1'"),
		`UPDATE bookmarks SET edition_id = 'ed_1', sid = 'c1.p1.s1' WHERE id = 'bm_1'`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := w.Exec(`DELETE FROM editions`); err != nil {
		t.Fatal(err)
	}
	if err := st.Reader().QueryRow(`SELECT edition_id, sid FROM bookmarks WHERE id = 'bm_1'`).Scan(&ed, &sid); err != nil || ed.Valid || sid.String != "c1.p1.s1" {
		t.Errorf("bookmark after edition delete = %v %v, %v", ed, sid, err)
	}
	var quote string
	if err := st.Reader().QueryRow(`SELECT quote, edition_id FROM highlights WHERE id = 'hl_1'`).Scan(&quote, &ed); err != nil || quote != "Hello" || ed.Valid {
		t.Errorf("highlight after edition delete = %q %v, %v", quote, ed, err)
	}
	if _, err := w.Exec(`DELETE FROM users WHERE id = 'us_1'`); err != nil {
		t.Fatal(err)
	}
	if countRows(t, st.Reader(), "highlights") != 0 {
		t.Error("highlights must go with the user")
	}
}

// TestMigration0019FromEmpty: the checks and keys hold on a fresh database.
func TestMigration0019FromEmpty(t *testing.T) {
	st := openUpTo(t, 19, t.TempDir())
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
		{"start_ms without end_ms", hlInsert("hl_x", ", start_ms", ", 5")},
		{"end_ms before start_ms", hlInsert("hl_x", ", start_ms, end_ms", ", 9, 4")},
		{"chapter 0", strings.Replace(hlInsert("hl_x", "", ""), "'bk_1', 1,", "'bk_1', 0,", 1)},
		{"same client id", hlInsert("hl_y", ", client_id", ", 'c1'")},
		{"short sid", strings.Replace(hlInsert("hl_x", "", ""), "'c1.p1.s0', 0,", "'c1.p', 0,", 1)},
	}
	for _, c := range bad {
		if _, err := w.Exec(c.q); err == nil {
			t.Errorf("%s must fail", c.name)
		}
	}
}
