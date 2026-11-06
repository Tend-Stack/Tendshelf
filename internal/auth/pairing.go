package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"math/big"
	"net/netip"
	"strings"
	"time"
	"unicode"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// Pairings add a phone by QR code (decision 0028). A signed-in session shows a QR holding a one-time
// token. The token only claims: the first phone to claim it gets a 4-digit number on its own screen,
// the person types that number on the device that showed the QR, and only then can the phone finish.
// Finishing makes a session and an ordinary device code (device_codes), so the rest is the flow of
// decision 0026. Token, claim and code are stored as hashes. Every transition is one write
// transaction with a guarded statement, so two requests cannot both win.

// PairTTL is how long a pairing lives from the moment the QR is shown. A claim does not extend it.
const PairTTL = 3 * time.Minute

// Pairing is a pairing as the pages show it. Number is set only by ClaimedPairing, for the phone.
type Pairing struct {
	ID, State, Number, PhoneName, PhoneIP, PersonName string
	ExpiresAt                                         int64
}

// Pairing states.
const (
	PairWaiting  = "waiting"
	PairClaimed  = "claimed"
	PairApproved = "approved"
)

const maxPhoneName = 64

// CleanPhoneName trims a self-reported phone name, drops control characters, keeps 64 runes and
// falls back to "Phone".
func CleanPhoneName(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || !unicode.IsPrint(r) && !unicode.IsSpace(r) {
			return -1
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > maxPhoneName {
		s = strings.TrimSpace(string(r[:maxPhoneName]))
	}
	if s == "" {
		return "Phone"
	}
	return s
}

// matchNumber is a random 4-digit number, zero padded.
func matchNumber() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(10000))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%04d", n.Int64()), nil
}

// PairFailed counts one failed public pairing request against the address, like InviteFailed. The
// token is never recorded.
func (a *Service) PairFailed(ctx context.Context, ip netip.Addr, reason string) {
	a.ipLim.Fail(IPKey(ip))
	a.audit(ctx, db.AuditEntry{Action: "auth.pair_failed", Detail: map[string]string{"ip": ip.String(), "reason": reason}})
}

// PairAllowed is the cheap check before a public pairing request is looked at.
func (a *Service) PairAllowed(ip netip.Addr) error { return a.InviteAllowed(ip) }

// StartPairing makes a pairing for the signed-in session and returns the raw token for the QR. It
// first deletes expired pairings and this session's older one, so a session holds one live pairing.
func (a *Service) StartPairing(ctx context.Context, sess Session, ip netip.Addr) (Pairing, string, error) {
	tok, hash := NewToken()
	p := Pairing{ID: db.NewID("pr")}
	err := a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		now := a.ms()
		if _, err := tx.ExecContext(ctx, `DELETE FROM device_pairings WHERE expires_at < ? OR owner_session = ?`, now, sess.Hash[:]); err != nil {
			return err
		}
		p.State, p.ExpiresAt = PairWaiting, now+PairTTL.Milliseconds()
		if _, err := tx.ExecContext(ctx, `INSERT INTO device_pairings (id, token_hash, owner_session, state, created_at, expires_at)
			VALUES (?, ?, ?, 'waiting', ?, ?)`, p.ID, hash[:], sess.Hash[:], now, p.ExpiresAt); err != nil {
			return err
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: sess.UserID, Action: "auth.pair_started", Target: sess.UserID,
			Detail: map[string]string{"ip": ip.String()}})
	})
	if err != nil {
		return Pairing{}, "", err
	}
	return p, tok, nil
}

// PairingPending reports, without changing anything, whether tok names a pairing that is still
// waiting for a claim. Every other case is ErrPairEnded.
func (a *Service) PairingPending(ctx context.Context, tok string) error {
	hash, ok := HashToken(tok)
	if !ok {
		return ErrPairEnded
	}
	var n int
	err := a.st.Reader().QueryRowContext(ctx, `SELECT count(*) FROM device_pairings WHERE token_hash = ? AND state = 'waiting' AND expires_at >= ?`,
		hash[:], a.ms()).Scan(&n)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrPairEnded
	}
	return nil
}

