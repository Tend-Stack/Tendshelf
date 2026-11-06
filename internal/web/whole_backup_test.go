package web

import (
	"context"
	"html"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

// TestWholeBackupFolderRoutes: only the owner sets the backup folder; a refused folder (relative, missing,
// a file, inside the data folder) is shown in plain words under the field with nothing saved; a good one
// is saved and shown; admins, members and guests get 404 and change nothing.
func TestWholeBackupFolderRoutes(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	ctx := context.Background()
	body := backupsGet(a, owner, "/admin/backups").Body.String()
	for _, want := range []string{backupsFolderHeading, backupsFolderLabel, backupsFolderHint, backupsWholeTitle, "RESTORE.txt"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}

	if err := os.MkdirAll(a.dir+"/backups", 0o700); err != nil {
		t.Fatal(err)
	}
	file := a.dir + "/afile"
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ folder, want string }{
		{"backups", backupsErrRelative},
		{"/nonexistent/backup-folder-xyz", backupsErrMissing},
		{file, backupsErrNotDir},
		{a.dir, backupsErrInData},
		{a.dir + "/backups", backupsErrInData},
	} {
		rec := backupsPost(t, a, owner, "/admin/backups/folder", url.Values{"folder": {c.folder}}, false)
		usersWant(t, rec, http.StatusUnprocessableEntity, c.folder)
		b := html.UnescapeString(rec.Body.String())
		if !strings.Contains(b, c.want) || !strings.Contains(b, `value="`+c.folder+`"`) {
			t.Errorf("%q: message %q or the typed path missing", c.folder, c.want)
		}
		if got := a.srv.backups.Folder(ctx); got != "" {
			t.Fatalf("%q was saved as %q", c.folder, got)
		}
	}

	target := t.TempDir()
	rec := backupsPost(t, a, owner, "/admin/backups/folder", url.Values{"folder": {target}}, false)
	usersWant(t, rec, http.StatusSeeOther, "good folder")
	if location(rec) != "/admin/backups?saved=folder" || a.srv.backups.Folder(ctx) != target {
		t.Errorf("redirect %q, saved %q", location(rec), a.srv.backups.Folder(ctx))
	}
	body = backupsGet(a, owner, location(rec)).Body.String()
	for _, want := range []string{backupsFolderSaved, target, backupsFolderNever} {
		if !strings.Contains(body, want) {
			t.Errorf("saved page lacks %q", want)
		}
	}

	for _, role := range []users.Role{users.RoleAdmin, users.RoleMember, users.RoleGuest} {
		ck := a.as(t, role)
		usersWant(t, backupsPost(t, a, ck, "/admin/backups/folder", url.Values{"folder": {""}}, false), http.StatusNotFound, string(role))
	}
	usersWant(t, a.do(http.MethodPost, "/admin/backups/folder", url.Values{"folder": {""}}, nil, owner), http.StatusForbidden, "no token")
	if a.srv.backups.Folder(ctx) != target {
		t.Error("a refused request changed the folder")
	}

	rec = backupsPost(t, a, owner, "/admin/backups/folder", url.Values{"folder": {""}}, false)
	if location(rec) != "/admin/backups?saved=nofolder" || a.srv.backups.Folder(ctx) != "" {
		t.Errorf("clearing: %q, folder %q", location(rec), a.srv.backups.Folder(ctx))
	}
}

// TestWholeBackupFolderInLibrary: a folder inside a library folder is refused (the check reads the libraries).
func TestWholeBackupFolderInLibrary(t *testing.T) {
	e := sweepSeed(t)
	libs, err := e.a.srv.lib.Folders(context.Background(), e.libID)
	if err != nil || len(libs) == 0 {
		t.Skipf("the seeded library has no folder: %v", err)
	}
	if err := os.MkdirAll(libs[0].Path, 0o755); err != nil {
		t.Skip(err)
	}
	rec := e.comparePost(t, sweepOwner, "/admin/backups/folder", url.Values{"folder": {libs[0].Path}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), backupsErrInLibrary) {
		t.Errorf("a library folder: %d", rec.Code)
	}
}
