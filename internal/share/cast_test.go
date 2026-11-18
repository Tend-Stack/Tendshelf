package share

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type castLab struct {
	*fixture
	r   *CastTokens
	now time.Time
}

func newCastLab(t *testing.T) *castLab {
	t.Helper()
	l := &castLab{fixture: openFixture(t)}
	l.now = l.t0
	l.r = NewCastTokens(l.st, func() time.Time { return l.now })
	return l
}

func (l *castLab) count(t *testing.T, user string) int {
	t.Helper()
	var n int
	if err := l.st.Reader().QueryRow(`SELECT count(*) FROM cast_tokens WHERE user_id = ?`, user).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestCastMintLookup(t *testing.T) {
	l := newCastLab(t)
	ctx := context.Background()
	tok, exp, err := l.r.Mint(ctx, "u_own", CastBook, "bk_1", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if exp != l.now.UnixMilli()+time.Hour.Milliseconds() {
		t.Errorf("expiresAt = %d", exp)
	}
	got, reason, err := l.r.Lookup(ctx, tok)
	if err != nil || reason != "" {
		t.Fatalf("lookup = %v %q", err, reason)
	}
	if got.UserID != "u_own" || got.Kind != CastBook || got.TargetID != "bk_1" || got.ExpiresAt != exp {
		t.Errorf("token = %+v", got)
	}
	if _, reason, err := l.r.Lookup(ctx, "short"); !errors.Is(err, ErrCastUnknown) || reason != CastMalformed {
		t.Errorf("malformed = %v %q", err, reason)
	}
	other, _, _ := l.r.Mint(ctx, "u_own", CastBook, "bk_2", time.Hour)
	if _, _, err := l.r.Lookup(ctx, strings.Repeat("A", len(other))); !errors.Is(err, ErrCastUnknown) {
		t.Errorf("guess = %v", err)
	}
	if _, _, err := l.r.Mint(ctx, "u_own", "file", "x", time.Hour); err == nil {
		t.Error("bad kind minted")
	}
}

func TestCastExpired(t *testing.T) {
	l := newCastLab(t)
	ctx := context.Background()
	tok, _, _ := l.r.Mint(ctx, "u_own", CastEpisode, "ep_1", time.Hour)
	l.now = l.now.Add(time.Hour)
	if _, reason, err := l.r.Lookup(ctx, tok); !errors.Is(err, ErrCastExpired) || reason != CastExpired {
		t.Fatalf("lookup at expiry = %v %q", err, reason)
	}
}

func TestCastPruneKeepsNewestEight(t *testing.T) {
	l := newCastLab(t)
	ctx := context.Background()
	var toks []string
	for i := 0; i < 10; i++ {
		l.now = l.now.Add(time.Second)
		tok, _, err := l.r.Mint(ctx, "u_own", CastBook, "bk_1", CastMaxLife)
		if err != nil {
			t.Fatal(err)
		}
		toks = append(toks, tok)
	}
	if n := l.count(t, "u_own"); n != 8 {
		t.Fatalf("rows = %d, want 8", n)
	}
	for i, tok := range toks {
		_, _, err := l.r.Lookup(ctx, tok)
		if (i < 2) != errors.Is(err, ErrCastUnknown) {
			t.Errorf("token %d: err = %v", i, err)
		}
	}
	if _, _, err := l.r.Mint(ctx, "u_adm", CastBook, "bk_1", time.Hour); err != nil || l.count(t, "u_own") != 8 {
		t.Errorf("another person's mint changed the first: %v", err)
	}
}

func TestCastMintSweepsExpired(t *testing.T) {
	l := newCastLab(t)
	ctx := context.Background()
	if _, _, err := l.r.Mint(ctx, "u_adm", CastBook, "bk_1", time.Hour); err != nil {
		t.Fatal(err)
	}
	l.now = l.now.Add(2 * time.Hour)
	if _, _, err := l.r.Mint(ctx, "u_own", CastBook, "bk_1", time.Hour); err != nil {
		t.Fatal(err)
	}
	if n := l.count(t, "u_adm"); n != 0 {
		t.Errorf("expired row of another person kept: %d", n)
	}
}

func TestCastDeleteForUser(t *testing.T) {
	l := newCastLab(t)
	ctx := context.Background()
	a, _, _ := l.r.Mint(ctx, "u_own", CastBook, "bk_1", time.Hour)
	b, _, _ := l.r.Mint(ctx, "u_adm", CastBook, "bk_1", time.Hour)
	if err := l.r.DeleteForUser(ctx, "u_own"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := l.r.Lookup(ctx, a); !errors.Is(err, ErrCastUnknown) {
		t.Errorf("deleted token = %v", err)
	}
	if _, _, err := l.r.Lookup(ctx, b); err != nil {
		t.Errorf("other person's token = %v", err)
	}
}

func TestCastStoresHashOnly(t *testing.T) {
	l := newCastLab(t)
	ctx := context.Background()
	tok, _, _ := l.r.Mint(ctx, "u_own", CastBook, "bk_1", time.Hour)
	rows, err := l.st.Reader().Query(`SELECT hex(token_hash), user_id, kind, target_id, CAST(created_at AS TEXT), CAST(expires_at AS TEXT) FROM cast_tokens`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		n++
		cols := make([]string, 6)
		if err := rows.Scan(&cols[0], &cols[1], &cols[2], &cols[3], &cols[4], &cols[5]); err != nil {
			t.Fatal(err)
		}
		for _, c := range cols {
			if strings.Contains(c, tok) {
				t.Errorf("a column holds the raw token: %q", c)
			}
		}
	}
	if n != 1 {
		t.Errorf("rows = %d", n)
	}
}

func TestCastLifeClamp(t *testing.T) {
	h := int64(time.Hour / time.Millisecond)
	for _, c := range []struct {
		ms   int64
		want time.Duration
	}{{0, 6 * time.Hour}, {-5, 6 * time.Hour}, {40 * h, 46 * time.Hour}, {100 * h, 72 * time.Hour}} {
		if got := CastLife(c.ms); got != c.want {
			t.Errorf("CastLife(%d) = %v, want %v", c.ms, got, c.want)
		}
	}
}
