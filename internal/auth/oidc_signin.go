package auth

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth/oidc"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/token"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// Sign-in with an OpenID Connect provider (decision 0037, plan docs/plans/oidc.md). A provider
// sign-in ends in the same sessions row as a password. No code, token, state, nonce or verifier is
// ever logged or audited.
const (
	oidcStartMax    = 30 // starts per address in limitWindow
	oidcOpenMax     = 500
	oidcOpenPerAddr = 20 // open sign-in rows one address key may hold
	oidcRowTTL      = 10 * time.Minute
	oidcBudget      = 15 * time.Second // the whole callback, including every call to the provider
)

// OIDCCallback is what the callback request carries. State is the query value, Cookie the ts_oidc
// cookie of the same browser.
type OIDCCallback struct{ State, Cookie, Code, Error string }

// OIDCResult is a finished callback: a session (Login, Next) for a sign-in, or LinkPending, the
// id of a link step waiting for the person's confirming POST.
type OIDCResult struct {
	Login       Login
	Next        string
	LinkPending string
}

// oidcConfig loads the settings and refuses unless the provider is on and complete.
func (a *Service) oidcConfig(ctx context.Context) (oidc.Config, error) {
	if a.oidc == nil {
		return oidc.Config{}, ErrOIDCOff
	}
	cfg, err := oidc.LoadConfig(ctx, a.st.Reader())
	if err != nil {
		return oidc.Config{}, err
	}
	if !cfg.Ready() {
		return oidc.Config{}, ErrOIDCOff
	}
	return cfg, nil
}

// OIDCEnabled reports whether the provider is on, for the pages that offer the button.
func (a *Service) OIDCEnabled(ctx context.Context) (oidc.Config, bool) {
	cfg, err := a.oidcConfig(ctx)
	return cfg, err == nil
}

// OIDCStart begins a sign-in (purpose "signin") or a link (purpose "link", which needs the person's
// session). It returns the address to send the browser to and the raw state, which the caller puts
// in the ts_oidc cookie. Errors: *RateLimitedError, ErrOIDCOff, ErrOIDCBusy, and oidc.ErrProviderDown
// or oidc.ErrProviderConfig (or *oidc.IssuerMismatchError, *oidc.AlgError) from discovery.
func (a *Service) OIDCStart(ctx context.Context, purpose, next string, ip netip.Addr, sess *Session) (authURL, state string, err error) {
	if a.oidc == nil {
		return "", "", ErrOIDCOff
	}
	key := IPKey(ip)
	if err := limited(a.oidcStart.Allow(key)); err != nil {
		return "", "", err
	}
	a.oidcStart.Fail(key) // every start costs one of the address's thirty
	if purpose != "signin" && purpose != "link" {
		return "", "", ErrOIDCOff
	}
	if purpose == "link" && (sess == nil || sess.DeviceID != "") {
		return "", "", ErrOIDCOff
	}
	cfg, err := a.oidcConfig(ctx)
	if err != nil {
		return "", "", err
	}
	var open, mine int
	if err := a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM oidc_logins WHERE expires_at < ?`, a.ms()); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM oidc_logins WHERE addr_key = ?`, key).Scan(&mine); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, `SELECT count(*) FROM oidc_logins`).Scan(&open)
	}); err != nil {
		return "", "", err
	}
	if open >= oidcOpenMax || mine >= oidcOpenPerAddr {
		return "", "", ErrOIDCBusy
	}
	dctx, cancel := context.WithTimeout(ctx, oidcBudget)
	defer cancel()
	p, err := a.oidc.Discover(dctx, cfg.Issuer)
	if err != nil {
		return "", "", err
	}
	state, nonce, verifier := oidc.RandomToken(), oidc.RandomToken(), oidc.RandomToken()
	hash, _ := token.Hash(state)
	now := a.ms()
	var userID, sessionHash any
	if purpose == "link" {
		userID, sessionHash = sess.UserID, sess.Hash[:]
	}
	err = a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO oidc_logins (id, state_hash, purpose, issuer, nonce, verifier, next, user_id, session_hash, created_at, expires_at, addr_key)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, db.NewID("oil"), hash[:], purpose, cfg.Issuer, nonce, verifier, SafeNext(next),
			userID, sessionHash, now, now+oidcRowTTL.Milliseconds(), key)
		return err
	})
	if err != nil {
		return "", "", err
	}
	return oidc.AuthorizeURL(p, cfg, state, nonce, verifier), state, nil
}

