package web

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/library"
)

// eadmSeedMaybe gives an app one ebook that is a book of its own and may be the same book as `book` (it names
// `author` and another title, so the engine leaves it), for the accessibility audit and the captures.
func eadmSeedMaybe(t *testing.T, a *testApp, book library.Book, userID, author string) {
	t.Helper()
	id, err := a.lib.CreateTextBook(context.Background(), library.TextBookIn{LibraryID: book.LibraryID, FolderID: book.FolderID,
		RootPath: filepath.Join(a.dir, "ebooks", "ebf_a11y_maybe"), Title: "Maybe Almanac", ID: "bk_a11y_maybe"})
	if err != nil {
		t.Fatal(err)
	}
	sha := fmt.Sprintf("%064x", 424242)
	if _, err := a.st.Writer().Exec(`INSERT INTO ebook_files(id, origin, library_id, book_id, book_default, ext, size, mtime_ns, quick_hash, sha256, state,
		original_name, uploaded_by, created_at, updated_at) VALUES ('ebf_a11y_maybe', 'upload', ?, ?, 1, 'epub', 10, 0, ?, ?, 'ready', 'maybe-almanac.epub', ?, 1, 1)`,
		book.LibraryID, id, sha, sha, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.st.Writer().Exec(`INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, title, authors_json,
		chapter_count, word_count, char_count, created_at) VALUES ('ed_a11y_maybe', 'ebf_a11y_maybe', ?, ?, ?, 10, 'epub', 'Maybe Almanac', ?, 3, 30, 300, 1)`,
		sha, ebook.Recipe, sha, `["`+author+`"]`); err != nil {
		t.Fatal(err)
	}
}
