package auth

import (
	"errors"
	"fmt"
	"time"
)

// Errors the web layer matches on.
var (
	// ErrNoSession means no valid session: unknown token, expired, or the
	// account is disabled.
	ErrNoSession = errors.New("auth: no valid session")
	// ErrBadCredentials is the one answer to every failed sign-in: unknown
	// username, wrong password, disabled account.
	ErrBadCredentials = errors.New("auth: wrong username or password")
	// ErrSetupCode means the first-run setup code did not match.
	ErrSetupCode = errors.New("auth: wrong setup code")
)

// RateLimitedError says to wait before trying again.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("auth: too many attempts, retry in %s", e.RetryAfter.Round(time.Second))
}
