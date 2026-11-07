package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/token"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// Sign-in links (decision 0030): a one-time address an admin makes so a person without a password,
// or who forgot it, can sign in and choose a new one.
const (
	// SignInLinkTTL is how long a link works: long enough to hand over by message the same day.
	SignInLinkTTL = 24 * time.Hour
	// SignInResetWindow is how long after spending a link its session may set a password without
	// knowing the old one.
	SignInResetWindow = time.Hour
	// signInLinkKeep is how long spent, revoked and expired rows stay before they are pruned.
	signInLinkKeep = 48 * time.Hour
)

var (
	// ErrSignInLinkBroken is every way a link can fail; callers show one page for all of them.
	ErrSignInLinkBroken = errors.New("auth: sign-in link does not work")
	// ErrNotLinkSession means the session is not the one a live sign-in link started.
	ErrNotLinkSession = errors.New("auth: this session did not start from a sign-in link")
	// ErrNoPassword means the account has no password yet; it chooses one on /welcome instead.
	ErrNoPassword = errors.New("auth: the account has no password yet")
)

// brokenLink carries the reason out of a transaction so the failure is audited after rollback.
type brokenLink string

func (b brokenLink) Error() string { return "auth: sign-in link " + string(b) }

// MakeSignInLink revokes the person's earlier links, stores a new one and returns the raw token
// (shown once) and when it stops working. The caller has already re-checked that byID manages userID.
func (a *Service) MakeSignInLink(ctx context.Context, tx *sql.Tx, byID, userID string) (string, int64, error) {
	now := a.now()
	if _, err := tx.ExecContext(ctx, `DELETE FROM sign_in_links WHERE created_at < ?`, now.Add(-signInLinkKeep).UnixMilli()); err != nil {
		return "", 0, err
	}
	if _, err := a.RevokeSignInLinks(ctx, tx, userID, "new_link"); err != nil {
		return "", 0, err
	}
	tok, hash := token.New()
	expires := now.Add(SignInLinkTTL).UnixMilli()
	if _, err := tx.ExecContext(ctx, `INSERT INTO sign_in_links (id, token_hash, user_id, created_by, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?, ?)`, db.NewID("sl"), hash[:], userID, byID, now.UnixMilli(), expires); err != nil {
		return "", 0, err
	}
	err := db.Audit(ctx, tx, db.AuditEntry{At: now.UnixMilli(), UserID: byID, Action: "user.signin_link", Target: "user:" + userID,
		Detail: map[string]int64{"expires_at": expires}})
	if err != nil {
		return "", 0, err
	}
	return tok, expires, nil
}

// RevokeSignInLinks ends every link of the person that is not revoked, including a spent one (which
// clears the password step it unlocked). why is "new_link", "disabled" or "role". It audits only when
// it touched rows.
func (a *Service) RevokeSignInLinks(ctx context.Context, ex db.Execer, userID, why string) (int64, error) {
	now := a.ms()
	res, err := ex.ExecContext(ctx, `UPDATE sign_in_links SET revoked_at = ?, session_hash = NULL WHERE user_id = ? AND revoked_at IS NULL`, now, userID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return 0, err
	}
	return n, db.Audit(ctx, ex, db.AuditEntry{At: now, Action: "user.signin_link_revoked", Target: "user:" + userID,
		Detail: map[string]string{"why": why}})
}

// PendingSignInLink is the person's open, unexpired link: when it stops working.
func (a *Service) PendingSignInLink(ctx context.Context, userID string) (int64, bool, error) {
	var exp int64
	err := a.st.Reader().QueryRowContext(ctx, `SELECT expires_at FROM sign_in_links
		WHERE user_id = ? AND used_at IS NULL AND revoked_at IS NULL AND expires_at > ?`, userID, a.ms()).Scan(&exp)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return exp, true, nil
}

type linkRow struct {
	id, userID, name, username, display, role string
	expires                                   int64
	usedAt, revokedAt                         sql.NullInt64
	disabledInt                               int
}

// readLink finds the link for hash and decides why it cannot be used ("" when it can).
func (a *Service) readLink(ctx context.Context, q db.RowQueryer, hash [32]byte) (linkRow, string) {
	var r linkRow
	err := q.QueryRowContext(ctx, `SELECT l.id, l.user_id, l.expires_at, l.used_at, l.revoked_at, u.username, u.display_name, u.role, u.disabled
		FROM sign_in_links l JOIN users u ON u.id = l.user_id WHERE l.token_hash = ?`, hash[:]).
		Scan(&r.id, &r.userID, &r.expires, &r.usedAt, &r.revokedAt, &r.username, &r.display, &r.role, &r.disabledInt)
	if err != nil {
		return r, "unknown"
	}
	r.name = r.display
	if r.name == "" {
		r.name = r.username
	}
	switch {
	case r.revokedAt.Valid:
		return r, "revoked"
	case r.usedAt.Valid:
		return r, "used"
	case r.expires <= a.ms():
		return r, "expired"
	case users.Role(r.role) == users.RoleOwner:
		return r, "owner"
	case r.disabledInt != 0:
		return r, "disabled"
	}
	return r, ""
}

// signInFailed costs the address one try and records why; the token is never recorded.
func (a *Service) signInFailed(ctx context.Context, ip netip.Addr, reason string) {
	a.ipLim.Fail(IPKey(ip))
	a.audit(ctx, db.AuditEntry{Action: "auth.signin_link_failed", Detail: map[string]string{"ip": ip.String(), "reason": reason}})
}

