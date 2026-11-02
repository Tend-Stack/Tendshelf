package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/scan"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// adminPostAs posts form as the person holding ck, with a valid CSRF token for that person.
func (a *testApp) adminPostAs(t *testing.T, ck *http.Cookie, path string, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	f := url.Values{}
	for k, v := range form {
		f[k] = v
	}
	f.Set("_csrf", a.tokenFor(t, ck))
	return a.do(http.MethodPost, path, f, nil, ck)
}

// adminEnqueueScan queues a scan job of a library, as the Scan button does.
func (a *testApp) adminEnqueueScan(t *testing.T, libraryID string) string {
	t.Helper()
	id, err := a.runner.Enqueue(context.Background(), scan.KindScan, scan.ScanPayload{LibraryID: libraryID}, jobs.EnqueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (a *testApp) adminSetJobState(t *testing.T, id, state string) {
	t.Helper()
	lease := any(nil)
	if state == "running" {
		lease = int64(4_102_444_800_000)
	}
	if _, err := a.st.Writer().Exec(`UPDATE jobs SET state = ?, lease_until = ? WHERE id = ?`, state, lease, id); err != nil {
		t.Fatal(err)
	}
}

func adminScanJobs(t *testing.T, a *testApp) []jobs.Job {
	t.Helper()
	rows, err := a.st.Reader().Query(`SELECT id FROM jobs WHERE kind = ? ORDER BY created_at, id`, scan.KindScan)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []jobs.Job
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		j, err := a.runner.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, j)
	}
	return out
}

