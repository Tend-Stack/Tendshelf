package web

import (
	"context"
	"crypto/tls"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/auth/oidc"
	"github.com/Tend-Stack/tendshelf/internal/auth/oidc/oidctest"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// oidcWeb is the whole handler with a fake provider. The server address must be https or loopback,
// so every request that keeps httptest's default host is moved to localhost:8095.
type oidcWeb struct {
	*usersEnv
	p *oidctest.Provider
}

func newOIDCWeb(t *testing.T, mut func(*oidc.ConfigInput)) *oidcWeb {
	t.Helper()
	o := &oidcWeb{usersEnv: usersNewEnv(t), p: oidctest.New(t)}
	inner := o.a.h
	o.a.h = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "example.com" {
			r.Host = "localhost:8095"
		}
		inner.ServeHTTP(w, r)
	})
	o.configure(mut)
	return o
}

func (o *oidcWeb) configure(mut func(*oidc.ConfigInput)) {
	o.t.Helper()
	in := oidc.ConfigInput{Enabled: true, Name: "Fake", Issuer: o.p.Issuer(), ClientID: "tendshelf", ServerAddress: "http://localhost:8095", MatchEmail: true}
	if mut != nil {
		mut(&in)
	}
	owner := users.User{ID: o.ownerID}
	err := o.a.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		_, err := oidc.SaveConfig(context.Background(), tx, tx, owner, in, "", o.a.srv.now())
		return err
	})
	if err != nil {
		o.t.Fatal(err)
	}
}

// start opens /login/oidc as a fresh browser and returns the provider's address and the ts_oidc cookie.
func (o *oidcWeb) start(path string, ck *http.Cookie, mod func(*http.Request)) (*httptest.ResponseRecorder, *http.Cookie) {
	o.t.Helper()
	rec := o.a.do(http.MethodGet, path, nil, mod, ck)
	return rec, cookieNamed(rec, oidcCookie)
}

// callback follows the provider's redirect back to the callback with the cookie of the browser.
func (o *oidcWeb) callback(authURL string, oc *http.Cookie, ck *http.Cookie) *httptest.ResponseRecorder {
	o.t.Helper()
	code, state := o.p.Authorize(authURL)
	return o.a.do(http.MethodGet, "/login/oidc/callback?code="+url.QueryEscape(code)+"&state="+url.QueryEscape(state), nil, nil, oc, ck)
}

func TestOIDCLoginButtonOnlyWhenOn(t *testing.T) {
	o := newOIDCWeb(t, nil)
	body := o.a.do(http.MethodGet, "/login", nil, nil).Body.String()
	for _, want := range []string{"Sign in with Fake", `href="/login/oidc"`, "or use your password", `name="password"`} {
		if !strings.Contains(body, want) {
			t.Errorf("/login with the provider on lacks %q", want)
		}
	}
	rec := o.a.do(http.MethodGet, "/login?next=%2Fbooks", nil, nil)
	if !strings.Contains(rec.Body.String(), `href="/login/oidc?next=%2Fbooks"`) {
		t.Errorf("the button does not carry next: %s", rec.Body.String())
	}
	rec = o.post(o.owner, "/admin/sso/off", nil, false)
	usersWant(t, rec, http.StatusSeeOther, "turn off")
	body = o.a.do(http.MethodGet, "/login", nil, nil).Body.String()
	if strings.Contains(body, "Sign in with Fake") || strings.Contains(body, "/login/oidc") || !strings.Contains(body, `name="password"`) {
		t.Errorf("/login with the provider off shows the button or lost the form")
	}
	usersWant(t, o.a.do(http.MethodGet, "/login/oidc", nil, nil), http.StatusNotFound, "start while off")
	usersWant(t, o.a.do(http.MethodGet, "/login/oidc/callback?code=x&state=y", nil, nil), http.StatusNotFound, "callback while off")
}

