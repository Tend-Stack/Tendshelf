package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

var signInTokenRe = regexp.MustCompile(`/signin/([A-Za-z0-9_-]{43})`)

// normalizeCSRF removes the per-visitor token so two answers can be compared.
var csrfInBody = regexp.MustCompile(`(X-CSRF-Token&#34;:&#34;|name="_csrf" value=")[^&"]+`)

// signInPerson adds a person with a password (so the reset step applies) and a session.
func (e *usersEnv) signInPerson(role users.Role, password bool) (string, *http.Cookie) {
	e.t.Helper()
	id, ck := e.person(role)
	if password {
		h, err := e.a.srv.users.HashPassword(context.Background(), "an old password 1")
		if err != nil {
			e.t.Fatal(err)
		}
		e.exec(`UPDATE users SET password_hash = ? WHERE id = ?`, h, id)
	}
	return id, ck
}

// makeLink posts the make-a-link form as ck and returns the answer and the token in the panel.
func (e *usersEnv) makeLink(ck *http.Cookie, id string) (*httptest.ResponseRecorder, string) {
	e.t.Helper()
	rec := e.post(ck, "/admin/users/"+id+"/signin-link", nil, false)
	m := signInTokenRe.FindStringSubmatch(rec.Body.String())
	if m == nil {
		return rec, ""
	}
	return rec, m[1]
}

// openLink inserts a link row for userID by SQL and returns the raw token; mutate changes the row.
func (e *usersEnv) openLink(userID, set string) string {
	e.t.Helper()
	tok, hash := auth.NewToken()
	now := time.Now().UnixMilli()
	e.exec(`INSERT INTO sign_in_links (id, token_hash, user_id, created_at, expires_at) VALUES (?, ?, ?, ?, ?)`,
		"sl_"+userID, hash[:], userID, now, now+3600_000)
	if set != "" {
		e.exec(`UPDATE sign_in_links SET `+set+` WHERE id = ?`, "sl_"+userID)
	}
	return tok
}

func (e *usersEnv) linkUsed(tok string) bool {
	e.t.Helper()
	h, _ := auth.HashToken(tok)
	return e.count(`SELECT count(*) FROM sign_in_links WHERE token_hash = ? AND used_at IS NOT NULL`, h[:]) == 1
}

// spendAnon opens and confirms a link as a fresh browser and returns the POST answer and the cookie it set.
func (e *usersEnv) spendAnon(tok string) (*httptest.ResponseRecorder, *http.Cookie) {
	e.t.Helper()
	c, csrf := e.a.anon(e.t, "/signin/"+tok)
	rec := e.a.do(http.MethodPost, "/signin/"+tok, url.Values{"_csrf": {csrf}}, nil, c)
	return rec, cookieNamed(rec, auth.CookieName)
}

