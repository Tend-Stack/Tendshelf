package auth

import (
	"context"
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth/oidc"
	"github.com/Tend-Stack/tendshelf/internal/auth/oidc/oidctest"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

func TestOIDCOpenRowsPerAddress(t *testing.T) {
	ctx := context.Background()
	o := newOIDCEnv(t, nil)
	now := o.clk.Now().UnixMilli()
	nonce := strings.Repeat("n", 43)
	for i := range oidcOpenPerAddr {
		o.sql(t, `INSERT INTO oidc_logins (id, state_hash, purpose, issuer, nonce, verifier, created_at, expires_at, addr_key) VALUES (?, randomblob(32), 'signin', 'x', ?, ?, ?, ?, ?)`,
			"row"+strconv.Itoa(i), nonce, nonce, now, now+60_000, IPKey(ip1))
	}
	if _, _, err := o.svc.OIDCStart(ctx, "signin", "/", ip1, nil); !errors.Is(err, ErrOIDCBusy) {
		t.Fatalf("21st start from one key: %v", err)
	}
	// Another address in the same /64 shares the key.
	if _, _, err := o.svc.OIDCStart(ctx, "signin", "/", netip.MustParseAddr("203.0.113.99"), nil); err != nil {
		t.Fatalf("another key: %v", err)
	}
	// Rows that expire free the slot.
	o.sql(t, `UPDATE oidc_logins SET expires_at = ? WHERE addr_key = ?`, now-1, IPKey(ip1))
	if _, _, err := o.svc.OIDCStart(ctx, "signin", "/", ip1, nil); err != nil {
		t.Fatalf("after expiry: %v", err)
	}
	if got := o.count(t, `SELECT count(*) FROM oidc_logins WHERE addr_key = ?`, IPKey(ip1)); got != 1 {
		t.Errorf("rows for the key = %d", got)
	}
}

func TestOIDCEmailASCIIFolding(t *testing.T) {
	t.Run("asciiLower folds A-Z only", func(t *testing.T) {
		for in, want := range map[string]string{"Ana@Example.COM": "ana@example.com", "JÜRGEN@x.de": "jÜrgen@x.de", "K@x": "k@x"} {
			if got := asciiLower(in); got != want {
				t.Errorf("asciiLower(%q) = %q, want %q", in, got, want)
			}
		}
		if asciiLower("K@x") == "k@x" {
			t.Error("the Kelvin sign folded to ASCII k")
		}
	})
	t.Run("ASCII case differences still match", func(t *testing.T) {
		o := newOIDCEnv(t, func(in *oidc.ConfigInput) { in.MatchEmail = true })
		m := o.member(t, "Rosa", users.RoleMember, "ANA@Example.com", true)
		res, err := o.signIn(t)
		if err != nil || res.Login.User.ID != m.ID {
			t.Fatalf("res = %+v, err = %v", res, err)
		}
	})
	t.Run("non-ASCII case difference creates no second holder of the address", func(t *testing.T) {
		o := newOIDCEnv(t, func(in *oidc.ConfigInput) { in.MatchEmail, in.CreateAccounts = true, true })
		o.member(t, "Jurgen", users.RoleMember, "JÜRGEN@example.de", true)
		o.p.Update(func(p *oidctest.Provider) { p.Email = "jürgen@example.de" })
		res, err := o.signIn(t)
		if err != nil {
			t.Fatal(err)
		}
		// Not matched (no silent takeover), and the new account does not store the address.
		var email *string
		if err := o.st.Reader().QueryRow(`SELECT email FROM users WHERE id = ?`, res.Login.User.ID).Scan(&email); err != nil {
			t.Fatal(err)
		}
		if email != nil && *email != "" {
			t.Errorf("new account stored %q", *email)
		}
		if o.count(t, `SELECT count(*) FROM users WHERE lower(email) = 'jürgen@example.de'`) != 0 {
			t.Error("two accounts hold the address")
		}
	})
	t.Run("Kelvin sign does not match ASCII k", func(t *testing.T) {
		o := newOIDCEnv(t, func(in *oidc.ConfigInput) { in.MatchEmail = true })
		o.member(t, "Rosa", users.RoleMember, "k@example.com", true)
		o.p.Update(func(p *oidctest.Provider) { p.Email = "K@example.com" })
		if _, err := o.signIn(t); !errors.Is(err, ErrOIDCNoAccount) || o.identities(t) != 0 {
			t.Fatalf("err = %v, identities %d", err, o.identities(t))
		}
	})
}

func TestOIDCCookielessCallbackKeepsRow(t *testing.T) {
	ctx := context.Background()
	o := newOIDCEnv(t, func(in *oidc.ConfigInput) { in.CreateAccounts = true })
	cb := o.begin(t, "signin", nil)
	for _, bad := range []string{"", oidc.RandomToken()} {
		if _, err := o.svc.OIDCFinish(ctx, OIDCCallback{State: cb.State, Cookie: bad, Code: cb.Code}, ip1, "ua", nil); !errors.Is(err, ErrOIDCFailed) {
			t.Fatalf("cookie %q: %v", bad, err)
		}
		if o.openRows(t) != 1 {
			t.Fatalf("row consumed by a callback with cookie %q", bad)
		}
	}
	if _, err := o.svc.OIDCFinish(ctx, cb, ip1, "ua", nil); err != nil {
		t.Fatalf("the real callback: %v", err)
	}
}
