package auth

import (
	"context"
	"crypto/md5" // #nosec G501 -- KOReader sends the MD5 of the key; this only reproduces what it hashes, then SHA-256 is stored
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/token"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// IsKeyKind reports whether k is one of the two app-key kinds.
func IsKeyKind(k DeviceKind) bool { return k == KindOPDS || k == KindKOReader }

// KeyHash is the value stored for an app key. For opds it is the SHA-256 of the key as typed. For koreader
// it is the SHA-256 of the lower-case hex MD5 of the key, because KOReader sends the MD5 of what is typed
// and never the key itself; the server cannot undo it either way, and only a SHA-256 is stored.
func KeyHash(kind DeviceKind, secret string) [32]byte {
	if kind == KindKOReader {
		sum := md5.Sum([]byte(secret)) // #nosec G401 -- see the import note
		return KeyHashFromMD5(hex.EncodeToString(sum[:]))
	}
	return sha256.Sum256([]byte(secret))
}

// KeyHashFromMD5 is the stored value for a koreader key when the MD5 is what arrived (the x-auth-key header of
// KOReader's sync protocol): the SHA-256 of the lower-case hex MD5.
func KeyHashFromMD5(md5hex string) [32]byte {
	return sha256.Sum256([]byte(strings.ToLower(md5hex)))
}

// IssueKey creates an app key for userID inside the caller's transaction and returns the key, which is
// shown once and never stored. It shares Issue's pruning and the limit of live devices.
func (d *Devices) IssueKey(ctx context.Context, tx *sql.Tx, userID, name string, kind DeviceKind) (string, Device, error) {
	if !IsKeyKind(kind) {
		return "", Device{}, ErrNoDevice
	}
	secret := token.NewTypable()
	dev, err := d.insert(ctx, tx, userID, name, kind, KeyHash(kind, secret))
	if err != nil {
		return "", Device{}, err
	}
	return secret, dev, nil
}

// LookupKey finds the live key of exactly this kind for a hash (see KeyHash) and its account. It returns
// ErrNoDevice when the hash is unknown, revoked, of another kind, idle past DeviceIdleTTL or the person is
// disabled, and writes last_seen_at as Lookup does.
func (d *Devices) LookupKey(ctx context.Context, kind DeviceKind, hash [32]byte) (Device, users.User, error) {
	if !IsKeyKind(kind) {
		return Device{}, users.User{}, ErrNoDevice
	}
	return d.lookup(ctx, hash, kind)
}

// OwnKeys lists the person's live app keys, most recently used first.
func (d *Devices) OwnKeys(ctx context.Context, userID string) ([]Device, error) {
	rows, err := d.st.Reader().QueryContext(ctx, `SELECT `+deviceColumns+` FROM devices
		WHERE user_id = ? AND revoked_at IS NULL AND kind IN ('opds', 'koreader')
		ORDER BY COALESCE(last_seen_at, created_at) DESC, id`, userID)
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
