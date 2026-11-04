package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

var unsafeMethods = []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

// session does a GET and returns the ts_csrf cookie and its token.
func session(t *testing.T, h http.Handler) (*http.Cookie, string) {
	t.Helper()
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	for _, c := range rec.Result().Cookies() {
		if c.Name == csrfCookie {
			tok := tokenFor(t, h, c)
			return c, tok
		}
	}
	t.Fatal("no ts_csrf cookie on GET /")
	return nil, ""
}

// tokenFor reads the token the page embeds in hx-headers for this cookie.
func tokenFor(t *testing.T, h http.Handler, c *http.Cookie) string {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	body := rec.Body.String()
	const marker = "X-CSRF-Token&#34;:&#34;"
	i := strings.Index(body, marker)
	if i < 0 {
		t.Fatalf("no token marker in page")
	}
	rest := body[i+len(marker):]
	return rest[:strings.Index(rest, "&#34;")]
}

func send(h http.Handler, method, path string, c *http.Cookie, mod func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if c != nil {
		req.AddCookie(c)
	}
	if mod != nil {
		mod(req)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCSRFMissingToken403(t *testing.T) {
	h := csrfApp(t, nil)
	c, _ := session(t, h)
	for _, m := range unsafeMethods {
		for name, cookie := range map[string]*http.Cookie{"with cookie": c, "no cookie": nil} {
			t.Run(m+" "+name, func(t *testing.T) {
				rec := send(h, m, "/", cookie, nil)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403", rec.Code)
				}
				if !strings.Contains(rec.Body.String(), "This form expired.") {
					t.Fatal("403 page has no plain words")
				}
			})
		}
	}
}

func TestCSRFWrongToken403(t *testing.T) {
	h := csrfApp(t, nil)
	c, tok := session(t, h)
	other, otherTok := session(t, h)
	cases := map[string]string{"garbage": "nope", "other session": otherTok, "truncated": tok[:len(tok)-1]}
	_ = other
	for _, m := range unsafeMethods {
		for name, bad := range cases {
			t.Run(m+" "+name, func(t *testing.T) {
				rec := send(h, m, "/", c, func(r *http.Request) { r.Header.Set(csrfHeader, bad) })
				if rec.Code != http.StatusForbidden {
					t.Fatalf("status = %d, want 403", rec.Code)
				}
			})
		}
	}
}

func TestCSRFHeaderOK(t *testing.T) {
	h := csrfApp(t, nil)
	c, tok := session(t, h)
	for _, m := range unsafeMethods {
		t.Run(m, func(t *testing.T) {
			rec := send(h, m, "/", c, func(r *http.Request) { r.Header.Set(csrfHeader, tok) })
			// Past the guard the request reaches the mux; "/" has no unsafe route, so 404, not 403.
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (guard passed)", rec.Code)
			}
		})
	}
}

func TestCSRFFormFieldOK(t *testing.T) {
	h := csrfApp(t, nil)
	c, tok := session(t, h)
	for _, m := range unsafeMethods {
		t.Run(m, func(t *testing.T) {
			form := url.Values{"_csrf": {tok}}.Encode()
			rec := send(h, m, "/", c, func(r *http.Request) {
				r.Body = nopCloser{strings.NewReader(form)}
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			})
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (guard passed)", rec.Code)
			}
			bad := url.Values{"_csrf": {"nope"}}.Encode()
			rec = send(h, m, "/", c, func(r *http.Request) {
				r.Body = nopCloser{strings.NewReader(bad)}
				r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			})
			if rec.Code != http.StatusForbidden {
				t.Fatalf("bad field: status = %d, want 403", rec.Code)
			}
		})
	}
}

func TestCSRFMultipartFieldOK(t *testing.T) {
	h := csrfApp(t, nil)
	c, tok := session(t, h)
	for token, want := range map[string]int{tok: http.StatusNotFound, "nope": http.StatusForbidden} {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		_ = mw.WriteField("_csrf", token)
		_ = mw.Close()
		rec := send(h, http.MethodPost, "/", c, func(r *http.Request) {
			r.Body = nopCloser{strings.NewReader(buf.String())}
			r.Header.Set("Content-Type", mw.FormDataContentType())
		})
		if rec.Code != want {
			t.Errorf("multipart token %q: status %d, want %d", token, rec.Code, want)
		}
	}
}

type nopCloser struct{ *strings.Reader }

func (nopCloser) Close() error { return nil }

func TestCSRFCrossSite403(t *testing.T) {
	h := csrfApp(t, nil)
	c, tok := session(t, h)
	for _, m := range unsafeMethods {
		t.Run(m, func(t *testing.T) {
			rec := send(h, m, "/", c, func(r *http.Request) {
				r.Header.Set(csrfHeader, tok)
				r.Header.Set("Sec-Fetch-Site", "cross-site")
			})
			if rec.Code != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", rec.Code)
			}
		})
	}
}