// A provider that is down never takes the password form with it.
func TestOIDCProviderDownKeepsPasswordForm(t *testing.T) {
	o := newOIDCWeb(t, nil)
	o.p.Update(func(p *oidctest.Provider) { p.Down = true })
	body := o.a.do(http.MethodGet, "/login", nil, nil)
	usersWant(t, body, http.StatusOK, "login with the provider down")
	if !strings.Contains(body.Body.String(), `name="password"`) {
		t.Error("the password form is gone")
	}
	rec, oc := o.start("/login/oidc", nil, nil)
	usersWant(t, rec, http.StatusServiceUnavailable, "start with the provider down")
	if oc != nil && oc.MaxAge > 0 {
		t.Error("a start that failed set the state cookie")
	}
	if b := rec.Body.String(); !strings.Contains(b, "Fake is not answering right now") || !strings.Contains(b, `name="password"`) {
		t.Errorf("the down page lacks its words or the password form")
	}
	c, tok := o.a.anon(t, "/login")
	rec = o.a.do(http.MethodPost, "/login", url.Values{"_csrf": {tok}, "username": {"ana"}, "password": {testPassword}}, nil, c)
	usersWant(t, rec, http.StatusSeeOther, "password login with the provider down")
}

func TestOIDCStartCookieAndHeaders(t *testing.T) {
	o := newOIDCWeb(t, nil)
	rec, oc := o.start("/login/oidc", nil, nil)
	usersWant(t, rec, http.StatusSeeOther, "start")
	if oc == nil {
		t.Fatal("no ts_oidc cookie")
	}
	if !oc.HttpOnly || oc.SameSite != http.SameSiteLaxMode || oc.Path != "/login/oidc/callback" || oc.MaxAge != 600 || oc.Secure || len(oc.Value) != 43 {
		t.Errorf("cookie = %+v", oc)
	}
	if got := rec.Header().Get("Referrer-Policy"); got != "no-referrer" {
		t.Errorf("start Referrer-Policy = %q", got)
	}
	loc := rec.Header().Get("Location")
	if !strings.HasPrefix(loc, o.p.Issuer()+"/") || !strings.Contains(loc, "code_challenge_method=S256") || !strings.Contains(loc, "state="+oc.Value) {
		t.Errorf("Location = %s", loc)
	}
	// Over https the cookie is Secure, as the session cookie is.
	o.configure(func(in *oidc.ConfigInput) { in.ServerAddress = "https://localhost:8095" })
	rec, oc = o.start("/login/oidc", nil, func(r *http.Request) { r.TLS = &tls.ConnectionState{} })
	usersWant(t, rec, http.StatusSeeOther, "start over https")
	if oc == nil || !oc.Secure {
		t.Errorf("https cookie = %+v", oc)
	}
}

func TestOIDCStartOnAnotherOrigin(t *testing.T) {
	o := newOIDCWeb(t, nil)
	other := func(r *http.Request) { r.Host = "other.example:8443" }
	rec, oc := o.start("/login/oidc?next=%2Fbooks", nil, other)
	usersWant(t, rec, http.StatusSeeOther, "start on another origin")
	if oc != nil {
		t.Error("a cookie was set on the wrong origin")
	}
	if loc := rec.Header().Get("Location"); loc != "http://localhost:8095/login/oidc?next=%2Fbooks&ro=1" {
		t.Errorf("Location = %q", loc)
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Error("the origin redirect leaks a referrer")
	}
	// The Android window keeps its own origin and says where to connect.
	rec, oc = o.start("/login/oidc", nil, func(r *http.Request) {
		r.Host = "other.example:8443"
		r.AddCookie(&http.Cookie{Name: shellCookie, Value: "android"})
	})
	usersWant(t, rec, http.StatusOK, "android on another origin")
	if oc != nil || !strings.Contains(rec.Body.String(), "To sign in with Fake in the app, connect it to http://localhost:8095.") {
		t.Errorf("android page: cookie %v, body lacks the address words", oc)
	}
	// No loop when the configured address still does not match.
	rec, _ = o.start("/login/oidc?ro=1", nil, other)
	usersWant(t, rec, http.StatusOK, "second hop")
}

func (o *oidcWeb) memberWithEmail(email string) (string, *http.Cookie) {
	id, ck := o.person(users.RoleMember)
	o.exec(`UPDATE users SET email = ? WHERE id = ?`, email, id)
	return id, ck
}

