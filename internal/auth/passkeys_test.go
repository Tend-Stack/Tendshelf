package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth/passkey"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

const (
	pkRP     = "books.example"
	pkOrigin = "https://books.example"
)

// softKey is a software ES256 authenticator, just enough for the service flows. The ceremony
// rules themselves are tested in internal/auth/passkey with all three algorithms.
type softKey struct {
	key    *ecdsa.PrivateKey
	credID []byte
	handle []byte
	flags  byte
}

func newSoftKey(t *testing.T) *softKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	rand.Read(id)
	return &softKey{key: k, credID: id, flags: 0x01 | 0x04}
}

func cborHead(major byte, n int) []byte {
	if n < 24 {
		return []byte{major<<5 | byte(n)}
	}
	if n < 256 {
		return []byte{major<<5 | 24, byte(n)}
	}
	return []byte{major<<5 | 25, byte(n >> 8), byte(n)}
}

func cborBytes(b []byte) []byte { return append(cborHead(2, len(b)), b...) }
func cborText(s string) []byte  { return append(cborHead(3, len(s)), s...) }

func (s *softKey) cose() []byte {
	pt, _ := s.key.PublicKey.Bytes()
	out := []byte{0xa5, 0x01, 0x02, 0x03, 0x26, 0x20, 0x01, 0x21}
	out = append(out, cborBytes(pt[1:33])...)
	out = append(out, 0x22)
	return append(out, cborBytes(pt[33:])...)
}

func pkClient(typ string, ch []byte, origin string) []byte {
	return []byte(fmt.Sprintf(`{"type":%q,"challenge":%q,"origin":%q}`, typ, base64.RawURLEncoding.EncodeToString(ch), origin))
}

func rpSum(rp string) []byte { h := sha256.Sum256([]byte(rp)); return h[:] }

func (s *softKey) registration(ch []byte, rp, origin string) passkey.RegistrationInput {
	ad := append(rpSum(rp), s.flags|0x40)
	ad = binary.BigEndian.AppendUint32(ad, 0)
	ad = append(ad, make([]byte, 16)...)
	ad = binary.BigEndian.AppendUint16(ad, uint16(len(s.credID)))
	ad = append(ad, s.credID...)
	ad = append(ad, s.cose()...)
	obj := []byte{0xa3}
	obj = append(obj, cborText("fmt")...)
	obj = append(obj, cborText("none")...)
	obj = append(obj, cborText("attStmt")...)
	obj = append(obj, 0xa0)
	obj = append(obj, cborText("authData")...)
	obj = append(obj, cborBytes(ad)...)
	return passkey.RegistrationInput{ClientDataJSON: pkClient("webauthn.create", ch, origin), AttestationObject: obj, Origin: origin, RPID: rp}
}

func (s *softKey) assertion(ch []byte, rp, origin string, count uint32) PasskeyAssertion {
	ad := append(rpSum(rp), s.flags)
	ad = binary.BigEndian.AppendUint32(ad, count)
	cd := pkClient("webauthn.get", ch, origin)
	cdh := sha256.Sum256(cd)
	h := sha256.Sum256(append(append([]byte(nil), ad...), cdh[:]...))
	sig, err := ecdsa.SignASN1(rand.Reader, s.key, h[:])
	if err != nil {
		panic(err)
	}
	return PasskeyAssertion{RawID: s.credID, UserHandle: s.handle, ClientDataJSON: cd, AuthenticatorData: ad, Signature: sig}
}

// sessionFor starts a session for u the way a sign-in does.
func (e *env) sessionFor(t *testing.T, u users.User) (Session, string) {
	t.Helper()
	var tok string
	var s Session
	err := e.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		tok, s, err = e.sess.Create(context.Background(), tx, u.ID, "ua")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, tok
}

// addPasskey runs both halves of a registration and returns the key with its handle set.
func (e *env) addPasskey(t *testing.T, s Session, u users.User, password string) (*softKey, PasskeyRow) {
	t.Helper()
	ctx := context.Background()
	k := newSoftKey(t)
	opts, err := e.svc.PasskeyRegisterBegin(ctx, s, u, password, pkRP, pkOrigin, ip1)
	if err != nil {
		t.Fatalf("register begin: %v", err)
	}
	k.handle = opts.UserHandle
	row, err := e.svc.PasskeyRegister(ctx, s, u, "", k.registration(opts.Challenge, pkRP, pkOrigin))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	return k, row
}

