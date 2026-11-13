package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/auth/passkey"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// Passkeys (decision 0041). A passkey sign-in ends in the same sessions row as a password one,
// through the same address and account limiters.
const (
	passkeyTTL      = 5 * time.Minute
	passkeyStartMax = 30  // sign-in starts per address in limitWindow
	passkeyOpenMax  = 500 // ceremonies open at once
	passkeyPerUser  = 20
	passkeyFresh    = 15 * time.Minute
	passkeyNameMax  = 64 // runes
)

// Errors of the passkey flows. Sign-in failures are all ErrBadCredentials.
var (
	// ErrPasskeyBusy means too many ceremonies are open; try again in a few minutes.
	ErrPasskeyBusy = errors.New("auth: too many passkey sign-ins under way")
	// ErrPasskeyStale means the account has no password and its session is older than 15 minutes.
	ErrPasskeyStale = errors.New("auth: sign in again to add a passkey")
	// ErrPasskeyLimit means the account already holds 20 passkeys.
	ErrPasskeyLimit = errors.New("auth: the account has the most passkeys it can hold")
	// ErrPasskeyTaken means this credential is already stored.
	ErrPasskeyTaken = errors.New("auth: this passkey is already on an account")
	// ErrPasskeyLast means the passkey is the account's only way to sign in.
	ErrPasskeyLast = errors.New("auth: this passkey is the only way to sign in")
	// ErrPasskeyFailed is the one answer to every registration that cannot finish: unknown,
	// expired or foreign challenge, a failed check, a bad statement.
	ErrPasskeyFailed = errors.New("auth: the passkey could not be added")
)

// PasskeyAssertion is what the browser returned from navigator.credentials.get, decoded.
type PasskeyAssertion struct {
	RawID, UserHandle, ClientDataJSON, AuthenticatorData, Signature []byte
}

// RegisterOptions is what the browser needs to start navigator.credentials.create.
type RegisterOptions struct {
	Challenge   []byte
	UserHandle  []byte
	Username    string
	DisplayName string
	ExcludeIDs  [][]byte // this account's passkeys made for the same relying-party id
}

// PasskeyRow is one passkey as the account page lists it. LastUsedAt and CopiedAt are 0 when unset.
type PasskeyRow struct {
	ID             string
	Name           string
	CreatedAt      int64
	LastUsedAt     int64
	CopiedAt       int64
	BackupEligible bool
	RPID           string
}

func challengeHash(ch []byte) []byte { h := sha256.Sum256(ch); return h[:] }

// PasskeyBegin errors map one-to-one onto audit reasons.
func passkeyReason(err error) string {
	switch {
	case errors.Is(err, passkey.ErrType):
		return "type"
	case errors.Is(err, passkey.ErrChallenge):
		return "challenge"
	case errors.Is(err, passkey.ErrOrigin):
		return "origin"
	case errors.Is(err, passkey.ErrCrossOrigin):
		return "cross_origin"
	case errors.Is(err, passkey.ErrRPID):
		return "rp_id"
	case errors.Is(err, passkey.ErrPresence):
		return "presence"
	case errors.Is(err, passkey.ErrVerification):
		return "verification"
	case errors.Is(err, passkey.ErrBackupState):
		return "backup_state"
	case errors.Is(err, passkey.ErrAlgorithm):
		return "algorithm"
	case errors.Is(err, passkey.ErrKey):
		return "key"
	case errors.Is(err, passkey.ErrSignature):
		return "signature"
	case errors.Is(err, passkey.ErrCounter):
		return "counter"
	}
	return "malformed"
}

