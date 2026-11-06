package auth

import (
	"context"
	"database/sql"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// pairEnv is an owner with one signed-in session that shows a QR.
type pairEnv struct {
	*env
	owner Session
	pair  Pairing
	tok   string
}

func newPairEnv(t *testing.T) *pairEnv {
	t.Helper()
	e := newEnv(t).withOwner(t)
	l, err := e.svc.Login(context.Background(), "ana", testPW, ip1, "ua", nil)
	if err != nil {
		t.Fatal(err)
	}
	sess, _, _, err := e.sess.Lookup(context.Background(), l.Token)
	if err != nil {
		t.Fatal(err)
	}
	p, tok, err := e.svc.StartPairing(context.Background(), sess, ip1)
	if err != nil {
		t.Fatal(err)
	}
	return &pairEnv{env: e, owner: sess, pair: p, tok: tok}
}

// claim plays the phone: claim with a fresh flow and read the number off its page.
func (e *pairEnv) claim(t *testing.T) (claim, number string, f a1Flow) {
	t.Helper()
	f = a1NewFlow()
	claim, err := e.svc.ClaimPairing(context.Background(), e.tok, f.challenge, "Pixel", ip1)
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.ClaimedPairing(context.Background(), claim)
	if err != nil || p.Number == "" || p.State != PairClaimed {
		t.Fatalf("claimed = %+v, %v", p, err)
	}
	return claim, p.Number, f
}

func (e *pairEnv) approved(t *testing.T) (string, a1Flow) {
	t.Helper()
	claim, number, f := e.claim(t)
	if err := e.svc.ApprovePairing(context.Background(), e.pair.ID, e.owner, number, ip1); err != nil {
		t.Fatal(err)
	}
	return claim, f
}

func (e *pairEnv) rows(t *testing.T) int { return e.count(t, `SELECT COUNT(*) FROM device_pairings`) }

func (e *pairEnv) wantNothingMade(t *testing.T, sessions, codes int) {
	t.Helper()
	if n := e.count(t, `SELECT COUNT(*) FROM sessions`); n != sessions {
		t.Errorf("sessions = %d, want %d", n, sessions)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM device_codes`); n != codes {
		t.Errorf("device codes = %d, want %d", n, codes)
	}
}

func TestPairHappyPath(t *testing.T) {
	e := newPairEnv(t)
	ctx := context.Background()
	hash, _ := HashToken(e.tok)
	if n := e.count(t, `SELECT COUNT(*) FROM device_pairings WHERE token_hash = ?`, hash[:]); n != 1 {
		t.Fatal("token hash not stored")
	}
	if n := e.count(t, `SELECT COUNT(*) FROM device_pairings WHERE token_hash = ?`, []byte(e.tok)); n != 0 {
		t.Fatal("raw token stored")
	}
	claim, number, f := e.claim(t)
	if len(number) != 4 {
		t.Fatalf("number = %q", number)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM device_pairings WHERE claim_hash = ?`, []byte(claim)); n != 0 {
		t.Fatal("raw claim stored")
	}
	owner, err := e.svc.OwnerPairing(ctx, e.pair.ID, e.owner)
	if err != nil || owner.State != PairClaimed || owner.PhoneName != "Pixel" || owner.PhoneIP != ip1.String() || owner.Number != "" {
		t.Fatalf("owner view = %+v, %v", owner, err)
	}
	if err := e.svc.ApprovePairing(ctx, e.pair.ID, e.owner, number, ip1); err != nil {
		t.Fatal(err)
	}
	p, err := e.svc.ClaimedPairing(ctx, claim)
	if err != nil || p.State != PairApproved || p.PersonName != "Ana" || p.Number != "" {
		t.Fatalf("approved view = %+v, %v", p, err)
	}
	sessions := e.count(t, `SELECT COUNT(*) FROM sessions`)
	login, code, err := e.svc.FinishPairing(ctx, claim, "phone-ua", ip1, nil)
	if err != nil || login.Token == "" || code == "" {
		t.Fatalf("finish = %+v %q %v", login, code, err)
	}
	if e.rows(t) != 0 {
		t.Error("the pairing outlived the finish")
	}
	e.wantNothingMade(t, sessions+1, 1)
	// The last leg is the ordinary one: the code swaps with the claim's verifier, no password.
	dl, err := e.svc.SwapDeviceCode(ctx, code, f.verifier, "Pixel", nil, ip1)
	if err != nil || dl.Token == "" || dl.Device.Kind != KindMobile || dl.User.ID != e.env.owner.ID {
		t.Fatalf("swap = %+v, %v", dl, err)
	}
	for _, a := range []string{"auth.pair_started", "auth.pair_claimed", "auth.pair_approved", "auth.device_code_issued", "auth.device_issued"} {
		if e.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = ?`, a) == 0 {
			t.Errorf("no audit row %s", a)
		}
	}
	if e.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'auth.login' AND detail_json LIKE '%"via":"pair"%'`) != 1 {
		t.Error("no auth.login via pair")
	}
}

func TestPairForbiddenClaim(t *testing.T) {
	ctx := context.Background()
	f := a1NewFlow()
	t.Run("second claim", func(t *testing.T) {
		e := newPairEnv(t)
		e.claim(t)
		if _, err := e.svc.ClaimPairing(ctx, e.tok, f.challenge, "Other", ip1); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("err = %v", err)
		}
		if err := e.svc.PairingPending(ctx, e.tok); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("a claimed pairing is still pending: %v", err)
		}
	})
	t.Run("expired by 1 ms", func(t *testing.T) {
		e := newPairEnv(t)
		e.clk.Add(PairTTL)
		if err := e.svc.PairingPending(ctx, e.tok); err != nil {
			t.Fatalf("at the last millisecond: %v", err)
		}
		e.clk.Add(time.Millisecond)
		if _, err := e.svc.ClaimPairing(ctx, e.tok, f.challenge, "P", ip1); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("err = %v", err)
		}
		if err := e.svc.PairingPending(ctx, e.tok); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("pending = %v", err)
		}
	})
	t.Run("unknown and malformed", func(t *testing.T) {
		e := newPairEnv(t)
		other, _ := NewToken()
		for _, tok := range []string{other, "", "short", strings.Repeat("!", 43)} {
			if _, err := e.svc.ClaimPairing(ctx, tok, f.challenge, "P", ip1); !errors.Is(err, ErrPairEnded) {
				t.Errorf("%q: %v", tok, err)
			}
		}
	})
	t.Run("a claim does not extend life", func(t *testing.T) {
		e := newPairEnv(t)
		e.clk.Add(PairTTL - time.Second)
		claim, number, _ := e.claim(t)
		e.clk.Add(2 * time.Second)
		if err := e.svc.ApprovePairing(ctx, e.pair.ID, e.owner, number, ip1); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("approve after expiry = %v", err)
		}
		if _, err := e.svc.ClaimedPairing(ctx, claim); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("claimed view after expiry = %v", err)
		}
	})
}