func (e *env) signIn(t *testing.T, k *softKey, count uint32, ip netip.Addr, prev *Session) (Login, error) {
	t.Helper()
	ch, err := e.svc.PasskeySignInBegin(context.Background(), pkRP, pkOrigin, ip)
	if err != nil {
		t.Fatalf("sign-in begin: %v", err)
	}
	return e.svc.PasskeySignIn(context.Background(), k.assertion(ch, pkRP, pkOrigin, count), pkRP, pkOrigin, ip, "ua-pk", prev)
}

func (e *env) pkFailed(t *testing.T, reason string) int {
	t.Helper()
	return e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'auth.passkey_failed' AND detail_json LIKE ?`, `%"reason":"`+reason+`"%`)
}

func TestPasskeySignInMatchesPasswordSession(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	s, _ := e.sessionFor(t, e.owner)
	k, _ := e.addPasskey(t, s, e.owner, testPW)

	e.clk.Add(time.Minute)
	pw, err := e.svc.Login(ctx, "ana", testPW, ip1, "ua-pk", nil)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := e.signIn(t, k, 1, ip1, &s)
	if err != nil {
		t.Fatalf("passkey sign-in: %v", err)
	}
	if pk.User.ID != e.owner.ID || pk.User.LastSeenAt == 0 {
		t.Errorf("user = %+v", pk.User)
	}
	a, _, _, err := e.sess.Lookup(ctx, pw.Token)
	if err != nil {
		t.Fatal(err)
	}
	b, _, _, err := e.sess.Lookup(ctx, pk.Token)
	if err != nil {
		t.Fatalf("passkey session does not look up: %v", err)
	}
	if a.ExpiresAt-a.CreatedAt != b.ExpiresAt-b.CreatedAt || a.UserAgent != b.UserAgent || a.UserID != b.UserID || a.DeviceID != b.DeviceID {
		t.Errorf("sessions differ: password %+v, passkey %+v", a, b)
	}
	if n := e.count(t, `SELECT count(*) FROM sessions WHERE token_hash = ?`, s.Hash[:]); n != 0 {
		t.Errorf("previous session still there")
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'auth.login' AND detail_json LIKE '%"via":"passkey"%'`); n != 1 {
		t.Errorf("auth.login via passkey rows = %d, want 1", n)
	}
	if n := e.count(t, `SELECT count(*) FROM passkeys WHERE last_used_at IS NOT NULL AND sign_count = 1`); n != 1 {
		t.Errorf("passkey not updated")
	}
}

func TestPasskeySignInSingleUseChallenge(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	s, _ := e.sessionFor(t, e.owner)
	k, _ := e.addPasskey(t, s, e.owner, testPW)
	ch, err := e.svc.PasskeySignInBegin(ctx, pkRP, pkOrigin, ip1)
	if err != nil {
		t.Fatal(err)
	}
	as := k.assertion(ch, pkRP, pkOrigin, 1)
	if _, err := e.svc.PasskeySignIn(ctx, as, pkRP, pkOrigin, ip1, "ua", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PasskeySignIn(ctx, as, pkRP, pkOrigin, ip1, "ua", nil); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("replay = %v, want ErrBadCredentials", err)
	}
	// A challenge the server never issued.
	if _, err := e.svc.PasskeySignIn(ctx, k.assertion(passkey.NewChallenge(), pkRP, pkOrigin, 2), pkRP, pkOrigin, ip1, "ua", nil); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("unissued challenge = %v", err)
	}
	if n := e.pkFailed(t, "challenge"); n != 2 {
		t.Errorf("challenge failures = %d, want 2", n)
	}
}

func TestPasskeySignInExpired(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	s, _ := e.sessionFor(t, e.owner)
	k, _ := e.addPasskey(t, s, e.owner, testPW)
	ch, _ := e.svc.PasskeySignInBegin(ctx, pkRP, pkOrigin, ip1)
	e.clk.Add(passkeyTTL + time.Second)
	if _, err := e.svc.PasskeySignIn(ctx, k.assertion(ch, pkRP, pkOrigin, 1), pkRP, pkOrigin, ip1, "ua", nil); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("expired = %v", err)
	}
	if e.pkFailed(t, "challenge") != 1 {
		t.Error("expiry not audited as a challenge failure")
	}
}

