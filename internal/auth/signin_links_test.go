package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

// person makes a member; withPassword false leaves the account without one, as an invite does.
func (e *env) person(t *testing.T, name string, role users.Role, withPassword bool) users.User {
	t.Helper()
	ctx := context.Background()
	hash := ""
	if withPassword {
		var err error
		if hash, err = e.users.HashPassword(ctx, testPW); err != nil {
			t.Fatal(err)
		}
	}
	var u users.User
	err := e.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		u, err = e.users.CreateInvited(ctx, tx, users.NewUser{DisplayName: name, Role: role}, hash)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return u
}

// link makes a sign-in link for userID by the owner.
func (e *env) link(t *testing.T, userID string) string {
	t.Helper()
	ctx := context.Background()
	var tok string
	err := e.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		tok, _, err = e.svc.MakeSignInLink(ctx, tx, e.owner.ID, userID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func (e *env) failedReasons(t *testing.T, reason string) int {
	t.Helper()
	return e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'auth.signin_link_failed' AND detail_json LIKE ?`, `%"reason":"`+reason+`"%`)
}

func (e *env) spend(t *testing.T, tok string) (Login, bool) {
	t.Helper()
	l, has, err := e.svc.SpendSignInLink(context.Background(), tok, ip1, "ua", nil)
	if err != nil {
		t.Fatalf("spend: %v", err)
	}
	return l, has
}

func (e *env) sessionOf(t *testing.T, tok string) Session {
	t.Helper()
	s, _, _, err := e.sess.Lookup(context.Background(), tok)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestSignInLinkSpendOnce(t *testing.T) {
	e := newEnv(t).withOwner(t)
	p := e.person(t, "Rosa", users.RoleMember, true)
	tok := e.link(t, p.ID)
	l, has := e.spend(t, tok)
	if l.User.ID != p.ID || !has {
		t.Fatalf("login %+v has=%v", l.User, has)
	}
	if _, _, err := e.svc.SpendSignInLink(context.Background(), tok, ip1, "ua", nil); !errors.Is(err, ErrSignInLinkBroken) {
		t.Fatalf("second spend: %v", err)
	}
	if e.failedReasons(t, "used") != 1 {
		t.Error("the second spend was not audited as used")
	}
}

func TestSignInLinkExpired(t *testing.T) {
	e := newEnv(t).withOwner(t)
	p := e.person(t, "Rosa", users.RoleMember, true)
	tok := e.link(t, p.ID)
	e.clk.Add(SignInLinkTTL)
	if _, _, err := e.svc.SpendSignInLink(context.Background(), tok, ip1, "ua", nil); !errors.Is(err, ErrSignInLinkBroken) {
		t.Fatalf("at the deadline: %v", err)
	}
	if e.failedReasons(t, "expired") != 1 {
		t.Error("expiry not audited")
	}
	// one millisecond before the end it still works
	tok2 := e.link(t, p.ID)
	e.clk.Add(SignInLinkTTL - time.Millisecond)
	e.spend(t, tok2)
}

func TestSignInLinkNewRevokesOld(t *testing.T) {
	e := newEnv(t).withOwner(t)
	p := e.person(t, "Rosa", users.RoleMember, true)
	old := e.link(t, p.ID)
	l, _ := e.spend(t, old)
	if ok, _ := e.svc.SignInBound(context.Background(), e.sessionOf(t, l.Token)); !ok {
		t.Fatal("the link session is not bound")
	}
	fresh := e.link(t, p.ID)
	if _, _, err := e.svc.SpendSignInLink(context.Background(), old, ip1, "ua", nil); !errors.Is(err, ErrSignInLinkBroken) {
		t.Fatalf("old link: %v", err)
	}
	if ok, _ := e.svc.SignInBound(context.Background(), e.sessionOf(t, l.Token)); ok {
		t.Error("the old binding survived a new link")
	}
	if _, err := e.svc.CheckSignInLink(context.Background(), fresh, ip1); err != nil {
		t.Errorf("new link: %v", err)
	}
}

func TestSignInLinkRevokedOnDisable(t *testing.T) {
	e := newEnv(t).withOwner(t)
	p := e.person(t, "Rosa", users.RoleMember, true)
	tok := e.link(t, p.ID)
	ctx := context.Background()
	err := e.st.WriteTx(ctx, func(tx *sql.Tx) error {
		n, err := e.svc.RevokeSignInLinks(ctx, tx, p.ID, "disabled")
		if n != 1 {
			t.Errorf("revoked %d", n)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.CheckSignInLink(ctx, tok, ip1); !errors.Is(err, ErrSignInLinkBroken) {
		t.Fatalf("revoked link: %v", err)
	}
	if e.failedReasons(t, "revoked") != 1 {
		t.Error("revocation not audited as a reason")
	}
	if e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'user.signin_link_revoked' AND detail_json LIKE '%disabled%'`) != 1 {
		t.Error("revoke audit row missing")
	}
	// nothing to revoke writes no audit row
	err = e.st.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := e.svc.RevokeSignInLinks(ctx, tx, p.ID, "role")
		return err
	})
	if err != nil || e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'user.signin_link_revoked'`) != 1 {
		t.Errorf("empty revoke audited: %v", err)
	}
}

func TestSignInLinkDisabledAccount(t *testing.T) {
	e := newEnv(t).withOwner(t)
	p := e.person(t, "Rosa", users.RoleMember, true)
	tok := e.link(t, p.ID)
	if _, err := e.st.Writer().Exec(`UPDATE users SET disabled = 1 WHERE id = ?`, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.SpendSignInLink(context.Background(), tok, ip1, "ua", nil); !errors.Is(err, ErrSignInLinkBroken) {
		t.Fatalf("disabled: %v", err)
	}
	if e.failedReasons(t, "disabled") != 1 {
		t.Error("disabled not audited")
	}
	if e.count(t, `SELECT count(*) FROM sign_in_links WHERE used_at IS NOT NULL`) != 0 {
		t.Error("a refused spend marked the link used")
	}
}

func TestSignInLinkOwnerRowRefused(t *testing.T) {
	e := newEnv(t).withOwner(t)
	tok, hash := NewToken()
	h, _ := HashToken(tok)
	_ = hash
	now := e.clk.Now().UnixMilli()
	if _, err := e.st.Writer().Exec(`INSERT INTO sign_in_links (id, token_hash, user_id, created_at, expires_at) VALUES ('x', ?, ?, ?, ?)`,
		h[:], e.owner.ID, now, now+1000); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.svc.SpendSignInLink(context.Background(), tok, ip1, "ua", nil); !errors.Is(err, ErrSignInLinkBroken) {
		t.Fatalf("owner: %v", err)
	}
	if e.failedReasons(t, "owner") != 1 {
		t.Error("owner not audited")
	}
}

func TestSignInLinkMalformedNoRead(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	if _, _, err := e.svc.SpendSignInLink(ctx, "not-a-token", ip1, "ua", nil); !errors.Is(err, ErrSignInLinkBroken) {
		t.Fatalf("spend: %v", err)
	}
	if _, err := e.svc.CheckSignInLink(ctx, "", ip1); !errors.Is(err, ErrSignInLinkBroken) {
		t.Fatalf("check: %v", err)
	}
	if e.failedReasons(t, "malformed") != 2 {
		t.Error("malformed attempts not audited")
	}
	unknown, _ := NewToken()
	if _, err := e.svc.CheckSignInLink(ctx, unknown, ip1); !errors.Is(err, ErrSignInLinkBroken) {
		t.Fatalf("unknown: %v", err)
	}
	if e.failedReasons(t, "unknown") != 1 {
		t.Error("unknown not audited")
	}
}

func TestSignInLinkLimiter(t *testing.T) {
	e := newEnv(t).withOwner(t)
	p := e.person(t, "Rosa", users.RoleMember, true)
	good := e.link(t, p.ID)
	ctx := context.Background()
	other := netip.MustParseAddr("198.51.100.9")
	for i := 0; i < ipMaxFails; i++ {
		if _, err := e.svc.CheckSignInLink(ctx, "bad", other); !errors.Is(err, ErrSignInLinkBroken) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	var rl *RateLimitedError
	if _, err := e.svc.CheckSignInLink(ctx, "bad", other); !errors.As(err, &rl) {
		t.Fatalf("21st attempt: %v", err)
	}
	if _, err := e.svc.CheckSignInLink(ctx, good, other); !errors.As(err, &rl) {
		t.Fatalf("good link from a limited address: %v", err)
	}
	if _, _, err := e.svc.SpendSignInLink(ctx, good, other, "ua", nil); !errors.As(err, &rl) {
		t.Fatalf("spend from a limited address: %v", err)
	}
	if e.count(t, `SELECT count(*) FROM sign_in_links WHERE used_at IS NOT NULL`) != 0 {
		t.Error("a limited attempt spent the link")
	}
	if _, err := e.svc.CheckSignInLink(ctx, good, ip1); err != nil {
		t.Errorf("another address: %v", err)
	}
}

func TestSignInLinkCheckDoesNotSpend(t *testing.T) {
	e := newEnv(t).withOwner(t)
	p := e.person(t, "Rosa", users.RoleMember, true)
	tok := e.link(t, p.ID)
	for i := 0; i < 3; i++ {
		name, err := e.svc.CheckSignInLink(context.Background(), tok, ip1)
		if err != nil || name != "Rosa" {
			t.Fatalf("check %d: %q %v", i, name, err)
		}
	}
	if e.count(t, `SELECT count(*) FROM sign_in_links WHERE used_at IS NOT NULL OR session_hash IS NOT NULL`) != 0 {
		t.Error("a check changed the link")
	}
	if e.count(t, `SELECT count(*) FROM sessions WHERE user_id = ?`, p.ID) != 0 {
		t.Error("a check started a session")
	}
	e.spend(t, tok) // still works
}

func TestSignInLinkSpendRace(t *testing.T) {
	e := newEnv(t).withOwner(t)
	p := e.person(t, "Rosa", users.RoleMember, true)
	tok := e.link(t, p.ID)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok := 0
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := e.svc.SpendSignInLink(context.Background(), tok, ip1, "ua", nil); err == nil {
				mu.Lock()
				ok++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if ok != 1 {
		t.Fatalf("%d spends succeeded", ok)
	}
	if e.count(t, `SELECT count(*) FROM sessions WHERE user_id = ?`, p.ID) != 1 {
		t.Error("more than one session started")
	}
}

func TestSignInLinkEndsPrevSession(t *testing.T) {
	e := newEnv(t).withOwner(t)
	p := e.person(t, "Rosa", users.RoleMember, true)
	ctx := context.Background()
	old, err := e.svc.Login(ctx, "ana", testPW, ip1, "ua", nil)
	if err != nil {
		t.Fatal(err)
	}
	prev := e.sessionOf(t, old.Token)
	tok := e.link(t, p.ID)
	l, _, err := e.svc.SpendSignInLink(ctx, tok, ip1, "ua", &prev)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := e.sess.Lookup(ctx, old.Token); !errors.Is(err, ErrNoSession) {
		t.Errorf("previous session survived: %v", err)
	}
	if _, u, _, err := e.sess.Lookup(ctx, l.Token); err != nil || u.ID != p.ID {
		t.Errorf("new session: %v", err)
	}
}

func TestSignInBound(t *testing.T) {
	e := newEnv(t).withOwner(t)
	p := e.person(t, "Rosa", users.RoleMember, true)
	ctx := context.Background()
	l, _ := e.spend(t, e.link(t, p.ID))
	linkSess := e.sessionOf(t, l.Token)
	// an ordinary session of the same person
	var ordinary Session
	if err := e.st.WriteTx(ctx, func(tx *sql.Tx) error {
		_, s, err := e.sess.Create(ctx, tx, p.ID, "ua")
		ordinary = s
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if ok, err := e.svc.SignInBound(ctx, linkSess); err != nil || !ok {
		t.Fatalf("link session: %v %v", ok, err)
	}
	if ok, _ := e.svc.SignInBound(ctx, ordinary); ok {
		t.Error("an ordinary session is bound")
	}
	dev := linkSess
	dev.DeviceID = "dev1"
	if ok, _ := e.svc.SignInBound(ctx, dev); ok {
		t.Error("a device-bound session is bound")
	}
	e.clk.Add(SignInResetWindow + time.Millisecond)
	if ok, _ := e.svc.SignInBound(ctx, linkSess); ok {
		t.Error("still bound after the window")
	}
	// revoke: bound again for a fresh link, then revoked
	e.clk.Add(-SignInResetWindow - time.Millisecond)
	if err := e.st.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := e.svc.RevokeSignInLinks(ctx, tx, p.ID, "role")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if ok, _ := e.svc.SignInBound(ctx, linkSess); ok {
		t.Error("bound after revoke")
	}
}

func TestSetPasswordFromLink(t *testing.T) {
	e := newEnv(t).withOwner(t)
	p := e.person(t, "Rosa", users.RoleMember, true)
	ctx := context.Background()
	// an older session and a device token of Rosa
	var oldTok string
	if err := e.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		oldTok, _, err = e.sess.Create(ctx, tx, p.ID, "ua")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	var devTok string
	if err := e.st.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE users SET username = 'rosa' WHERE id = ?`, p.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	d, err := e.svc.IssueDevice(ctx, "rosa", testPW, "", KindWeb, ip1)
	if err != nil {
		t.Fatal(err)
	}
	devTok = d.Token

	l, _ := e.spend(t, e.link(t, p.ID))
	sess := e.sessionOf(t, l.Token)
	if _, err := e.svc.SetPasswordFromLink(ctx, sess, "short", ip1); err == nil {
		t.Fatal("a short password was accepted")
	} else {
		var fe users.FieldErrors
		if !errors.As(err, &fe) || fe["password"] == "" {
			t.Fatalf("short password: %v", err)
		}
	}
	next := "a brand new password 42"
	newTok, err := e.svc.SetPasswordFromLink(ctx, sess, next, ip1)
	if err != nil {
		t.Fatal(err)
	}
	for name, tok := range map[string]string{"older session": oldTok, "link session": l.Token} {
		if _, _, _, err := e.sess.Lookup(ctx, tok); !errors.Is(err, ErrNoSession) {
			t.Errorf("%s survived: %v", name, err)
		}
	}
	if _, _, err := e.dev.Lookup(ctx, devTok); !errors.Is(err, ErrNoDevice) {
		t.Errorf("device token survived: %v", err)
	}
	fresh := e.sessionOf(t, newTok)
	if ok, _ := e.svc.SignInBound(ctx, fresh); ok {
		t.Error("the new session is bound")
	}
	if _, err := e.svc.SetPasswordFromLink(ctx, fresh, "another password 99", ip1); !errors.Is(err, ErrNotLinkSession) {
		t.Errorf("second call: %v", err)
	}
	if _, err := e.svc.Login(ctx, "rosa", next, ip1, "ua", nil); err != nil {
		t.Errorf("login with the new password: %v", err)
	}
	if _, err := e.svc.Login(ctx, "rosa", testPW, ip1, "ua", nil); err == nil {
		t.Error("the old password still works")
	}
}