type oidcRow struct {
	id, purpose, issuer, nonce, verifier, next string
	userID                                     string
	sessionHash                                []byte
}

// consumeLogin makes the row for a state hash single-use before anything else happens: a sign-in
// row is deleted, a link row is stamped used (it stays for the confirming POST). An expired row is
// deleted and not returned.
func (a *Service) consumeLogin(ctx context.Context, hash [32]byte) (oidcRow, bool, error) {
	var r oidcRow
	found := false
	err := a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		now := a.ms()
		if _, err := tx.ExecContext(ctx, `DELETE FROM oidc_logins WHERE state_hash = ? AND expires_at <= ?`, hash[:], now); err != nil {
			return err
		}
		const cols = ` RETURNING id, purpose, issuer, nonce, verifier, next, coalesce(user_id, ''), session_hash`
		for _, q := range []string{
			`DELETE FROM oidc_logins WHERE state_hash = ? AND purpose = 'signin' AND expires_at > ?` + cols,
			`UPDATE oidc_logins SET used_at = ? WHERE state_hash = ? AND purpose = 'link' AND used_at IS NULL AND expires_at > ?` + cols,
		} {
			args := []any{hash[:], now}
			if strings.HasPrefix(q, "UPDATE") {
				args = []any{now, hash[:], now}
			}
			err := tx.QueryRowContext(ctx, q, args...).Scan(&r.id, &r.purpose, &r.issuer, &r.nonce, &r.verifier, &r.next, &r.userID, &r.sessionHash)
			if err == nil {
				found = true
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		return nil
	})
	return r, found, err
}

func (a *Service) dropLogin(ctx context.Context, id string) {
	if id != "" {
		_, _ = a.st.Writer().ExecContext(context.WithoutCancel(ctx), `DELETE FROM oidc_logins WHERE id = ?`, id)
	}
}

// oidcFailed costs the address one try, records why (never a code, token or state) and drops the row.
func (a *Service) oidcFailed(ctx context.Context, ip netip.Addr, reason, rowID string) {
	a.ipLim.Fail(IPKey(ip))
	a.dropLogin(ctx, rowID)
	a.audit(context.WithoutCancel(ctx), db.AuditEntry{Action: "auth.oidc_failed", Detail: map[string]string{"ip": ip.String(), "reason": reason}})
}

// oidcTrouble records a provider that did not answer or answered wrongly. That is not the person's
// fault, so the address is not charged.
func (a *Service) oidcTrouble(ctx context.Context, ip netip.Addr, err error, rowID string) error {
	a.dropLogin(ctx, rowID)
	reason := "provider_down"
	if !errors.Is(err, oidc.ErrProviderDown) {
		reason = "provider_config"
	}
	a.audit(context.WithoutCancel(ctx), db.AuditEntry{Action: "auth.oidc_failed", Detail: map[string]string{"ip": ip.String(), "reason": reason}})
	return err
}

