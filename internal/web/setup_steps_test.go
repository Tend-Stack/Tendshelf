package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

func TestSetupStepsFlow(t *testing.T) {
	a := newTestApp(t)
	// First-run setup leads to step 2.
	c, tok := a.anon(t, "/setup")
	rec := a.do(http.MethodPost, "/setup", setupForm(tok), nil, c)
	if rec.Code != http.StatusSeeOther || location(rec) != "/setup/server" {
		t.Fatalf("setup: %d -> %q", rec.Code, location(rec))
	}
	owner := cookieNamed(rec, "ts_session")

	rec = a.do(http.MethodGet, "/setup/server", nil, nil, owner)
	body := adminHTMLUnescape(rec.Body.String())
	if rec.Code != 200 || !strings.Contains(body, "Let's set up your shelf") || !strings.Contains(body, "Step 2 of 3") ||
		!strings.Contains(body, "Name your server") {
		t.Fatalf("step 2: %d %s", rec.Code, body)
	}
	rec = a.adminPostAs(t, owner, "/setup/server", url.Values{"name": {"  Reyes family shelf "}})
	if rec.Code != http.StatusSeeOther || location(rec) != "/setup/folder" {
		t.Fatalf("name: %d -> %q", rec.Code, location(rec))
	}
	var name string
	if ok, err := db.GetSetting(context.Background(), a.st.Reader(), "server.name", &name); err != nil || !ok || name != "Reyes family shelf" {
		t.Fatalf("server.name = %q %v %v", name, ok, err)
	}
	if adminAuditCount(t, a, "setup.server_named") != 1 {
		t.Error("no audit row for the server name")
	}

	rec = a.do(http.MethodGet, "/setup/folder", nil, nil, owner)
	body = adminHTMLUnescape(rec.Body.String())
	if rec.Code != 200 || !strings.Contains(body, "Where are your audiobooks?") || !strings.Contains(body, "Step 3 of 3") ||
		!strings.Contains(body, "Type the full path, like /srv/media/audiobooks.") || !strings.Contains(body, `id="folder"`) ||
		!strings.Contains(body, "Use this folder") {
		t.Fatalf("step 3: %d %s", rec.Code, body)
	}
	dir := adminAudioDir(t, true)
	rec = a.adminPostAs(t, owner, "/setup/folder", url.Values{"folder": {dir}})
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(location(rec), "/setup/folder?job=") {
		t.Fatalf("folder: %d -> %q %s", rec.Code, location(rec), rec.Body.String())
	}
	jobs := adminScanJobs(t, a)
	if len(jobs) != 1 || jobs[0].CreatedBy == "" {
		t.Fatalf("scan jobs = %+v", jobs)
	}
	rec = a.do(http.MethodGet, location(rec), nil, nil, owner)
	body = adminHTMLUnescape(rec.Body.String())
	if rec.Code != 200 || !strings.Contains(body, `hx-trigger="every 2s"`) || !strings.Contains(body, "Tendshelf can read this folder. It never changes your files.") ||
		!strings.Contains(body, "You can keep going while it reads.") || !strings.Contains(body, `href="/setup/done"`) || !strings.Contains(body, "readonly") {
		t.Fatalf("progress page: %d %s", rec.Code, body)
	}
	if adminAuditCount(t, a, "library.folder_added") != 1 {
		t.Error("no audit row for the folder")
	}
	libs, _ := a.lib.Libraries(context.Background())
	if len(libs) != 1 || libs[0].Name != libraryNameFor(dir) {
		t.Fatalf("libraries = %+v", libs)
	}

	// Done: counts in words.
	_, f := librarytest.NewLibrary(t, a.lib, "Elsewhere", adminAudioDir(t, false))
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "A", DurationMs: 90 * 60 * 1000, Series: "S", Narrators: []string{"N"}})
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "B", DurationMs: 90 * 60 * 1000, Refused: "drm"})
	rec = a.do(http.MethodGet, "/setup/done", nil, nil, owner)
	body = adminHTMLUnescape(rec.Body.String())
	for _, want := range []string{"Your shelf is ready", "1 book, all in one place", "2 hours of listening · 1 series · 1 narrator.",
		"Covers and details came from your files.", "1 book needs a look.", "See what's up", "Start listening"} {
		if !strings.Contains(body, want) {
			t.Errorf("done page lacks %q: %s", want, body)
		}
	}

	// Once the folder is added the steps send the owner home.
	for _, p := range []string{"/setup/server", "/setup/folder"} {
		if rec := a.do(http.MethodGet, p, nil, nil, owner); rec.Code != http.StatusSeeOther || location(rec) != "/" {
			t.Errorf("GET %s after setup: %d -> %q", p, rec.Code, location(rec))
		}
		if rec := a.adminPostAs(t, owner, p, url.Values{"name": {"x"}, "folder": {adminAudioDir(t, false)}}); rec.Code != http.StatusSeeOther || location(rec) != "/" {
			t.Errorf("POST %s after setup: %d -> %q", p, rec.Code, location(rec))
		}
	}
	if libs, _ := a.lib.Libraries(context.Background()); len(libs) != 2 {
		t.Errorf("a finished setup still created a library: %+v", libs)
	}
}

