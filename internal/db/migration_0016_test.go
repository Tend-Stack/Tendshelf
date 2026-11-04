package db

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

const (
	hexA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	hexB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func ebookFileSQL(id, origin, path, sha, state, reason string) string {
	q := func(s string) string {
		if s == "" {
			return "NULL"
		}
		return "'" + s + "'"
	}
	folder := "NULL"
	if path != "" {
		folder = "'lf_1'"
	}
	return `INSERT INTO ebook_files(id, origin, library_id, folder_id, path, book_id, ext, size, quick_hash, sha256, state, refused_reason, created_at, updated_at)
VALUES ('` + id + `', '` + origin + `', 'lib_1', ` + folder + `, ` + q(path) + `, 'bk_1', 'epub', 10, '` + hexA + `', ` + q(sha) + `, '` + state + `', ` + q(reason) + `, 1, 1)`
}

// TestMigration0016Ebooks: a v15 database keeps its rows, the new tables take rows, and deleting the
// book leaves the ebook file unpaired instead of failing.
func TestMigration0016Ebooks(t *testing.T) {
	ms := embedded(t)
	dir := t.TempDir()
	prev, err := open(context.Background(), dir, openConfig{migrations: ms[:15], supported: 15, now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	seed7(t, prev)
	prev.Close()
	st := mustOpen(t, dir)
	defer st.Close()
	if countRows(t, st.Reader(), "books") != 1 {
		t.Fatal("books lost its row")
	}
	w := st.Writer()
	for _, q := range []string{
		ebookFileSQL("ef_1", "library", "/audio/a/a.epub", "", "ready", ""),
		`UPDATE ebook_files SET paired_by = 'folder' WHERE id = 'ef_1'`,
		`INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, chapter_count, word_count, char_count, created_at)
VALUES ('ed_1', 'ef_1', '` + hexA + `', 'tendshelf-ebook-v1', '` + hexB + `', 5, 'epub', 1, 2, 3, 1)`,
		`INSERT INTO edition_chapters(edition_id, idx, block_count, word_count, char_count) VALUES ('ed_1', 1, 1, 2, 3)`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := w.Exec(`DELETE FROM books WHERE id = 'bk_1'`); err != nil {
		t.Fatalf("deleting the book: %v", err)
	}
	var book sql.NullString
	if err := st.Reader().QueryRow(`SELECT book_id FROM ebook_files WHERE id = 'ef_1'`).Scan(&book); err != nil {
		t.Fatalf("file row gone: %v", err)
	}
	if book.Valid {
		t.Errorf("book_id = %q, want NULL", book.String)
	}
	if _, err := w.Exec(`DELETE FROM ebook_files WHERE id = 'ef_1'`); err != nil {
		t.Fatal(err)
	}
	if countRows(t, st.Reader(), "editions") != 0 || countRows(t, st.Reader(), "edition_chapters") != 0 {
		t.Error("editions and chapters must cascade with the file")
	}
}

// TestMigration0016FromEmpty: the tables exist and their checks hold.
func TestMigration0016FromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	w := st.Writer()
	for _, q := range []string{
		`INSERT INTO libraries(id, name, created_at) VALUES ('lib_1', 'Books', 1)`,
		`INSERT INTO library_folders(id, library_id, path, created_at) VALUES ('lf_1', 'lib_1', '/audio', 1)`,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for _, tbl := range []string{"ebook_files", "editions", "edition_chapters"} {
		if _, err := st.Reader().Exec(`SELECT 1 FROM ` + tbl); err != nil {
			t.Errorf("%s: %v", tbl, err)
		}
	}
	bad := strings.Replace(ebookFileSQL("ef_u", "upload", "", "", "ready", ""), "'bk_1'", "NULL", 1)
	if _, err := w.Exec(bad); err == nil {
		t.Error("an upload row with NULL sha256 must be rejected")
	}
	bad = strings.Replace(ebookFileSQL("ef_r", "library", "/audio/x.epub", "", "refused", ""), "'bk_1'", "NULL", 1)
	if _, err := w.Exec(bad); err == nil {
		t.Error("a refused row without a reason must be rejected")
	}
	ok := strings.Replace(ebookFileSQL("ef_ok", "upload", "", hexB, "ready", ""), "'bk_1'", "NULL", 1)
	if _, err := w.Exec(ok); err != nil {
		t.Errorf("valid upload row: %v", err)
	}
}