func TestCSRFCookieMintedOnGet(t *testing.T) {
	rec := send(csrfApp(t, nil), http.MethodGet, "/", nil, nil)
	var got *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == csrfCookie {
			got = c
		}
	}
	if got == nil {
		t.Fatal("no ts_csrf cookie")
	}
	if !got.HttpOnly || got.SameSite != http.SameSiteLaxMode || got.Path != "/" || got.MaxAge != 0 || got.Secure {
		t.Fatalf("cookie attributes: %+v", got)
	}
	if len(got.Value) != 43 {
		t.Fatalf("cookie value length %d, want 43 (32 bytes base64url)", len(got.Value))
	}
	// A malformed cookie is replaced.
	rec = send(csrfApp(t, nil), http.MethodGet, "/", &http.Cookie{Name: csrfCookie, Value: "short"}, nil)
	if !strings.Contains(strings.Join(rec.Header().Values("Set-Cookie"), ";"), csrfCookie+"=") {
		t.Fatal("malformed cookie was not replaced")
	}
	// A valid cookie is kept.
	rec = send(csrfApp(t, nil), http.MethodGet, "/", got, nil)
	if len(rec.Header().Values("Set-Cookie")) != 0 {
		t.Fatal("a valid cookie must not be re-minted")
	}
}

func TestCSRFSecureCookieOnTLS(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	rec := httptest.NewRecorder()
	csrfApp(t, nil).ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == csrfCookie && !c.Secure {
			t.Fatal("cookie over TLS must be Secure")
		}
	}
}

func TestCSRFTokenStable(t *testing.T) {
	h := csrfApp(t, nil)
	c, tok := session(t, h)
	if again := tokenFor(t, h, c); again != tok {
		t.Fatalf("token changed for the same cookie: %q vs %q", tok, again)
	}
	_, tok2 := session(t, h)
	if tok2 == tok {
		t.Fatal("two browsers must not share a token")
	}
	// A fixed key makes tokens survive a restart.
	key := []byte(strings.Repeat("k", 32))
	a := csrfApp(t, key)
	b := csrfApp(t, key)
	if tokenFor(t, a, c) != tokenFor(t, b, c) {
		t.Fatal("same key and cookie must give the same token")
	}
}

func TestCSRFHXRequestGetsAlertToast(t *testing.T) {
	h := csrfApp(t, nil)
	c, _ := session(t, h)
	rec := send(h, http.MethodPost, "/", c, func(r *http.Request) { r.Header.Set("HX-Request", "true") })
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	body := rec.Body.String()
	want := `<div hx-swap-oob="beforeend:#toasts"><div role="alert" class="ts-toast" data-ts-ms="8000">This form expired. Reload the page and try again.</div></div>`
	if strings.TrimSpace(body) != want {
		t.Fatalf("body = %q", body)
	}
}

// csrfApp is the CSRF guard in front of a stub app: GET / shows the token in the same markup the
// layout uses, and any other route is a 404 (so a request that passes the guard answers 404).
// A nil key means a random key; the binding is the default, the ts_csrf cookie.
func csrfApp(t *testing.T, key []byte) http.Handler {
	t.Helper()
	c := NewCSRF(key, nil)
	s := &server{assets: mustAssets(t), csrf: c}
	c.Deny = http.HandlerFunc(s.forbidden)
	return c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<body hx-headers="{&#34;X-CSRF-Token&#34;:&#34;` + c.TokenFrom(r.Context()) + `&#34;}">`))
			return
		}
		http.NotFound(w, r)
	}))
}

// TestCSRFInjectableBinding checks the seam the server uses to bind tokens to a session.
func TestCSRFInjectableBinding(t *testing.T) {
	sess := func(r *http.Request) (string, bool) {
		if v := r.Header.Get("X-Test-Session"); v != "" {
			return "s:" + v, true
		}
		return cookieBinding(r)
	}
	c := NewCSRF([]byte(strings.Repeat("k", 32)), sess)
	h := c.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	tok := c.Token("s:abc")
	post := func(session, token string) int {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		if session != "" {
			req.Header.Set("X-Test-Session", session)
		}
		req.Header.Set(csrfHeader, token)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if got := post("abc", tok); got != http.StatusNoContent {
		t.Fatalf("matching session: %d", got)
	}
	if got := post("abd", tok); got != http.StatusForbidden {
		t.Fatalf("other session: %d", got)
	}
	if c.Token("c:abc") == tok {
		t.Fatal("binding kinds collide")
	}
}

// The ebook upload's large body limit is for signed-in requests only; a visitor gets the form limit.
func TestCSRFTextLimitNeedsSession(t *testing.T) {
	anon := httptest.NewRequest(http.MethodPost, "/books/bk_1/text", nil)
	if got := csrfLimit(anon); got != csrfFormLimit {
		t.Errorf("anonymous limit %d, want the form limit %d", got, csrfFormLimit)
	}
	signed := anon.WithContext(auth.WithSession(anon.Context(), auth.Session{UserID: "u_1"}, users.User{ID: "u_1"}))
	if got := csrfLimit(signed); got != csrfTextLimit {
		t.Errorf("signed-in limit %d, want %d", got, csrfTextLimit)
	}
	if got := csrfMemory(signed); got != csrfTextMemory {
		t.Errorf("signed-in memory %d, want %d", got, csrfTextMemory)
	}
}
