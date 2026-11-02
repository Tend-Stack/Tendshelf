package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/token"
)

// InviteAllowed is the cheap check before an invite link is looked at: it answers
// *RateLimitedError when this address has failed too often. Invite links share the sign-in limiter
// (20 failures per 15 minutes per address), so guessing links costs the same as guessing passwords.
func (a *Service) InviteAllowed(ip netip.Addr) error {
	return limited(a.ipLim.Allow(IPKey(ip)))
}

// InviteFailed counts one failed invite attempt against the address and records it in the audit
// log. The token is never recorded; reason is a short code such as "unknown" or "malformed".
func (a *Service) InviteFailed(ctx context.Context, ip netip.Addr, reason string) {
	a.ipLim.Fail(IPKey(ip))
	a.audit(ctx, db.AuditEntry{Action: "invite.failed", Detail: map[string]string{"ip": ip.String(), "reason": reason}})
}

// AcceptInvite turns the invite named by tok into an account and signs it in, in one transaction.
// prev, when not nil, is the session the browser already had; it is ended. The account has no
// password. Unknown, malformed, revoked, refused and empty invites are share.ErrInviteUnknown,
// ErrInviteRefused and ErrInviteNoAccess (each costs the address one failure), expired and used-up
// invites their own errors, and a limited address gets *RateLimitedError before the database is read.
func (a *Service) AcceptInvite(ctx context.Context, tok string, ip netip.Addr, ua string, prev *Session) (Login, share.Invite, error) {
	if a.invites == nil {
		return Login{}, share.Invite{}, share.ErrInviteUnknown
	}
	if err := a.InviteAllowed(ip); err != nil {
		return Login{}, share.Invite{}, err
	}
	hash, ok := token.Hash(tok)
	if !ok {
		a.InviteFailed(ctx, ip, "malformed")
		return Login{}, share.Invite{}, share.ErrInviteUnknown
	}
	var out Login
	var inv share.Invite
	err := a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := a.sessions.DeleteExpired(ctx, tx); err != nil {
			return err
		}
		u, got, err := a.invites.Accept(ctx, tx, hash, "")
		if err != nil {
			return err
		}
		if prev != nil {
			if err := a.sessions.Delete(ctx, tx, prev.Hash); err != nil {
				return err
			}
		}
		tok, _, err := a.sessions.Create(ctx, tx, u.ID, ua)
		if err != nil {
			return err
		}
		now := a.ms()
		if err := a.users.TouchLastSeen(ctx, tx, u.ID, now); err != nil {
			return err
		}
		u.LastSeenAt = now
		out, inv = Login{Token: tok, User: u}, got
		return db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: u.ID, Action: "auth.login", Target: u.ID,
			Detail: map[string]string{"ip": ip.String(), "via": "invite"}})
	})
	switch {
	case err == nil:
		return out, inv, nil
	case errors.Is(err, share.ErrInviteUnknown):
		a.InviteFailed(ctx, ip, "unknown")
	case errors.Is(err, share.ErrInviteRefused):
		a.InviteFailed(ctx, ip, "refused")
	case errors.Is(err, share.ErrInviteNoAccess):
		a.InviteFailed(ctx, ip, "no_access")
	}
	return Login{}, share.Invite{}, err
}