func TestOIDCCallbackSignsInAndKeepsNext(t *testing.T) {
	o := newOIDCWeb(t, nil)
	id, _ := o.memberWithEmail("ana@example.com")
	o.p.Update(func(p *oidctest.Provider) { p.Sub, p.Email, p.EmailVerified = "sub-ana", "ana@example.com", true })
	rec, oc := o.start("/login/oidc?next="+url.QueryEscape("/app/connect?challenge=abc"), nil, nil)
	usersWant(t, rec, http.StatusSeeOther, "start")
	authURL := rec.Header().Get("Location")
	rec = o.callback(authURL, oc, nil)
	usersWant(t, rec, http.StatusSeeOther, "callback")
	if loc := rec.Header().Get("Location"); loc != "/app/connect?challenge=abc" {
		t.Errorf("next lost: %q", loc)
	}
	if sc := cookieNamed(rec, auth.CookieName); sc == nil || sc.Value == "" {
		t.Error("no session cookie")
	}
	if cc := cookieNamed(rec, oidcCookie); cc == nil || cc.MaxAge != -1 || cc.Path != "/login/oidc/callback" {
		t.Errorf("ts_oidc not cleared: %+v", cc)
	}
	if rec.Header().Get("Referrer-Policy") != "no-referrer" {
		t.Error("callback Referrer-Policy")
	}
	if n := o.count(`SELECT count(*) FROM oidc_identities WHERE user_id = ?`, id); n != 1 {
		t.Errorf("identities = %d", n)
	}
	// The same callback again fails: the row is gone.
	code, state := o.p.Authorize(authURL)
	replay := "/login/oidc/callback?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(state)
	usersWant(t, o.a.do(http.MethodGet, replay, nil, nil, oc), http.StatusForbidden, "replay")
}

func TestOIDCCallbackFailuresClearCookieAndSayLittle(t *testing.T) {
	o := newOIDCWeb(t, nil)
	o.memberWithEmail("ana@example.com")
	rec, oc := o.start("/login/oidc", nil, nil)
	authURL := rec.Header().Get("Location")
	code, state := o.p.Authorize(authURL)

	cases := map[string]struct {
		path   string
		cookie *http.Cookie
		status int
		words  string
	}{
		"no cookie":    {"/login/oidc/callback?code=" + code + "&state=" + state, nil, 403, "could not sign you in with Fake"},
		"wrong cookie": {"/login/oidc/callback?code=" + code + "&state=" + state, &http.Cookie{Name: oidcCookie, Value: strings.Repeat("a", 43)}, 403, "could not sign you in with Fake"},
		"cancelled":    {"/login/oidc/callback?error=access_denied&state=" + state, oc, 200, "Sign-in with Fake was cancelled."},
		"no state":     {"/login/oidc/callback?code=" + code, oc, 403, "could not sign you in with Fake"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			// A fresh row for each case so the cookie case that would succeed does not use up the others.
			rec := o.a.do(http.MethodGet, c.path, nil, nil, c.cookie)
			if rec.Code != c.status {
				t.Fatalf("status = %d, want %d", rec.Code, c.status)
			}
			b := rec.Body.String()
			if !strings.Contains(b, c.words) || !strings.Contains(b, `name="password"`) {
				t.Errorf("body lacks %q or the password form", c.words)
			}
			for _, secret := range []string{code, state} {
				if secret != "" && strings.Contains(b, secret) {
					t.Errorf("the page echoes the code or state")
				}
			}
			if cc := cookieNamed(rec, oidcCookie); cc == nil || cc.MaxAge != -1 {
				t.Errorf("ts_oidc not cleared: %+v", cc)
			}
			if rec.Header().Get("Referrer-Policy") != "no-referrer" || strings.Contains(rec.Header().Get("Location"), "state") {
				t.Error("headers")
			}
			if cookieNamed(rec, auth.CookieName) != nil {
				t.Error("a failed callback set a session")
			}
		})
	}
}

