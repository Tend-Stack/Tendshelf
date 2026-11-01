package web

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/db"
)

func (a *testApp) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := a.st.Reader().QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func location(rec *httptest.ResponseRecorder) string { return rec.Header().Get("Location") }

func hx(r *http.Request) { r.Header.Set("HX-Request", "true") }

func TestGateNoOwner(t *testing.T) {
	a := newTestApp(t)
	for _, p := range []string{"/", "/libraries/x", "/nope", "/setup/server"} {
		rec := a.do(http.MethodGet, p, nil, nil)
		if rec.Code != http.StatusSeeOther || location(rec) != "/setup" {
			t.Errorf("GET %s: %d -> %q, want 303 /setup", p, rec.Code, location(rec))
		}
	}
	rec := a.do(http.MethodGet, "/libraries", nil, hx)
	if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Redirect") != "/setup" {
		t.Errorf("htmx: %d HX-Redirect %q", rec.Code, rec.Header().Get("HX-Redirect"))
	}
	if rec := a.do(http.MethodGet, "/login", nil, nil); rec.Code != http.StatusSeeOther || location(rec) != "/setup" {
		t.Errorf("/login without an owner: %d -> %q", rec.Code, location(rec))
	}
	if rec := a.do(http.MethodGet, "/healthz", nil, nil); rec.Code != http.StatusOK {
		t.Errorf("/healthz: %d", rec.Code)
	}
	if rec := a.do(http.MethodGet, "/setup", nil, nil); rec.Code != http.StatusOK {
		t.Errorf("/setup: %d", rec.Code)
	}
}

func TestGateAnonymous(t *testing.T) {
	a := newTestApp(t)
	a.owner(t)
	cases := []struct {
		path, want string
	}{
		{"/", "/login"},
		{"/libraries/x?y=1", "/login?next=%2Flibraries%2Fx%3Fy%3D1"},
		{"/no/such/page", "/login?next=%2Fno%2Fsuch%2Fpage"},
		{"/setup/server", "/login"}, // a setup path is never a next target
		{"/hx/jobs/jb_1", "/login?next=%2Fhx%2Fjobs%2Fjb_1"},
	}
	for _, c := range cases {
		rec := a.do(http.MethodGet, c.path, nil, nil)
		if rec.Code != http.StatusSeeOther || location(rec) != c.want {
			t.Errorf("GET %s: %d -> %q, want 303 %s", c.path, rec.Code, location(rec), c.want)
		}
		if rec.Header().Get("Content-Security-Policy") == "" {
			t.Errorf("GET %s: no security headers on a redirect", c.path)
		}
	}
	if rec := a.do(http.MethodHead, "/libraries", nil, nil); rec.Code != http.StatusSeeOther || location(rec) != "/login?next=%2Flibraries" {
		t.Errorf("HEAD: %d -> %q", rec.Code, location(rec))
	}

	// htmx: 204 with HX-Redirect, next from the page the browser is on.
	rec := a.do(http.MethodGet, "/hx/x", nil, func(r *http.Request) {
		hx(r)
		r.Header.Set("HX-Current-URL", "http://example.com/books/bk_1?tab=chapters")
	})
	if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Redirect") != "/login?next=%2Fbooks%2Fbk_1%3Ftab%3Dchapters" {
		t.Errorf("htmx: %d HX-Redirect %q", rec.Code, rec.Header().Get("HX-Redirect"))
	}
	if rec.Body.Len() != 0 {
		t.Error("a 204 must have no body")
	}
	for name, cur := range map[string]string{"other host": "http://evil.example/steal", "none": "", "garbage": "::", "login page": "http://example.com/login"} {
		rec := a.do(http.MethodGet, "/hx/x", nil, func(r *http.Request) {
			hx(r)
			if cur != "" {
				r.Header.Set("HX-Current-URL", cur)
			}
		})
		if rec.Code != http.StatusNoContent || rec.Header().Get("HX-Redirect") != "/login" {
			t.Errorf("htmx %s: %d HX-Redirect %q", name, rec.Code, rec.Header().Get("HX-Redirect"))
		}
	}

	// Open paths.
	assets := mustAssets(t)
	for _, p := range []string{"/healthz", assets.URL("css/tokens.css"), "/login", "/setup"} {
		code := a.do(http.MethodGet, p, nil, nil).Code
		want := http.StatusOK
		if p == "/setup" {
			want = http.StatusNotFound // owner exists
		}
		if code != want {
			t.Errorf("GET %s: %d, want %d", p, code, want)
		}
	}
	// A browser without a token is refused before the gate says anything.
	if rec := a.do(http.MethodPost, "/libraries", url.Values{}, nil); rec.Code != http.StatusForbidden {
		t.Errorf("anonymous POST without a token: %d", rec.Code)
	}
	// With a cookie-bound token it is redirected to sign in; it never reaches a handler.
	c, tok := a.anon(t, "/login")
	rec = a.do(http.MethodPost, "/libraries", url.Values{"_csrf": {tok}}, nil, c)
	if rec.Code != http.StatusSeeOther || location(rec) != "/login" {
		t.Errorf("anonymous POST with a token: %d -> %q", rec.Code, location(rec))
	}
}

