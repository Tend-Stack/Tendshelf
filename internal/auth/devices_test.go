package auth

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// issue creates a device token for the env's owner through the service.
func (e *env) issue(t *testing.T, name string, kind DeviceKind) DeviceLogin {
	t.Helper()
	l, err := e.svc.IssueDevice(context.Background(), "ana", testPW, name, kind, ip1)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	return l
}

func (e *env) deviceRow(t *testing.T, id string) (lastSeen sql.NullInt64, revoked sql.NullInt64, hashLen int) {
	t.Helper()
	err := e.st.Reader().QueryRow(`SELECT last_seen_at, revoked_at, COALESCE(length(token_hash), 0) FROM devices WHERE id = ?`, id).
		Scan(&lastSeen, &revoked, &hashLen)
	if err != nil {
		t.Fatal(err)
	}
	return
}

func TestDeviceIssueLookup(t *testing.T) {
	e := newEnv(t).withOwner(t)
	l := e.issue(t, "Kitchen tablet", KindMobile)
	raw, err := base64.RawURLEncoding.DecodeString(l.Token)
	if err != nil || len(raw) != 32 {
		t.Fatalf("token: %v len %d", err, len(raw))
	}
	sum := sha256.Sum256(raw)
	if n := e.count(t, `SELECT COUNT(*) FROM devices WHERE token_hash = ?`, sum[:]); n != 1 {
		t.Fatal("the SHA-256 of the token is not stored")
	}
	if n := e.count(t, `SELECT COUNT(*) FROM devices WHERE token_hash = ? OR name = ?`, raw, l.Token); n != 0 {
		t.Fatal("the raw token is stored")
	}
	dev, u, err := e.dev.Lookup(context.Background(), l.Token)
	if err != nil || dev.ID != l.Device.ID || u.ID != e.owner.ID || dev.Kind != KindMobile || dev.Name != "Kitchen tablet" {
		t.Fatalf("lookup: %+v %+v %v", dev, u, err)
	}
	var all string
	_ = e.st.Reader().QueryRow(`SELECT group_concat(detail_json || target || action) FROM audit_log`).Scan(&all)
	if strings.Contains(all, l.Token) || !strings.Contains(all, "auth.device_issued") {
		t.Fatalf("audit: %q", all)
	}
}