func TestSignInLinkMakeByRole(t *testing.T) {
	e := usersNewEnv(t)
	adminB, _ := e.person(users.RoleAdmin)
	memberID, memberCk := e.person(users.RoleMember)
	guestID, guestCk := e.person(users.RoleGuest)
	contribID, contribCk := e.person(users.RoleMember)
	e.exec(`UPDATE users SET adds_books = 1 WHERE id = ?`, contribID)
	disabledID, _ := e.person(users.RoleMember)
	e.exec(`UPDATE users SET disabled = 1 WHERE id = ?`, disabledID)

	ok := func(who string, ck *http.Cookie, id string) {
		rec, tok := e.makeLink(ck, id)
		if rec.Code != 200 || tok == "" || !strings.Contains(rec.Body.String(), `id="signin-link"`) {
			t.Errorf("%s -> %s: %d, token %q", who, id, rec.Code, tok)
			return
		}
		if rec.Header().Get("Location") != "" || !strings.Contains(rec.Header().Get("Cache-Control"), "no-store") {
			t.Errorf("%s -> %s: Location %q Cache-Control %q", who, id, rec.Header().Get("Location"), rec.Header().Get("Cache-Control"))
		}
	}
	refused := func(who string, ck *http.Cookie, id string) {
		if rec, tok := e.makeLink(ck, id); rec.Code != 404 || tok != "" {
			t.Errorf("%s -> %s: %d, token %q", who, id, rec.Code, tok)
		}
	}
	for _, id := range []string{e.adminID, memberID, guestID, contribID} {
		ok("owner", e.owner, id)
	}
	for _, id := range []string{memberID, guestID, contribID} {
		ok("admin", e.admin, id)
	}
	refused("admin", e.admin, e.ownerID)
	refused("admin", e.admin, adminB)
	refused("admin", e.admin, e.adminID) // self
	refused("owner", e.owner, e.ownerID) // self
	refused("owner", e.owner, disabledID)
	refused("admin", e.admin, disabledID)
	for who, ck := range map[string]*http.Cookie{"member": memberCk, "guest": guestCk, "contributor": contribCk} {
		for _, id := range []string{e.ownerID, e.adminID, memberID, guestID, contribID} {
			refused(who, ck, id)
		}
	}
	c, csrf := e.a.anon(t, "/login")
	if rec := e.a.do(http.MethodPost, "/admin/users/"+memberID+"/signin-link", url.Values{"_csrf": {csrf}}, nil, c); rec.Code != 303 || !strings.HasPrefix(location(rec), "/login") {
		t.Errorf("anonymous: %d -> %q", rec.Code, location(rec))
	}
	// no CSRF token
	rec := e.a.do(http.MethodPost, "/admin/users/"+memberID+"/signin-link", url.Values{}, nil, e.owner)
	if rec.Code != 403 {
		t.Errorf("missing CSRF: %d", rec.Code)
	}
	if e.count(`SELECT count(*) FROM sign_in_links WHERE user_id = ?`, adminB) != 0 {
		t.Error("a refused request stored a link")
	}
}

func TestSignInLinkOneAnswer(t *testing.T) {
	e := usersNewEnv(t)
	person := func(disabled bool) string {
		id, _ := e.person(users.RoleMember)
		if disabled {
			e.exec(`UPDATE users SET disabled = 1 WHERE id = ?`, id)
		}
		return id
	}
	unknown, _ := auth.NewToken()
	cases := map[string]string{
		"malformed": "not-a-token",
		"unknown":   unknown,
		"used":      e.openLink(person(false), "used_at = 1"),
		"expired":   e.openLink(person(false), "expires_at = 1"),
		"revoked":   e.openLink(person(false), "revoked_at = 1"),
		"disabled":  e.openLink(person(true), ""),
		"owner":     e.openLink(e.ownerID, ""),
	}
	norm := func(rec *httptest.ResponseRecorder) string {
		return csrfInBody.ReplaceAllString(rec.Body.String(), "$1")
	}
	var first string
	for name, tok := range cases {
		get := e.a.do(http.MethodGet, "/signin/"+tok, nil, nil)
		c, csrf := e.a.anon(t, "/login")
		post := e.a.do(http.MethodPost, "/signin/"+tok, url.Values{"_csrf": {csrf}}, nil, c)
		if get.Code != 404 || post.Code != 404 {
			t.Errorf("%s: GET %d POST %d", name, get.Code, post.Code)
			continue
		}
		if first == "" {
			first = norm(get)
		}
		if norm(get) != first || norm(post) != first {
			t.Errorf("%s: the answer differs from the others", name)
		}
		if cookieNamed(post, auth.CookieName) != nil {
			t.Errorf("%s: a session cookie was set", name)
		}
		if strings.Contains(get.Body.String(), tok) && name != "malformed" {
			t.Errorf("%s: the page repeats the token", name)
		}
	}
}

