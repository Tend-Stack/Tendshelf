package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

// pkHost is the host the handler tests use: localhost is a secure context over plain http, and
// httptest's default example.com is not (passkeys are then switched off, which one test relies on).
const pkHost = "localhost"

// pkAdd adds a passkey for the person holding ck through the two JSON routes and returns the
// authenticator that holds it.
func pkAdd(t *testing.T, a *testApp, ck *http.Cookie, name, password string) *softAuth {
	t.Helper()
	tok := a.tokenFor(t, ck)
	opts := a.jsonPost("/me/passkeys/options", tok, pkHost, map[string]string{"password": password}, ck)
	if opts.Code != http.StatusOK {
		t.Fatalf("register options: %d %s", opts.Code, opts.Body.String())
	}
	pk := jsonOf(t, opts)["publicKey"].(map[string]any)
	s := newSoftAuth(t)
	s.handle = unb64t(t, pk["user"].(map[string]any)["id"].(string))
	cd, att := s.create(unb64t(t, pk["challenge"].(string)), pkHost, "http://"+pkHost)
	rec := a.jsonPost("/me/passkeys", tok, pkHost, map[string]any{
		"name": name, "type": "public-key", "clientDataJSON": cd, "attestationObject": att, "transports": []string{"internal"},
	}, ck)
	if rec.Code != http.StatusOK || jsonOf(t, rec)["redirect"] != "/me/passkeys?added=1" {
		t.Fatalf("register: %d %s", rec.Code, rec.Body.String())
	}
	return s
}

// pkOptions asks for a sign-in challenge as an anonymous browser; it returns the challenge, the
// ts_csrf cookie and the token.
func pkOptions(t *testing.T, a *testApp) (challenge []byte, c *http.Cookie, tok string) {
	t.Helper()
	c, tok = a.anon(t, "/login")
	rec := a.jsonPost("/login/passkey/options", tok, pkHost, struct{}{}, c)
	if rec.Code != http.StatusOK {
		t.Fatalf("sign-in options: %d %s", rec.Code, rec.Body.String())
	}
	pk := jsonOf(t, rec)["publicKey"].(map[string]any)
	if pk["rpId"] != pkHost || pk["userVerification"] != "required" {
		t.Fatalf("options: %v", pk)
	}
	return unb64t(t, pk["challenge"].(string)), c, tok
}

