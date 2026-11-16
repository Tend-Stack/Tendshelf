package web

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/scan"
	"github.com/Tend-Stack/tendshelf/internal/upload"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// removeLab is an app whose library folder holds books put there by hand, with the real upload service and a
// scanner over fake ffprobe, so a removal moves real files in a t.TempDir() library (decision 0045).
type removeLab struct {
	a                           *testApp
	libID, dir                  string
	folder                      library.Folder
	owner, admin, member, guest *http.Cookie
	contributor                 *http.Cookie
	scanner                     *scan.Scanner
	svc                         *upload.Service
	runner                      *jobs.Runner // runs only the upload jobs, so a restore's scan does not race the test's own scans
}

func newRemoveLab(t *testing.T) *removeLab {
	t.Helper()
	l := &removeLab{}
	a := newTestApp(t, func(o *Options) {
		r2, err := jobs.New(o.Store, jobs.Options{})
		if err != nil {
			t.Fatal(err)
		}
		l.runner = r2
		l.svc = upload.New(upload.Config{DataDir: o.DataDir, ChunkSize: 1024, MaxFile: 1 << 20, MaxBook: 4 << 20},
			o.Store, o.Library, mediatest.FakeTools(t), r2, nil)
		if err := l.svc.Register(r2); err != nil {
			t.Fatal(err)
		}
		o.Uploads = l.svc
	})
	l.a = a
	l.owner, l.admin = a.as(t, users.RoleOwner), a.as(t, users.RoleAdmin)
	l.member, l.guest = a.as(t, users.RoleMember), a.as(t, users.RoleGuest)
	l.contributor = a.as(t, users.RoleMember)
	if _, err := a.st.Writer().Exec(`UPDATE users SET adds_books = 1 WHERE id = ?`, sweepUserID(t, a, l.contributor)); err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	l.dir = dir
	lib, f := librarytest.NewLibrary(t, a.lib, "Hand library", dir)
	l.libID, l.folder = lib.ID, f
	l.scanner = scan.New(a.lib, mediatest.FakeTools(t), a.runner, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return l
}

// handBook writes a book of fake audio files into the library folder, as a person copying files would.
func (l *removeLab) handBook(t *testing.T, dir string, names ...string) {
	t.Helper()
	for _, n := range names {
		p := filepath.Join(l.dir, filepath.FromSlash(dir), n)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		mediatest.WriteFake(t, p, mediatest.FakeFile{Container: "mp3", Codec: "mp3", Tags: map[string]string{"title": dir + n}, PadBytes: 100})
	}
}

func (l *removeLab) scanAll(t *testing.T) {
	t.Helper()
	if _, err := l.scanner.ScanLibrary(context.Background(), l.libID, nil); err != nil {
		t.Fatal(err)
	}
}

func (l *removeLab) bookAt(t *testing.T, dir string) string {
	t.Helper()
	var id string
	if err := l.a.st.Reader().QueryRow(`SELECT id FROM books WHERE root_path = ?`, filepath.Join(l.dir, dir)).Scan(&id); err != nil {
		t.Fatalf("no book at %s: %v", dir, err)
	}
	return id
}

func (l *removeLab) books(t *testing.T) int { return l.a.count(t, `SELECT count(*) FROM books`) }

func (l *removeLab) get(t *testing.T, ck *http.Cookie, path string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if ck != nil {
		r.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	l.a.h.ServeHTTP(rec, r)
	return rec
}

// post sends a form. token false leaves the CSRF header out; hx sends it as htmx does.
func (l *removeLab) post(t *testing.T, ck *http.Cookie, path string, form url.Values, token, hx bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if token {
		r.Header.Set(csrfHeader, l.a.tokenFor(t, ck))
	}
	if hx {
		r.Header.Set("HX-Request", "true")
	}
	if ck != nil {
		r.AddCookie(ck)
	}
	rec := httptest.NewRecorder()
	l.a.h.ServeHTTP(rec, r)
	return rec
}

func (l *removeLab) plan(t *testing.T, bookID string) upload.RemovalPlan {
	t.Helper()
	p, err := l.svc.PlanRemoval(context.Background(), bookID)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// waitState waits for the removal to reach one of the states.
func (l *removeLab) waitState(t *testing.T, rid string, want ...string) upload.Removal {
	t.Helper()
	for end := time.Now().Add(20 * time.Second); time.Now().Before(end); time.Sleep(25 * time.Millisecond) {
		rm, err := l.svc.Removal(context.Background(), rid)
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range want {
			if rm.State == w {
				return rm
			}
		}
	}
	t.Fatalf("removal %s did not reach %v", rid, want)
	return upload.Removal{}
}

func (l *removeLab) startRunner(t *testing.T) {
	t.Helper()
	if err := l.runner.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.runner.Stop(context.Background()) })
}

func exists(p string) bool { _, err := os.Lstat(p); return err == nil }

func TestRemoveAnyConfirmPage(t *testing.T) {
	l := newRemoveLab(t)
	l.handBook(t, "Hand Book", "01.mp3", "02.mp3")
	l.handBook(t, "Other", "01.mp3")
	if err := os.WriteFile(filepath.Join(l.dir, "Hand Book", "cover.jpg"), []byte("jpg"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(l.dir, "Hand Book", "notes.pdf"), []byte("pdf"), 0o644); err != nil {
		t.Fatal(err)
	}
	l.scanAll(t)
	id := l.bookAt(t, "Hand Book")
	for name, ck := range map[string]*http.Cookie{"owner": l.owner, "admin": l.admin} {
		rec := l.get(t, ck, "/books/"+id+"/remove")
		body := rec.Body.String()
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: %d", name, rec.Code)
		}
		for _, want := range []string{removeAnyTitle, "01.mp3", "02.mp3", "cover.jpg", "3 files", "1 other file in the folder stays",
			removeAnyCheck, removeAnyButton, removeAnyKeep, removeAnyCancel, "30 days", "name=\"plan\"", "required", "hx-boost=\"false\""} {
			if !strings.Contains(body, want) {
				t.Errorf("%s: the page lacks %q", name, want)
			}
		}
		if strings.Contains(body, "notes.pdf") {
			t.Errorf("%s: a file that stays is listed", name)
		}
	}
	for name, ck := range map[string]*http.Cookie{"member": l.member, "guest": l.guest, "contributor": l.contributor, "signed out": nil} {
		rec := l.get(t, ck, "/books/"+id+"/remove")
		if rec.Code == http.StatusOK {
			t.Errorf("%s: got the confirm page", name)
		}
	}
	if rec := l.get(t, l.owner, "/books/bk_nope/remove"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown book: %d", rec.Code)
	}
}

func TestRemoveAnyRefusals(t *testing.T) {
	l := newRemoveLab(t)
	l.handBook(t, "Hand Book", "01.mp3")
	l.scanAll(t)
	id := l.bookAt(t, "Hand Book")
	hash := l.plan(t, id).Hash
	path := "/books/" + id + "/remove-files"
	ok := url.Values{"plan": {hash}, "mode": {"files"}, "understand": {"1"}}

	// Nobody but the owner and admins may start it, and a refusal changes nothing.
	for name, ck := range map[string]*http.Cookie{"member": l.member, "guest": l.guest, "contributor": l.contributor} {
		if rec := l.post(t, ck, path, ok, true, false); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if rec := l.post(t, nil, path, ok, false, false); rec.Code == http.StatusSeeOther && !strings.Contains(rec.Header().Get("Location"), "login") {
		t.Errorf("signed out: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	if rec := l.post(t, l.owner, path, ok, false, false); rec.Code != http.StatusForbidden {
		t.Errorf("no CSRF token: %d", rec.Code)
	}
	// The box is not ticked: a plain post shows the page again with the words, an htmx post a toast.
	noBox := url.Values{"plan": {hash}, "mode": {"files"}}
	rec := l.post(t, l.owner, path, noBox, true, false)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), removeAnyNotConfirmed) || !strings.Contains(rec.Body.String(), "01.mp3") {
		t.Errorf("box not ticked: %d %s", rec.Code, rec.Body.String())
	}
	rec = l.post(t, l.owner, path, noBox, true, true)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "hx-swap-oob") || strings.Contains(rec.Body.String(), "<html") ||
		!strings.Contains(rec.Body.String(), removeAnyNotConfirmed) {
		t.Errorf("box not ticked (htmx): %d %s", rec.Code, rec.Body.String())
	}
	// A mode that is not offered.
	if rec := l.post(t, l.owner, path, url.Values{"plan": {hash}, "mode": {"burn"}, "understand": {"1"}}, true, false); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("bad mode: %d", rec.Code)
	}
	// The disk changed after the page was drawn: plain words and the fresh list.
	if err := os.WriteFile(filepath.Join(l.dir, "Hand Book", "cover.jpg"), []byte("jpg"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec = l.post(t, l.owner, path, ok, true, false)
	body := rec.Body.String()
	if rec.Code != http.StatusConflict || !strings.Contains(body, removeAnyPlanChanged) || !strings.Contains(body, "cover.jpg") {
		t.Errorf("plan changed: %d %s", rec.Code, body)
	}
	rec = l.post(t, l.owner, path, ok, true, true)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "hx-swap-oob") || !strings.Contains(rec.Body.String(), removeAnyPlanChanged) {
		t.Errorf("plan changed (htmx): %d %s", rec.Code, rec.Body.String())
	}
	if n := l.a.count(t, `SELECT count(*) FROM removals`); n != 0 || !exists(filepath.Join(l.dir, "Hand Book", "01.mp3")) {
		t.Errorf("a refusal changed something: %d removals", n)
	}
}

