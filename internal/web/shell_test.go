package web

import (
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

func shellCk(v string) *http.Cookie { return &http.Cookie{Name: shellCookie, Value: v} }

func setCookie(rec *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range rec.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// TestShellLinkTable: GET /?shell= marks, clears or ignores; next goes through auth.SafeNext.
func TestShellLinkTable(t *testing.T) {
	a := newTestApp(t)
	rows := []struct {
		name, url    string
		wantCookie   string // "" none; "-" cleared
		wantLocation string
	}{
		{"desktop", "/?shell=desktop", "desktop", "/"},
		{"ios", "/?shell=ios", "ios", "/"},
		{"android", "/?shell=android", "android", "/"},
		{"browser clears", "/?shell=browser", "-", "/"},
		{"next kept", "/?shell=desktop&next=/books", "desktop", "/books"},
		{"next scheme-relative", "/?shell=desktop&next=//evil", "desktop", "/"},
		{"next absolute", "/?shell=desktop&next=https://evil.example/", "desktop", "/"},
		{"next backslash", "/?shell=desktop&next=/%5Cevil", "desktop", "/"},
		{"setup code survives", "/?shell=desktop&next=%2Fsetup%3Fcode%3Dx", "desktop", "/setup?code=x"},
		{"setup code raw", "/?shell=desktop&next=/setup?code=x", "desktop", "/setup?code=x"},
		{"setup subpath refused", "/?shell=desktop&next=/setup/x", "desktop", "/"},
		{"login refused", "/?shell=desktop&next=/login", "desktop", "/"},
		{"unknown value falls through", "/?shell=tv", "", ""},
		{"empty value falls through", "/?shell=", "", ""},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			rec := a.do(http.MethodGet, row.url, nil, nil)
			c := setCookie(rec, shellCookie)
			switch row.wantCookie {
			case "":
				if c != nil || rec.Code == http.StatusSeeOther && strings.Contains(rec.Header().Get("Location"), "shell") {
					t.Fatalf("expected fall through, got %d cookie %v", rec.Code, c)
				}
				return
			case "-":
				if c == nil || c.MaxAge >= 0 || c.Value != "" {
					t.Fatalf("cookie not cleared: %+v", c)
				}
			default:
				if c == nil || c.Value != row.wantCookie {
					t.Fatalf("cookie = %+v, want %s", c, row.wantCookie)
				}
				if !c.HttpOnly || c.Path != "/" || c.SameSite != http.SameSiteLaxMode || c.MaxAge != 365*24*3600 || c.Secure {
					t.Errorf("cookie attributes: %+v", c)
				}
			}
			if rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != row.wantLocation {
				t.Errorf("got %d to %q, want 303 to %q", rec.Code, rec.Header().Get("Location"), row.wantLocation)
			}
		})
	}
	t.Run("only GET on /", func(t *testing.T) {
		if c := setCookie(a.do(http.MethodPost, "/?shell=desktop", nil, nil), shellCookie); c != nil {
			t.Error("POST set the cookie")
		}
		if c := setCookie(a.do(http.MethodGet, "/login?shell=desktop", nil, nil), shellCookie); c != nil {
			t.Error("/login set the cookie")
		}
	})
	t.Run("secure behind https", func(t *testing.T) {
		rec := a.do(http.MethodGet, "/?shell=desktop", nil, func(r *http.Request) { r.TLS = &tls.ConnectionState{} })
		if c := setCookie(rec, shellCookie); c == nil || !c.Secure {
			t.Errorf("cookie over TLS: %+v", c)
		}
	})
}

// TestShellCrossSiteIgnored: a request another site caused never changes the mark.
func TestShellCrossSiteIgnored(t *testing.T) {
	a := newTestApp(t)
	for _, site := range []string{"cross-site", "same-site", "same-origin"} {
		rec := a.do(http.MethodGet, "/?shell=desktop", nil, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", site) })
		if setCookie(rec, shellCookie) != nil {
			t.Errorf("Sec-Fetch-Site %s set the cookie", site)
		}
		rec = a.do(http.MethodGet, "/?shell=browser", nil, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", site) }, shellCk("desktop"))
		if setCookie(rec, shellCookie) != nil {
			t.Errorf("Sec-Fetch-Site %s cleared the cookie", site)
		}
	}
	rec := a.do(http.MethodGet, "/?shell=desktop", nil, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "none") })
	if c := setCookie(rec, shellCookie); c == nil || c.Value != "desktop" {
		t.Error("Sec-Fetch-Site none must be allowed")
	}
}

