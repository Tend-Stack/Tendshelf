package auth

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// DeviceKind says what sort of app a device token belongs to.
type DeviceKind string

// The kinds the devices table allows.
const (
	KindWeb     DeviceKind = "web"
	KindPWA     DeviceKind = "pwa"
	KindDesktop DeviceKind = "desktop"
	KindMobile  DeviceKind = "mobile"
)

// ParseDeviceKind returns the kind named by s and whether it is one of the four.
func ParseDeviceKind(s string) (DeviceKind, bool) {
	switch k := DeviceKind(s); k {
	case KindWeb, KindPWA, KindDesktop, KindMobile:
		return k, true
	}
	return "", false
}

// KindLabel is the plain-words name of a kind, used when an app sends no device name.
func KindLabel(k DeviceKind) string {
	switch k {
	case KindDesktop:
		return "Desktop app"
	case KindMobile:
		return "Phone app"
	case KindPWA:
		return "Installed app"
	}
	return "Web app"
}

// Device lifetime and size limits.
const (
	// DeviceIdleTTL is how long a token may go unused before it dies: forgotten phones expire.
	DeviceIdleTTL = 180 * 24 * time.Hour
	// MaxLiveDevices caps the live device tokens of one person.
	MaxLiveDevices = 50
	// revokedKeep is how long a revoked row stays visible in the table before issue prunes it.
	revokedKeep = 30 * 24 * time.Hour
	// maxDeviceName matches the table's CHECK (runes).
	maxDeviceName = 64
)

// Device is one app signed in with a token. Times are Unix milliseconds UTC.
type Device struct {
	ID         string
	UserID     string
	Name       string
	Kind       DeviceKind
	CreatedAt  int64
	LastSeenAt int64 // 0: never used
}

// Devices reads and writes the devices table. Only the SHA-256 of a token is stored.
type Devices struct {
	st  *db.Store
	now func() time.Time
}

// NewDevices returns a Devices on st; a nil now means time.Now.
func NewDevices(st *db.Store, now func() time.Time) *Devices {
	if now == nil {
		now = time.Now
	}
	return &Devices{st: st, now: now}
}

// DeviceName cleans a name sent by an app: control characters dropped, runs of white space
// collapsed, trimmed, cut to 64 runes. An empty result becomes the kind's label.
func DeviceName(raw string, kind DeviceKind) string {
	var b strings.Builder
	for _, r := range raw {
		switch {
		case unicode.IsSpace(r):
			b.WriteRune(' ')
		case unicode.IsControl(r):
		default:
			b.WriteRune(r)
		}
	}
	name := strings.Join(strings.Fields(b.String()), " ")
	if rs := []rune(name); len(rs) > maxDeviceName {
		name = strings.TrimSpace(string(rs[:maxDeviceName]))
	}
	if name == "" {
		return KindLabel(kind)
	}
	return name
}

