//go:build dev

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// devicesAsCookie serves the app to a browser tab that always sends one session cookie, so two tabs
// stand for two browsers of the same person.
func devicesAsCookie(t *testing.T, a *testApp, ck *http.Cookie) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(&http.Cookie{Name: ck.Name, Value: ck.Value})
		a.h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestDevicesBrowser drives "Your devices" and the Users Devices tab in Chromium at 390 and 1440.
// A second browser signs in and shows on /me; the first signs it out and the second is sent to the
// sign-in page on its next request. An app token is signed out the same way and then answers 401. An
// admin signs a member's app out from Users.
func TestDevicesBrowser(t *testing.T) {
	for _, w := range []int{390, 1440} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			b := chromium(t)
			a := newTestApp(t)
			owner := a.owner(t)
			second := settingsLogin(t, a, settingsChromeLinux)
			ownerID := sweepUserID(t, a, owner)
			tok := deviceBearer(t, a, ownerID)
			secondID := settingsDeviceID(t, second)

			tab := b.NewTab(w, 900, w < 768, false)
			tab.Navigate(devicesAsCookie(t, a, owner) + "/me/devices")
			tab.WaitFor(`document.querySelector(".ts-me__device") !== null`, 10*time.Second)
			var rows []string
			tab.Eval(`[...document.querySelectorAll(".ts-me__device .ts-me__rowtitle")].map(e => e.textContent)`, &rows)
			if len(rows) != 3 || rows[0] != "This device" {
				t.Fatalf("device rows = %v, want this device plus two others", rows)
			}
			var m struct {
				Small []string `json:"small"`
			}
			tab.Eval(`({small: [...document.querySelectorAll(".ts-me__device button, a.ts-me__rename")].filter(e => { const r = e.getBoundingClientRect(); return r.height < 44 || r.width < 44; }).map(e => e.textContent)})`, &m)
			if len(m.Small) > 0 {
				t.Errorf("sign-out buttons under 44 px: %v", m.Small)
			}
			for _, typ := range []string{"keyDown", "keyUp"} {
				if err := tab.Call("Input.dispatchKeyEvent", map[string]any{"type": typ, "key": "Tab", "code": "Tab", "windowsVirtualKeyCode": 9}, nil); err != nil {
					t.Fatal(err)
				}
			}
			var ring bool
			tab.Eval(`document.activeElement !== document.body && getComputedStyle(document.activeElement).boxShadow !== "none"`, &ring)
			if !ring {
				t.Error("no visible focus ring after Tab")
			}

			// The second browser works, then the first signs it out.
			second2 := b.NewTab(w, 900, w < 768, false)
			second2.Navigate(devicesAsCookie(t, a, second) + "/me/devices")
			second2.WaitFor(`document.querySelector(".ts-me__device") !== null`, 10*time.Second)
			playerClickSel(t, tab, `form[action="/me/sessions/`+secondID+`/signout"] button`)
			usersWaitFor(t, "second browser signed out", func() bool {
				return a.do(http.MethodGet, "/me", nil, nil, second).Code != http.StatusOK
			})
			second2.Navigate(devicesAsCookie(t, a, second) + "/me/devices")
			var path string
			second2.Eval(`location.pathname`, &path)
			if !strings.HasPrefix(path, "/login") {
				t.Errorf("signed-out browser lands on %q, want /login", path)
			}

			// Naming this browser: the Name control opens a form, the saved name leads the row.
			tab.WaitFor(`document.querySelector("a.ts-me__rename") !== null`, 10*time.Second)
			playerClickSel(t, tab, `a.ts-me__rename`)
			tab.WaitFor(`document.querySelector(".ts-me__renameform") !== null`, 10*time.Second)
			tab.Eval(`(() => { const f = document.querySelector(".ts-me__renameform"); f.querySelector("input[name=name]").value = "Kitchen laptop"; f.requestSubmit(); })()`, nil)
			tab.WaitFor(`document.querySelector(".ts-me__rowtitle").textContent === "Kitchen laptop"`, 10*time.Second)
			var meta string
			tab.Eval(`document.querySelector(".ts-me__device .caption").textContent`, &meta)
			if !strings.HasPrefix(meta, "This browser") {
				t.Errorf("named current row says %q, want it to start with This browser", meta)
			}

			// The app token goes the same way.
			if rec := deviceBearerReq(a, http.MethodGet, "/api/v1/me", tok, nil); rec.Code != 200 {
				t.Fatalf("token before sign-out = %d", rec.Code)
			}
			tab.WaitFor(`document.querySelector('form[action$="/signout"][action^="/me/devices/"] button') !== null`, 10*time.Second)
			playerClickSel(t, tab, `form[action$="/signout"][action^="/me/devices/"] button`)
			usersWaitFor(t, "app signed out", func() bool {
				return deviceBearerReq(a, http.MethodGet, "/api/v1/me", tok, nil).Code == http.StatusUnauthorized
			})
		})
	}
}

// TestDevicesAdminBrowser: on Users, the Devices tab lists a member's app and the owner signs it out.
func TestDevicesAdminBrowser(t *testing.T) {
	for _, w := range []int{390, 1440} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			b := chromium(t)
			a := newTestApp(t)
			owner := a.owner(t)
			member := a.as(t, users.RoleMember)
			memberID := sweepUserID(t, a, member)
			deviceFor(t, a, memberID, "Kitchen tablet", auth.KindPWA)

			tab := b.NewTab(w, 900, w < 768, false)
			tab.Navigate(devicesAsCookie(t, a, owner) + "/admin/users")
			tab.WaitFor(`document.querySelector("#users-devices .ts-invite__item") !== null`, 10*time.Second)
			var tabText string
			tab.Eval(`[...document.querySelectorAll(".ts-tabs__item")].map(e => e.textContent.trim()).join("|")`, &tabText)
			if !strings.Contains(tabText, "Devices · 3") {
				t.Errorf("tabs = %q, want Devices · 3", tabText)
			}
			playerClickSel(t, tab, `#users-devices form[action*="/devices/dev_"] button`)
			usersWaitFor(t, "member app revoked", func() bool {
				var n int
				_ = a.st.Reader().QueryRow(`SELECT count(*) FROM devices WHERE user_id = ? AND revoked_at IS NULL`, memberID).Scan(&n)
				return n == 0
			})
		})
	}
}
