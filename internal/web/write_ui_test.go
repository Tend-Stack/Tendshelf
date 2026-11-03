package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"html"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/scan"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// writeLab is an app with the real write job on the fake ffmpeg and one scanned book of fake m4b
// files in a library folder. The job runner is not started until a test needs the job to run.
type writeLab struct {
	a                           *testApp
	writes                      *mediajobs.Writes
	book                        library.Book
	root                        string
	files                       []string
	owner, admin, member, guest *http.Cookie
}

// newWriteLab makes a book of n fake files. They are backdated past the 10 minute rule unless recent.
func newWriteLab(t *testing.T, n int, recent bool) *writeLab {
	t.Helper()
	ctx := context.Background()
	a := newTestApp(t)
	tools := mediatest.FakeTools(t)
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	l := &writeLab{a: a}
	l.owner = a.as(t, users.RoleOwner)
	l.admin = a.as(t, users.RoleAdmin)
	l.member = a.as(t, users.RoleMember)
	l.guest = a.as(t, users.RoleGuest)
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lib, err := a.lib.CreateLibrary(ctx, "Write library", 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.lib.AddFolder(ctx, lib.ID, root); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "Lantern Book")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= n; i++ {
		name := "01.m4b"
		if n > 1 {
			name = "Part " + strconv.Itoa(i) + ".m4b"
		}
		p := filepath.Join(dir, name)
		mediatest.WriteFake(t, p, mediatest.FakeFile{PadBytes: 3000 + 100*i,
			Tags: map[string]string{"album": "Old Title", "artist": "Ann Author", "comment": name}})
		if !recent {
			old := time.Now().Add(-time.Hour)
			if err := os.Chtimes(p, old, old); err != nil {
				t.Fatal(err)
			}
		}
		l.files = append(l.files, p)
	}
	l.root = dir
	sc := scan.New(a.lib, tools, a.runner, quiet)
	if _, err := sc.ScanLibrary(ctx, lib.ID, nil); err != nil {
		t.Fatal(err)
	}
	ids, err := a.lib.UnsettledAudioFileIDs(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if _, err := sc.ProbeFile(ctx, id, nil); err != nil {
			t.Fatal(err)
		}
	}
	if l.book, err = a.lib.BookByRootPath(ctx, dir); err != nil {
		t.Fatal(err)
	}
	for i, ck := range []*http.Cookie{l.member, l.guest} {
		if _, err := a.st.Writer().Exec(`INSERT INTO grants(id, user_id, scope, library_id, can_download, can_read, created_at, updated_at)
			VALUES (?, ?, 'library', ?, 1, 0, 1, 1)`, "gr_write_"+strconv.Itoa(i), sweepUserID(t, a, ck), lib.ID); err != nil {
			t.Fatal(err)
		}
	}
	l.writes = mediajobs.NewWrites(a.lib, tools, a.dir, a.runner, a.runner, quiet)
	if err := mediajobs.Register(a.runner, nil, nil, nil, l.writes); err != nil {
		t.Fatal(err)
	}
	a.srv.writes = l.writes
	return l
}

// start runs the job runner until the test ends.
func (l *writeLab) start(t *testing.T) {
	t.Helper()
	if err := l.a.runner.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.a.runner.Stop(context.Background()) })
}

func (l *writeLab) get(ck *http.Cookie, path string, htmx bool) *httptest.ResponseRecorder {
	var mod func(*http.Request)
	if htmx {
		mod = hx
	}
	return l.a.do(http.MethodGet, path, nil, mod, ck)
}

func (l *writeLab) post(t *testing.T, ck *http.Cookie, path string) *httptest.ResponseRecorder {
	t.Helper()
	return l.a.do(http.MethodPost, path, url.Values{"_csrf": {l.a.tokenFor(t, ck)}}, nil, ck)
}

func (l *writeLab) sheet() string { return "/books/" + l.book.ID + "/details?sheet=write" }
func (l *writeLab) tab() string   { return "/books/" + l.book.ID + "/details" }