func TestOIDCNoAccountAndEmailTaken(t *testing.T) {
	o := newOIDCWeb(t, func(in *oidc.ConfigInput) { in.MatchEmail = false })
	rec, oc := o.start("/login/oidc", nil, nil)
	rec = o.callback(rec.Header().Get("Location"), oc, nil)
	usersWant(t, rec, http.StatusForbidden, "no account")
	if !strings.Contains(rec.Body.String(), "No account here uses this Fake sign-in yet.") {
		t.Error("no-account words")
	}
	o.memberWithEmail("ana@example.com")
	o.configure(func(in *oidc.ConfigInput) { in.MatchEmail, in.CreateAccounts = false, true })
	rec, oc = o.start("/login/oidc", nil, nil)
	rec = o.callback(rec.Header().Get("Location"), oc, nil)
	usersWant(t, rec, http.StatusConflict, "email taken")
	if !strings.Contains(rec.Body.String(), "An account here already uses this email.") {
		t.Error("email-taken words")
	}
}

func TestOIDCLinkConfirmUnlink(t *testing.T) {
	o := newOIDCWeb(t, func(in *oidc.ConfigInput) { in.MatchEmail = false })
	id, ck := o.person(users.RoleMember) // a member with no password
	page := o.get(ck, "/me/sso")
	usersWant(t, page, http.StatusOK, "account page")
	if b := page.Body.String(); !strings.Contains(b, "Link Fake") || strings.Contains(b, "Unlink") {
		t.Errorf("unlinked page: %s", b)
	}
	rec, oc := o.start("/me/sso/link", ck, nil)
	usersWant(t, rec, http.StatusSeeOther, "link start")
	rec = o.callback(rec.Header().Get("Location"), oc, ck)
	usersWant(t, rec, http.StatusSeeOther, "link callback")
	confirm := rec.Header().Get("Location")
	if !strings.HasPrefix(confirm, "/me/sso/confirm?id=") {
		t.Fatalf("Location = %q", confirm)
	}
	if n := o.count(`SELECT count(*) FROM oidc_identities WHERE user_id = ?`, id); n != 0 {
		t.Fatal("the callback linked before the confirm")
	}
	rec = o.get(ck, confirm)
	usersWant(t, rec, http.StatusOK, "confirm page")
	if rec.Header().Get("Referrer-Policy") != "no-referrer" || !strings.Contains(rec.Body.String(), "Link Fake?") || !strings.Contains(rec.Body.String(), "ana@example.com") {
		t.Errorf("confirm page: %s", rec.Body.String())
	}
	rowID := strings.TrimPrefix(confirm, "/me/sso/confirm?id=")
	// CSRF is checked on the confirming POST.
	rec = o.a.do(http.MethodPost, "/me/sso/confirm", url.Values{"id": {rowID}}, nil, ck)
	usersWant(t, rec, http.StatusForbidden, "confirm without CSRF")
	// Another person's session cannot confirm it.
	_, other := o.person(users.RoleMember)
	usersWant(t, o.post(other, "/me/sso/confirm", url.Values{"id": {rowID}}, false), http.StatusConflict, "confirm by another session")
	usersWant(t, o.post(ck, "/me/sso/confirm", url.Values{"id": {rowID}}, false), http.StatusSeeOther, "confirm")
	if n := o.count(`SELECT count(*) FROM oidc_identities WHERE user_id = ?`, id); n != 1 {
		t.Fatalf("identities = %d", n)
	}
	b := o.get(ck, "/me/sso").Body.String()
	if !strings.Contains(b, "Linked to ana@example.com since") || !strings.Contains(b, "Add a password first") || strings.Contains(b, `action="/me/sso/unlink"`) {
		t.Errorf("linked page without a password: %s", b)
	}
	// No password: unlinking is refused, also by a direct POST; CSRF is checked first.
	usersWant(t, o.a.do(http.MethodPost, "/me/sso/unlink", url.Values{}, nil, ck), http.StatusForbidden, "unlink without CSRF")
	usersWant(t, o.post(ck, "/me/sso/unlink", nil, false), http.StatusConflict, "unlink without a password")
	o.exec(`UPDATE users SET password_hash = (SELECT password_hash FROM users WHERE id = ?) WHERE id = ?`, o.ownerID, id)
	b = o.get(ck, "/me/sso").Body.String()
	if !strings.Contains(b, `action="/me/sso/unlink"`) {
		t.Error("the Unlink button is missing for a person with a password")
	}
	usersWant(t, o.post(ck, "/me/sso/unlink", nil, false), http.StatusSeeOther, "unlink")
	if n := o.count(`SELECT count(*) FROM oidc_identities WHERE user_id = ?`, id); n != 0 {
		t.Error("identity still there")
	}
}

