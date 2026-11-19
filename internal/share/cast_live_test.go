package share

import (
	"context"
	"errors"
	"testing"
	"time"
)

// The iPhone live address (decision 0049).

func (l *castLab) device(t *testing.T, id string) {
	t.Helper()
	l.exec(t, `INSERT INTO devices(id, user_id, name, kind, token_hash, created_at) VALUES (?, 'u_own', 'Phone', 'mobile', randomblob(32), 1)`, id)
}

func TestCastLiveOnePerDevice(t *testing.T) {
	l := newCastLab(t)
	ctx := context.Background()
	l.device(t, "dv_a")
	l.device(t, "dv_b")
	first, exp, err := l.r.MintLive(ctx, "u_own", "dv_a", "st_1")
	if err != nil || exp != l.now.Add(LiveAddressLife).UnixMilli() {
		t.Fatalf("mint = %v, %d", err, exp)
	}
	second, _, err := l.r.MintLive(ctx, "u_own", "dv_a", "st_1")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := l.r.Lookup(ctx, first); !errors.Is(err, ErrCastUnknown) {
		t.Errorf("first after second mint = %v", err)
	}
	got, _, err := l.r.Lookup(ctx, second)
	if err != nil || got.DeviceID != "dv_a" || got.Kind != CastStation || got.TargetID != "st_1" {
		t.Fatalf("second = %+v, %v", got, err)
	}
	if _, _, err := l.r.MintLive(ctx, "u_own", "dv_b", "st_1"); err != nil {
		t.Fatal(err)
	}
	if n := l.count(t, "u_own"); n != 2 {
		t.Errorf("rows = %d, want 2", n)
	}
	for _, bad := range [][3]string{{"", "dv_a", "st_1"}, {"u_own", "", "st_1"}, {"u_own", "dv_a", ""}} {
		if _, _, err := l.r.MintLive(ctx, bad[0], bad[1], bad[2]); !errors.Is(err, ErrCastUnknown) {
			t.Errorf("MintLive%v = %v", bad, err)
		}
	}
}

func TestCastLiveExpiresAndRevoked(t *testing.T) {
	l := newCastLab(t)
	ctx := context.Background()
	l.device(t, "dv_a")
	tok, _, _ := l.r.MintLive(ctx, "u_own", "dv_a", "st_1")
	l.now = l.now.Add(LiveAddressLife)
	if _, reason, err := l.r.Lookup(ctx, tok); !errors.Is(err, ErrCastExpired) || reason != CastExpired {
		t.Fatalf("at +1h = %v %q", err, reason)
	}
	l.now = l.t0
	if _, _, err := l.r.Lookup(ctx, tok); err != nil {
		t.Fatalf("before expiry = %v", err)
	}
	l.exec(t, `UPDATE devices SET revoked_at = 1, token_hash = NULL WHERE id = 'dv_a'`)
	if _, reason, err := l.r.Lookup(ctx, tok); !errors.Is(err, ErrCastExpired) || reason != CastExpired {
		t.Fatalf("revoked device = %v %q", err, reason)
	}
}

func TestCastLiveDeletes(t *testing.T) {
	l := newCastLab(t)
	ctx := context.Background()
	l.device(t, "dv_a")
	l.device(t, "dv_b")
	l.r.MintLive(ctx, "u_own", "dv_a", "st_1")
	l.r.MintLive(ctx, "u_own", "dv_b", "st_1")
	l.r.Mint(ctx, "u_own", CastBook, "bk_1", time.Hour)
	if err := l.r.DeleteForUser(ctx, "u_own"); err != nil {
		t.Fatal(err)
	}
	if n := l.count(t, "u_own"); n != 2 {
		t.Fatalf("after DeleteForUser rows = %d, want the 2 device rows", n)
	}
	if err := l.r.DeleteForDevice(ctx, "dv_a"); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := l.st.Reader().QueryRow(`SELECT count(*) FROM cast_tokens WHERE device_id = 'dv_b'`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("dv_b rows = %d, %v", n, err)
	}
	if got := l.count(t, "u_own"); got != 1 {
		t.Errorf("rows = %d, want 1", got)
	}
}
