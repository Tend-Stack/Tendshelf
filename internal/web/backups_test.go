package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/backups"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// Backups tests (M3b-K). The job runner is never started, so queued backups stay queued.

func backupsPost(t *testing.T, a *testApp, ck *http.Cookie, path string, form url.Values, htmx bool) *httptest.ResponseRecorder {
	t.Helper()
	f := url.Values{"_csrf": {a.tokenFor(t, ck)}}
	for k, v := range form {
		f[k] = v
	}
	return a.do(http.MethodPost, path, f, func(r *http.Request) {
		if htmx {
			r.Header.Set("HX-Request", "true")
		}
	}, ck)
}

func backupsGet(a *testApp, ck *http.Cookie, path string) *httptest.ResponseRecorder {
	return a.do(http.MethodGet, path, nil, nil, ck)
}

func TestBackupsPage(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	rec := backupsGet(a, owner, "/admin/backups")
	usersWant(t, rec, http.StatusOK, "owner page")
	body := rec.Body.String()
	for _, want := range []string{"<h1", "Automatic backups", "Every day", "Every week", "Off", "No backup yet", "Back up now",
		"Tendshelf keeps the newest 7 copies.", "How to restore a copy", "Stop Tendshelf.", "Start Tendshelf again."} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	if !strings.Contains(body, `value="daily" checked`) {
		t.Error("daily is not the default choice")
	}
	// Copies show up with their size and name.
	if _, err := db.WriteCopy(context.Background(), a.st, a.dir, a.srv.now()); err != nil {
		t.Fatal(err)
	}
	body = backupsGet(a, owner, "/admin/backups").Body.String()
	if !strings.Contains(body, "Last backup just now") || !strings.Contains(body, "tendshelf-copy-v") {
		t.Errorf("copy not listed")
	}
	// The profile row and the preferences item are the owner's.
	if prof := backupsGet(a, owner, "/me").Body.String(); !strings.Contains(prof, `href="/admin/backups"`) || !strings.Contains(prof, "Last backup") {
		t.Error("profile has no Backups row")
	}
	if !strings.Contains(backupsGet(a, owner, "/me/settings").Body.String(), `href="/admin/backups"`) {
		t.Error("preferences list has no Backups item")
	}
}

func TestBackupsForbidden(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	id, err := a.srv.backups.Start(context.Background(), "", "manual")
	if err != nil {
		t.Fatal(err)
	}
	if rec := backupsGet(a, owner, "/hx/jobs/"+id); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Backing up") {
		t.Fatalf("owner job status = %d %q", rec.Code, rec.Body.String())
	}
	for _, role := range []users.Role{users.RoleAdmin, users.RoleMember, users.RoleGuest} {
		ck := a.as(t, role)
		for _, p := range []string{"/admin/backups", "/hx/jobs/" + id} {
			usersWant(t, backupsGet(a, ck, p), http.StatusNotFound, string(role)+" GET "+p)
		}
		for _, p := range []string{"/admin/backups/schedule", "/admin/backups/now"} {
			usersWant(t, backupsPost(t, a, ck, p, url.Values{"schedule": {"weekly"}}, false), http.StatusNotFound, string(role)+" POST "+p)
		}
		if strings.Contains(backupsGet(a, ck, "/me").Body.String(), "/admin/backups") {
			t.Errorf("%s sees the Backups row", role)
		}
	}
	if got := a.srv.backups.Schedule(context.Background()); got != backups.Daily {
		t.Errorf("schedule changed by a forbidden request: %s", got)
	}
}

func TestBackupsSchedule(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	rec := backupsPost(t, a, owner, "/admin/backups/schedule", url.Values{"schedule": {"hourly"}}, false)
	usersWant(t, rec, http.StatusUnprocessableEntity, "bad value")
	if !strings.Contains(rec.Body.String(), "Pick off, every day or every week.") {
		t.Error("no field message")
	}
	rec = backupsPost(t, a, owner, "/admin/backups/schedule", url.Values{"schedule": {"hourly"}}, true)
	usersWant(t, rec, http.StatusUnprocessableEntity, "bad value (htmx)")
	if strings.Contains(rec.Body.String(), "<html") || !strings.Contains(rec.Body.String(), "Pick off") {
		t.Errorf("htmx error is not a toast fragment: %q", rec.Body.String())
	}
	rec = backupsPost(t, a, owner, "/admin/backups/schedule", url.Values{"schedule": {"weekly"}}, false)
	usersWant(t, rec, http.StatusSeeOther, "weekly")
	if location(rec) != "/admin/backups?saved=1" {
		t.Errorf("redirect = %q", location(rec))
	}
	if got := a.srv.backups.Schedule(context.Background()); got != backups.Weekly {
		t.Errorf("schedule = %s", got)
	}
	body := backupsGet(a, owner, location(rec)).Body.String()
	if !strings.Contains(body, "Backup schedule saved.") || !strings.Contains(body, `value="weekly" checked`) {
		t.Error("saved page")
	}
	// No CSRF token, no change.
	usersWant(t, a.do(http.MethodPost, "/admin/backups/schedule", url.Values{"schedule": {"off"}}, nil, owner), http.StatusForbidden, "no token")
	usersWant(t, a.do(http.MethodPost, "/admin/backups/now", url.Values{}, nil, owner), http.StatusForbidden, "no token (now)")
}

func TestBackupsNow(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	rec := backupsPost(t, a, owner, "/admin/backups/now", nil, true)
	usersWant(t, rec, http.StatusOK, "htmx")
	body := rec.Body.String()
	if !strings.Contains(body, `id="backup-status"`) || !strings.Contains(body, `hx-trigger="every 2s"`) || !strings.Contains(body, "Backing up") || strings.Contains(body, "<html") {
		t.Fatalf("status fragment: %q", body)
	}
	i := strings.Index(body, "/hx/jobs/")
	if i < 0 {
		t.Fatal("fragment does not poll /hx/jobs/")
	}
	id := body[i+len("/hx/jobs/"):]
	id = id[:strings.IndexByte(id, '"')]
	plain := backupsPost(t, a, owner, "/admin/backups/now", nil, false)
	usersWant(t, plain, http.StatusSeeOther, "plain")
	if location(plain) != "/admin/backups?job="+id {
		t.Errorf("a second press: location %q, want the same job %s", location(plain), id)
	}
	page := backupsGet(a, owner, location(plain)).Body.String()
	if !strings.Contains(page, "Backing up") {
		t.Error("plain page does not show the running job")
	}
	// Done and failed lines stop polling.
	a.adminSetJobState(t, id, "done")
	if s := backupsGet(a, owner, "/hx/jobs/"+id).Body.String(); !strings.Contains(s, "Backed up") || strings.Contains(s, "every 2s") {
		t.Errorf("done fragment: %q", s)
	}
	if _, err := a.st.Writer().Exec(`UPDATE jobs SET state = 'failed', error_code = 'backup_failed' WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	s := backupsGet(a, owner, "/hx/jobs/"+id).Body.String()
	if !strings.Contains(s, "The backup didn&#39;t finish. Check that the data folder has free space.") || strings.Contains(s, "every 2s") {
		t.Errorf("failed fragment: %q", s)
	}
	var n int
	if err := a.st.Reader().QueryRow(`SELECT count(*) FROM jobs WHERE kind = ?`, backups.Kind).Scan(&n); err != nil || n != 1 {
		t.Errorf("jobs = %d, %v", n, err)
	}
	if _, err := os.Stat(filepath.Join(a.dir, "backups")); err == nil {
		t.Error("a backup folder exists before any job ran")
	}
}
