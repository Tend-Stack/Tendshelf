package web

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// textLab is an app with the real ebook service (its job runs only when a test starts the runner),
// one library with one book, and one person of each kind: the owner, an admin, readers who hold a
// grant with can_read, and people who hold one without it.
type textLab struct {
	a                               *testApp
	svc                             *ebook.Service
	book                            library.Book
	owner, admin                    *http.Cookie
	reader, noRead, guest, guestNoR *http.Cookie
	ownerID                         string
}

func newTextLab(t *testing.T) *textLab {
	t.Helper()
	a := newTestApp(t)
	l := &textLab{a: a}
	l.owner = a.as(t, users.RoleOwner)
	l.ownerID = sweepUserID(t, a, l.owner)
	l.admin = a.as(t, users.RoleAdmin)
	lib, folder := librarytest.NewLibrary(t, a.lib, "Text library", adminAudioDir(t, true))
	l.book = librarytest.AddBook(t, a.lib, folder, librarytest.BookSpec{Title: "Textword Lantern", Authors: []string{"Imogen Textauthor"}, DurationMs: sweepHourMs, AddedAt: 5, Files: 1})
	grant := func(role users.Role, read int) *http.Cookie {
		ck := a.as(t, role)
		id := sweepUserID(t, a, ck)
		if _, err := a.st.Writer().Exec(`INSERT INTO grants(id, user_id, scope, library_id, can_download, can_read, created_at, updated_at)
			VALUES (?, ?, 'library', ?, 0, ?, 1, 1)`, "gr_"+id, id, lib.ID, read); err != nil {
			t.Fatal(err)
		}
		return ck
	}
	l.reader, l.noRead = grant(users.RoleMember, 1), grant(users.RoleMember, 0)
	l.guest, l.guestNoR = grant(users.RoleGuest, 1), grant(users.RoleGuest, 0)
	l.svc = ebook.New(a.st, a.lib, a.runner, a.dir, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := l.svc.Register(a.runner); err != nil {
		t.Fatal(err)
	}
	a.srv.ebooks = l.svc
	return l
}

func (l *textLab) path(suffix string) string { return "/books/" + l.book.ID + "/text" + suffix }

func (l *textLab) get(ck *http.Cookie, path string, htmx bool) *httptest.ResponseRecorder {
	return l.a.do(http.MethodGet, path, nil, func(r *http.Request) {
		if htmx {
			r.Header.Set("HX-Request", "true")
		}
	}, ck)
}

// post sends a multipart form with the token and, when name is not empty, one file part.
func (l *textLab) post(t *testing.T, ck *http.Cookie, path, name string, data []byte, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if err := mw.WriteField("_csrf", l.a.tokenFor(t, ck)); err != nil {
		t.Fatal(err)
	}
	if name != "" {
		fw, err := mw.CreateFormFile("file", name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write(data)
	}
	_ = mw.Close()
	req := httptest.NewRequest(http.MethodPost, path, &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if htmx {
		req.Header.Set("HX-Request", "true")
	}
	req.AddCookie(ck)
	rec := httptest.NewRecorder()
	l.a.h.ServeHTTP(rec, req)
	return rec
}

// postForm sends a urlencoded form with the token (the dismiss button).
func (l *textLab) postForm(t *testing.T, ck *http.Cookie, path string, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	form := map[string][]string{"_csrf": {l.a.tokenFor(t, ck)}}
	return l.a.do(http.MethodPost, path, form, func(r *http.Request) {
		if htmx {
			r.Header.Set("HX-Request", "true")
		}
	}, ck)
}

// epub builds a synthetic EPUB with n chapters after a title page.
func textEPUB(t *testing.T, title string, n int) []byte {
	t.Helper()
	chs := []ebooktest.EPUBChapter{{Title: "Title Page", EpubType: "titlepage", Paras: []string{title}}}
	for i := 1; i <= n; i++ {
		chs = append(chs, ebooktest.EPUBChapter{Title: fmt.Sprintf("Chapter %d", i), EpubType: "chapter",
			Paras: []string{fmt.Sprintf("The first line of chapter %d of %s. It goes on for a while.", i, title), "A second paragraph follows here."}})
	}
	return ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: title, Authors: []string{"A. Writer"}, Chapters: chs})
}

// run starts the job runner until the test ends.
func (l *textLab) run(t *testing.T) {
	t.Helper()
	if err := l.a.runner.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.a.runner.Stop(context.Background()) })
}

