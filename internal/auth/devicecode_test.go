package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"
)

// a1Flow is one app's start of the sign-in: a verifier it keeps and the challenge it gives the page.
type a1Flow struct {
	verifier  string
	challenge [32]byte
}

func a1NewFlow() a1Flow {
	v, _ := NewToken() // 32 random bytes in base64url: the shape of a verifier
	return a1Flow{verifier: v, challenge: sha256.Sum256([]byte(v))}
}

// a1Code signs the owner in on a session, stores a code for flow and returns both.
func (e *env) a1Code(t *testing.T, f a1Flow) (string, Session) {
	t.Helper()
	l, err := e.svc.Login(context.Background(), "ana", testPW, ip1, "ua", nil)
	if err != nil {
		t.Fatal(err)
	}
	sess, _, _, err := e.sess.Lookup(context.Background(), l.Token)
	if err != nil {
		t.Fatal(err)
	}
	code, err := e.svc.IssueDeviceCode(context.Background(), sess, f.challenge, ip1)
	if err != nil {
		t.Fatal(err)
	}
	return code, sess
}

func (e *env) a1Devices(t *testing.T) int {
	return e.count(t, `SELECT COUNT(*) FROM devices`)
}

func TestA1ParseChallenge(t *testing.T) {
	f := a1NewFlow()
	good := base64.RawURLEncoding.EncodeToString(f.challenge[:])
	if got, ok := ParseChallenge(good); !ok || got != f.challenge {
		t.Fatal("a good challenge is refused")
	}
	// The last character carries 2 spare bits; a spelling with them set is not canonical.
	sloppy := good[:42] + "B"
	if good[42] == 'B' {
		sloppy = good[:42] + "C"
	}
	for name, s := range map[string]string{
		"empty": "", "short": good[:42], "long": good + "A", "plain": f.verifier + f.verifier,
		"padded": good + "=", "sloppy": sloppy,
		"not base64": strings.Repeat("!", 43),
	} {
		if _, ok := ParseChallenge(s); ok {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestA1SwapHappy(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	f := a1NewFlow()
	code, sess := e.a1Code(t, f)
	codeHash, _ := HashToken(code)
	if n := e.count(t, `SELECT COUNT(*) FROM device_codes WHERE code_hash = ?`, codeHash[:]); n != 1 {
		t.Fatal("the code hash is not stored")
	}
	if n := e.count(t, `SELECT COUNT(*) FROM device_codes WHERE code_hash = ? OR challenge = ?`, []byte(code), []byte(f.verifier)); n != 0 {
		t.Fatal("a raw code or verifier is stored")
	}
	l, err := e.svc.SwapDeviceCode(ctx, code, f.verifier, "Pixel", nil, ip1)
	if err != nil || l.Token == "" || l.Device.Kind != KindMobile || l.Device.Name != "Pixel" {
		t.Fatalf("swap: %+v %v", l, err)
	}
	if _, _, err := e.dev.Lookup(ctx, l.Token); err != nil {
		t.Fatalf("the token does not work: %v", err)
	}
	var bound string
	_ = e.st.Reader().QueryRow(`SELECT device_id FROM sessions WHERE token_hash = ?`, sess.Hash[:]).Scan(&bound)
	if bound != l.Device.ID {
		t.Fatalf("session bound to %q, want %q", bound, l.Device.ID)
	}
	var all string
	_ = e.st.Reader().QueryRow(`SELECT group_concat(detail_json || target || action) FROM audit_log`).Scan(&all)
	for _, secret := range []string{code, f.verifier, l.Token} {
		if strings.Contains(all, secret) {
			t.Fatal("the audit log holds a secret")
		}
	}
	for _, want := range []string{"auth.device_code_issued", "auth.device_issued", `"via":"code"`} {
		if !strings.Contains(all, want) {
			t.Errorf("audit lacks %s", want)
		}
	}
}

func TestA1SwapForbidden(t *testing.T) {
	ctx := context.Background()
	type step func(e *env, f a1Flow, code string, sess Session) (swapCode, verifier string)
	right := func(e *env, f a1Flow, code string, sess Session) (string, string) { return code, f.verifier }
	cases := map[string]step{
		"wrong verifier": func(e *env, f a1Flow, code string, sess Session) (string, string) {
			return code, a1NewFlow().verifier
		},
		"verifier of another code": func(e *env, f a1Flow, code string, sess Session) (string, string) {
			g := a1NewFlow()
			if _, err := e.svc.IssueDeviceCode(ctx, sess, g.challenge, ip1); err != nil { // replaces the first code
				t.Fatal(err)
			}
			return code, g.verifier
		},
		"short verifier": func(e *env, f a1Flow, code string, sess Session) (string, string) {
			return code, f.verifier[:42]
		},
		"challenge used as verifier": func(e *env, f a1Flow, code string, sess Session) (string, string) {
			return code, base64.RawURLEncoding.EncodeToString(f.challenge[:])
		},
		"unknown code": func(e *env, f a1Flow, code string, sess Session) (string, string) {
			other, _ := NewToken()
			return other, f.verifier
		},
		"malformed code": func(e *env, f a1Flow, code string, sess Session) (string, string) { return "abc", f.verifier },
		"empty code":     func(e *env, f a1Flow, code string, sess Session) (string, string) { return "", f.verifier },
		"expired": func(e *env, f a1Flow, code string, sess Session) (string, string) {
			e.clk.Add(DeviceCodeTTL + time.Millisecond)
			return right(e, f, code, sess)
		},
		"signed out": func(e *env, f a1Flow, code string, sess Session) (string, string) {
			if err := e.svc.Logout(ctx, sess); err != nil {
				t.Fatal(err)
			}
			return right(e, f, code, sess)
		},
		"person disabled": func(e *env, f a1Flow, code string, sess Session) (string, string) {
			if _, err := e.st.Writer().Exec(`UPDATE users SET disabled = 1`); err != nil {
				t.Fatal(err)
			}
			return right(e, f, code, sess)
		},
		"password changed": func(e *env, f a1Flow, code string, sess Session) (string, string) {
			if _, err := e.svc.ChangePassword(ctx, sess, testPW, "another long password", ip1); err != nil {
				t.Fatal(err)
			}
			return right(e, f, code, sess)
		},
		"person deleted": func(e *env, f a1Flow, code string, sess Session) (string, string) {
			if _, err := e.st.Writer().Exec(`PRAGMA foreign_keys = ON`); err != nil {
				t.Fatal(err)
			}
			if _, err := e.st.Writer().Exec(`DELETE FROM users WHERE id = ?`, e.owner.ID); err != nil {
				t.Fatal(err)
			}
			return right(e, f, code, sess)
		},
		"session expired": func(e *env, f a1Flow, code string, sess Session) (string, string) {
			if _, err := e.st.Writer().Exec(`UPDATE sessions SET expires_at = ?`, e.clk.Now().UnixMilli()-1); err != nil {
				t.Fatal(err)
			}
			return right(e, f, code, sess)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t).withOwner(t)
			f := a1NewFlow()
			code, sess := e.a1Code(t, f)
			before := e.a1Devices(t)
			c, v := mutate(e, f, code, sess)
			_, err := e.svc.SwapDeviceCode(ctx, c, v, "x", nil, ip1)
			if !errors.Is(err, ErrBadCode) {
				t.Fatalf("err = %v, want ErrBadCode", err)
			}
			if got := e.a1Devices(t); got != before {
				t.Fatalf("devices %d -> %d", before, got)
			}
			if e.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'auth.device_code_failed'`) != 1 {
				t.Error("the refusal is not audited")
			}
			var all string
			_ = e.st.Reader().QueryRow(`SELECT group_concat(detail_json) FROM audit_log`).Scan(&all)
			if strings.Contains(all, code) || strings.Contains(all, f.verifier) {
				t.Error("the audit log holds a secret")
			}
		})
	}
}

func TestA1SwapReplayAndBurn(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t).withOwner(t)
	f := a1NewFlow()
	code, _ := e.a1Code(t, f)
	if _, err := e.svc.SwapDeviceCode(ctx, code, f.verifier, "", nil, ip1); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.SwapDeviceCode(ctx, code, f.verifier, "", nil, ip1); !errors.Is(err, ErrBadCode) {
		t.Fatalf("replay: %v", err)
	}
	if e.a1Devices(t) != 1 {
		t.Fatal("replay added a device")
	}

	// A wrong verifier burns the code: the right one fails afterwards.
	g := a1NewFlow()
	code2, _ := e.a1Code(t, g)
	if _, err := e.svc.SwapDeviceCode(ctx, code2, a1NewFlow().verifier, "", nil, ip1); !errors.Is(err, ErrBadCode) {
		t.Fatal(err)
	}
	if _, err := e.svc.SwapDeviceCode(ctx, code2, g.verifier, "", nil, ip1); !errors.Is(err, ErrBadCode) {
		t.Fatalf("after a wrong verifier the right one worked: %v", err)
	}

	// Exactly the time to live is still fine.
	h := a1NewFlow()
	code3, _ := e.a1Code(t, h)
	e.clk.Add(DeviceCodeTTL)
	if _, err := e.svc.SwapDeviceCode(ctx, code3, h.verifier, "", nil, ip1); err != nil {
		t.Fatalf("at the limit: %v", err)
	}
}

func TestA1SwapOneLiveCodePerSession(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t).withOwner(t)
	f := a1NewFlow()
	first, sess := e.a1Code(t, f)
	second, err := e.svc.IssueDeviceCode(ctx, sess, f.challenge, ip1)
	if err != nil {
		t.Fatal(err)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM device_codes`); n != 1 {
		t.Fatalf("codes = %d, want 1", n)
	}
	if _, err := e.svc.SwapDeviceCode(ctx, first, f.verifier, "", nil, ip1); !errors.Is(err, ErrBadCode) {
		t.Fatalf("the replaced code worked: %v", err)
	}
	// The first attempt did not touch the second code.
	if _, err := e.svc.SwapDeviceCode(ctx, second, f.verifier, "", nil, ip1); err != nil {
		t.Fatal(err)
	}
	// Expired codes are swept on the next issue.
	g := a1NewFlow()
	_, sess2 := e.a1Code(t, g)
	e.clk.Add(time.Hour)
	if _, err := e.svc.IssueDeviceCode(ctx, sess2, g.challenge, ip1); err != nil {
		t.Fatal(err)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM device_codes`); n != 1 {
		t.Fatalf("codes = %d after the sweep, want 1", n)
	}
}

func TestA1SwapDeviceLimitKeepsCode(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t).withOwner(t)
	for i := 0; i < MaxLiveDevices; i++ {
		e.issue(t, "", KindWeb)
	}
	f := a1NewFlow()
	code, _ := e.a1Code(t, f)
	if _, err := e.svc.SwapDeviceCode(ctx, code, f.verifier, "", nil, ip1); !errors.Is(err, ErrTooManyDevices) {
		t.Fatalf("at the limit: %v", err)
	}
	if e.a1Devices(t) != MaxLiveDevices {
		t.Fatal("a device was added")
	}
	if e.count(t, `SELECT COUNT(*) FROM device_codes`) != 1 {
		t.Fatal("the code was burned by a rollback that should keep it")
	}
}

func TestA1SwapRebind(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t).withOwner(t)
	first := e.issue(t, "Phone", KindMobile)

	f := a1NewFlow()
	code, sess := e.a1Code(t, f)
	caller := first.Device
	l, err := e.svc.SwapDeviceCode(ctx, code, f.verifier, "ignored", &caller, ip1)
	if err != nil || l.Token != "" || l.Device.ID != first.Device.ID {
		t.Fatalf("rebind: %+v %v", l, err)
	}
	if e.a1Devices(t) != 1 {
		t.Fatal("rebind added a device")
	}
	var bound string
	_ = e.st.Reader().QueryRow(`SELECT device_id FROM sessions WHERE token_hash = ?`, sess.Hash[:]).Scan(&bound)
	if bound != first.Device.ID {
		t.Fatalf("session bound to %q", bound)
	}
	if e.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'auth.device_rebound'`) != 1 {
		t.Error("the rebind is not audited")
	}

	// A caller that is not a mobile device is refused and the code is burned.
	web := e.issue(t, "", KindWeb).Device
	g := a1NewFlow()
	code2, _ := e.a1Code(t, g)
	if _, err := e.svc.SwapDeviceCode(ctx, code2, g.verifier, "", &web, ip1); !errors.Is(err, ErrCodeOtherPerson) {
		t.Fatalf("web caller: %v", err)
	}
	if _, err := e.svc.SwapDeviceCode(ctx, code2, g.verifier, "", nil, ip1); !errors.Is(err, ErrBadCode) {
		t.Fatalf("burned code: %v", err)
	}

	// A device of another person is refused and nothing is bound.
	if _, err := e.st.Writer().ExecContext(ctx, `INSERT INTO users (id, username, display_name, role, password_hash, disabled, created_at)
		VALUES ('usr_bea', 'bea', 'Bea', 'member', 'x', 0, 1)`); err != nil {
		t.Fatal(err)
	}
	var theirs Device
	err = e.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		_, theirs, err = e.dev.Issue(ctx, tx, "usr_bea", "", KindMobile)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	h := a1NewFlow()
	code3, sess3 := e.a1Code(t, h)
	if _, err := e.svc.SwapDeviceCode(ctx, code3, h.verifier, "", &theirs, ip1); !errors.Is(err, ErrCodeOtherPerson) {
		t.Fatalf("other person: %v", err)
	}
	var dev sql.NullString
	_ = e.st.Reader().QueryRow(`SELECT device_id FROM sessions WHERE token_hash = ?`, sess3.Hash[:]).Scan(&dev)
	if dev.Valid {
		t.Fatal("a session was bound by a refused swap")
	}
}

// bind swaps a fresh code and returns the token, the device and the bound session's raw token.
func (e *env) a1Bound(t *testing.T) (DeviceLogin, string, Session) {
	t.Helper()
	f := a1NewFlow()
	l, err := e.svc.Login(context.Background(), "ana", testPW, ip1, "ua", nil)
	if err != nil {
		t.Fatal(err)
	}
	sess, _, _, err := e.sess.Lookup(context.Background(), l.Token)
	if err != nil {
		t.Fatal(err)
	}
	code, err := e.svc.IssueDeviceCode(context.Background(), sess, f.challenge, ip1)
	if err != nil {
		t.Fatal(err)
	}
	dl, err := e.svc.SwapDeviceCode(context.Background(), code, f.verifier, "Pixel", nil, ip1)
	if err != nil {
		t.Fatal(err)
	}
	sess.DeviceID = dl.Device.ID
	return dl, l.Token, sess
}

func TestA1BoundSessionEndsWithDevice(t *testing.T) {
	ctx := context.Background()
	revokers := map[string]func(e *env, dl DeviceLogin, sess Session){
		"revoke": func(e *env, dl DeviceLogin, sess Session) {
			if ok, err := e.svc.RevokeDevice(ctx, e.owner.ID, dl.Device.ID, e.owner.ID); err != nil || !ok {
				t.Fatal(ok, err)
			}
		},
		"admin revoke": func(e *env, dl DeviceLogin, sess Session) {
			if ok, err := e.svc.RevokeDevice(ctx, e.owner.ID, dl.Device.ID, "usr_admin"); err != nil || !ok {
				t.Fatal(ok, err)
			}
		},
		"pruned row": func(e *env, dl DeviceLogin, sess Session) {
			if _, err := e.st.Writer().Exec(`DELETE FROM devices WHERE id = ?`, dl.Device.ID); err != nil {
				t.Fatal(err)
			}
		},
		"logout": func(e *env, dl DeviceLogin, sess Session) {
			if err := e.svc.Logout(ctx, sess); err != nil {
				t.Fatal(err)
			}
		},
		"sign out others from a browser": func(e *env, dl DeviceLogin, sess Session) {
			other := e.login(t)
			s2, _, _, _ := e.sess.Lookup(ctx, other)
			if _, err := e.svc.SignOutOthers(ctx, s2); err != nil {
				t.Fatal(err)
			}
		},
		"password change": func(e *env, dl DeviceLogin, sess Session) {
			if _, err := e.svc.ChangePassword(ctx, sess, testPW, "another long password", ip1); err != nil {
				t.Fatal(err)
			}
		},
		"person disabled": func(e *env, dl DeviceLogin, sess Session) {
			if _, err := e.st.Writer().Exec(`UPDATE users SET disabled = 1`); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, end := range revokers {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t).withOwner(t)
			dl, tok, sess := e.a1Bound(t)
			if _, _, _, err := e.sess.Lookup(ctx, tok); err != nil {
				t.Fatalf("bound session before: %v", err)
			}
			end(e, dl, sess)
			if _, _, _, err := e.sess.Lookup(ctx, tok); !errors.Is(err, ErrNoSession) {
				t.Errorf("session after: %v", err)
			}
			if _, _, err := e.dev.Lookup(ctx, dl.Token); !errors.Is(err, ErrNoDevice) {
				t.Errorf("token after: %v", err)
			}
		})
	}
}

func TestA1BoundSessionExpiryLeavesToken(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t).withOwner(t)
	dl, tok, _ := e.a1Bound(t)
	e.clk.Add(SessionTTL + time.Hour)
	if _, _, _, err := e.sess.Lookup(ctx, tok); !errors.Is(err, ErrNoSession) {
		t.Fatalf("expired session: %v", err)
	}
	if _, _, err := e.dev.Lookup(ctx, dl.Token); err != nil {
		t.Fatalf("the token died with the session: %v", err)
	}
}

func TestA1SignOutOthersKeepsThePhone(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t).withOwner(t)
	dl, tok, sess := e.a1Bound(t)
	other := e.issue(t, "Tablet", KindMobile)
	browser := e.login(t)
	if _, err := e.svc.SignOutOthers(ctx, sess); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := e.sess.Lookup(ctx, tok); err != nil {
		t.Errorf("the phone's session ended: %v", err)
	}
	if _, _, err := e.dev.Lookup(ctx, dl.Token); err != nil {
		t.Errorf("the phone's token ended: %v", err)
	}
	if _, _, err := e.dev.Lookup(ctx, other.Token); !errors.Is(err, ErrNoDevice) {
		t.Errorf("another token lived: %v", err)
	}
	if _, _, _, err := e.sess.Lookup(ctx, browser); !errors.Is(err, ErrNoSession) {
		t.Errorf("a browser session lived: %v", err)
	}
}

func TestA1BoundSessionsLeaveTheLists(t *testing.T) {
	ctx := context.Background()
	e := newEnv(t).withOwner(t)
	_, _, sess := e.a1Bound(t)
	browser := e.login(t)
	bs, _, _, _ := e.sess.Lookup(ctx, browser)
	list, err := e.sess.ListForUser(ctx, e.owner.ID, bs.Hash)
	if err != nil || len(list) == 0 {
		t.Fatalf("list: %+v %v", list, err)
	}
	all, err := e.sess.ListAll(ctx, bs.Hash)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range [][]SessionInfo{list, all} {
		for _, si := range l {
			if si.PublicID == PublicID(sess.Hash) {
				t.Fatal("a bound session is listed")
			}
		}
	}
}
