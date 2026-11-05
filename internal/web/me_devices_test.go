package web

import (
	"database/sql"
	"net/http"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// deviceFor issues a device token with a name and kind for a person and returns its id.
func deviceFor(t *testing.T, a *testApp, userID, name string, kind auth.DeviceKind) string {
	t.Helper()
	err := a.st.WriteTx(t.Context(), func(tx *sql.Tx) error {
		_, _, err := a.srv.devices.Issue(t.Context(), tx, userID, name, kind)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	var id string
	if err := a.st.Reader().QueryRow(`SELECT id FROM devices WHERE user_id = ? AND name = ?`, userID, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestProfileDevicesMerged: "Your devices" lists this browser first, then the other browsers and the
// apps together, each app with its kind and "Not used yet", a sign-out form each, and counts both
// kinds in "Sign out everywhere else". Nobody else's rows appear.
func TestProfileDevicesMerged(t *testing.T) {
	a := newTestApp(t)
	owner := a.owner(t)
	ownerID := sweepUserID(t, a, owner)
	member := a.as(t, users.RoleMember)
	settingsLogin(t, a, settingsChromeLinux)
	phone := deviceFor(t, a, ownerID, "Pixel 8", auth.KindMobile)
	deviceFor(t, a, sweepUserID(t, a, member), "Their laptop", auth.KindDesktop)

	page := a.do(http.MethodGet, "/me/devices", nil, nil, owner).Body.String()
	for _, frag := range []string{
		"This device", "Chrome on Linux", "Pixel 8", "Phone app · Not used yet",
		"/me/devices/" + phone + "/signout", `aria-label="Sign out Pixel 8"`, "Sign out everywhere else",
	} {
		if !strings.Contains(page, frag) {
			t.Errorf("Profile lacks %q", frag)
		}
	}
	if strings.Contains(page, "Their laptop") {
		t.Error("another person's app is listed")
	}
	if i, j := strings.Index(page, "This device"), strings.Index(page, "Pixel 8"); i < 0 || j < i {
		t.Error("this device is not first")
	}

	// Signing the app out removes it and the "everywhere else" button stays for the browser.
	rec := a.do(http.MethodPost, "/me/devices/"+phone+"/signout", map[string][]string{"_csrf": {a.tokenFor(t, owner)}}, nil, owner)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("sign out app = %d", rec.Code)
	}
	if page = a.do(http.MethodGet, "/me/devices", nil, nil, owner).Body.String(); strings.Contains(page, "Pixel 8") {
		t.Error("the revoked app is still listed")
	}
}

// TestUsersDevicesTab: the Devices tab counts every live browser and app, lists them per person, and
// shows Sign out only for a person the viewer may manage: the owner sees it for a member, an admin
// does not for the owner, and nobody for themself.
func TestUsersDevicesTab(t *testing.T) {
	a := newTestApp(t)
	owner := a.owner(t)
	ownerID := sweepUserID(t, a, owner)
	admin := a.as(t, users.RoleAdmin)
	member := a.as(t, users.RoleMember)
	memberID := sweepUserID(t, a, member)
	dev := deviceFor(t, a, memberID, "Kitchen tablet", auth.KindPWA)

	// Sessions: owner, admin, member (3) plus the one app.
	page := a.do(http.MethodGet, "/admin/users", nil, nil, owner).Body.String()
	for _, frag := range []string{`href="#users-devices"`, "Devices · 4", `id="users-devices"`, "Kitchen tablet", "Installed app · Not used yet",
		"/admin/users/" + memberID + "/devices/" + dev + "/signout", "Manage your own on your Profile."} {
		if !strings.Contains(page, frag) {
			t.Errorf("owner's Users page lacks %q", frag)
		}
	}
	if strings.Contains(page, "/admin/users/"+ownerID+"/devices/") {
		t.Error("the owner is offered a sign-out for themself")
	}

	page = a.do(http.MethodGet, "/admin/users", nil, nil, admin).Body.String()
	if strings.Contains(page, "/admin/users/"+ownerID+"/devices/") {
		t.Error("an admin is offered a sign-out for the owner")
	}
	if !strings.Contains(page, "Kitchen tablet") {
		t.Error("an admin does not see a member's app")
	}
	if !strings.Contains(page, "/admin/users/"+memberID+"/devices/"+dev+"/signout") {
		t.Error("an admin has no sign-out for a member's app")
	}
}
