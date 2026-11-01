package users

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// HashParams are the argon2id cost settings used when a new hash is made.
// A stored hash carries its own parameters, so changing these later never
// breaks an existing password.
type HashParams struct {
	MemoryKiB uint32
	Time      uint32
	Threads   uint8
	SaltLen   uint32
	KeyLen    uint32
}

// DefaultHash is the OWASP password-storage recommendation for argon2id
// (19 MiB, 2 iterations, 1 lane).
var DefaultHash = HashParams{MemoryKiB: 19456, Time: 2, Threads: 1, SaltLen: 16, KeyLen: 32}

// ErrBadHash means a stored password hash could not be read.
var ErrBadHash = errors.New("users: malformed password hash")

// Limits on the parameters read back from a stored hash, so a damaged row can
// not ask for gigabytes of memory.
const (
	maxHashMemoryKiB = 1 << 20 // 1 GiB
	maxHashTime      = 16
	maxHashThreads   = 16
	maxHashKeyLen    = 128
)

var b64 = base64.RawStdEncoding

func (r *Repo) acquire(ctx context.Context) error {
	select {
	case r.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Repo) release() { <-r.sem }

// HashPassword returns the encoded argon2id hash of pw.
func (r *Repo) HashPassword(ctx context.Context, pw string) (string, error) {
	if err := r.acquire(ctx); err != nil {
		return "", err
	}
	defer r.release()
	p := r.opts.Hash
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(pw), salt, p.Time, p.MemoryKiB, p.Threads, p.KeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version,
		p.MemoryKiB, p.Time, p.Threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// VerifyPassword reports whether pw matches the encoded hash. The parameters
// come from the encoding, and the comparison is constant-time.
func (r *Repo) VerifyPassword(ctx context.Context, encoded, pw string) (bool, error) {
	p, salt, want, err := parseHash(encoded)
	if err != nil {
		return false, err
	}
	if err := r.acquire(ctx); err != nil {
		return false, err
	}
	defer r.release()
	got := argon2.IDKey([]byte(pw), salt, p.Time, p.MemoryKiB, p.Threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// VerifyDummy spends the same time as a real verification, against a hash made
// once per process. Login calls it for an unknown username so the answer
// takes as long as for a known one.
func (r *Repo) VerifyDummy(ctx context.Context, pw string) {
	r.dummyOnce.Do(func() {
		r.dummy, _ = r.HashPassword(context.Background(), "tendshelf dummy password")
	})
	_, _ = r.VerifyPassword(ctx, r.dummy, pw)
}

func parseHash(encoded string) (HashParams, []byte, []byte, error) {
	var p HashParams
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return p, nil, nil, ErrBadHash
	}
	var v int
	if n, err := fmt.Sscanf(parts[2], "v=%d", &v); err != nil || n != 1 || v != argon2.Version {
		return p, nil, nil, ErrBadHash
	}
	var m, t uint32
	var th uint8
	if n, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &th); err != nil || n != 3 {
		return p, nil, nil, ErrBadHash
	}
	if m < 8 || m > maxHashMemoryKiB || t < 1 || t > maxHashTime || th < 1 || th > maxHashThreads {
		return p, nil, nil, ErrBadHash
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return p, nil, nil, ErrBadHash
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil || len(key) < 16 || len(key) > maxHashKeyLen {
		return p, nil, nil, ErrBadHash
	}
	p = HashParams{MemoryKiB: m, Time: t, Threads: th, SaltLen: uint32(len(salt)), KeyLen: uint32(len(key))}
	return p, salt, key, nil
}