func TestRemoveAnyBusyAndStatusWords(t *testing.T) {
	l := newRemoveLab(t)
	l.handBook(t, "Hand Book", "01.mp3")
	l.scanAll(t)
	id := l.bookAt(t, "Hand Book")
	// The runner is not started, so the removal stays planned: a second start is refused as busy.
	hash := l.plan(t, id).Hash
	form := url.Values{"plan": {hash}, "mode": {"files"}, "understand": {"1"}}
	rec := l.post(t, l.owner, "/books/"+id+"/remove-files", form, true, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	loc := rec.Header().Get("Location")
	rid := strings.TrimPrefix(loc, "/removals/")
	rec = l.post(t, l.owner, "/books/"+id+"/remove-files", form, true, false)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), removeAnyBusy) {
		t.Errorf("busy: %d %s", rec.Code, rec.Body.String())
	}
	// Moving: the status polls itself.
	frag := l.get(t, l.owner, "/hx/removals/"+rid)
	if frag.Code != http.StatusOK || !strings.Contains(frag.Body.String(), removeAnyMoving) || !strings.Contains(frag.Body.String(), `hx-trigger="every 2s"`) ||
		strings.Contains(frag.Body.String(), "<html") {
		t.Errorf("moving fragment: %d %s", frag.Code, frag.Body.String())
	}
	// Every stored failure is said in plain words.
	for code, want := range map[string]string{
		"changed_since_scan": removeAnyChangedSinceScan, "read_only": removeAnyReadOnly, "cannot_move": removeAnyCannotMove,
		"interrupted": removeAnyInterrupted, "other": removeAnyFailedOther,
	} {
		if _, err := l.a.st.Writer().Exec(`UPDATE removals SET state = 'failed', error_code = ? WHERE id = ?`, code, rid); err != nil {
			t.Fatal(err)
		}
		page := l.get(t, l.owner, "/removals/"+rid)
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), want) || strings.Contains(page.Body.String(), `hx-trigger="every 2s"`) {
			t.Errorf("%s: %d %s", code, page.Code, page.Body.String())
		}
	}
	for name, ck := range map[string]*http.Cookie{"member": l.member, "guest": l.guest, "contributor": l.contributor} {
		if rec := l.get(t, ck, "/removals/"+rid); rec.Code != http.StatusNotFound {
			t.Errorf("%s page: %d", name, rec.Code)
		}
		if rec := l.get(t, ck, "/hx/removals/"+rid); rec.Code != http.StatusNotFound {
			t.Errorf("%s fragment: %d", name, rec.Code)
		}
		if rec := l.post(t, ck, "/removals/"+rid+"/restore", url.Values{}, true, false); rec.Code != http.StatusNotFound {
			t.Errorf("%s restore: %d", name, rec.Code)
		}
		if rec := l.post(t, ck, "/removals/"+rid+"/purge", url.Values{"understand": {"1"}}, true, false); rec.Code != http.StatusNotFound {
			t.Errorf("%s purge: %d", name, rec.Code)
		}
	}
	if rec := l.post(t, l.owner, "/removals/"+rid+"/restore", url.Values{}, true, true); rec.Code != http.StatusConflict ||
		!strings.Contains(rec.Body.String(), "hx-swap-oob") {
		t.Errorf("restore a failed removal (htmx): %d %s", rec.Code, rec.Body.String())
	}
}

