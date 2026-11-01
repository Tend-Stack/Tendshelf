package web

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
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
	h := NewHandler(Options{})
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
	h := NewHandler(Options{})
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
	h := NewHandler(Options{})
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
	h := NewHandler(Options{})
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
	h := NewHandler(Options{})
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
	h := NewHandler(Options{})
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
	rec := send(NewHandler(Options{}), http.MethodGet, "/", nil, nil)
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
	rec = send(NewHandler(Options{}), http.MethodGet, "/", &http.Cookie{Name: csrfCookie, Value: "short"}, nil)
	if !strings.Contains(strings.Join(rec.Header().Values("Set-Cookie"), ";"), csrfCookie+"=") {
		t.Fatal("malformed cookie was not replaced")
	}
	// A valid cookie is kept.
	rec = send(NewHandler(Options{}), http.MethodGet, "/", got, nil)
	if len(rec.Header().Values("Set-Cookie")) != 0 {
		t.Fatal("a valid cookie must not be re-minted")
	}
}

func TestCSRFSecureCookieOnTLS(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	rec := httptest.NewRecorder()
	NewHandler(Options{}).ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.Name == csrfCookie && !c.Secure {
			t.Fatal("cookie over TLS must be Secure")
		}
	}
}

func TestCSRFTokenStable(t *testing.T) {
	h := NewHandler(Options{})
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
	a := NewHandler(Options{CSRFKey: key})
	b := NewHandler(Options{CSRFKey: key})
	if tokenFor(t, a, c) != tokenFor(t, b, c) {
		t.Fatal("same key and cookie must give the same token")
	}
}

func TestCSRFHXRequestGetsAlertToast(t *testing.T) {
	h := NewHandler(Options{})
	c, _ := session(t, h)
	rec := send(h, http.MethodPost, "/", c, func(r *http.Request) { r.Header.Set("HX-Request", "true") })
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	body := rec.Body.String()
	want := `<div hx-swap-oob="beforeend:#toasts"><div role="alert" class="ts-toast">This form expired. Reload the page and try again.</div></div>`
	if strings.TrimSpace(body) != want {
		t.Fatalf("body = %q", body)
	}
}