// CheckSignInLink is the read-only look behind GET /signin/{token}: it returns the name the link
// signs in as, and changes nothing but the limiter and the failure audit. A limited address gets
// *RateLimitedError before the database is read.
func (a *Service) CheckSignInLink(ctx context.Context, tok string, ip netip.Addr) (string, error) {
	if err := a.InviteAllowed(ip); err != nil {
		return "", err
	}
	hash, ok := token.Hash(tok)
	if !ok {
		a.signInFailed(ctx, ip, "malformed")
		return "", ErrSignInLinkBroken
	}
	row, reason := a.readLink(ctx, a.st.Reader(), hash)
	if reason != "" {
		a.signInFailed(ctx, ip, reason)
		return "", ErrSignInLinkBroken
	}
	return row.name, nil
}

// SpendSignInLink uses the link once: it marks it used (only while still open), ends prev (the
// browser's current session), starts a session, binds that session to the link and audits, in one
// transaction. hasPassword says where the person goes next.
func (a *Service) SpendSignInLink(ctx context.Context, tok string, ip netip.Addr, ua string, prev *Session) (Login, bool, error) {
	if err := a.InviteAllowed(ip); err != nil {
		return Login{}, false, err
	}
	hash, ok := token.Hash(tok)
	if !ok {
		a.signInFailed(ctx, ip, "malformed")
		return Login{}, false, ErrSignInLinkBroken
	}
	var out Login
	err := a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := a.sessions.DeleteExpired(ctx, tx); err != nil {
			return err
		}
		row, reason := a.readLink(ctx, tx, hash)
		if reason != "" {
			return brokenLink(reason)
		}
		now := a.ms()
		res, err := tx.ExecContext(ctx, `UPDATE sign_in_links SET used_at = ? WHERE id = ? AND used_at IS NULL AND revoked_at IS NULL`, now, row.id)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return brokenLink("used")
		}
		if prev != nil {
			if err := a.sessions.Delete(ctx, tx, prev.Hash); err != nil {
				return err
			}
		}
		stok, sess, err := a.sessions.Create(ctx, tx, row.userID, ua)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE sign_in_links SET session_hash = ? WHERE id = ?`, sess.Hash[:], row.id); err != nil {
			return err
		}
		if err := a.users.TouchLastSeen(ctx, tx, row.userID, now); err != nil {
			return err
		}
		u, err := a.users.ByIDTx(ctx, tx, row.userID)
		if err != nil {
			return err
		}
		u.LastSeenAt = now
		out = Login{Token: stok, User: u}
		return db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: row.userID, Action: "auth.login", Target: row.userID,
			Detail: map[string]string{"ip": ip.String(), "via": "sign_in_link"}})
	})
	var bl brokenLink
	if errors.As(err, &bl) {
		a.signInFailed(ctx, ip, string(bl))
		return Login{}, false, ErrSignInLinkBroken
	}
	if err != nil {
		return Login{}, false, err
	}
	has, err := a.users.HasPassword(ctx, out.User.ID)
	if err != nil {
		return Login{}, false, err
	}
	return out, has, nil
}

// signInBound reports whether sess is the session a live link started, less than SignInResetWindow ago.
func (a *Service) signInBound(ctx context.Context, q db.RowQueryer, sess Session) (bool, error) {
	if sess.DeviceID != "" {
		return false, nil
	}
	var n int
	err := q.QueryRowContext(ctx, `SELECT count(*) FROM sign_in_links WHERE session_hash = ? AND user_id = ?
		AND revoked_at IS NULL AND used_at >= ?`, sess.Hash[:], sess.UserID, a.now().Add(-SignInResetWindow).UnixMilli()).Scan(&n)
	return n > 0, err
}

// SignInBound reports whether sess may set a password without the old one: it is the session a
// sign-in link started, the link is not revoked, and it was spent less than an hour ago.
func (a *Service) SignInBound(ctx context.Context, sess Session) (bool, error) {
	return a.signInBound(ctx, a.st.Reader(), sess)
}

// SetPasswordFromLink sets a new password for the person of a link-started session. In one
// transaction it sets the hash, ends every session and device token of the person, starts one
// fresh session and audits. It returns the new session token. users.FieldErrors is a password the
// rules refuse; ErrNotLinkSession and ErrNoPassword are the refusals described above.
func (a *Service) SetPasswordFromLink(ctx context.Context, sess Session, next string, ip netip.Addr) (string, error) {
	u, err := a.users.ByID(ctx, sess.UserID)
	if err != nil {
		return "", err
	}
	if errs := users.Validate(users.NewUser{Username: u.Username, DisplayName: "x", Password: next}); errs["password"] != "" {
		return "", users.FieldErrors{"password": errs["password"]}
	}
	newHash, err := a.users.HashPassword(ctx, next) // before the transaction: hashing is slow
	if err != nil {
		return "", err
	}
	var tok string
	err = a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		ok, err := a.signInBound(ctx, tx, sess)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotLinkSession
		}
		var has bool
		if err := tx.QueryRowContext(ctx, `SELECT password_hash IS NOT NULL FROM users WHERE id = ?`, u.ID).Scan(&has); err != nil {
			return err
		}
		if !has {
			return ErrNoPassword
		}
		if err := a.users.SetPassword(ctx, tx, u.ID, newHash); err != nil {
			return err
		}
		if _, err := a.sessions.EndAllForUser(ctx, tx, u.ID, a.ms()); err != nil {
			return err
		}
		if tok, _, err = a.sessions.Create(ctx, tx, u.ID, sess.UserAgent); err != nil {
			return err
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: a.ms(), UserID: u.ID, Action: "auth.password_reset", Target: u.ID,
			Detail: map[string]string{"by": "sign_in_link", "ip": ip.String()}})
	})
	if err != nil {
		return "", err
	}
	return tok, nil
}
