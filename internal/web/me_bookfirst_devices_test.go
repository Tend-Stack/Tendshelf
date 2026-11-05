package web

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/users"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// bookfirstSession is a browser session row for the grouping table.
func bookfirstSession(id, title, meta string) pages.ProfileDevice {
	return pages.ProfileDevice{ID: id, Title: title, Meta: meta, Session: true, Action: "/me/sessions/" + id + "/signout"}
}

func TestBookFirstGroupDevices(t *testing.T) {
	cur := pages.ProfileDevice{ID: "cur", Title: msgThisBrowser, Meta: "Chrome on Linux", Current: true, Session: true}
	app := func(id, name string) pages.ProfileDevice {
		return pages.ProfileDevice{ID: id, Title: name, Meta: "Phone app · 1 hour ago", Action: "/me/devices/" + id + "/signout"}
	}
	tests := []struct {
		name     string
		rows     []pages.ProfileDevice
		wantIDs  []string // the ID of each line, in order
		wantSess []int    // sessions per line (0 = a plain line)
		wantMeta map[int]string
	}{
		{name: "no sessions", rows: nil},
		{name: "current only", rows: []pages.ProfileDevice{cur}, wantIDs: []string{"cur"}, wantSess: []int{0}},
		{
			name:    "current stays alone beside one same label",
			rows:    []pages.ProfileDevice{cur, bookfirstSession("a", "Chrome on Linux", "2 hours ago")},
			wantIDs: []string{"cur", "a"}, wantSess: []int{0, 0},
			wantMeta: map[int]string{1: "2 hours ago"},
		},
		{
			name: "three same label",
			rows: []pages.ProfileDevice{cur, bookfirstSession("a", "Chrome on Linux", "2 hours ago"),
				bookfirstSession("b", "Chrome on Linux", "Yesterday"), bookfirstSession("c", "Chrome on Linux", "Last week")},
			wantIDs: []string{"cur", "a"}, wantSess: []int{0, 3},
			wantMeta: map[int]string{1: "3 sign-ins · 2 hours ago"},
		},
		{
			name: "two labels, order follows the newest of each",
			rows: []pages.ProfileDevice{bookfirstSession("a", "Safari", "1 hour ago"), bookfirstSession("b", "Chrome on Linux", "2 hours ago"),
				bookfirstSession("c", "Safari", "Yesterday"), bookfirstSession("d", "Chrome on Linux", "Last week")},
			wantIDs: []string{"a", "b"}, wantSess: []int{2, 2},
			wantMeta: map[int]string{0: "2 sign-ins · 1 hour ago", 1: "2 sign-ins · 2 hours ago"},
		},
		{
			name:    "apps with equal names never fold",
			rows:    []pages.ProfileDevice{app("x", "Pixel 8"), app("y", "Pixel 8")},
			wantIDs: []string{"x", "y"}, wantSess: []int{0, 0},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := groupDevices(tc.rows)
			if len(got) != len(tc.wantIDs) {
				t.Fatalf("%d lines, want %d", len(got), len(tc.wantIDs))
			}
			for i, l := range got {
				if l.ID != tc.wantIDs[i] || len(l.Sessions) != tc.wantSess[i] {
					t.Errorf("line %d = %s with %d sessions, want %s with %d", i, l.ID, len(l.Sessions), tc.wantIDs[i], tc.wantSess[i])
				}
				if want, ok := tc.wantMeta[i]; ok && l.Meta != want {
					t.Errorf("line %d Meta = %q, want %q", i, l.Meta, want)
				}
				for _, s := range l.Sessions {
					if !strings.HasPrefix(s.Aria, "Sign out "+l.Title+", ") {
						t.Errorf("session %s aria = %q", s.ID, s.Aria)
					}
				}
			}
		})
	}
}