func TestSetPasswordFromLinkNoPassword(t *testing.T) {
	e := newEnv(t).withOwner(t)
	p := e.person(t, "Rosa", users.RoleMember, false)
	ctx := context.Background()
	l, has := e.spend(t, e.link(t, p.ID))
	if has {
		t.Fatal("hasPassword is true for a person without one")
	}
	_, err := e.svc.SetPasswordFromLink(ctx, e.sessionOf(t, l.Token), "a brand new password 42", ip1)
	if !errors.Is(err, ErrNoPassword) {
		t.Fatalf("got %v", err)
	}
}

func TestSignInLinkAudit(t *testing.T) {
	e := newEnv(t).withOwner(t)
	p := e.person(t, "Rosa", users.RoleMember, true)
	ctx := context.Background()
	old := e.link(t, p.ID)
	tok := e.link(t, p.ID) // revokes the first: user.signin_link_revoked
	if _, err := e.svc.CheckSignInLink(ctx, old, ip1); err == nil {
		t.Fatal("old link worked")
	}
	l, _ := e.spend(t, tok)
	if _, err := e.svc.SetPasswordFromLink(ctx, e.sessionOf(t, l.Token), "a brand new password 42", ip1); err != nil {
		t.Fatal(err)
	}
	for _, a := range []string{"user.signin_link", "auth.login", "auth.signin_link_failed", "auth.password_reset", "user.signin_link_revoked"} {
		if e.count(t, `SELECT count(*) FROM audit_log WHERE action = ?`, a) == 0 {
			t.Errorf("no %s row", a)
		}
	}
	if e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'auth.login' AND detail_json LIKE '%sign_in_link%'`) != 1 {
		t.Error("login via sign_in_link not recorded")
	}
	if e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'auth.password_reset' AND detail_json LIKE '%sign_in_link%'`) != 1 {
		t.Error("password reset not recorded")
	}
	rows, err := e.st.Reader().Query(`SELECT action, coalesce(user_id,''), coalesce(target,''), coalesce(detail_json,'') FROM audit_log`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var a, u, tg, d string
		if err := rows.Scan(&a, &u, &tg, &d); err != nil {
			t.Fatal(err)
		}
		for _, raw := range []string{old, tok} {
			if strings.Contains(a+u+tg+d, raw) {
				t.Errorf("audit row %s holds a raw token", a)
			}
		}
	}
}