func TestRemoveAnyNotRemovableIsSaidInWords(t *testing.T) {
	l := newRemoveLab(t)
	l.handBook(t, "Linked", "01.mp3")
	l.scanAll(t)
	id := l.bookAt(t, "Linked")
	real := filepath.Join(l.dir, "Linked", "01.mp3")
	moved := filepath.Join(t.TempDir(), "x.mp3")
	if err := os.Rename(real, moved); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(moved, real); err != nil {
		t.Skip("no symlinks here")
	}
	rec := l.get(t, l.owner, "/books/"+id+"/remove")
	if rec.Code != http.StatusConflict || strings.Contains(rec.Body.String(), "name=\"plan\"") || !strings.Contains(rec.Body.String(), "role=\"alert\"") {
		t.Errorf("a linked file: %d %s", rec.Code, rec.Body.String())
	}
}

func TestRemoveAnyFilesEndToEnd(t *testing.T) {
	l := newRemoveLab(t)
	l.startRunner(t)
	l.handBook(t, "Hand Book", "01.mp3", "02.mp3")
	l.handBook(t, "Other", "01.mp3")
	l.scanAll(t)
	id := l.bookAt(t, "Hand Book")
	plan := l.plan(t, id)
	rec := l.post(t, l.admin, "/books/"+id+"/remove-files", url.Values{"plan": {plan.Hash}, "mode": {"files"}, "understand": {"1"}}, true, false)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/removals/rmv_") {
		t.Fatalf("start: %d %s", rec.Code, rec.Body.String())
	}
	rid := strings.TrimPrefix(rec.Header().Get("Location"), "/removals/")
	l.waitState(t, rid, upload.RemovalHeld)
	// The files moved to the holding folder, the book is gone, the other book stays.
	for _, n := range []string{"01.mp3", "02.mp3"} {
		if exists(filepath.Join(l.dir, "Hand Book", n)) || !exists(filepath.Join(l.dir, upload.HoldingDir, rid, "Hand Book", n)) {
			t.Errorf("%s was not moved to the holding folder", n)
		}
	}
	if l.books(t) != 1 {
		t.Errorf("books: %d", l.books(t))
	}
	if rec := l.get(t, l.owner, "/books/"+id); rec.Code != http.StatusNotFound {
		t.Errorf("the removed book's page: %d", rec.Code)
	}
	// The status reaches "held" with Put back and Delete now and no polling.
	frag := l.get(t, l.owner, "/hx/removals/"+rid).Body.String()
	for _, want := range []string{"Removed. The files are kept until", removeAnyPutBack, removeAnyDeleteNow} {
		if !strings.Contains(frag, want) {
			t.Errorf("held fragment lacks %q: %s", want, frag)
		}
	}
	if strings.Contains(frag, "every 2s") {
		t.Errorf("a held removal still polls")
	}
	list := l.get(t, l.admin, "/admin/libraries/"+l.libID+"/removed").Body.String()
	for _, want := range []string{"Hand Book", "2 files", "30 days left", removeAnyPutBack, removeAnyDeleteNow} {
		if !strings.Contains(list, want) {
			t.Errorf("removed books lacks %q", want)
		}
	}
	// Put back returns the files; a scan brings the book back.
	rec = l.post(t, l.owner, "/removals/"+rid+"/restore", url.Values{}, true, false)
	if rec.Code != http.StatusSeeOther || !strings.Contains(rec.Header().Get("Location"), "done=restored") {
		t.Fatalf("restore: %d %s", rec.Code, rec.Body.String())
	}
	for _, n := range []string{"01.mp3", "02.mp3"} {
		if !exists(filepath.Join(l.dir, "Hand Book", n)) {
			t.Errorf("%s did not come back", n)
		}
	}
	l.scanAll(t)
	if l.books(t) != 2 {
		t.Errorf("books after a scan: %d", l.books(t))
	}
	if page := l.get(t, l.owner, rec.Header().Get("Location")).Body.String(); !strings.Contains(page, removeAnyRestored) {
		t.Errorf("restored page: %s", page)
	}
}

