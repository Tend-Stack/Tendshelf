package web

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

// textOnlyBook is a text book whose n editions were uploaded on the Text page (stored by Tendshelf, no path).
func (p *removePartApp) textOnlyBook(t *testing.T, title string, n int) (library.Book, []string, []string) {
	t.Helper()
	root := filepath.Join(p.a.dir, "ebooks", "txt_"+strings.ReplaceAll(title, " ", "_"))
	id, err := p.a.lib.CreateTextBook(upCtx, library.TextBookIn{LibraryID: p.folder.LibraryID, FolderID: p.folder.ID, RootPath: root, Title: title})
	if err != nil {
		t.Fatal(err)
	}
	book, err := p.a.lib.Book(upCtx, id)
	if err != nil {
		t.Fatal(err)
	}
	var files, stored []string
	for i := 0; i < n; i++ {
		fid := fmt.Sprintf("ebf_to_%s_%d", strings.ReplaceAll(title, " ", ""), i)
		sha := fmt.Sprintf("%064x", 9000+len(title)*10+i)
		def := 0
		if i == 0 {
			def = 1
		}
		if _, err := p.a.st.Writer().Exec(`INSERT INTO ebook_files(id, origin, library_id, book_id, book_default, paired_by, ext, size, mtime_ns, quick_hash,
		sha256, state, original_name, uploaded_by, created_at, updated_at) VALUES (?, 'upload', ?, ?, ?, 'upload', 'epub', 10, 0, ?, ?, 'ready', 'Only Text.epub', ?, ?, ?)`,
			fid, book.LibraryID, book.ID, def, sha, sha, p.ownerID, i+1, i+1); err != nil {
			t.Fatal(err)
		}
		if _, err := p.a.st.Writer().Exec(`INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, title, authors_json,
		chapter_count, word_count, char_count, created_at) VALUES (?, ?, ?, ?, ?, 10, 'epub', ?, '[]', 3, 30, 300, 1)`,
			"ed_"+fid, fid, sha, ebook.Recipe, sha, title); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(p.a.dir, "ebooks", sha+".epub")
		if err := os.WriteFile(path, []byte("epub bytes"), 0o644); err != nil {
			t.Fatal(err)
		}
		files, stored = append(files, fid), append(stored, path)
	}
	return book, files, stored
}

func (p *removePartApp) postFor(bookID string, ck *http.Cookie, form url.Values) int {
	form.Set("_csrf", p.a.tokenFor(p.t, ck))
	return p.a.do(http.MethodPost, "/books/"+bookID+"/remove", form, nil, ck).Code
}

// A text-only book of one stored upload can be removed whole: page button, menu item, confirmation, POST.
func TestRemoveTextOnlyStoredUpload(t *testing.T) {
	p := newRemovePartApp(t)
	book, _, stored := p.textOnlyBook(t, "Lonely Text", 1)
	base := "/books/" + book.ID + "/remove"
	page := p.a.do(http.MethodGet, "/books/"+book.ID+"/details", nil, nil, p.owner).Body.String()
	if !strings.Contains(page, `href="`+base+`"`) || !strings.Contains(page, base+"?part=whole") || !strings.Contains(page, moreRemoveWhole) {
		t.Error("the page has no remove button or menu item")
	}
	if strings.Contains(page, "remove?file=") {
		t.Error("the only file of a text book is offered as an edition")
	}
	conf := p.a.do(http.MethodGet, base, nil, nil, p.owner)
	if conf.Code != http.StatusOK || !strings.Contains(conf.Body.String(), "Only Text.epub") {
		t.Fatalf("confirmation: %d", conf.Code)
	}
	for name, ck := range map[string]*http.Cookie{"member": p.member, "guest": p.guest} {
		if code := p.postFor(book.ID, ck, url.Values{}); code != http.StatusNotFound {
			t.Errorf("%s: %d", name, code)
		}
	}
	if code := p.postFor(book.ID, p.owner, url.Values{}); code != http.StatusSeeOther {
		t.Fatalf("remove: %d", code)
	}
	if n := p.a.count(t, `SELECT count(*) FROM books WHERE id = ?`, book.ID); n != 0 {
		t.Error("the book is still there")
	}
	if n := p.a.count(t, `SELECT count(*) FROM ebook_files WHERE id LIKE 'ebf_to_%'`); n != 0 {
		t.Errorf("ebook rows = %d", n)
	}
	if _, err := os.Stat(stored[0]); !os.IsNotExist(err) {
		t.Error("the stored copy is still on disk")
	}
	// The audiobook lab book is untouched.
	if n := p.a.count(t, `SELECT count(*) FROM books WHERE id = ?`, p.book.ID); n != 1 {
		t.Error("another book went")
	}
}