// ClaimPairing lets the first phone with the token claim it: the pairing takes the phone's PKCE
// challenge, a random 4-digit number and the phone's name and address, and the phone gets a claim
// secret, returned raw, which goes into its HttpOnly cookie. A second claim finds the row no longer
// waiting. Anything that is not a live waiting pairing is ErrPairEnded.
func (a *Service) ClaimPairing(ctx context.Context, tok string, challenge [32]byte, name string, ip netip.Addr) (string, error) {
	hash, ok := HashToken(tok)
	if !ok {
		return "", ErrPairEnded
	}
	number, err := matchNumber()
	if err != nil {
		return "", err
	}
	claim, claimHash := NewToken()
	name = CleanPhoneName(name)
	var ended bool
	err = a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		now := a.ms()
		var id string
		err := tx.QueryRowContext(ctx, `UPDATE device_pairings SET state = 'claimed', claim_hash = ?, challenge = ?, match_code = ?, phone_name = ?, phone_ip = ?
			WHERE token_hash = ? AND state = 'waiting' AND expires_at >= ?
			RETURNING id`,
			claimHash[:], challenge[:], number, name, ip.String(), hash[:], now).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			ended = true
			return nil
		}
		if err != nil {
			return err
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: now, Action: "auth.pair_claimed", Detail: map[string]string{"ip": ip.String(), "name": name}})
	})
	if err != nil {
		return "", err
	}
	if ended {
		return "", ErrPairEnded
	}
	return claim, nil
}

// OwnerPairing reads a pairing for the session that showed it. Another session's pairing, even the
// same person's, an expired one and an unknown one are all ErrPairEnded.
func (a *Service) OwnerPairing(ctx context.Context, id string, sess Session) (Pairing, error) {
	var p Pairing
	var name, pip sql.NullString
	err := a.st.Reader().QueryRowContext(ctx, `SELECT id, state, phone_name, phone_ip, expires_at FROM device_pairings
		WHERE id = ? AND owner_session = ? AND expires_at >= ?`, id, sess.Hash[:], a.ms()).Scan(&p.ID, &p.State, &name, &pip, &p.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Pairing{}, ErrPairEnded
	}
	if err != nil {
		return Pairing{}, err
	}
	p.PhoneName, p.PhoneIP = name.String, pip.String
	return p, nil
}

// ApprovePairing checks the number the owner typed. Only the owning session may, and only for a
// claimed pairing. A wrong number deletes the pairing (one try) and is ErrPairMismatch. A right one
// moves it to approved; the phone then finishes. Anything else is ErrPairEnded.
func (a *Service) ApprovePairing(ctx context.Context, id string, sess Session, number string, ip netip.Addr) error {
	var result error
	err := a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		now := a.ms()
		var state, want string
		var expires int64
		err := tx.QueryRowContext(ctx, `SELECT state, coalesce(match_code, ''), expires_at FROM device_pairings WHERE id = ? AND owner_session = ?`,
			id, sess.Hash[:]).Scan(&state, &want, &expires)
		if errors.Is(err, sql.ErrNoRows) {
			result = ErrPairEnded
			return nil
		}
		if err != nil {
			return err
		}
		if now > expires || state != PairClaimed {
			result = ErrPairEnded
			return nil
		}
		if len(number) != len(want) || subtle.ConstantTimeCompare([]byte(number), []byte(want)) != 1 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM device_pairings WHERE id = ?`, id); err != nil {
				return err
			}
			result = ErrPairMismatch
			return db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: sess.UserID, Action: "auth.pair_refused", Target: sess.UserID,
				Detail: map[string]string{"ip": ip.String(), "reason": "number"}})
		}
		if _, err := tx.ExecContext(ctx, `UPDATE device_pairings SET state = 'approved' WHERE id = ? AND state = 'claimed'`, id); err != nil {
			return err
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: sess.UserID, Action: "auth.pair_approved", Target: sess.UserID,
			Detail: map[string]string{"ip": ip.String()}})
	})
	if err != nil {
		return err
	}
	return result
}

// CancelPairing deletes the owner's pairing ("Not my phone"). A pairing already gone is not an error.
func (a *Service) CancelPairing(ctx context.Context, id string, sess Session, ip netip.Addr) error {
	return a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM device_pairings WHERE id = ? AND owner_session = ?`, id, sess.Hash[:])
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil
		}
		return db.Audit(ctx, tx, db.AuditEntry{At: a.ms(), UserID: sess.UserID, Action: "auth.pair_refused", Target: sess.UserID,
			Detail: map[string]string{"ip": ip.String(), "reason": "declined"}})
	})
}

