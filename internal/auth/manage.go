package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/netip"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// PublicIDLen is how many hex characters of a stored session hash identify it in HTML.
const PublicIDLen = 16

// PublicID is the first PublicIDLen hex characters of the stored hash. It is enough to pick one
// of a person's own sessions and is useless as a credential: it is neither the token nor the hash.
func PublicID(hash [32]byte) string { return hex.EncodeToString(hash[:])[:PublicIDLen] }

// SessionInfo is one signed-in browser of a person, for the Profile page.
type SessionInfo struct {
	UserID    string
	PublicID  string
	UserAgent string
	CreatedAt int64 // Unix milliseconds UTC
	// LastSeenAt is Unix milliseconds UTC.
	LastSeenAt int64
	Current    bool
}

// ListForUser lists the person's live sessions, the most recently seen first. current marks the
// one the request came from.
func (s *Sessions) ListForUser(ctx context.Context, userID string, current [32]byte) ([]SessionInfo, error) {
	return s.list(ctx, current, ` AND user_id = ?`, userID)
}

// ListAll lists every live session of every person, in the same order as ListForUser.
func (s *Sessions) ListAll(ctx context.Context, current [32]byte) ([]SessionInfo, error) {
	return s.list(ctx, current, "")
}

func (s *Sessions) list(ctx context.Context, current [32]byte, where string, args ...any) ([]SessionInfo, error) {
	args = append([]any{s.now().UnixMilli()}, args...)
	rows, err := s.st.Reader().QueryContext(ctx, `SELECT token_hash, user_id, user_agent, created_at, last_seen_at
		FROM sessions WHERE expires_at >= ? AND device_id IS NULL`+where+` ORDER BY last_seen_at DESC, created_at DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SessionInfo
	for rows.Next() {
		var raw []byte
		var si SessionInfo
		if err := rows.Scan(&raw, &si.UserID, &si.UserAgent, &si.CreatedAt, &si.LastSeenAt); err != nil {
			return nil, err
		}
		if len(raw) != 32 {
			continue
		}
		var h [32]byte
		copy(h[:], raw)
		si.PublicID = PublicID(h)
		si.Current = h == current
		out = append(out, si)
	}
	return out, rows.Err()
}

// IsSessionPublicID reports whether id has the shape of a session public id (16 lowercase hex).
// Device ids start with "dev_", so the two never collide.
func IsSessionPublicID(id string) bool {
	if len(id) != PublicIDLen {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// DeleteByPublicID ends one of the person's own sessions. ok is false when the id is malformed,
// matches none of that person's sessions, or is ambiguous; another person's session id never
// matches. deleted is the stored hash of the ended session, so the caller can tell whether it was
// the current one.
func (s *Sessions) DeleteByPublicID(ctx context.Context, userID, publicID string) (deleted [32]byte, ok bool, err error) {
	if !IsSessionPublicID(publicID) {
		return deleted, false, nil
	}
	err = s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT token_hash FROM sessions
			WHERE user_id = ? AND substr(lower(hex(token_hash)), 1, ?) = ?`, userID, PublicIDLen, publicID)
		if err != nil {
			return err
		}
		var found [][]byte
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				_ = rows.Close()
				return err
			}
			found = append(found, raw)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if len(found) != 1 || len(found[0]) != 32 {
			return nil
		}
		copy(deleted[:], found[0])
		if _, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ? AND user_id = ?`, deleted[:], userID); err != nil {
			return err
		}
		ok = true
		return db.Audit(ctx, tx, db.AuditEntry{At: s.now().UnixMilli(), UserID: userID, Action: "auth.session_signed_out", Target: userID})
	})
	if err != nil {
		return [32]byte{}, false, err
	}
	return deleted, ok, nil
}

// DeleteOthers ends every session of the person except keep, inside the caller's transaction, and
// returns how many it ended. The kept session is never touched.
func (s *Sessions) DeleteOthers(ctx context.Context, ex db.Execer, userID string, keep [32]byte) (int64, error) {
	res, err := ex.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ? AND token_hash <> ?`, userID, keep[:])
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// SignOutOthers ends every other session of the signed-in person, revokes all their device
// tokens except the one this session is bound to (the phone does not sign itself out; a plain
// browser has none, so every token goes), and records it.
func (a *Service) SignOutOthers(ctx context.Context, sess Session) (int64, error) {
	var n int64
	err := a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		if n, err = a.sessions.DeleteOthers(ctx, tx, sess.UserID, sess.Hash); err != nil {
			return err
		}
		devices, err := revokeAllDevices(ctx, tx, sess.UserID, a.ms(), sess.DeviceID)
		if err != nil {
			return err
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: a.ms(), UserID: sess.UserID, Action: "auth.sessions_signed_out",
			Target: sess.UserID, Detail: map[string]int64{"count": n, "devices": devices}})
	})
	return n, err
}