func TestPasskeySignInOtherRPID(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	s, _ := e.sessionFor(t, e.owner)
	k, _ := e.addPasskey(t, s, e.owner, testPW)
	// The challenge was issued for another host than the one that finishes it.
	ch, _ := e.svc.PasskeySignInBegin(ctx, "other.example", "https://other.example", ip1)
	if _, err := e.svc.PasskeySignIn(ctx, k.assertion(ch, pkRP, pkOrigin, 1), pkRP, pkOrigin, ip1, "ua", nil); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("challenge of another host = %v", err)
	}
	// A passkey made at books.example does not sign in at other.example, even with a fresh challenge there.
	ch, _ = e.svc.PasskeySignInBegin(ctx, "other.example", "https://other.example", ip1)
	if _, err := e.svc.PasskeySignIn(ctx, k.assertion(ch, "other.example", "https://other.example", 1), "other.example", "https://other.example", ip1, "ua", nil); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("passkey at another host = %v", err)
	}
	if e.pkFailed(t, "unknown") != 1 {
		t.Error("unknown passkey not audited")
	}
}

func TestPasskeySignInDisabledUser(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	bob := e.person(t, "Bob", users.RoleMember, true)
	s, _ := e.sessionFor(t, bob)
	k, _ := e.addPasskey(t, s, bob, testPW)
	if err := e.st.WriteTx(ctx, func(tx *sql.Tx) error { return e.users.SetDisabled(ctx, tx, e.owner.ID, bob.ID, true) }); err != nil {
		t.Fatal(err)
	}
	if _, err := e.signIn(t, k, 1, ip1, nil); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("disabled = %v, want ErrBadCredentials", err)
	}
	if e.pkFailed(t, "disabled") != 1 {
		t.Error("not audited as disabled")
	}
	if err := e.st.WriteTx(ctx, func(tx *sql.Tx) error { return e.users.SetDisabled(ctx, tx, e.owner.ID, bob.ID, false) }); err != nil {
		t.Fatal(err)
	}
	if _, err := e.signIn(t, k, 1, ip1, nil); err != nil {
		t.Errorf("after turning back on: %v", err)
	}
}

func TestPasskeySignInRateLimited(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	s, _ := e.sessionFor(t, e.owner)
	k, _ := e.addPasskey(t, s, e.owner, testPW)

	// The account limiter: ten bad signatures from ten addresses lock the account for a good one.
	for i := 0; i < userMaxFails; i++ {
		ip := netip.MustParseAddr(fmt.Sprintf("198.51.100.%d", i+1))
		ch, _ := e.svc.PasskeySignInBegin(ctx, pkRP, pkOrigin, ip)
		as := k.assertion(ch, pkRP, pkOrigin, uint32(i+1))
		as.Signature[len(as.Signature)-1] ^= 1
		if _, err := e.svc.PasskeySignIn(ctx, as, pkRP, pkOrigin, ip, "ua", nil); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("bad signature %d = %v", i, err)
		}
	}
	var rl *RateLimitedError
	if _, err := e.signIn(t, k, 50, netip.MustParseAddr("198.51.100.200"), nil); !errors.As(err, &rl) {
		t.Fatalf("locked account = %v, want *RateLimitedError", err)
	}

	// The address limiter answers before any database read: close the store's view of the table.
	e2 := newEnv(t).withOwner(t)
	for i := 0; i < ipMaxFails; i++ {
		_, _ = e2.svc.PasskeySignIn(ctx, PasskeyAssertion{RawID: make([]byte, 5)}, pkRP, pkOrigin, ip1, "ua", nil)
	}
	before := e2.count(t, `SELECT count(*) FROM audit_log`)
	_, err := e2.svc.PasskeySignIn(ctx, PasskeyAssertion{RawID: make([]byte, 5)}, pkRP, pkOrigin, ip1, "ua", nil)
	if !errors.As(err, &rl) || rl.RetryAfter <= 0 {
		t.Fatalf("address limit = %v", err)
	}
	if after := e2.count(t, `SELECT count(*) FROM audit_log`); after != before {
		t.Errorf("a limited attempt wrote audit rows (%d -> %d)", before, after)
	}
	// The same address is also limited for password sign-in: one shared limiter.
	if _, err := e2.svc.Login(ctx, "ana", testPW, ip1, "ua", nil); !errors.As(err, &rl) {
		t.Errorf("password after passkey failures = %v, want *RateLimitedError", err)
	}

	// Starts are limited per address.
	e3 := newEnv(t).withOwner(t)
	for i := 0; i < passkeyStartMax; i++ {
		if _, err := e3.svc.PasskeySignInBegin(ctx, pkRP, pkOrigin, ip1); err != nil {
			t.Fatalf("start %d: %v", i, err)
		}
	}
	if _, err := e3.svc.PasskeySignInBegin(ctx, pkRP, pkOrigin, ip1); !errors.As(err, &rl) {
		t.Errorf("31st start = %v", err)
	}
}