// Several stored editions: each card removes its own; the last one is removed with the whole book.
func TestRemoveTextOnlySeveralEditions(t *testing.T) {
	p := newRemovePartApp(t)
	book, files, stored := p.textOnlyBook(t, "Three Texts", 3)
	base := "/books/" + book.ID + "/remove"
	tab := p.a.do(http.MethodGet, "/books/"+book.ID+"/text", nil, nil, p.owner).Body.String()
	for _, f := range files {
		if !strings.Contains(tab, base+"?file="+f) {
			t.Errorf("no remove link for %s", f)
		}
	}
	if !strings.Contains(p.a.do(http.MethodGet, "/books/"+book.ID+"/details", nil, nil, p.owner).Body.String(), base+"?part=whole") {
		t.Error("no whole-book item")
	}
	for _, f := range files[:2] {
		if code := p.postFor(book.ID, p.owner, url.Values{"part": {"text"}, "file": {f}}); code != http.StatusSeeOther {
			t.Fatalf("edition %s: %d", f, code)
		}
	}
	if n := p.a.count(t, `SELECT count(*) FROM books WHERE id = ?`, book.ID); n != 1 {
		t.Fatal("the book went with two editions")
	}
	if n := p.a.count(t, `SELECT count(*) FROM ebook_files WHERE book_id = ? AND book_default = 1`, book.ID); n != 1 {
		t.Error("no edition is the book's")
	}
	// One edition left: no per-edition link, the whole book is the way.
	if strings.Contains(p.a.do(http.MethodGet, "/books/"+book.ID+"/text", nil, nil, p.owner).Body.String(), "remove?file=") {
		t.Error("the last edition is offered on its own")
	}
	if code := p.postFor(book.ID, p.owner, url.Values{}); code != http.StatusSeeOther {
		t.Fatalf("whole: %d", code)
	}
	if n := p.a.count(t, `SELECT count(*) FROM books WHERE id = ?`, book.ID); n != 0 {
		t.Error("the book is still there")
	}
	for i, s := range stored {
		if _, err := os.Stat(s); !os.IsNotExist(err) {
			t.Errorf("stored copy %d is still on disk", i)
		}
	}
}

// An audiobook Tendshelf added is removable whole and offers it in the menu.
func TestRemoveAudioOnlyOffered(t *testing.T) {
	p := newRemovePartApp(t)
	b := librarytest.AddBook(t, p.a.lib, p.folder, librarytest.BookSpec{Title: "Audio Only", Files: 1})
	files, err := p.a.lib.AudioFiles(upCtx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, af := range files {
		if _, err := p.a.st.Writer().Exec(`INSERT INTO upload_paths(id, upload_id, library_folder_id, path, kind, state, size, mtime_ns, created_at, updated_at)
  VALUES (?, 'up_x', ?, ?, 'file', 'present', 1, 1, 1, 1)`, fmt.Sprintf("upp_ao_%d", i), p.folder.ID, af.Path); err != nil {
			t.Fatal(err)
		}
	}
	page := p.a.do(http.MethodGet, "/books/"+b.ID, nil, nil, p.owner).Body.String()
	if !strings.Contains(page, moreRemoveWhole) || strings.Contains(page, moreRemoveAudio) || strings.Contains(page, moreRemoveText) {
		t.Error("an audio-only book should offer only the whole book")
	}
	if code := p.postFor(b.ID, p.owner, url.Values{}); code != http.StatusSeeOther {
		t.Fatalf("remove: %d", code)
	}
}
