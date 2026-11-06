package token

import "crypto/rand"

// TypableLen is the length of an app key; with a 32-letter alphabet that is 120 random bits.
const TypableLen = 24

// typableAlphabet is base32 without padding, lower case: a-z and 2-7. No look-alike pairs
// (0/o, 1/l), no symbols, and nothing a small keyboard has to shift for.
const typableAlphabet = "abcdefghijklmnopqrstuvwxyz234567"

// NewTypable returns a fresh app key: 24 characters from a-z2-7, 5 bits each, drawn from crypto/rand.
// It is meant to be typed on an e-ink reader (decision 0025), so it has no separators.
func NewTypable() string {
	var raw [TypableLen]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("tendshelf: crypto/rand failed: " + err.Error())
	}
	out := make([]byte, TypableLen)
	for i, b := range raw {
		out[i] = typableAlphabet[b&31] // 256 is a multiple of 32, so there is no modulo bias
	}
	return string(out)
}

// IsTypable reports whether s has the shape of an app key; it avoids a database read for junk.
func IsTypable(s string) bool {
	if len(s) != TypableLen {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= 'a' && c <= 'z' || c >= '2' && c <= '7') {
			return false
		}
	}
	return true
}