func TestSetupPage(t *testing.T) {
	a := newTestApp(t)
	rec := a.do(http.MethodGet, "/setup?code="+testSetupCode, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	body := html.UnescapeString(rec.Body.String())
	for _, w := range []string{
		"Create your account", "You're the owner. You decide who listens, and to what.", "Step 1 of 3",
		"Setup code", "It's in the server's log, next to the setup link.", "Your name", "Email", "(optional, for invites)",
		"Username", "You'll use this to sign in.", "Password", "At least 10 characters.", "Create account",
		"Tendshelf sends nothing about you anywhere. No telemetry, no tracking, no ads.",
		`action="/setup"`, `method="post"`, `value="` + testSetupCode + `"`, `name="_csrf"`, `role="progressbar"`,
		`type="password"`, `autocomplete="new-password"`, `ts-steps__seg is-done`,
	} {
		if !strings.Contains(body, w) {
			t.Errorf("missing %q", w)
		}
	}
	for _, bad := range []string{"hx-boost", "hx-headers", "ts-shell", `id="nav-`} {
		if strings.Contains(body, bad) {
			t.Errorf("the setup page must be a plain frame, found %q", bad)
		}
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Error("setup page must not be cached")
	}
	if cookieNamed(rec, csrfCookie) == nil {
		t.Error("no ts_csrf cookie for the form")
	}
	// A long or odd code in the link is not echoed.
	rec = a.do(http.MethodGet, "/setup?code="+strings.Repeat("A", 40), nil, nil)
	if strings.Contains(rec.Body.String(), strings.Repeat("A", 40)) {
		t.Error("an over-long code was echoed")
	}
	rec = a.do(http.MethodGet, `/setup?code="><script>x</script>`, nil, nil)
	if strings.Contains(rec.Body.String(), "<script>x") {
		t.Error("code not escaped")
	}

	// Once there is an owner, /setup is a 404 for everyone.
	own := a.owner(t)
	if rec := a.do(http.MethodGet, "/setup", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("anonymous: %d", rec.Code)
	}
	if rec := a.do(http.MethodGet, "/setup", nil, nil, own); rec.Code != http.StatusNotFound {
		t.Errorf("signed in: %d", rec.Code)
	}
}

func TestSetupCreatesOwner(t *testing.T) {
	a := newTestApp(t)
	c, tok := a.anon(t, "/setup")
	rec := a.do(http.MethodPost, "/setup", setupForm(tok), nil, c)
	if rec.Code != http.StatusSeeOther || location(rec) != setupNextURL {
		t.Fatalf("status %d -> %q", rec.Code, location(rec))
	}
	ck := cookieNamed(rec, "ts_session")
	if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.Path != "/" || ck.MaxAge != 2592000 || ck.Secure {
		t.Fatalf("session cookie: %+v", ck)
	}
	var role, email, hash, display string
	if err := a.st.Reader().QueryRow(`SELECT role, email, password_hash, display_name FROM users WHERE username = 'ana'`).Scan(&role, &email, &hash, &display); err != nil {
		t.Fatal(err)
	}
	if role != "owner" || email != "ana@example.org" || display != "Ana Reyes" || !strings.HasPrefix(hash, "$argon2id$v=19$") || strings.Contains(hash, testPassword) {
		t.Fatalf("user row: %s %s %s %s", role, email, display, hash)
	}
	// The raw cookie value is not in the table; its hash is.
	raw, err := base64.RawURLEncoding.DecodeString(ck.Value)
	if err != nil || len(raw) != 32 {
		t.Fatalf("cookie value: %v %d", err, len(raw))
	}
	if a.count(t, `SELECT COUNT(*) FROM sessions WHERE token_hash = ?`, raw) != 0 {
		t.Fatal("the raw token is stored")
	}
	if a.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'setup.owner_created'`) != 1 {
		t.Fatal("no audit row")
	}
	if rec := a.do(http.MethodGet, "/", nil, nil, ck); rec.Code != http.StatusOK {
		t.Fatalf("GET / after setup: %d", rec.Code)
	}
	// The setup code is accepted with different case and spacing.
	b := newTestApp(t)
	c2, tok2 := b.anon(t, "/setup")
	f := setupForm(tok2)
	f.Set("code", " abcde fghjk ")
	if rec := b.do(http.MethodPost, "/setup", f, nil, c2); rec.Code != http.StatusSeeOther {
		t.Fatalf("lenient code: %d", rec.Code)
	}
}

func TestSetupForbidden(t *testing.T) {
	a := newTestApp(t)
	c, tok := a.anon(t, "/setup")
	secret := "SuperSecretPassw0rd!"
	post := func(mod func(url.Values), req func(*http.Request), cookies ...*http.Cookie) *httptest.ResponseRecorder {
		f := setupForm(tok)
		f.Set("password", secret)
		if mod != nil {
			mod(f)
		}
		return a.do(http.MethodPost, "/setup", f, req, cookies...)
	}
	// 403: no token, wrong token, no cookie, cross-site.
	if rec := post(func(f url.Values) { f.Del("_csrf") }, nil, c); rec.Code != http.StatusForbidden {
		t.Errorf("no token: %d", rec.Code)
	}
	if rec := post(func(f url.Values) { f.Set("_csrf", "nope") }, nil, c); rec.Code != http.StatusForbidden {
		t.Errorf("wrong token: %d", rec.Code)
	}
	if rec := post(nil, nil); rec.Code != http.StatusForbidden {
		t.Errorf("no cookie: %d", rec.Code)
	}
	if rec := post(nil, func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }, c); rec.Code != http.StatusForbidden {
		t.Errorf("cross-site: %d", rec.Code)
	}
	if a.count(t, `SELECT COUNT(*) FROM users`) != 0 {
		t.Fatal("a refused request created a user")
	}
	// 422: wrong code, each validation problem; the password is never echoed.
	cases := map[string]struct {
		mod  func(url.Values)
		want string
	}{
		"wrong code":     {func(f url.Values) { f.Set("code", "ZZZZZ-ZZZZZ") }, "That setup code doesn't match. Check the server's log."},
		"empty code":     {func(f url.Values) { f.Del("code") }, "That setup code doesn't match."},
		"no name":        {func(f url.Values) { f.Set("display_name", " ") }, "Tell us your name."},
		"bad username":   {func(f url.Values) { f.Set("username", "a b") }, "Use 3 to 32 letters, numbers, dots, dashes or underscores."},
		"bad email":      {func(f url.Values) { f.Set("email", "not-an-email") }, "Check the email address."},
		"short password": {func(f url.Values) { f.Set("password", "short") }, "Use at least 10 characters."},
		"same password":  {func(f url.Values) { f.Set("username", "anaanaanaana"); f.Set("password", "anaanaanaana") }, "Choose a password that is different from your username."},
	}
	for name, tc := range cases {
		rec := post(tc.mod, nil, c)
		body := html.UnescapeString(rec.Body.String())
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(body, tc.want) {
			t.Errorf("%s: %d, missing %q", name, rec.Code, tc.want)
		}
		if strings.Contains(body, secret) || strings.Contains(body, `type="password" value`) {
			t.Errorf("%s: the password is echoed", name)
		}
		if !strings.Contains(body, `aria-invalid="true"`) {
			t.Errorf("%s: no aria-invalid", name)
		}
	}
	if a.count(t, `SELECT COUNT(*) FROM users`) != 0 {
		t.Fatal("a refused request created a user")
	}
	// A rejected form keeps what was typed (not the password).
	rec := post(func(f url.Values) { f.Set("email", "bad") }, nil, c)
	body := rec.Body.String()
	if !strings.Contains(body, `value="Ana Reyes"`) || !strings.Contains(body, `value="ana"`) {
		t.Error("typed values were lost")
	}
	// 429 after 20 wrong codes from one address, with Retry-After.
	for i := 0; i < 20; i++ {
		post(func(f url.Values) { f.Set("code", "ZZZZZ-ZZZZZ") }, nil, c)
	}
	rec = post(nil, nil, c)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || !strings.Contains(rec.Body.String(), "Too many tries. Wait") {
		t.Errorf("limited: %d Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if a.count(t, `SELECT COUNT(*) FROM users`) != 0 {
		t.Fatal("a limited request created a user")
	}

	// 409 once there is an owner (a fresh app, since this address is limited).
	b := newTestApp(t)
	b.owner(t)
	c2, tok2 := b.anon(t, "/login")
	rec = b.do(http.MethodPost, "/setup", setupForm(tok2), nil, c2)
	if rec.Code != http.StatusConflict || !strings.Contains(html.UnescapeString(rec.Body.String()), "This server already has an owner") {
		t.Errorf("second setup: %d", rec.Code)
	}
	if b.count(t, `SELECT COUNT(*) FROM users`) != 1 {
		t.Error("a second user exists")
	}
}

func TestSetupConcurrent(t *testing.T) {
	a := newTestApp(t)
	const n = 10
	type ticket struct {
		c   *http.Cookie
		tok string
	}
	tickets := make([]ticket, n)
	for i := range tickets {
		c, tok := a.anon(t, "/setup")
		tickets[i] = ticket{c, tok}
	}
	codes := make([]int, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f := setupForm(tickets[i].tok)
			f.Set("username", fmt.Sprintf("owner%d", i))
			f.Set("email", "")
			rec := a.do(http.MethodPost, "/setup", f, nil, tickets[i].c)
			codes[i] = rec.Code
		}()
	}
	wg.Wait()
	var ok, taken int
	for _, c := range codes {
		switch c {
		case http.StatusSeeOther:
			ok++
		case http.StatusConflict:
			taken++
		default:
			t.Errorf("unexpected status %d", c)
		}
	}
	if ok != 1 || taken != n-1 {
		t.Fatalf("303s=%d 409s=%d (%v)", ok, taken, codes)
	}
	if a.count(t, `SELECT COUNT(*) FROM users WHERE role = 'owner'`) != 1 || a.count(t, `SELECT COUNT(*) FROM users`) != 1 {
		t.Fatal("not exactly one owner")
	}
	if a.count(t, `SELECT COUNT(*) FROM sessions`) != 1 {
		t.Fatal("losing requests left sessions behind")
	}
}

func loginForm(tok, user, pw string) url.Values {
	return url.Values{"_csrf": {tok}, "username": {user}, "password": {pw}}
}

func TestLoginFlow(t *testing.T) {
	a := newTestApp(t)
	a.owner(t)
	c, tok := a.anon(t, "/login")
	page := a.do(http.MethodGet, "/login", nil, nil, c)
	body := html.UnescapeString(page.Body.String())
	for _, w := range []string{"Sign in", "Welcome back.", "Username", "Password", `autocomplete="current-password"`, `action="/login"`} {
		if !strings.Contains(body, w) {
			t.Errorf("login page lacks %q", w)
		}
	}

	// Every failure looks the same, and the typed password is never shown.
	bodies := map[string]string{}
	for name, cr := range map[string][2]string{"wrong password": {"ana", "wrong password!"}, "unknown user": {"ghost", "wrong password!"}} {
		rec := a.do(http.MethodPost, "/login", loginForm(tok, cr[0], cr[1]), nil, c)
		b := html.UnescapeString(rec.Body.String())
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(b, "That username and password don't match. Try again.") {
			t.Errorf("%s: %d", name, rec.Code)
		}
		if strings.Contains(b, "wrong password!") {
			t.Errorf("%s: password echoed", name)
		}
		if cookieNamed(rec, "ts_session") != nil {
			t.Errorf("%s: a session cookie was set", name)
		}
		bodies[name] = strings.ReplaceAll(strings.ReplaceAll(b, cr[0], "U"), tok, "T")
	}
	if bodies["wrong password"] != bodies["unknown user"] {
		t.Error("the two failures render differently")
	}

	rec := a.do(http.MethodPost, "/login", loginForm(tok, " ANA ", testPassword), nil, c)
	if rec.Code != http.StatusSeeOther || location(rec) != "/" {
		t.Fatalf("login: %d -> %q", rec.Code, location(rec))
	}
	ck := cookieNamed(rec, "ts_session")
	if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.MaxAge != 2592000 {
		t.Fatalf("cookie %+v", ck)
	}
	if a.do(http.MethodGet, "/", nil, nil, ck).Code != http.StatusOK {
		t.Fatal("the new session does not open /")
	}
	// next is followed.
	rec = a.do(http.MethodPost, "/login", func() url.Values {
		f := loginForm(tok, "ana", testPassword)
		f.Set("next", "/libraries/x?y=1")
		return f
	}(), nil, c)
	if location(rec) != "/libraries/x?y=1" {
		t.Errorf("next: %q", location(rec))
	}
	// The page carries next through the form.
	page = a.do(http.MethodGet, "/login?next=%2Fbooks%2Fbk_1", nil, nil, c)
	if !strings.Contains(page.Body.String(), `name="next" value="/books/bk_1"`) {
		t.Error("next is not carried in the form")
	}
	// Without the token it is refused.
	if rec := a.do(http.MethodPost, "/login", url.Values{"username": {"ana"}, "password": {testPassword}}, nil, c); rec.Code != http.StatusForbidden {
		t.Errorf("no token: %d", rec.Code)
	}
}

func TestLoginRateLimited(t *testing.T) {
	a := newTestApp(t)
	a.owner(t)
	c, tok := a.anon(t, "/login")
	for i := 0; i < 20; i++ {
		a.do(http.MethodPost, "/login", loginForm(tok, fmt.Sprintf("ghost%d", i), "x"), nil, c)
	}
	rec := a.do(http.MethodPost, "/login", loginForm(tok, "ana", testPassword), nil, c)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || !strings.Contains(rec.Body.String(), "Too many tries. Wait 15 minutes, then try again.") {
		t.Fatalf("%d Retry-After %q", rec.Code, rec.Header().Get("Retry-After"))
	}
	if cookieNamed(rec, "ts_session") != nil {
		t.Fatal("a limited login set a session")
	}
}

func TestLoginOpenRedirect(t *testing.T) {
	a := newTestApp(t)
	a.owner(t)
	c, tok := a.anon(t, "/login")
	for _, next := range []string{"//evil.com", `/\evil.com`, "https://evil.com", "http:/x", "javascript:alert(1)", "/login", "/logout", "/setup/server", "/a%0d%0aSet-Cookie:x", "", "evil.com"} {
		f := loginForm(tok, "ana", testPassword)
		f.Set("next", next)
		rec := a.do(http.MethodPost, "/login", f, nil, c)
		if rec.Code != http.StatusSeeOther || location(rec) != "/" {
			t.Errorf("next=%q: %d -> %q, want /", next, rec.Code, location(rec))
		}
	}
	own := a.owner(t)
	for _, next := range []string{"//evil.com", "https://evil.com", `/\evil.com`} {
		rec := a.do(http.MethodGet, "/login?next="+url.QueryEscape(next), nil, nil, own)
		if rec.Code != http.StatusSeeOther || location(rec) != "/" {
			t.Errorf("GET next=%q: %d -> %q", next, rec.Code, location(rec))
		}
	}
}

func TestLoginWhenSignedIn(t *testing.T) {
	a := newTestApp(t)
	own := a.owner(t)
	if rec := a.do(http.MethodGet, "/login", nil, nil, own); rec.Code != http.StatusSeeOther || location(rec) != "/" {
		t.Errorf("plain: %d -> %q", rec.Code, location(rec))
	}
	if rec := a.do(http.MethodGet, "/login?next=%2Fbooks%2Fbk_1", nil, nil, own); location(rec) != "/books/bk_1" {
		t.Errorf("next: %q", location(rec))
	}
}

func TestLogout(t *testing.T) {
	a := newTestApp(t)
	own := a.owner(t)
	tok := a.tokenFor(t, own)
	if rec := a.do(http.MethodPost, "/logout", url.Values{}, nil, own); rec.Code != http.StatusForbidden {
		t.Errorf("no token: %d", rec.Code)
	}
	if rec := a.do(http.MethodGet, "/logout", nil, nil, own); rec.Code != http.StatusNotFound {
		t.Errorf("GET /logout signed in: %d", rec.Code)
	}
	rec := a.do(http.MethodPost, "/logout", url.Values{"_csrf": {tok}}, nil, own)
	if rec.Code != http.StatusSeeOther || location(rec) != "/login" {
		t.Fatalf("logout: %d -> %q", rec.Code, location(rec))
	}
	gone := cookieNamed(rec, "ts_session")
	if gone == nil || gone.MaxAge != -1 {
		t.Fatalf("cookie not cleared: %+v", gone)
	}
	if a.count(t, `SELECT COUNT(*) FROM sessions`) != 0 {
		t.Fatal("session row not deleted")
	}
	if a.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'auth.logout'`) != 1 {
		t.Fatal("no logout audit row")
	}
	// The old cookie no longer opens anything.
	if rec := a.do(http.MethodGet, "/", nil, nil, own); rec.Code != http.StatusSeeOther {
		t.Errorf("old cookie: %d", rec.Code)
	}
	// Anonymous with a cookie-bound token: the gate sends them to sign in.
	c, ctok := a.anon(t, "/login")
	rec = a.do(http.MethodPost, "/logout", url.Values{"_csrf": {ctok}}, nil, c)
	if rec.Code != http.StatusSeeOther || location(rec) != "/login" {
		t.Errorf("anonymous logout: %d -> %q", rec.Code, location(rec))
	}
}

