package auth

import (
	"time"

	"github.com/Tend-Stack/tendshelf/internal/token"
)

// Session cookie and lifetime.
const (
	CookieName   = "ts_session"
	SessionTTL   = 30 * 24 * time.Hour // sliding: every refresh moves expiry to now + SessionTTL
	RefreshAfter = time.Hour           // a session is refreshed at most this often
)

// NewToken returns a fresh session token and the SHA-256 of its raw bytes. The
// token goes to the browser; only the hash is stored.
func NewToken() (string, [32]byte) { return token.New() }

// HashToken returns the stored hash for a token from a cookie. It rejects
// anything that does not decode to exactly 32 bytes, without touching the
// database.
func HashToken(tok string) ([32]byte, bool) { return token.Hash(tok) }
