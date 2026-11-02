package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// withInvites gives the env's service an invites repository and returns it.
func (e *env) withInvites() *share.Invites {
	inv := share.NewInvites(e.st, share.New(e.st), share.NewGrants(e.st), e.users, e.clk.Now)
	e.svc = NewService(e.st, e.users, e.sess, ServiceOptions{SetupCode: testCode, Now: e.clk.Now, Invites: inv})
	return inv
}

func (e *env) adminInvite(t *testing.T, inv *share.Invites, name string) string {
	t.Helper()
	var tok string
	err := e.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		_, tok, err = inv.Create(context.Background(), tx, e.owner, share.InviteSpec{Name: name, Role: users.RoleAdmin})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

func TestAcceptInviteStartsSession(t *testing.T) {
	e := newEnv(t).withOwner(t)
	inv := e.withInvites()
	ctx := context.Background()
	tok := e.adminInvite(t, inv, "Rosa")

	// the browser already had a session: it ends
	old, err := e.svc.Login(ctx, "ana", testPW, ip1, "ua", nil)
	if err != nil {
		t.Fatal(err)
	}
	prev, _, _, err := e.sess.Lookup(ctx, old.Token)
	if err != nil {
		t.Fatal(err)
	}
	l, got, err := e.svc.AcceptInvite(ctx, tok, ip1, "ua", &prev)
	if err != nil {
		t.Fatal(err)
	}
	if l.User.DisplayName != "Rosa" || l.User.Role != users.RoleAdmin || got.UseCount != 1 {
		t.Fatalf("login %+v invite %+v", l.User, got)
	}
	if _, u, _, err := e.sess.Lookup(ctx, l.Token); err != nil || u.ID != l.User.ID {
		t.Fatalf("new session: %v", err)
	}
	if _, _, _, err := e.sess.Lookup(ctx, old.Token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("previous session survived: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'auth.login' AND detail_json LIKE '%invite%'`); n != 1 {
		t.Errorf("login audit rows = %d", n)
	}
	// the same link a second time
	if _, _, err := e.svc.AcceptInvite(ctx, tok, ip1, "ua", nil); !errors.Is(err, share.ErrInviteUsedUp) {
		t.Fatalf("second accept: %v", err)
	}
}

func TestPasswordlessAccountCannotLogInAndSessionSlides(t *testing.T) {
	e := newEnv(t).withOwner(t)
	inv := e.withInvites()
	ctx := context.Background()
	l, _, err := e.svc.AcceptInvite(ctx, e.adminInvite(t, inv, "Rosa"), ip1, "ua", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, pw := range []string{"", testPW, "anything at all"} {
		if _, err := e.svc.Login(ctx, l.User.Username, pw, ip1, "ua", nil); !errors.Is(err, ErrBadCredentials) {
			t.Errorf("password %q: err = %v, want ErrBadCredentials", pw, err)
		}
	}
	// used every few weeks, the cookie never lapses: each hour of use pushes the expiry out
	for i := 0; i < 4; i++ {
		e.clk.Add(20 * 24 * time.Hour)
		sess, _, refreshed, err := e.sess.Lookup(ctx, l.Token)
		if err != nil || !refreshed {
			t.Fatalf("week %d: err %v refreshed %v", i, err, refreshed)
		}
		if want := e.clk.Now().Add(SessionTTL).UnixMilli(); sess.ExpiresAt != want {
			t.Fatalf("expiry %d, want %d", sess.ExpiresAt, want)
		}
	}
	// left alone past the lifetime, it lapses
	e.clk.Add(SessionTTL + time.Hour)
	if _, _, _, err := e.sess.Lookup(ctx, l.Token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("after a lapse: %v", err)
	}
}

func TestAcceptInviteCountsFailuresAndLimits(t *testing.T) {
	e := newEnv(t).withOwner(t)
	inv := e.withInvites()
	ctx := context.Background()
	good := e.adminInvite(t, inv, "Rosa")
	other := netip.MustParseAddr("198.51.100.9")
	unknown, _ := NewToken()
	for i := 0; i < ipMaxFails; i++ {
		bad := unknown
		if i%2 == 0 {
			bad = "not-a-token"
		}
		if _, _, err := e.svc.AcceptInvite(ctx, bad, other, "ua", nil); !errors.Is(err, share.ErrInviteUnknown) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	// the next attempt is limited before the database is read, even with a good link
	var rl *RateLimitedError
	if _, _, err := e.svc.AcceptInvite(ctx, good, other, "ua", nil); !errors.As(err, &rl) || rl.RetryAfter < time.Second {
		t.Fatalf("21st attempt: %v", err)
	}
	if err := e.svc.InviteAllowed(other); !errors.As(err, &rl) {
		t.Fatalf("InviteAllowed: %v", err)
	}
	if n := e.count(t, `SELECT use_count FROM invites`); n != 0 {
		t.Errorf("a limited attempt used the invite")
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'invite.failed' AND detail_json LIKE '%unknown%'`); n != ipMaxFails/2 {
		t.Errorf("unknown audit rows = %d", n)
	}
	// another address is unaffected
	if _, _, err := e.svc.AcceptInvite(ctx, good, ip1, "ua", nil); err != nil {
		t.Fatalf("other address: %v", err)
	}
}
