package db

import (
	"strings"
	"testing"
)

const wlEdition = `INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, chapter_count, word_count, char_count, created_at)
VALUES ('ed_1', 'ef_1', '` + hexA + `', 'tendshelf-ebook-v1', '` + hexB + `', 5, 'epub', 1, 2, 3, 1)`

func wlInsert(id, folded, rest string) string {
	return `INSERT INTO word_list(id, user_id, book_id, word, folded, created_at` + rest + `) VALUES ('` + id + `', 'us_1', 'bk_1', 'Word', '` + folded + `', 1`
}

// TestMigration0022FromPrevious: a v21 database keeps every row it had and gains an empty word_list.
func TestMigration0022FromPrevious(t *testing.T) {
	dir := t.TempDir()
	prev := openUpTo(t, 21, dir)
	seed7(t, prev)
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	if v := st.SchemaVersion(); v != SchemaVersion {
		t.Fatalf("schema version = %d, want %d", v, SchemaVersion)
	}
	if !tableExists(t, st, "word_list") || countRows(t, st.Reader(), "word_list") != 0 {
		t.Fatal("word_list missing or not empty")
	}
	if countRows(t, st.Reader(), "progress") != 1 || countRows(t, st.Reader(), "shelf_items") != 1 {
		t.Error("existing progress or shelf rows were lost")
	}
	if _, err := st.Writer().Exec(wlInsert("wd_1", "word", "") + `)`); err != nil {
		t.Fatalf("a word cannot be saved after the upgrade: %v", err)
	}
	if countRows(t, st.Reader(), "word_list") != 1 {
		t.Error("the saved word did not survive")
	}
}

// TestMigration0022FromEmpty: the checks, the unique key and the cascades hold on a fresh database.
func TestMigration0022FromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	w := st.Writer()
	for _, q := range []string{
		`INSERT INTO users(id, username, role, created_at) VALUES ('us_1', 'Ana', 'owner', 1)`,
		`INSERT INTO libraries(id, name, created_at) VALUES ('lib_1', 'Books', 1)`,
		`INSERT INTO library_folders(id, library_id, path, created_at) VALUES ('lf_1', 'lib_1', '/audio', 1)`,
		`INSERT INTO books(id, library_id, folder_id, root_path, title, added_at, updated_at, kind) VALUES ('bk_1', 'lib_1', 'lf_1', '/audio/a.epub', 'A', 1, 1, 'text')`,
		ebookFileSQL("ef_1", "library", "/audio/a.epub", "", "ready", ""),
		wlEdition,
		wlInsert("wd_1", "word", ", edition_id, sid, off") + `, 'ed_1', 'c0001b0001s0000', 4)`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	for name, q := range map[string]string{
		"same word twice": wlInsert("wd_2", "word", "") + `)`,
		"empty word":      `INSERT INTO word_list(id, user_id, book_id, word, folded, created_at) VALUES ('wd_3', 'us_1', 'bk_1', '', 'x', 1)`,
		"long word":       `INSERT INTO word_list(id, user_id, book_id, word, folded, created_at) VALUES ('wd_3', 'us_1', 'bk_1', '` + strings.Repeat("w", 65) + `', 'x', 1)`,
		"long quote":      wlInsert("wd_3", "other", ", quote") + `, '` + strings.Repeat("q", 401) + `')`,
		"short sid":       wlInsert("wd_3", "other", ", sid") + `, 'abc')`,
		"negative off":    wlInsert("wd_3", "other", ", off") + `, -1)`,
		"no such person":  `INSERT INTO word_list(id, user_id, book_id, word, folded, created_at) VALUES ('wd_3', 'us_9', 'bk_1', 'w', 'w', 1)`,
	} {
		if _, err := w.Exec(q); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// A gone edition keeps the word (no jump); a gone book takes it along.
	if _, err := w.Exec(`DELETE FROM editions WHERE id = 'ed_1'`); err != nil {
		t.Fatal(err)
	}
	var ed any
	if err := st.Reader().QueryRow(`SELECT edition_id FROM word_list WHERE id = 'wd_1'`).Scan(&ed); err != nil || ed != nil {
		t.Fatalf("edition_id = %v, %v; want NULL", ed, err)
	}
	if _, err := w.Exec(`DELETE FROM books WHERE id = 'bk_1'`); err != nil {
		t.Fatal(err)
	}
	if countRows(t, st.Reader(), "word_list") != 0 {
		t.Error("a deleted book left words behind")
	}
}
