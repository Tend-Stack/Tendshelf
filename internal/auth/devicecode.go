package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"net/netip"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// Device codes let the phone app trade the sign-in a person just did on the server's own page for a
// device token, without the app ever seeing the password (decision 0026). The flow is PKCE (RFC 7636,
// S256 only): the app keeps a random verifier and gives the page its SHA-256, the challenge. The page
// stores a one-time code beside that challenge. The code is useless without the verifier, and the
// verifier never leaves the app.

// DeviceCodeTTL is how long a code can be swapped after the person taps Connect.
const DeviceCodeTTL = 2 * time.Minute

// verifierLen is the length of a verifier: 32 random bytes in base64url without padding.
const verifierLen = 43

// ParseChallenge decodes a PKCE challenge (base64url of a SHA-256, 43 characters, no padding). The
// strict decoder also refuses stray bits in the last character, so one challenge has one spelling.
func ParseChallenge(s string) ([32]byte, bool) {
	var out [32]byte
	if len(s) != base64.RawURLEncoding.EncodedLen(32) {
		return out, false
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(s)
	if err != nil || len(raw) != 32 {
		return out, false
	}
	copy(out[:], raw)
	return out, true
}

// validVerifier reports whether v has the shape of a verifier. It avoids a database read for junk.
func validVerifier(v string) bool {
	if len(v) != verifierLen {
		return false
	}
	for i := 0; i < len(v); i++ {
		c := v[i]
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '-' || c == '_') {
			return false
		}
	}
	return true
}

// IssueDeviceCode stores a one-time code for the signed-in session and the app's challenge, and
// returns the raw code, which is shown once and kept nowhere. It first drops codes that expired and
// any older code of this session, so a session holds at most one live code.
func (a *Service) IssueDeviceCode(ctx context.Context, sess Session, challenge [32]byte, ip netip.Addr) (string, error) {
	var code string
	err := a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		code, err = a.insertDeviceCode(ctx, tx, sess.Hash, sess.UserID, challenge, ip)
		return err
	})
	if err != nil {
		return "", err
	}
	return code, nil
}

// insertDeviceCode is the write behind IssueDeviceCode, inside the caller's transaction: it drops
// expired codes and the session's older code, stores the new code's hash and audits it. Approving a
// pairing (pairing.go) ends in the same write, so there is one kind of code.
func (a *Service) insertDeviceCode(ctx context.Context, tx *sql.Tx, sessHash [32]byte, userID string, challenge [32]byte, ip netip.Addr) (string, error) {
	code, hash := NewToken()
	now := a.ms()
	if _, err := tx.ExecContext(ctx, `DELETE FROM device_codes WHERE expires_at < ? OR session_hash = ?`, now, sessHash[:]); err != nil {
		return "", err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO device_codes (code_hash, session_hash, challenge, created_at, expires_at)
		VALUES (?, ?, ?, ?, ?)`, hash[:], sessHash[:], challenge[:], now, now+DeviceCodeTTL.Milliseconds()); err != nil {
		return "", err
	}
	err := db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: userID, Action: "auth.device_code_issued", Target: userID,
		Detail: map[string]string{"ip": ip.String()}})
	return code, err
}

// SwapDeviceCode trades a code and its verifier for a mobile device token. Every way of failing
// (unknown, used, expired, wrong verifier, malformed, session gone, account disabled) is ErrBadCode,
// and a code that was found is burned whatever the outcome, so one guess spends it.
//
// caller is the device that sent a live bearer token, or nil. With a caller of the same person the
// new session is bound to that device and no token is made (the phone reconnecting); a caller of
// another person, or one that is not a mobile device, is ErrCodeOtherPerson. ErrTooManyDevices
// rolls the whole swap back, code included, so a freed slot lets the same code work again.
// The failure limit is the web layer's, which knows the address.
func (a *Service) SwapDeviceCode(ctx context.Context, code, verifier, name string, caller *Device, ip netip.Addr) (DeviceLogin, error) {
	if a.devices == nil {
		return DeviceLogin{}, errors.New("auth: device tokens are not set up")
	}
	hash, ok := HashToken(code)
	if !ok || !validVerifier(verifier) {
		a.codeFailed(ctx, "", "malformed", ip)
		return DeviceLogin{}, ErrBadCode
	}
	var out DeviceLogin
	var refused error
	var reason, userID string
	err := a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		now := a.ms()
		var sessHash, challenge []byte
		var expires int64
		err := tx.QueryRowContext(ctx, `DELETE FROM device_codes WHERE code_hash = ? RETURNING session_hash, challenge, expires_at`,
			hash[:]).Scan(&sessHash, &challenge, &expires)
		if errors.Is(err, sql.ErrNoRows) {
			refused, reason = ErrBadCode, "unknown"
			return nil
		}
		if err != nil {
			return err
		}
		want := sha256.Sum256([]byte(verifier))
		switch {
		case now > expires:
			refused, reason = ErrBadCode, "expired"
			return nil
		case subtle.ConstantTimeCompare(want[:], challenge) != 1:
			refused, reason = ErrBadCode, "verifier"
			return nil
		}
		var sessUser string
		err = tx.QueryRowContext(ctx, `SELECT user_id FROM sessions WHERE token_hash = ? AND expires_at >= ?`, sessHash, now).Scan(&sessUser)
		if errors.Is(err, sql.ErrNoRows) {
			refused, reason = ErrBadCode, "session"
			return nil
		}
		if err != nil {
			return err
		}
		u, err := a.users.ByIDTx(ctx, tx, sessUser)
		if err != nil && !errors.Is(err, users.ErrNotFound) {
			return err
		}
		if err != nil || u.Disabled {
			refused, reason, userID = ErrBadCode, "disabled", sessUser
			return nil
		}
		userID = u.ID
		var sh [32]byte
		copy(sh[:], sessHash)
		if caller != nil {
			if caller.UserID != u.ID || caller.Kind != KindMobile {
				refused, reason = ErrCodeOtherPerson, "other_person"
				return nil
			}
			if err := a.sessions.BindDevice(ctx, tx, sh, caller.ID); err != nil {
				return err
			}
			out = DeviceLogin{Device: *caller, User: u}
			return db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: u.ID, Action: "auth.device_rebound", Target: caller.ID,
				Detail: map[string]string{"ip": ip.String(), "device": caller.ID}})
		}
		tok, dev, err := a.devices.Issue(ctx, tx, u.ID, name, KindMobile)
		if err != nil {
			return err
		}
		if err := a.sessions.BindDevice(ctx, tx, sh, dev.ID); err != nil {
			return err
		}
		if err := a.users.TouchLastSeen(ctx, tx, u.ID, now); err != nil {
			return err
		}
		u.LastSeenAt = now
		out = DeviceLogin{Token: tok, Device: dev, User: u}
		return db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: u.ID, Action: "auth.device_issued", Target: dev.ID,
			Detail: map[string]string{"ip": ip.String(), "device": dev.ID, "kind": string(dev.Kind), "via": "code"}})
	})
	if err != nil {
		return DeviceLogin{}, err
	}
	if refused != nil {
		a.codeFailed(ctx, userID, reason, ip)
		return DeviceLogin{}, refused
	}
	return out, nil
}

// codeFailed records a refused swap. It never carries the code or the verifier.
func (a *Service) codeFailed(ctx context.Context, userID, reason string, ip netip.Addr) {
	a.audit(ctx, db.AuditEntry{UserID: userID, Action: "auth.device_code_failed", Target: userID,
		Detail: map[string]string{"ip": ip.String(), "reason": reason}})
}