func TestSignInLinkGetDoesNotSpend(t *testing.T) {
	e := usersNewEnv(t)
	id, _ := e.person(users.RoleMember)
	_, tok := e.makeLink(e.owner, id)
	for i := 0; i < 2; i++ {
		rec := e.a.do(http.MethodGet, "/signin/"+tok, nil, nil)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Sign in") || !strings.Contains(rec.Body.String(), "Sign in to ") {
			t.Fatalf("GET: %d", rec.Code)
		}
		if rec.Header().Get("Cache-Control") != "no-store" || cookieNamed(rec, auth.CookieName) != nil {
			t.Fatal("GET cached or set a session")
		}
	}
	if e.linkUsed(tok) || e.count(`SELECT count(*) FROM sessions WHERE user_id = ?`, id) != 1 {
		t.Error("GET changed the link or the sessions")
	}
}

func TestSignInLinkNoPasswordToWelcome(t *testing.T) {
	e := usersNewEnv(t)
	id, _ := e.signInPerson(users.RoleMember, false)
	_, tok := e.makeLink(e.owner, id)
	rec, ck := e.spendAnon(tok)
	if rec.Code != 303 || location(rec) != "/welcome" || ck == nil {
		t.Fatalf("spend: %d -> %q, cookie %v", rec.Code, location(rec), ck)
	}
	if strings.Contains(location(rec), tok) {
		t.Error("the token is in Location")
	}
	if page := e.get(ck, "/welcome"); page.Code != 200 {
		t.Errorf("/welcome: %d", page.Code)
	}
	if page := e.get(ck, "/me/new-password"); page.Code != 303 || location(page) != "/welcome" {
		t.Errorf("/me/new-password without a password: %d -> %q", page.Code, location(page))
	}
}

func TestSignInLinkResetFlow(t *testing.T) {
	e := usersNewEnv(t)
	id, other := e.signInPerson(users.RoleMember, true)
	_, tok := e.makeLink(e.owner, id)
	rec, ck := e.spendAnon(tok)
	if rec.Code != 303 || location(rec) != "/me/new-password" || ck == nil {
		t.Fatalf("spend: %d -> %q", rec.Code, location(rec))
	}
	if again := e.a.do(http.MethodGet, "/signin/"+tok, nil, nil); again.Code != 404 {
		t.Errorf("link after it was spent: %d", again.Code)
	}
	page := e.get(ck, "/me/new-password")
	if page.Code != 200 || !strings.Contains(page.Body.String(), "Choose a new password") || !strings.Contains(page.Body.String(), `autocomplete="new-password"`) {
		t.Fatalf("GET /me/new-password: %d", page.Code)
	}
	short := e.post(ck, "/me/new-password", url.Values{"password": {"short"}}, false)
	if short.Code != 422 || !strings.Contains(short.Body.String(), "Choose a new password") {
		t.Fatalf("short password: %d", short.Code)
	}
	next := "a brand new password 42"
	done := e.post(ck, "/me/new-password", url.Values{"password": {next}}, false)
	fresh := cookieNamed(done, auth.CookieName)
	if done.Code != 303 || location(done) != "/me?changed=password" || fresh == nil {
		t.Fatalf("save: %d -> %q", done.Code, location(done))
	}
	if r := e.get(other, "/"); r.Code != 303 || !strings.HasPrefix(location(r), "/login") {
		t.Errorf("the other browser's session survived: %d", r.Code)
	}
	if r := e.get(ck, "/"); r.Code != 303 {
		t.Errorf("the link session survived: %d", r.Code)
	}
	if r := e.get(fresh, "/me"); r.Code != 200 {
		t.Errorf("the new session: %d", r.Code)
	}
	// the step cannot be repeated
	if r := e.get(fresh, "/me/new-password"); r.Code != 404 {
		t.Errorf("repeat: %d", r.Code)
	}
	if e.count(`SELECT count(*) FROM audit_log WHERE action = 'auth.password_reset'`) != 1 {
		t.Error("no password_reset audit row")
	}
}