// OIDCFinish handles the provider's redirect back. See the plan for the order; in short: the address
// is checked first, then the state against the cookie and the row (single use), then the exchange
// and the ID token, then the account. A failure of the person's side is ErrOIDCFailed (with a reason
// in the audit log), ErrOIDCNoAccount, ErrOIDCEmailTaken or ErrOIDCCancelled; a provider that does
// not answer is oidc.ErrProviderDown or oidc.ErrProviderConfig; a limited address is
// *RateLimitedError before any query. prev is the browser's current session, if any.
func (a *Service) OIDCFinish(ctx context.Context, q OIDCCallback, ip netip.Addr, ua string, prev *Session) (OIDCResult, error) {
	if a.oidc == nil {
		return OIDCResult{}, ErrOIDCOff
	}
	if err := a.InviteAllowed(ip); err != nil {
		return OIDCResult{}, err
	}
	cfg, err := a.oidcConfig(ctx)
	if err != nil {
		return OIDCResult{}, err
	}
	hash, hashOK := token.Hash(q.State)
	var row oidcRow
	found := false
	// The cookie must match the state before the row is touched, so a request without the browser's
	// cookie cannot burn a sign-in that is still under way.
	cookieOK := q.Cookie != "" && subtle.ConstantTimeCompare([]byte(q.State), []byte(q.Cookie)) == 1
	if hashOK && cookieOK {
		if row, found, err = a.consumeLogin(ctx, hash); err != nil {
			return OIDCResult{}, err
		}
	}
	if !hashOK || !found || !cookieOK {
		a.oidcFailed(ctx, ip, "state", row.id)
		return OIDCResult{}, ErrOIDCFailed
	}
	switch {
	case row.issuer != cfg.Issuer:
		a.oidcFailed(ctx, ip, "issuer", row.id)
		return OIDCResult{}, ErrOIDCFailed
	case row.purpose == "link" && (prev == nil || prev.DeviceID != "" || subtle.ConstantTimeCompare(prev.Hash[:], row.sessionHash) != 1):
		a.oidcFailed(ctx, ip, "session", row.id)
		return OIDCResult{}, ErrOIDCFailed
	case q.Error != "":
		if q.Error == "access_denied" {
			a.dropLogin(ctx, row.id)
			return OIDCResult{}, ErrOIDCCancelled
		}
		a.oidcFailed(ctx, ip, "provider_error", row.id)
		return OIDCResult{}, ErrOIDCFailed
	case q.Code == "" || len(q.Code) > 2048:
		a.oidcFailed(ctx, ip, "code", row.id)
		return OIDCResult{}, ErrOIDCFailed
	}
	cctx, cancel := context.WithTimeout(ctx, oidcBudget)
	defer cancel()
	p, err := a.oidc.Discover(cctx, cfg.Issuer)
	if err != nil {
		return OIDCResult{}, a.oidcTrouble(ctx, ip, err, row.id)
	}
	claims, err := a.oidc.Exchange(cctx, p, cfg, q.Code, row.verifier, row.nonce)
	var te *oidc.TokenError
	switch {
	case errors.As(err, &te):
		a.oidcFailed(ctx, ip, te.Reason, row.id)
		return OIDCResult{}, ErrOIDCFailed
	case errors.Is(err, oidc.ErrProviderRefused):
		a.oidcFailed(ctx, ip, "token_refused", row.id)
		return OIDCResult{}, ErrOIDCFailed
	case err != nil:
		return OIDCResult{}, a.oidcTrouble(ctx, ip, err, row.id)
	}
	if row.purpose == "link" {
		now := a.ms()
		_, err := a.st.Writer().ExecContext(ctx, `UPDATE oidc_logins SET subject = ?, email = ?, email_verified = ?, expires_at = ? WHERE id = ?`,
			claims.Subject, nullIfEmpty(claims.Email), boolInt(claims.EmailVerified), now+oidcRowTTL.Milliseconds(), row.id)
		if err != nil {
			return OIDCResult{}, err
		}
		return OIDCResult{LinkPending: row.id, Next: row.next}, nil
	}
	var out Login
	var failure error
	var reason string
	err = a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := a.sessions.DeleteExpired(ctx, tx); err != nil {
			return err
		}
		now := a.ms()
		uid, via, err := a.oidcResolve(ctx, tx, cfg, claims, now)
		var re resolveError
		if errors.As(err, &re) {
			failure, reason = re.err, re.reason
			return err
		}
		if err != nil {
			return err
		}
		if prev != nil {
			if err := a.sessions.Delete(ctx, tx, prev.Hash); err != nil {
				return err
			}
		}
		stok, _, err := a.sessions.Create(ctx, tx, uid, ua)
		if err != nil {
			return err
		}
		if err := a.users.TouchLastSeen(ctx, tx, uid, now); err != nil {
			return err
		}
		u, err := a.users.ByIDTx(ctx, tx, uid)
		if err != nil {
			return err
		}
		u.LastSeenAt = now
		out = Login{Token: stok, User: u}
		if via != "" {
			if err := db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: uid, Action: "auth.oidc_linked", Target: uid,
				Detail: map[string]string{"ip": ip.String(), "via": via}}); err != nil {
				return err
			}
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: uid, Action: "auth.login", Target: uid,
			Detail: map[string]string{"ip": ip.String(), "via": "oidc"}})
	})
	if failure != nil {
		a.oidcFailed(ctx, ip, reason, "")
		return OIDCResult{}, failure
	}
	if err != nil {
		return OIDCResult{}, err
	}
	return OIDCResult{Login: out, Next: row.next}, nil
}

// resolveError is a refusal decided inside the transaction; reason goes to the audit log.
type resolveError struct {
	err    error
	reason string
}