// TestShellAttrOnAllFrames: Layout, Frame and LinkShell write data-shell; only desktop loads desktop.js.
func TestShellAttrOnAllFrames(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	pagesToTry := []struct {
		name, path string
		ck         []*http.Cookie
	}{
		{"layout", "/", []*http.Cookie{owner}},
		{"frame", "/login", nil},
		{"link shell", "/s/not-a-link", nil},
	}
	for _, p := range pagesToTry {
		for _, shell := range []string{"desktop", "ios", "android"} {
			rec := a.do(http.MethodGet, p.path, nil, nil, append([]*http.Cookie{shellCk(shell)}, p.ck...)...)
			body := rec.Body.String()
			if !strings.Contains(body, `data-shell="`+shell+`"`) {
				t.Errorf("%s as %s: no data-shell", p.name, shell)
			}
			if got := strings.Contains(body, "js/desktop"); got != (shell == "desktop") {
				t.Errorf("%s as %s: desktop.js present = %v", p.name, shell, got)
			}
			if strings.Contains(body, "manifest.webmanifest") || strings.Contains(body, "js/offline") {
				t.Errorf("%s as %s: install link or offline.js present", p.name, shell)
			}
		}
	}
}

// TestShellNoCookieNoMarkup: a browser (no cookie, or an unknown value) sees none of it.
func TestShellNoCookieNoMarkup(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	for _, ck := range [][]*http.Cookie{{owner}, {owner, shellCk("tv")}, {owner, shellCk("")}} {
		body := a.do(http.MethodGet, "/", nil, nil, ck...).Body.String()
		for _, not := range []string{"data-shell", "desktop.js", "pages-desktop"} {
			if strings.Contains(body, not) {
				t.Errorf("browser page contains %q", not)
			}
		}
		if !strings.Contains(body, "manifest.webmanifest") || !strings.Contains(body, "js/offline") {
			t.Error("browser page lost the install link or offline.js")
		}
	}
	body := a.do(http.MethodGet, "/login", nil, nil).Body.String()
	if strings.Contains(body, "data-shell") {
		t.Error("login frame contains data-shell")
	}
}

func TestShellHealthz(t *testing.T) {
	rec := newTestApp(t, func(o *Options) { o.Version = "1.2.3" }).do(http.MethodGet, "/healthz", nil, nil)
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["app"] != "tendshelf" || body["shell"] != "1" || body["status"] != "ok" || body["version"] != "1.2.3" {
		t.Fatalf("body = %v", body)
	}
}

func TestShellDownloadsPage(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	const note = "Downloads are for your phone or browser. This app plays straight from your library or server."
	body := a.do(http.MethodGet, "/me/downloads", nil, nil, owner, shellCk("desktop")).Body.String()
	if !strings.Contains(body, "Downloads</h1>") || !strings.Contains(body, note) {
		t.Error("shell downloads page lacks the title or the sentence")
	}
	for _, not := range []string{"data-ts-storage", "data-ts-empty", "Storage on this phone"} {
		if strings.Contains(body, not) {
			t.Errorf("shell downloads page contains %q", not)
		}
	}
	body = a.do(http.MethodGet, "/me/downloads", nil, nil, owner).Body.String()
	if strings.Contains(body, note) || !strings.Contains(body, "data-ts-storage") {
		t.Error("browser downloads page changed")
	}
}

// TestShellOfflineGuards: the worker is never registered and the download manager never loads
// under data-shell (text checks of both guards in offline.js).
func TestShellOfflineGuards(t *testing.T) {
	src, err := os.ReadFile("static/js/offline.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(src)
	reg := js[strings.Index(js, "async function registerWorker"):]
	reg = reg[:strings.Index(reg, "navigator.serviceWorker.register")]
	if !strings.Contains(reg, `hasAttribute("data-shell")`) || !strings.Contains(reg, "return") {
		t.Error("registerWorker has no data-shell guard before register()")
	}
	need := js[strings.Index(js, "function needsDownloads"):]
	need = need[:strings.Index(need, "querySelector")]
	if !strings.Contains(need, `hasAttribute("data-shell")) return false`) {
		t.Error("needsDownloads does not return false under data-shell")
	}
}

func TestShellCSPUnchanged(t *testing.T) {
	a := newTestApp(t)
	owner := a.as(t, users.RoleOwner)
	plain := a.do(http.MethodGet, "/", nil, nil, owner).Header().Get("Content-Security-Policy")
	marked := a.do(http.MethodGet, "/", nil, nil, owner, shellCk("desktop")).Header().Get("Content-Security-Policy")
	redirect := a.do(http.MethodGet, "/?shell=desktop", nil, nil).Header().Get("Content-Security-Policy")
	if plain == "" || plain != marked || plain != redirect || plain != contentSecurityPolicy {
		t.Errorf("CSP differs: %q / %q / %q", plain, marked, redirect)
	}
	body := a.do(http.MethodGet, "/", nil, nil, owner, shellCk("desktop")).Body.String()
	if strings.Contains(body, " style=\"") || strings.Contains(body, "<script>") {
		t.Error("inline style or script in a shell page")
	}
}
