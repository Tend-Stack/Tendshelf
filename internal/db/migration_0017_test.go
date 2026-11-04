package db

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"
)

// TestMigration0017Pairing: a v16 database keeps its ebook file, the new columns take their defaults, the
// book's default is unique, and deleting the book unpairs the file and removes the pairing rows.
func TestMigration0017Pairing(t *testing.T) {
	ms := embedded(t)
	dir := t.TempDir()
	prev, err := open(context.Background(), dir, openConfig{migrations: ms[:16], supported: 16, now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	seed7(t, prev)
	if _, err := prev.Writer().Exec(ebookFileSQL("ef_1", "library", "/audio/a/a.epub", "", "ready", "")); err != nil {
		t.Fatal(err)
	}
	prev.Close()

	st := mustOpen(t, dir)
	defer st.Close()
	var hold sql.NullString
	var def int
	if err := st.Reader().QueryRow(`SELECT hold, book_default FROM ebook_files WHERE id = 'ef_1'`).Scan(&hold, &def); err != nil {
		t.Fatalf("file row lost: %v", err)
	}
	if hold.Valid || def != 0 {
		t.Errorf("hold=%v book_default=%d, want NULL and 0", hold, def)
	}
	w := st.Writer()
	second := strings.Replace(ebookFileSQL("ef_2", "library", "/audio/a/b.epub", "", "ready", ""), "'"+hexA+"'", "'"+hexB+"'", 1)
	for _, q := range []string{
		`INSERT INTO ebook_not_same(ebook_file_id, book_id, user_id, created_at) VALUES ('ef_1', 'bk_1', 'us_1', 1)`,
		`INSERT INTO text_choices(user_id, book_id, ebook_file_id, updated_at) VALUES ('us_1', 'bk_1', 'ef_1', 1)`,
		`UPDATE ebook_files SET book_default = 1 WHERE id = 'ef_1'`,
		second,
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := w.Exec(`UPDATE ebook_files SET book_default = 1 WHERE id = 'ef_2'`); err == nil {
		t.Error("a second default on one book must fail")
	}
	if _, err := w.Exec(`DELETE FROM books WHERE id = 'bk_1'`); err != nil {
		t.Fatalf("deleting the book: %v", err)
	}
	var book sql.NullString
	if err := st.Reader().QueryRow(`SELECT book_id FROM ebook_files WHERE id = 'ef_1'`).Scan(&book); err != nil || book.Valid {
		t.Errorf("book_id = %v, err %v; want NULL", book, err)
	}
	if countRows(t, st.Reader(), "ebook_not_same") != 0 || countRows(t, st.Reader(), "text_choices") != 0 {
		t.Error("not-same and choice rows must go with the book")
	}
}

// TestMigration0017FromEmpty: the checks and keys hold on a fresh database.
func TestMigration0017FromEmpty(t *testing.T) {
	st := mustOpen(t, t.TempDir())
	defer st.Close()
	w := st.Writer()
	for _, q := range []string{
		`INSERT INTO libraries(id, name, created_at) VALUES ('lib_1', 'Books', 1)`,
		`INSERT INTO library_folders(id, library_id, path, created_at) VALUES ('lf_1', 'lib_1', '/audio', 1)`,
		`INSERT INTO users(id, username, role, created_at) VALUES ('us_1', 'Ana', 'owner', 1)`,
		`INSERT INTO books(id, library_id, folder_id, root_path, title, added_at, updated_at) VALUES ('bk_1', 'lib_1', 'lf_1', '/audio/a', 'A', 1, 1)`,
		ebookFileSQL("ef_1", "library", "/audio/a/a.epub", "", "ready", ""),
	} {
		if _, err := w.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if _, err := w.Exec(`UPDATE ebook_files SET hold = 'x' WHERE id = 'ef_1'`); err == nil {
		t.Error("hold = 'x' must fail its CHECK")
	}
	if _, err := w.Exec(`UPDATE ebook_files SET book_default = 2 WHERE id = 'ef_1'`); err == nil {
		t.Error("book_default = 2 must fail its CHECK")
	}
	if _, err := w.Exec(`UPDATE ebook_files SET hold = 'kept' WHERE id = 'ef_1'`); err != nil {
		t.Errorf("hold = 'kept': %v", err)
	}
	ins := `INSERT INTO text_choices(user_id, book_id, ebook_file_id, updated_at) VALUES ('us_1', 'bk_1', 'ef_1', 1)`
	if _, err := w.Exec(ins); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(ins); err == nil {
		t.Error("a second choice for one person and book must fail")
	}
	if _, err := w.Exec(`DELETE FROM users WHERE id = 'us_1'`); err != nil {
		t.Fatal(err)
	}
	if countRows(t, st.Reader(), "text_choices") != 0 {
		t.Error("the choice must go with the person")
	}
}