func adminAuditCount(t *testing.T, a *testApp, action string) int {
	t.Helper()
	var n int
	if err := a.st.Reader().QueryRow(`SELECT count(*) FROM audit_log WHERE action = ?`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// adminAudioDir makes a library folder on disk that holds one audio file (or none).
func adminAudioDir(t *testing.T, withAudio bool) string {
	t.Helper()
	d := t.TempDir()
	if withAudio {
		if err := os.WriteFile(filepath.Join(d, "a.mp3"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return d
}

func TestAdminAccess(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	member := a.as(t, users.RoleMember)
	guest := a.as(t, users.RoleGuest)
	admin := a.as(t, users.RoleAdmin)
	l, f := librarytest.NewLibrary(t, a.lib, "Books", adminAudioDir(t, false))
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "One"})
	job := a.adminEnqueueScan(t, l.ID)

	gets := []string{"/admin/libraries", "/admin/libraries/folders/new", "/admin/folders/" + f.ID + "/emptied", "/hx/jobs/" + job}
	posts := []string{"/admin/libraries/folders", "/admin/libraries/scan", "/admin/libraries/" + l.ID + "/scan", "/admin/folders/" + f.ID + "/emptied"}

	for _, p := range gets {
		for name, ck := range map[string]*http.Cookie{"owner": owner, "admin": admin} {
			if rec := a.do(http.MethodGet, p, nil, nil, ck); rec.Code != http.StatusOK {
				t.Errorf("GET %s as %s: %d", p, name, rec.Code)
			}
		}
		for name, ck := range map[string]*http.Cookie{"member": member, "guest": guest} {
			if rec := a.do(http.MethodGet, p, nil, nil, ck); rec.Code != http.StatusNotFound {
				t.Errorf("GET %s as %s: %d, want 404", p, name, rec.Code)
			}
		}
		rec := a.do(http.MethodGet, p, nil, nil)
		if want := "/login?next=" + url.QueryEscape(p); rec.Code != http.StatusSeeOther || location(rec) != want {
			t.Errorf("anonymous GET %s: %d -> %q, want 303 %s", p, rec.Code, location(rec), want)
		}
		rec = a.do(http.MethodGet, p, nil, hx)
		if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Redirect") == "" {
			t.Errorf("anonymous htmx GET %s: %d HX-Redirect %q", p, rec.Code, rec.Header().Get("HX-Redirect"))
		}
	}
	for _, p := range posts {
		if rec := a.do(http.MethodPost, p, url.Values{}, nil, owner); rec.Code != http.StatusForbidden {
			t.Errorf("POST %s without a token: %d, want 403", p, rec.Code)
		}
		for name, ck := range map[string]*http.Cookie{"member": member, "guest": guest} {
			if rec := a.adminPostAs(t, ck, p, url.Values{}); rec.Code != http.StatusNotFound {
				t.Errorf("POST %s as %s with a token: %d, want 404", p, name, rec.Code)
			}
		}
	}
	// Nothing happened for the refused people.
	if n := len(adminScanJobs(t, a)); n != 1 {
		t.Errorf("%d scan jobs, want only the seeded one", n)
	}
}

func TestAddFolder(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	dir := adminAudioDir(t, true)

	rec := a.adminPostAs(t, owner, "/admin/libraries/folders", url.Values{"folder": {dir}, "library": {""}, "new_name": {"Kids"}})
	if rec.Code != http.StatusSeeOther || location(rec) != "/admin/libraries" {
		t.Fatalf("add: %d -> %q %s", rec.Code, location(rec), rec.Body.String())
	}
	libs, _ := a.lib.Libraries(context.Background())
	if len(libs) != 1 || libs[0].Name != "Kids" {
		t.Fatalf("libraries = %+v", libs)
	}
	fs, _ := a.lib.Folders(context.Background(), libs[0].ID)
	if len(fs) != 1 || fs[0].Path != filepath.Clean(dir) {
		t.Fatalf("folders = %+v", fs)
	}
	js := adminScanJobs(t, a)
	if len(js) != 1 || js[0].CreatedBy == "" || jobLibrary(js[0]) != libs[0].ID {
		t.Fatalf("scan jobs = %+v", js)
	}
	if adminAuditCount(t, a, "library.folder_added") != 1 {
		t.Error("no audit row")
	}

	// A second folder into the existing library: no new library, the running scan is reused.
	dir2 := adminAudioDir(t, false)
	rec = a.adminPostAs(t, owner, "/admin/libraries/folders", url.Values{"folder": {dir2}, "library": {libs[0].ID}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("add to library: %d %s", rec.Code, rec.Body.String())
	}
	if libs, _ = a.lib.Libraries(context.Background()); len(libs) != 1 {
		t.Fatalf("libraries = %+v", libs)
	}
	if fs, _ = a.lib.Folders(context.Background(), libs[0].ID); len(fs) != 2 {
		t.Fatalf("folders = %+v", fs)
	}
	if len(adminScanJobs(t, a)) != 1 {
		t.Error("a second scan job was queued while one was waiting")
	}

	// A blank new-library name falls back to the folder's own name.
	dir3 := filepath.Join(t.TempDir(), "Lantern Street")
	if err := os.Mkdir(dir3, 0o755); err != nil {
		t.Fatal(err)
	}
	if rec = a.adminPostAs(t, owner, "/admin/libraries/folders", url.Values{"folder": {dir3}, "library": {""}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("blank name: %d", rec.Code)
	}
	if l, _ := a.lib.Libraries(context.Background()); len(l) != 2 || l[0].Name != "Kids" && l[1].Name != "Kids" {
		t.Fatalf("libraries = %+v", l)
	}
}

func TestAddFolderRefusals(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	holder := filepath.Join(t.TempDir(), "shelves")
	existing := filepath.Join(holder, "books")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}
	librarytest.NewLibrary(t, a.lib, "Books", existing)

	file := filepath.Join(t.TempDir(), "f.txt")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	link := filepath.Join(outside, "into-data")
	if err := os.Symlink(a.dir, link); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(a.dir, "backups")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	inner := filepath.Join(existing, "inner")
	if err := os.Mkdir(inner, 0o755); err != nil {
		t.Fatal(err)
	}
	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(locked, 0o755) })

	cases := []struct{ name, path, code string }{
		{"empty", "", "path_relative"},
		{"relative", "audiobooks/here", "path_relative"},
		{"missing", filepath.Join(outside, "nope"), "path_missing"},
		{"file", file, "path_file"},
		{"data dir", a.dir, "path_data"},
		{"inside data dir", sub, "path_data"},
		{"parent of data dir", filepath.Dir(a.dir), "path_data"},
		{"symlink into data dir", link, "path_data"},
		{"same folder", existing, "path_overlap"},
		{"inside a folder", inner, "path_overlap"},
		{"holds a folder", holder, "path_overlap"},
	}
	if os.Geteuid() != 0 {
		cases = append(cases, struct{ name, path, code string }{"unreadable", locked, "path_unreadable"})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := a.adminPostAs(t, owner, "/admin/libraries/folders", url.Values{"folder": {c.path}, "library": {""}})
			body := rec.Body.String()
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("%d, want 422", rec.Code)
			}
			if want := pathMessage(c.code); !strings.Contains(body, adminHTMLEscape(want)) {
				t.Fatalf("page lacks %q", want)
			}
		})
	}
	if libs, _ := a.lib.Libraries(context.Background()); len(libs) != 1 {
		t.Fatalf("a refused folder left a library behind: %+v", libs)
	}
	if len(adminScanJobs(t, a)) != 0 {
		t.Error("a scan was queued for a refused folder")
	}
	// An unknown library is a 404; a name over 200 characters is refused.
	if rec := a.adminPostAs(t, owner, "/admin/libraries/folders", url.Values{"folder": {adminAudioDir(t, false)}, "library": {"lib_missing"}}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown library: %d", rec.Code)
	}
	rec := a.adminPostAs(t, owner, "/admin/libraries/folders", url.Values{"folder": {adminAudioDir(t, false)}, "library": {""}, "new_name": {strings.Repeat("x", 201)}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Give the library a name.") {
		t.Errorf("long name: %d", rec.Code)
	}
}

func adminHTMLEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "'", "&#39;", "\"", "&#34;", "<", "&lt;", ">", "&gt;").Replace(s)
}

func TestScanNowReusesActiveJob(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	l1, _ := librarytest.NewLibrary(t, a.lib, "One", adminAudioDir(t, false))
	l2, _ := librarytest.NewLibrary(t, a.lib, "Two", adminAudioDir(t, false))

	if rec := a.adminPostAs(t, owner, "/admin/libraries/scan", nil); rec.Code != http.StatusSeeOther || location(rec) != "/admin/libraries" {
		t.Fatalf("scan all: %d -> %q", rec.Code, location(rec))
	}
	if n := len(adminScanJobs(t, a)); n != 2 {
		t.Fatalf("%d jobs after scan all, want 2", n)
	}
	// Pressing it again, or the library's own button, adds nothing while those jobs wait.
	a.adminPostAs(t, owner, "/admin/libraries/scan", nil)
	if rec := a.adminPostAs(t, owner, "/admin/libraries/"+l1.ID+"/scan", nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("scan one: %d", rec.Code)
	}
	if n := len(adminScanJobs(t, a)); n != 2 {
		t.Fatalf("%d jobs, want the two active ones reused", n)
	}
	// Once a job is over, a new press queues a new job.
	for _, j := range adminScanJobs(t, a) {
		if jobLibrary(j) == l2.ID {
			a.adminSetJobState(t, j.ID, "done")
		}
	}
	a.adminPostAs(t, owner, "/admin/libraries/"+l2.ID+"/scan", nil)
	if n := len(adminScanJobs(t, a)); n != 3 {
		t.Fatalf("%d jobs, want a new one for the finished library", n)
	}
	if rec := a.adminPostAs(t, owner, "/admin/libraries/lib_nope/scan", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unknown library: %d", rec.Code)
	}
}

