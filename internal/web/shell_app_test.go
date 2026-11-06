package web

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth"
)

// a1Bound signs the owner in on the app's window, swaps a code and returns the window's session cookie
// and the device token: the two halves of one connected phone.
func a1Bound(t *testing.T, a *testApp) (*http.Cookie, deviceIssued) {
	t.Helper()
	ck := a.freshSession(t)
	f := a1NewFlow()
	rec := a1Swap(a, right1(a1CodeOf(t, a1Connect(t, a, f.challenge, ck)), f), nil)
	var dev deviceIssued
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &dev) != nil || dev.Token == "" {
		t.Fatalf("swap = %d %s", rec.Code, rec.Body)
	}
	return ck, dev
}

func a1SessionAlive(a *testApp, ck *http.Cookie) bool {
	return a.do(http.MethodGet, "/me/devices", nil, nil, ck).Code == http.StatusOK
}

func a1TokenAlive(a *testApp, tok string) bool {
	return deviceBearerReq(a, http.MethodGet, "/api/v1/me", tok, nil).Code == http.StatusOK
}

// TestA1EndingTheDeviceEndsTheWindow: every way of ending the phone ends both halves, and the other
// way round where the plan says so.
func TestA1EndingTheDeviceEndsTheWindow(t *testing.T) {
	type end func(t *testing.T, a *testApp, ck *http.Cookie, dev deviceIssued)
	other := func(t *testing.T, a *testApp) *http.Cookie { return a.freshSession(t) } // a browser of the same person
	cases := map[string]end{
		"sign out from the devices page": func(t *testing.T, a *testApp, ck *http.Cookie, dev deviceIssued) {
			b := other(t, a)
			rec := a.do(http.MethodPost, "/me/devices/"+dev.Device.ID+"/signout", url.Values{"_csrf": {a.tokenFor(t, b)}}, nil, b)
			if rec.Code != http.StatusSeeOther {
				t.Fatalf("signout = %d", rec.Code)
			}
		},
		"token signs itself out": func(t *testing.T, a *testApp, ck *http.Cookie, dev deviceIssued) {
			if rec := deviceBearerReq(a, http.MethodDelete, "/api/v1/auth/token", dev.Token, nil); rec.Code != http.StatusNoContent {
				t.Fatalf("DELETE = %d", rec.Code)
			}
		},
		"admin revoke": func(t *testing.T, a *testApp, ck *http.Cookie, dev deviceIssued) {
			// The owner is the person here, so the admin route would refuse "self"; the service call is the same one.
			if ok, err := a.srv.auth.RevokeDevice(context.Background(), sweepUserID(t, a, a.owner(t)), dev.Device.ID, "usr_admin"); err != nil || !ok {
				t.Fatal(ok, err)
			}
		},
		"logout of the window": func(t *testing.T, a *testApp, ck *http.Cookie, dev deviceIssued) {
			if rec := a.do(http.MethodPost, "/logout", url.Values{"_csrf": {a.tokenFor(t, ck)}}, nil, ck); rec.Code != http.StatusSeeOther {
				t.Fatalf("logout = %d", rec.Code)
			}
		},
		"password change": func(t *testing.T, a *testApp, ck *http.Cookie, dev deviceIssued) {
			rec := a.do(http.MethodPost, "/me/password", url.Values{"_csrf": {a.tokenFor(t, ck)}, "current": {testPassword}, "password": {"another long password"}}, nil, ck)
			if rec.Code >= 400 {
				t.Fatalf("password = %d %s", rec.Code, rec.Body)
			}
		},
		"person disabled": func(t *testing.T, a *testApp, ck *http.Cookie, dev deviceIssued) {
			if _, err := a.st.Writer().Exec(`UPDATE users SET disabled = 1`); err != nil {
				t.Fatal(err)
			}
		},
		"device row pruned": func(t *testing.T, a *testApp, ck *http.Cookie, dev deviceIssued) {
			if _, err := a.st.Writer().Exec(`DELETE FROM devices WHERE id = ?`, dev.Device.ID); err != nil {
				t.Fatal(err)
			}
		},
		"sign out of everything else from a browser": func(t *testing.T, a *testApp, ck *http.Cookie, dev deviceIssued) {
			b := other(t, a)
			if rec := a.do(http.MethodPost, "/me/sessions/signout-others", url.Values{"_csrf": {a.tokenFor(t, b)}}, nil, b); rec.Code != http.StatusSeeOther {
				t.Fatalf("signout-others = %d", rec.Code)
			}
		},
	}
	for name, end := range cases {
		t.Run(name, func(t *testing.T) {
			a := newTestApp(t)
			a.owner(t)
			ck, dev := a1Bound(t, a)
			if !a1SessionAlive(a, ck) || !a1TokenAlive(a, dev.Token) {
				t.Fatal("the connected phone does not work before it is ended")
			}
			end(t, a, ck, dev)
			if a1SessionAlive(a, ck) {
				t.Error("the window's session lives on")
			}
			if a1TokenAlive(a, dev.Token) {
				t.Error("the token lives on")
			}
		})
	}
}