// ClaimedPairing reads a pairing for the phone that holds the claim secret. The number is returned
// while the pairing is claimed; PersonName is filled only once the owner approved. Anything else is
// ErrPairEnded.
func (a *Service) ClaimedPairing(ctx context.Context, claim string) (Pairing, error) {
	hash, ok := HashToken(claim)
	if !ok {
		return Pairing{}, ErrPairEnded
	}
	var p Pairing
	var number, name, pip, person sql.NullString
	err := a.st.Reader().QueryRowContext(ctx, `SELECT p.id, p.state, p.match_code, p.phone_name, p.phone_ip, p.expires_at, u.display_name
		FROM device_pairings p LEFT JOIN sessions s ON s.token_hash = p.owner_session LEFT JOIN users u ON u.id = s.user_id
		WHERE p.claim_hash = ? AND p.expires_at >= ?`, hash[:], a.ms()).Scan(&p.ID, &p.State, &number, &name, &pip, &p.ExpiresAt, &person)
	if errors.Is(err, sql.ErrNoRows) {
		return Pairing{}, ErrPairEnded
	}
	if err != nil {
		return Pairing{}, err
	}
	p.PhoneName, p.PhoneIP = name.String, pip.String
	if p.State == PairClaimed {
		p.Number = number.String
	}
	if p.State == PairApproved {
		p.PersonName = person.String
	}
	return p, nil
}

// FinishPairing is the phone's last tap. In one transaction it spends an approved pairing, checks that
// the session that showed the QR is still live and its person is not disabled, ends prev (the
// phone's own earlier session) and makes a new session plus a device code for the claim's PKCE
// challenge. prev bound to a device is ErrPairBound and nothing changes. Every other way of failing
// is ErrPairEnded and the pairing is spent.
func (a *Service) FinishPairing(ctx context.Context, claim, ua string, ip netip.Addr, prev *Session) (Login, string, error) {
	if prev != nil && prev.DeviceID != "" {
		return Login{}, "", ErrPairBound
	}
	hash, ok := HashToken(claim)
	if !ok {
		return Login{}, "", ErrPairEnded
	}
	var out Login
	var code string
	var refused error
	err := a.st.WriteTx(ctx, func(tx *sql.Tx) error {
		now := a.ms()
		var owner, challenge []byte
		err := tx.QueryRowContext(ctx, `DELETE FROM device_pairings WHERE claim_hash = ? AND state = 'approved' AND expires_at >= ?
			RETURNING owner_session, challenge`, hash[:], now).Scan(&owner, &challenge)
		if errors.Is(err, sql.ErrNoRows) {
			refused = ErrPairEnded
			return nil
		}
		if err != nil {
			return err
		}
		var userID string
		err = tx.QueryRowContext(ctx, `SELECT user_id FROM sessions WHERE token_hash = ? AND expires_at >= ?`, owner, now).Scan(&userID)
		if errors.Is(err, sql.ErrNoRows) {
			refused = ErrPairEnded
			return nil
		}
		if err != nil {
			return err
		}
		u, err := a.users.ByIDTx(ctx, tx, userID)
		if err != nil && !errors.Is(err, users.ErrNotFound) {
			return err
		}
		if err != nil || u.Disabled {
			refused = ErrPairEnded
			return nil
		}
		if prev != nil {
			if err := a.sessions.Delete(ctx, tx, prev.Hash); err != nil {
				return err
			}
		}
		tok, sess, err := a.sessions.Create(ctx, tx, u.ID, ua)
		if err != nil {
			return err
		}
		var ch [32]byte
		copy(ch[:], challenge)
		if code, err = a.insertDeviceCode(ctx, tx, sess.Hash, u.ID, ch, ip); err != nil {
			return err
		}
		if err := a.users.TouchLastSeen(ctx, tx, u.ID, now); err != nil {
			return err
		}
		u.LastSeenAt = now
		out = Login{Token: tok, User: u}
		return db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: u.ID, Action: "auth.login", Target: u.ID,
			Detail: map[string]string{"ip": ip.String(), "via": "pair"}})
	})
	if err != nil {
		return Login{}, "", err
	}
	if refused != nil {
		return Login{}, "", refused
	}
	return out, code, nil
}