func TestPairForbiddenApprove(t *testing.T) {
	ctx := context.Background()
	t.Run("before the claim", func(t *testing.T) {
		e := newPairEnv(t)
		if err := e.svc.ApprovePairing(ctx, e.pair.ID, e.owner, "0000", ip1); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("err = %v", err)
		}
		if e.rows(t) != 1 {
			t.Error("a premature approve ended the pairing")
		}
	})
	t.Run("another session of the same person", func(t *testing.T) {
		e := newPairEnv(t)
		_, number, _ := e.claim(t)
		l, _ := e.svc.Login(ctx, "ana", testPW, ip1, "ua2", nil)
		other, _, _, _ := e.sess.Lookup(ctx, l.Token)
		if err := e.svc.ApprovePairing(ctx, e.pair.ID, other, number, ip1); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("err = %v", err)
		}
		if _, err := e.svc.OwnerPairing(ctx, e.pair.ID, other); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("owner view for another session = %v", err)
		}
		if e.rows(t) != 1 {
			t.Error("another session ended the pairing")
		}
	})
	t.Run("wrong number deletes, then the right one fails", func(t *testing.T) {
		e := newPairEnv(t)
		claim, number, f := e.claim(t)
		wrong := "0000"
		if number == wrong {
			wrong = "0001"
		}
		if err := e.svc.ApprovePairing(ctx, e.pair.ID, e.owner, wrong, ip1); !errors.Is(err, ErrPairMismatch) {
			t.Fatalf("err = %v", err)
		}
		if e.rows(t) != 0 {
			t.Fatal("the pairing survived a wrong number")
		}
		if err := e.svc.ApprovePairing(ctx, e.pair.ID, e.owner, number, ip1); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("right number after wrong = %v", err)
		}
		if _, _, err := e.svc.FinishPairing(ctx, claim, "ua", ip1, nil); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("finish = %v", err)
		}
		_ = f
		if e.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'auth.pair_refused' AND detail_json LIKE '%number%'`) != 1 {
			t.Error("wrong number not audited")
		}
	})
	t.Run("second approve", func(t *testing.T) {
		e := newPairEnv(t)
		_, number, _ := e.claim(t)
		if err := e.svc.ApprovePairing(ctx, e.pair.ID, e.owner, number, ip1); err != nil {
			t.Fatal(err)
		}
		if err := e.svc.ApprovePairing(ctx, e.pair.ID, e.owner, number, ip1); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("not my phone", func(t *testing.T) {
		e := newPairEnv(t)
		claim, _, _ := e.claim(t)
		if err := e.svc.CancelPairing(ctx, e.pair.ID, e.owner, ip1); err != nil {
			t.Fatal(err)
		}
		if e.rows(t) != 0 {
			t.Fatal("cancel left the row")
		}
		if _, err := e.svc.ClaimedPairing(ctx, claim); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("err = %v", err)
		}
		if e.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'auth.pair_refused' AND detail_json LIKE '%declined%'`) != 1 {
			t.Error("decline not audited")
		}
	})
}