func TestDeviceLookupRefuses(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	good := e.issue(t, "", KindWeb)
	other, _ := NewToken()
	for name, tok := range map[string]string{"empty": "", "malformed": "abc", "unknown": other} {
		if _, _, err := e.dev.Lookup(ctx, tok); !errors.Is(err, ErrNoDevice) {
			t.Errorf("%s: %v", name, err)
		}
	}

	revoked := e.issue(t, "r", KindWeb)
	if _, err := e.dev.Revoke(ctx, e.st.Writer(), e.owner.ID, revoked.Device.ID, e.clk.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.dev.Lookup(ctx, revoked.Token); !errors.Is(err, ErrNoDevice) {
		t.Errorf("revoked: %v", err)
	}

	// Idle: exactly the limit is fine, one millisecond more revokes the row.
	e.clk.Add(DeviceIdleTTL)
	if _, _, err := e.dev.Lookup(ctx, good.Token); err != nil {
		t.Fatalf("idle exactly 180 days: %v", err)
	}
	// That lookup counted as use; make a fresh idle device instead.
	idle := e.issue(t, "idle", KindWeb)
	e.clk.Add(DeviceIdleTTL + time.Millisecond)
	if _, _, err := e.dev.Lookup(ctx, idle.Token); !errors.Is(err, ErrNoDevice) {
		t.Fatalf("idle 180 days + 1 ms: %v", err)
	}
	if _, rev, hl := e.deviceRow(t, idle.Device.ID); !rev.Valid || hl != 0 {
		t.Fatalf("idle row not revoked: %v %d", rev, hl)
	}

	// Disabled account.
	again := e.issue(t, "d", KindWeb)
	if _, err := e.st.Writer().Exec(`UPDATE users SET disabled = 1 WHERE id = ?`, e.owner.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.dev.Lookup(ctx, again.Token); !errors.Is(err, ErrNoDevice) {
		t.Fatalf("disabled: %v", err)
	}
	if _, rev, hl := e.deviceRow(t, again.Device.ID); rev.Valid || hl != 32 {
		t.Fatal("a disabled user's token must be refused but its row left alone")
	}
}

func TestDeviceLastSeenThrottle(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	l := e.issue(t, "", KindDesktop)
	if _, _, err := e.dev.Lookup(ctx, l.Token); err != nil { // first use writes
		t.Fatal(err)
	}
	first, _, _ := e.deviceRow(t, l.Device.ID)
	if !first.Valid {
		t.Fatal("first use did not write last_seen_at")
	}
	e.clk.Add(59 * time.Minute)
	if _, _, err := e.dev.Lookup(ctx, l.Token); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := e.deviceRow(t, l.Device.ID); got != first {
		t.Fatalf("59 minutes wrote: %v -> %v", first, got)
	}
	e.clk.Add(time.Minute)
	if _, _, err := e.dev.Lookup(ctx, l.Token); err != nil {
		t.Fatal(err)
	}
	now := e.clk.Now().UnixMilli()
	if got, _, _ := e.deviceRow(t, l.Device.ID); got.Int64 != now {
		t.Fatalf("60 minutes did not write the device: %v want %d", got, now)
	}
	var seen int64
	_ = e.st.Reader().QueryRow(`SELECT last_seen_at FROM users WHERE id = ?`, e.owner.ID).Scan(&seen)
	if seen != now {
		t.Fatalf("user last_seen_at %d want %d", seen, now)
	}
}

func TestDeviceRevokeScoped(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	l := e.issue(t, "", KindWeb)
	now := e.clk.Now().UnixMilli()
	if ok, err := e.dev.Revoke(ctx, e.st.Writer(), "someone-else", l.Device.ID, now); ok || err != nil {
		t.Fatalf("another person's revoke: %v %v", ok, err)
	}
	if ok, err := e.svc.RevokeDevice(ctx, e.owner.ID, l.Device.ID, e.owner.ID); !ok || err != nil {
		t.Fatalf("own revoke: %v %v", ok, err)
	}
	if ok, _ := e.svc.RevokeDevice(ctx, e.owner.ID, l.Device.ID, e.owner.ID); ok {
		t.Fatal("revoking twice reported success")
	}
	if n := e.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'auth.device_revoked'`); n != 1 {
		t.Fatalf("audit rows = %d", n)
	}
	if list, _ := e.dev.ListForUser(ctx, e.owner.ID); len(list) != 0 {
		t.Fatalf("revoked device listed: %+v", list)
	}
}

func TestDeviceRevokeAll(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	a, b := e.issue(t, "a", KindWeb), e.issue(t, "b", KindPWA)
	var n int64
	err := e.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var err error
		n, err = e.dev.RevokeAll(ctx, tx, e.owner.ID, e.clk.Now().UnixMilli())
		return err
	})
	if err != nil || n != 2 {
		t.Fatalf("revoked %d: %v", n, err)
	}
	for _, tok := range []string{a.Token, b.Token} {
		if _, _, err := e.dev.Lookup(ctx, tok); !errors.Is(err, ErrNoDevice) {
			t.Fatalf("token still works: %v", err)
		}
	}
}

func TestDeviceListOrder(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	old := e.issue(t, "old", KindWeb)
	e.clk.Add(time.Minute)
	fresh := e.issue(t, "fresh", KindWeb)
	all, err := e.dev.ListAll(ctx)
	if err != nil || len(all) != 2 || all[0].ID != fresh.Device.ID || all[1].ID != old.Device.ID {
		t.Fatalf("order: %+v %v", all, err)
	}
	if all[0].LastSeenAt != 0 {
		t.Fatal("never used must read as 0")
	}
}

func TestDeviceIssueCap(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	for i := 0; i < MaxLiveDevices; i++ {
		e.issue(t, fmt.Sprintf("d%d", i), KindWeb)
	}
	_, err := e.svc.IssueDevice(ctx, "ana", testPW, "one too many", KindWeb, ip1)
	if !errors.Is(err, ErrTooManyDevices) {
		t.Fatalf("51st: %v", err)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM devices`); n != MaxLiveDevices {
		t.Fatalf("rows = %d", n)
	}
}

func TestDeviceIssuePrunes(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	oldDev, newDev := e.issue(t, "old", KindWeb), e.issue(t, "new", KindWeb)
	day := 24 * time.Hour
	now := e.clk.Now()
	for id, age := range map[string]time.Duration{oldDev.Device.ID: 31 * day, newDev.Device.ID: 29 * day} {
		if _, err := e.st.Writer().Exec(`UPDATE devices SET revoked_at = ?, token_hash = NULL WHERE id = ?`,
			now.Add(-age).UnixMilli(), id); err != nil {
			t.Fatal(err)
		}
	}
	e.issue(t, "trigger", KindWeb)
	if n := e.count(t, `SELECT COUNT(*) FROM devices WHERE id = ?`, oldDev.Device.ID); n != 0 {
		t.Fatal("a row revoked 31 days ago was kept")
	}
	if n := e.count(t, `SELECT COUNT(*) FROM devices WHERE id = ?`, newDev.Device.ID); n != 1 {
		t.Fatal("a row revoked 29 days ago was pruned")
	}
	_ = ctx
}

func TestDeviceName(t *testing.T) {
	long := strings.Repeat("é", 70)
	for _, c := range []struct {
		in   string
		kind DeviceKind
		want string
	}{
		{"  My   phone ", KindMobile, "My phone"},
		{"a\x00b\x1b[31mc\td", KindWeb, "ab[31mc d"},
		{"line\nbreak", KindWeb, "line break"},
		{long, KindWeb, strings.Repeat("é", 64)},
		{"", KindWeb, "Web app"},
		{" \t\x07 ", KindPWA, "Installed app"},
		{"", KindDesktop, "Desktop app"},
		{"", KindMobile, "Phone app"},
	} {
		if got := DeviceName(c.in, c.kind); got != c.want {
			t.Errorf("DeviceName(%q, %s) = %q, want %q", c.in, c.kind, got, c.want)
		}
	}
	if _, ok := ParseDeviceKind("tv"); ok {
		t.Error("tv accepted")
	}
	if k, ok := ParseDeviceKind("pwa"); !ok || k != KindPWA {
		t.Error("pwa refused")
	}
}

func TestIssueDeviceSharesLimiter(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	var rl *RateLimitedError

	// 20 failed password sign-ins from one address stop token issue from it.
	ip := netip.MustParseAddr("198.51.100.30")
	for i := 0; i < ipMaxFails; i++ {
		_, _ = e.svc.Login(ctx, fmt.Sprintf("ghost%d", i), "x", ip, "ua", nil)
	}
	if _, err := e.svc.IssueDevice(ctx, "ana", testPW, "", KindWeb, ip); !errors.As(err, &rl) {
		t.Fatalf("after 20 failed logins: %v", err)
	}

	// 10 failures against the account, from different addresses, stop it everywhere.
	for i := 0; i < userMaxFails; i++ {
		other := netip.MustParseAddr(fmt.Sprintf("192.0.2.%d", i+1))
		if _, err := e.svc.IssueDevice(ctx, "ana", "wrong", "", KindWeb, other); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("try %d: %v", i, err)
		}
	}
	rl = nil
	_, err := e.svc.IssueDevice(ctx, "ana", testPW, "", KindWeb, netip.MustParseAddr("192.0.2.200"))
	if !errors.As(err, &rl) {
		t.Fatalf("per-account limit: %v", err)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM devices`); n != 0 {
		t.Fatal("a token was issued while limited")
	}
	if n := e.count(t, `SELECT COUNT(*) FROM audit_log WHERE action = 'auth.login_failed'`); n < userMaxFails {
		t.Fatalf("failures not audited: %d", n)
	}
}

func TestIssueDeviceDisabled(t *testing.T) {
	e := newEnv(t).withOwner(t)
	if _, err := e.st.Writer().Exec(`UPDATE users SET disabled = 1`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.IssueDevice(context.Background(), "ana", testPW, "", KindWeb, ip1); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("disabled: %v", err)
	}
	if n := e.count(t, `SELECT COUNT(*) FROM devices`); n != 0 {
		t.Fatal("token issued to a disabled account")
	}
}

func TestSignOutOthersRevokesDevices(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	l := e.issue(t, "", KindWeb)
	cur, _ := e.manageLogin(t, "A")
	if _, err := e.svc.SignOutOthers(ctx, cur); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.dev.Lookup(ctx, l.Token); !errors.Is(err, ErrNoDevice) {
		t.Fatalf("token survived: %v", err)
	}
}

func TestChangePasswordRevokesDevices(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	l := e.issue(t, "", KindWeb)
	cur, _ := e.manageLogin(t, "A")
	if _, err := e.svc.ChangePassword(ctx, cur, testPW, "a brand new password 42", ip1); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.dev.Lookup(ctx, l.Token); !errors.Is(err, ErrNoDevice) {
		t.Fatalf("token survived: %v", err)
	}
}

func TestResetPasswordRevokesDevices(t *testing.T) {
	e := newEnv(t).withOwner(t)
	ctx := context.Background()
	l := e.issue(t, "", KindWeb)
	if _, err := e.svc.ResetPassword(ctx, "ana"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.dev.Lookup(ctx, l.Token); !errors.Is(err, ErrNoDevice) {
		t.Fatalf("token survived: %v", err)
	}
}