func TestCSRFBoundToSession(t *testing.T) {
	a := newTestApp(t)
	own := a.owner(t)
	tokA := a.tokenFor(t, own)
	// A second session for the same person.
	c, ctok := a.anon(t, "/login")
	rec := a.do(http.MethodPost, "/login", loginForm(ctok, "ana", testPassword), nil, c)
	sessB := cookieNamed(rec, "ts_session")
	if sessB == nil {
		t.Fatalf("second login: %d", rec.Code)
	}
	tokB := a.tokenFor(t, sessB)
	if tokA == tokB {
		t.Fatal("two sessions share a token")
	}
	// POST / passes the guard and reaches the 404 catch-all; anything else is a 403.
	probe := func(token string, cookies ...*http.Cookie) int {
		return a.do(http.MethodPost, "/", url.Values{"_csrf": {token}}, nil, cookies...).Code
	}
	if got := probe(tokA, own); got != http.StatusNotFound {
		t.Errorf("own token: %d, want 404 (guard passed)", got)
	}
	if got := probe(tokB, sessB); got != http.StatusNotFound {
		t.Errorf("session B: %d", got)
	}
	if got := probe(tokB, own); got != http.StatusForbidden {
		t.Errorf("another session's token: %d, want 403", got)
	}
	// A cookie-bound token is no good while signed in, and the other way round.
	if got := probe(ctok, own); got != http.StatusForbidden {
		t.Errorf("cookie-bound token on a session: %d, want 403", got)
	}
	if got := probe(tokA, c); got != http.StatusForbidden {
		t.Errorf("session token while anonymous: %d, want 403", got)
	}
	// The signed-in app shell never sets a ts_csrf cookie.
	if rec := a.do(http.MethodGet, "/", nil, nil, own); cookieNamed(rec, csrfCookie) != nil {
		t.Error("a signed-in page minted a ts_csrf cookie")
	}
	// htmx requests refused for a bad token get the toast, not a page.
	rec = a.do(http.MethodPost, "/", url.Values{}, func(r *http.Request) { hx(r); r.Header.Set(csrfHeader, tokB) }, own)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), `hx-swap-oob="beforeend:#toasts"`) {
		t.Errorf("htmx refusal: %d %q", rec.Code, rec.Body.String())
	}
}

