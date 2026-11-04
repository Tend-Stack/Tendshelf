package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/library"
)

// /admin/ebooks after ebooks became books (M5 B3). A "text book" below is an ebook file with a book of its own;
// l.book is the audiobook. An author-only match (same author, other title) is a suggestion the engine never takes.

// eadmPost sends a urlencoded form with the token and the extra fields.
func (l *textLab) eadmPost(t *testing.T, ck *http.Cookie, path string, fields map[string]string, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	form := map[string][]string{"_csrf": {l.a.tokenFor(t, ck)}}
	for k, v := range fields {
		form[k] = []string{v}
	}
	return l.a.do(http.MethodPost, path, form, func(r *http.Request) {
		if htmx {
			r.Header.Set("HX-Request", "true")
		}
	}, ck)
}

func eadmHas(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("page lacks %q", w)
		}
	}
}

func eadmLacks(t *testing.T, body string, not ...string) {
	t.Helper()
	for _, w := range not {
		if strings.Contains(body, w) {
			t.Errorf("page has %q", w)
		}
	}
}

// eadmText adds an ebook file as a book of its own, the way a scan and the catch-up do.
func (l *textLab) eadmText(t *testing.T, name, title, author string) ebook.File {
	t.Helper()
	f := l.textPairAdd(t, name, title, author, "")
	l.svc.Finisher()(context.Background(), l.book.LibraryID)
	return l.textPairFile(t, f.ID)
}

func (l *textLab) eadmRow(t *testing.T, id string) (book, hold, by string) {
	t.Helper()
	if err := l.a.st.Reader().QueryRow(`SELECT COALESCE(book_id,''), COALESCE(hold,''), COALESCE(paired_by,'') FROM ebook_files WHERE id = ?`, id).Scan(&book, &hold, &by); err != nil {
		t.Fatal(err)
	}
	return book, hold, by
}

