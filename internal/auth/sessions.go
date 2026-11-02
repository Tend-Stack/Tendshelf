package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// Session is one signed-in browser. Times are Unix milliseconds UTC.
type Session struct {
	Hash       [32]byte
	UserID     string
	CreatedAt  int64
	ExpiresAt  int64
	LastSeenAt int64
	UserAgent  string
}

// Sessions reads and writes the sessions table. Only the SHA-256 of a token is
// stored.
type Sessions struct {
	st  *db.Store
	now func() time.Time
}

// NewSessions returns a Sessions on st; a nil now means time.Now.
func NewSessions(st *db.Store, now func() time.Time) *Sessions {
	if now == nil {
		now = time.Now
	}
	return &Sessions{st: st, now: now}
}

const maxUserAgent = 256

// Create starts a session for userID inside the caller's transaction and
// returns the raw token for the cookie.
func (s *Sessions) Create(ctx context.Context, ex db.Execer, userID, ua string) (string, Session, error) {
	tok, hash := NewToken()
	now := s.now()
	sess := Session{
		Hash: hash, UserID: userID, CreatedAt: now.UnixMilli(),
		ExpiresAt: now.Add(SessionTTL).UnixMilli(), LastSeenAt: now.UnixMilli(),
	}
	if len(ua) > maxUserAgent {
		ua = strings.ToValidUTF8(ua[:maxUserAgent], "")
	}
	sess.UserAgent = ua
	_, err := ex.ExecContext(ctx, `INSERT INTO sessions (token_hash, user_id, created_at, expires_at, last_seen_at, user_agent)
		VALUES (?, ?, ?, ?, ?, ?)`, sess.Hash[:], sess.UserID, sess.CreatedAt, sess.ExpiresAt, sess.LastSeenAt, ua)
	if err != nil {
		return "", Session{}, err
	}
	return tok, sess, nil
}

// Lookup finds the session for a cookie token. It returns ErrNoSession when the
// token is malformed, unknown or expired (the row is deleted), or when the
// account is disabled. A session not seen for RefreshAfter is refreshed:
// expiry moves to now + SessionTTL and both last_seen_at columns are updated.
func (s *Sessions) Lookup(ctx context.Context, tok string) (Session, users.User, bool, error) {
	hash, ok := HashToken(tok)
	if !ok {
		return Session{}, users.User{}, false, ErrNoSession
	}
	var sess Session
	var u users.User
	var role string
	var disabled int
	err := s.st.Reader().QueryRowContext(ctx, `SELECT s.created_at, s.expires_at, s.last_seen_at, s.user_agent,
			u.id, u.username, u.display_name, COALESCE(u.email, ''), u.role, u.disabled, u.created_at, COALESCE(u.last_seen_at, 0)
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = ?`, hash[:]).Scan(
		&sess.CreatedAt, &sess.ExpiresAt, &sess.LastSeenAt, &sess.UserAgent,
		&u.ID, &u.Username, &u.DisplayName, &u.Email, &role, &disabled, &u.CreatedAt, &u.LastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, users.User{}, false, ErrNoSession
	}
	if err != nil {
		return Session{}, users.User{}, false, err
	}
	sess.Hash, sess.UserID = hash, u.ID
	u.Role, u.Disabled = users.Role(role), disabled != 0

	now := s.now()
	nowMs := now.UnixMilli()
	if nowMs > sess.ExpiresAt {
		if err := s.Delete(ctx, s.st.Writer(), hash); err != nil {
			return Session{}, users.User{}, false, err
		}
		return Session{}, users.User{}, false, ErrNoSession
	}
	if u.Disabled {
		return Session{}, users.User{}, false, ErrNoSession
	}
	if nowMs-sess.LastSeenAt < RefreshAfter.Milliseconds() {
		return sess, u, false, nil
	}
	newExpiry := now.Add(SessionTTL).UnixMilli()
	err = s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE token_hash = ?`,
			nowMs, newExpiry, hash[:]); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE users SET last_seen_at = ? WHERE id = ?`, nowMs, u.ID)
		return err
	})
	if err != nil {
		// A busy or failing write must not sign anyone out; the next request tries again.
		return sess, u, false, nil
	}
	sess.LastSeenAt, sess.ExpiresAt = nowMs, newExpiry
	u.LastSeenAt = nowMs
	return sess, u, true, nil
}

// Delete removes one session.
func (s *Sessions) Delete(ctx context.Context, ex db.Execer, hash [32]byte) error {
	_, err := ex.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, hash[:])
	return err
}

// DeleteExpired removes every session past its expiry and returns how many.
func (s *Sessions) DeleteExpired(ctx context.Context, ex db.Execer) (int64, error) {
	res, err := ex.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, s.now().UnixMilli())
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// EndAllForUser signs the person out everywhere inside the caller's transaction: it deletes
// every session and revokes every device token (the row stays, the token is gone). It returns
// how many sessions it deleted.
func (s *Sessions) EndAllForUser(ctx context.Context, ex db.Execer, userID string, nowMs int64) (int64, error) {
	res, err := ex.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = ?`, userID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	_, err = revokeAllDevices(ctx, ex, userID, nowMs)
	return n, err
}
