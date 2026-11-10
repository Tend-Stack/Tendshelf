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
	// ErrPairEnded is the one answer to every pairing that cannot go on: unknown, expired, already
	// claimed, in the wrong state, owner session gone, person disabled. Nothing is made.
	ErrPairEnded = errors.New("auth: this code has ended")
	// ErrPairMismatch means the number typed did not match. The pairing is deleted.
	ErrPairMismatch = errors.New("auth: the number did not match")
	// ErrPairBound means the phone's own session is already bound to a device (a connected app), so it
	// may not be made someone else's.
	ErrPairBound = errors.New("auth: this phone is already signed in")
	// ErrSetupCode means the first-run setup code did not match.
	ErrSetupCode = errors.New("auth: wrong setup code")
)

// RateLimitedError says to wait before trying again.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string {
	return fmt.Sprintf("auth: too many attempts, retry in %s", e.RetryAfter.Round(time.Second))
}

// Errors of sign-in with an OpenID Connect provider (decision 0037). The web layer maps each to
// words and a status; none carries provider text.
var (
	// ErrOIDCOff means sign-in with a provider is not switched on (or not complete), or a link was
	// started without a session.
	ErrOIDCOff = errors.New("auth: sign-in with a provider is off")
	// ErrOIDCBusy means too many sign-ins are under way.
	ErrOIDCBusy = errors.New("auth: too many provider sign-ins are under way")
	// ErrOIDCFailed is the one answer to every callback that cannot sign anyone in: wrong or missing
	// state, a replay, a refused ID token, a disabled account. The reason goes to the audit log.
	ErrOIDCFailed = errors.New("auth: the provider sign-in failed")
	// ErrOIDCCancelled means the person cancelled at the provider.
	ErrOIDCCancelled = errors.New("auth: the provider sign-in was cancelled")
	// ErrOIDCNoAccount means no account is linked to this provider account and none may be made.
	ErrOIDCNoAccount = errors.New("auth: no account uses this provider account")
	// ErrOIDCEmailTaken means a verified email belongs to an account that may not be linked or
	// duplicated by email; the person links from that account.
	ErrOIDCEmailTaken = errors.New("auth: an account already uses this email")
	// ErrOIDCLinkGone means the link step is unknown, used, expired or belongs to another session.
	ErrOIDCLinkGone = errors.New("auth: the link step ran out")
	// ErrOIDCTaken means the provider account is already linked to someone else.
	ErrOIDCTaken = errors.New("auth: that provider account is linked to someone else")
	// ErrOIDCAlready means the person already has a provider account linked for this provider.
	ErrOIDCAlready = errors.New("auth: a provider account is already linked")
	// ErrOIDCLastWayIn means unlinking would leave the account without a way in.
	ErrOIDCLastWayIn = errors.New("auth: add a password before unlinking")
	// ErrOIDCNotLinked means there is no provider account to unlink.
	ErrOIDCNotLinked = errors.New("auth: no provider account is linked")
)