func TestNewPasswordOrdinarySession404(t *testing.T) {
	e := usersNewEnv(t)
	id, ordinary := e.signInPerson(users.RoleMember, true)
	if r := e.get(ordinary, "/me/new-password"); r.Code != 404 {
		t.Errorf("GET: %d", r.Code)
	}
	if r := e.post(ordinary, "/me/new-password", url.Values{"password": {"a brand new password 42"}}, false); r.Code != 404 {
		t.Errorf("POST: %d", r.Code)
	}
	// a link starts another session of the same person; the ordinary one still gets 404
	_, tok := e.makeLink(e.owner, id)
	rec, linkCk := e.spendAnon(tok)
	if rec.Code != 303 || linkCk == nil {
		t.Fatalf("spend: %d", rec.Code)
	}
	if r := e.get(linkCk, "/me/new-password"); r.Code != 200 {
		t.Fatalf("link session: %d", r.Code)
	}
	if r := e.get(ordinary, "/me/new-password"); r.Code != 404 {
		t.Errorf("ordinary session after a link: %d", r.Code)
	}
	if r := e.post(ordinary, "/me/new-password", url.Values{"password": {"a brand new password 42"}}, false); r.Code != 404 {
		t.Errorf("ordinary POST after a link: %d", r.Code)
	}
	// an hour later even the link session is refused
	e.exec(`UPDATE sign_in_links SET used_at = used_at - 3600001 WHERE user_id = ?`, id)
	if r := e.get(linkCk, "/me/new-password"); r.Code != 404 {
		t.Errorf("link session after the hour: %d", r.Code)
	}
}

func TestSignInLinkAppSession409(t *testing.T) {
	e := usersNewEnv(t)
	id, _ := e.signInPerson(users.RoleMember, true)
	_, tok := e.makeLink(e.owner, id)
	_, appCk := e.person(users.RoleMember)
	h, _ := auth.HashToken(appCk.Value)
	appUser := sweepUserID(t, e.a, appCk)
	e.exec(`INSERT INTO devices(id, user_id, name, kind, token_hash, created_at) VALUES ('dv_app', ?, 'Pixel', 'mobile', randomblob(32), 1)`, appUser)
	e.exec(`UPDATE sessions SET device_id = 'dv_app' WHERE token_hash = ?`, h[:])
	if r := e.a.do(http.MethodGet, "/signin/"+tok, nil, nil, appCk); r.Code != 409 {
		t.Errorf("GET: %d", r.Code)
	}
	csrf := e.a.tokenFor(t, appCk)
	if r := e.a.do(http.MethodPost, "/signin/"+tok, url.Values{"_csrf": {csrf}}, nil, appCk); r.Code != 409 {
		t.Errorf("POST: %d", r.Code)
	}
	if e.linkUsed(tok) {
		t.Error("the link was spent")
	}
}

func TestSignInLinkBearer(t *testing.T) {
	e := usersNewEnv(t)
	id, _ := e.signInPerson(users.RoleMember, true)
	_, tok := e.makeLink(e.owner, id)
	dev := deviceIssue(t, e.a)
	bearer := func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+dev.Token) }
	for _, p := range []struct {
		method, path string
	}{{http.MethodGet, "/me/new-password"}, {http.MethodPost, "/me/new-password"}, {http.MethodPost, "/signin/" + tok}} {
		rec := e.a.do(p.method, p.path, url.Values{"password": {"a brand new password 42"}}, bearer)
		if rec.Code == 200 || rec.Code == 303 && !strings.HasPrefix(location(rec), "/login") {
			t.Errorf("%s %s: %d -> %q", p.method, p.path, rec.Code, location(rec))
		}
		if cookieNamed(rec, auth.CookieName) != nil {
			t.Errorf("%s %s set a session", p.method, p.path)
		}
	}
	if e.linkUsed(tok) {
		t.Error("a bearer request spent the link")
	}
}

