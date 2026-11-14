package web

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// removePartApp is an audiobook Tendshelf added, with three ebook editions of one title uploaded on its Text page.
type removePartApp struct {
	*uploadApp
	t       *testing.T
	book    library.Book
	files   [3]string // ebook file ids, oldest first; files[0] is the book's edition
	stored  [3]string // their stored copies in the data folder
	ownerID string
}

func newRemovePartApp(t *testing.T) *removePartApp {
	t.Helper()
	u := newUploadApp(t)
	p := &removePartApp{uploadApp: u, t: t}
	u.a.srv.ebooks = ebook.New(u.a.st, u.a.lib, u.a.runner, u.a.dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	p.ownerID = sweepUserID(t, u.a, u.owner)
	p.book = librarytest.AddBook(t, u.a.lib, u.folder, librarytest.BookSpec{Title: "Dominican", Files: 2})
	audio, err := u.a.lib.AudioFiles(upCtx, p.book.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, af := range audio {
		if _, err := u.a.st.Writer().Exec(`INSERT INTO upload_paths(id, upload_id, library_folder_id, path, kind, state, size, mtime_ns, created_at, updated_at)
  VALUES (?, 'up_x', ?, ?, 'file', 'present', 1, 1, 1, 1)`, fmt.Sprintf("upp_rp_%d", i), u.folder.ID, af.Path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(u.a.dir, "ebooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	for i := range p.files {
		id := fmt.Sprintf("ebf_rp_%d", i)
		sha := fmt.Sprintf("%064x", 7000+i)
		def := 0
		if i == 0 {
			def = 1
		}
		if _, err := u.a.st.Writer().Exec(`INSERT INTO ebook_files(id, origin, library_id, book_id, book_default, paired_by, ext, size, mtime_ns, quick_hash,
		sha256, state, original_name, uploaded_by, created_at, updated_at) VALUES (?, 'upload', ?, ?, ?, 'upload', 'epub', 10, 0, ?, ?, 'ready', 'The Dominican Affair.epub', ?, ?, ?)`,
			id, p.book.LibraryID, p.book.ID, def, sha, sha, p.ownerID, i+1, i+1); err != nil {
			t.Fatal(err)
		}
		if _, err := u.a.st.Writer().Exec(`INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, title, authors_json,
		chapter_count, word_count, char_count, created_at) VALUES (?, ?, ?, ?, ?, 10, 'epub', 'The Dominican Affair', '[]', 3, 30, 300, 1)`,
			"ed_rp_"+id, id, sha, ebook.Recipe, sha); err != nil {
			t.Fatal(err)
		}
		p.files[i] = id
		p.stored[i] = filepath.Join(u.a.dir, "ebooks", sha+".epub")
		if err := os.WriteFile(p.stored[i], []byte("epub bytes"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := u.a.st.Writer().Exec(`INSERT INTO text_choices(user_id, book_id, ebook_file_id, updated_at) VALUES (?, ?, ?, 1)`, p.ownerID, p.book.ID, p.files[0]); err != nil {
		t.Fatal(err)
	}
	return p
}

func (p *removePartApp) post(ck *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	return p.a.do(http.MethodPost, "/books/"+p.book.ID+"/remove", form, func(r *http.Request) {
		if ck != nil {
			r.Header.Set(csrfHeader, p.a.tokenFor(p.t, ck))
		}
	}, ck)
}

func TestRemoveOneEditionOfThree(t *testing.T) {
	p := newRemovePartApp(t)
	// The Text tab offers "Remove this edition" on each card, and the link opens a confirmation of that file.
	body := p.a.do(http.MethodGet, "/books/"+p.book.ID+"/text", nil, nil, p.owner).Body.String()
	for _, f := range p.files {
		if !strings.Contains(body, "/books/"+p.book.ID+"/remove?file="+f) {
			t.Errorf("no remove link for %s", f)
		}
	}
	conf := p.a.do(http.MethodGet, "/books/"+p.book.ID+"/remove?file="+p.files[0], nil, nil, p.owner)
	if conf.Code != http.StatusOK || !strings.Contains(conf.Body.String(), "Remove this edition?") || !strings.Contains(conf.Body.String(), `name="file" value="`+p.files[0]+`"`) {
		t.Fatalf("confirmation: %d", conf.Code)
	}
	rec := p.post(p.owner, url.Values{"part": {"text"}, "file": {p.files[0]}})
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/books/"+p.book.ID+"/text") {
		t.Fatalf("remove: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if n := p.a.count(t, `SELECT count(*) FROM ebook_files WHERE book_id = ?`, p.book.ID); n != 2 {
		t.Errorf("editions left = %d, want 2", n)
	}
	if n := p.a.count(t, `SELECT count(*) FROM ebook_files WHERE id = ?`, p.files[0]); n != 0 {
		t.Error("the removed edition is still there")
	}
	if _, err := os.Stat(p.stored[0]); !os.IsNotExist(err) {
		t.Error("its stored copy is still on disk")
	}
	for _, i := range []int{1, 2} {
		if _, err := os.Stat(p.stored[i]); err != nil {
			t.Errorf("edition %d's file was touched: %v", i, err)
		}
	}
	// The audiobook is untouched, and the book's edition moved to the newest ready one; the owner's own choice went with the file.
	if n := p.a.count(t, `SELECT count(*) FROM audio_files WHERE book_id = ?`, p.book.ID); n != 2 {
		t.Errorf("audio files = %d", n)
	}
	if n := p.a.count(t, `SELECT count(*) FROM books WHERE id = ?`, p.book.ID); n != 1 {
		t.Error("the book went")
	}
	if n := p.a.count(t, `SELECT count(*) FROM ebook_files WHERE id = ? AND book_default = 1`, p.files[2]); n != 1 {
		t.Error("the book's edition did not move to the newest ready file")
	}
	if n := p.a.count(t, `SELECT count(*) FROM ebook_files WHERE book_id = ? AND book_default = 1`, p.book.ID); n != 1 {
		t.Errorf("book editions = %d, want 1", n)
	}
	if n := p.a.count(t, `SELECT count(*) FROM text_choices`); n != 0 {
		t.Errorf("text_choices = %d", n)
	}
	if !strings.Contains(p.a.do(http.MethodGet, rec.Header().Get("Location"), nil, nil, p.owner).Body.String(), bookRemoveEditionDone) {
		t.Error("the Text tab does not say the edition was removed")
	}
}

func TestRemoveEditionRefusals(t *testing.T) {
	p := newRemovePartApp(t)
	contributor := p.a.as(t, users.RoleMember)
	if _, err := p.a.st.Writer().Exec(`UPDATE users SET adds_books = 1 WHERE id = ?`, sweepUserID(t, p.a, contributor)); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"part": {"text"}, "file": {p.files[1]}}
	for name, ck := range map[string]*http.Cookie{"member": p.member, "guest": p.guest, "contributor": contributor} {
		if rec := p.post(ck, form); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", name, rec.Code)
		}
		if rec := p.a.do(http.MethodGet, "/books/"+p.book.ID+"/remove?file="+p.files[1], nil, nil, ck); rec.Code != http.StatusNotFound {
			t.Errorf("%s confirmation: %d", name, rec.Code)
		}
	}
	// A file of another book, an unknown part and an unknown file are refused.
	if rec := p.post(p.owner, url.Values{"part": {"text"}, "file": {"ebf_nope"}}); rec.Code != http.StatusConflict {
		t.Errorf("unknown file: %d", rec.Code)
	}
	if rec := p.post(p.owner, url.Values{"part": {"sideways"}}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown part: %d", rec.Code)
	}
	// A library ebook Tendshelf did not add is not offered and not removed.
	if _, err := p.a.st.Writer().Exec(`INSERT INTO ebook_files(id, origin, library_id, folder_id, path, book_id, paired_by, ext, size, mtime_ns, quick_hash, state, created_at, updated_at)
VALUES ('ebf_rp_lib', 'library', ?, ?, ?, ?, 'folder', 'epub', 10, 0, ?, 'ready', 9, 9)`,
		p.book.LibraryID, p.folder.ID, filepath.Join(p.folder.Path, "theirs.epub"), p.book.ID, strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if rec := p.a.do(http.MethodGet, "/books/"+p.book.ID+"/remove?file=ebf_rp_lib", nil, nil, p.owner); rec.Code != http.StatusNotFound {
		t.Errorf("library file confirmation: %d", rec.Code)
	}
	if body := p.a.do(http.MethodGet, "/books/"+p.book.ID+"/text", nil, nil, p.owner).Body.String(); strings.Contains(body, "remove?file=ebf_rp_lib") {
		t.Error("a library ebook is offered for removal")
	}
	if rec := p.post(p.owner, url.Values{"part": {"text"}, "file": {"ebf_rp_lib"}}); rec.Code != http.StatusConflict {
		t.Errorf("library file: %d", rec.Code)
	}
	if n := p.a.count(t, `SELECT count(*) FROM ebook_files`); n != 4 {
		t.Errorf("ebook files = %d, want 4", n)
	}
}

func TestRemoveAudioPartLeavesTextBooks(t *testing.T) {
	p := newRemovePartApp(t)
	// The page offers the audiobook on its own.
	conf := p.a.do(http.MethodGet, "/books/"+p.book.ID+"/remove", nil, nil, p.owner)
	if conf.Code != http.StatusOK || !strings.Contains(conf.Body.String(), bookRemoveAudioButton) {
		t.Fatalf("confirmation: %d", conf.Code)
	}
	rec := p.post(p.owner, url.Values{"part": {"audio"}})
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "removed=audio") {
		t.Fatalf("remove audio: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if n := p.a.count(t, `SELECT count(*) FROM books WHERE id = ?`, p.book.ID); n != 0 {
		t.Error("the audiobook is still there")
	}
	if n := p.a.count(t, `SELECT count(*) FROM audio_files`); n != 0 {
		t.Errorf("audio files = %d", n)
	}
	if n := p.a.count(t, `SELECT count(*) FROM ebook_files f JOIN books b ON b.id = f.book_id WHERE b.kind = 'text'`); n != 3 {
		t.Errorf("ebooks on text books = %d, want 3", n)
	}
	for i := range p.stored {
		if _, err := os.Stat(p.stored[i]); err != nil {
			t.Errorf("edition %d's file went: %v", i, err)
		}
	}
}