func TestRemoveAnyKeepFilesAndPurge(t *testing.T) {
	l := newRemoveLab(t)
	l.startRunner(t)
	l.handBook(t, "Keep Me", "01.mp3")
	l.handBook(t, "Delete Me", "01.mp3")
	l.scanAll(t)

	keepID := l.bookAt(t, "Keep Me")
	rec := l.post(t, l.owner, "/books/"+keepID+"/remove-files", url.Values{"plan": {l.plan(t, keepID).Hash}, "mode": {"keep_files"}}, true, false)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("keep files: %d %s", rec.Code, rec.Body.String())
	}
	keepRid := strings.TrimPrefix(rec.Header().Get("Location"), "/removals/")
	if !exists(filepath.Join(l.dir, "Keep Me", "01.mp3")) || exists(filepath.Join(l.dir, upload.HoldingDir)) {
		t.Errorf("keep files touched the files")
	}
	l.scanAll(t)
	if l.books(t) != 1 {
		t.Errorf("the next scan added the book again: %d books", l.books(t))
	}
	page := l.get(t, l.owner, "/removals/"+keepRid).Body.String()
	if !strings.Contains(page, removeAnyKeptOut) || !strings.Contains(page, removeAnyShowAgain) || strings.Contains(page, removeAnyDeleteNow) {
		t.Errorf("keep-files page: %s", page)
	}
	if rec := l.post(t, l.owner, "/removals/"+keepRid+"/purge", url.Values{"understand": {"1"}}, true, false); rec.Code != http.StatusConflict {
		t.Errorf("purge of a keep-files removal: %d", rec.Code)
	}

	delID := l.bookAt(t, "Delete Me")
	rec = l.post(t, l.owner, "/books/"+delID+"/remove-files", url.Values{"plan": {l.plan(t, delID).Hash}, "mode": {"files"}, "understand": {"1"}}, true, false)
	delRid := strings.TrimPrefix(rec.Header().Get("Location"), "/removals/")
	l.waitState(t, delRid, upload.RemovalHeld)
	// Delete now needs its own tick.
	if rec := l.post(t, l.owner, "/removals/"+delRid+"/purge", url.Values{}, true, false); rec.Code != http.StatusUnprocessableEntity ||
		!exists(filepath.Join(l.dir, upload.HoldingDir, delRid)) {
		t.Errorf("purge without the box: %d", rec.Code)
	}
	if rec := l.post(t, l.owner, "/removals/"+delRid+"/purge", url.Values{"understand": {"1"}}, true, false); rec.Code != http.StatusSeeOther {
		t.Errorf("purge: %d", rec.Code)
	}
	if exists(filepath.Join(l.dir, upload.HoldingDir, delRid)) {
		t.Errorf("the holding folder is still there")
	}
	if rm, _ := l.svc.Removal(context.Background(), delRid); rm.State != upload.RemovalPurged {
		t.Errorf("state %s", rm.State)
	}

	// Show again from the list brings the kept book back after a scan.
	list := l.get(t, l.owner, "/admin/libraries/"+l.libID+"/removed").Body.String()
	if !strings.Contains(list, "Keep Me") || !strings.Contains(list, removeAnyShowAgain) || strings.Contains(list, "Delete Me") {
		t.Errorf("list: %s", list)
	}
	rec = l.post(t, l.owner, "/removals/"+keepRid+"/restore", url.Values{"next": {"list"}}, true, false)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/admin/libraries/"+l.libID+"/removed") {
		t.Errorf("show again: %d %s", rec.Code, rec.Header().Get("Location"))
	}
	l.scanAll(t)
	if l.books(t) != 1 || l.a.count(t, `SELECT count(*) FROM books WHERE root_path = ?`, filepath.Join(l.dir, "Keep Me")) != 1 {
		t.Errorf("the kept book did not return: %d books", l.books(t))
	}
}