func TestSignInLinkRoleChangeRevokes(t *testing.T) {
	e := usersNewEnv(t)
	id, _ := e.signInPerson(users.RoleMember, true)
	_, tok := e.makeLink(e.owner, id)
	if r := e.post(e.owner, "/admin/users/"+id+"/role", url.Values{"role": {"guest"}}, false); r.Code != 303 {
		t.Fatalf("role: %d", r.Code)
	}
	if r := e.a.do(http.MethodGet, "/signin/"+tok, nil, nil); r.Code != 404 {
		t.Errorf("after a role change: %d", r.Code)
	}
	_, tok2 := e.makeLink(e.owner, id)
	if r := e.post(e.owner, "/admin/users/"+id+"/disable", nil, false); r.Code != 303 {
		t.Fatalf("disable: %d", r.Code)
	}
	if r := e.a.do(http.MethodGet, "/signin/"+tok2, nil, nil); r.Code != 404 {
		t.Errorf("after disabling: %d", r.Code)
	}
	if rec, _ := e.makeLink(e.owner, id); rec.Code != 404 {
		t.Errorf("a link for a disabled account: %d", rec.Code)
	}
}

func TestLoginNoPasswordLine(t *testing.T) {
	a := newTestApp(t)
	a.owner(t)
	rec := a.do(http.MethodGet, "/login", nil, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), loginNoPassword) {
		t.Fatalf("/login: %d, line missing", rec.Code)
	}
}

func TestPersonPageUsername(t *testing.T) {
	e := usersNewEnv(t)
	withPW, _ := e.signInPerson(users.RoleMember, true)
	noPW, _ := e.signInPerson(users.RoleGuest, false)
	body := func(id string) string {
		rec := e.get(e.owner, "/admin/users/"+id)
		if rec.Code != 200 {
			t.Fatalf("person page: %d", rec.Code)
		}
		return rec.Body.String()
	}
	var un string
	if err := e.a.st.Reader().QueryRow(`SELECT username FROM users WHERE id = ?`, withPW).Scan(&un); err != nil {
		t.Fatal(err)
	}
	b := body(withPW)
	if !strings.Contains(b, "Username: "+un) || strings.Contains(b, "No password yet") || !strings.Contains(b, "Make a sign-in link") {
		t.Error("page of a person with a password")
	}
	if !strings.Contains(body(noPW), "No password yet") {
		t.Error("page of a person without a password")
	}
	if strings.Contains(body(noPW), "A sign-in link is open") {
		t.Error("an open-link line without a link")
	}
	e.makeLink(e.owner, withPW)
	if !strings.Contains(body(withPW), "A sign-in link is open until ") {
		t.Error("no open-link line after making one")
	}
	if tok := signInTokenRe.FindString(body(withPW)); tok != "" {
		t.Errorf("the address came back on a later load: %s", tok)
	}
}

func TestGateSignInPaths(t *testing.T) {
	for p, want := range map[string]bool{"/signin/x": true, "/signin/": false, "/signin/x/y": false, "/signin": false, "/signinx/y": false} {
		if got := isSignInPath(p); got != want {
			t.Errorf("isSignInPath(%q) = %v", p, got)
		}
	}
	a := newTestApp(t)
	a.owner(t)
	if rec := a.do(http.MethodGet, "/signin/x", nil, nil); rec.Code != 404 {
		t.Errorf("/signin/x anonymous: %d (public)", rec.Code)
	}
	for _, p := range []string{"/signin/", "/signin/x/y"} {
		if rec := a.do(http.MethodGet, p, nil, nil); rec.Code != 303 || !strings.HasPrefix(location(rec), "/login") {
			t.Errorf("%s anonymous: %d -> %q", p, rec.Code, location(rec))
		}
	}
	if rec := a.do(http.MethodGet, "/me/new-password", nil, nil); rec.Code != 303 || !strings.HasPrefix(location(rec), "/login") {
		t.Errorf("/me/new-password anonymous: %d -> %q", rec.Code, location(rec))
	}
	if got := logPath(httptest.NewRequest(http.MethodGet, "/signin/secrettoken", nil)); strings.Contains(got, "secret") {
		t.Errorf("logPath keeps the token: %q", got)
	}
}

func TestServiceWorkerPassesSignIn(t *testing.T) {
	b, err := os.ReadFile("static/js/sw.js")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`const PASS = \[[^\]]*"/signin/"`).Match(b) {
		t.Error(`sw.js PASS does not list "/signin/"`)
	}
}