func TestSessionCookieSecure(t *testing.T) {
	px, err := auth.ParseProxies("10.0.0.0/8")
	if err != nil {
		t.Fatal(err)
	}
	a := newTestApp(t, func(o *Options) { o.TrustedProxies = px })
	viaProxy := func(r *http.Request) {
		r.RemoteAddr = "10.1.1.1:5000"
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-For", "198.51.100.4")
	}
	rec := a.do(http.MethodGet, "/setup", nil, viaProxy)
	c := cookieNamed(rec, csrfCookie)
	if c == nil || !c.Secure {
		t.Fatalf("ts_csrf behind a trusted https proxy: %+v", c)
	}
	// A client that is not a trusted proxy can not claim https.
	spoof := a.do(http.MethodGet, "/setup", nil, func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") })
	if c := cookieNamed(spoof, csrfCookie); c == nil || c.Secure {
		t.Fatalf("spoofed header: %+v", c)
	}
	tok := pageToken(t, rec.Body.String())
	rec = a.do(http.MethodPost, "/setup", setupForm(tok), viaProxy, c)
	ck := cookieNamed(rec, "ts_session")
	if rec.Code != http.StatusSeeOther || ck == nil || !ck.Secure || !ck.HttpOnly {
		t.Fatalf("session cookie behind https: %d %+v", rec.Code, ck)
	}
	// The audit row carries the forwarded client address.
	var detail string
	_ = a.st.Reader().QueryRow(`SELECT detail_json FROM audit_log WHERE action = 'setup.owner_created'`).Scan(&detail)
	if !strings.Contains(detail, "198.51.100.4") {
		t.Errorf("audit detail %q", detail)
	}
	// Plain http: not Secure (covered by TestSetupCreatesOwner); direct TLS is Secure.
	req := httptest.NewRequest(http.MethodGet, "https://example.test/login", nil)
	out := httptest.NewRecorder()
	a.h.ServeHTTP(out, req)
	if c := cookieNamed(out, csrfCookie); c == nil || !c.Secure {
		t.Errorf("direct TLS: %+v", c)
	}
}

func TestCSRFKeyPersisted(t *testing.T) {
	a := newTestApp(t)
	c, tok := a.anon(t, "/setup")
	var enc string
	ok, err := db.GetSetting(context.Background(), a.st.Reader(), "web.csrf_key", &enc)
	if err != nil || !ok {
		t.Fatalf("setting: %v %v", ok, err)
	}
	if key, err := base64.RawURLEncoding.DecodeString(enc); err != nil || len(key) != 32 {
		t.Fatalf("stored key: %v len %d", err, len(key))
	}
	// A second handler on the same database (a restart) accepts the first one's form.
	h2, err := NewHandler(context.Background(), Options{Version: "test", Store: a.st, SetupCode: testSetupCode, Hash: cheapHash})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/setup", strings.NewReader(setupForm(tok).Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.AddCookie(c)
	rec := httptest.NewRecorder()
	h2.ServeHTTP(rec, req)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("after restart: %d", rec.Code)
	}
}

