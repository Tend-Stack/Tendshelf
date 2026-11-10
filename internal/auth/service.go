package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"strings"
	"sync/atomic"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth/oidc"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// ServiceOptions configures a Service.
type ServiceOptions struct {
	SetupCode string           // the first-run code; empty means setup can never succeed
	Now       func() time.Time // nil: time.Now
	Invites   *share.Invites   // nil: invite links never work
	Devices   *Devices         // nil: device tokens cannot be issued
	OIDC      *oidc.Client     // nil: sign-in with a provider is off (decision 0037)
}

// Limits on failed attempts: per client address (shared by sign-in and setup)
// and per account.
const (
	ipMaxFails    = 20
	userMaxFails  = 10
	limitWindow   = 15 * time.Minute
	limiterKeys   = 4096
	maxPasswordIn = 1024 // bytes; longer input is refused before any hashing
	maxUsernameIn = 64
)

// Service is sign-in, sign-out and first-run setup.
type Service struct {
	st        *db.Store
	users     *users.Repo
	sessions  *Sessions
	now       func() time.Time
	setupCode string
	ipLim     *Limiter
	userLim   *Limiter
	invites   *share.Invites
	devices   *Devices
	oidc      *oidc.Client
	oidcStart *Limiter
	ownerSeen atomic.Bool
}

// NewService returns a Service.
func NewService(st *db.Store, u *users.Repo, s *Sessions, o ServiceOptions) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	return &Service{
		st: st, users: u, sessions: s, now: o.Now, setupCode: o.SetupCode, invites: o.Invites, devices: o.Devices, oidc: o.OIDC,
		ipLim:     NewLimiter(ipMaxFails, limitWindow, limiterKeys, o.Now),
		userLim:   NewLimiter(userMaxFails, limitWindow, limiterKeys, o.Now),
		oidcStart: NewLimiter(oidcStartMax, limitWindow, limiterKeys, o.Now),
	}
}

// Login is a successful sign-in: the raw token for the cookie and the account.
type Login struct {
	Token string
	User  users.User
}

func (a *Service) ms() int64 { return a.now().UnixMilli() }

// OwnerExists reports whether setup is done. Once true it stays true without
// asking the database again.
func (a *Service) OwnerExists(ctx context.Context) (bool, error) {
	if a.ownerSeen.Load() {
		return true, nil
	}
	ok, err := a.users.OwnerExists(ctx)
	if err == nil && ok {
		a.ownerSeen.Store(true)
	}
	return ok, err
}

// audit writes a row best-effort: a failure to record a refused attempt must
// not change the answer to it.
func (a *Service) audit(ctx context.Context, e db.AuditEntry) {
	e.At = a.ms()
	_ = db.Audit(ctx, a.st.Writer(), e)
}

func limited(ok bool, wait time.Duration) error {
	if ok {
		return nil
	}
	if wait < time.Second {
		wait = time.Second
	}
	return &RateLimitedError{RetryAfter: wait}
}

// checkPassword is the shared first half of every password sign-in: the address and account
// limits, the dummy verify for unknown or password-less accounts, and the failure audit. On success
// it returns the account and its limiter key, which the caller resets once its own write succeeds.
// Every failure is ErrBadCredentials; limits answer with *RateLimitedError before any hashing.
func (a *Service) checkPassword(ctx context.Context, username, password string, ip netip.Addr) (users.User, string, error) {
	ipKey := IPKey(ip)
	if err := limited(a.ipLim.Allow(ipKey)); err != nil {
		return users.User{}, "", err
	}
	username = strings.ToLower(strings.TrimSpace(username))
	fail := func(reason, userID string) error {
		a.ipLim.Fail(ipKey)
		if userID != "" {
			a.userLim.Fail("u:" + userID)
		}
		a.audit(ctx, db.AuditEntry{UserID: userID, Action: "auth.login_failed", Detail: map[string]string{"ip": ip.String(), "reason": reason}})
		return ErrBadCredentials
	}
	if len(password) > maxPasswordIn || len(username) > maxUsernameIn {
		return users.User{}, "", fail("input_too_long", "")
	}

	u, hash, err := a.users.ByUsername(ctx, username)
	if errors.Is(err, users.ErrNotFound) {
		a.users.VerifyDummy(ctx, password)
		if err := ctx.Err(); err != nil {
			return users.User{}, "", err
		}
		return users.User{}, "", fail("unknown_user", "")
	}
	if err != nil {
		return users.User{}, "", err
	}
	lim := "u:" + u.ID
	if err := limited(a.userLim.Allow(lim)); err != nil {
		return users.User{}, "", err
	}
	// Verify even for a disabled account, so its answer takes as long as any other.
	ok, err := a.users.VerifyPassword(ctx, hash, password)
	if errors.Is(err, users.ErrBadHash) {
		a.users.VerifyDummy(ctx, password) // an account with no password costs the same time
		ok, err = false, nil
	}
	if err != nil {
		return users.User{}, "", err
	}
	switch {
	case u.Disabled:
		return users.User{}, "", fail("disabled", u.ID)
	case !ok:
		return users.User{}, "", fail("bad_password", u.ID)
	}
	return u, lim, nil
}