// Issue creates a device token for userID inside the caller's transaction and returns the raw
// token, which is shown once and never stored. It first retires tokens idle past DeviceIdleTTL and
// prunes rows revoked long ago, then refuses with ErrTooManyDevices at MaxLiveDevices.
func (d *Devices) Issue(ctx context.Context, tx *sql.Tx, userID, name string, kind DeviceKind) (string, Device, error) {
	now := d.now().UnixMilli()
	if _, err := tx.ExecContext(ctx, `DELETE FROM devices WHERE user_id = ? AND revoked_at IS NOT NULL AND revoked_at < ?`,
		userID, now-revokedKeep.Milliseconds()); err != nil {
		return "", Device{}, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE devices SET revoked_at = ?, token_hash = NULL
		WHERE user_id = ? AND revoked_at IS NULL AND COALESCE(last_seen_at, created_at) < ?`,
		now, userID, now-DeviceIdleTTL.Milliseconds()); err != nil {
		return "", Device{}, err
	}
	var live int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM devices WHERE user_id = ? AND revoked_at IS NULL`, userID).Scan(&live); err != nil {
		return "", Device{}, err
	}
	if live >= MaxLiveDevices {
		return "", Device{}, ErrTooManyDevices
	}
	tok, hash := NewToken()
	dev := Device{ID: db.NewID("dev"), UserID: userID, Name: DeviceName(name, kind), Kind: kind, CreatedAt: now}
	if _, err := tx.ExecContext(ctx, `INSERT INTO devices (id, user_id, name, kind, token_hash, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		dev.ID, dev.UserID, dev.Name, string(dev.Kind), hash[:], dev.CreatedAt); err != nil {
		return "", Device{}, err
	}
	return tok, dev, nil
}

// Lookup finds the live device for a bearer token and its account. It returns ErrNoDevice when
// the token is malformed, unknown or revoked, when the account is disabled, or when the device sat
// idle past DeviceIdleTTL (its row is then revoked). last_seen_at is written at most once per
// RefreshAfter; a failed write never refuses the request.
func (d *Devices) Lookup(ctx context.Context, tok string) (Device, users.User, error) {
	hash, ok := HashToken(tok)
	if !ok {
		return Device{}, users.User{}, ErrNoDevice
	}
	var dev Device
	var u users.User
	var role, kind string
	var disabled int
	var lastSeen sql.NullInt64
	err := d.st.Reader().QueryRowContext(ctx, `SELECT d.id, d.name, d.kind, d.created_at, d.last_seen_at,
			u.id, u.username, u.display_name, COALESCE(u.email, ''), u.role, u.disabled, u.created_at, COALESCE(u.last_seen_at, 0)
		FROM devices d JOIN users u ON u.id = d.user_id WHERE d.token_hash = ? AND d.revoked_at IS NULL`, hash[:]).Scan(
		&dev.ID, &dev.Name, &kind, &dev.CreatedAt, &lastSeen,
		&u.ID, &u.Username, &u.DisplayName, &u.Email, &role, &disabled, &u.CreatedAt, &u.LastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Device{}, users.User{}, ErrNoDevice
	}
	if err != nil {
		return Device{}, users.User{}, err
	}
	dev.UserID, dev.Kind = u.ID, DeviceKind(kind)
	dev.LastSeenAt = lastSeen.Int64
	u.Role, u.Disabled = users.Role(role), disabled != 0
	if u.Disabled {
		return Device{}, users.User{}, ErrNoDevice
	}

	nowMs := d.now().UnixMilli()
	base := dev.CreatedAt
	if lastSeen.Valid {
		base = lastSeen.Int64
	}
	if nowMs-base > DeviceIdleTTL.Milliseconds() {
		_, _ = d.Revoke(ctx, d.st.Writer(), u.ID, dev.ID, nowMs) // best effort: the refusal stands either way
		return Device{}, users.User{}, ErrNoDevice
	}
	if lastSeen.Valid && nowMs-lastSeen.Int64 < RefreshAfter.Milliseconds() {
		return dev, u, nil
	}
	err = d.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE devices SET last_seen_at = ? WHERE id = ?`, nowMs, dev.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE users SET last_seen_at = ? WHERE id = ?`, nowMs, u.ID)
		return err
	})
	if err == nil {
		dev.LastSeenAt, u.LastSeenAt = nowMs, nowMs
	}
	return dev, u, nil
}

const deviceColumns = `id, user_id, name, kind, created_at, COALESCE(last_seen_at, 0)`

// ListForUser lists the person's live devices, most recently used first.
func (d *Devices) ListForUser(ctx context.Context, userID string) ([]Device, error) {
	return d.list(ctx, ` AND user_id = ?`, userID)
}

// ListAll lists every live device of every person, most recently used first.
func (d *Devices) ListAll(ctx context.Context) ([]Device, error) {
	return d.list(ctx, "")
}

func (d *Devices) list(ctx context.Context, where string, args ...any) ([]Device, error) {
	rows, err := d.st.Reader().QueryContext(ctx, `SELECT `+deviceColumns+` FROM devices WHERE revoked_at IS NULL`+where+
		` ORDER BY COALESCE(last_seen_at, created_at) DESC, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Device
	for rows.Next() {
		var dev Device
		var kind string
		if err := rows.Scan(&dev.ID, &dev.UserID, &dev.Name, &kind, &dev.CreatedAt, &dev.LastSeenAt); err != nil {
			return nil, err
		}
		dev.Kind = DeviceKind(kind)
		out = append(out, dev)
	}
	return out, rows.Err()
}

// Revoke kills one live device of the person (the row stays, the token is gone) and reports
// whether there was one. Another person's device id never matches.
func (d *Devices) Revoke(ctx context.Context, ex db.Execer, userID, id string, nowMs int64) (bool, error) {
	res, err := ex.ExecContext(ctx, `UPDATE devices SET revoked_at = ?, token_hash = NULL
		WHERE id = ? AND user_id = ? AND revoked_at IS NULL`, nowMs, id, userID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// RevokeAll kills every live device of the person and returns how many.
func (d *Devices) RevokeAll(ctx context.Context, ex db.Execer, userID string, nowMs int64) (int64, error) {
	return revokeAllDevices(ctx, ex, userID, nowMs)
}

func revokeAllDevices(ctx context.Context, ex db.Execer, userID string, nowMs int64) (int64, error) {
	res, err := ex.ExecContext(ctx, `UPDATE devices SET revoked_at = ?, token_hash = NULL WHERE user_id = ? AND revoked_at IS NULL`, nowMs, userID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
