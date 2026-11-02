package share

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

func linkRepo(t *testing.T) (*fixture, *Links) {
	t.Helper()
	f := openFixture(t)
	return f, NewLinks(f.st, f.c, func() time.Time { return f.t0 })
}

func linkCreate(f *fixture, r *Links, by string, s LinkSpec, pw string) (l Link, tok string, err error) {
	err = f.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var e error
		l, tok, e = r.Create(context.Background(), tx, f.user(by), s, pw)
		return e
	})
	return
}

func TestCanLink(t *testing.T) {
	f := openFixture(t)
	now := f.t0.UnixMilli()
	ok := Link{ID: "l1", BookID: "bk_a1", ExpiresAt: now + 1, CreatorActive: true}
	book := func(id string) Target { return Target{Kind: TargetBook, ID: id} }
	cases := []struct {
		name string
		l    Link
		a    Action
		t    Target
		want bool
	}{
		{"view", ok, ViewBook, book("bk_a1"), true},
		{"listen", ok, ListenBook, book("bk_a1"), true},
		{"download", ok, DownloadBook, book("bk_a1"), false},
		{"read", ok, ReadText, book("bk_a1"), false},
		{"edit", ok, EditBook, book("bk_a1"), false},
		{"manage", ok, ManageUsers, book("bk_a1"), false},
		{"other book", ok, ViewBook, book("bk_a2"), false},
		{"empty target", ok, ViewBook, book(""), false},
		{"server", ok, ViewBook, Target{Kind: TargetServer}, false},
		{"library", ok, ViewBook, Target{Kind: TargetLibrary, ID: "bk_a1"}, false},
		{"revoked", Link{BookID: "bk_a1", ExpiresAt: now + 1, RevokedAt: 5, CreatorActive: true}, ViewBook, book("bk_a1"), false},
		{"expires exactly now", Link{BookID: "bk_a1", ExpiresAt: now, CreatorActive: true}, ViewBook, book("bk_a1"), false},
		{"creator inactive", Link{BookID: "bk_a1", ExpiresAt: now + 1}, ListenBook, book("bk_a1"), false},
	}
	for _, c := range cases {
		if got := f.c.CanLink(c.l, c.a, c.t); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestLinksCreateChecks(t *testing.T) {
	f, r := linkRepo(t)
	bad := []struct {
		name string
		by   string
		s    LinkSpec
		want error
	}{
		{"days 0", "u_own", LinkSpec{BookID: "bk_a1"}, ErrLinkSpec},
		{"days 2", "u_own", LinkSpec{BookID: "bk_a1", Days: 2}, ErrLinkSpec},
		{"days 91", "u_own", LinkSpec{BookID: "bk_a1", Days: 91}, ErrLinkSpec},
		{"label 81 runes", "u_own", LinkSpec{BookID: "bk_a1", Days: 7, Label: strings.Repeat("é", 81)}, ErrLinkSpec},
		{"member", "u_all", LinkSpec{BookID: "bk_a1", Days: 7}, ErrLinkRefused},
		{"guest", "u_g", LinkSpec{BookID: "bk_b1", Days: 7}, ErrLinkRefused},
		{"disabled", "u_dis", LinkSpec{BookID: "bk_a1", Days: 7}, ErrLinkRefused},
		{"unknown book", "u_own", LinkSpec{BookID: "nope", Days: 7}, ErrLinkRefused},
	}
	for _, c := range bad {
		if _, tok, err := linkCreate(f, r, c.by, c.s, ""); !errors.Is(err, c.want) || tok != "" {
			t.Errorf("%s: err %v, token %q, want %v", c.name, err, tok, c.want)
		}
	}
	f.exec(t, `UPDATE books SET refused_reason = 'drm' WHERE id = 'bk_a2'`)
	f.exec(t, `UPDATE books SET missing_since = 5 WHERE id = 'bk_b1'`)
	for _, id := range []string{"bk_a2", "bk_b1"} {
		if _, _, err := linkCreate(f, r, "u_adm", LinkSpec{BookID: id, Days: 7}, ""); !errors.Is(err, ErrLinkRefused) {
			t.Errorf("book %s: %v", id, err)
		}
	}
	if n := count(t, f, `SELECT count(*) FROM share_links`); n != 0 {
		t.Errorf("%d links written by refused creates", n)
	}
}

func count(t *testing.T, f *fixture, q string) int {
	t.Helper()
	var n int
	if err := f.st.Reader().QueryRow(q).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestLinksLifecycle(t *testing.T) {
	f, r := linkRepo(t)
	ctx := context.Background()
	l, tok, err := linkCreate(f, r, "u_adm", LinkSpec{BookID: "bk_a1", Days: 30, Label: "  Gran  "}, "hash")
	if err != nil {
		t.Fatal(err)
	}
	now := f.t0.UnixMilli()
	if l.Label != "Gran" || l.ExpiresAt != now+30*86400000 || !l.HasPassword() || !l.CreatorActive || l.BookID != "bk_a1" {
		t.Fatalf("created %+v", l)
	}
	got, err := r.Lookup(ctx, tok)
	if err != nil || got.ID != l.ID {
		t.Fatalf("lookup %+v %v", got, err)
	}
	if _, err := r.Lookup(ctx, "short"); !errors.Is(err, ErrLinkUnknown) {
		t.Errorf("malformed: %v", err)
	}
	if _, err := r.Lookup(ctx, strings.Repeat("A", 43)); !errors.Is(err, ErrLinkUnknown) {
		t.Errorf("unknown: %v", err)
	}
	// A link without a password, then a second book, then a longer-lived third.
	l2, _, err := linkCreate(f, r, "u_own", LinkSpec{BookID: "bk_b1", Days: 1}, "")
	if err != nil || l2.HasPassword() {
		t.Fatalf("second %+v %v", l2, err)
	}
	rows, err := r.Active(ctx, now, "", 50)
	if err != nil || len(rows) != 2 {
		t.Fatalf("active %v %v", rows, err)
	}
	if rows, _ := r.Active(ctx, now, "bk_a1", 50); len(rows) != 1 || rows[0].BookTitle != "bk_a1" {
		t.Errorf("per book %v", rows)
	}
	if rows, _ := r.Active(ctx, now+2*86400000, "", 50); len(rows) != 1 {
		t.Errorf("expired link listed: %v", rows)
	}
	revoke := func(by, id string) error {
		return f.st.WriteTx(ctx, func(tx *sql.Tx) error { return r.Revoke(ctx, tx, f.user(by), id) })
	}
	if err := revoke("u_all", l.ID); !errors.Is(err, ErrLinkRefused) {
		t.Errorf("member revoke: %v", err)
	}
	if err := revoke("u_own", "nope"); !errors.Is(err, ErrLinkUnknown) {
		t.Errorf("unknown revoke: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := revoke("u_adm", l.ID); err != nil {
			t.Fatalf("revoke %d: %v", i, err)
		}
	}
	if n := count(t, f, `SELECT count(*) FROM audit_log WHERE action = 'link.revoke'`); n != 1 {
		t.Errorf("%d revoke audit rows", n)
	}
	if rows, _ := r.Active(ctx, now, "", 50); len(rows) != 1 || rows[0].ID != l2.ID {
		t.Errorf("revoked still listed: %v", rows)
	}
	if got, _ := r.Lookup(ctx, tok); got.RevokedAt == 0 || f.c.CanLink(got, ViewBook, Target{Kind: TargetBook, ID: "bk_a1"}) {
		t.Errorf("revoked link %+v", got)
	}
}

func TestLinkCreatorDemoted(t *testing.T) {
	f, r := linkRepo(t)
	l, tok, err := linkCreate(f, r, "u_adm", LinkSpec{BookID: "bk_a1", Days: 7}, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{`UPDATE users SET role = 'member' WHERE id = 'u_adm'`, `UPDATE users SET role = 'admin', disabled = 1 WHERE id = 'u_adm'`} {
		f.exec(t, q)
		got, err := r.Lookup(context.Background(), tok)
		if err != nil || got.CreatorActive || f.c.CanLink(got, ViewBook, Target{Kind: TargetBook, ID: l.BookID}) {
			t.Errorf("%s: %+v %v", q, got, err)
		}
		if rows, _ := r.Active(context.Background(), f.t0.UnixMilli(), "", 50); len(rows) != 0 {
			t.Errorf("%s: listed %v", q, rows)
		}
	}
}
