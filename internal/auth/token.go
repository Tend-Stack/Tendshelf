package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"time"
)

// Session cookie and lifetime.
const (
	CookieName   = "ts_session"
	SessionTTL   = 30 * 24 * time.Hour // sliding: every refresh moves expiry to now + SessionTTL
	RefreshAfter = time.Hour           // a session is refreshed at most this often
)

// NewToken returns a fresh session token and the SHA-256 of its raw bytes. The
// token goes to the browser; only the hash is stored.
func NewToken() (string, [32]byte) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("tendshelf: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), sha256.Sum256(raw[:])
}

// HashToken returns the stored hash for a token from a cookie. It rejects
// anything that does not decode to exactly 32 bytes, without touching the
// database.
func HashToken(tok string) ([32]byte, bool) {
	var zero [32]byte
	if len(tok) != base64.RawURLEncoding.EncodedLen(32) {
		return zero, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(tok)
	if err != nil || len(raw) != 32 {
		return zero, false
	}
	return sha256.Sum256(raw), true
}