func TestPairForbiddenFinish(t *testing.T) {
	ctx := context.Background()
	t.Run("before approval", func(t *testing.T) {
		e := newPairEnv(t)
		claim, _, _ := e.claim(t)
		s := e.count(t, `SELECT COUNT(*) FROM sessions`)
		if _, _, err := e.svc.FinishPairing(ctx, claim, "ua", ip1, nil); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("err = %v", err)
		}
		e.wantNothingMade(t, s, 0)
		if e.rows(t) != 1 {
			t.Error("a premature finish spent the pairing")
		}
	})
	t.Run("the QR token is not a claim", func(t *testing.T) {
		e := newPairEnv(t)
		e.approved(t)
		s := e.count(t, `SELECT COUNT(*) FROM sessions`)
		if _, _, err := e.svc.FinishPairing(ctx, e.tok, "ua", ip1, nil); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("err = %v", err)
		}
		other, _ := NewToken()
		if _, _, err := e.svc.FinishPairing(ctx, other, "ua", ip1, nil); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("err = %v", err)
		}
		e.wantNothingMade(t, s, 0)
	})
	t.Run("twice", func(t *testing.T) {
		e := newPairEnv(t)
		claim, _ := e.approved(t)
		if _, _, err := e.svc.FinishPairing(ctx, claim, "ua", ip1, nil); err != nil {
			t.Fatal(err)
		}
		s := e.count(t, `SELECT COUNT(*) FROM sessions`)
		if _, _, err := e.svc.FinishPairing(ctx, claim, "ua", ip1, nil); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("err = %v", err)
		}
		e.wantNothingMade(t, s, 1)
	})
	t.Run("expired", func(t *testing.T) {
		e := newPairEnv(t)
		claim, _ := e.approved(t)
		e.clk.Add(PairTTL + time.Millisecond)
		s := e.count(t, `SELECT COUNT(*) FROM sessions`)
		if _, _, err := e.svc.FinishPairing(ctx, claim, "ua", ip1, nil); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("err = %v", err)
		}
		e.wantNothingMade(t, s, 0)
	})
	t.Run("owner signed out", func(t *testing.T) {
		e := newPairEnv(t)
		claim, _ := e.approved(t)
		if err := e.st.WriteTx(ctx, func(tx *sql.Tx) error { return e.sess.Delete(ctx, tx, e.owner.Hash) }); err != nil {
			t.Fatal(err)
		}
		if e.rows(t) != 0 {
			t.Fatal("the pairing outlived its session")
		}
		s := e.count(t, `SELECT COUNT(*) FROM sessions`)
		if _, _, err := e.svc.FinishPairing(ctx, claim, "ua", ip1, nil); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("err = %v", err)
		}
		e.wantNothingMade(t, s, 0)
	})
	t.Run("owner session lapsed", func(t *testing.T) {
		e := newPairEnv(t)
		claim, _ := e.approved(t)
		if _, err := e.st.Writer().Exec(`UPDATE sessions SET expires_at = ?`, e.clk.Now().UnixMilli()-1); err != nil {
			t.Fatal(err)
		}
		s := e.count(t, `SELECT COUNT(*) FROM sessions`)
		if _, _, err := e.svc.FinishPairing(ctx, claim, "ua", ip1, nil); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("err = %v", err)
		}
		e.wantNothingMade(t, s, 0)
	})
	t.Run("password changed", func(t *testing.T) {
		e := newPairEnv(t)
		claim, _ := e.approved(t)
		// A password change ends every session of the person, the pairing's owner among them.
		if _, err := e.st.Writer().Exec(`DELETE FROM sessions WHERE user_id = ?`, e.env.owner.ID); err != nil {
			t.Fatal(err)
		}
		if _, _, err := e.svc.FinishPairing(ctx, claim, "ua", ip1, nil); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("err = %v", err)
		}
		e.wantNothingMade(t, 0, 0)
	})
	t.Run("person disabled", func(t *testing.T) {
		e := newPairEnv(t)
		claim, _ := e.approved(t)
		if _, err := e.st.Writer().Exec(`UPDATE users SET disabled = 1 WHERE id = ?`, e.env.owner.ID); err != nil {
			t.Fatal(err)
		}
		s := e.count(t, `SELECT COUNT(*) FROM sessions`)
		if _, _, err := e.svc.FinishPairing(ctx, claim, "ua", ip1, nil); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("err = %v", err)
		}
		e.wantNothingMade(t, s, 0)
	})
	t.Run("person deleted", func(t *testing.T) {
		e := newPairEnv(t)
		claim, _ := e.approved(t)
		if _, err := e.st.Writer().Exec(`PRAGMA foreign_keys = OFF`); err != nil {
			t.Fatal(err)
		}
		defer e.st.Writer().Exec(`PRAGMA foreign_keys = ON`)
		if _, err := e.st.Writer().Exec(`DELETE FROM users WHERE id = ?`, e.env.owner.ID); err != nil {
			t.Fatal(err)
		}
		s := e.count(t, `SELECT COUNT(*) FROM sessions`)
		if _, _, err := e.svc.FinishPairing(ctx, claim, "ua", ip1, nil); !errors.Is(err, ErrPairEnded) {
			t.Fatalf("err = %v", err)
		}
		e.wantNothingMade(t, s, 0)
	})
	t.Run("bound previous session", func(t *testing.T) {
		e := newPairEnv(t)
		claim, _ := e.approved(t)
		s := e.count(t, `SELECT COUNT(*) FROM sessions`)
		prev := Session{DeviceID: "dv_1"}
		if _, _, err := e.svc.FinishPairing(ctx, claim, "ua", ip1, &prev); !errors.Is(err, ErrPairBound) {
			t.Fatalf("err = %v", err)
		}
		e.wantNothingMade(t, s, 0)
		if e.rows(t) != 1 {
			t.Error("a refused finish spent the pairing")
		}
	})
	t.Run("an unbound previous session is ended", func(t *testing.T) {
		e := newPairEnv(t)
		claim, _ := e.approved(t)
		l, _ := e.svc.Login(ctx, "ana", testPW, ip1, "ua2", nil)
		prev, _, _, _ := e.sess.Lookup(ctx, l.Token)
		s := e.count(t, `SELECT COUNT(*) FROM sessions`)
		if _, _, err := e.svc.FinishPairing(ctx, claim, "ua", ip1, &prev); err != nil {
			t.Fatal(err)
		}
		e.wantNothingMade(t, s, 1) // one ended, one made
	})
}