// PasskeySignInBegin issues a sign-in challenge for rpID and origin. Errors: *RateLimitedError,
// ErrPasskeyBusy.
func (a *Service) PasskeySignInBegin(ctx context.Context, rpID, origin string, ip netip.Addr) ([]byte, error) {
	key := IPKey(ip)
	if err := limited(a.pkStart.Allow(key)); err != nil {
		return nil, err
	}
	a.pkStart.Fail(key) // every start costs one of the address's thirty
	ch := passkey.NewChallenge()
	now := a.ms()
	err := a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM passkey_ceremonies WHERE expires_at < ?`, now); err != nil {
			return err
		}
		var open int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM passkey_ceremonies`).Scan(&open); err != nil {
			return err
		}
		if open >= passkeyOpenMax {
			return ErrPasskeyBusy
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO passkey_ceremonies (challenge_hash, purpose, rp_id, origin, created_at, expires_at, addr_key)
			VALUES (?, 'signin', ?, ?, ?, ?, ?)`, challengeHash(ch), rpID, origin, now, now+passkeyTTL.Milliseconds(), key)
		return err
	})
	if err != nil {
		return nil, err
	}
	return ch, nil
}

// PasskeySignIn finishes a sign-in with a passkey and starts a session like Login does. prev, when
// not nil, is the session the browser already had; it is ended. Every failure is ErrBadCredentials
// (audited as auth.passkey_failed with a reason); limits answer with *RateLimitedError before any
// database read.
func (a *Service) PasskeySignIn(ctx context.Context, in PasskeyAssertion, rpID, origin string, ip netip.Addr, ua string, prev *Session) (Login, error) {
	ipKey := IPKey(ip)
	if err := limited(a.ipLim.Allow(ipKey)); err != nil {
		return Login{}, err
	}
	userID := ""
	fail := func(reason string) error {
		a.ipLim.Fail(ipKey)
		if userID != "" {
			a.userLim.Fail("u:" + userID)
		}
		a.audit(ctx, db.AuditEntry{UserID: userID, Action: "auth.passkey_failed", Detail: map[string]string{"ip": ip.String(), "reason": reason}})
		return ErrBadCredentials
	}
	if len(in.RawID) < 16 || len(in.RawID) > 1023 || len(in.UserHandle) != 32 || len(in.Signature) > 2048 ||
		len(in.AuthenticatorData) > 2048 {
		return Login{}, fail("malformed")
	}
	challenge, err := passkey.ChallengeOf(in.ClientDataJSON)
	if err != nil {
		return Login{}, fail("malformed")
	}

	// The ceremony row is burned first, whatever happens next.
	var okRow bool
	err = a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var purpose, rp, org string
		var exp int64
		err := tx.QueryRowContext(ctx, `DELETE FROM passkey_ceremonies WHERE challenge_hash = ? RETURNING purpose, rp_id, origin, expires_at`,
			challengeHash(challenge)).Scan(&purpose, &rp, &org, &exp)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		okRow = err == nil && purpose == "signin" && rp == rpID && org == origin && exp > a.ms()
		return err
	})
	if err != nil {
		return Login{}, err
	}
	if !okRow {
		return Login{}, fail("challenge")
	}

	var (
		pkID, uid        string
		pub              []byte
		alg              int
		count            int64
		be, bs, disabled bool
		copiedAt         sql.NullInt64
	)
	err = a.st.Reader().QueryRowContext(ctx, `SELECT p.id, p.user_id, p.public_key, p.alg, p.sign_count, p.backup_eligible, p.backed_up, p.copied_at, u.disabled
		FROM passkeys p JOIN users u ON u.id = p.user_id
		WHERE p.credential_id = ? AND u.passkey_handle = ? AND p.rp_id = ?`, in.RawID, in.UserHandle, rpID).
		Scan(&pkID, &uid, &pub, &alg, &count, &be, &bs, &copiedAt, &disabled)
	if errors.Is(err, sql.ErrNoRows) {
		return Login{}, fail("unknown")
	}
	if err != nil {
		return Login{}, err
	}
	userID = uid
	lim := "u:" + uid
	if err := limited(a.userLim.Allow(lim)); err != nil {
		return Login{}, err
	}
	cred := passkey.Credential{ID: in.RawID, PublicKey: pub, Alg: alg, SignCount: uint32(count), BackupEligible: be, BackedUp: bs}
	res, verr := passkey.VerifyAssertion(passkey.AssertionInput{
		ClientDataJSON: in.ClientDataJSON, AuthenticatorData: in.AuthenticatorData, Signature: in.Signature,
		Challenge: challenge, Origin: origin, RPID: rpID,
	}, cred)
	switch {
	case errors.Is(verr, passkey.ErrCounter):
		now := a.ms()
		_, _ = a.st.Writer().ExecContext(ctx, `UPDATE passkeys SET copied_at = ? WHERE id = ? AND copied_at IS NULL`, now, pkID)
		a.audit(ctx, db.AuditEntry{UserID: uid, Action: "passkey.copied", Target: pkID})
		return Login{}, fail("counter")
	case verr != nil:
		return Login{}, fail(passkeyReason(verr))
	case copiedAt.Valid:
		return Login{}, fail("copied")
	case disabled:
		return Login{}, fail("disabled")
	}

	var out Login
	var gone bool
	err = a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := a.sessions.DeleteExpired(ctx, tx); err != nil {
			return err
		}
		u, err := a.users.ByIDTx(ctx, tx, uid)
		if errors.Is(err, users.ErrNotFound) || (err == nil && u.Disabled) {
			gone = true
			return nil
		}
		if err != nil {
			return err
		}
		if prev != nil {
			if err := a.sessions.Delete(ctx, tx, prev.Hash); err != nil {
				return err
			}
		}
		tok, _, err := a.sessions.Create(ctx, tx, uid, ua)
		if err != nil {
			return err
		}
		now := a.ms()
		if _, err := tx.ExecContext(ctx, `UPDATE passkeys SET sign_count = ?, backed_up = ?, last_used_at = ? WHERE id = ?`,
			int64(res.SignCount), boolInt(res.BackedUp), now, pkID); err != nil {
			return err
		}
		if err := a.users.TouchLastSeen(ctx, tx, uid, now); err != nil {
			return err
		}
		u.LastSeenAt = now
		out = Login{Token: tok, User: u}
		return db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: uid, Action: "auth.login", Target: uid,
			Detail: map[string]string{"ip": ip.String(), "via": "passkey"}})
	})
	if err != nil {
		return Login{}, err
	}
	if gone {
		return Login{}, fail("disabled")
	}
	a.userLim.Reset(lim)
	return out, nil
}

// hasWayIn reports whether the account can sign in without a passkey: a password or a provider
// identity.
func hasWayIn(ctx context.Context, q db.RowQueryer, userID string) (password, other bool, err error) {
	err = q.QueryRowContext(ctx, `SELECT coalesce(password_hash, '') <> '',
		EXISTS (SELECT 1 FROM oidc_identities WHERE user_id = users.id) FROM users WHERE id = ?`, userID).Scan(&password, &other)
	return
}

// PasskeyRegisterBegin starts adding a passkey for u, who is signed in with sess. An account with a
// password must give it (ErrBadCredentials or *RateLimitedError); one without needs a session
// started in the last 15 minutes (ErrPasskeyStale). Other errors: ErrPasskeyLimit.
func (a *Service) PasskeyRegisterBegin(ctx context.Context, sess Session, u users.User, password, rpID, origin string, ip netip.Addr) (RegisterOptions, error) {
	hasPW, _, err := hasWayIn(ctx, a.st.Reader(), u.ID)
	if err != nil {
		return RegisterOptions{}, err
	}
	lim := ""
	if hasPW {
		_, lim, err = a.checkPassword(ctx, u.Username, password, ip)
		if err != nil {
			return RegisterOptions{}, err
		}
	} else if a.now().Sub(time.UnixMilli(sess.CreatedAt)) > passkeyFresh {
		return RegisterOptions{}, ErrPasskeyStale
	}
	var opts RegisterOptions
	now := a.ms()
	err = a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM passkeys WHERE user_id = ?`, u.ID).Scan(&n); err != nil {
			return err
		}
		if n >= passkeyPerUser {
			return ErrPasskeyLimit
		}
		handle := make([]byte, 32)
		if _, err := rand.Read(handle); err != nil {
			return err
		}
		// First registration makes the handle; later ones keep it.
		if _, err := tx.ExecContext(ctx, `UPDATE users SET passkey_handle = ? WHERE id = ? AND passkey_handle IS NULL`, handle, u.ID); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT passkey_handle FROM users WHERE id = ?`, u.ID).Scan(&handle); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT credential_id FROM passkeys WHERE user_id = ? AND rp_id = ?`, u.ID, rpID)
		if err != nil {
			return err
		}
		var exclude [][]byte
		for rows.Next() {
			var id []byte
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			exclude = append(exclude, id)
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		// One open registration per session keeps the table bounded.
		if _, err := tx.ExecContext(ctx, `DELETE FROM passkey_ceremonies WHERE expires_at < ? OR (purpose = 'register' AND session_hash = ?)`, now, sess.Hash[:]); err != nil {
			return err
		}
		ch := passkey.NewChallenge()
		if _, err := tx.ExecContext(ctx, `INSERT INTO passkey_ceremonies (challenge_hash, purpose, rp_id, origin, user_id, session_hash, created_at, expires_at, addr_key)
			VALUES (?, 'register', ?, ?, ?, ?, ?, ?, ?)`, challengeHash(ch), rpID, origin, u.ID, sess.Hash[:], now, now+passkeyTTL.Milliseconds(), IPKey(ip)); err != nil {
			return err
		}
		opts = RegisterOptions{Challenge: ch, UserHandle: handle, Username: u.Username, DisplayName: u.DisplayName, ExcludeIDs: exclude}
		return nil
	})
	if err != nil {
		return RegisterOptions{}, err
	}
	if lim != "" {
		a.userLim.Reset(lim)
	}
	return opts, nil
}

