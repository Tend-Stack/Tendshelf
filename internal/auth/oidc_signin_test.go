package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth/oidc"
	"github.com/Tend-Stack/tendshelf/internal/auth/oidc/oidctest"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

type oidcEnv struct {
	*env
	p *oidctest.Provider
}

// newOIDCEnv is an env with an owner, a fake provider and the provider switched on.
func newOIDCEnv(t *testing.T, mut func(*oidc.ConfigInput)) *oidcEnv {
	t.Helper()
	e := newEnv(t).withOwner(t)
	p := oidctest.New(t)
	p.Update(func(p *oidctest.Provider) { p.Now = e.clk.Now })
	e.svc = NewService(e.st, e.users, e.sess, ServiceOptions{SetupCode: testCode, Now: e.clk.Now, Devices: e.dev,
		OIDC: oidc.NewClient(oidc.ClientOptions{Now: e.clk.Now})})
	o := &oidcEnv{env: e, p: p}
	o.configure(t, mut)
	return o
}

func (o *oidcEnv) configure(t *testing.T, mut func(*oidc.ConfigInput)) {
	t.Helper()
	in := oidc.ConfigInput{Enabled: true, Name: "Fake", Issuer: o.p.Issuer(), ClientID: "tendshelf", ServerAddress: "http://127.0.0.1:8095"}
	if mut != nil {
		mut(&in)
	}
	err := o.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		_, err := oidc.SaveConfig(context.Background(), tx, tx, o.owner, in, "", o.clk.Now())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func (o *oidcEnv) sql(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := o.st.Writer().Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// member makes an account with an email.
func (o *oidcEnv) member(t *testing.T, name string, role users.Role, email string, password bool) users.User {
	t.Helper()
	u := o.person(t, name, role, password)
	if email != "" {
		o.sql(t, `UPDATE users SET email = ? WHERE id = ?`, email, u.ID)
	}
	return u
}

func (o *oidcEnv) sessionFor(t *testing.T, u users.User) Session {
	t.Helper()
	var s Session
	err := o.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		_, s, err = o.sess.Create(context.Background(), tx, u.ID, "ua")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// begin starts a flow and drives the provider's authorize hop; the callback is what the browser would send.
func (o *oidcEnv) begin(t *testing.T, purpose string, sess *Session) OIDCCallback {
	t.Helper()
	authURL, state, err := o.svc.OIDCStart(context.Background(), purpose, "/app/connect?x=1", ip1, sess)
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	code, st := o.p.Authorize(authURL)
	if st != state {
		t.Fatalf("state changed on the way")
	}
	return OIDCCallback{State: state, Cookie: state, Code: code}
}

func (o *oidcEnv) signIn(t *testing.T) (OIDCResult, error) {
	t.Helper()
	return o.svc.OIDCFinish(context.Background(), o.begin(t, "signin", nil), ip1, "ua", nil)
}

func (o *oidcEnv) failReasons(t *testing.T) []string {
	t.Helper()
	rows, err := o.st.Reader().Query(`SELECT detail_json FROM audit_log WHERE action = 'auth.oidc_failed' ORDER BY at, rowid`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var d string
		if err := rows.Scan(&d); err != nil {
			t.Fatal(err)
		}
		i := strings.Index(d, `"reason":"`)
		if i < 0 {
			t.Fatalf("no reason in %s", d)
		}
		d = d[i+len(`"reason":"`):]
		out = append(out, d[:strings.Index(d, `"`)])
	}
	return out
}

func (o *oidcEnv) lastReason(t *testing.T) string {
	t.Helper()
	r := o.failReasons(t)
	if len(r) == 0 {
		return ""
	}
	return r[len(r)-1]
}

func (o *oidcEnv) identities(t *testing.T) int {
	return o.count(t, `SELECT count(*) FROM oidc_identities`)
}

func (o *oidcEnv) openRows(t *testing.T) int { return o.count(t, `SELECT count(*) FROM oidc_logins`) }

func TestOIDCSignInExistingIdentity(t *testing.T) {
	o := newOIDCEnv(t, nil)
	m := o.member(t, "Rosa", users.RoleMember, "", true)
	o.sql(t, `INSERT INTO oidc_identities(issuer, subject, user_id, linked_via, created_at) VALUES (?, 'sub-1', ?, 'account', 1)`, o.p.Issuer(), m.ID)
	prev := o.sessionFor(t, m)
	res, err := o.svc.OIDCFinish(context.Background(), o.begin(t, "signin", nil), ip1, "ua", &prev)
	if err != nil {
		t.Fatal(err)
	}
	if res.Login.User.ID != m.ID || res.Next != "/app/connect?x=1" || res.Login.Token == "" {
		t.Fatalf("result = %+v", res)
	}
	if _, _, _, err := o.sess.Lookup(context.Background(), res.Login.Token); err != nil {
		t.Fatalf("new session does not work: %v", err)
	}
	if o.count(t, `SELECT count(*) FROM sessions WHERE token_hash = ?`, prev.Hash[:]) != 0 {
		t.Error("the previous session was kept")
	}
	if o.count(t, `SELECT count(*) FROM audit_log WHERE action = 'auth.login' AND detail_json LIKE '%"via":"oidc"%'`) != 1 {
		t.Error("no auth.login via oidc audit row")
	}
	if o.openRows(t) != 0 {
		t.Error("the sign-in row was kept")
	}
	if o.count(t, `SELECT count(*) FROM oidc_identities WHERE last_used_at = ?`, o.clk.Now().UnixMilli()) != 1 {
		t.Error("last_used_at not set")
	}
}

func TestOIDCNextIsSafe(t *testing.T) {
	o := newOIDCEnv(t, nil)
	m := o.member(t, "Rosa", users.RoleMember, "", true)
	o.sql(t, `INSERT INTO oidc_identities(issuer, subject, user_id, linked_via, created_at) VALUES (?, 'sub-1', ?, 'account', 1)`, o.p.Issuer(), m.ID)
	authURL, state, err := o.svc.OIDCStart(context.Background(), "signin", "//evil.example/x", ip1, nil)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := o.p.Authorize(authURL)
	res, err := o.svc.OIDCFinish(context.Background(), OIDCCallback{State: state, Cookie: state, Code: code}, ip1, "ua", nil)
	if err != nil || res.Next != "/" {
		t.Fatalf("next = %q, %v", res.Next, err)
	}
}

func TestOIDCStateAndReplay(t *testing.T) {
	ctx := context.Background()
	t.Run("state differs from the cookie", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		cb := o.begin(t, "signin", nil)
		cb.Cookie = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
		if _, err := o.svc.OIDCFinish(ctx, cb, ip1, "ua", nil); !errors.Is(err, ErrOIDCFailed) {
			t.Fatalf("err = %v", err)
		}
		if o.lastReason(t) != "state" || o.openRows(t) != 0 {
			t.Errorf("reason %q, open rows %d", o.lastReason(t), o.openRows(t))
		}
	})
	t.Run("no cookie", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		cb := o.begin(t, "signin", nil)
		cb.Cookie = ""
		if _, err := o.svc.OIDCFinish(ctx, cb, ip1, "ua", nil); !errors.Is(err, ErrOIDCFailed) || o.lastReason(t) != "state" {
			t.Fatalf("err = %v, reason %q", err, o.lastReason(t))
		}
		if o.openRows(t) != 0 {
			t.Error("the row survived a callback without the cookie")
		}
	})
	t.Run("unknown and malformed state", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		for _, st := range []string{"", "short", oidc.RandomToken()} {
			cb := OIDCCallback{State: st, Cookie: st, Code: "c"}
			if _, err := o.svc.OIDCFinish(ctx, cb, ip1, "ua", nil); !errors.Is(err, ErrOIDCFailed) {
				t.Fatalf("state %q: err = %v", st, err)
			}
		}
	})
	t.Run("replay", func(t *testing.T) {
		o := newOIDCEnv(t, func(in *oidc.ConfigInput) { in.CreateAccounts = true })
		cb := o.begin(t, "signin", nil)
		if _, err := o.svc.OIDCFinish(ctx, cb, ip1, "ua", nil); err != nil {
			t.Fatal(err)
		}
		users1 := o.count(t, `SELECT count(*) FROM users`)
		if _, err := o.svc.OIDCFinish(ctx, cb, ip1, "ua", nil); !errors.Is(err, ErrOIDCFailed) {
			t.Fatalf("replay err = %v", err)
		}
		if o.lastReason(t) != "state" || o.count(t, `SELECT count(*) FROM users`) != users1 {
			t.Error("a replay did something")
		}
	})
	t.Run("expired row", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		cb := o.begin(t, "signin", nil)
		o.clk.Add(11 * time.Minute)
		if _, err := o.svc.OIDCFinish(ctx, cb, ip1, "ua", nil); !errors.Is(err, ErrOIDCFailed) || o.lastReason(t) != "state" {
			t.Fatalf("err = %v, reason %q", err, o.lastReason(t))
		}
		if o.openRows(t) != 0 {
			t.Error("the expired row survived")
		}
	})
	t.Run("code from another sign-in", func(t *testing.T) {
		o := newOIDCEnv(t, func(in *oidc.ConfigInput) { in.CreateAccounts = true })
		a, b := o.begin(t, "signin", nil), o.begin(t, "signin", nil)
		mixed := OIDCCallback{State: b.State, Cookie: b.State, Code: a.Code}
		if _, err := o.svc.OIDCFinish(ctx, mixed, ip1, "ua", nil); !errors.Is(err, ErrOIDCFailed) {
			t.Fatalf("err = %v", err)
		}
		if o.identities(t) != 0 {
			t.Error("an identity was made")
		}
	})
	t.Run("nonce of another row", func(t *testing.T) {
		o := newOIDCEnv(t, func(in *oidc.ConfigInput) { in.CreateAccounts = true })
		o.p.Update(func(p *oidctest.Provider) { p.NonceOverride = "not-the-nonce-of-this-row" })
		if _, err := o.signIn(t); !errors.Is(err, ErrOIDCFailed) || o.lastReason(t) != "nonce" {
			t.Fatalf("err = %v, reason %q", err, o.lastReason(t))
		}
	})
	t.Run("provider error", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		cb := o.begin(t, "signin", nil)
		cb.Code, cb.Error = "", "server_error"
		if _, err := o.svc.OIDCFinish(ctx, cb, ip1, "ua", nil); !errors.Is(err, ErrOIDCFailed) || o.lastReason(t) != "provider_error" {
			t.Fatalf("err = %v, reason %q", err, o.lastReason(t))
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		cb := o.begin(t, "signin", nil)
		cb.Code, cb.Error = "", "access_denied"
		if _, err := o.svc.OIDCFinish(ctx, cb, ip1, "ua", nil); !errors.Is(err, ErrOIDCCancelled) {
			t.Fatalf("err = %v", err)
		}
		if o.openRows(t) != 0 || len(o.failReasons(t)) != 0 {
			t.Error("a cancel left a row or counted as a failure")
		}
	})
}