func TestOIDCSignedOutAccountRoutes(t *testing.T) {
	o := newOIDCWeb(t, nil)
	for _, p := range []string{"/me/sso", "/me/sso/link", "/me/sso/confirm?id=x"} {
		rec := o.a.do(http.MethodGet, p, nil, nil)
		if rec.Code != http.StatusSeeOther || !strings.HasPrefix(rec.Header().Get("Location"), "/login") {
			t.Errorf("GET %s signed out: %d %q", p, rec.Code, rec.Header().Get("Location"))
		}
	}
	usersWant(t, o.a.do(http.MethodPost, "/me/sso/unlink", url.Values{}, nil), http.StatusForbidden, "unlink signed out")
}

func TestOIDCAdminPageOwnerOnlyAndSecret(t *testing.T) {
	o := newOIDCWeb(t, nil)
	_, member := o.person(users.RoleMember)
	_, guest := o.person(users.RoleGuest)
	contribID, contrib := o.person(users.RoleMember)
	o.exec(`UPDATE users SET adds_books = 1 WHERE id = ?`, contribID)
	for name, ck := range map[string]*http.Cookie{"admin": o.admin, "member": member, "guest": guest, "contributor": contrib} {
		usersWant(t, o.get(ck, "/admin/sso"), http.StatusNotFound, name+" GET")
		usersWant(t, o.post(ck, "/admin/sso", url.Values{"name": {"x"}}, false), http.StatusNotFound, name+" POST")
		usersWant(t, o.post(ck, "/admin/sso/off", nil, false), http.StatusNotFound, name+" off")
	}
	usersWant(t, o.a.do(http.MethodGet, "/admin/sso", nil, nil), http.StatusSeeOther, "signed out")
	// A device bearer token reaches /api/v1 only.
	tok := o.a.tokenFor(t, o.owner)
	_ = tok
	rec := o.a.do(http.MethodGet, "/admin/sso", nil, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 43)) })
	if rec.Code == http.StatusOK {
		t.Error("a bearer token opened /admin/sso")
	}

	const secret = "s3cr3t-client-value"
	form := url.Values{"name": {"Fake"}, "issuer": {o.p.Issuer()}, "client_id": {"tendshelf"}, "client_secret": {secret},
		"server_address": {"http://localhost:8095"}, "enabled": {"1"}, "new_role": {"guest"}, "match_email": {"0"}, "create_accounts": {"1"}}
	rec = o.post(o.owner, "/admin/sso", form, false)
	usersWant(t, rec, http.StatusSeeOther, "save")
	for _, path := range []string{"/admin/sso?saved=1", "/admin/sso", "/login", "/me"} {
		if strings.Contains(o.get(o.owner, path).Body.String(), secret) {
			t.Errorf("the client secret is on %s", path)
		}
	}
	if !strings.Contains(o.get(o.owner, "/admin/sso").Body.String(), "A secret is saved.") {
		t.Error("no sign that a secret is saved")
	}
	cfg, err := oidc.LoadConfig(context.Background(), o.a.st.Reader())
	if err != nil || cfg.ClientSecret != secret || cfg.NewRole != users.RoleGuest || !cfg.CreateAccounts {
		t.Errorf("saved config = %v, %v", cfg, err)
	}
	// An empty secret keeps the saved one.
	form.Set("client_secret", "")
	usersWant(t, o.post(o.owner, "/admin/sso", form, false), http.StatusSeeOther, "save without a secret")
	if cfg, _ = oidc.LoadConfig(context.Background(), o.a.st.Reader()); cfg.ClientSecret != secret {
		t.Error("an empty field dropped the secret")
	}
	// Audit rows never carry the secret.
	var n int
	_ = o.a.st.Reader().QueryRow(`SELECT count(*) FROM audit_log WHERE detail_json LIKE ?`, "%"+secret+"%").Scan(&n)
	if n != 0 {
		t.Error("the secret is in the audit log")
	}
}

