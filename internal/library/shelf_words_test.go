package library_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

func TestShelfViewTextWords(t *testing.T) {
	r, st := newRepo(t)
	lib, f := librarytest.NewLibrary(t, r, "Main", "/shelfwords")
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := st.Writer().Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	sha := func(n int) string { return fmt.Sprintf("%064x", n) }
	file := func(id, bookID string, def int, state string, n int) {
		exec(`INSERT INTO ebook_files(id, origin, library_id, book_id, book_default, ext, size, mtime_ns, quick_hash, sha256, state,
			original_name, created_at, updated_at) VALUES (?, 'upload', ?, ?, ?, 'epub', 10, 0, ?, ?, ?, 'x.epub', 1, 1)`,
			id, lib.ID, bookID, def, sha(n), sha(n), state)
	}
	edition := func(id, fileID string, words, pages, created, n int) {
		exec(`INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, title, authors_json,
			chapter_count, word_count, page_count, char_count, created_at) VALUES (?, ?, ?, 'r', ?, 10, 'epub', 'T', '[]', 1, ?, ?, 1, ?)`,
			id, fileID, sha(n), sha(n+1000), words, pages, created)
	}
	text := func(title string) library.Book {
		b := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: title, AddedAt: 10})
		exec(`UPDATE books SET kind = 'text', duration_ms = 0 WHERE id = ?`, b.ID)
		return b
	}
	words := text("Words")
	file("f_w_old", words.ID, 1, "ready", 1)
	edition("e_w_old", "f_w_old", 9300, 0, 1, 1)
	edition("e_w_new", "f_w_old", 93000, 0, 2, 2)
	pages := text("Pages")
	file("f_p", pages.ID, 1, "ready", 3)
	edition("e_p", "f_p", 0, 10, 1, 3)
	pending := text("Pending")
	file("f_x", pending.ID, 1, "pending", 4)
	edition("e_x", "f_x", 500, 0, 1, 4)
	audio := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Audio", AddedAt: 10, DurationMs: 3_600_000})

	page, err := r.ListBooks(context.Background(), allScope, library.ListQuery{})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]int64{words.ID: 93000, pages.ID: 3000, pending.ID: 0, audio.ID: 0}
	if len(page.Books) != len(want) {
		t.Fatalf("%d books, want %d", len(page.Books), len(want))
	}
	for _, b := range page.Books {
		if b.TextWords != want[b.ID] {
			t.Errorf("%s: TextWords %d, want %d", b.Title, b.TextWords, want[b.ID])
		}
	}

	// The subquery finds its file through an ebook_files index, never a table scan.
	rows, err := st.Reader().Query(`EXPLAIN QUERY PLAN SELECT COALESCE((SELECT CASE WHEN e.word_count > 0 THEN e.word_count ELSE e.page_count * 300 END
		FROM ebook_files f JOIN editions e ON e.ebook_file_id = f.id WHERE b.kind = 'text' AND f.book_id = b.id AND f.state = 'ready'
		ORDER BY f.book_default DESC, e.created_at DESC, e.id DESC LIMIT 1), 0) FROM books b WHERE b.id = ?`, words.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var plan []string
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		plan = append(plan, detail)
	}
	all := strings.Join(plan, "\n")
	if !strings.Contains(all, "ebook_files") || strings.Contains(all, "SCAN f") || strings.Contains(all, "SCAN ebook_files") {
		t.Errorf("plan scans ebook_files:\n%s", all)
	}
}
