package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"strings"
)

const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// NewSetupCode returns a fresh first-run code: 10 Crockford base32 characters
// shown as XXXXX-XXXXX. A new one is made on every start; it lives in memory
// and in the start-up log line, nowhere else.
func NewSetupCode() string {
	var b [10]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic("tendshelf: crypto/rand failed: " + err.Error())
	}
	var out [11]byte
	for i, x := range b {
		j := i
		if i >= 5 {
			j = i + 1
		}
		out[j] = crockford[x&31] // 256 is a multiple of 32, so there is no modulo bias
	}
	out[5] = '-'
	return string(out[:])
}

func normalizeCode(s string) string {
	s = strings.ToUpper(s)
	return strings.NewReplacer("-", "", " ", "", "\t", "").Replace(s)
}

// SetupCodeOK compares a typed code with the real one in constant time,
// ignoring case, hyphens and spaces. An empty real code never matches.
func SetupCodeOK(want, got string) bool {
	w, g := normalizeCode(want), normalizeCode(got)
	if w == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(w), []byte(g)) == 1
}