func TestBookFirstDevicesLine(t *testing.T) {
	if got := []string{devicesLine(0), devicesLine(1), devicesLine(8)}; got[0] != "Only this device" || got[1] != "This device and 1 other" || got[2] != "This device and 8 others" {
		t.Fatalf("lines = %q", got)
	}
	a := newTestApp(t)
	owner := a.owner(t)
	page := func() string { return a.do(http.MethodGet, "/me", nil, nil, owner).Body.String() }
	if p := page(); !strings.Contains(p, "Only this device") || !strings.Contains(p, `href="/me/devices"`) {
		t.Error("Profile lacks the single-device line or the link")
	}
	settingsLogin(t, a, settingsChromeLinux)
	if p := page(); !strings.Contains(p, "This device and 1 other") {
		t.Error("Profile lacks the one-other line")
	}
	settingsLogin(t, a, "curl/8")
	p := page()
	if !strings.Contains(p, "This device and 2 others") {
		t.Error("Profile lacks the many-others line")
	}
	if strings.Contains(p, `/signout"`) {
		t.Error("Profile still carries a sign-out form")
	}
	if strings.Contains(p, "Sign out everywhere else") {
		t.Error("Profile still carries the sweep button")
	}
}

func TestBookFirstDevicesPage(t *testing.T) {
	a := newTestApp(t)
	owner := a.owner(t)
	member := a.as(t, users.RoleMember)
	mine := map[string]bool{settingsDeviceID(t, owner): true}
	for range 5 {
		mine[settingsDeviceID(t, settingsLogin(t, a, settingsChromeLinux))] = true
	}
	formRE := regexp.MustCompile(`action="/me/sessions/([0-9a-f]{16})/signout"`)
	page := a.do(http.MethodGet, "/me/devices", nil, nil, owner).Body.String()
	if n := strings.Count(page, `<details class="ts-me__devgroup"`); n != 1 {
		t.Fatalf("%d <details>, want 1", n)
	}
	if strings.Contains(page, "<details class=\"ts-me__devgroup\" open") {
		t.Error("the fold is open by default")
	}
	forms := formRE.FindAllStringSubmatch(page, -1)
	if len(forms) != 5 {
		t.Fatalf("%d sign-out forms, want 5 (the sixth session is this device)", len(forms))
	}
	if !strings.Contains(page, "5 sign-ins") || !strings.Contains(page, "Sign out everywhere else") {
		t.Error("page lacks the group line or the sweep button")
	}
	if strings.Contains(page, settingsDeviceID(t, member)) {
		t.Error("another person's session appears")
	}

	// A form signs out only its own session.
	first := forms[0][1]
	if !mine[first] {
		t.Fatalf("form for %s is not mine", first)
	}
	rec := settingsPost(t, a, owner, "/me/sessions/"+first+"/signout", nil, false)
	if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/me/devices" {
		t.Fatalf("sign out one = %d %s", rec.Code, rec.Header().Get("Location"))
	}
	after := a.do(http.MethodGet, "/me/devices", nil, nil, owner)
	if after.Code != http.StatusOK {
		t.Fatalf("the current session was ended: %d", after.Code)
	}
	if got := len(formRE.FindAllString(after.Body.String(), -1)); got != 4 {
		t.Errorf("%d forms left, want 4", got)
	}
	if strings.Contains(after.Body.String(), first) {
		t.Error("the signed-out session is still listed")
	}

	// Forbidden: the other person's session id is a 404 and leaves that session valid.
	if rec := settingsPost(t, a, owner, "/me/sessions/"+settingsDeviceID(t, member)+"/signout", nil, false); rec.Code != http.StatusNotFound {
		t.Errorf("other person's id = %d, want 404", rec.Code)
	}
	if rec := a.do(http.MethodGet, "/me", nil, nil, member); rec.Code != http.StatusOK {
		t.Error("the other person was signed out")
	}

	// The member sees none of the owner's sessions.
	mp := a.do(http.MethodGet, "/me/devices", nil, nil, member).Body.String()
	for id := range mine {
		if strings.Contains(mp, id) {
			t.Errorf("member's page shows the owner's session %s", id)
		}
	}
	if strings.Contains(mp, `<details class="ts-me__devgroup"`) {
		t.Error("member's page has a fold")
	}

	// Anonymous is sent to sign in.
	if rec := a.do(http.MethodGet, "/me/devices", nil, nil); rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/login") {
		t.Errorf("anonymous = %d %s", rec.Code, rec.Header().Get("Location"))
	}
}