// jobOf reads the job id from the Location of a started write.
func (l *writeLab) jobOf(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	loc := location(rec)
	prefix := l.sheet() + "&job="
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(loc, prefix) {
		t.Fatalf("start write: %d, Location %q, body %q", rec.Code, loc, rec.Body.String())
	}
	return strings.TrimPrefix(loc, prefix)
}

// waitDone polls the job like the sheet does until it is no longer waiting, and returns the last body.
func (l *writeLab) waitDone(t *testing.T, ck *http.Cookie, jobID string) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		rec := l.get(ck, "/hx/jobs/"+jobID, true)
		if rec.Code != http.StatusOK {
			t.Fatalf("poll: %d %s", rec.Code, rec.Body.String())
		}
		if body := rec.Body.String(); !strings.Contains(body, "every 2s") {
			return body
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s still waiting", jobID)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func fileDigest(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// listing is every entry of the book folder with its size and modification time.
func (l *writeLab) listing(t *testing.T) []string {
	t.Helper()
	es, err := os.ReadDir(l.root)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		fi, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, e.Name()+" "+strconv.FormatInt(fi.Size(), 10)+" "+strconv.FormatInt(fi.ModTime().UnixNano(), 10))
	}
	sort.Strings(out)
	return out
}

func (l *writeLab) ledger(t *testing.T) int {
	return l.a.count(t, `SELECT COUNT(*) FROM file_writes`)
}

func TestWriteUIFlow(t *testing.T) {
	l := newWriteLab(t, 1, false)
	l.start(t)
	before := fileDigest(t, l.files[0])

	// The sheet lists the file and the details and offers the button; the Details tab offers the opener.
	rec := l.get(l.owner, l.sheet(), false)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("sheet: %d", rec.Code)
	}
	for _, want := range []string{writeSheetTitle, "01.m4b", l.book.Title, writeNote, writeButton} {
		if !strings.Contains(body, html.EscapeString(want)) {
			t.Errorf("sheet lacks %q", want)
		}
	}
	if strings.Contains(body, "ts-write__refused") {
		t.Error("the sheet shows a refusal for a file that can be written")
	}
	if tab := l.get(l.owner, l.tab(), false).Body.String(); !strings.Contains(tab, "ts-book__write") {
		t.Error("the Details tab lacks the opener")
	}

	// Start: 303 to the sheet that follows the job; it waits, then the poll shows the two choices.
	jobID := l.jobOf(t, l.post(t, l.owner, "/books/"+l.book.ID+"/write"))
	if n := l.a.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'book.write.start'`); n != 1 {
		t.Errorf("start audit rows = %d", n)
	}
	if page := l.get(l.owner, l.sheet()+"&job="+jobID, false); page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "write-body") {
		t.Errorf("sheet with the job: %d", page.Code)
	}
	done := l.waitDone(t, l.owner, jobID)
	for _, want := range []string{writeJobDone, writePendingBody, writeConfirmBtn, writeUndoBtn} {
		if !strings.Contains(done, html.EscapeString(want)) {
			t.Errorf("finished sheet lacks %q", want)
		}
	}
	if fileDigest(t, l.files[0]) == before {
		t.Error("the file was not rewritten")
	}

	// While the write waits for a choice the Details tab shows the panel and no opener.
	tab := l.get(l.owner, l.tab(), false).Body.String()
	if !strings.Contains(tab, writePendingHead) || strings.Contains(tab, "ts-book__write") {
		t.Errorf("pending tab: panel %v, opener %v", strings.Contains(tab, writePendingHead), strings.Contains(tab, "ts-book__write"))
	}
	// A second start is refused while the files wait for a choice.
	if rec := l.post(t, l.owner, "/books/"+l.book.ID+"/write"); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("start while pending: %d", rec.Code)
	}

	// Undo puts the file back as it was.
	rec = l.post(t, l.owner, "/books/"+l.book.ID+"/write/undo")
	if rec.Code != http.StatusSeeOther || location(rec) != l.tab()+"?written=undone" {
		t.Fatalf("undo: %d %q", rec.Code, location(rec))
	}
	if got := fileDigest(t, l.files[0]); got != before {
		t.Error("Undo did not restore the original file")
	}
	tab = l.get(l.owner, location(rec), false).Body.String()
	if !strings.Contains(tab, html.EscapeString(writeUndone)) || strings.Contains(tab, writePendingHead) || !strings.Contains(tab, "ts-book__write") {
		t.Error("after Undo the tab lacks the note or the opener, or still shows the panel")
	}
	if n := l.a.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'book.write.undo'`); n != 1 {
		t.Errorf("undo audit rows = %d", n)
	}
	for _, e := range l.listing(t) {
		if strings.Contains(e, "tendshelf-") {
			t.Errorf("hidden working file left behind: %s", e)
		}
	}
}