func TestPasskeyRoundTripAndLoginSetsSessionCookie(t *testing.T) {
	a := newTestApp(t)
	own := a.owner(t)
	s := pkAdd(t, a, own, "Work laptop", testPassword)

	// The page lists it, with the words of the plan.
	body := a.do(http.MethodGet, "/me/passkeys", nil, func(r *http.Request) { r.Host = pkHost }, own).Body.String()
	for _, w := range []string{"Work laptop", "Not used yet", "On one device", `aria-label="Remove passkey Work laptop"`, "Add a passkey"} {
		if !strings.Contains(body, w) {
			t.Errorf("page lacks %q", w)
		}
	}

	// Sign in with it in a browser that has no session.
	ch, c, tok := pkOptions(t, a)
	rec := a.jsonPost("/login/passkey", tok, pkHost, s.get(ch, pkHost, "http://"+pkHost, "/libraries/x?y=1"), c)
	if rec.Code != http.StatusOK || jsonOf(t, rec)["redirect"] != "/libraries/x?y=1" {
		t.Fatalf("sign-in: %d %s", rec.Code, rec.Body.String())
	}
	ck := cookieNamed(rec, "ts_session")
	if ck == nil || !ck.HttpOnly || ck.SameSite != http.SameSiteLaxMode || ck.MaxAge != 2592000 {
		t.Fatalf("cookie %+v", ck)
	}
	if a.do(http.MethodGet, "/", nil, nil, ck).Code != http.StatusOK {
		t.Fatal("the passkey session does not open /")
	}
	if n := a.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'auth.login' AND detail_json LIKE '%passkey%'`); n != 1 {
		t.Errorf("auth.login audit rows via passkey: %d", n)
	}
	// The same challenge never works twice.
	rec = a.jsonPost("/login/passkey", tok, pkHost, s.get(ch, pkHost, "http://"+pkHost, ""), c)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "That passkey did not work here.") {
		t.Errorf("replay: %d %s", rec.Code, rec.Body.String())
	}
}

func TestPasskeyLoginSafeNext(t *testing.T) {
	a := newTestApp(t)
	s := pkAdd(t, a, a.owner(t), "Phone", testPassword)
	for _, next := range []string{"//evil.com", "https://evil.com", `/\evil.com`, "javascript:alert(1)"} {
		ch, c, tok := pkOptions(t, a)
		rec := a.jsonPost("/login/passkey", tok, pkHost, s.get(ch, pkHost, "http://"+pkHost, next), c)
		if rec.Code != http.StatusOK || jsonOf(t, rec)["redirect"] != "/" {
			t.Errorf("next=%q: %d %s", next, rec.Code, rec.Body.String())
		}
	}
}

func TestPasskeyLoginNeedsCSRF(t *testing.T) {
	a := newTestApp(t)
	a.owner(t)
	c, _ := a.anon(t, "/login")
	for _, path := range []string{"/login/passkey/options", "/login/passkey"} {
		if rec := a.jsonPost(path, "", pkHost, struct{}{}, c); rec.Code != http.StatusForbidden {
			t.Errorf("%s without a token: %d", path, rec.Code)
		}
		if rec := a.jsonPost(path, "wrong", pkHost, struct{}{}, c); rec.Code != http.StatusForbidden {
			t.Errorf("%s with a wrong token: %d", path, rec.Code)
		}
	}
	// The signed-in routes too.
	own := a.owner(t)
	for _, path := range []string{"/me/passkeys/options", "/me/passkeys"} {
		if rec := a.jsonPost(path, "wrong", pkHost, struct{}{}, own); rec.Code != http.StatusForbidden {
			t.Errorf("%s with a wrong token: %d", path, rec.Code)
		}
	}
}

func TestPasskeyJSONOnly(t *testing.T) {
	a := newTestApp(t)
	a.owner(t)
	c, tok := a.anon(t, "/login")
	rec := a.do(http.MethodPost, "/login/passkey/options", url.Values{"_csrf": {tok}}, func(r *http.Request) { r.Host = pkHost }, c)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("a form body: %d", rec.Code)
	}
	rec = a.jsonPost("/login/passkey", tok, pkHost, "not an object", c)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("a string body: %d", rec.Code)
	}
	rec = a.jsonPost("/login/passkey", tok, pkHost, map[string]string{"id": "!!", "type": "public-key"}, c)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("bad base64: %d", rec.Code)
	}
}

func TestPasskeyLoginRateLimited(t *testing.T) {
	a := newTestApp(t)
	a.owner(t)
	c, tok := a.anon(t, "/login")
	junk := map[string]string{"id": b64([]byte("0123456789abcdef")), "type": "public-key", "clientDataJSON": b64([]byte("{}")),
		"authenticatorData": b64(make([]byte, 37)), "signature": b64([]byte("sig")), "userHandle": b64(make([]byte, 32))}
	for i := 0; i < 20; i++ {
		if rec := a.jsonPost("/login/passkey", tok, pkHost, junk, c); rec.Code != http.StatusUnauthorized {
			t.Fatalf("failure %d: %d %s", i, rec.Code, rec.Body.String())
		}
	}
	rec := a.jsonPost("/login/passkey", tok, pkHost, junk, c)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || !strings.Contains(rec.Body.String(), "Too many tries.") {
		t.Fatalf("%d Retry-After %q %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body.String())
	}
	if cookieNamed(rec, "ts_session") != nil {
		t.Fatal("a limited sign-in set a session")
	}
}

func TestPasskeyLoginHiddenOnIPHost(t *testing.T) {
	a := newTestApp(t)
	a.owner(t)
	for _, host := range []string{"127.0.0.1:8095", "192.168.1.5", "[::1]:8095"} {
		rec := a.do(http.MethodGet, "/login", nil, func(r *http.Request) { r.Host = host })
		if strings.Contains(rec.Body.String(), "Sign in with a passkey") || strings.Contains(rec.Body.String(), "passkey.js") {
			t.Errorf("%s: the login page offers passkeys", host)
		}
		c, tok := a.anon(t, "/login")
		if rec := a.jsonPost("/login/passkey/options", tok, host, struct{}{}, c); rec.Code != http.StatusNotFound {
			t.Errorf("%s: options %d", host, rec.Code)
		}
	}
	own := a.owner(t)
	page := a.do(http.MethodGet, "/me/passkeys", nil, func(r *http.Request) { r.Host = "127.0.0.1:8095" }, own)
	if !strings.Contains(page.Body.String(), "Passkeys need a server name, not a number address.") || strings.Contains(page.Body.String(), "data-ts-module") {
		t.Error("the Passkeys page on an IP host does not explain")
	}
}

func TestPasskeyLoginHiddenOverHTTP(t *testing.T) {
	a := newTestApp(t)
	a.owner(t)
	// example.com over plain http is not a secure context.
	rec := a.do(http.MethodGet, "/login", nil, func(r *http.Request) { r.Host = "books.example" })
	if strings.Contains(rec.Body.String(), "Sign in with a passkey") {
		t.Error("the login page offers passkeys over http")
	}
	c, tok := a.anon(t, "/login")
	if rec := a.jsonPost("/login/passkey/options", tok, "books.example", struct{}{}, c); rec.Code != http.StatusNotFound {
		t.Errorf("options over http: %d", rec.Code)
	}
	page := a.do(http.MethodGet, "/me/passkeys", nil, nil, a.owner(t))
	if !strings.Contains(page.Body.String(), "Passkeys need a secure address (https).") {
		t.Error("the Passkeys page over http does not explain")
	}
	// Behind a trusted proxy that says https, and on localhost, they are on.
	if rec := a.do(http.MethodGet, "/login", nil, func(r *http.Request) { r.Host = pkHost }); !strings.Contains(rec.Body.String(), "Sign in with a passkey") {
		t.Error("the login page on localhost lacks the button")
	}
}

func TestPasskeyLoginHiddenInShell(t *testing.T) {
	a := newTestApp(t)
	own := a.owner(t)
	for _, shell := range []string{"desktop", "android"} {
		sc := &http.Cookie{Name: shellCookie, Value: shell}
		rec := a.do(http.MethodGet, "/login", nil, func(r *http.Request) { r.Host = pkHost }, sc)
		if strings.Contains(rec.Body.String(), "Sign in with a passkey") || strings.Contains(rec.Body.String(), "passkey.js") {
			t.Errorf("%s: the login page offers passkeys", shell)
		}
		c, tok := a.anon(t, "/login")
		if rec := a.jsonPost("/login/passkey/options", tok, pkHost, struct{}{}, c, sc); rec.Code != http.StatusNotFound {
			t.Errorf("%s: options %d", shell, rec.Code)
		}
		page := a.do(http.MethodGet, "/me/passkeys", nil, func(r *http.Request) { r.Host = pkHost }, own, sc)
		if !strings.Contains(page.Body.String(), "Add passkeys in a web browser.") || strings.Contains(page.Body.String(), "passkey.js") {
			t.Errorf("%s: the Passkeys page does not explain", shell)
		}
		menu := a.do(http.MethodGet, "/me", nil, nil, own, sc).Body.String()
		if strings.Contains(menu, `href="/me/passkeys"`) {
			t.Errorf("%s: Profile links Passkeys", shell)
		}
		if rec := a.jsonPost("/me/passkeys/options", a.tokenFor(t, own), pkHost, map[string]string{"password": testPassword}, own, sc); rec.Code != http.StatusNotFound {
			t.Errorf("%s: register options %d", shell, rec.Code)
		}
	}
	if menu := a.do(http.MethodGet, "/me", nil, nil, own).Body.String(); !strings.Contains(menu, `href="/me/passkeys"`) || !strings.Contains(menu, "None yet") {
		t.Error("Profile lacks the Passkeys row in a browser")
	}
}

func TestPasskeyOriginMismatchRefused(t *testing.T) {
	a := newTestApp(t)
	own := a.owner(t)
	tok := a.tokenFor(t, own)
	opts := a.jsonPost("/me/passkeys/options", tok, pkHost, map[string]string{"password": testPassword}, own)
	pk := jsonOf(t, opts)["publicKey"].(map[string]any)
	s := newSoftAuth(t)
	cd, att := s.create(unb64t(t, pk["challenge"].(string)), pkHost, "http://other.localhost")
	rec := a.jsonPost("/me/passkeys", tok, pkHost, map[string]any{"name": "x", "type": "public-key", "clientDataJSON": cd, "attestationObject": att}, own)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "That passkey could not be added.") {
		t.Errorf("register with another origin: %d %s", rec.Code, rec.Body.String())
	}
	if n := a.count(t, `SELECT COUNT(*) FROM passkeys`); n != 0 {
		t.Errorf("%d passkeys stored", n)
	}

	// Sign in: the passkey is made for localhost; the request says Host a.localhost, clientData says b.
	s = pkAdd(t, a, own, "Good", testPassword)
	c, tk := a.anon(t, "/login")
	o := a.jsonPost("/login/passkey/options", tk, "a.localhost", struct{}{}, c)
	ch := unb64t(t, jsonOf(t, o)["publicKey"].(map[string]any)["challenge"].(string))
	rec = a.jsonPost("/login/passkey", tk, "a.localhost", s.get(ch, "a.localhost", "http://b.localhost", ""), c)
	if rec.Code != http.StatusUnauthorized || cookieNamed(rec, "ts_session") != nil {
		t.Errorf("sign-in with another origin: %d", rec.Code)
	}
	// And a credential made for localhost does not sign in at a.localhost.
	ch2 := unb64t(t, jsonOf(t, a.jsonPost("/login/passkey/options", tk, "a.localhost", struct{}{}, c))["publicKey"].(map[string]any)["challenge"].(string))
	rec = a.jsonPost("/login/passkey", tk, "a.localhost", s.get(ch2, "a.localhost", "http://a.localhost", ""), c)
	if rec.Code != http.StatusUnauthorized || cookieNamed(rec, "ts_session") != nil {
		t.Errorf("sign-in on another host: %d", rec.Code)
	}
}

func TestPasskeyRegisterNeedsPassword(t *testing.T) {
	a := newTestApp(t)
	own := a.owner(t)
	tok := a.tokenFor(t, own)
	rec := a.jsonPost("/me/passkeys/options", tok, pkHost, map[string]string{"password": "not the password"}, own)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "That password is not right.") {
		t.Errorf("wrong password: %d %s", rec.Code, rec.Body.String())
	}
}

func TestMePasskeysOwnOnly(t *testing.T) {
	a := newTestApp(t)
	own := a.owner(t)
	pkAdd(t, a, own, "Owner phone", testPassword)
	var id string
	if err := a.st.Reader().QueryRow(`SELECT id FROM passkeys`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	mem := a.as(t, users.RoleMember)
	rec := a.do(http.MethodPost, "/me/passkeys/"+id+"/remove", url.Values{"_csrf": {a.tokenFor(t, mem)}}, nil, mem)
	if rec.Code != http.StatusNotFound {
		t.Errorf("another person's passkey: %d", rec.Code)
	}
	if n := a.count(t, `SELECT COUNT(*) FROM passkeys`); n != 1 {
		t.Fatalf("the passkey was removed: %d left", n)
	}
	// It does not show on their page either.
	if strings.Contains(a.do(http.MethodGet, "/me/passkeys", nil, nil, mem).Body.String(), "Owner phone") {
		t.Error("another person's passkey is listed")
	}
	// The owner removes it; the owner still has a password.
	rec = a.do(http.MethodPost, "/me/passkeys/"+id+"/remove", url.Values{"_csrf": {a.tokenFor(t, own)}}, nil, own)
	if rec.Code != http.StatusSeeOther || location(rec) != "/me/passkeys?removed=1" {
		t.Errorf("own removal: %d -> %q", rec.Code, location(rec))
	}
	if n := a.count(t, `SELECT COUNT(*) FROM passkeys`); n != 0 {
		t.Errorf("%d left", n)
	}
	if !strings.Contains(a.do(http.MethodGet, "/me/passkeys?removed=1", nil, nil, own).Body.String(), "Passkey removed.") {
		t.Error("no removed banner")
	}
}

func TestMePasskeysLastWayInRefused(t *testing.T) {
	a := newTestApp(t)
	mem := a.as(t, users.RoleMember) // no password, no provider: the passkey is the only way in
	pkAdd(t, a, mem, "Only", "")
	var id string
	if err := a.st.Reader().QueryRow(`SELECT id FROM passkeys`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	rec := a.do(http.MethodPost, "/me/passkeys/"+id+"/remove", url.Values{"_csrf": {a.tokenFor(t, mem)}}, nil, mem)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "This passkey is your only way to sign in. Add a password first.") {
		t.Errorf("last way in: %d", rec.Code)
	}
	if n := a.count(t, `SELECT COUNT(*) FROM passkeys`); n != 1 {
		t.Errorf("%d left", n)
	}
}

func TestMePasskeysEveryRole(t *testing.T) {
	a := newTestApp(t)
	contrib := a.as(t, users.RoleMember)
	if _, err := a.st.Writer().Exec(`UPDATE users SET adds_books = 1 WHERE id = ?`, sweepUserID(t, a, contrib)); err != nil {
		t.Fatal(err)
	}
	for name, ck := range map[string]*http.Cookie{"admin": a.as(t, users.RoleAdmin), "member": a.as(t, users.RoleMember), "guest": a.as(t, users.RoleGuest), "contributor": contrib} {
		page := a.do(http.MethodGet, "/me/passkeys", nil, func(r *http.Request) { r.Host = pkHost }, ck)
		if page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Add a passkey") {
			t.Errorf("%s: page %d", name, page.Code)
		}
		pkAdd(t, a, ck, name+" key", "")
	}
}

func TestMePasskeysAnonymousRedirects(t *testing.T) {
	a := newTestApp(t)
	a.owner(t)
	c, tok := a.anon(t, "/login")
	rec := a.do(http.MethodGet, "/me/passkeys", nil, nil)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(location(rec), "/login") {
		t.Errorf("GET: %d -> %q", rec.Code, location(rec))
	}
	for _, path := range []string{"/me/passkeys/options", "/me/passkeys"} {
		rec := a.jsonPost(path, tok, pkHost, struct{}{}, c)
		if rec.Code != http.StatusSeeOther || !strings.HasPrefix(location(rec), "/login") {
			t.Errorf("POST %s: %d -> %q", path, rec.Code, location(rec))
		}
	}
	rec = a.do(http.MethodPost, "/me/passkeys/x/remove", url.Values{"_csrf": {tok}}, nil, c)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(location(rec), "/login") {
		t.Errorf("POST remove: %d -> %q", rec.Code, location(rec))
	}
	rec = a.do(http.MethodPost, "/admin/users/x/passkeys/remove", url.Values{"_csrf": {tok}}, nil, c)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(location(rec), "/login") {
		t.Errorf("POST admin remove: %d -> %q", rec.Code, location(rec))
	}
}

func TestAdminRemovePasskeysForbiddenRoles(t *testing.T) {
	a := newTestApp(t)
	own := a.owner(t)
	pkAdd(t, a, own, "Owner key", testPassword)
	target := a.as(t, users.RoleMember)
	pkAdd(t, a, target, "Member key", "")
	targetID := sweepUserID(t, a, target)
	contrib := a.as(t, users.RoleMember)
	if _, err := a.st.Writer().Exec(`UPDATE users SET adds_books = 1 WHERE id = ?`, sweepUserID(t, a, contrib)); err != nil {
		t.Fatal(err)
	}
	for name, ck := range map[string]*http.Cookie{"member": a.as(t, users.RoleMember), "guest": a.as(t, users.RoleGuest), "contributor": contrib} {
		rec := a.do(http.MethodPost, "/admin/users/"+targetID+"/passkeys/remove", url.Values{"_csrf": {a.tokenFor(t, ck)}}, nil, ck)
		if rec.Code != http.StatusForbidden && rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	// An admin cannot touch the owner.
	adm := a.as(t, users.RoleAdmin)
	rec := a.do(http.MethodPost, "/admin/users/"+sweepUserID(t, a, own)+"/passkeys/remove", url.Values{"_csrf": {a.tokenFor(t, adm)}}, nil, adm)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Errorf("admin on owner: %d", rec.Code)
	}
	// Nobody removes their own through the admin route.
	rec = a.do(http.MethodPost, "/admin/users/"+sweepUserID(t, a, own)+"/passkeys/remove", url.Values{"_csrf": {a.tokenFor(t, own)}}, nil, own)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusForbidden {
		t.Errorf("owner on self: %d", rec.Code)
	}
	if n := a.count(t, `SELECT COUNT(*) FROM passkeys`); n != 2 {
		t.Errorf("%d passkeys left, want 2", n)
	}
}

func TestAdminRemovePasskeys(t *testing.T) {
	a := newTestApp(t)
	own := a.owner(t)
	mem := a.as(t, users.RoleMember)
	pkAdd(t, a, mem, "One", "")
	pkAdd(t, a, mem, "Two", "")
	id := sweepUserID(t, a, mem)
	page := a.do(http.MethodGet, "/admin/users/"+id, nil, nil, own).Body.String()
	if !strings.Contains(page, "2 passkeys") || !strings.Contains(page, "Remove their passkeys") || !strings.Contains(page, "/passkeys/remove") {
		t.Error("the person page lacks the passkeys control")
	}
	rec := a.do(http.MethodPost, "/admin/users/"+id+"/passkeys/remove", url.Values{"_csrf": {a.tokenFor(t, own)}}, nil, own)
	if rec.Code != http.StatusSeeOther || location(rec) != "/admin/users/"+id+"?passkeys=removed" {
		t.Fatalf("%d -> %q", rec.Code, location(rec))
	}
	if n := a.count(t, `SELECT COUNT(*) FROM passkeys WHERE user_id = ?`, id); n != 0 {
		t.Errorf("%d left", n)
	}
	if n := a.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'passkey.removed_all'`); n != 1 {
		t.Errorf("removed_all audit rows: %d", n)
	}
	after := a.do(http.MethodGet, "/admin/users/"+id+"?passkeys=removed", nil, nil, own).Body.String()
	if !strings.Contains(after, "Their passkeys are removed.") || strings.Contains(after, "Remove their passkeys") {
		t.Error("banner or button wrong after removal")
	}
	// Their sessions are untouched.
	if a.do(http.MethodGet, "/", nil, nil, mem).Code != http.StatusOK {
		t.Error("the person was signed out")
	}
}