// PasskeyRegister finishes adding a passkey. in carries ClientDataJSON, AttestationObject,
// Transports, and this request's Origin and RPID; the challenge is read from the client data and
// matched to the row PasskeyRegisterBegin made for this session. Errors: ErrPasskeyFailed,
// ErrPasskeyLimit, ErrPasskeyTaken, users.FieldErrors{"name": "too_long"}.
func (a *Service) PasskeyRegister(ctx context.Context, sess Session, u users.User, name string, in passkey.RegistrationInput) (PasskeyRow, error) {
	name = strings.TrimSpace(name)
	if utf8.RuneCountInString(name) > passkeyNameMax {
		return PasskeyRow{}, users.FieldErrors{"name": "too_long"}
	}
	challenge, err := passkey.ChallengeOf(in.ClientDataJSON)
	if err != nil {
		return PasskeyRow{}, ErrPasskeyFailed
	}
	var row PasskeyRow
	err = a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var purpose, rp, org, uid string
		var sh []byte
		var exp int64
		err := tx.QueryRowContext(ctx, `DELETE FROM passkey_ceremonies WHERE challenge_hash = ?
			RETURNING purpose, rp_id, origin, coalesce(user_id, ''), session_hash, expires_at`, challengeHash(challenge)).Scan(&purpose, &rp, &org, &uid, &sh, &exp)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrPasskeyFailed
		}
		if err != nil {
			return err
		}
		if purpose != "register" || rp != in.RPID || org != in.Origin || uid != u.ID || string(sh) != string(sess.Hash[:]) || exp <= a.ms() {
			return ErrPasskeyFailed
		}
		in.Challenge = challenge
		cred, err := passkey.VerifyRegistration(in)
		if err != nil {
			return ErrPasskeyFailed
		}
		var n, dup int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM passkeys WHERE user_id = ?`, u.ID).Scan(&n); err != nil {
			return err
		}
		if n >= passkeyPerUser {
			return ErrPasskeyLimit
		}
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM passkeys WHERE credential_id = ?`, cred.ID).Scan(&dup); err != nil {
			return err
		}
		if dup > 0 {
			return ErrPasskeyTaken
		}
		if name == "" {
			name = "Passkey " + strconv.Itoa(n+1)
		}
		now := a.ms()
		id := db.NewID("pk")
		if _, err := tx.ExecContext(ctx, `INSERT INTO passkeys (id, user_id, credential_id, rp_id, public_key, alg, sign_count, backup_eligible, backed_up,
			transports, aaguid, name, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, u.ID, cred.ID, rp, cred.PublicKey, cred.Alg, int64(cred.SignCount), boolInt(cred.BackupEligible), boolInt(cred.BackedUp),
			strings.Join(cred.Transports, ","), cred.AAGUID, name, now); err != nil {
			return err
		}
		row = PasskeyRow{ID: id, Name: name, CreatedAt: now, BackupEligible: cred.BackupEligible, RPID: rp}
		return db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: u.ID, Action: "passkey.added", Target: id,
			Detail: map[string]string{"alg": strconv.Itoa(cred.Alg), "synced": strconv.Itoa(boolInt(cred.BackupEligible))}})
	})
	if err != nil {
		// Any failed finish spends its challenge (a refused answer, the limit, a key already in use);
		// the transaction rolled back the delete, so burn it again outside.
		_, _ = a.st.Writer().ExecContext(ctx, `DELETE FROM passkey_ceremonies WHERE challenge_hash = ?`, challengeHash(challenge))
		return PasskeyRow{}, err
	}
	return row, nil
}

// Passkeys lists the person's passkeys, newest first.
func (a *Service) Passkeys(ctx context.Context, userID string) ([]PasskeyRow, error) {
	rows, err := a.st.Reader().QueryContext(ctx, `SELECT id, name, created_at, coalesce(last_used_at, 0), coalesce(copied_at, 0), backup_eligible, rp_id
		FROM passkeys WHERE user_id = ? ORDER BY created_at DESC, id DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PasskeyRow
	for rows.Next() {
		var r PasskeyRow
		if err := rows.Scan(&r.ID, &r.Name, &r.CreatedAt, &r.LastUsedAt, &r.CopiedAt, &r.BackupEligible, &r.RPID); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// PasskeyRemove deletes one of the person's own passkeys. Another person's id is users.ErrNotFound.
// The last passkey of an account with no password and no provider identity is ErrPasskeyLast.
func (a *Service) PasskeyRemove(ctx context.Context, userID, passkeyID string) error {
	return a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var n, own int
		if err := tx.QueryRowContext(ctx, `SELECT count(*), coalesce(sum(id = ?), 0) FROM passkeys WHERE user_id = ?`, passkeyID, userID).Scan(&n, &own); err != nil {
			return err
		}
		if own == 0 {
			return users.ErrNotFound
		}
		if n == 1 {
			pw, other, err := hasWayIn(ctx, tx, userID)
			if err != nil {
				return err
			}
			if !pw && !other {
				return ErrPasskeyLast
			}
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM passkeys WHERE id = ? AND user_id = ?`, passkeyID, userID); err != nil {
			return err
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: a.ms(), UserID: userID, Action: "passkey.removed", Target: passkeyID})
	})
}

// PasskeysRemoveAll deletes every passkey of userID for an admin, inside the caller's transaction.
// The caller has checked share.Can(ManageUsers) and rechecked inside tx. It returns how many went.
func (a *Service) PasskeysRemoveAll(ctx context.Context, tx *sql.Tx, actorID, userID string) (int, error) {
	res, err := tx.ExecContext(ctx, `DELETE FROM passkeys WHERE user_id = ?`, userID)
	if err != nil {
		return 0, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, err
	}
	err = db.Audit(ctx, tx, db.AuditEntry{At: a.ms(), UserID: actorID, Action: "passkey.removed_all", Target: userID,
		Detail: map[string]string{"count": strconv.FormatInt(n, 10)}})
	return int(n), err
}