// addReady uploads an EPUB as the owner and waits for its read to finish.
func (l *textLab) addReady(t *testing.T, name, title string, n int) ebook.File {
	t.Helper()
	f, err := l.svc.AddUpload(context.Background(), l.book, l.ownerID, name, bytes.NewReader(textEPUB(t, title, n)))
	if err != nil {
		t.Fatal(err)
	}
	return l.waitState(t, f.ID, "ready")
}

func (l *textLab) waitState(t *testing.T, id, state string) ebook.File {
	t.Helper()
	for end := time.Now().Add(20 * time.Second); time.Now().Before(end); time.Sleep(25 * time.Millisecond) {
		f, ok, err := l.svc.Store().File(context.Background(), id)
		if err != nil || !ok {
			t.Fatalf("file %s: %v %v", id, ok, err)
		}
		if f.State == state {
			return f
		}
	}
	t.Fatalf("file %s never became %s", id, state)
	return ebook.File{}
}

// addPlaced inserts a file row of an upload in the given state (no job runs for it).
func (l *textLab) addPlaced(t *testing.T, name, state, reason string) string {
	t.Helper()
	id := fmt.Sprintf("ebf_%s_%s", state, strings.ReplaceAll(name, ".", "_"))
	sha := fmt.Sprintf("%064x", len(id)*7919+int(name[0]))
	var r any
	if reason != "" {
		r = reason
	}
	if _, err := l.a.st.Writer().Exec(`INSERT INTO ebook_files(id, origin, library_id, book_id, paired_by, ext, size, mtime_ns, quick_hash,
		sha256, state, refused_reason, original_name, uploaded_by, created_at, updated_at)
		VALUES (?, 'upload', ?, ?, 'upload', 'epub', 10, 0, ?, ?, ?, ?, ?, ?, 1, 1)`,
		id, l.book.LibraryID, l.book.ID, sha, sha, state, r, name, l.ownerID); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestTextTabVisibility(t *testing.T) {
	l := newTextLab(t)
	l.run(t)
	tab := `id="book-tab-text"`
	type who struct {
		name string
		ck   *http.Cookie
	}
	editors := []who{{"owner", l.owner}, {"admin", l.admin}}
	readers := []who{{"member with can_read", l.reader}, {"guest with can_read", l.guest}}
	refused := []who{{"member without can_read", l.noRead}, {"guest without can_read", l.guestNoR}}

	// No ebook yet: editors have the tab, everybody else a 404 and no tab.
	for _, p := range editors {
		if rec := l.get(p.ck, l.path(""), false); rec.Code != 200 || !strings.Contains(rec.Body.String(), "Add an ebook") {
			t.Errorf("%s, no edition: %d", p.name, rec.Code)
		}
		if rec := l.get(p.ck, "/books/"+l.book.ID, false); !strings.Contains(rec.Body.String(), tab) {
			t.Errorf("%s: no Text tab on the book page", p.name)
		}
	}
	for _, p := range append(append([]who(nil), readers...), refused...) {
		if rec := l.get(p.ck, l.path(""), false); rec.Code != 404 {
			t.Errorf("%s, no edition: %d, want 404", p.name, rec.Code)
		}
		if rec := l.get(p.ck, "/books/"+l.book.ID, false); rec.Code != 200 || strings.Contains(rec.Body.String(), tab) {
			t.Errorf("%s: book page %d or has a Text tab", p.name, rec.Code)
		}
	}

	// A pending ebook is for editors only: readers still see nothing.
	l.addPlaced(t, "waiting.epub", "pending", "")
	for _, p := range readers {
		if rec := l.get(p.ck, l.path(""), false); rec.Code != 404 {
			t.Errorf("%s, pending only: %d, want 404", p.name, rec.Code)
		}
	}

	// With a ready edition, readers who may read get it, the others still a 404.
	l.addReady(t, "lantern.epub", "Lantern", 3)
	for _, p := range append(append([]who(nil), editors...), readers...) {
		rec := l.get(p.ck, l.path(""), false)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Chapter 2") {
			t.Errorf("%s, ready edition: %d", p.name, rec.Code)
		}
		if page := l.get(p.ck, "/books/"+l.book.ID, false); !strings.Contains(page.Body.String(), tab) {
			t.Errorf("%s: no Text tab with a ready edition", p.name)
		}
	}
	for _, p := range refused {
		if rec := l.get(p.ck, l.path(""), false); rec.Code != 404 {
			t.Errorf("%s, ready edition: %d, want 404", p.name, rec.Code)
		}
		if page := l.get(p.ck, "/books/"+l.book.ID, false); strings.Contains(page.Body.String(), tab) {
			t.Errorf("%s: Text tab without can_read", p.name)
		}
	}
	// Anonymous visitors are sent to sign in.
	if rec := l.a.do(http.MethodGet, l.path(""), nil, nil); rec.Code == 200 {
		t.Errorf("anonymous: %d", rec.Code)
	}
}

