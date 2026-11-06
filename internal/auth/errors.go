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
	// ErrNoDevice means no live device token: malformed, unknown, revoked, idle too long, or the
	// account is disabled.
	ErrNoDevice = errors.New("auth: no valid device token")
	// ErrTooManyDevices means the person already has MaxLiveDevices device tokens.
	ErrTooManyDevices = errors.New("auth: too many devices")
	// ErrBadCode is the one answer to every failed device-code swap: unknown, used, expired, wrong
	// verifier, malformed, session gone or account disabled.
	ErrBadCode = errors.New("auth: this sign-in has expired")
	// ErrCodeOtherPerson means the code was made by a session of another person than the caller's
	// device. The code is burned.
	ErrCodeOtherPerson = errors.New("auth: code belongs to another person")
	// ErrSetupCode means the first-run setup code did not match.
	ErrSetupCode = errors.New("auth: wrong setup code")
)

// RateLimitedError says to wait before trying again.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("auth: too many attempts, retry in %s", e.RetryAfter.Round(time.Second))
}