// Login checks the credentials and starts a session. prev, when not nil, is the
// session the browser already had; it is ended so a new sign-in never reuses a
// token. Every failure is ErrBadCredentials, whether the username exists or
// not; limits answer with *RateLimitedError before any hashing happens.
func (a *Service) Login(ctx context.Context, username, password string, ip netip.Addr, ua string, prev *Session) (Login, error) {
	u, lim, err := a.checkPassword(ctx, username, password, ip)
	if err != nil {
		return Login{}, err
	}
	var out Login
	err = a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := a.sessions.DeleteExpired(ctx, tx); err != nil {
			return err
		}
		if prev != nil {
			if err := a.sessions.Delete(ctx, tx, prev.Hash); err != nil {
				return err
			}
		}
		tok, _, err := a.sessions.Create(ctx, tx, u.ID, ua)
		if err != nil {
			return err
		}
		now := a.ms()
		if err := a.users.TouchLastSeen(ctx, tx, u.ID, now); err != nil {
			return err
		}
		u.LastSeenAt = now
		out = Login{Token: tok, User: u}
		return db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: u.ID, Action: "auth.login", Target: u.ID, Detail: map[string]string{"ip": ip.String()}})
	})
	if err != nil {
		return Login{}, err
	}
	a.userLim.Reset(lim)
	return out, nil
}

// Logout ends the session. A session bound to a device takes the device with it: signing out of
// the phone's page signs out the app too (decision 0026).
func (a *Service) Logout(ctx context.Context, s Session) error {
	return a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := a.sessions.Delete(ctx, tx, s.Hash); err != nil {
			return err
		}
		if s.DeviceID != "" && a.devices != nil {
			if _, err := a.devices.Revoke(ctx, tx, s.UserID, s.DeviceID, a.ms()); err != nil {
				return err
			}
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: a.ms(), UserID: s.UserID, Action: "auth.logout", Target: s.UserID})
	})
}

// Setup creates the owner account and signs it in, when the setup code is right
// and there is no owner yet. Problems with the form come back as
// users.FieldErrors; users.ErrOwnerExists means someone else got there first.
func (a *Service) Setup(ctx context.Context, code string, n users.NewUser, ip netip.Addr, ua string) (Login, error) {
	ipKey := IPKey(ip)
	if err := limited(a.ipLim.Allow(ipKey)); err != nil {
		return Login{}, err
	}
	if !SetupCodeOK(a.setupCode, code) {
		a.ipLim.Fail(ipKey)
		a.audit(ctx, db.AuditEntry{Action: "setup.code_failed", Detail: map[string]string{"ip": ip.String()}})
		return Login{}, ErrSetupCode
	}
	n = users.Normalize(n)
	if errs := users.Validate(n); errs != nil {
		return Login{}, errs
	}
	hash, err := a.users.HashPassword(ctx, n.Password) // before the transaction: hashing is slow
	if err != nil {
		return Login{}, err
	}
	var out Login
	err = a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		u, err := a.users.CreateOwner(ctx, tx, n, hash)
		if err != nil {
			return err
		}
		tok, _, err := a.sessions.Create(ctx, tx, u.ID, ua)
		if err != nil {
			return err
		}
		now := a.ms()
		if err := a.users.TouchLastSeen(ctx, tx, u.ID, now); err != nil {
			return err
		}
		u.LastSeenAt = now
		out = Login{Token: tok, User: u}
		return db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: u.ID, Action: "setup.owner_created", Target: u.ID, Detail: map[string]string{"ip": ip.String()}})
	})
	if err != nil {
		return Login{}, err
	}
	a.ownerSeen.Store(true)
	return out, nil
}

// DeviceLogin is a successful token sign-in: the raw token (shown once), the device and the account.
type DeviceLogin struct {
	Token  string
	Device Device
	User   users.User
}

// IssueDevice checks the password exactly as Login does (same limits, same failures, same audit)
// and, on success, creates a device token. ErrTooManyDevices means the person is at the cap.
func (a *Service) IssueDevice(ctx context.Context, username, password, name string, kind DeviceKind, ip netip.Addr) (DeviceLogin, error) {
	if a.devices == nil {
		return DeviceLogin{}, errors.New("auth: device tokens are not set up")
	}
	u, lim, err := a.checkPassword(ctx, username, password, ip)
	if err != nil {
		return DeviceLogin{}, err
	}
	var out DeviceLogin
	err = a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		tok, dev, err := a.devices.Issue(ctx, tx, u.ID, name, kind)
		if err != nil {
			return err
		}
		now := a.ms()
		if err := a.users.TouchLastSeen(ctx, tx, u.ID, now); err != nil {
			return err
		}
		u.LastSeenAt = now
		out = DeviceLogin{Token: tok, Device: dev, User: u}
		return db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: u.ID, Action: "auth.device_issued", Target: dev.ID,
			Detail: map[string]string{"ip": ip.String(), "device": dev.ID, "kind": string(dev.Kind)}})
	})
	if err != nil {
		return DeviceLogin{}, err
	}
	a.userLim.Reset(lim)
	return out, nil
}

// RenameDevice names one of the person's own devices and records it (the audit detail names the device,
// not the name). It reports whether there was a live device with that id. name is already cleaned and checked.
func (a *Service) RenameDevice(ctx context.Context, userID, deviceID, name string) (bool, error) {
	if a.devices == nil {
		return false, nil
	}
	var ok bool
	err := a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		if ok, err = a.devices.Rename(ctx, tx, userID, deviceID, name); err != nil || !ok {
			return err
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: a.ms(), UserID: userID, Action: "auth.device_renamed", Target: userID,
			Detail: map[string]string{"device": deviceID, "by": userID}})
	})
	return ok, err
}

// RevokeDevice kills one of the person's devices and records who did it (by is the actor's id). It
// reports whether there was a live device with that id.
func (a *Service) RevokeDevice(ctx context.Context, userID, deviceID, by string) (bool, error) {
	if a.devices == nil {
		return false, nil
	}
	var ok bool
	err := a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		if ok, err = a.devices.Revoke(ctx, tx, userID, deviceID, a.ms()); err != nil || !ok {
			return err
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: a.ms(), UserID: by, Action: "auth.device_revoked", Target: userID,
			Detail: map[string]string{"device": deviceID, "by": by}})
	})
	return ok, err
}
