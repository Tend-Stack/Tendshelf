package auth

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

// manageLogin signs ana in with the given user agent and returns the session and its token.
func (e *env) manageLogin(t *testing.T, ua string) (Session, string) {
	t.Helper()
	l, err := e.svc.Login(context.Background(), "ana", testPW, ip1, ua, nil)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	h, _ := HashToken(l.Token)
	sess, _, _, err := e.sess.Lookup(context.Background(), l.Token)
	if err != nil {
		t.Fatal(err)
	}
	if sess.Hash != h {
		t.Fatal("lookup returned another session")
	}
	return sess, l.Token
}

func TestListForUserMarksCurrent(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	e.clk.Add(time.Minute)
	a, _ := e.manageLogin(t, "Agent A")
	e.clk.Add(time.Minute)
	b, _ := e.manageLogin(t, "Agent B")
	list, err := e.sess.ListForUser(ctx, e.owner.ID, a.Hash)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 { // the setup session and two sign-ins
		t.Fatalf("len = %d", len(list))
	}
	if list[0].PublicID != PublicID(b.Hash) || list[0].Current || list[0].UserAgent != "Agent B" {
		t.Fatalf("newest first: %+v", list[0])
	}
	cur := 0
	for _, si := range list {
		if len(si.PublicID) != PublicIDLen {
			t.Fatalf("public id %q", si.PublicID)
		}
		if si.Current {
			cur++
			if si.PublicID != PublicID(a.Hash) {
				t.Fatalf("wrong current: %+v", si)
			}
		}
	}
	if cur != 1 {
		t.Fatalf("current marks = %d", cur)
	}
	// Expired sessions are not listed.
	e.clk.Add(SessionTTL + time.Hour)
	if list, err := e.sess.ListForUser(ctx, e.owner.ID, a.Hash); err != nil || len(list) != 0 {
		t.Fatalf("expired listed: %d, %v", len(list), err)
	}
}