func TestPasskeyCounterMarksCopied(t *testing.T) {
	e := newEnv(t).withOwner(t)
	s, _ := e.sessionFor(t, e.owner)
	k, row := e.addPasskey(t, s, e.owner, testPW)
	if _, err := e.signIn(t, k, 5, ip1, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := e.signIn(t, k, 5, ip1, nil); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("repeated count = %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM passkeys WHERE id = ? AND copied_at IS NOT NULL`, row.ID); n != 1 {
		t.Error("copied_at not set")
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'passkey.copied' AND target = ?`, row.ID); n != 1 {
		t.Error("passkey.copied not audited")
	}
	// Once marked, even a good count is refused until the person removes it.
	if _, err := e.signIn(t, k, 9, ip1, nil); !errors.Is(err, ErrBadCredentials) {
		t.Errorf("marked passkey signed in: %v", err)
	}
	if e.pkFailed(t, "copied") != 1 {
		t.Error("copied refusal not audited")
	}
}

func TestPasskeyRegisterNeedsPasswordOrFreshSession(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	s, _ := e.sessionFor(t, e.owner)
	for _, pw := range []string{"", "wrong password here"} {
		if _, err := e.svc.PasskeyRegisterBegin(ctx, s, e.owner, pw, pkRP, pkOrigin, ip1); !errors.Is(err, ErrBadCredentials) {
			t.Errorf("password %q = %v, want ErrBadCredentials", pw, err)
		}
	}
	if _, err := e.svc.PasskeyRegisterBegin(ctx, s, e.owner, testPW, pkRP, pkOrigin, ip1); err != nil {
		t.Errorf("right password: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM users WHERE passkey_handle IS NOT NULL AND length(passkey_handle) = 32`); n != 1 {
		t.Errorf("handles = %d, want 1", n)
	}

	bob := e.person(t, "Bob", users.RoleMember, false)
	bs, _ := e.sessionFor(t, bob)
	if _, err := e.svc.PasskeyRegisterBegin(ctx, bs, bob, "", pkRP, pkOrigin, ip1); err != nil {
		t.Errorf("fresh session without a password: %v", err)
	}
	e.clk.Add(passkeyFresh + time.Minute)
	if _, err := e.svc.PasskeyRegisterBegin(ctx, bs, bob, "", pkRP, pkOrigin, ip1); !errors.Is(err, ErrPasskeyStale) {
		t.Errorf("old session without a password = %v, want ErrPasskeyStale", err)
	}
}

func TestPasskeyRegisterOtherSessionRefused(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	a, _ := e.sessionFor(t, e.owner)
	b, _ := e.sessionFor(t, e.owner)
	bob := e.person(t, "Bob", users.RoleMember, true)
	k := newSoftKey(t)
	opts, err := e.svc.PasskeyRegisterBegin(ctx, a, e.owner, testPW, pkRP, pkOrigin, ip1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PasskeyRegister(ctx, b, e.owner, "x", k.registration(opts.Challenge, pkRP, pkOrigin)); !errors.Is(err, ErrPasskeyFailed) {
		t.Errorf("other session = %v", err)
	}
	// The refused finish burned the challenge: the right session cannot reuse it either.
	if _, err := e.svc.PasskeyRegister(ctx, a, e.owner, "x", k.registration(opts.Challenge, pkRP, pkOrigin)); !errors.Is(err, ErrPasskeyFailed) {
		t.Errorf("burned challenge = %v", err)
	}
	opts, _ = e.svc.PasskeyRegisterBegin(ctx, a, e.owner, testPW, pkRP, pkOrigin, ip1)
	bs, _ := e.sessionFor(t, bob)
	if _, err := e.svc.PasskeyRegister(ctx, bs, bob, "x", k.registration(opts.Challenge, pkRP, pkOrigin)); !errors.Is(err, ErrPasskeyFailed) {
		t.Errorf("other person = %v", err)
	}
	// A sign-in challenge is not a registration one.
	ch, _ := e.svc.PasskeySignInBegin(ctx, pkRP, pkOrigin, ip1)
	if _, err := e.svc.PasskeyRegister(ctx, a, e.owner, "x", k.registration(ch, pkRP, pkOrigin)); !errors.Is(err, ErrPasskeyFailed) {
		t.Errorf("sign-in challenge = %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM passkeys`); n != 0 {
		t.Errorf("passkeys stored = %d, want 0", n)
	}
}

func TestPasskeyRegisterLimit20(t *testing.T) {
	e := newEnv(t).withOwner(t)
	s, _ := e.sessionFor(t, e.owner)
	for i := 0; i < passkeyPerUser; i++ {
		e.addPasskey(t, s, e.owner, testPW)
	}
	if _, err := e.svc.PasskeyRegisterBegin(context.Background(), s, e.owner, testPW, pkRP, pkOrigin, ip1); !errors.Is(err, ErrPasskeyLimit) {
		t.Errorf("21st = %v, want ErrPasskeyLimit", err)
	}
	rows, err := e.svc.Passkeys(context.Background(), e.owner.ID)
	if err != nil || len(rows) != 20 || rows[0].Name != "Passkey 20" {
		t.Errorf("list = %d rows (%v), first %q", len(rows), err, func() string {
			if len(rows) > 0 {
				return rows[0].Name
			}
			return ""
		}())
	}
}

func TestPasskeyRegisterDuplicate(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	s, _ := e.sessionFor(t, e.owner)
	k, _ := e.addPasskey(t, s, e.owner, testPW)
	opts, err := e.svc.PasskeyRegisterBegin(ctx, s, e.owner, testPW, pkRP, pkOrigin, ip1)
	if err != nil {
		t.Fatal(err)
	}
	if len(opts.ExcludeIDs) != 1 || string(opts.ExcludeIDs[0]) != string(k.credID) {
		t.Errorf("exclude list = %x", opts.ExcludeIDs)
	}
	if string(opts.UserHandle) != string(k.handle) {
		t.Error("the user handle changed between registrations")
	}
	if _, err := e.svc.PasskeyRegister(ctx, s, e.owner, "Again", k.registration(opts.Challenge, pkRP, pkOrigin)); !errors.Is(err, ErrPasskeyTaken) {
		t.Errorf("duplicate = %v, want ErrPasskeyTaken", err)
	}
	// Name rules.
	opts, _ = e.svc.PasskeyRegisterBegin(ctx, s, e.owner, testPW, pkRP, pkOrigin, ip1)
	var fe users.FieldErrors
	if _, err := e.svc.PasskeyRegister(ctx, s, e.owner, fmt.Sprintf("%065d", 0), newSoftKey(t).registration(opts.Challenge, pkRP, pkOrigin)); !errors.As(err, &fe) || fe["name"] == "" {
		t.Errorf("65-rune name = %v", err)
	}
}

func TestPasskeyRemoveOwnOnly(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	bob := e.person(t, "Bob", users.RoleMember, true)
	s, _ := e.sessionFor(t, e.owner)
	bs, _ := e.sessionFor(t, bob)
	_, ana := e.addPasskey(t, s, e.owner, testPW)
	e.addPasskey(t, bs, bob, testPW)
	if err := e.svc.PasskeyRemove(ctx, bob.ID, ana.ID); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("remove another's = %v, want ErrNotFound", err)
	}
	if err := e.svc.PasskeyRemove(ctx, e.owner.ID, "pk_nothing"); !errors.Is(err, users.ErrNotFound) {
		t.Errorf("remove unknown = %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM passkeys`); n != 2 {
		t.Errorf("passkeys = %d, want 2", n)
	}
	if err := e.svc.PasskeyRemove(ctx, e.owner.ID, ana.ID); err != nil {
		t.Errorf("remove own: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'passkey.removed' AND target = ?`, ana.ID); n != 1 {
		t.Error("removal not audited")
	}
	// Admin removal: all of one person's, inside the caller's transaction.
	var n int
	if err := e.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		n, err = e.svc.PasskeysRemoveAll(ctx, tx, e.owner.ID, bob.ID)
		return err
	}); err != nil || n != 1 {
		t.Errorf("remove all = %d, %v", n, err)
	}
	if e.count(t, `SELECT count(*) FROM passkeys`) != 0 || e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'passkey.removed_all'`) != 1 {
		t.Error("remove all left rows or no audit")
	}
}

func TestPasskeyRemoveLastWayIn(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	// An account with no password: its last passkey stays.
	bob := e.person(t, "Bob", users.RoleMember, false)
	bs, _ := e.sessionFor(t, bob)
	_, first := e.addPasskey(t, bs, bob, "")
	if err := e.svc.PasskeyRemove(ctx, bob.ID, first.ID); !errors.Is(err, ErrPasskeyLast) {
		t.Errorf("last way in = %v, want ErrPasskeyLast", err)
	}
	_, second := e.addPasskey(t, bs, bob, "")
	if err := e.svc.PasskeyRemove(ctx, bob.ID, first.ID); err != nil {
		t.Errorf("one of two: %v", err)
	}
	if err := e.svc.PasskeyRemove(ctx, bob.ID, second.ID); !errors.Is(err, ErrPasskeyLast) {
		t.Errorf("last again = %v", err)
	}
	// A provider identity counts as another way in.
	if _, err := e.st.Writer().Exec(`INSERT INTO oidc_identities (issuer, subject, user_id, linked_via, created_at) VALUES ('https://i', 's', ?, 'account', 1)`, bob.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.PasskeyRemove(ctx, bob.ID, second.ID); err != nil {
		t.Errorf("with a provider identity: %v", err)
	}
	// An account with a password may remove its last passkey.
	s, _ := e.sessionFor(t, e.owner)
	_, only := e.addPasskey(t, s, e.owner, testPW)
	if err := e.svc.PasskeyRemove(ctx, e.owner.ID, only.ID); err != nil {
		t.Errorf("with a password: %v", err)
	}
}

func TestPasskeyOpenCeremonyCap(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	now := e.clk.Now().UnixMilli()
	err := e.st.WriteTx(ctx, func(tx *sql.Tx) error {
		for i := 0; i < passkeyOpenMax; i++ {
			if _, err := tx.ExecContext(ctx, `INSERT INTO passkey_ceremonies (challenge_hash, purpose, rp_id, origin, created_at, expires_at)
				VALUES (randomblob(32), 'signin', ?, ?, ?, ?)`, pkRP, pkOrigin, now, now+int64(passkeyTTL/time.Millisecond)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.PasskeySignInBegin(ctx, pkRP, pkOrigin, ip1); !errors.Is(err, ErrPasskeyBusy) {
		t.Errorf("at the cap = %v, want ErrPasskeyBusy", err)
	}
	e.clk.Add(passkeyTTL + time.Second)
	if _, err := e.svc.PasskeySignInBegin(ctx, pkRP, pkOrigin, ip1); err != nil {
		t.Errorf("after they expired: %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM passkey_ceremonies`); n != 1 {
		t.Errorf("expired rows not purged: %d", n)
	}
}

func TestPasswordChangeKeepsPasskeys(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	s, _ := e.sessionFor(t, e.owner)
	k, _ := e.addPasskey(t, s, e.owner, testPW)
	if _, err := e.svc.ChangePassword(ctx, s, testPW, "another long passphrase", ip1); err != nil {
		t.Fatal(err)
	}
	if n := e.count(t, `SELECT count(*) FROM passkeys`); n != 1 {
		t.Errorf("passkeys after a password change = %d, want 1", n)
	}
	if _, err := e.signIn(t, k, 1, ip1, nil); err != nil {
		t.Errorf("passkey after a password change: %v", err)
	}
}