func TestJobsFragment(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	member := a.as(t, users.RoleMember)
	l, f := librarytest.NewLibrary(t, a.lib, "Books", adminAudioDir(t, false))
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "A", Files: 2})
	id := a.adminEnqueueScan(t, l.ID)
	get := func(path string, ck *http.Cookie) *httptest.ResponseRecorder {
		return a.do(http.MethodGet, path, nil, nil, ck)
	}

	body := get("/hx/jobs/"+id, owner).Body.String()
	if !strings.Contains(body, `hx-trigger="every 2s"`) || !strings.Contains(body, `hx-get="/hx/jobs/`+id+`"`) {
		t.Errorf("queued job does not poll: %s", body)
	}
	if !strings.Contains(body, "Found 1 book… reading covers") || !strings.Contains(body, `role="progressbar"`) {
		t.Errorf("queued job body: %s", body)
	}
	// Done, but a file still waits for its probe: keep polling.
	a.adminSetJobState(t, id, "done")
	if _, err := a.st.Writer().Exec(`UPDATE audio_files SET probe_json = NULL WHERE book_id IN (SELECT id FROM books) AND idx = 1`); err != nil {
		t.Fatal(err)
	}
	if body = get("/hx/jobs/"+id, owner).Body.String(); !strings.Contains(body, `hx-trigger="every 2s"`) {
		t.Errorf("done job with waiting files stops polling: %s", body)
	}
	// Done and settled: no polling attributes at all.
	if _, err := a.st.Writer().Exec(`UPDATE audio_files SET probe_json = '{}'`); err != nil {
		t.Fatal(err)
	}
	body = get("/hx/jobs/"+id, owner).Body.String()
	if strings.Contains(body, "hx-get") || strings.Contains(body, "hx-trigger") {
		t.Errorf("settled job still polls: %s", body)
	}
	if !strings.Contains(body, "Found 1 book.") || !strings.Contains(body, `role="status"`) {
		t.Errorf("settled body: %s", body)
	}
	// Failed: the job's own words, or the plain fallback.
	if _, err := a.st.Writer().Exec(`UPDATE jobs SET state = 'failed', error_text = 'That folder is gone.' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	body = get("/hx/jobs/"+id, owner).Body.String()
	if !strings.Contains(body, "That folder is gone.") || strings.Contains(body, "hx-trigger") {
		t.Errorf("failed body: %s", body)
	}
	if _, err := a.st.Writer().Exec(`UPDATE jobs SET error_text = NULL WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if body = get("/hx/jobs/"+id, owner).Body.String(); !strings.Contains(body, "The scan stopped. Try Scan now again.") {
		t.Errorf("failed fallback body: %s", body)
	}

	if rec := get("/hx/jobs/"+id, member); rec.Code != http.StatusNotFound {
		t.Errorf("member: %d", rec.Code)
	}
	if rec := get("/hx/jobs/nope", owner); rec.Code != http.StatusNotFound {
		t.Errorf("unknown: %d", rec.Code)
	}
	probe, err := a.runner.Enqueue(context.Background(), scan.KindProbe, scan.ProbePayload{AudioFileID: "af_x"}, jobs.EnqueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if rec := get("/hx/jobs/"+probe, owner); rec.Code != http.StatusNotFound {
		t.Errorf("media.probe job: %d", rec.Code)
	}
	// The setup reassurance shows when the page asking is a setup step.
	a.adminSetJobState(t, id, "running")
	rec := a.do(http.MethodGet, "/hx/jobs/"+id, nil, func(r *http.Request) {
		r.Header.Set("HX-Request", "true")
		r.Header.Set("HX-Current-URL", "http://example.com/setup/folder?job="+id)
	}, owner)
	if !strings.Contains(rec.Body.String(), "You can keep going while it reads.") {
		t.Errorf("setup note missing: %s", rec.Body.String())
	}
}

func TestAdminLibrariesPage(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	l, f := librarytest.NewLibrary(t, a.lib, "Audiobooks", adminAudioDir(t, false))
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Saltmarsh", Files: 2})
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "The Glass Orchard", Refused: library.RefusedDRM})
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Lost", MissingSince: 1})

	body := adminHTMLUnescape(a.do(http.MethodGet, "/admin/libraries", nil, nil, owner).Body.String())
	for _, want := range []string{
		"Libraries &amp; scanning", "1 book ready to play", "No scan yet.", "3 things need a look",
		"1 protected file was skipped", "“The Glass Orchard” is copy-protected, so Tendshelf can’t open it.",
		"1 book is missing its files", "1 book has no cover", "Audiobooks", "1 book ·",
		"This folder was emptied on purpose", "Scan now", "Add a folder",
	} {
		if !strings.Contains(strings.ReplaceAll(body, "&amp;", "&"), strings.ReplaceAll(want, "&amp;", "&")) {
			t.Errorf("page lacks %q", want)
		}
	}
	if strings.Contains(body, "hx-trigger") {
		t.Error("an idle page polls")
	}
	// An active scan shows its status with a poller.
	a.adminEnqueueScan(t, l.ID)
	body = a.do(http.MethodGet, "/admin/libraries", nil, nil, owner).Body.String()
	if !strings.Contains(body, `hx-trigger="every 2s"`) {
		t.Error("an active scan does not show its status")
	}
	if !strings.Contains(body, "Scanning your audiobook folder.") || strings.Contains(body, "No scan yet.") {
		t.Error("the summary line does not say a scan is running")
	}
	if banner := a.do(http.MethodGet, "/admin/libraries?marked=3", nil, nil, owner).Body.String(); !strings.Contains(banner, "Marked 3 books as missing.") {
		t.Error("no banner")
	}
}