func TestA1PhoneKeepsItselfOnSignOutOthers(t *testing.T) {
	a := newTestApp(t)
	a.owner(t)
	ck, dev := a1Bound(t, a)
	b := a.freshSession(t)
	otherTok := deviceBearer(t, a, sweepUserID(t, a, a.owner(t)))
	if rec := a.do(http.MethodPost, "/me/sessions/signout-others", url.Values{"_csrf": {a.tokenFor(t, ck)}}, nil, ck); rec.Code != http.StatusSeeOther {
		t.Fatalf("signout-others = %d", rec.Code)
	}
	if !a1SessionAlive(a, ck) || !a1TokenAlive(a, dev.Token) {
		t.Error("the phone signed itself out")
	}
	if a1SessionAlive(a, b) || a1TokenAlive(a, otherTok) {
		t.Error("another browser or app lived")
	}
}

func TestA1SessionExpiryLeavesTheToken(t *testing.T) {
	a := newTestApp(t)
	a.owner(t)
	ck, dev := a1Bound(t, a)
	if _, err := a.st.Writer().Exec(`UPDATE sessions SET expires_at = 1 WHERE device_id = ?`, dev.Device.ID); err != nil {
		t.Fatal(err)
	}
	if a1SessionAlive(a, ck) {
		t.Error("an expired session worked")
	}
	if !a1TokenAlive(a, dev.Token) {
		t.Error("the token died with the session")
	}
}

// TestA1TokenNeverBecomesASession: with a phone token, every page, /hx/, /admin and /me route of the
// route matrix answers 401, and nothing under /api/v1 sets a session cookie.
func TestA1TokenNeverBecomesASession(t *testing.T) {
	e := sweepSeed(t)
	tok := deviceBearer(t, e.a, e.uid[sweepOwner])
	var pages, apiGets int
	for _, row := range sweepRows() {
		path := row.Path(e, sweepOwner)
		rec := e.a.do(row.Method, path, nil, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) })
		if cookieNamed(rec, auth.CookieName) != nil {
			t.Errorf("%s %s set a session cookie", row.Method, path)
		}
		if strings.HasPrefix(path, "/api/v1/") {
			apiGets++
			continue
		}
		pages++
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "Device tokens work only for /api/v1") {
			t.Errorf("token on %s %s = %d", row.Method, path, rec.Code)
		}
	}
	if pages < 100 || apiGets < 20 {
		t.Errorf("the walk was too short: %d pages, %d api routes", pages, apiGets)
	}
	// Each of the three Connect routes and the code swap: no cookie either.
	rec := a1Swap(e.a, a1SwapBody("abc", a1Verifier, nil), nil)
	if len(rec.Result().Cookies()) != 0 {
		t.Error("the swap answered with a cookie")
	}
}

// TestA1ShellMarkChangesNoStatus: every GET row of the matrix answers the same status with the Android
// mark as without, except the Connect routes; the mark is not a credential.
func TestA1ShellMarkChangesNoStatus(t *testing.T) {
	e := sweepSeed(t)
	checked := 0
	for _, row := range sweepRows() {
		if row.Method != http.MethodGet || strings.HasPrefix(row.Pattern, "GET /app/connect") {
			continue
		}
		for role := -1; role < len(sweepRoles); role++ {
			path := row.Path(e, max(role, 0))
			var cks []*http.Cookie
			if role >= 0 {
				cks = append(cks, e.ck[role])
			}
			plain := e.a.do(http.MethodGet, path, nil, nil, cks...)
			marked := e.a.do(http.MethodGet, path, nil, nil, append(cks, a1Android)...)
			if plain.Code != marked.Code {
				t.Errorf("role %d GET %s: %d without the mark, %d with it", role, path, plain.Code, marked.Code)
			}
			checked++
		}
	}
	if checked < 500 {
		t.Errorf("only %d requests compared", checked)
	}
}