func TestPairSwapVerifier(t *testing.T) {
	ctx := context.Background()
	e := newPairEnv(t)
	claim, f := e.approved(t)
	_, code, err := e.svc.FinishPairing(ctx, claim, "ua", ip1, nil)
	if err != nil {
		t.Fatal(err)
	}
	other := a1NewFlow()
	if _, err := e.svc.SwapDeviceCode(ctx, code, other.verifier, "P", nil, ip1); !errors.Is(err, ErrBadCode) {
		t.Fatalf("another verifier = %v", err)
	}
	if _, err := e.svc.SwapDeviceCode(ctx, code, f.verifier, "P", nil, ip1); !errors.Is(err, ErrBadCode) {
		t.Fatalf("the code survived a wrong verifier: %v", err)
	}
}

func TestPairStartReplacesAndSweeps(t *testing.T) {
	ctx := context.Background()
	e := newPairEnv(t)
	_, tok2, err := e.svc.StartPairing(ctx, e.owner, ip1)
	if err != nil {
		t.Fatal(err)
	}
	if e.rows(t) != 1 {
		t.Fatalf("rows = %d, want one live pairing per session", e.rows(t))
	}
	if err := e.svc.PairingPending(ctx, e.tok); !errors.Is(err, ErrPairEnded) {
		t.Fatalf("old token = %v", err)
	}
	if err := e.svc.PairingPending(ctx, tok2); err != nil {
		t.Fatal(err)
	}
}

func TestPairFailedCountsAndLimits(t *testing.T) {
	ctx := context.Background()
	e := newPairEnv(t)
	for i := 0; i < ipMaxFails; i++ {
		if err := e.svc.PairAllowed(ip1); err != nil {
			t.Fatalf("limited after %d failures", i)
		}
		e.svc.PairFailed(ctx, ip1, "unknown")
	}
	var rl *RateLimitedError
	if err := e.svc.PairAllowed(ip1); !errors.As(err, &rl) {
		t.Fatalf("not limited: %v", err)
	}
	if err := e.svc.PairAllowed(netip.MustParseAddr("198.51.100.9")); err != nil {
		t.Fatalf("another address limited: %v", err)
	}
}

func TestPairCleanPhoneName(t *testing.T) {
	for in, want := range map[string]string{
		"": "Phone", "   ": "Phone", "Pixel 8": "Pixel 8", "  Ana's\x00 phone\n": "Ana's phone",
		strings.Repeat("é", 100): strings.Repeat("é", 64), "‮evil": "evil",
	} {
		if got := CleanPhoneName(in); got != want {
			t.Errorf("CleanPhoneName(%q) = %q, want %q", in, got, want)
		}
	}
}