func TestTextTabLists(t *testing.T) {
	l := newTextLab(t)
	l.run(t)
	l.addReady(t, "lantern.epub", "Lantern", 14)
	pending := l.addPlaced(t, "waiting.epub", "pending", "")
	drm := l.addPlaced(t, "locked.epub", "refused", "drm")
	l.addPlaced(t, "pictures.epub", "refused", "no_text")

	body := l.get(l.owner, l.path(""), false).Body.String()
	for _, want := range []string{
		"Add an ebook", "Choose an ebook file", "Add this ebook", "Or add a file",
		"EPUB · 15 chapters", "Added from this device", // the title page counts as a chapter
		"The first line of chapter 1 of Lantern.", "Front matter", "Show all 15 chapters", "?all=1",
		"Reading waiting.epub…", `id="ebook-file-` + pending + `"`,
		"This ebook is copy-protected", "can&#39;t pair locked.epub with the audiobook. Nothing was changed.",
		"Choose another file", "Why can&#39;t Tendshelf open it?", "Textword Lantern still plays as an audiobook.",
		"Tendshelf found no text in this file.", `id="ebook-file-` + drm + `"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("owner page lacks %q", want)
		}
	}
	if strings.Contains(body, "Chapter 14") || strings.Contains(body, "Chapter 13") {
		t.Error("the list shows more than the first twelve chapters")
	}
	if strings.Contains(body, "up to 200 MB. No copy protection. Kept on your server.") == false || strings.Contains(body, "next to the audio") {
		t.Error("the upload hint is not the operator's wording")
	}
	all := l.get(l.owner, l.path("?all=1"), false).Body.String()
	if !strings.Contains(all, "Chapter 14") || strings.Contains(all, "Show all 15") {
		t.Error("?all=1 does not list every chapter")
	}

	// A reader sees the ready edition only: no pending, no refused, no form, no folder path.
	rd := l.get(l.reader, l.path(""), false).Body.String()
	if !strings.Contains(rd, "Chapter 2") {
		t.Error("the reader does not see the chapters")
	}
	for _, bad := range []string{"waiting.epub", "locked.epub", "pictures.epub", "copy-protected", "Add this ebook", "Added from this device", "Add an ebook"} {
		if strings.Contains(rd, bad) {
			t.Errorf("the reader's page shows %q", bad)
		}
	}
}

func TestTextTabUpload(t *testing.T) {
	l := newTextLab(t)
	good := textEPUB(t, "Upload Lantern", 2)

	// htmx: 200, the files and form again, and the new file polls.
	rec := l.post(t, l.owner, l.path(""), "up.epub", good, true)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `hx-trigger="every 2s"`) || !strings.Contains(body, "Reading up.epub…") {
		t.Fatalf("htmx upload: %d %s", rec.Code, body)
	}
	if strings.Contains(body, "<html") {
		t.Error("htmx upload answered a whole page")
	}
	files, _ := l.svc.Store().FilesForBook(context.Background(), l.book.ID)
	if len(files) != 1 || files[0].Origin != "upload" || files[0].State != "pending" {
		t.Fatalf("files after upload: %+v", files)
	}

	// The same bytes again are a duplicate; the message sits in the form.
	rec = l.post(t, l.owner, l.path(""), "again.epub", good, true)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "This ebook is already on this book.") {
		t.Errorf("duplicate: %d %s", rec.Code, rec.Body.String())
	}

	cases := []struct {
		name  string
		file  string
		data  []byte
		code  int
		words string
	}{
		{"no file", "", nil, 422, "Choose a file first."},
		{"empty", "e.epub", []byte{}, 422, "That file is empty."},
		{"unsupported", "x.exe", []byte("MZ"), 422, "Tendshelf cannot read this kind of file."},
		{"kfx", "x.kfx", []byte("x"), 422, "This ebook is copy-protected, so Tendshelf will not open it."},
		{"aax", "book.aax", []byte("x"), 422, "Tendshelf cannot read this kind of file."},
	}
	for _, c := range cases {
		for _, htmx := range []bool{true, false} {
			rec := l.post(t, l.owner, l.path(""), c.file, c.data, htmx)
			if rec.Code != c.code || !strings.Contains(rec.Body.String(), c.words) {
				t.Errorf("%s (htmx %v): %d, want %d with %q", c.name, htmx, rec.Code, c.code, c.words)
			}
			if !htmx && !strings.Contains(rec.Body.String(), "<html") {
				t.Errorf("%s: a plain form got a fragment", c.name)
			}
			if htmx && strings.Contains(rec.Body.String(), "<html") {
				t.Errorf("%s: htmx got a whole page", c.name)
			}
		}
	}

	// Plain form: 303 back to the tab, which shows the file.
	rec = l.post(t, l.owner, l.path(""), "plain.epub", textEPUB(t, "Plain Lantern", 2), false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != l.path("") {
		t.Errorf("plain upload: %d %s", rec.Code, rec.Header().Get("Location"))
	}

	// Nobody who may not edit the book gets in, and nothing is stored.
	before, _ := l.svc.Store().FilesForBook(context.Background(), l.book.ID)
	for name, ck := range map[string]*http.Cookie{"admin ok": l.admin, "reader": l.reader, "noRead": l.noRead, "guest": l.guest} {
		rec := l.post(t, ck, l.path(""), "z.epub", textEPUB(t, "Z "+name, 2), false)
		want := http.StatusNotFound
		if name == "admin ok" {
			want = http.StatusSeeOther
		}
		if rec.Code != want {
			t.Errorf("%s upload: %d, want %d", name, rec.Code, want)
		}
	}
	after, _ := l.svc.Store().FilesForBook(context.Background(), l.book.ID)
	if len(after) != len(before)+1 {
		t.Errorf("files %d -> %d, want exactly the admin's upload", len(before), len(after))
	}
	if rec := l.post(t, l.owner, "/books/bk_none/text", "z.epub", good, true); rec.Code != 404 {
		t.Errorf("unknown book: %d", rec.Code)
	}
	// Without the token the house CSRF refusal answers.
	req := httptest.NewRequest(http.MethodPost, l.path(""), strings.NewReader(""))
	req.AddCookie(l.owner)
	rr := httptest.NewRecorder()
	l.a.h.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("no token: %d", rr.Code)
	}
}

func TestTextTabNoJS(t *testing.T) {
	l := newTextLab(t)
	l.run(t)
	page := l.get(l.owner, l.path(""), false).Body.String()
	for _, want := range []string{`method="post"`, `enctype="multipart/form-data"`, `name="file"`, `name="_csrf"`} {
		if !strings.Contains(page, want) {
			t.Errorf("the form lacks %q", want)
		}
	}
	rec := l.post(t, l.owner, l.path(""), "nojs.epub", textEPUB(t, "No JS Lantern", 3), false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("plain upload: %d", rec.Code)
	}
	files, _ := l.svc.Store().FilesForBook(context.Background(), l.book.ID)
	l.waitState(t, files[0].ID, "ready")
	if body := l.get(l.owner, rec.Header().Get("Location"), false).Body.String(); !strings.Contains(body, "Chapter 3") {
		t.Error("the tab does not show the chapters after a plain upload")
	}
}

func TestTextTabPoll(t *testing.T) {
	l := newTextLab(t)
	f, err := l.svc.AddUpload(context.Background(), l.book, l.ownerID, "poll.epub", bytes.NewReader(textEPUB(t, "Poll Lantern", 2)))
	if err != nil || f.JobID == "" {
		t.Fatalf("upload: %v %+v", err, f)
	}
	poll := "/hx/jobs/" + f.JobID
	rec := l.get(l.owner, poll, true)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `hx-trigger="every 2s"`) || !strings.Contains(rec.Body.String(), "Reading poll.epub…") {
		t.Errorf("pending poll: %d %s", rec.Code, rec.Body.String())
	}
	for name, ck := range map[string]*http.Cookie{"reader": l.reader, "noRead": l.noRead, "guest": l.guest} {
		if rec := l.get(ck, poll, true); rec.Code != 404 {
			t.Errorf("%s poll: %d, want 404", name, rec.Code)
		}
	}
	if rec := l.get(l.admin, poll, true); rec.Code != 200 {
		t.Errorf("admin poll: %d", rec.Code)
	}
	l.run(t)
	l.waitState(t, f.ID, "ready")
	rec = l.get(l.owner, poll, true)
	body := rec.Body.String()
	if rec.Code != 200 || strings.Contains(body, "every 2s") || !strings.Contains(body, "Chapter 1") {
		t.Errorf("finished poll: %d %s", rec.Code, body)
	}
}

func TestTextTabDismiss(t *testing.T) {
	l := newTextLab(t)
	drm := l.addPlaced(t, "locked.epub", "refused", "drm")
	pend := l.addPlaced(t, "waiting.epub", "pending", "")
	path := func(id string) string { return l.path("/" + id + "/dismiss") }

	for name, ck := range map[string]*http.Cookie{"reader": l.reader, "noRead": l.noRead, "guest": l.guest} {
		if rec := l.postForm(t, ck, path(drm), true); rec.Code != 404 {
			t.Errorf("%s dismiss: %d, want 404", name, rec.Code)
		}
	}
	// A file that is still being read cannot be cleared.
	if rec := l.postForm(t, l.owner, path(pend), true); rec.Code != 404 {
		t.Errorf("dismiss pending: %d, want 404", rec.Code)
	}
	if rec := l.postForm(t, l.owner, l.path("/ebf_none/dismiss"), true); rec.Code != 404 {
		t.Errorf("dismiss unknown: %d, want 404", rec.Code)
	}
	rec := l.postForm(t, l.owner, path(drm), true)
	if rec.Code != 200 || rec.Body.Len() != 0 {
		t.Errorf("htmx dismiss: %d %q", rec.Code, rec.Body.String())
	}
	if _, ok, _ := l.svc.Store().File(context.Background(), drm); ok {
		t.Error("the refused upload is still there")
	}
	again := l.addPlaced(t, "locked.epub", "refused", "drm")
	rec = l.postForm(t, l.admin, path(again), false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != l.path("") {
		t.Errorf("plain dismiss: %d %s", rec.Code, rec.Header().Get("Location"))
	}
}
