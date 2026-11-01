package web

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	csrfCookie = "ts_csrf"
	csrfHeader = "X-CSRF-Token"
	csrfField  = "_csrf"
	// csrfFormLimit bounds the form body read while looking for the _csrf field.
	csrfFormLimit = 1 << 20
)

type csrfContextKey struct{}

var csrfBindingKey = csrfContextKey{}

// BindingFunc returns the value a CSRF token is bound to for this request and whether there is one.
// P1b: bind to session ID.
type BindingFunc func(*http.Request) (string, bool)

// CSRF protects unsafe methods with a token (a keyed hash of a per-browser binding) and with the
// standard library's cross-origin check (Sec-Fetch-Site / Origin).
type CSRF struct {
	key     []byte
	binding BindingFunc
	// Deny writes the 403 response. The server replaces it with the HTML error page.
	Deny http.Handler
}

// NewCSRF returns a CSRF guard. A nil key means 32 random bytes, so tokens are valid only for
// this process. P1b passes a key kept in the settings table.
func NewCSRF(key []byte) *CSRF {
	if key == nil {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			panic("web: no randomness for the CSRF key: " + err.Error())
		}
	}
	return &CSRF{
		key:     key,
		binding: cookieBinding,
		Deny: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "This form expired. Reload the page and try again.", http.StatusForbidden)
		}),
	}
}

// Token returns the token for a binding: base64url(HMAC-SHA256(key, "tendshelf-csrf-v1\x00"+binding)).
func (c *CSRF) Token(binding string) string {
	m := hmac.New(sha256.New, c.key)
	m.Write([]byte("tendshelf-csrf-v1\x00" + binding))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// cookieBinding is the default binding: the value of the ts_csrf cookie, if well formed.
func cookieBinding(r *http.Request) (string, bool) {
	ck, err := r.Cookie(csrfCookie)
	if err != nil {
		return "", false
	}
	raw, err := base64.RawURLEncoding.DecodeString(ck.Value)
	if err != nil || len(raw) != 32 {
		return "", false
	}
	return ck.Value, true
}

// TokenFrom returns the token for the request in ctx, or "" outside the middleware.
func (c *CSRF) TokenFrom(ctx context.Context) string {
	b, ok := ctx.Value(csrfBindingKey).(string)
	if !ok {
		return ""
	}
	return c.Token(b)
}

// Middleware wraps next. Every response gets a binding (the ts_csrf cookie is minted when missing
// or malformed); unsafe methods must also carry a matching token, and cross-site browser
// requests are refused first.
func (c *CSRF) Middleware(next http.Handler) http.Handler {
	cop := http.NewCrossOriginProtection()
	cop.SetDenyHandler(c.Deny)
	guarded := cop.Handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !safeMethod(r.Method) && !c.tokenOK(w, r) {
			c.Deny.ServeHTTP(w, r)
			return
		}
		next.ServeHTTP(w, r)
	}))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		binding, ok := c.binding(r)
		if !ok {
			binding = c.mint(w, r)
		}
		guarded.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), csrfBindingKey, binding)))
	})
}

func safeMethod(m string) bool {
	return m == http.MethodGet || m == http.MethodHead || m == http.MethodOptions
}

func (c *CSRF) mint(w http.ResponseWriter, r *http.Request) string {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		panic("web: no randomness for a CSRF cookie: " + err.Error())
	}
	v := base64.RawURLEncoding.EncodeToString(raw)
	http.SetCookie(w, &http.Cookie{
		Name: csrfCookie, Value: v, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: r.TLS != nil,
	})
	return v
}

// tokenOK checks the header, then the form field, against the token for the request's binding. A
// binding that was only just minted can never match, so a request without the cookie is refused.
func (c *CSRF) tokenOK(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := c.binding(r); !ok {
		return false
	}
	want := c.TokenFrom(r.Context())
	got := r.Header.Get(csrfHeader)
	if got == "" {
		ct := r.Header.Get("Content-Type")
		switch {
		case strings.HasPrefix(ct, "application/x-www-form-urlencoded"):
			// Read the body ourselves: ParseForm ignores the body of a DELETE. Put it back for the handler.
			body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, csrfFormLimit))
			if err != nil {
				return false
			}
			r.Body = io.NopCloser(bytes.NewReader(body))
			vals, err := url.ParseQuery(string(body))
			if err != nil {
				return false
			}
			got = vals.Get(csrfField)
		case strings.HasPrefix(ct, "multipart/form-data"):
			r.Body = http.MaxBytesReader(w, r.Body, csrfFormLimit)
			if r.ParseMultipartForm(csrfFormLimit) != nil {
				return false
			}
			if v := r.MultipartForm.Value[csrfField]; len(v) > 0 {
				got = v[0]
			}
		}
	}
	return got != "" && hmac.Equal([]byte(got), []byte(want))
}
