package web

import (
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Removing the whole book also removes the ebooks uploaded on its Text page, rows and stored files.
func TestRemoveWholeBookTakesTextPageEditions(t *testing.T) {
	p := newRemovePartApp(t)
	rec := p.post(p.owner, url.Values{})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("remove: %d", rec.Code)
	}
	if n := p.a.count(t, `SELECT count(*) FROM books WHERE id = ?`, p.book.ID); n != 0 {
		t.Error("the book is still there")
	}
	if n := p.a.count(t, `SELECT count(*) FROM ebook_files`); n != 0 {
		t.Errorf("ebook rows left = %d", n)
	}
	for i, f := range p.stored {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Errorf("stored copy %d is still on disk", i)
		}
	}
}

// A library ebook Tendshelf did not place stays on disk and is only unpaired; the page says so.
func TestRemoveWholeBookLeavesLibraryEbook(t *testing.T) {
	p := newRemovePartApp(t)
	theirs := filepath.Join(p.folder.Path, "theirs.epub")
	if err := os.WriteFile(theirs, []byte("their epub"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := p.a.st.Writer().Exec(`INSERT INTO ebook_files(id, origin, library_id, folder_id, path, book_id, paired_by, ext, size, mtime_ns, quick_hash, state, created_at, updated_at)
VALUES ('ebf_rp_lib', 'library', ?, ?, ?, ?, 'folder', 'epub', 10, 0, ?, 'ready', 9, 9)`,
		p.book.LibraryID, p.folder.ID, theirs, p.book.ID, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	conf := p.a.do(http.MethodGet, "/books/"+p.book.ID+"/remove?part=whole", nil, nil, p.owner)
	if conf.Code != http.StatusOK || !strings.Contains(conf.Body.String(), "1 file stays on disk because Tendshelf did not place it.") {
		t.Fatalf("confirmation: %d", conf.Code)
	}
	if rec := p.post(p.owner, url.Values{}); rec.Code != http.StatusSeeOther {
		t.Fatalf("remove: %d", rec.Code)
	}
	if n := p.a.count(t, `SELECT count(*) FROM books WHERE id = ?`, p.book.ID); n != 0 {
		t.Error("the book is still there")
	}
	if _, err := os.Stat(theirs); err != nil {
		t.Errorf("their file was touched: %v", err)
	}
	if n := p.a.count(t, `SELECT count(*) FROM ebook_files WHERE id = 'ebf_rp_lib' AND book_id IS NULL`); n != 1 {
		t.Error("the library ebook is not left unpaired")
	}
	if n := p.a.count(t, `SELECT count(*) FROM ebook_files`); n != 1 {
		t.Errorf("ebook rows = %d, want only the library one", n)
	}
}