func TestOIDCRefusedIDTokens(t *testing.T) {
	for name, tc := range map[string]struct {
		set    func(*oidctest.Provider)
		reason string
	}{
		"wrong issuer":   {func(p *oidctest.Provider) { p.IssClaim = "https://evil.example" }, "issuer"},
		"wrong audience": {func(p *oidctest.Provider) { p.Aud = []string{"someone-else"} }, "audience"},
		"expired":        {func(p *oidctest.Provider) { p.ExpIn = -5 * time.Minute }, "expired"},
		"HS256":          {func(p *oidctest.Provider) { p.Alg = "HS256" }, "alg"},
		"none":           {func(p *oidctest.Provider) { p.Alg = "none" }, "alg"},
		"wrong key":      {func(p *oidctest.Provider) { p.WrongKey = true }, "signature"},
	} {
		t.Run(name, func(t *testing.T) {
			o := newOIDCEnv(t, func(in *oidc.ConfigInput) { in.CreateAccounts = true })
			o.p.Update(tc.set)
			if _, err := o.signIn(t); !errors.Is(err, ErrOIDCFailed) || o.lastReason(t) != tc.reason {
				t.Fatalf("err = %v, reason %q", err, o.lastReason(t))
			}
			if o.identities(t) != 0 || o.count(t, `SELECT count(*) FROM users`) != 1 {
				t.Error("a refused token made an account")
			}
		})
	}
}