func (e resolveError) Error() string { return e.err.Error() }
func (e resolveError) Unwrap() error { return e.err }

// oidcResolve finds or makes the account for a verified provider identity (decision 0037 points 6
// and 7). via is "" for an identity already linked, else account/email/new for the link just made.
func (a *Service) oidcResolve(ctx context.Context, tx *sql.Tx, cfg oidc.Config, c oidc.Claims, now int64) (userID, via string, err error) {
	var disabled bool
	err = tx.QueryRowContext(ctx, `SELECT i.user_id, u.disabled FROM oidc_identities i JOIN users u ON u.id = i.user_id
		WHERE i.issuer = ? AND i.subject = ?`, cfg.Issuer, c.Subject).Scan(&userID, &disabled)
	switch {
	case err == nil:
		if disabled {
			return "", "", resolveError{ErrOIDCFailed, "disabled"}
		}
		_, err = tx.ExecContext(ctx, `UPDATE oidc_identities SET last_used_at = ? WHERE issuer = ? AND subject = ?`, now, cfg.Issuer, c.Subject)
		return userID, "", err
	case !errors.Is(err, sql.ErrNoRows):
		return "", "", err
	}
	email := asciiLower(strings.TrimSpace(c.Email))
	verified := c.EmailVerified && email != ""
	type holder struct {
		id, role string
		disabled bool
		linked   bool
	}
	var holders []holder
	if verified {
		rows, err := tx.QueryContext(ctx, `SELECT u.id, u.role, u.disabled, EXISTS(SELECT 1 FROM oidc_identities i WHERE i.user_id = u.id AND i.issuer = ?)
			FROM users u WHERE lower(u.email) = ? LIMIT 3`, cfg.Issuer, email)
		if err != nil {
			return "", "", err
		}
		for rows.Next() {
			var h holder
			if err := rows.Scan(&h.id, &h.role, &h.disabled, &h.linked); err != nil {
				rows.Close()
				return "", "", err
			}
			holders = append(holders, h)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return "", "", err
		}
		rows.Close()
	}
	// Exactly one enabled member or guest, with no provider account yet; never an owner or an admin.
	if cfg.MatchEmail && len(holders) == 1 {
		h := holders[0]
		if (users.Role(h.role) == users.RoleMember || users.Role(h.role) == users.RoleGuest) && !h.disabled && !h.linked {
			return h.id, "email", a.oidcLink(ctx, tx, cfg.Issuer, c, h.id, "email", now)
		}
	}
	if len(holders) > 0 {
		return "", "", resolveError{ErrOIDCEmailTaken, "email_taken"}
	}
	if !cfg.CreateAccounts {
		return "", "", resolveError{ErrOIDCNoAccount, "no_account"}
	}
	name := c.Name
	if name == "" {
		name = c.PreferredUsername
	}
	if name == "" {
		name = "Reader"
	}
	nu := users.NewUser{DisplayName: name, Role: cfg.NewRole}
	// A non-ASCII address is never stored: SQLite folds ASCII only, so a later match could not
	// tell it from a stored address that differs in the case of a non-ASCII letter.
	if verified && isASCII(email) {
		nu.Email = email
	}
	u, err := a.users.CreateInvited(ctx, tx, nu, "")
	if err != nil {
		return "", "", err
	}
	return u.ID, "new", a.oidcLink(ctx, tx, cfg.Issuer, c, u.ID, "new", now)
}

