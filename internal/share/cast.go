package share

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/token"
)

// Errors the callers match on (decision 0047). The web layer answers both with one plain 404.
var (
	ErrCastUnknown = errors.New("share: no such cast address")
	ErrCastExpired = errors.New("share: this cast address has expired")
)

// What a cast token carries.
const (
	CastBook    = "book"
	CastEpisode = "episode"
	CastStation = "station"
)

// Reasons Lookup gives for a token that does not answer. Only the first two count as a guess.
const (
	CastMalformed = "malformed"
	CastUnknown   = "unknown"
	CastExpired   = "expired"
)

const (
	castMaxLive = 8 // newest tokens one person keeps; older ones are deleted at the next mint
	// CastMargin is added to a book's length so a long listen outlasts the token.
	CastMargin = 6 * time.Hour
	// CastMaxLife is the longest a book or episode token lives (operator, 2026-11-17).
	CastMaxLife = 72 * time.Hour
	// CastStationLife is how long a station token lives; a live stream has no length.
	CastStationLife = 12 * time.Hour
	// LiveAddressLife is how long the iPhone app's live address lives (decision 0049).
	LiveAddressLife = time.Hour
)

// CastToken is one row of cast_tokens: a short-lived address that lets a Cast device fetch one book,
// episode or station for one person. Delete is revoke; there is no revoked column.
type CastToken struct {
	UserID, Kind, TargetID string
	// DeviceID is set for an iPhone live address (decision 0049): the device token it is bound to.
	DeviceID             string
	CreatedAt, ExpiresAt int64
}

// CastTokens reads and writes cast_tokens. It decides nothing about access: the web layer checks the
// owner's rights at mint and at every fetch.
type CastTokens struct {
	st  *db.Store
	now func() time.Time
}

// NewCastTokens returns the repository; a nil now means time.Now.
func NewCastTokens(st *db.Store, now func() time.Time) *CastTokens {
	if now == nil {
		now = time.Now
	}
	return &CastTokens{st: st, now: now}
}

// CastLife is how long a token for something lengthMs long lives: the length plus CastMargin, at
// least CastMargin and at most CastMaxLife.
func CastLife(lengthMs int64) time.Duration {
	life := time.Duration(max(lengthMs, 0))*time.Millisecond + CastMargin
	return min(max(life, CastMargin), CastMaxLife)
}

// Mint makes a token for userID and returns it raw, shown once, with its expiry in Unix ms. In one
// write it deletes every expired row, inserts the new one and deletes this person's rows beyond the
// newest castMaxLive. The caller has checked the person's rights.
func (c *CastTokens) Mint(ctx context.Context, userID, kind, targetID string, life time.Duration) (string, int64, error) {
	if kind != CastBook && kind != CastEpisode && kind != CastStation || targetID == "" || life <= 0 {
		return "", 0, ErrCastUnknown
	}
	now := c.now().UnixMilli()
	expires := now + life.Milliseconds()
	tok, hash := token.New()
	err := c.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM cast_tokens WHERE expires_at <= ?`, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cast_tokens (token_hash, user_id, kind, target_id, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?)`, hash[:], userID, kind, targetID, now, expires); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM cast_tokens WHERE user_id = ? AND token_hash NOT IN
			(SELECT token_hash FROM cast_tokens WHERE user_id = ? ORDER BY created_at DESC, token_hash DESC LIMIT ?)`,
			userID, userID, castMaxLive)
		return err
	})
	if err != nil {
		return "", 0, err
	}
	return tok, expires, nil
}

// MintLive makes the iPhone app's live address for one station (decision 0049): a station token bound to
// deviceID. In one write it deletes expired rows and this device's earlier address, inserts the new row and
// applies the same per-person trim as Mint. The caller has checked the person's rights and that the device is a phone.
func (c *CastTokens) MintLive(ctx context.Context, userID, deviceID, stationID string) (string, int64, error) {
	if userID == "" || deviceID == "" || stationID == "" {
		return "", 0, ErrCastUnknown
	}
	now := c.now().UnixMilli()
	expires := now + LiveAddressLife.Milliseconds()
	tok, hash := token.New()
	err := c.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM cast_tokens WHERE expires_at <= ?`, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM cast_tokens WHERE device_id = ?`, deviceID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO cast_tokens (token_hash, user_id, kind, target_id, device_id, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, hash[:], userID, CastStation, stationID, deviceID, now, expires); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM cast_tokens WHERE user_id = ? AND token_hash NOT IN
			(SELECT token_hash FROM cast_tokens WHERE user_id = ? ORDER BY created_at DESC, token_hash DESC LIMIT ?)`,
			userID, userID, castMaxLive)
		return err
	})
	if err != nil {
		return "", 0, err
	}
	return tok, expires, nil
}

// Lookup finds the token a cast address names. The reason is one of CastMalformed, CastUnknown or
// CastExpired whenever the error is not nil; an expired row is returned too, so callers can tell it from a guess.
func (c *CastTokens) Lookup(ctx context.Context, tok string) (CastToken, string, error) {
	hash, ok := token.Hash(tok)
	if !ok {
		return CastToken{}, CastMalformed, ErrCastUnknown
	}
	var t CastToken
	var deviceLive bool
	err := c.st.Reader().QueryRowContext(ctx, `SELECT c.user_id, c.kind, c.target_id, coalesce(c.device_id, ''), c.created_at, c.expires_at,
		(d.revoked_at IS NULL AND d.token_hash IS NOT NULL)
		FROM cast_tokens c LEFT JOIN devices d ON d.id = c.device_id WHERE c.token_hash = ?`, hash[:]).
		Scan(&t.UserID, &t.Kind, &t.TargetID, &t.DeviceID, &t.CreatedAt, &t.ExpiresAt, &deviceLive)
	if errors.Is(err, sql.ErrNoRows) {
		return CastToken{}, CastUnknown, ErrCastUnknown
	}
	if err != nil {
		return CastToken{}, "", err
	}
	if t.ExpiresAt <= c.now().UnixMilli() || t.DeviceID != "" && !deviceLive {
		// A live address ends with its device token (decision 0049): a revoked device reads as expired.
		return t, CastExpired, ErrCastExpired
	}
	return t, "", nil
}

// DeleteForUser removes every cast token of the person: at sign-out, when casting is turned off and
// when the person is deleted. A phone's live address stays: it ends with its device token (decision 0049).
func (c *CastTokens) DeleteForUser(ctx context.Context, userID string) error {
	return c.st.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM cast_tokens WHERE user_id = ? AND device_id IS NULL`, userID)
		return err
	})
}

// DeleteForDevice removes the live address bound to deviceID: when the device token is revoked.
func (c *CastTokens) DeleteForDevice(ctx context.Context, deviceID string) error {
	return c.st.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM cast_tokens WHERE device_id = ?`, deviceID)
		return err
	})
}