func TestOIDCAdminSaveRefusals(t *testing.T) {
	o := newOIDCWeb(t, nil)
	base := func() url.Values {
		return url.Values{"name": {"Fake"}, "issuer": {o.p.Issuer()}, "client_id": {"tendshelf"}, "server_address": {"http://localhost:8095"}, "enabled": {"1"}}
	}
	// A bad address, a provider that calls itself something else, and one that does not answer.
	f := base()
	f.Set("issuer", "not a url")
	rec := o.post(o.owner, "/admin/sso", f, false)
	usersWant(t, rec, http.StatusUnprocessableEntity, "bad issuer")
	if !strings.Contains(rec.Body.String(), "Use the full address") {
		t.Error("bad issuer words")
	}
	o.p.Update(func(p *oidctest.Provider) { p.DiscoveryIssuer = "https://elsewhere.example" })
	rec = o.post(o.owner, "/admin/sso", base(), false)
	usersWant(t, rec, http.StatusUnprocessableEntity, "issuer mismatch")
	if !strings.Contains(rec.Body.String(), "The provider calls itself https://elsewhere.example. Use that address.") {
		t.Errorf("mismatch words: %s", rec.Body.String())
	}
	o.p.Update(func(p *oidctest.Provider) { p.DiscoveryIssuer = ""; p.Down = true })
	rec = o.post(o.owner, "/admin/sso", base(), false)
	usersWant(t, rec, http.StatusUnprocessableEntity, "provider down")
	if !strings.Contains(rec.Body.String(), "could not read the provider") {
		t.Error("discovery words")
	}
	// Saved off, nothing is checked.
	g := base()
	g.Del("enabled")
	usersWant(t, o.post(o.owner, "/admin/sso", g, false), http.StatusSeeOther, "save off")
}

func TestOIDCOriginsList(t *testing.T) {
	o := newOIDCWeb(t, nil)
	rec := o.a.do(http.MethodGet, "/login/oidc/origins", nil, nil)
	usersWant(t, rec, http.StatusOK, "origins")
	var got struct{ Origins []string }
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Origins) != 1 || got.Origins[0] != o.p.Issuer() {
		t.Errorf("origins = %v, want only %s", got.Origins, o.p.Issuer())
	}
	if rec.Header().Get("Cache-Control") != "no-store" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Error("origins headers")
	}
	usersWant(t, o.post(o.owner, "/admin/sso/off", nil, false), http.StatusSeeOther, "off")
	if b := strings.TrimSpace(o.a.do(http.MethodGet, "/login/oidc/origins", nil, nil).Body.String()); b != `{"origins":[]}` {
		t.Errorf("off -> %s", b)
	}
}

func TestOIDCPublicPathsExact(t *testing.T) {
	for p, want := range map[string]bool{
		"/login/oidc": true, "/login/oidc/callback": true, "/login/oidc/origins": true,
		"/login/oidc/": false, "/login/oidc/callback/x": false, "/login/oidcx": false, "/me/sso": false, "/admin/sso": false,
	} {
		if got := isOIDCPublicPath(p); got != want {
			t.Errorf("isOIDCPublicPath(%q) = %v", p, got)
		}
	}
}

func TestOIDCNoLogOfCredentials(t *testing.T) {
	// The only Location that carries a state is the authorize address the browser is sent to; the
	// callback's answers never echo the query.
	o := newOIDCWeb(t, nil)
	o.memberWithEmail("ana@example.com")
	rec, oc := o.start("/login/oidc", nil, nil)
	authURL := rec.Header().Get("Location")
	code, state := o.p.Authorize(authURL)
	rec = o.a.do(http.MethodGet, "/login/oidc/callback?code="+code+"&state="+state, nil, nil, oc)
	if loc := rec.Header().Get("Location"); strings.Contains(loc, code) || strings.Contains(loc, state) {
		t.Errorf("Location echoes the code or state: %q", loc)
	}
	if strings.Contains(rec.Body.String(), code) || strings.Contains(rec.Body.String(), state) {
		t.Error("body echoes the code or state")
	}
}