// asciiLower folds A-Z only, exactly what SQLite's lower() does, so Go and SQL compare the same
// way (Unicode folding would turn the Kelvin sign into "k" on one side only).
func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// oidcLink writes the identity row.
func (a *Service) oidcLink(ctx context.Context, tx *sql.Tx, issuer string, c oidc.Claims, userID, via string, now int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO oidc_identities (issuer, subject, user_id, email, linked_via, created_at, last_used_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`, issuer, c.Subject, userID, nullIfEmpty(c.Email), via, now, now)
	return err
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// oidcLinkRow is a link step read for the confirm page or the confirming POST.
type oidcLinkRow struct {
	subject, email string
}

// readOIDCLink returns the used, unexpired link step bound to sess, or ErrOIDCLinkGone.
func (a *Service) readOIDCLink(ctx context.Context, q db.RowQueryer, rowID string, sess Session) (oidcLinkRow, string, error) {
	var r oidcLinkRow
	if sess.DeviceID != "" {
		return r, "", ErrOIDCLinkGone
	}
	var issuer string
	var email sql.NullString
	err := q.QueryRowContext(ctx, `SELECT issuer, subject, email FROM oidc_logins WHERE id = ? AND purpose = 'link' AND used_at IS NOT NULL
		AND subject IS NOT NULL AND expires_at > ? AND session_hash = ? AND user_id = ?`, rowID, a.ms(), sess.Hash[:], sess.UserID).Scan(&issuer, &r.subject, &email)
	if errors.Is(err, sql.ErrNoRows) {
		return oidcLinkRow{}, "", ErrOIDCLinkGone
	}
	r.email = email.String
	return r, issuer, err
}

// OIDCLinkView returns what the confirm page names: the provider account's email, or its subject
// when it sent none. It changes nothing.
func (a *Service) OIDCLinkView(ctx context.Context, rowID string, sess Session) (string, error) {
	r, _, err := a.readOIDCLink(ctx, a.st.Reader(), rowID, sess)
	if err != nil {
		return "", err
	}
	if r.email != "" {
		return r.email, nil
	}
	return r.subject, nil
}

// OIDCConfirmLink links the provider account the callback found to the signed-in person, from the
// session that started the step. Errors: ErrOIDCLinkGone, ErrOIDCTaken (the provider account belongs
// to someone else), ErrOIDCAlready (the person already has one for this provider).
func (a *Service) OIDCConfirmLink(ctx context.Context, rowID string, sess Session) error {
	var failure error
	err := a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		r, issuer, err := a.readOIDCLink(ctx, tx, rowID, sess)
		if err != nil {
			failure = err
			return err
		}
		var owner string
		err = tx.QueryRowContext(ctx, `SELECT user_id FROM oidc_identities WHERE issuer = ? AND subject = ?`, issuer, r.subject).Scan(&owner)
		switch {
		case err == nil && owner != sess.UserID:
			failure = ErrOIDCTaken
			return failure
		case err == nil:
			failure = ErrOIDCAlready
			return failure
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM oidc_identities WHERE user_id = ? AND issuer = ?`, sess.UserID, issuer).Scan(&n); err != nil {
			return err
		}
		if n > 0 {
			failure = ErrOIDCAlready
			return failure
		}
		now := a.ms()
		if err := a.oidcLink(ctx, tx, issuer, oidc.Claims{Subject: r.subject, Email: r.email}, sess.UserID, "account", now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM oidc_logins WHERE id = ?`, rowID); err != nil {
			return err
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: sess.UserID, Action: "auth.oidc_linked", Target: sess.UserID,
			Detail: map[string]string{"via": "account"}})
	})
	if failure != nil {
		if !errors.Is(failure, ErrOIDCLinkGone) {
			a.dropLogin(ctx, rowID) // a refused step is finished
		}
		return failure
	}
	return err
}

// OIDCUnlink removes the person's provider account. An account without a password keeps it: the
// provider would be its only way in.
func (a *Service) OIDCUnlink(ctx context.Context, userID string) error {
	var failure error
	err := a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var hasPW bool
		if err := tx.QueryRowContext(ctx, `SELECT password_hash IS NOT NULL FROM users WHERE id = ?`, userID).Scan(&hasPW); err != nil {
			return err
		}
		if !hasPW {
			failure = ErrOIDCLastWayIn
			return failure
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM oidc_identities WHERE user_id = ?`, userID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			failure = ErrOIDCNotLinked
			return failure
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: a.ms(), UserID: userID, Action: "auth.oidc_unlinked", Target: userID})
	})
	if failure != nil {
		return failure
	}
	return err
}

// OIDCIdentity returns the person's linked provider account, if any.
func (a *Service) OIDCIdentity(ctx context.Context, userID string) (oidc.Identity, bool, error) {
	var id oidc.Identity
	var email sql.NullString
	var used sql.NullInt64
	err := a.st.Reader().QueryRowContext(ctx, `SELECT issuer, subject, email, linked_via, created_at, last_used_at FROM oidc_identities
		WHERE user_id = ? ORDER BY created_at LIMIT 1`, userID).Scan(&id.Issuer, &id.Subject, &email, &id.LinkedVia, &id.CreatedAt, &used)
	if errors.Is(err, sql.ErrNoRows) {
		return oidc.Identity{}, false, nil
	}
	id.Email, id.LastUsedAt = email.String, used.Int64
	return id, err == nil, err
}