func adminHTMLUnescape(s string) string {
	return strings.NewReplacer("&#39;", "'", "&#34;", "\"", "&amp;", "&", "&lt;", "<", "&gt;", ">").Replace(s)
}

func TestFolderEmptiedConfirm(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	get := func(id string) (int, string) {
		rec := a.do(http.MethodGet, "/admin/folders/"+id+"/emptied", nil, nil, owner)
		return rec.Code, adminHTMLUnescape(rec.Body.String())
	}

	// ready: books in the database, an empty readable folder
	_, ready := librarytest.NewLibrary(t, a.lib, "A", adminAudioDir(t, false))
	librarytest.AddBook(t, a.lib, ready, librarytest.BookSpec{Title: "x"})
	librarytest.AddBook(t, a.lib, ready, librarytest.BookSpec{Title: "y"})
	if code, body := get(ready.ID); code != 200 || !strings.Contains(body, "Mark 2 books missing") ||
		!strings.Contains(body, "Was this folder emptied on purpose?") || !strings.Contains(body, "Keep them") {
		t.Errorf("ready: %d %s", code, body)
	}
	// unavailable: the folder is gone
	gone := filepath.Join(t.TempDir(), "drive")
	if err := os.Mkdir(gone, 0o755); err != nil {
		t.Fatal(err)
	}
	_, unavailable := librarytest.NewLibrary(t, a.lib, "B", gone)
	librarytest.AddBook(t, a.lib, unavailable, librarytest.BookSpec{Title: "z"})
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if code, body := get(unavailable.ID); code != 200 || !strings.Contains(body, "so it won't mark anything") || strings.Contains(body, "Mark 1 book missing") {
		t.Errorf("unavailable: %d %s", code, body)
	}
	// audio left
	_, has := librarytest.NewLibrary(t, a.lib, "C", adminAudioDir(t, true))
	librarytest.AddBook(t, a.lib, has, librarytest.BookSpec{Title: "w"})
	if code, body := get(has.ID); code != 200 || !strings.Contains(body, "still has audiobooks in it") || strings.Contains(body, "Mark 1 book missing") {
		t.Errorf("audio: %d %s", code, body)
	}
	// nothing to mark
	_, none := librarytest.NewLibrary(t, a.lib, "D", adminAudioDir(t, false))
	if code, body := get(none.ID); code != 200 || !strings.Contains(body, "No books in this folder need marking.") || !strings.Contains(body, "Back to libraries") {
		t.Errorf("nothing: %d %s", code, body)
	}
	if code, _ := get("lf_nope"); code != 404 {
		t.Errorf("unknown folder: %d", code)
	}
}