func TestWriteUIConfirm(t *testing.T) {
	l := newWriteLab(t, 2, false)
	l.start(t)
	jobID := l.jobOf(t, l.post(t, l.owner, "/books/"+l.book.ID+"/write"))
	l.waitDone(t, l.admin, jobID) // the admin may follow the owner's job
	rec := l.post(t, l.admin, "/books/"+l.book.ID+"/write/confirm")
	if rec.Code != http.StatusSeeOther || location(rec) != l.tab()+"?written=kept" {
		t.Fatalf("confirm: %d %q", rec.Code, location(rec))
	}
	tab := l.get(l.admin, location(rec), false).Body.String()
	if !strings.Contains(tab, html.EscapeString(writeKept)) || strings.Contains(tab, writePendingHead) {
		t.Error("after Looks good the tab lacks the note or still shows the panel")
	}
	for _, e := range l.listing(t) {
		if strings.Contains(e, ".tendshelf-before") {
			t.Errorf("backup left after Looks good: %s", e)
		}
	}
	if n := l.a.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'book.write.confirm'`); n != 2 {
		t.Errorf("confirm audit rows = %d, want one per file", n)
	}
	// With nothing waiting, Looks good and Undo change nothing and still go back to the tab.
	if rec := l.post(t, l.owner, "/books/"+l.book.ID+"/write/undo"); rec.Code != http.StatusSeeOther {
		t.Errorf("undo with nothing to undo: %d", rec.Code)
	}
}

// TestWriteUIRefusal: every reason the files cannot be written is shown in plain words on the sheet
// without the button, a POST gives 422 and the ledger and the job queue stay empty.
func TestWriteUIRefusal(t *testing.T) {
	cases := []struct {
		name   string
		recent bool
		tweak  func(t *testing.T, l *writeLab)
		code   string
	}{
		{name: "recent", recent: true, code: mediajobs.WriteRecent},
		{name: "leftover", code: mediajobs.WriteLeftover, tweak: func(t *testing.T, l *writeLab) {
			if err := os.WriteFile(filepath.Join(l.root, ".01.m4b.tendshelf-before"), []byte("x"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "missing", code: mediajobs.WriteMissing, tweak: func(t *testing.T, l *writeLab) {
			if err := os.Remove(l.files[0]); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "protected", code: mediajobs.WriteProtected, tweak: func(t *testing.T, l *writeLab) {
			if _, err := l.a.st.Writer().Exec(`UPDATE audio_files SET encrypted = 1`); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "unsupported", code: mediajobs.WriteUnsupported, tweak: func(t *testing.T, l *writeLab) {
			if _, err := l.a.st.Writer().Exec(`UPDATE audio_files SET container = 'wav'`); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "no files", code: mediajobs.WriteNoFiles, tweak: func(t *testing.T, l *writeLab) {
			if _, err := l.a.st.Writer().Exec(`DELETE FROM audio_files`); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := newWriteLab(t, 1, c.recent)
			if c.tweak != nil {
				c.tweak(t, l)
			}
			refs, err := l.writes.Preflight(context.Background(), l.book.ID)
			if err != nil || len(refs) == 0 || refs[0].Code != c.code {
				t.Fatalf("Preflight = %+v, %v; want %s", refs, err, c.code)
			}
			text := html.EscapeString(refs[0].Text)
			if text == "" || strings.Contains(text, "_") {
				t.Fatalf("refusal text %q is empty or looks like a code", text)
			}
			rec := l.get(l.owner, l.sheet(), false)
			body := rec.Body.String()
			if rec.Code != http.StatusOK || !strings.Contains(body, "ts-write__refused") || !strings.Contains(body, text) {
				t.Errorf("sheet: %d, refusal shown %v, text shown %v", rec.Code, strings.Contains(body, "ts-write__refused"), strings.Contains(body, text))
			}
			if strings.Contains(body, `action="/books/`+l.book.ID+`/write"`) {
				t.Error("the sheet offers the button although the files are refused")
			}
			rec = l.post(t, l.owner, "/books/"+l.book.ID+"/write")
			if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), text) {
				t.Errorf("POST: %d, text shown %v", rec.Code, strings.Contains(rec.Body.String(), text))
			}
			if n := l.ledger(t); n != 0 {
				t.Errorf("ledger rows = %d", n)
			}
			if n := l.a.count(t, `SELECT COUNT(*) FROM jobs WHERE kind = ?`, mediajobs.KindWriteDetails); n != 0 {
				t.Errorf("write jobs = %d", n)
			}
		})
	}
}

// TestWriteUIBusy: a write that is already queued for the book refuses a second one in plain words.
func TestWriteUIBusy(t *testing.T) {
	l := newWriteLab(t, 1, false)
	l.jobOf(t, l.post(t, l.owner, "/books/"+l.book.ID+"/write")) // queued; the runner is not started
	rec := l.post(t, l.owner, "/books/"+l.book.ID+"/write")
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "already working on this book") {
		t.Errorf("second start: %d %q", rec.Code, rec.Body.String())
	}
}

// TestWriteUIFailedJob: the files changed after the click, so the job stops and says so in plain
// words, with no choices to make.
func TestWriteUIFailedJob(t *testing.T) {
	l := newWriteLab(t, 1, false)
	jobID := l.jobOf(t, l.post(t, l.owner, "/books/"+l.book.ID+"/write"))
	now := time.Now()
	if err := os.Chtimes(l.files[0], now, now); err != nil {
		t.Fatal(err)
	}
	l.start(t)
	body := l.waitDone(t, l.owner, jobID)
	if !strings.Contains(body, "changed in the last 10 minutes") || strings.Contains(body, writeConfirmBtn) {
		t.Errorf("failed job body: %s", body)
	}
	if n := l.ledger(t); n != 0 {
		t.Errorf("ledger rows = %d", n)
	}
}

// TestWriteUIProgress: the waiting line names the file being written.
func TestWriteUIProgress(t *testing.T) {
	l := newWriteLab(t, 3, false)
	jobID := l.jobOf(t, l.post(t, l.owner, "/books/"+l.book.ID+"/write"))
	if _, err := l.a.st.Writer().Exec(`UPDATE jobs SET progress_json = '{"file":2,"of":3}' WHERE id = ?`, jobID); err != nil {
		t.Fatal(err)
	}
	body := l.get(l.owner, "/hx/jobs/"+jobID, true).Body.String()
	if !strings.Contains(body, "Writing file 2 of 3…") || !strings.Contains(body, "every 2s") {
		t.Errorf("waiting body: %s", body)
	}
}

// TestWriteUINeedsAttention: a file that changed after it was written is left alone by Undo, the
// Details tab says so, and managers see the folder.
func TestWriteUINeedsAttention(t *testing.T) {
	l := newWriteLab(t, 1, false)
	l.start(t)
	l.waitDone(t, l.owner, l.jobOf(t, l.post(t, l.owner, "/books/"+l.book.ID+"/write")))
	if err := os.WriteFile(l.files[0], []byte("someone else wrote this"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := l.post(t, l.owner, "/books/"+l.book.ID+"/write/undo")
	if rec.Code != http.StatusSeeOther || location(rec) != l.tab() {
		t.Fatalf("undo of a changed file: %d %q", rec.Code, location(rec))
	}
	tab := l.get(l.owner, l.tab(), false).Body.String()
	if !strings.Contains(tab, html.EscapeString(writeAttention)) || !strings.Contains(tab, html.EscapeString(l.root)) {
		t.Error("the tab lacks the notice or the folder for the owner")
	}
	if strings.Contains(tab, writePendingHead) {
		t.Error("the tab still waits for a choice")
	}
}

// TestWriteUIGetIsReadOnly: GETting the sheet, the tab and the job leaves the library folder as it was.
func TestWriteUIGetIsReadOnly(t *testing.T) {
	l := newWriteLab(t, 2, false)
	before := l.listing(t)
	jobID := l.jobOf(t, l.post(t, l.owner, "/books/"+l.book.ID+"/write")) // queued, never run
	for _, p := range []string{l.sheet(), l.sheet() + "&job=" + jobID, l.tab(), "/hx/jobs/" + jobID} {
		if rec := l.get(l.owner, p, false); rec.Code != http.StatusOK {
			t.Errorf("GET %s: %d", p, rec.Code)
		}
	}
	if after := l.listing(t); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Errorf("the folder changed:\nbefore %v\nafter  %v", before, after)
	}
	if n := l.ledger(t); n != 0 {
		t.Errorf("ledger rows = %d", n)
	}
}

// TestWriteUIForbidden: who may use the write routes. A job of another book is no job.
func TestWriteUIForbidden(t *testing.T) {
	l := newWriteLab(t, 1, false)
	jobID := l.jobOf(t, l.post(t, l.owner, "/books/"+l.book.ID+"/write"))
	for name, ck := range map[string]*http.Cookie{"member": l.member, "guest": l.guest} {
		for _, p := range []string{l.sheet(), l.sheet() + "&job=" + jobID, "/hx/jobs/" + jobID} {
			if rec := l.get(ck, p, false); rec.Code != http.StatusNotFound {
				t.Errorf("%s GET %s: %d, want 404", name, p, rec.Code)
			}
		}
		for _, p := range []string{"/write", "/write/confirm", "/write/undo"} {
			if rec := l.post(t, ck, "/books/"+l.book.ID+p); rec.Code != http.StatusNotFound {
				t.Errorf("%s POST %s: %d, want 404", name, p, rec.Code)
			}
		}
	}
	if tab := l.get(l.member, l.tab(), false).Body.String(); strings.Contains(tab, "ts-book__write") {
		t.Error("a member sees the write opener")
	}
	// Anonymous: the sign-in answer, never the sheet.
	if rec := l.a.do(http.MethodGet, l.sheet(), nil, nil); rec.Code != http.StatusSeeOther {
		t.Errorf("anonymous sheet: %d", rec.Code)
	}
	// With writing switched off every route is 404, even for the owner.
	l.a.srv.writes = nil
	for _, p := range []string{l.sheet(), "/hx/jobs/" + jobID} {
		if rec := l.get(l.owner, p, false); rec.Code != http.StatusNotFound {
			t.Errorf("writes off, GET %s: %d", p, rec.Code)
		}
	}
	if rec := l.post(t, l.owner, "/books/"+l.book.ID+"/write"); rec.Code != http.StatusNotFound {
		t.Errorf("writes off, POST: %d", rec.Code)
	}
	if tab := l.get(l.owner, l.tab(), false).Body.String(); strings.Contains(tab, "ts-book__write") {
		t.Error("the opener shows while writing is off")
	}
}
