package web

import (
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"
)

// TestCSPCastOnlyWhenOn: the base policy is untouched unless the signed-in person turned casting on in a plain
// browser; a shell window gets the base policy whatever the setting (decision 0047).
func TestCSPCastOnlyWhenOn(t *testing.T) {
	l := newCastLab(t)
	page := func(shell string) (csp, body string) {
		rec := l.a.do(http.MethodGet, "/", nil, func(r *http.Request) {
			proxyDefault(r)
			if shell != "" {
				r.AddCookie(&http.Cookie{Name: "ts_shell", Value: shell})
			}
		}, l.owner)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET / = %d", rec.Code)
		}
		return rec.Header().Get("Content-Security-Policy"), rec.Body.String()
	}
	if csp, _ := page(""); csp != contentSecurityPolicy {
		t.Errorf("off: CSP %q", csp)
	}
	l.setCast(t, "owner", true)
	csp, body := page("")
	if csp != contentSecurityPolicyCast {
		t.Errorf("on in a browser: CSP %q", csp)
	}
	if want := strings.Replace(contentSecurityPolicy, "script-src 'self'", "script-src 'self' https://www.gstatic.com", 1); csp != want {
		t.Errorf("cast CSP differs from the base by more than the script source:\n%s\n%s", csp, want)
	}
	if strings.Contains(body, "gstatic") {
		t.Error("a page names gstatic; only cast.js may load it")
	}
	for _, shell := range []string{"desktop", "android", "ios"} {
		if csp, body := page(shell); csp != contentSecurityPolicy || strings.Contains(body, "js/cast.js") || strings.Contains(body, "gstatic") {
			t.Errorf("shell %s: CSP %q", shell, csp)
		}
	}
	// Another person's page, and a signed-out one, keep the base policy.
	rec := l.a.do(http.MethodGet, "/", nil, proxyDefault, l.cookies["m_dl"])
	if rec.Header().Get("Content-Security-Policy") != contentSecurityPolicy {
		t.Error("a person with casting off got the cast policy")
	}
	if rec := l.a.do(http.MethodGet, "/login", nil, proxyDefault); rec.Header().Get("Content-Security-Policy") != contentSecurityPolicy {
		t.Error("the sign-in page got the cast policy")
	}
}

// TestCastScriptOnlyWhenOn: no page names cast.js or gstatic while casting is off or in a shell window.
func TestCastScriptOnlyWhenOn(t *testing.T) {
	l := newCastLab(t)
	for _, shell := range []string{"", "desktop", "android", "ios"} {
		rec := l.a.do(http.MethodGet, "/me/settings/playback", nil, func(r *http.Request) {
			proxyDefault(r)
			if shell != "" {
				r.AddCookie(&http.Cookie{Name: "ts_shell", Value: shell})
			}
		}, l.owner)
		if body := rec.Body.String(); strings.Contains(body, "js/cast.js") || strings.Contains(body, "gstatic") {
			t.Errorf("casting off, shell %q: the page names the cast script", shell)
		}
	}
	l.setCast(t, "owner", true)
	for _, shell := range []string{"desktop", "android", "ios"} {
		rec := l.a.do(http.MethodGet, "/me/settings/playback", nil, func(r *http.Request) {
			proxyDefault(r)
			r.AddCookie(&http.Cookie{Name: "ts_shell", Value: shell})
		}, l.owner)
		if body := rec.Body.String(); strings.Contains(body, "js/cast.js") || strings.Contains(body, "gstatic") {
			t.Errorf("casting on, shell %q: the page names the cast script", shell)
		}
	}
}

func TestServiceWorkerNeverCast(t *testing.T) {
	b, err := os.ReadFile("static/js/sw.js")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/cast/", "/casting/"} {
		if !regexp.MustCompile(`const PASS = \[[^\]]*"` + p + `"`).Match(b) {
			t.Errorf("sw.js PASS does not list %q", p)
		}
	}
}

// TestStreamRefusesCastToken: /stream takes a session, never a cast token, whichever way the token is sent.
func TestStreamRefusesCastToken(t *testing.T) {
	l := newCastLab(t)
	tok, _ := l.mint(t, "owner", "book", l.single.ID)
	f := l.files[l.single.ID][0].ID
	for _, p := range []string{"/stream/" + f, "/stream/" + f + "?compat=1", "/stream/" + tok} {
		for name, mod := range map[string]func(*http.Request){
			"bearer": func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) },
			"query":  func(r *http.Request) { r.URL.RawQuery = "token=" + tok },
			"cookie": func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "ts_session", Value: tok}) },
		} {
			if rec := l.get(p, mod); rec.Code == http.StatusOK || rec.Code == http.StatusPartialContent {
				t.Errorf("%s with the token as %s = %d", p, name, rec.Code)
			}
		}
	}
}
