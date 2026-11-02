package auth

import (
	"context"
	"errors"
	"net/netip"
	"testing"
)

func TestLinkLimiters(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	var rl *RateLimitedError
	other := netip.MustParseAddr("198.51.100.20")

	// ten wrong passwords close the link for everyone, not the address
	for i := 0; i < userMaxFails; i++ {
		if err := e.svc.LinkAllowed(other, "lnk_a"); err != nil {
			t.Fatalf("attempt %d: %v", i, err)
		}
		e.svc.LinkFailed(ctx, other, "lnk_a", "password")
	}
	if err := e.svc.LinkAllowed(ip1, "lnk_a"); !errors.As(err, &rl) {
		t.Fatalf("link not closed: %v", err)
	}
	if err := e.svc.LinkAllowed(ip1, "lnk_b"); err != nil {
		t.Fatalf("another link closed: %v", err)
	}
	if err := e.svc.LinkAllowed(ip1, ""); err != nil {
		t.Fatalf("a clean address closed: %v", err)
	}
	e.svc.LinkUnlocked("lnk_a")
	if err := e.svc.LinkAllowed(ip1, "lnk_a"); err != nil {
		t.Fatalf("unlock did not reopen: %v", err)
	}

	// twenty failures close the address, whatever the link
	for i := 0; i < ipMaxFails; i++ {
		e.svc.LinkFailed(ctx, ip1, "", "unknown")
	}
	if err := e.svc.LinkAllowed(ip1, ""); !errors.As(err, &rl) {
		t.Fatalf("address not closed: %v", err)
	}
	if err := e.svc.LinkAllowed(ip1, "lnk_b"); !errors.As(err, &rl) {
		t.Fatalf("address not closed with a link: %v", err)
	}
	// the same limiter guards sign-in
	if err := e.svc.InviteAllowed(ip1); !errors.As(err, &rl) {
		t.Fatalf("limiter not shared: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'link.failed'`); n != userMaxFails+ipMaxFails {
		t.Errorf("audit rows = %d", n)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'link.failed' AND detail_json LIKE '%lnk_a%'`); n != 0 {
		t.Errorf("link id in audit detail")
	}
}