func TestNewHandlerRequiresStoreAndCode(t *testing.T) {
	a := newTestApp(t)
	if _, err := NewHandler(context.Background(), Options{SetupCode: "X"}); err == nil {
		t.Error("nil store accepted")
	}
	if _, err := NewHandler(context.Background(), Options{Store: a.st}); err == nil {
		t.Error("empty setup code accepted")
	}
}

func TestSecurityHeadersOnRedirects(t *testing.T) {
	a := newTestApp(t)
	a.owner(t)
	for _, p := range []string{"/", "/healthz", mustAssets(t).URL("css/tokens.css")} {
		rec := a.do(http.MethodGet, p, nil, nil)
		if rec.Header().Get("Content-Security-Policy") != contentSecurityPolicy || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: security headers missing", p)
		}
	}
}

func TestNoSecretsInLogs(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	defer slog.SetDefault(old)

	a := newTestApp(t)
	own := a.owner(t)
	c, tok := a.anon(t, "/login")
	a.do(http.MethodPost, "/login", loginForm(tok, "ana", "wrong password!"), nil, c)
	a.do(http.MethodGet, "/", nil, nil, &http.Cookie{Name: "ts_session", Value: "bogus"})
	for _, secret := range []string{testPassword, "wrong password!", testSetupCode, own.Value} {
		if strings.Contains(buf.String(), secret) {
			t.Fatalf("a secret reached the log: %q", secret)
		}
	}
}

func TestFieldMessagesAndLimitCopy(t *testing.T) {
	for field, codes := range map[string][]string{
		"display_name": {"required", "too_long", "invalid"}, "username": {"invalid", "taken"}, "email": {"invalid"},
		"password": {"too_short", "too_long", "same_as_username"},
	} {
		for _, c := range codes {
			m := fieldMessage(field, c)
			if m == "" || m == "Check this field." || strings.Contains(m, "_") {
				t.Errorf("%s/%s: %q", field, c, m)
			}
		}
	}
	if fieldMessage("nope", "x") == "" {
		t.Error("unknown field has no fallback")
	}
	for in, want := range map[time.Duration]string{
		time.Second: "Wait 1 minute,", 61 * time.Second: "Wait 2 minutes,", 15 * time.Minute: "Wait 15 minutes,", 0: "Wait 1 minute,",
	} {
		if got := limitedMessage(in); !strings.Contains(got, want) {
			t.Errorf("limitedMessage(%v) = %q", in, got)
		}
	}
}