func TestFolderEmptiedPost(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	member := a.as(t, users.RoleMember)
	dir := adminAudioDir(t, false)
	_, f := librarytest.NewLibrary(t, a.lib, "A", dir)
	b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "x", Files: 2})
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "y"})
	path := "/admin/folders/" + f.ID + "/emptied"
	missing := func() int {
		var n int
		if err := a.st.Reader().QueryRow(`SELECT count(*) FROM books WHERE missing_since IS NOT NULL`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	if rec := a.do(http.MethodPost, path, url.Values{}, nil, owner); rec.Code != http.StatusForbidden {
		t.Fatalf("no token: %d", rec.Code)
	}
	if rec := a.adminPostAs(t, member, path, nil); rec.Code != http.StatusNotFound {
		t.Fatalf("member: %d", rec.Code)
	}
	// An audio file appears after the confirm page was shown: refuse, mark nothing.
	if err := os.WriteFile(filepath.Join(dir, "late.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := a.adminPostAs(t, owner, path, nil)
	if rec.Code != http.StatusConflict || !strings.Contains(adminHTMLUnescape(rec.Body.String()), "still has audiobooks in it") {
		t.Fatalf("audio appeared: %d %s", rec.Code, rec.Body.String())
	}
	if missing() != 0 || adminAuditCount(t, a, "library.folder_emptied") != 0 {
		t.Fatal("something was marked despite the 409")
	}
	if err := os.Remove(filepath.Join(dir, "late.mp3")); err != nil {
		t.Fatal(err)
	}
	rec = a.adminPostAs(t, owner, path, nil)
	if rec.Code != http.StatusSeeOther || location(rec) != "/admin/libraries?marked=2" {
		t.Fatalf("mark: %d -> %q", rec.Code, location(rec))
	}
	if missing() != 2 || adminAuditCount(t, a, "library.folder_emptied") != 1 {
		t.Fatalf("missing %d, audit %d", missing(), adminAuditCount(t, a, "library.folder_emptied"))
	}
	var detail string
	_ = a.st.Reader().QueryRow(`SELECT detail_json FROM audit_log WHERE action = 'library.folder_emptied'`).Scan(&detail)
	var d map[string]int
	if json.Unmarshal([]byte(detail), &d) != nil || d["books"] != 2 {
		t.Errorf("audit detail = %s", detail)
	}
	files, _ := a.lib.AudioFiles(context.Background(), b.ID)
	for _, af := range files {
		if af.MissingSince == 0 {
			t.Error("a file of a marked book is not marked")
		}
	}
	// Nothing is left to mark the second time.
	if rec = a.adminPostAs(t, owner, path, nil); rec.Code != http.StatusConflict {
		t.Errorf("second post: %d", rec.Code)
	}
	// The files on disk were never touched.
	if _, err := os.Stat(dir); err != nil {
		t.Error(err)
	}
	if missing() != 2 {
		t.Error(fmt.Sprint("missing changed to ", missing()))
	}
}

func TestSidebarAdminLink(t *testing.T) {
	a := newTestApp(t)
	has := func(ck *http.Cookie) bool {
		rec := a.do(http.MethodGet, "/", nil, nil, ck)
		return strings.Contains(rec.Body.String(), `href="/admin/libraries"`)
	}
	if !has(a.as(t, users.RoleOwner)) {
		t.Error("the owner has no Libraries link")
	}
	if !has(a.as(t, users.RoleAdmin)) {
		t.Error("an admin has no Libraries link")
	}
	if has(a.as(t, users.RoleMember)) {
		t.Error("a member sees the Libraries link")
	}
	body := a.do(http.MethodGet, "/admin/libraries", nil, nil, a.as(t, users.RoleOwner)).Body.String()
	if !strings.Contains(body, `data-nav="/admin/libraries" aria-current="page"`) {
		t.Error("Libraries is not the current page on its own page")
	}
	if !strings.Contains(bookMain(body), "pages-admin") {
		t.Error("the admin stylesheet is not linked inside #main")
	}
}
