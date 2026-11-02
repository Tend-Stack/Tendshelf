// Package token makes and parses the random 32-byte secrets that sessions and invites use. A token
// goes to the person (cookie or link); only its SHA-256 is stored. It is a leaf package so that both
// auth and share can use it without importing each other.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
)

// New returns a fresh token and the SHA-256 of its raw bytes.
func New() (string, [32]byte) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		panic("tendshelf: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), sha256.Sum256(raw[:])
}

// Hash returns the stored hash for a token typed or sent by a person. It rejects anything that does
// not decode to exactly 32 bytes, without touching the database.
func Hash(tok string) ([32]byte, bool) {
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