func (l *textLab) eadmCount(t *testing.T, q string, args ...any) (n int) {
	t.Helper()
	if err := l.a.st.Reader().QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestEbookAdminSections(t *testing.T) {
	l := newTextLab(t)
	l.run(t)
	if body := l.get(l.owner, "/admin/ebooks", false).Body.String(); !strings.Contains(body, "Nothing here needs a look.") {
		t.Error("the empty page lacks its sentence")
	}
	maybe := l.eadmText(t, "maybe.epub", "Zzyzx Almanac", "Imogen Textauthor") // same author as the audiobook only
	alone := l.eadmText(t, "alone.epub", "Quite Another Tale", "Nobody Known") // matches nothing: an ordinary book
	if maybe.BookID == "" || alone.BookID == "" {
		t.Fatal("the ebooks did not become books")
	}
	l.addPlaced(t, "waiting.epub", "pending", "")
	drm := l.addPlaced(t, "locked.epub", "refused", "drm")
	failed := l.addPlaced(t, "failed.epub", "failed", "")
	for _, id := range []string{drm, failed} {
		if _, err := l.a.st.Writer().Exec(`UPDATE ebook_files SET book_id = NULL, paired_by = NULL WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := l.a.st.Writer().Exec(`UPDATE ebook_files SET book_id = NULL, paired_by = NULL WHERE state = 'pending'`); err != nil {
		t.Fatal(err)
	}
	rec := l.get(l.owner, "/admin/ebooks", false)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("page: %d", rec.Code)
	}
	eadmHas(t, body, ">Ebooks<", "Maybe the same book", "inbox-"+maybe.ID, "Zzyzx Almanac", "Textword Lantern",
		"Make one book", "Not the same book", "Keep it as an ebook on its own", "/admin/ebooks/"+maybe.ID+"/pair",
		"/admin/ebooks/"+maybe.ID+"/not-same", "/admin/ebooks/"+maybe.ID+"/keep",
		"Could not be opened", "Reading waiting.epub…", "This ebook is copy-protected")
	eadmLacks(t, body, "Quite Another Tale", "Kept on their own", "Ebooks to pair", "People who can see this ebook today")
	if i, j := strings.Index(body, "Maybe the same book"), strings.Index(body, "Could not be opened"); i > j {
		t.Error("the sections are in the wrong order")
	}
	// A download only for a file that failed to read, never for a copy-protected one.
	eadmHas(t, body, "/admin/ebooks/"+failed+"/file")
	eadmLacks(t, body, "/admin/ebooks/"+drm+"/file")
	// The libraries page links the page by name, with no count.
	lib := l.get(l.owner, "/admin/libraries", false).Body.String()
	eadmHas(t, lib, `href="/admin/ebooks"`, ">Ebooks<")
	eadmLacks(t, lib, "to pair")
}

// A confident match the engine's guard refuses (a grant on the ebook would widen) is a suggestion that says so.
func TestEbookAdminGuardSentence(t *testing.T) {
	l := newTextLab(t)
	l.run(t)
	f := l.textPairAdd(t, "guarded.epub", "Textword Lantern", "Imogen Textauthor", "") // same title and author as the audiobook
	id, err := l.a.lib.CreateTextBook(context.Background(), library.TextBookIn{LibraryID: l.book.LibraryID, FolderID: l.book.FolderID,
		RootPath: filepath.Join(l.a.dir, "ebooks", f.ID), Title: "Textword Lantern"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := l.a.st.Writer().Exec(`UPDATE ebook_files SET book_id = ?, book_default = 1, paired_by = NULL WHERE id = ?`, id, f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.a.st.Writer().Exec(`INSERT INTO grants(id, user_id, scope, book_id, created_at, updated_at) VALUES ('gr_text', ?, 'book', ?, 1, 1)`,
		sweepUserID(t, l.a, l.reader), id); err != nil {
		t.Fatal(err)
	}
	if err := l.svc.PairBook(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if book, _, _ := l.eadmRow(t, f.ID); book != id {
		t.Fatal("the engine joined a book the guard must refuse")
	}
	body := l.get(l.owner, "/admin/ebooks", false).Body.String()
	eadmHas(t, body, "Maybe the same book", "People who can see this ebook today lose it, or may gain the audiobook. Check who has access first.", "Make one book")
}

func TestEbookAdminMerge(t *testing.T) {
	l := newTextLab(t)
	l.run(t)
	f := l.eadmText(t, "join.epub", "Zzyzx Almanac", "Imogen Textauthor")
	text := f.BookID
	// A book grant on the ebook goes along with a person's join, and the page says so first.
	if _, err := l.a.st.Writer().Exec(`INSERT INTO grants(id, user_id, scope, book_id, created_at, updated_at) VALUES ('gr_text', ?, 'book', ?, 1, 1)`,
		sweepUserID(t, l.a, l.reader), text); err != nil {
		t.Fatal(err)
	}
	eadmHas(t, l.get(l.owner, "/admin/ebooks/"+f.ID, false).Body.String(), "People who can see this ebook today lose it, or may gain the audiobook. Check who has access first.", "Make one book")
	// Unknown, empty and ebook targets are refused without moving anything.
	if got := l.eadmPost(t, l.owner, "/admin/ebooks/"+f.ID+"/pair", map[string]string{"book": "nobook"}, false).Code; got != 404 {
		t.Errorf("unknown book: %d", got)
	}
	if got := l.eadmPost(t, l.owner, "/admin/ebooks/"+f.ID+"/pair", map[string]string{"book": text}, false).Code; got != 404 {
		t.Errorf("an ebook as the target: %d", got)
	}
	rec := l.eadmPost(t, l.owner, "/admin/ebooks/"+f.ID+"/pair", nil, true)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "Choose a book first.") || strings.Contains(rec.Body.String(), "<html") {
		t.Errorf("htmx, no book: %d %q", rec.Code, rec.Body.String())
	}
	if book, _, _ := l.eadmRow(t, f.ID); book != text {
		t.Fatal("a refused join moved the file")
	}
	rec = l.eadmPost(t, l.owner, "/admin/ebooks/"+f.ID+"/pair", map[string]string{"book": l.book.ID}, false)
	if rec.Code != 303 || rec.Header().Get("Location") != "/admin/ebooks" {
		t.Fatalf("join: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	book, hold, by := l.eadmRow(t, f.ID)
	if book != l.book.ID || by != "person" || hold != "" {
		t.Errorf("file after the join: book %q hold %q by %q", book, hold, by)
	}
	if l.eadmCount(t, `SELECT count(*) FROM books WHERE id = ?`, text) != 0 || l.eadmCount(t, `SELECT count(*) FROM merged_books WHERE from_id = ? AND to_id = ?`, text, l.book.ID) != 1 {
		t.Error("the ebook's book was not merged away with its alias")
	}
	if l.eadmCount(t, `SELECT count(*) FROM grants WHERE id = 'gr_text'`) != 0 {
		t.Error("the grant stayed on a book that no longer exists")
	}
	if l.eadmCount(t, `SELECT count(*) FROM audit_log WHERE action = 'ebook.merge' AND user_id = ?`, l.ownerID) != 1 {
		t.Error("no audit row for the person's join")
	}
	// The old link answers with a redirect to the audiobook.
	if rec := l.get(l.owner, "/books/"+text, false); rec.Code != http.StatusPermanentRedirect {
		t.Errorf("old ebook link: %d", rec.Code)
	}
	if body := l.get(l.owner, "/admin/ebooks", false).Body.String(); strings.Contains(body, "inbox-"+f.ID) {
		t.Error("a joined ebook is still listed")
	}
}

func TestEbookAdminNotSame(t *testing.T) {
	l := newTextLab(t)
	l.run(t)
	f := l.eadmText(t, "no.epub", "Zzyzx Almanac", "Imogen Textauthor")
	g := l.eadmText(t, "keepme.epub", "Yyx Almanac", "Imogen Textauthor")
	own := f.BookID
	if rec := l.eadmPost(t, l.owner, "/admin/ebooks/"+f.ID+"/not-same", map[string]string{"book": l.book.ID}, false); rec.Code != 303 {
		t.Fatalf("not the same book: %d", rec.Code)
	}
	if rec := l.eadmPost(t, l.owner, "/admin/ebooks/"+g.ID+"/keep", nil, false); rec.Code != 303 {
		t.Fatalf("keep: %d", rec.Code)
	}
	if book, hold, _ := l.eadmRow(t, f.ID); book != own || hold != "" {
		t.Errorf("not-same moved or held the file: book %q hold %q", book, hold)
	}
	if book, hold, _ := l.eadmRow(t, g.ID); book != g.BookID || hold != "kept" {
		t.Errorf("keep: book %q hold %q", book, hold)
	}
	if l.eadmCount(t, `SELECT count(*) FROM ebook_not_same WHERE ebook_file_id = ? AND book_id = ?`, f.ID, l.book.ID) != 1 {
		t.Error("the answer was not remembered")
	}
	body := l.get(l.owner, "/admin/ebooks", false).Body.String()
	eadmLacks(t, body, "inbox-"+f.ID, "inbox-"+g.ID)
	// The engine and a later catch-up leave it alone too.
	l.svc.Finisher()(context.Background(), l.book.LibraryID)
	if book, _, _ := l.eadmRow(t, f.ID); book != own {
		t.Error("the engine joined a book a person said was not the same")
	}
	// A refused target answers 404 and writes nothing.
	if got := l.eadmPost(t, l.owner, "/admin/ebooks/"+g.ID+"/not-same", map[string]string{"book": "nobook"}, false).Code; got != 404 {
		t.Errorf("unknown book: %d", got)
	}
}

// A merged book splits again from the Text tab ("Not the same book"), keeps its old id, and is not suggested again.
func TestEbookAdminSplitRemembered(t *testing.T) {
	l := newTextLab(t)
	l.run(t)
	f := l.eadmText(t, "split.epub", "Zzyzx Almanac", "Imogen Textauthor")
	old := f.BookID
	if rec := l.eadmPost(t, l.owner, "/admin/ebooks/"+f.ID+"/pair", map[string]string{"book": l.book.ID}, false); rec.Code != 303 {
		t.Fatalf("join: %d", rec.Code)
	}
	if rec := l.eadmPost(t, l.owner, l.path("/"+f.ID+"/unpair"), nil, false); rec.Code != 303 {
		t.Fatalf("split: %d", rec.Code)
	}
	book, hold, _ := l.eadmRow(t, f.ID)
	if book != old || hold != "person" {
		t.Errorf("after the split: book %q (want %q) hold %q", book, old, hold)
	}
	body := l.get(l.owner, "/admin/ebooks", false).Body.String()
	eadmLacks(t, body, "inbox-"+f.ID)
	l.svc.Finisher()(context.Background(), l.book.LibraryID)
	if book, _, _ := l.eadmRow(t, f.ID); book != old {
		t.Error("the engine joined a split book again")
	}
	// The Text tab of the text book offers a way to the audiobook, and no "Not the same book" for its only file.
	tab := l.get(l.owner, "/books/"+old+"/text", false).Body.String()
	eadmHas(t, tab, "Find the audiobook", "/admin/ebooks/"+f.ID)
	eadmLacks(t, tab, "Not the same book")
}

func TestEbookAdminRoles(t *testing.T) {
	l := newTextLab(t)
	l.run(t)
	f := l.eadmText(t, "roles.epub", "Zzyzx Almanac", "Imogen Textauthor")
	failed := l.addPlaced(t, "roles-failed.epub", "failed", "")
	if _, err := l.a.st.Writer().Exec(`UPDATE ebook_files SET book_id = NULL, paired_by = NULL WHERE id = ?`, failed); err != nil {
		t.Fatal(err)
	}
	for _, who := range []struct {
		name string
		ck   *http.Cookie
		ok   bool
	}{{"member", l.reader, false}, {"member no read", l.noRead, false}, {"guest", l.guest, false}, {"guest no read", l.guestNoR, false}, {"admin", l.admin, true}, {"owner", l.owner, true}} {
		want := map[bool]int{true: 200, false: 404}[who.ok]
		for _, path := range []string{"/admin/ebooks", "/admin/ebooks/" + f.ID} {
			if got := l.get(who.ck, path, false).Code; got != want {
				t.Errorf("%s GET %s: %d, want %d", who.name, path, got, want)
			}
		}
		if who.ok {
			continue
		}
		// The failed file has no bytes in this test, so even staff get 404 there; a member never gets anything else.
		if got := l.get(who.ck, "/admin/ebooks/"+failed+"/file", false).Code; got != 404 {
			t.Errorf("%s GET file: %d, want 404", who.name, got)
		}
		for _, verb := range []string{"pair", "not-same", "keep"} {
			if got := l.eadmPost(t, who.ck, "/admin/ebooks/"+f.ID+"/"+verb, map[string]string{"book": l.book.ID}, false).Code; got != 404 {
				t.Errorf("%s POST %s: %d, want 404", who.name, verb, got)
			}
		}
	}
	if book, hold, _ := l.eadmRow(t, f.ID); book != f.BookID || hold != "" || l.eadmCount(t, `SELECT count(*) FROM ebook_not_same`) != 0 {
		t.Errorf("a refused person changed the file: book %q hold %q", book, hold)
	}
	if rec := l.a.do(http.MethodGet, "/admin/ebooks", nil, nil); rec.Code == 200 {
		t.Error("anonymous GET /admin/ebooks: 200")
	}
}

// Only audiobooks are candidates, and only ones that are there: a text book, a missing audiobook never appear.
func TestEbookAdminFilter(t *testing.T) {
	l := newTextLab(t)
	l.run(t)
	f := l.eadmText(t, "filter.epub", "Zzyzx Almanac", "Imogen Textauthor")
	other := l.eadmText(t, "other.epub", "Textword Lantern Companion", "Imogen Textauthor")
	if body := l.get(l.owner, "/admin/ebooks/"+f.ID, false).Body.String(); strings.Contains(body, "Companion") {
		t.Error("another ebook is offered as an audiobook")
	}
	if body := l.get(l.owner, "/admin/ebooks/"+f.ID+"?q=Companion", false).Body.String(); strings.Contains(body, "Textword Lantern Companion") {
		t.Errorf("the search lists an ebook: %v", other.ID)
	}
	if _, err := l.a.st.Writer().Exec(`UPDATE books SET missing_since = 1 WHERE id = ?`, l.book.ID); err != nil {
		t.Fatal(err)
	}
	body := l.get(l.owner, "/admin/ebooks", false).Body.String()
	eadmLacks(t, body, "Maybe the same book", "inbox-"+f.ID)
	eadmLacks(t, l.get(l.owner, "/admin/ebooks/"+f.ID, false).Body.String(), "Textword Lantern")
}

func TestEbookAdminDownload(t *testing.T) {
	l := newTextLab(t)
	l.run(t)
	failed := l.addPlaced(t, "dl-failed.epub", "failed", "")
	drm := l.addPlaced(t, "dl-locked.epub", "refused", "drm")
	ready := l.addPlaced(t, "dl-ready.epub", "ready", "")
	var sha string
	if err := l.a.st.Reader().QueryRow(`SELECT sha256 FROM ebook_files WHERE id = ?`, failed).Scan(&sha); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(l.a.dir, "ebooks"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.a.dir, "ebooks", sha+".epub"), []byte("original bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{failed, drm, ready} {
		if _, err := l.a.st.Writer().Exec(`UPDATE ebook_files SET book_id = NULL, paired_by = NULL WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
	}
	rec := l.get(l.owner, "/admin/ebooks/"+failed+"/file", false)
	if rec.Code != 200 || rec.Body.String() != "original bytes" ||
		!strings.HasPrefix(rec.Header().Get("Content-Disposition"), "attachment") || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("download: %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	for name, id := range map[string]string{"copy-protected": drm, "ready": ready, "unknown": "nope"} {
		if got := l.get(l.owner, "/admin/ebooks/"+id+"/file", false).Code; got != 404 {
			t.Errorf("%s: %d, want 404", name, got)
		}
	}
	// Reading the file changed nothing in the library.
	if l.eadmCount(t, `SELECT count(*) FROM ebook_files WHERE id = ? AND state = 'failed'`, failed) != 1 {
		t.Error("the file's row changed")
	}
}
