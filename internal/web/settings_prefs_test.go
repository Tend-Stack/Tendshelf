package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

var prefsSections = []string{"playback", "appearance", "accessibility", "privacy"}

func TestPrefsSections(t *testing.T) {
	a := newTestApp(t)
	for _, role := range []users.Role{users.RoleOwner, users.RoleMember, users.RoleGuest} {
		ck := a.as(t, role)
		for _, sec := range prefsSections {
			rec := a.do(http.MethodGet, "/me/settings/"+sec, nil, nil, ck)
			if rec.Code != http.StatusOK {
				t.Errorf("%s GET /me/settings/%s = %d", role, sec, rec.Code)
			}
		}
		if rec := a.do(http.MethodGet, "/me/settings/bogus", nil, nil, ck); rec.Code != http.StatusNotFound {
			t.Errorf("%s GET /me/settings/bogus = %d, want 404", role, rec.Code)
		}
	}
}

// prefsNavOf is the markup of the category list, so the sidebar's own aria-current stays out of it.
func prefsNavOf(body string) string {
	i := strings.Index(body, `<nav class="ts-prefs__nav"`)
	if i < 0 {
		return ""
	}
	j := strings.Index(body[i:], "</nav>")
	return body[i : i+j]
}

func TestPrefsCurrentItem(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	index := prefsNavOf(a.do(http.MethodGet, "/me/settings", nil, nil, ck).Body.String())
	if !strings.Contains(index, `href="/me/settings/playback" aria-current="true"`) {
		t.Error("the index does not mark Playback aria-current=true")
	}
	if strings.Contains(index, `aria-current="page"`) {
		t.Error("the index marks an item as the page")
	}
	page := prefsNavOf(a.do(http.MethodGet, "/me/settings/appearance", nil, nil, ck).Body.String())
	if !strings.Contains(page, `href="/me/settings/appearance" aria-current="page"`) {
		t.Error("Appearance is not aria-current=page on its own page")
	}
	if strings.Contains(page, `aria-current="true"`) {
		t.Error("a category page marks an item aria-current=true")
	}
	if strings.Contains(page, `href="/me/settings/playback" aria-current`) {
		t.Error("Playback is marked on the Appearance page")
	}
}

func TestPrefsPartialPost(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	before := settingsOf(t, a)
	rec := settingsPost(t, a, ck, "/me/settings", url.Values{"theme": {"light"}, "section": {"appearance"}}, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/me/settings/appearance" {
		t.Fatalf("post = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	got := settingsOf(t, a)
	want := before
	want.Theme = "light"
	if got != want {
		t.Errorf("stored %+v, want only the theme changed from %+v", got, before)
	}
	rec = settingsPost(t, a, ck, "/me/settings", url.Values{"speed": {"1.50"}, "section": {"../x"}}, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/me/settings" {
		t.Errorf("bad section = %d %s, want 303 /me/settings", rec.Code, rec.Header().Get("Location"))
	}
}

func TestPrefsInvalidRendersSection(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	rec := settingsPost(t, a, ck, "/me/settings", url.Values{"theme": {"neon"}, "section": {"appearance"}}, false)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("bad value = %d, want 422", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `id="st-theme-dark"`) || strings.Contains(body, `id="st-speed"`) {
		t.Error("the 422 does not render the Appearance section")
	}
	if !strings.Contains(body, `role="alert"`) {
		t.Error("the 422 has no refusal line")
	}
}

func TestPrefsListPerRole(t *testing.T) {
	a := newTestApp(t)
	member := a.do(http.MethodGet, "/me/settings", nil, nil, a.as(t, users.RoleMember)).Body.String()
	if strings.Contains(member, "Your server") || strings.Contains(member, `href="/admin/`) {
		t.Error("a member's list shows Your server or an admin link")
	}
	admin := a.as(t, users.RoleAdmin)
	body := a.do(http.MethodGet, "/me/settings", nil, nil, admin).Body.String()
	for _, l := range []string{`href="/admin/users"`, `href="/admin/libraries"`} {
		if !strings.Contains(body, l) {
			t.Errorf("an admin's list lacks %s", l)
		}
	}
	if strings.Contains(body, `href="/admin/email"`) {
		t.Error("an admin's list shows Email")
	}
	if rec := a.do(http.MethodGet, "/admin/email", nil, nil, admin); rec.Code != http.StatusNotFound {
		t.Errorf("admin GET /admin/email = %d, want 404", rec.Code)
	}
	owner := a.do(http.MethodGet, "/me/settings", nil, nil, a.owner(t)).Body.String()
	for _, l := range []string{`href="/admin/users"`, `href="/admin/libraries"`, `href="/admin/email"`} {
		if !strings.Contains(owner, l) {
			t.Errorf("the owner's list lacks %s", l)
		}
	}
}

func TestPrefsProfileEmailRow(t *testing.T) {
	a := newTestApp(t)
	if body := a.do(http.MethodGet, "/me", nil, nil, a.owner(t)).Body.String(); !strings.Contains(body, `href="/admin/email"`) {
		t.Error("the owner's Profile lacks the Email row")
	}
	if body := a.do(http.MethodGet, "/me", nil, nil, a.as(t, users.RoleAdmin)).Body.String(); strings.Contains(body, `href="/admin/email"`) {
		t.Error("an admin's Profile shows the Email row")
	}
}
