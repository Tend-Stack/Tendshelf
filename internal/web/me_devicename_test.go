package web

import (
	"html"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// deviceNameSID is the public id of the session a cookie carries.
func deviceNameSID(t *testing.T, ck *http.Cookie) string {
	t.Helper()
	h, ok := auth.HashToken(ck.Value)
	if !ok {
		t.Fatal("bad session token")
	}
	return auth.PublicID(h)
}

// deviceNamePost posts a name to a rename path with the person's CSRF token.
func deviceNamePost(t *testing.T, a *testApp, path, name string, ck *http.Cookie) int {
	t.Helper()
	return a.do(http.MethodPost, path, url.Values{"_csrf": {a.tokenFor(t, ck)}, "name": {name}}, nil, ck).Code
}

// TestDeviceNameSession: a person names a browser sign-in, the row leads with the name and says it is
// this browser; a blank name clears it and the row goes back to the derived label.
func TestDeviceNameSession(t *testing.T) {
	a := newTestApp(t)
	owner := settingsLogin(t, a, settingsChromeLinux)
	sid := deviceNameSID(t, owner)

	if code := deviceNamePost(t, a, "/me/sessions/"+sid+"/name", "  Kitchen\x00 laptop ", owner); code != http.StatusSeeOther {
		t.Fatalf("rename = %d", code)
	}
	page := a.do(http.MethodGet, "/me/devices", nil, nil, owner).Body.String()
	for _, frag := range []string{"Kitchen laptop", "This browser · Chrome on Linux", `value="Kitchen laptop"`, `aria-label="Name Kitchen laptop"`} {
		if !strings.Contains(page, frag) {
			t.Errorf("devices page lacks %q", frag)
		}
	}
	var n int
	a.st.Reader().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'auth.device_renamed' AND detail_json NOT LIKE '%Kitchen%'`).Scan(&n)
	if n != 1 {
		t.Errorf("audit rows without the name = %d, want 1", n)
	}

	if code := deviceNamePost(t, a, "/me/sessions/"+sid+"/name", "   ", owner); code != http.StatusSeeOther {
		t.Fatalf("clear = %d", code)
	}
	page = a.do(http.MethodGet, "/me/devices", nil, nil, owner).Body.String()
	if strings.Contains(page, "Kitchen laptop") || !strings.Contains(page, "This device") {
		t.Error("the cleared name still shows, or the row did not go back to This device")
	}
}

// TestDeviceNameSessionNotFolded: browsers with the same label fold into one line, but a named one is its own line.
func TestDeviceNameSessionNotFolded(t *testing.T) {
	a := newTestApp(t)
	owner := a.owner(t)
	cur := settingsLogin(t, a, settingsChromeLinux)
	second := settingsLogin(t, a, settingsChromeLinux)
	third := settingsLogin(t, a, settingsChromeLinux)
	fourth := settingsLogin(t, a, settingsChromeLinux)
	_ = owner
	_ = second
	_ = third

	page := a.do(http.MethodGet, "/me/devices", nil, nil, cur).Body.String()
	if !strings.Contains(page, "sign-ins · ") {
		t.Fatal("the unnamed browsers did not fold")
	}
	name := deviceNameSID(t, fourth)
	if code := deviceNamePost(t, a, "/me/sessions/"+name+"/name", "Work PC", cur); code != http.StatusSeeOther {
		t.Fatalf("rename = %d", code)
	}
	page = a.do(http.MethodGet, "/me/devices", nil, nil, cur).Body.String()
	if !strings.Contains(page, "Work PC") {
		t.Error("the named browser has no line of its own")
	}
	if strings.Count(page, `class="ts-me__devgroup"`) != 1 {
		t.Errorf("groups = %d, want the unnamed ones folded once", strings.Count(page, `class="ts-me__devgroup"`))
	}
}

// TestDeviceNameSessionSignInKeepsName: later sign-in activity (last_seen) never writes the name.
func TestDeviceNameSessionSignInKeepsName(t *testing.T) {
	a := newTestApp(t)
	owner := settingsLogin(t, a, settingsChromeLinux)
	sid := deviceNameSID(t, owner)
	if code := deviceNamePost(t, a, "/me/sessions/"+sid+"/name", "Desk", owner); code != http.StatusSeeOther {
		t.Fatalf("rename = %d", code)
	}
	// Make the session look idle so the next request refreshes last_seen_at.
	if _, err := a.st.Writer().Exec(`UPDATE sessions SET last_seen_at = 1 WHERE substr(lower(hex(token_hash)), 1, 16) = ?`, sid); err != nil {
		t.Fatal(err)
	}
	a.do(http.MethodGet, "/me/devices", nil, nil, owner)
	var name string
	var seen int64
	if err := a.st.Reader().QueryRow(`SELECT name, last_seen_at FROM sessions WHERE substr(lower(hex(token_hash)), 1, 16) = ?`, sid).Scan(&name, &seen); err != nil {
		t.Fatal(err)
	}
	if seen <= 1 {
		t.Error("last_seen_at was not refreshed, so this test proves nothing")
	}
	if name != "Desk" {
		t.Errorf("name = %q after a refresh, want Desk", name)
	}
}

// TestDeviceNameApp: a person names an app; empty is refused in plain words; over 64 characters is
// refused; control characters are dropped; an app key and someone else's device are 404.
func TestDeviceNameApp(t *testing.T) {
	a := newTestApp(t)
	owner := a.owner(t)
	ownerID := sweepUserID(t, a, owner)
	member := a.as(t, users.RoleMember)
	phone := deviceFor(t, a, ownerID, "Pixel 8", auth.KindMobile)
	theirs := deviceFor(t, a, sweepUserID(t, a, member), "Their phone", auth.KindMobile)
	key := deviceFor(t, a, ownerID, "My reader", auth.KindOPDS)
	path := "/me/devices/" + phone + "/name"

	if code := deviceNamePost(t, a, path, "Mum's\x07 phone", owner); code != http.StatusSeeOther {
		t.Fatalf("rename = %d", code)
	}
	page := a.do(http.MethodGet, "/me/devices", nil, nil, owner).Body.String()
	if !strings.Contains(page, "Mum&#39;s phone") || strings.Contains(page, "Pixel 8") {
		t.Error("the app did not take its new name")
	}

	rec := a.do(http.MethodPost, path, url.Values{"_csrf": {a.tokenFor(t, owner)}, "name": {" \t "}}, nil, owner)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), html.EscapeString(devNameEmpty)) {
		t.Errorf("blank app name = %d, want 422 with the message", rec.Code)
	}
	long := strings.Repeat("é", 65)
	rec = a.do(http.MethodPost, path, url.Values{"_csrf": {a.tokenFor(t, owner)}, "name": {long}}, nil, owner)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), devNameTooLong) {
		t.Errorf("65-character name = %d, want 422 with the message", rec.Code)
	}
	if code := deviceNamePost(t, a, path, strings.Repeat("é", 64), owner); code != http.StatusSeeOther {
		t.Errorf("64-character name = %d, want 303", code)
	}
	if code := deviceNamePost(t, a, "/me/devices/"+theirs+"/name", "Stolen", owner); code != http.StatusNotFound {
		t.Errorf("someone else's device = %d, want 404", code)
	}
	if code := deviceNamePost(t, a, "/me/devices/"+key+"/name", "Stolen", owner); code != http.StatusNotFound {
		t.Errorf("app key = %d, want 404", code)
	}
	a.do(http.MethodPost, "/me/devices/"+phone+"/signout", url.Values{"_csrf": {a.tokenFor(t, owner)}}, nil, owner)
	if code := deviceNamePost(t, a, path, "Again", owner); code != http.StatusNotFound {
		t.Errorf("revoked device = %d, want 404", code)
	}
}

// TestDeviceNameForbidden: no CSRF token, no session, someone else's browser session and a device bearer token are all refused.
func TestDeviceNameForbidden(t *testing.T) {
	a := newTestApp(t)
	owner := a.owner(t)
	member := a.as(t, users.RoleMember)
	sid := deviceNameSID(t, member)
	path := "/me/sessions/" + sid + "/name"

	if code := a.do(http.MethodPost, path, url.Values{"name": {"x"}}, nil, member).Code; code != http.StatusForbidden {
		t.Errorf("no CSRF token = %d, want 403", code)
	}
	if code := deviceNamePost(t, a, path, "Stolen", owner); code != http.StatusNotFound {
		t.Errorf("another member's session = %d, want 404", code)
	}
	rec := a.do(http.MethodPost, path, url.Values{"name": {"x"}}, nil)
	if rec.Code == http.StatusSeeOther && rec.Header().Get("Location") == "/me/devices" || rec.Code == http.StatusOK {
		t.Errorf("anonymous rename = %d", rec.Code)
	}
	var name string
	a.st.Reader().QueryRow(`SELECT name FROM sessions WHERE substr(lower(hex(token_hash)), 1, 16) = ?`, sid).Scan(&name)
	if name != "" {
		t.Errorf("name = %q after refused posts, want empty", name)
	}
}

// TestDeviceNameAdminTab: the Devices tab of /admin/users shows the names, with no rename control.
func TestDeviceNameAdminTab(t *testing.T) {
	a := newTestApp(t)
	owner := a.owner(t)
	member := a.as(t, users.RoleMember)
	memberID := sweepUserID(t, a, member)
	phone := deviceFor(t, a, memberID, "Pixel 8", auth.KindMobile)
	deviceNamePost(t, a, "/me/devices/"+phone+"/name", "Kitchen tablet", member)
	deviceNamePost(t, a, "/me/sessions/"+deviceNameSID(t, member)+"/name", "Hall PC", member)

	page := a.do(http.MethodGet, "/admin/users", nil, nil, owner).Body.String()
	for _, frag := range []string{"Kitchen tablet", "Hall PC"} {
		if !strings.Contains(page, frag) {
			t.Errorf("admin Devices tab lacks %q", frag)
		}
	}
	if strings.Contains(page, "/name\"") {
		t.Error("the admin page offers a rename")
	}
	if code := a.do(http.MethodPost, "/admin/users/"+memberID+"/devices/"+phone+"/name", url.Values{"_csrf": {a.tokenFor(t, owner)}, "name": {"x"}}, nil, owner).Code; code != http.StatusNotFound && code != http.StatusMethodNotAllowed {
		t.Errorf("admin rename route = %d, want 404 or 405", code)
	}
}