func TestRemovedBooksListIsForStaffOnly(t *testing.T) {
	l := newRemoveLab(t)
	for name, ck := range map[string]*http.Cookie{"member": l.member, "guest": l.guest, "contributor": l.contributor} {
		if rec := l.get(t, ck, "/admin/libraries/"+l.libID+"/removed"); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	for name, ck := range map[string]*http.Cookie{"owner": l.owner, "admin": l.admin} {
		rec := l.get(t, ck, "/admin/libraries/"+l.libID+"/removed")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), removeAnyListEmpty) {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if rec := l.get(t, l.owner, "/admin/libraries/lib_nope/removed"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown library: %d", rec.Code)
	}
	if page := l.get(t, l.owner, "/admin/libraries").Body.String(); !strings.Contains(page, "/admin/libraries/"+l.libID+"/removed") {
		t.Errorf("Libraries & scanning does not link the removed books")
	}
}

// The menu offers "Remove this book" only to the owner and admins, and only when the upload removal is not offered.
func TestRemoveAnyMenuEntry(t *testing.T) {
	l := newRemoveLab(t)
	l.handBook(t, "Hand Book", "01.mp3")
	l.handBook(t, "Placed", "01.mp3")
	l.scanAll(t)
	hand, placed := l.bookAt(t, "Hand Book"), l.bookAt(t, "Placed")
	link := func(id string) string { return `href="/books/` + id + `/remove"` }
	for name, ck := range map[string]*http.Cookie{"owner": l.owner, "admin": l.admin} {
		if body := l.get(t, ck, "/books/"+hand).Body.String(); !strings.Contains(body, link(hand)) || !strings.Contains(body, "Remove this book") {
			t.Errorf("%s: no menu entry on a hand-added book", name)
		}
	}
	for name, ck := range map[string]*http.Cookie{"member": l.member, "guest": l.guest} {
		if body := l.get(t, ck, "/books/"+hand).Body.String(); strings.Contains(body, link(hand)) {
			t.Errorf("%s sees the entry", name)
		}
	}
	// A book Tendshelf placed keeps its own entry and gets no second one.
	if _, err := l.a.st.Writer().Exec(`INSERT INTO upload_paths(id, upload_id, library_folder_id, path, kind, state, created_at, updated_at)
  VALUES ('upp_web', 'up_web', ?, ?, 'file', 'present', 1, 1)`, l.folder.ID, filepath.Join(l.dir, "Placed", "01.mp3")); err != nil {
		t.Fatal(err)
	}
	body := l.get(t, l.owner, "/books/"+placed).Body.String()
	if !strings.Contains(body, "Remove the whole book") || strings.Contains(body, `href="/books/`+placed+`/remove"`) {
		t.Errorf("a placed book: %s", body)
	}
}