func TestOIDCDisabledUser(t *testing.T) {
	o := newOIDCEnv(t, nil)
	m := o.member(t, "Rosa", users.RoleMember, "", true)
	o.sql(t, `INSERT INTO oidc_identities(issuer, subject, user_id, linked_via, created_at) VALUES (?, 'sub-1', ?, 'account', 1)`, o.p.Issuer(), m.ID)
	o.sql(t, `UPDATE users SET disabled = 1 WHERE id = ?`, m.ID)
	if _, err := o.signIn(t); !errors.Is(err, ErrOIDCFailed) || o.lastReason(t) != "disabled" {
		t.Fatalf("err = %v, reason %q", err, o.lastReason(t))
	}
	if o.count(t, `SELECT count(*) FROM sessions WHERE user_id = ?`, m.ID) != 0 {
		t.Error("a disabled account got a session")
	}
}

func TestOIDCEmailMatching(t *testing.T) {
	const email = "ana@example.com"
	matchOn := func(in *oidc.ConfigInput) { in.MatchEmail = true }
	t.Run("verified email links a member", func(t *testing.T) {
		o := newOIDCEnv(t, matchOn)
		m := o.member(t, "Rosa", users.RoleMember, "Ana@Example.com", true)
		res, err := o.signIn(t)
		if err != nil || res.Login.User.ID != m.ID {
			t.Fatalf("res = %+v, err = %v", res, err)
		}
		if o.count(t, `SELECT count(*) FROM oidc_identities WHERE user_id = ? AND linked_via = 'email'`, m.ID) != 1 {
			t.Error("identity not written with via email")
		}
		if o.count(t, `SELECT count(*) FROM audit_log WHERE action = 'auth.oidc_linked' AND detail_json LIKE '%"via":"email"%'`) != 1 {
			t.Error("no link audit row")
		}
	})
	t.Run("links a guest", func(t *testing.T) {
		o := newOIDCEnv(t, matchOn)
		g := o.member(t, "Gus", users.RoleGuest, email, true)
		if res, err := o.signIn(t); err != nil || res.Login.User.ID != g.ID {
			t.Fatalf("res = %+v, err = %v", res, err)
		}
	})
	t.Run("unverified email never links", func(t *testing.T) {
		o := newOIDCEnv(t, matchOn)
		o.member(t, "Rosa", users.RoleMember, email, true)
		o.p.Update(func(p *oidctest.Provider) { p.EmailVerified = false })
		if _, err := o.signIn(t); !errors.Is(err, ErrOIDCNoAccount) || o.identities(t) != 0 {
			t.Fatalf("err = %v, identities %d", err, o.identities(t))
		}
	})
	t.Run("match off never links", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		o.member(t, "Rosa", users.RoleMember, email, true)
		if _, err := o.signIn(t); !errors.Is(err, ErrOIDCEmailTaken) || o.identities(t) != 0 {
			t.Fatalf("err = %v, identities %d", err, o.identities(t))
		}
	})
	t.Run("owner is never linked by email", func(t *testing.T) {
		o := newOIDCEnv(t, func(in *oidc.ConfigInput) { in.MatchEmail, in.CreateAccounts = true, true })
		o.sql(t, `UPDATE users SET email = ? WHERE id = ?`, email, o.owner.ID)
		if _, err := o.signIn(t); !errors.Is(err, ErrOIDCEmailTaken) || o.identities(t) != 0 {
			t.Fatalf("err = %v, identities %d", err, o.identities(t))
		}
		if o.count(t, `SELECT count(*) FROM sessions WHERE user_id = ?`, o.owner.ID) != 1 { // only the setup session
			t.Error("the owner got a provider session")
		}
	})
	t.Run("admin is never linked by email", func(t *testing.T) {
		o := newOIDCEnv(t, matchOn)
		o.member(t, "Adi", users.RoleAdmin, email, true)
		if _, err := o.signIn(t); !errors.Is(err, ErrOIDCEmailTaken) || o.identities(t) != 0 {
			t.Fatalf("err = %v, identities %d", err, o.identities(t))
		}
	})
	t.Run("two accounts with the email", func(t *testing.T) {
		o := newOIDCEnv(t, matchOn)
		o.member(t, "Rosa", users.RoleMember, email, true)
		o.member(t, "Ruben", users.RoleMember, email, true)
		if _, err := o.signIn(t); !errors.Is(err, ErrOIDCEmailTaken) || o.identities(t) != 0 {
			t.Fatalf("err = %v, identities %d", err, o.identities(t))
		}
	})
	t.Run("disabled account is not linked", func(t *testing.T) {
		o := newOIDCEnv(t, matchOn)
		m := o.member(t, "Rosa", users.RoleMember, email, true)
		o.sql(t, `UPDATE users SET disabled = 1 WHERE id = ?`, m.ID)
		if _, err := o.signIn(t); !errors.Is(err, ErrOIDCEmailTaken) || o.identities(t) != 0 {
			t.Fatalf("err = %v, identities %d", err, o.identities(t))
		}
	})
	t.Run("account that already has a provider account", func(t *testing.T) {
		o := newOIDCEnv(t, matchOn)
		m := o.member(t, "Rosa", users.RoleMember, email, true)
		o.sql(t, `INSERT INTO oidc_identities(issuer, subject, user_id, linked_via, created_at) VALUES (?, 'other-sub', ?, 'account', 1)`, o.p.Issuer(), m.ID)
		if _, err := o.signIn(t); !errors.Is(err, ErrOIDCEmailTaken) || o.identities(t) != 1 {
			t.Fatalf("err = %v, identities %d", err, o.identities(t))
		}
	})
	t.Run("email only in userinfo still matches", func(t *testing.T) {
		o := newOIDCEnv(t, matchOn)
		m := o.member(t, "Rosa", users.RoleMember, email, true)
		o.p.Update(func(p *oidctest.Provider) { p.EmailInUserinfo = true })
		if res, err := o.signIn(t); err != nil || res.Login.User.ID != m.ID {
			t.Fatalf("res = %+v, err = %v", res, err)
		}
	})
}

