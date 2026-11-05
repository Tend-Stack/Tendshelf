package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func navPost(t *testing.T, a *testApp, path, mode string, htmx, withToken bool, referer string) (code int, body, loc string) {
	t.Helper()
	ck := a.owner(t)
	f := url.Values{"nav": {mode}}
	if withToken {
		f.Set("_csrf", a.tokenFor(t, ck))
	}
	rec := a.do(http.MethodPost, path, f, func(r *http.Request) {
		if htmx {
			r.Header.Set("HX-Request", "true")
			r.Header.Set("HX-Current-URL", "http://example.com/libraries")
		}
		if referer != "" {
			r.Header.Set("Referer", referer)
		}
	}, ck)
	return rec.Code, rec.Body.String(), rec.Header().Get("Location")
}

func TestNavToggleSavesAndRenders(t *testing.T) {
	a := browseOwnerApp(t)
	if body := browseGet(a, "/", false, nil).Body.String(); !strings.Contains(body, `data-nav="full"`) ||
		!strings.Contains(body, `aria-label="Collapse menu"`) || !strings.Contains(body, `aria-pressed="false"`) {
		t.Fatal("default sidebar is not full with a Collapse menu toggle")
	}

	// Plain form: saved, redirected back to the same-origin referrer.
	code, _, loc := navPost(t, a, "/me/nav", "rail", false, true, "http://example.com/libraries?x=1")
	if code != http.StatusSeeOther || loc != "/libraries?x=1" {
		t.Fatalf("plain save = %d %q", code, loc)
	}
	if got := settingsOf(t, a).Nav; got != "rail" {
		t.Fatalf("stored nav %q", got)
	}
	body := browseGet(a, "/", false, nil).Body.String()
	if !strings.Contains(body, `id="nav-side"`) || !strings.Contains(body, `data-nav="rail"`) ||
		!strings.Contains(body, `aria-label="Expand menu"`) || !strings.Contains(body, `aria-pressed="true"`) || strings.Contains(body, "Your shelves") {
		t.Fatal("rail page missing data-nav, Expand menu toggle or pressed state")
	}

	// htmx twin: the sidebar alone, without hx-preserve, the current page marked.
	code, frag, _ := navPost(t, a, "/hx/me/nav", "full", true, true, "")
	if code != http.StatusOK || !strings.HasPrefix(strings.TrimSpace(frag), "<nav") || strings.Contains(frag, "hx-preserve") ||
		!strings.Contains(frag, `data-nav="full"`) || strings.Contains(frag, "<main") {
		t.Fatalf("htmx answer = %d %.200s", code, frag)
	}
	if got := settingsOf(t, a).Nav; got != "full" {
		t.Fatalf("stored nav %q after htmx", got)
	}
	// Full pages keep hx-preserve.
	if body := browseGet(a, "/", false, nil).Body.String(); !strings.Contains(body, "hx-preserve") {
		t.Error("full page lost hx-preserve on the sidebar")
	}
}

func TestNavToggleRefusals(t *testing.T) {
	a := browseOwnerApp(t)
	for _, path := range []string{"/me/nav", "/hx/me/nav"} {
		htmx := strings.HasPrefix(path, "/hx")
		if code, _, _ := navPost(t, a, path, "sideways", htmx, true, ""); code != http.StatusBadRequest {
			t.Errorf("%s bad value = %d, want 400", path, code)
		}
		if code, _, _ := navPost(t, a, path, "rail", htmx, false, ""); code != http.StatusForbidden {
			t.Errorf("%s without token = %d, want 403", path, code)
		}
	}
	if got := settingsOf(t, a).Nav; got != "full" {
		t.Fatalf("refused posts changed the setting to %q", got)
	}
}

func TestNavRedirectStaysOnSite(t *testing.T) {
	for _, tc := range []struct{ referer, want string }{
		{"", "/"},
		{"//evil.example", "/"},
		{"//evil.example/x", "/"},
		{"https://evil.example/path", "/"},
		{"https://evil.example", "/"},
		{`/\evil.example`, "/"},
		{"/me/settings", "/me/settings"},
		{"http://example.com/shelves?a=b", "/shelves?a=b"},
	} {
		if got := sameOriginPath(tc.referer, "example.com"); got != tc.want {
			t.Errorf("sameOriginPath(%q) = %q, want %q", tc.referer, got, tc.want)
		}
	}
}

func TestNavKeyFor(t *testing.T) {
	for path, want := range map[string]string{
		"/": "home", "/libraries/lib_1": "library", "/me": "profile", "/me/settings": "profile", "/me/devices": "profile", "/me/downloads": "downloads",
		"/shelves": "shelves", "/shelves/sh_1": "shelf-sh_1", "/admin/users": "admin-users", "/admin/libraries": "admin-libraries", "/books/x": "",
	} {
		if got := navKeyFor(path); got != want {
			t.Errorf("navKeyFor(%q) = %q, want %q", path, got, want)
		}
	}
}
