package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

// The iPhone app is a second phone app (decision 0048, D12): the four app-window rules answer it as they
// answer Android, and no other shell. Names are prefixed TestIOS.

var a1IOS = &http.Cookie{Name: shellCookie, Value: "ios"}

func TestIsAppShell(t *testing.T) {
	for shell, want := range map[string]bool{
		"android": true, "ios": true,
		"desktop": false, "": false, "browser": false, "IOS": false, "Android": false, "ios ": false, "tvos": false,
	} {
		if got := isAppShell(shell); got != want {
			t.Errorf("isAppShell(%q) = %v, want %v", shell, got, want)
		}
	}
}

func TestIOSConnectPage(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	f := a1NewFlow()
	path := "/app/connect?challenge=" + url.QueryEscape(f.challenge)

	page := a.do(http.MethodGet, path, nil, nil, ck, a1IOS)
	body := page.Body.String()
	if page.Code != http.StatusOK || page.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("ios page = %d, Cache-Control %q", page.Code, page.Header().Get("Cache-Control"))
	}
	for _, want := range []string{`name="challenge" value="` + f.challenge + `"`, `action="/app/connect"`, `data-shell="ios"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the ios page lacks %q", want)
		}
	}
	// Each forbidden case: another shell, and no shell at all, is told this is not the app.
	for name, shell := range map[string]*http.Cookie{
		"desktop": {Name: shellCookie, Value: "desktop"},
		"browser": {Name: shellCookie, Value: "browser"},
		"none":    nil,
	} {
		cks := []*http.Cookie{ck}
		if shell != nil {
			cks = append(cks, shell)
		}
		if rec := a.do(http.MethodGet, path, nil, nil, cks...); rec.Code != http.StatusForbidden || strings.Contains(rec.Body.String(), `name="challenge"`) {
			t.Errorf("%s: GET /app/connect = %d", name, rec.Code)
		}
	}
}

func TestIOSConnectSwap(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	f := a1NewFlow()

	page := a.do(http.MethodGet, "/app/connect?challenge="+url.QueryEscape(f.challenge), nil, nil, ck, a1IOS)
	if page.Code != http.StatusOK {
		t.Fatalf("GET /app/connect = %d", page.Code)
	}
	form := url.Values{"_csrf": {pageToken(t, page.Body.String())}, "challenge": {f.challenge}}
	rec := a.do(http.MethodPost, "/app/connect", form, nil, ck, a1IOS)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/app/connect/done?code=") {
		t.Fatalf("POST /app/connect = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	code := a1CodeOf(t, rec)
	done := a.do(http.MethodGet, "/app/connect/done?code="+code, nil, nil, ck, a1IOS)
	if done.Code != http.StatusOK || strings.Contains(done.Body.String(), code) {
		t.Errorf("done = %d, holds the code: %v", done.Code, strings.Contains(done.Body.String(), code))
	}
	if sw := a1Swap(a, a1SwapBody(code, f.verifier, nil), nil); sw.Code != http.StatusCreated {
		t.Fatalf("swap = %d %s", sw.Code, sw.Body)
	}

	// Each forbidden case: another shell cannot make a code, and the done page is the app's alone.
	for name, shell := range map[string]*http.Cookie{"desktop": {Name: shellCookie, Value: "desktop"}, "none": nil} {
		cks := []*http.Cookie{ck}
		if shell != nil {
			cks = append(cks, shell)
		}
		before := a.count(t, `SELECT COUNT(*) FROM device_codes`)
		post := a.do(http.MethodPost, "/app/connect", url.Values{"_csrf": {a.tokenFor(t, ck)}, "challenge": {f.challenge}}, nil, cks...)
		if post.Code != http.StatusForbidden || a.count(t, `SELECT COUNT(*) FROM device_codes`) != before {
			t.Errorf("%s: POST /app/connect = %d", name, post.Code)
		}
		if d := a.do(http.MethodGet, "/app/connect/done", nil, nil, cks...); d.Code != http.StatusForbidden {
			t.Errorf("%s: GET /app/connect/done = %d", name, d.Code)
		}
	}
}

func TestIOSNoWorker(t *testing.T) {
	a := newTestApp(t)
	for name, c := range map[string]*http.Cookie{
		"android": a1Android,
		"ios":     a1IOS,
	} {
		if rec := a.do(http.MethodGet, "/sw.js", nil, nil, c); rec.Code != http.StatusNotFound {
			t.Errorf("%s: /sw.js = %d, want 404", name, rec.Code)
		}
	}
	for name, c := range map[string]*http.Cookie{
		"desktop": {Name: shellCookie, Value: "desktop"},
		"browser": nil,
	} {
		var rec = a.do(http.MethodGet, "/sw.js", nil, nil)
		if c != nil {
			rec = a.do(http.MethodGet, "/sw.js", nil, nil, c)
		}
		if rec.Code != http.StatusOK {
			t.Errorf("%s: /sw.js = %d, want 200", name, rec.Code)
		}
	}
}

func TestIOSPairPost(t *testing.T) {
	a := newTestApp(t)
	owner := a.owner(t)
	for _, tc := range []struct {
		name   string
		cookie *http.Cookie
		app    bool
	}{
		{"desktop", &http.Cookie{Name: shellCookie, Value: "desktop"}, false},
		{"none", nil, false},
		{"ios", a1IOS, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := pairStart(t, a, owner)
			ph := newPhone(t, a, false)
			if tc.cookie != nil {
				ph.jar[shellCookie] = tc.cookie
			}
			rec := ph.post("/login", "/pair/"+s.token, url.Values{"challenge": {s.phoneCh.challenge}, "name": {"x"}})
			notInApp := rec.Code == http.StatusForbidden
			if notInApp == tc.app {
				t.Errorf("POST /pair = %d, not-in-app page: %v, want %v", rec.Code, notInApp, !tc.app)
			}
			if !tc.app && a.count(t, `SELECT COUNT(*) FROM device_pairings WHERE state = 'waiting'`) < 1 {
				t.Error("a claim outside the app spent the pairing")
			}
		})
	}
}

func TestIOSOIDCWrongAddress(t *testing.T) {
	o := newOIDCWeb(t, nil)
	const words = "To sign in with Fake in the app, connect it to http://localhost:8095."
	for _, shell := range []string{"android", "ios"} {
		rec, oc := o.start("/login/oidc", nil, func(r *http.Request) {
			r.Host = "other.example:8443"
			r.AddCookie(&http.Cookie{Name: shellCookie, Value: shell})
		})
		usersWant(t, rec, http.StatusOK, shell+" on another origin")
		if oc != nil || !strings.Contains(rec.Body.String(), words) {
			t.Errorf("%s: cookie %v, body lacks the address words", shell, oc)
		}
	}
	// Each forbidden case: the desktop shell and a browser are sent to the configured address, not told to connect.
	for name, c := range map[string]*http.Cookie{"desktop": {Name: shellCookie, Value: "desktop"}, "browser": nil} {
		rec, oc := o.start("/login/oidc", nil, func(r *http.Request) {
			r.Host = "other.example:8443"
			if c != nil {
				r.AddCookie(c)
			}
		})
		usersWant(t, rec, http.StatusSeeOther, name+" on another origin")
		if oc != nil || strings.Contains(rec.Body.String(), words) {
			t.Errorf("%s: cookie %v or the app words are shown", name, oc)
		}
	}
}