// ChangePassword sets a new password for the signed-in person. It needs the current password
// (failures count against the account and the address, as at sign-in), checks the new one with
// the account rules, hashes before the write transaction, and in one transaction stores the hash,
// ends every session and starts a fresh one for this browser. It returns that session's raw token
// for the cookie: the old token is dead, so a stolen one stops working. Problems with the new
// password come back as users.FieldErrors{"password": code}; a wrong current password is
// ErrBadCredentials; limits answer *RateLimitedError before any hashing.
func (a *Service) ChangePassword(ctx context.Context, sess Session, current, next string, ip netip.Addr) (string, error) {
	ipKey, lim := IPKey(ip), "u:"+sess.UserID
	if err := limited(a.userLim.Allow(lim)); err != nil {
		return "", err
	}
	if err := limited(a.ipLim.Allow(ipKey)); err != nil {
		return "", err
	}
	u, err := a.users.ByID(ctx, sess.UserID)
	if err != nil {
		return "", err
	}
	_, hash, err := a.users.ByUsername(ctx, u.Username)
	if err != nil {
		return "", err
	}
	ok := false
	if len(current) <= maxPasswordIn {
		ok, err = a.users.VerifyPassword(ctx, hash, current)
		if errors.Is(err, users.ErrBadHash) {
			a.users.VerifyDummy(ctx, current)
			ok, err = false, nil
		}
		if err != nil {
			return "", err
		}
	}
	if !ok {
		a.userLim.Fail(lim)
		a.ipLim.Fail(ipKey)
		a.audit(ctx, db.AuditEntry{UserID: u.ID, Action: "auth.password_change_failed", Target: u.ID,
			Detail: map[string]string{"ip": ip.String()}})
		return "", ErrBadCredentials
	}
	// Same rules as account creation; only the password matters here.
	if errs := users.Validate(users.NewUser{Username: u.Username, DisplayName: "x", Password: next}); errs["password"] != "" {
		return "", users.FieldErrors{"password": errs["password"]}
	}
	newHash, err := a.users.HashPassword(ctx, next) // before the transaction: hashing is slow
	if err != nil {
		return "", err
	}
	var tok string
	err = a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := a.users.SetPassword(ctx, tx, u.ID, newHash); err != nil {
			return err
		}
		if _, err := a.sessions.EndAllForUser(ctx, tx, u.ID, a.ms()); err != nil {
			return err
		}
		var err error
		if tok, _, err = a.sessions.Create(ctx, tx, u.ID, sess.UserAgent); err != nil {
			return err
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: a.ms(), UserID: u.ID, Action: "auth.password_changed", Target: u.ID,
			Detail: map[string]string{"ip": ip.String()}})
	})
	if err != nil {
		return "", err
	}
	a.userLim.Reset(lim)
	return tok, nil
}

// newTemporaryPassword is 16 random Crockford characters (the setup-code alphabet) in four groups: 80 bits.
func newTemporaryPassword() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	out := make([]byte, 0, 19)
	for i, x := range b {
		if i > 0 && i%4 == 0 {
			out = append(out, '-')
		}
		out = append(out, crockford[x&31]) // 256 is a multiple of 32: no bias
	}
	return string(out), nil
}

// ResetPassword gives the account a new generated password and signs it out everywhere. It is for
// the command-line tool, run by whoever holds the server. The password is returned once and is
// never logged or stored in clear. An unknown username is users.ErrNotFound.
func (a *Service) ResetPassword(ctx context.Context, username string) (string, error) {
	u, _, err := a.users.ByUsername(ctx, strings.ToLower(strings.TrimSpace(username)))
	if err != nil {
		return "", err
	}
	pw, err := newTemporaryPassword()
	if err != nil {
		return "", err
	}
	hash, err := a.users.HashPassword(ctx, pw)
	if err != nil {
		return "", err
	}
	err = a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := a.users.SetPassword(ctx, tx, u.ID, hash); err != nil {
			return err
		}
		if _, err := a.sessions.EndAllForUser(ctx, tx, u.ID, a.ms()); err != nil {
			return err
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: a.ms(), UserID: u.ID, Action: "auth.password_reset", Target: u.ID,
			Detail: map[string]string{"by": "cli"}})
	})
	if err != nil {
		return "", err
	}
	return pw, nil
}