// TestA1ShellPage: the Android window loads native-audio.js and not offline.js, has no Downloads link
// and says data-shell; a browser page has none of that and the same security headers.
func TestA1ShellPage(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	plain := a.do(http.MethodGet, "/", nil, nil, ck)
	app := a.do(http.MethodGet, "/", nil, nil, ck, a1Android)
	pb, ab := plain.Body.String(), app.Body.String()
	for _, want := range []string{`data-shell="android"`, "/js/native-audio."} {
		if !strings.Contains(ab, want) || strings.Contains(pb, want) {
			t.Errorf("%q: android %v, browser %v", want, strings.Contains(ab, want), strings.Contains(pb, want))
		}
	}
	if strings.Contains(ab, "/js/offline.") || !strings.Contains(pb, "/js/offline.") {
		t.Errorf("offline.js: android %v, browser %v", strings.Contains(ab, "/js/offline."), strings.Contains(pb, "/js/offline."))
	}
	if strings.Contains(ab, `href="/me/downloads"`) || !strings.Contains(pb, `href="/me/downloads"`) {
		t.Error("the Downloads link is wrong in one of the pages")
	}
	if strings.Contains(pb, "data-shell") {
		t.Error("a browser page names a shell")
	}
	// A4: the app's one extra word is rendered in the app window only (A1, invariant 8).
	if !strings.Contains(ab, "data-ts-msg-native-background=") || !strings.Contains(ab, "data-ts-msg-native-failed=") || strings.Contains(pb, "native-failed") {
		t.Errorf("native-failed: android %v, browser %v", strings.Contains(ab, "data-ts-msg-native-failed="), strings.Contains(pb, "native-failed"))
	}
	if plain.Header().Get("Content-Security-Policy") == "" || plain.Header().Get("Content-Security-Policy") != app.Header().Get("Content-Security-Policy") {
		t.Errorf("CSP differs: %q vs %q", plain.Header().Get("Content-Security-Policy"), app.Header().Get("Content-Security-Policy"))
	}
	// The mark from a cross-site navigation sets nothing.
	rec := a.do(http.MethodGet, "/?shell=android", nil, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") })
	if cookieNamed(rec, shellCookie) != nil {
		t.Error("a cross-site request set the shell mark")
	}
	if rec := a.do(http.MethodGet, "/?shell=android&next="+url.QueryEscape("/app/connect?challenge="+a1Challenge), nil, nil); cookieNamed(rec, shellCookie) == nil ||
		rec.Header().Get("Location") != "/app/connect?challenge="+a1Challenge {
		t.Errorf("the app's opening link = %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestA1ServiceWorkerAbsentInTheApp(t *testing.T) {
	a := newTestApp(t)
	for mark, want := range map[string]int{"android": http.StatusNotFound, "desktop": http.StatusOK, "ios": http.StatusOK, "": http.StatusOK} {
		var cks []*http.Cookie
		if mark != "" {
			cks = append(cks, &http.Cookie{Name: shellCookie, Value: mark})
		}
		if rec := a.do(http.MethodGet, "/sw.js", nil, nil, cks...); rec.Code != want {
			t.Errorf("/sw.js with mark %q = %d, want %d", mark, rec.Code, want)
		}
	}
}

// TestA1HiddenBooksStayHidden: a member's token on the new routes never shows a book the member may
// not open, by id or through the "ended" book of the queue.
func TestA1HiddenBooksStayHidden(t *testing.T) {
	e := sweepSeed(t)
	tok := deviceBearer(t, e.a, e.uid[sweepMember])
	id := e.book.ID
	post := func(method, path, body string) (int, string) {
		req := e.a.do(method, path, nil, func(r *http.Request) {
			r.Header.Set("Authorization", "Bearer "+tok)
			r.Header.Set("Content-Type", "application/json")
			r.Body = io.NopCloser(strings.NewReader(body))
			r.ContentLength = int64(len(body))
		})
		return req.Code, req.Body.String()
	}
	if code, body := post(http.MethodPut, "/api/v1/books/"+id+"/speed", `{"speed":1.5}`); code != http.StatusNotFound || strings.Contains(body, "Sweepword") {
		t.Errorf("PUT speed on a hidden book = %d", code)
	}
	if code, _ := post(http.MethodGet, "/api/v1/books/"+id, ""); code != http.StatusNotFound {
		t.Errorf("GET book = %d", code)
	}
	code, body := post(http.MethodPost, "/api/v1/queue/next", `{"ended":"`+id+`","series":true}`)
	if code != http.StatusNoContent || body != "" {
		t.Errorf("queue/next with a hidden ended book = %d %q", code, body)
	}
	// A hidden book the member has a place in is not on the Continue shelf.
	res, err := e.a.st.Writer().Exec(`UPDATE progress SET position_ms = 5000, finished_at = NULL WHERE user_id = ? AND book_id = ?`, e.uid[sweepMember], id)
	if n, _ := res.RowsAffected(); err != nil || n == 0 {
		t.Fatalf("the member has no place in the seeded book to move: %v", err)
	}
	code, body = post(http.MethodGet, "/api/v1/continue", "")
	if code != http.StatusOK || strings.Contains(body, "Sweepword") || strings.Contains(body, id) {
		t.Errorf("continue = %d %s", code, body)
	}
}