func TestSetupDoneEmpty(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	body := adminHTMLUnescape(a.do(http.MethodGet, "/setup/done", nil, nil, owner).Body.String())
	if !strings.Contains(body, "Tendshelf is still reading your folder") || !strings.Contains(body, "Books appear on Home as they're found.") {
		t.Errorf("empty done page: %s", body)
	}
}

func TestSetupDoneLeavesOutZeroCounts(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	_, f := librarytest.NewLibrary(t, a.lib, "Audiobooks", adminAudioDir(t, false))
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Alone", DurationMs: 3 * 60 * 60 * 1000})
	body := adminHTMLUnescape(a.do(http.MethodGet, "/setup/done", nil, nil, owner).Body.String())
	if !strings.Contains(body, "3 hours of listening. Covers and details came from your files.") {
		t.Errorf("stats line: %s", body)
	}
	if strings.Contains(body, "0 series") || strings.Contains(body, "0 narrators") {
		t.Error("zero counts are shown")
	}
}

func TestSetupStepsForbidden(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	member := a.as(t, users.RoleMember)
	admin := a.as(t, users.RoleAdmin)
	paths := []string{"/setup/server", "/setup/folder", "/setup/done"}

	for _, p := range paths {
		rec := a.do(http.MethodGet, p, nil, nil)
		// SafeNext never returns to a /setup page, so the sign-in link carries no next.
		if rec.Code != http.StatusSeeOther || location(rec) != "/login" {
			t.Errorf("anonymous GET %s: %d -> %q", p, rec.Code, location(rec))
		}
		for name, ck := range map[string]*http.Cookie{"member": member, "admin": admin} {
			if rec := a.do(http.MethodGet, p, nil, nil, ck); rec.Code != http.StatusNotFound {
				t.Errorf("GET %s as %s: %d, want 404", p, name, rec.Code)
			}
		}
	}
	for _, p := range paths[:2] {
		for name, ck := range map[string]*http.Cookie{"member": member, "admin": admin} {
			if rec := a.adminPostAs(t, ck, p, url.Values{"name": {"x"}}); rec.Code != http.StatusNotFound {
				t.Errorf("POST %s as %s: %d, want 404", p, name, rec.Code)
			}
		}
		if rec := a.do(http.MethodPost, p, url.Values{"name": {"x"}}, nil, owner); rec.Code != http.StatusForbidden {
			t.Errorf("POST %s without a token: %d, want 403", p, rec.Code)
		}
	}
	// Invalid input is a 422 with plain words.
	for name, c := range map[string]struct{ val, msg string }{
		"empty":     {"   ", "Give your server a name."},
		"too long":  {strings.Repeat("n", 81), "Use 80 characters or fewer."},
		"control":   {"a\x07b", "Give your server a name."},
		"newline":   {"a\nb", "Give your server a name."},
		"tab break": {"\t", "Give your server a name."},
	} {
		rec := a.adminPostAs(t, owner, "/setup/server", url.Values{"name": {c.val}})
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(adminHTMLUnescape(rec.Body.String()), c.msg) {
			t.Errorf("name %s: %d", name, rec.Code)
		}
	}
	rec := a.adminPostAs(t, owner, "/setup/folder", url.Values{"folder": {"relative/path"}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(adminHTMLUnescape(rec.Body.String()), "Type the full path to the folder") {
		t.Errorf("relative folder: %d", rec.Code)
	}
	var done bool
	if ok, _ := db.GetSetting(context.Background(), a.st.Reader(), "setup.done", &done); ok {
		t.Error("setup.done was set by refused requests")
	}
}
