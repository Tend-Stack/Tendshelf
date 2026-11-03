package web

import (
	"net/http"
	"strings"
	"testing"
)

// prefsServerPages lists the server pages that render inside the preferences window, each with the
// category its list marks and the lowest role allowed (sweep order: owner 0, admin 1).
func prefsServerPages(e *sweepEnv) []struct {
	path, cat string
	lowest    int
} {
	return []struct {
		path, cat string
		lowest    int
	}{
		{"/admin/users", "users", sweepAdmin},
		{"/admin/users/" + e.extra, "users", sweepAdmin},
		{"/admin/users/" + e.extra + "/stop", "users", sweepAdmin},
		{"/admin/libraries", "libraries", sweepAdmin},
		{"/admin/libraries/folders/new", "libraries", sweepAdmin},
		{"/admin/folders/" + e.folderID + "/emptied", "libraries", sweepAdmin},
		{"/admin/libraries/" + e.libID + "/lookups", "libraries", sweepAdmin},
		{"/admin/email", "email", sweepOwner},
	}
}

func TestPrefsServerPagesInShell(t *testing.T) {
	e := sweepSeed(t)
	for _, p := range prefsServerPages(e) {
		for role := sweepOwner; role <= sweepGuest; role++ {
			rec := e.compareGet(role, p.path, nil)
			if role > p.lowest {
				if rec.Code != http.StatusNotFound {
					t.Errorf("%s GET %s = %d, want 404", sweepRoleNames[role], p.path, rec.Code)
				}
				continue
			}
			if rec.Code != http.StatusOK {
				t.Errorf("%s GET %s = %d, want 200", sweepRoleNames[role], p.path, rec.Code)
				continue
			}
			list := prefsNavOf(rec.Body.String())
			if list == "" {
				t.Errorf("%s GET %s has no category list", sweepRoleNames[role], p.path)
				continue
			}
			want := `href="/admin/` + p.cat + `" aria-current="page"`
			if p.cat == "users" || p.cat == "libraries" || p.cat == "email" {
				if !strings.Contains(list, want) {
					t.Errorf("%s GET %s does not mark %s current", sweepRoleNames[role], p.path, p.cat)
				}
			}
			if strings.Count(list, `aria-current="page"`) != 1 {
				t.Errorf("%s GET %s marks %d items as the page", sweepRoleNames[role], p.path, strings.Count(list, `aria-current="page"`))
			}
		}
	}
}

func TestPrefsServerListPerRole(t *testing.T) {
	e := sweepSeed(t)
	member := e.compareGet(sweepMember, "/me/settings", nil).Body.String()
	if strings.Contains(member, "Your server") || strings.Contains(member, `href="/admin/`) {
		t.Error("a member's list shows Your server or an admin link")
	}
	for _, path := range []string{"/admin/users", "/admin/libraries", "/admin/email"} {
		if rec := e.compareGet(sweepMember, path, nil); rec.Code != http.StatusNotFound {
			t.Errorf("member GET %s = %d, want 404", path, rec.Code)
		}
	}
	admin := prefsNavOf(e.compareGet(sweepAdmin, "/admin/users", nil).Body.String())
	for _, l := range []string{`href="/admin/users"`, `href="/admin/libraries"`} {
		if !strings.Contains(admin, l) {
			t.Errorf("an admin's list lacks %s", l)
		}
	}
	if strings.Contains(admin, `href="/admin/email"`) {
		t.Error("an admin's list shows Email")
	}
	if rec := e.compareGet(sweepAdmin, "/admin/email", nil); rec.Code != http.StatusNotFound {
		t.Errorf("admin GET /admin/email = %d, want 404", rec.Code)
	}
	owner := prefsNavOf(e.compareGet(sweepOwner, "/admin/email", nil).Body.String())
	for _, l := range []string{`href="/admin/users"`, `href="/admin/libraries"`, `href="/admin/email"`} {
		if !strings.Contains(owner, l) {
			t.Errorf("the owner's list lacks %s", l)
		}
	}
}