func TestOIDCCreateAccounts(t *testing.T) {
	t.Run("on makes a member with no grants and no password", func(t *testing.T) {
		o := newOIDCEnv(t, func(in *oidc.ConfigInput) { in.CreateAccounts = true })
		res, err := o.signIn(t)
		if err != nil {
			t.Fatal(err)
		}
		u := res.Login.User
		if u.Role != users.RoleMember || u.DisplayName != "Ana Lopez" || u.Email != "ana@example.com" || u.AddsBooks {
			t.Fatalf("user = %+v", u)
		}
		if has, _ := o.users.HasPassword(context.Background(), u.ID); has {
			t.Error("the new account has a password")
		}
		if o.count(t, `SELECT count(*) FROM grants WHERE user_id = ?`, u.ID) != 0 {
			t.Error("the new account has grants")
		}
		if o.count(t, `SELECT count(*) FROM oidc_identities WHERE user_id = ? AND linked_via = 'new'`, u.ID) != 1 {
			t.Error("identity not written with via new")
		}
		// The same provider account signs in to the same account next time.
		again, err := o.signIn(t)
		if err != nil || again.Login.User.ID != u.ID || o.count(t, `SELECT count(*) FROM users`) != 2 {
			t.Fatalf("second sign-in: %+v, %v", again, err)
		}
	})
	t.Run("guest by setting, unverified email not stored, name falls back", func(t *testing.T) {
		o := newOIDCEnv(t, func(in *oidc.ConfigInput) { in.CreateAccounts, in.NewRole = true, users.RoleGuest })
		o.p.Update(func(p *oidctest.Provider) { p.EmailVerified = false; p.Name = ""; p.PreferredName = "ana" })
		res, err := o.signIn(t)
		if err != nil {
			t.Fatal(err)
		}
		if u := res.Login.User; u.Role != users.RoleGuest || u.Email != "" || u.DisplayName != "ana" {
			t.Fatalf("user = %+v", u)
		}
	})
	t.Run("a long name is clamped", func(t *testing.T) {
		o := newOIDCEnv(t, func(in *oidc.ConfigInput) { in.CreateAccounts = true })
		o.p.Update(func(p *oidctest.Provider) { p.Name = strings.Repeat("é", 200) })
		res, err := o.signIn(t)
		if err != nil || len([]rune(res.Login.User.DisplayName)) != 80 {
			t.Fatalf("name = %d runes, %v", len([]rune(res.Login.User.DisplayName)), err)
		}
	})
	t.Run("off", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		if _, err := o.signIn(t); !errors.Is(err, ErrOIDCNoAccount) || o.identities(t) != 0 {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("verified email of an account with create on", func(t *testing.T) {
		o := newOIDCEnv(t, func(in *oidc.ConfigInput) { in.CreateAccounts = true })
		o.member(t, "Rosa", users.RoleMember, "ana@example.com", true)
		if _, err := o.signIn(t); !errors.Is(err, ErrOIDCEmailTaken) || o.count(t, `SELECT count(*) FROM users`) != 2 {
			t.Fatalf("err = %v", err)
		}
	})
}

func TestOIDCOffAndProviderTrouble(t *testing.T) {
	ctx := context.Background()
	t.Run("off", func(t *testing.T) {
		o := newOIDCEnv(t, func(in *oidc.ConfigInput) { in.Enabled = false })
		if _, _, err := o.svc.OIDCStart(ctx, "signin", "/", ip1, nil); !errors.Is(err, ErrOIDCOff) {
			t.Fatalf("start err = %v", err)
		}
		if _, err := o.svc.OIDCFinish(ctx, OIDCCallback{State: "x"}, ip1, "ua", nil); !errors.Is(err, ErrOIDCOff) {
			t.Fatalf("finish err = %v", err)
		}
	})
	t.Run("no client", func(t *testing.T) {
		e := newEnv(t).withOwner(t)
		if _, _, err := e.svc.OIDCStart(ctx, "signin", "/", ip1, nil); !errors.Is(err, ErrOIDCOff) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("link needs a session", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		if _, _, err := o.svc.OIDCStart(ctx, "link", "/me/sso", ip1, nil); !errors.Is(err, ErrOIDCOff) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("provider down at start", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		o.p.Update(func(p *oidctest.Provider) { p.Down = true })
		if _, _, err := o.svc.OIDCStart(ctx, "signin", "/", ip1, nil); !errors.Is(err, oidc.ErrProviderDown) {
			t.Fatalf("err = %v", err)
		}
		if o.openRows(t) != 0 {
			t.Error("a row was made")
		}
	})
	t.Run("provider down at the callback is not the person's failure", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		cb := o.begin(t, "signin", nil)
		o.p.Update(func(p *oidctest.Provider) { p.Down = true })
		before := o.svc.ipLim.Len()
		if _, err := o.svc.OIDCFinish(ctx, cb, ip1, "ua", nil); !errors.Is(err, oidc.ErrProviderDown) {
			t.Fatalf("err = %v", err)
		}
		if o.svc.ipLim.Len() != before || o.lastReason(t) != "provider_down" {
			t.Errorf("limiter keys %d -> %d, reason %q", before, o.svc.ipLim.Len(), o.lastReason(t))
		}
	})
	t.Run("password sign-in works with the provider down", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		o.p.Update(func(p *oidctest.Provider) { p.Down = true })
		if _, err := o.svc.Login(ctx, "ana", testPW, ip1, "ua", nil); err != nil {
			t.Fatalf("password sign-in: %v", err)
		}
	})
}

func TestOIDCLimits(t *testing.T) {
	ctx := context.Background()
	t.Run("limited address is refused before any query", func(t *testing.T) {
		o := newOIDCEnv(t, func(in *oidc.ConfigInput) { in.CreateAccounts = true })
		cb := o.begin(t, "signin", nil)
		for range ipMaxFails {
			o.svc.ipLim.Fail(IPKey(ip1))
		}
		hits := o.p.TokenHits.Load()
		var rl *RateLimitedError
		if _, err := o.svc.OIDCFinish(ctx, cb, ip1, "ua", nil); !errors.As(err, &rl) {
			t.Fatalf("err = %v", err)
		}
		if o.openRows(t) != 1 || o.p.TokenHits.Load() != hits {
			t.Error("a limited address reached the database or the provider")
		}
	})
	t.Run("failed callbacks cost the address", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		for i := range ipMaxFails {
			st := oidc.RandomToken()
			if _, err := o.svc.OIDCFinish(ctx, OIDCCallback{State: st, Cookie: st, Code: "c"}, ip1, "ua", nil); !errors.Is(err, ErrOIDCFailed) {
				t.Fatalf("try %d: %v", i, err)
			}
		}
		var rl *RateLimitedError
		st := oidc.RandomToken()
		if _, err := o.svc.OIDCFinish(ctx, OIDCCallback{State: st, Cookie: st}, ip1, "ua", nil); !errors.As(err, &rl) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("thirty starts per address", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		for i := range oidcStartMax {
			if _, _, err := o.svc.OIDCStart(ctx, "signin", "/", ip1, nil); err != nil {
				t.Fatalf("start %d: %v", i, err)
			}
		}
		var rl *RateLimitedError
		if _, _, err := o.svc.OIDCStart(ctx, "signin", "/", ip1, nil); !errors.As(err, &rl) {
			t.Fatalf("err = %v", err)
		}
		other := netip.MustParseAddr("203.0.113.99")
		if _, _, err := o.svc.OIDCStart(ctx, "signin", "/", other, nil); err != nil {
			t.Fatalf("another address: %v", err)
		}
	})
	t.Run("500 open rows is busy; expired rows are pruned", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		now := o.clk.Now().UnixMilli()
		nonce := strings.Repeat("n", 43)
		for i := range oidcOpenMax {
			o.sql(t, `INSERT INTO oidc_logins (id, state_hash, purpose, issuer, nonce, verifier, created_at, expires_at) VALUES (?, randomblob(32), 'signin', 'x', ?, ?, ?, ?)`,
				"row"+strings.Repeat("0", 3)+string(rune('a'+i%26))+string(rune('a'+i/26%26))+string(rune('a'+i/676)), nonce, nonce, now, now+60_000)
		}
		if _, _, err := o.svc.OIDCStart(ctx, "signin", "/", ip1, nil); !errors.Is(err, ErrOIDCBusy) {
			t.Fatalf("err = %v", err)
		}
		o.clk.Add(2 * time.Minute)
		o.sql(t, `UPDATE oidc_logins SET expires_at = ?`, o.clk.Now().UnixMilli()-1)
		if _, _, err := o.svc.OIDCStart(ctx, "signin", "/", ip1, nil); err != nil {
			t.Fatalf("after expiry: %v", err)
		}
		if o.openRows(t) != 1 {
			t.Errorf("open rows after pruning = %d", o.openRows(t))
		}
	})
}

func TestOIDCLinkFlow(t *testing.T) {
	ctx := context.Background()
	t.Run("link, view, confirm", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		m := o.member(t, "Rosa", users.RoleMember, "", true)
		sess := o.sessionFor(t, m)
		res, err := o.svc.OIDCFinish(ctx, o.begin(t, "link", &sess), ip1, "ua", &sess)
		if err != nil || res.LinkPending == "" || res.Login.Token != "" {
			t.Fatalf("res = %+v, err = %v", res, err)
		}
		if o.identities(t) != 0 {
			t.Fatal("the callback linked without a confirming POST")
		}
		who, err := o.svc.OIDCLinkView(ctx, res.LinkPending, sess)
		if err != nil || who != "ana@example.com" {
			t.Fatalf("view = %q, %v", who, err)
		}
		if err := o.svc.OIDCConfirmLink(ctx, res.LinkPending, sess); err != nil {
			t.Fatal(err)
		}
		id, ok, err := o.svc.OIDCIdentity(ctx, m.ID)
		if err != nil || !ok || id.Subject != "sub-1" || id.LinkedVia != "account" || id.Issuer != o.p.Issuer() {
			t.Fatalf("identity = %+v, %v, %v", id, ok, err)
		}
		if err := o.svc.OIDCConfirmLink(ctx, res.LinkPending, sess); !errors.Is(err, ErrOIDCLinkGone) {
			t.Fatalf("second confirm err = %v", err)
		}
		if o.openRows(t) != 0 {
			t.Error("the link row was kept")
		}
		// A link works for an owner too, and the provider account then signs in to that owner.
		later, err := o.svc.OIDCFinish(ctx, o.begin(t, "signin", nil), ip1, "ua", nil)
		if err != nil || later.Login.User.ID != m.ID {
			t.Fatalf("sign-in after link: %+v, %v", later, err)
		}
	})
	t.Run("callback from another session", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		a := o.sessionFor(t, o.member(t, "Rosa", users.RoleMember, "", true))
		b := o.sessionFor(t, o.member(t, "Ruben", users.RoleMember, "", true))
		cb := o.begin(t, "link", &a)
		if _, err := o.svc.OIDCFinish(ctx, cb, ip1, "ua", &b); !errors.Is(err, ErrOIDCFailed) || o.lastReason(t) != "session" {
			t.Fatalf("err = %v, reason %q", err, o.lastReason(t))
		}
		if o.openRows(t) != 0 {
			t.Error("the row survived")
		}
		cb = o.begin(t, "link", &a)
		if _, err := o.svc.OIDCFinish(ctx, cb, ip1, "ua", nil); !errors.Is(err, ErrOIDCFailed) {
			t.Fatalf("no session err = %v", err)
		}
	})
	t.Run("link callback is single use", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		a := o.sessionFor(t, o.member(t, "Rosa", users.RoleMember, "", true))
		cb := o.begin(t, "link", &a)
		if _, err := o.svc.OIDCFinish(ctx, cb, ip1, "ua", &a); err != nil {
			t.Fatal(err)
		}
		if _, err := o.svc.OIDCFinish(ctx, cb, ip1, "ua", &a); !errors.Is(err, ErrOIDCFailed) {
			t.Fatalf("replay err = %v", err)
		}
	})
	t.Run("confirm by another session", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		a := o.sessionFor(t, o.member(t, "Rosa", users.RoleMember, "", true))
		b := o.sessionFor(t, o.member(t, "Ruben", users.RoleMember, "", true))
		res, err := o.svc.OIDCFinish(ctx, o.begin(t, "link", &a), ip1, "ua", &a)
		if err != nil {
			t.Fatal(err)
		}
		if err := o.svc.OIDCConfirmLink(ctx, res.LinkPending, b); !errors.Is(err, ErrOIDCLinkGone) {
			t.Fatalf("confirm err = %v", err)
		}
		if _, err := o.svc.OIDCLinkView(ctx, res.LinkPending, b); !errors.Is(err, ErrOIDCLinkGone) {
			t.Fatalf("view err = %v", err)
		}
		if o.identities(t) != 0 {
			t.Error("another session linked")
		}
		// The right session can still confirm.
		if err := o.svc.OIDCConfirmLink(ctx, res.LinkPending, a); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("confirm after the step ran out", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		a := o.sessionFor(t, o.member(t, "Rosa", users.RoleMember, "", true))
		res, err := o.svc.OIDCFinish(ctx, o.begin(t, "link", &a), ip1, "ua", &a)
		if err != nil {
			t.Fatal(err)
		}
		o.clk.Add(11 * time.Minute)
		if err := o.svc.OIDCConfirmLink(ctx, res.LinkPending, a); !errors.Is(err, ErrOIDCLinkGone) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("identity of someone else", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		other := o.member(t, "Ruben", users.RoleMember, "", true)
		o.sql(t, `INSERT INTO oidc_identities(issuer, subject, user_id, linked_via, created_at) VALUES (?, 'sub-1', ?, 'account', 1)`, o.p.Issuer(), other.ID)
		a := o.sessionFor(t, o.member(t, "Rosa", users.RoleMember, "", true))
		res, err := o.svc.OIDCFinish(ctx, o.begin(t, "link", &a), ip1, "ua", &a)
		if err != nil {
			t.Fatal(err)
		}
		if err := o.svc.OIDCConfirmLink(ctx, res.LinkPending, a); !errors.Is(err, ErrOIDCTaken) {
			t.Fatalf("err = %v", err)
		}
		if o.count(t, `SELECT count(*) FROM oidc_identities WHERE user_id = ?`, other.ID) != 1 || o.identities(t) != 1 {
			t.Error("identities changed")
		}
	})
	t.Run("person already linked", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		m := o.member(t, "Rosa", users.RoleMember, "", true)
		o.sql(t, `INSERT INTO oidc_identities(issuer, subject, user_id, linked_via, created_at) VALUES (?, 'older-sub', ?, 'account', 1)`, o.p.Issuer(), m.ID)
		a := o.sessionFor(t, m)
		res, err := o.svc.OIDCFinish(ctx, o.begin(t, "link", &a), ip1, "ua", &a)
		if err != nil {
			t.Fatal(err)
		}
		if err := o.svc.OIDCConfirmLink(ctx, res.LinkPending, a); !errors.Is(err, ErrOIDCAlready) {
			t.Fatalf("err = %v", err)
		}
		if id, _, _ := o.svc.OIDCIdentity(ctx, m.ID); id.Subject != "older-sub" {
			t.Errorf("identity replaced: %+v", id)
		}
	})
	t.Run("an owner links from the account page", func(t *testing.T) {
		o := newOIDCEnv(t, nil)
		a := o.sessionFor(t, o.owner)
		res, err := o.svc.OIDCFinish(ctx, o.begin(t, "link", &a), ip1, "ua", &a)
		if err != nil {
			t.Fatal(err)
		}
		if err := o.svc.OIDCConfirmLink(ctx, res.LinkPending, a); err != nil {
			t.Fatal(err)
		}
		later, err := o.signIn(t)
		if err != nil || later.Login.User.ID != o.owner.ID {
			t.Fatalf("owner sign-in: %+v, %v", later, err)
		}
	})
}

