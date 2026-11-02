package auth

import (
	"context"
	"net/netip"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

// linkKey is the per-link key in the user limiter: ten wrong passwords in 15 minutes close one
// link, whatever address they come from.
func linkKey(linkID string) string { return "link:" + linkID }

// LinkAllowed is the cheap check before a share link is looked at: *RateLimitedError when this
// address has failed too often (links share the sign-in limiter) or, with a linkID, when that link
// has had too many wrong passwords. Pass "" before the link is known.
func (a *Service) LinkAllowed(ip netip.Addr, linkID string) error {
	if err := limited(a.ipLim.Allow(IPKey(ip))); err != nil {
		return err
	}
	if linkID == "" {
		return nil
	}
	return limited(a.userLim.Allow(linkKey(linkID)))
}

// LinkFailed counts one failed attempt: an unknown or broken token ("unknown", "broken": the address
// only, linkID "") or a wrong password ("password": the address and the link). The audit row holds
// the address and the reason, never the token.
func (a *Service) LinkFailed(ctx context.Context, ip netip.Addr, linkID, reason string) {
	a.ipLim.Fail(IPKey(ip))
	if linkID != "" {
		a.userLim.Fail(linkKey(linkID))
	}
	a.audit(ctx, db.AuditEntry{Action: "link.failed", Detail: map[string]string{"ip": ip.String(), "reason": reason}})
}

// LinkUnlocked forgets the wrong passwords counted against the link.
func (a *Service) LinkUnlocked(linkID string) { a.userLim.Reset(linkKey(linkID)) }