func TestDeleteByPublicIDOwnRowsOnly(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	a, _ := e.manageLogin(t, "A")
	// A second person with a session.
	other := users.User{ID: "usr_other"}
	if _, err := e.st.Writer().ExecContext(ctx, `INSERT INTO users (id, username, display_name, role, password_hash, disabled, created_at)
		VALUES (?, 'bea', 'Bea', 'member', 'x', 0, 1)`, other.ID); err != nil {
		t.Fatal(err)
	}
	tok, osess, err := e.sess.Create(ctx, e.st.Writer(), other.ID, "B")
	_ = tok
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{PublicID(osess.Hash), "", "xyz", strings.Repeat("g", 16), strings.ToUpper(PublicID(a.Hash)), PublicID(a.Hash)[:15], PublicID(a.Hash) + "0"} {
		if _, ok, err := e.sess.DeleteByPublicID(ctx, e.owner.ID, id); err != nil || ok {
			t.Fatalf("id %q: ok=%v err=%v", id, ok, err)
		}
	}
	if n := e.count(t, `SELECT COUNT(*) FROM sessions WHERE user_id = ?`, other.ID); n != 1 {
		t.Fatalf("other person's session count = %d", n)
	}
	got, ok, err := e.sess.DeleteByPublicID(ctx, e.owner.ID, PublicID(a.Hash))
	if err != nil || !ok || got != a.Hash {
		t.Fatalf("own session: %v %v %x", ok, err, got)
	}
	if e.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'auth.session_signed_out'`) != 1 {
		t.Fatal("no audit row")
	}
	if _, ok, _ := e.sess.DeleteByPublicID(ctx, e.owner.ID, PublicID(a.Hash)); ok {
		t.Fatal("deleted twice")
	}
}

func TestSignOutOthersKeepsCurrent(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	cur, _ := e.manageLogin(t, "A")
	e.manageLogin(t, "B")
	n, err := e.svc.SignOutOthers(ctx, cur)
	if err != nil || n != 2 {
		t.Fatalf("ended %d, %v", n, err)
	}
	list, _ := e.sess.ListForUser(ctx, e.owner.ID, cur.Hash)
	if len(list) != 1 || !list[0].Current {
		t.Fatalf("after: %+v", list)
	}
}

func TestChangePassword(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	cur, curTok := e.manageLogin(t, "Agent A")
	_, otherTok := e.manageLogin(t, "Agent B")
	const next = "a brand new passphrase"

	// Wrong current password.
	if _, err := e.svc.ChangePassword(ctx, cur, "not it at all", next, ip1); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("wrong current = %v", err)
	}
	if e.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'auth.password_change_failed'`) != 1 {
		t.Fatal("no failure audit row")
	}
	// Weak new password: only the password field.
	_, err := e.svc.ChangePassword(ctx, cur, testPW, "short", ip1)
	var fe users.FieldErrors
	if !errors.As(err, &fe) || fe["password"] != "too_short" || len(fe) != 1 {
		t.Fatalf("weak new = %v", err)
	}
	if _, err := e.svc.ChangePassword(ctx, cur, testPW, "ANA", ip1); !errors.As(err, &fe) {
		t.Fatalf("short = %v", err)
	}

	tok, err := e.svc.ChangePassword(ctx, cur, testPW, next, ip1)
	if err != nil || tok == "" {
		t.Fatalf("change = %q, %v", tok, err)
	}
	if _, err := e.svc.Login(ctx, "ana", next, ip1, "ua", nil); err != nil {
		t.Fatalf("new password refused: %v", err)
	}
	if _, err := e.svc.Login(ctx, "ana", testPW, ip1, "ua", nil); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("old password accepted: %v", err)
	}
	// Every other session is gone; the old token of this browser is replaced by a new one.
	if _, _, _, err := e.sess.Lookup(ctx, otherTok); !errors.Is(err, ErrNoSession) {
		t.Fatalf("other session lives: %v", err)
	}
	if _, _, _, err := e.sess.Lookup(ctx, curTok); !errors.Is(err, ErrNoSession) {
		t.Fatalf("old token lives: %v", err)
	}
	ns, _, _, err := e.sess.Lookup(ctx, tok)
	if err != nil || ns.UserAgent != "Agent A" {
		t.Fatalf("new session: %+v, %v", ns, err)
	}
	if e.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'auth.password_changed'`) != 1 {
		t.Fatal("no change audit row")
	}
	var all string
	_ = e.st.Reader().QueryRow(`SELECT group_concat(detail_json || target || action) FROM audit_log`).Scan(&all)
	for _, secret := range []string{testPW, next, "not it at all", tok} {
		if strings.Contains(all, secret) {
			t.Fatalf("audit log contains a secret %q", secret)
		}
	}
}

func TestChangePasswordRateLimit(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	cur, _ := e.manageLogin(t, "A")
	for i := 0; i < userMaxFails; i++ {
		if _, err := e.svc.ChangePassword(ctx, cur, "nope nope nope", "another new password", ip1); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("try %d = %v", i, err)
		}
	}
	var rl *RateLimitedError
	// Even the right password is refused while limited.
	if _, err := e.svc.ChangePassword(ctx, cur, testPW, "another new password", ip1); !errors.As(err, &rl) {
		t.Fatalf("after %d fails = %v", userMaxFails, err)
	}
}

func TestResetPassword(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	e.manageLogin(t, "A")
	e.manageLogin(t, "B")
	pw, err := e.svc.ResetPassword(ctx, " ANA ")
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{4}(-[0-9A-HJKMNP-TV-Z]{4}){3}$`).MatchString(pw) {
		t.Fatalf("format %q", pw)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM sessions`); n != 0 {
		t.Fatalf("sessions left = %d", n)
	}
	if _, err := e.svc.Login(ctx, "ana", testPW, ip1, "ua", nil); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("old password accepted: %v", err)
	}
	if _, err := e.svc.Login(ctx, "ana", pw, ip1, "ua", nil); err != nil {
		t.Fatalf("new password refused: %v", err)
	}
	var detail string
	if err := e.st.Reader().QueryRow(`SELECT detail_json FROM audit_log WHERE action = 'auth.password_reset'`).Scan(&detail); err != nil {
		t.Fatal(err)
	}
	if detail != `{"by":"cli"}` {
		t.Fatalf("detail %q", detail)
	}
	var all string
	_ = e.st.Reader().QueryRow(`SELECT group_concat(detail_json || target || action) FROM audit_log`).Scan(&all)
	if strings.Contains(all, pw) {
		t.Fatal("audit log contains the generated password")
	}
	if _, err := e.svc.ResetPassword(ctx, "nobody"); !errors.Is(err, users.ErrNotFound) {
		t.Fatalf("unknown user = %v", err)
	}
	pw2, _ := e.svc.ResetPassword(ctx, "ana")
	if pw2 == pw {
		t.Fatal("two resets gave the same password")
	}
}