func TestOIDCUnlink(t *testing.T) {
	ctx := context.Background()
	o := newOIDCEnv(t, nil)
	noPW := o.member(t, "Pia", users.RoleMember, "", false)
	withPW := o.member(t, "Rosa", users.RoleMember, "", true)
	for i, u := range []users.User{noPW, withPW} {
		o.sql(t, `INSERT INTO oidc_identities(issuer, subject, user_id, linked_via, created_at) VALUES (?, ?, ?, 'account', 1)`, o.p.Issuer(), "s"+string(rune('a'+i)), u.ID)
	}
	if err := o.svc.OIDCUnlink(ctx, noPW.ID); !errors.Is(err, ErrOIDCLastWayIn) {
		t.Fatalf("no password err = %v", err)
	}
	if _, ok, _ := o.svc.OIDCIdentity(ctx, noPW.ID); !ok {
		t.Error("the identity of an account without a password was removed")
	}
	if err := o.svc.OIDCUnlink(ctx, withPW.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := o.svc.OIDCIdentity(ctx, withPW.ID); ok {
		t.Error("still linked")
	}
	if err := o.svc.OIDCUnlink(ctx, withPW.ID); !errors.Is(err, ErrOIDCNotLinked) {
		t.Fatalf("second unlink err = %v", err)
	}
	if o.count(t, `SELECT count(*) FROM audit_log WHERE action = 'auth.oidc_unlinked'`) != 1 {
		t.Error("no unlink audit row")
	}
}

func TestOIDCAuditCarriesNoSecrets(t *testing.T) {
	o := newOIDCEnv(t, func(in *oidc.ConfigInput) { in.CreateAccounts = true; in.MatchEmail = true })
	cb := o.begin(t, "signin", nil)
	o.p.Update(func(p *oidctest.Provider) { p.NonceOverride = "x" })
	_, _ = o.svc.OIDCFinish(context.Background(), cb, ip1, "ua", nil) // fails on the nonce
	o.p.Update(func(p *oidctest.Provider) { p.NonceOverride = "" })
	cb2 := o.begin(t, "signin", nil)
	if _, err := o.svc.OIDCFinish(context.Background(), cb2, ip1, "ua", nil); err != nil {
		t.Fatal(err)
	}
	q := o.p.LastAuthorize()
	secrets := []string{cb.State, cb.Code, cb2.State, cb2.Code, q.Get("nonce"), q.Get("code_challenge"), "at-"}
	rows, err := o.st.Reader().Query(`SELECT action, target, detail_json FROM audit_log`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var a, tg, d string
		if err := rows.Scan(&a, &tg, &d); err != nil {
			t.Fatal(err)
		}
		n++
		for _, s := range secrets {
			if s != "" && strings.Contains(a+tg+d, s) {
				t.Errorf("audit row %s carries %q", a, s)
			}
		}
	}
	if n == 0 {
		t.Fatal("no audit rows")
	}
	if o.count(t, `SELECT count(*) FROM oidc_logins WHERE state_hash = zeroblob(32)`) != 0 {
		t.Error("odd row")
	}
	if o.count(t, `SELECT count(*) FROM oidc_logins`) != 0 {
		t.Error("rows linger")
	}
}

func TestOIDCOnlyStateHashStored(t *testing.T) {
	o := newOIDCEnv(t, nil)
	_, state, err := o.svc.OIDCStart(context.Background(), "signin", "/", ip1, nil)
	if err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := o.st.Reader().QueryRow(`SELECT state_hash FROM oidc_logins`).Scan(&raw); err != nil || len(raw) != 32 {
		t.Fatalf("state_hash: %v len %d", err, len(raw))
	}
	if strings.Contains(string(raw), state) {
		t.Error("raw state stored")
	}
	if o.count(t, `SELECT count(*) FROM oidc_logins WHERE nonce = ? OR verifier = ? OR id = ?`, state, state, state) != 0 {
		t.Error("the state appears in another column")
	}
}
