package share

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/token"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

type inviteEnv struct {
	f   *fixture
	inv *Invites
}

func newInviteEnv(t *testing.T) *inviteEnv {
	t.Helper()
	f := openFixture(t)
	ur := users.New(f.st, users.Options{Now: func() time.Time { return f.t0 }})
	return &inviteEnv{f: f, inv: NewInvites(f.st, f.c, NewGrants(f.st), ur, func() time.Time { return f.t0 })}
}

func (e *inviteEnv) create(by string, s InviteSpec) (Invite, string, error) {
	var inv Invite
	var tok string
	err := e.f.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		inv, tok, err = e.inv.Create(context.Background(), tx, e.f.users[by], s)
		return err
	})
	return inv, tok, err
}

func (e *inviteEnv) mustCreate(t *testing.T, by string, s InviteSpec) (Invite, string) {
	t.Helper()
	inv, tok, err := e.create(by, s)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	return inv, tok
}

func (e *inviteEnv) accept(tok string) (users.User, Invite, error) {
	var u users.User
	var inv Invite
	hash, _ := token.Hash(tok)
	err := e.f.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		u, inv, err = e.inv.Accept(context.Background(), tx, hash, "")
		return err
	})
	return u, inv, err
}

func (e *inviteEnv) write(fn func(tx *sql.Tx) error) error {
	return e.f.st.WriteTx(context.Background(), fn)
}

func (e *inviteEnv) count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	if err := e.f.st.Reader().QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func memberSpec(grants ...InviteGrant) InviteSpec {
	if len(grants) == 0 {
		grants = []InviteGrant{{Scope: GrantBook, Target: "bk_a1"}}
	}
	return InviteSpec{Name: "Rosa", Email: "rosa@example.org", Note: "Hi", Role: users.RoleMember, Grants: grants}
}

func TestInviteCreate(t *testing.T) {
	e := newInviteEnv(t)
	adminSpec := InviteSpec{Name: "Ana", Role: users.RoleAdmin}
	if _, _, err := e.create("u_own", adminSpec); err != nil {
		t.Fatalf("owner invites an admin: %v", err)
	}
	for _, tc := range []struct {
		name string
		by   string
		spec InviteSpec
		want error
	}{
		{"admin invites an admin", "u_adm", adminSpec, ErrInviteRefused},
		{"member invites", "u_lib", memberSpec(), ErrInviteRefused},
		{"guest invites", "u_g", memberSpec(), ErrInviteRefused},
		{"disabled member", "u_dis", memberSpec(), ErrInviteRefused},
		{"unknown inviter", "u_nobody", memberSpec(), ErrInviteRefused},
		{"owner role", "u_own", InviteSpec{Name: "X", Role: users.RoleOwner}, ErrInviteRefused},
		{"admin with grants", "u_own", InviteSpec{Name: "Ana", Role: users.RoleAdmin, Grants: []InviteGrant{{Scope: GrantAll}}}, ErrInviteGrants},
		{"member with none", "u_adm", InviteSpec{Name: "R", Role: users.RoleMember}, ErrInviteGrants},
		{"duplicate", "u_adm", memberSpec(InviteGrant{Scope: GrantBook, Target: "bk_a1"}, InviteGrant{Scope: GrantBook, Target: "bk_a1", Read: true}), ErrInviteGrants},
		{"bad scope", "u_adm", memberSpec(InviteGrant{Scope: "nope"}), ErrInviteGrants},
		{"all with target", "u_adm", memberSpec(InviteGrant{Scope: GrantAll, Target: "x"}), ErrInviteGrants},
		{"bad target", "u_adm", memberSpec(InviteGrant{Scope: GrantBook, Target: "bk_gone"}), ErrGrantTarget},
		{"member shelf", "u_adm", memberSpec(InviteGrant{Scope: GrantShelf, Target: "sh_mem"}), ErrGrantTarget},
		{"too many uses", "u_adm", func() InviteSpec { s := memberSpec(); s.MaxUses = 51; return s }(), ErrInviteGrants},
	} {
		if _, _, err := e.create(tc.by, tc.spec); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", tc.name, err, tc.want)
		}
	}
	var many []InviteGrant
	for i := 0; i < 21; i++ {
		many = append(many, InviteGrant{Scope: GrantBook, Target: string(rune('a' + i))})
	}
	if _, _, err := e.create("u_adm", memberSpec(many...)); !errors.Is(err, ErrInviteGrants) {
		t.Errorf("21 grants: err = %v", err)
	}

	// field problems
	s := memberSpec()
	s.Name, s.Email, s.Note, s.ExpiresAt = "  ", "not mail", strings.Repeat("x", 501), e.f.t0.UnixMilli()
	_, _, err := e.create("u_adm", s)
	var fe users.FieldErrors
	if !errors.As(err, &fe) || fe["name"] != "required" || fe["email"] != "invalid" || fe["note"] != "too_long" || fe["expires"] != "past" {
		t.Errorf("field errors = %v", err)
	}
	if n := e.count(t, `SELECT count(*) FROM invites`); n != 1 {
		t.Errorf("failed creates wrote rows: %d invites", n)
	}
}

func TestInviteCreateStoresOnlyTheHash(t *testing.T) {
	e := newInviteEnv(t)
	s := memberSpec(InviteGrant{Scope: GrantLibrary, Target: "lib_a", Download: true})
	s.ExpiresAt = e.f.t0.UnixMilli() + 60_000
	inv, tok := e.mustCreate(t, "u_adm", s)
	hash, ok := token.Hash(tok)
	if !ok {
		t.Fatal("token does not parse")
	}
	var stored []byte
	if err := e.f.st.Reader().QueryRow(`SELECT token_hash FROM invites WHERE id = ?`, inv.ID).Scan(&stored); err != nil || string(stored) != string(hash[:]) {
		t.Fatalf("stored hash differs: %v", err)
	}
	for _, q := range []string{`SELECT count(*) FROM invites WHERE name || note || grants_json LIKE ?`, `SELECT count(*) FROM audit_log WHERE detail_json || target || action LIKE ?`} {
		if n := e.count(t, q, "%"+tok+"%"); n != 0 {
			t.Errorf("raw token found in the database (%s)", q)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'invite.create' AND user_id = 'u_adm'`); n != 1 {
		t.Errorf("create audit rows = %d", n)
	}
	if inv.InviterName == "" || inv.MaxUses != 1 || inv.CreatedBy != "u_adm" || len(inv.Grants) != 1 || !inv.Grants[0].Download {
		t.Errorf("invite = %+v", inv)
	}
}

func TestInviteAcceptCreatesAccountAndGrants(t *testing.T) {
	e := newInviteEnv(t)
	_, tok := e.mustCreate(t, "u_adm", memberSpec(
		InviteGrant{Scope: GrantLibrary, Target: "lib_a", Download: true},
		InviteGrant{Scope: GrantBook, Target: "bk_b1", Read: true}))
	u, inv, err := e.accept(tok)
	if err != nil {
		t.Fatal(err)
	}
	if u.Role != users.RoleMember || u.DisplayName != "Rosa" || u.Email != "rosa@example.org" || inv.UseCount != 1 {
		t.Fatalf("user %+v invite %+v", u, inv)
	}
	gs, _ := NewGrants(e.f.st).ForUser(context.Background(), u.ID)
	if len(gs) != 2 {
		t.Fatalf("grants = %+v", gs)
	}
	for _, g := range gs {
		if g.CreatedBy != "u_adm" || g.ExpiresAt != 0 {
			t.Errorf("grant %+v", g)
		}
		if (g.Scope == GrantLibrary) != g.CanDownload || (g.Scope == GrantBook) != g.CanRead {
			t.Errorf("flags %+v", g)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'invite.accept' AND user_id = ?`, u.ID); n != 1 {
		t.Errorf("accept audit rows = %d", n)
	}
	if has, _ := users.New(e.f.st, users.Options{}).HasPassword(context.Background(), u.ID); has {
		t.Error("an invited account must start without a password")
	}
	if _, _, err := e.accept(tok); !errors.Is(err, ErrInviteUsedUp) {
		t.Errorf("second accept: %v", err)
	}

	// an admin invite makes an admin with no grants
	_, atok := e.mustCreate(t, "u_own", InviteSpec{Name: "Ana", Role: users.RoleAdmin})
	a, _, err := e.accept(atok)
	if err != nil || a.Role != users.RoleAdmin {
		t.Fatalf("admin accept: %v %+v", err, a)
	}
	if n := e.count(t, `SELECT count(*) FROM grants WHERE user_id = ?`, a.ID); n != 0 {
		t.Errorf("admin has %d grants", n)
	}
}

func TestInviteAcceptRace(t *testing.T) {
	e := newInviteEnv(t)
	_, tok := e.mustCreate(t, "u_adm", memberSpec())
	before := e.count(t, `SELECT count(*) FROM users`)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _, err := e.accept(tok)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	ok, used := 0, 0
	for err := range results {
		switch {
		case err == nil:
			ok++
		case errors.Is(err, ErrInviteUsedUp):
			used++
		default:
			t.Errorf("unexpected error: %v", err)
		}
	}
	if ok != 1 || used != 7 {
		t.Fatalf("ok = %d, used up = %d; want 1 and 7", ok, used)
	}
	if after := e.count(t, `SELECT count(*) FROM users`); after != before+1 {
		t.Errorf("users %d -> %d", before, after)
	}
	if n := e.count(t, `SELECT use_count FROM invites`); n != 1 {
		t.Errorf("use_count = %d", n)
	}
}

func TestInviteExpiryBoundary(t *testing.T) {
	e := newInviteEnv(t)
	exp := e.f.t0.UnixMilli() + 10_000
	s := memberSpec()
	s.ExpiresAt = exp
	_, tok1 := e.mustCreate(t, "u_adm", s)
	_, tok2 := e.mustCreate(t, "u_adm", s)
	_, tok3 := e.mustCreate(t, "u_adm", s)
	e.f.t0 = time.UnixMilli(exp - 1)
	if inv, err := e.inv.Lookup(context.Background(), tok1); err != nil || inv.State(exp-1) != InvitePending {
		t.Fatalf("one ms before: %v %v", err, inv.State(exp-1))
	}
	if _, _, err := e.accept(tok1); err != nil {
		t.Fatalf("accept one ms before: %v", err)
	}
	for _, at := range []int64{exp, exp + 1} {
		e.f.t0 = time.UnixMilli(at)
		tok := tok2
		if at != exp {
			tok = tok3
		}
		if _, _, err := e.accept(tok); !errors.Is(err, ErrInviteExpired) {
			t.Errorf("accept at %d: %v, want ErrInviteExpired", at, err)
		}
		inv, err := e.inv.Lookup(context.Background(), tok)
		if err != nil || inv.State(at) != InviteExpired {
			t.Errorf("lookup at %d: %v state %v", at, err, inv.State(at))
		}
	}
	// "until I stop it" lives on
	e.f.t0 = time.UnixMilli(1_000_000_000)
	_, tok := e.mustCreate(t, "u_adm", memberSpec())
	e.f.t0 = e.f.t0.Add(10 * 365 * 24 * time.Hour)
	if _, _, err := e.accept(tok); err != nil {
		t.Errorf("no end date, far future: %v", err)
	}
}

func TestInviteInviterDemoted(t *testing.T) {
	for _, change := range []string{`UPDATE users SET role = 'member' WHERE id = 'u_adm'`, `UPDATE users SET disabled = 1 WHERE id = 'u_adm'`, `DELETE FROM sessions`} {
		e := newInviteEnv(t)
		_, tok := e.mustCreate(t, "u_adm", memberSpec())
		users0 := e.count(t, `SELECT count(*) FROM users`)
		e.f.exec(t, change)
		_, _, err := e.accept(tok)
		if change == `DELETE FROM sessions` {
			if err != nil {
				t.Errorf("control: %v", err)
			}
			continue
		}
		if !errors.Is(err, ErrInviteRefused) {
			t.Errorf("%s: err = %v, want ErrInviteRefused", change, err)
		}
		if n := e.count(t, `SELECT use_count FROM invites`); n != 0 {
			t.Errorf("use_count = %d after a refusal", n)
		}
		if n := e.count(t, `SELECT count(*) FROM users`); n != users0 {
			t.Errorf("a refused accept created a user")
		}
		if _, err := e.inv.Lookup(context.Background(), tok); !errors.Is(err, ErrInviteRefused) {
			t.Errorf("lookup after %s: %v", change, err)
		}
		// rotating or cancelling it as the demoted inviter is refused as well
		var id string
		_ = e.f.st.Reader().QueryRow(`SELECT id FROM invites`).Scan(&id)
		err = e.write(func(tx *sql.Tx) error {
			_, err := e.inv.Rotate(context.Background(), tx, e.f.users["u_adm"], id)
			return err
		})
		if !errors.Is(err, ErrInviteRefused) {
			t.Errorf("rotate by the demoted: %v", err)
		}
		err = e.write(func(tx *sql.Tx) error { return e.inv.Revoke(context.Background(), tx, e.f.users["u_adm"], id) })
		if !errors.Is(err, ErrInviteRefused) {
			t.Errorf("revoke by the demoted: %v", err)
		}
	}
}

func TestInviteGrantsRechecked(t *testing.T) {
	e := newInviteEnv(t)
	spec := memberSpec(
		InviteGrant{Scope: GrantBook, Target: "bk_a1"},
		InviteGrant{Scope: GrantBook, Target: "bk_a2", Download: true},
		InviteGrant{Scope: GrantShelf, Target: "sh_adm"},
		InviteGrant{Scope: GrantLibrary, Target: "lib_b"})
	_, tok := e.mustCreate(t, "u_own", spec)
	e.f.exec(t, `DELETE FROM books WHERE id = 'bk_a1'`)
	e.f.exec(t, `UPDATE users SET role = 'member' WHERE id = 'u_adm'`) // the shelf's owner is demoted
	u, _, err := e.accept(tok)
	if err != nil {
		t.Fatal(err)
	}
	gs, _ := NewGrants(e.f.st).ForUser(context.Background(), u.ID)
	got := map[string]bool{}
	for _, g := range gs {
		got[string(g.Scope)+":"+g.TargetID] = g.CanDownload
	}
	if len(got) != 2 || !got["book:bk_a2"] || got["library:lib_b"] {
		t.Fatalf("grants = %v", got)
	}
	var detail string
	_ = e.f.st.Reader().QueryRow(`SELECT detail_json FROM audit_log WHERE action = 'invite.accept'`).Scan(&detail)
	if !strings.Contains(detail, `"skipped":2`) || !strings.Contains(detail, `"grants":2`) {
		t.Errorf("audit detail = %s", detail)
	}

	// everything gone: refused, nothing written
	e2 := newInviteEnv(t)
	_, tok2 := e2.mustCreate(t, "u_adm", memberSpec(InviteGrant{Scope: GrantBook, Target: "bk_a1"}))
	e2.f.exec(t, `DELETE FROM books WHERE id = 'bk_a1'`)
	users0 := e2.count(t, `SELECT count(*) FROM users`)
	if _, _, err := e2.accept(tok2); !errors.Is(err, ErrInviteNoAccess) {
		t.Fatalf("all gone: %v", err)
	}
	if e2.count(t, `SELECT count(*) FROM users`) != users0 || e2.count(t, `SELECT use_count FROM invites`) != 0 {
		t.Error("a refused accept wrote something")
	}
}

func TestInviteRotateRevoke(t *testing.T) {
	e := newInviteEnv(t)
	ctx := context.Background()
	inv, old := e.mustCreate(t, "u_adm", memberSpec())
	var fresh string
	if err := e.write(func(tx *sql.Tx) error {
		var err error
		fresh, err = e.inv.Rotate(ctx, tx, e.f.users["u_adm"], inv.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if fresh == old {
		t.Fatal("rotate returned the same token")
	}
	if _, err := e.inv.Lookup(ctx, old); !errors.Is(err, ErrInviteUnknown) {
		t.Errorf("old token: %v", err)
	}
	if _, _, err := e.accept(old); !errors.Is(err, ErrInviteUnknown) {
		t.Errorf("accept old token: %v", err)
	}
	if _, err := e.inv.Lookup(ctx, fresh); err != nil {
		t.Errorf("new token: %v", err)
	}
	// an admin may not rotate an admin invite
	ainv, _ := e.mustCreate(t, "u_own", InviteSpec{Name: "Ana", Role: users.RoleAdmin})
	err := e.write(func(tx *sql.Tx) error { _, err := e.inv.Rotate(ctx, tx, e.f.users["u_adm"], ainv.ID); return err })
	if !errors.Is(err, ErrInviteRefused) {
		t.Errorf("admin rotates admin invite: %v", err)
	}
	// revoke twice: one audit row, lookups look like unknown
	for i := 0; i < 2; i++ {
		if err := e.write(func(tx *sql.Tx) error { return e.inv.Revoke(ctx, tx, e.f.users["u_adm"], inv.ID) }); err != nil {
			t.Fatal(err)
		}
	}
	if n := e.count(t, `SELECT count(*) FROM audit_log WHERE action = 'invite.revoke'`); n != 1 {
		t.Errorf("revoke audit rows = %d", n)
	}
	if _, err := e.inv.Lookup(ctx, fresh); !errors.Is(err, ErrInviteUnknown) {
		t.Errorf("revoked lookup: %v", err)
	}
	err = e.write(func(tx *sql.Tx) error { _, err := e.inv.Rotate(ctx, tx, e.f.users["u_adm"], inv.ID); return err })
	if !errors.Is(err, ErrInviteUnknown) {
		t.Errorf("rotate revoked: %v", err)
	}
	// a used invite cannot be rotated
	uinv, utok := e.mustCreate(t, "u_adm", memberSpec())
	if _, _, err := e.accept(utok); err != nil {
		t.Fatal(err)
	}
	err = e.write(func(tx *sql.Tx) error { _, err := e.inv.Rotate(ctx, tx, e.f.users["u_adm"], uinv.ID); return err })
	if !errors.Is(err, ErrInviteUnknown) {
		t.Errorf("rotate used: %v", err)
	}
	if _, err := e.inv.Lookup(ctx, "short"); !errors.Is(err, ErrInviteUnknown) {
		t.Errorf("malformed lookup: %v", err)
	}
	err = e.write(func(tx *sql.Tx) error { return e.inv.Revoke(ctx, tx, e.f.users["u_adm"], "inv_nope") })
	if !errors.Is(err, ErrInviteUnknown) {
		t.Errorf("revoke unknown: %v", err)
	}
}

func TestInvitePendingAndWaiting(t *testing.T) {
	e := newInviteEnv(t)
	ctx := context.Background()
	a, _ := e.mustCreate(t, "u_own", InviteSpec{Name: "Ana", Role: users.RoleAdmin})
	e.f.t0 = e.f.t0.Add(time.Second)
	m, _ := e.mustCreate(t, "u_adm", memberSpec())
	e.f.t0 = e.f.t0.Add(time.Second)
	g, gtok := e.mustCreate(t, "u_adm", InviteSpec{Name: "Gus", Role: users.RoleGuest, Grants: []InviteGrant{{Scope: GrantAll}}})
	now := e.f.t0.UnixMilli()
	list, err := e.inv.Pending(ctx, now, ManageableInviteRoles(e.f.users["u_own"]))
	if err != nil || len(list) != 3 || list[0].ID != a.ID || list[1].ID != m.ID || list[2].ID != g.ID {
		t.Fatalf("owner list = %v %v", ids(list), err)
	}
	list, _ = e.inv.Pending(ctx, now, ManageableInviteRoles(e.f.users["u_adm"]))
	if len(list) != 2 || list[0].ID != m.ID {
		t.Fatalf("admin list = %v", ids(list))
	}
	if list, _ := e.inv.Pending(ctx, now, ManageableInviteRoles(e.f.users["u_lib"])); len(list) != 0 {
		t.Errorf("member list = %v", ids(list))
	}
	if n, _ := WaitingInvites(ctx, e.f.st, now, ManageableInviteRoles(e.f.users["u_own"])); n != 3 {
		t.Errorf("owner waiting = %d", n)
	}
	if n, _ := WaitingInvites(ctx, e.f.st, now, ManageableInviteRoles(e.f.users["u_adm"])); n != 2 {
		t.Errorf("admin waiting = %d", n)
	}
	if n, _ := WaitingInvites(ctx, e.f.st, now, nil); n != 0 {
		t.Errorf("nil roles waiting = %d", n)
	}
	if _, _, err := e.accept(gtok); err != nil {
		t.Fatal(err)
	}
	if n, _ := WaitingInvites(ctx, e.f.st, now, ManageableInviteRoles(e.f.users["u_own"])); n != 2 {
		t.Errorf("after accept waiting = %d", n)
	}
	if ManageableInviteRoles(users.User{Role: users.RoleOwner, Disabled: true}) != nil {
		t.Error("a disabled owner may invite")
	}
}

func ids(l []Invite) []string {
	var out []string
	for _, i := range l {
		out = append(out, i.ID)
	}
	return out
}

func TestInviteFeatured(t *testing.T) {
	e := newInviteEnv(t)
	ctx := context.Background()
	e.f.exec(t, `UPDATE books SET added_at = 10 WHERE id = 'bk_a1'`)
	e.f.exec(t, `UPDATE books SET added_at = 20 WHERE id = 'bk_a2'`)
	e.f.exec(t, `UPDATE books SET added_at = 30 WHERE id = 'bk_b1'`)
	e.f.exec(t, `UPDATE books SET added_at = 40 WHERE id = 'bk_b2'`)
	feat := func(gs ...InviteGrant) (string, int) {
		inv := Invite{Grants: gs}
		id, others, err := e.inv.Featured(ctx, inv)
		if err != nil {
			t.Fatal(err)
		}
		return id, others
	}
	for _, tc := range []struct {
		name   string
		grants []InviteGrant
		book   string
		others int
	}{
		{"book", []InviteGrant{{Scope: GrantBook, Target: "bk_b1"}}, "bk_b1", 0},
		{"library newest", []InviteGrant{{Scope: GrantLibrary, Target: "lib_a"}}, "bk_a2", 1},
		{"all newest", []InviteGrant{{Scope: GrantAll}}, "bk_b2", 3},
		{"shelf", []InviteGrant{{Scope: GrantShelf, Target: "sh_adm"}}, "bk_b2", 0},
		{"first grant decides, overlap counted once", []InviteGrant{{Scope: GrantBook, Target: "bk_a1"}, {Scope: GrantLibrary, Target: "lib_a"}}, "bk_a1", 1},
		{"none", nil, "", 0},
	} {
		if id, others := feat(tc.grants...); id != tc.book || others != tc.others {
			t.Errorf("%s: got %q +%d, want %q +%d", tc.name, id, others, tc.book, tc.others)
		}
	}
	// refused and missing books never feature or count
	e.f.exec(t, `UPDATE books SET refused_reason = 'drm' WHERE id = 'bk_a2'`)
	e.f.exec(t, `UPDATE books SET missing_since = 5 WHERE id = 'bk_a1'`)
	if id, others := feat(InviteGrant{Scope: GrantLibrary, Target: "lib_a"}); id != "" || others != 0 {
		t.Errorf("library with nothing usable: %q +%d", id, others)
	}
	if id, others := feat(InviteGrant{Scope: GrantLibrary, Target: "lib_a"}, InviteGrant{Scope: GrantBook, Target: "bk_b1"}); id != "bk_b1" || others != 0 {
		t.Errorf("falls through to the next grant: %q +%d", id, others)
	}
	// a shelf whose owner stopped being an admin features nothing
	e.f.exec(t, `UPDATE users SET role = 'member' WHERE id = 'u_adm'`)
	if id, _ := feat(InviteGrant{Scope: GrantShelf, Target: "sh_adm"}); id != "" {
		t.Errorf("member-owned shelf features %q", id)
	}
}

// TestInviteContributor: an owner or an admin invites a contributor (member plus the flag), accepting makes
// an account with the flag, the flag with any other role is refused, and a member cannot invite one.
func TestInviteContributor(t *testing.T) {
	e := newInviteEnv(t)
	for _, by := range []string{"u_own", "u_adm"} {
		s := memberSpec(InviteGrant{Scope: GrantLibrary, Target: "lib_a"})
		s.AddsBooks = true
		inv, tok := e.mustCreate(t, by, s)
		if !inv.AddsBooks || inv.Role != users.RoleMember {
			t.Fatalf("%s: invite %+v", by, inv)
		}
		u, _, err := e.accept(tok)
		if err != nil || !u.AddsBooks || !u.Contributor() || u.Kind() != users.KindContributor {
			t.Fatalf("%s: accept %v %+v", by, err, u)
		}
		var flag int
		if err := e.f.st.Reader().QueryRow(`SELECT adds_books FROM users WHERE id = ?`, u.ID).Scan(&flag); err != nil || flag != 1 {
			t.Errorf("%s: stored flag = %d, %v", by, flag, err)
		}
	}
	// an invite without the flag stays a plain member
	_, tok := e.mustCreate(t, "u_adm", memberSpec())
	if u, _, err := e.accept(tok); err != nil || u.AddsBooks {
		t.Errorf("plain member invite: %v %+v", err, u)
	}
	for _, role := range []users.Role{users.RoleGuest, users.RoleAdmin} {
		if _, _, err := e.create("u_own", InviteSpec{Name: "X", Role: role, AddsBooks: true}); !errors.Is(err, ErrInviteRefused) {
			t.Errorf("flag with %s: %v", role, err)
		}
	}
	for _, by := range []string{"u_lib", "u_con", "u_g"} {
		s := memberSpec()
		s.AddsBooks = true
		if _, _, err := e.create(by, s); !errors.Is(err, ErrInviteRefused) {
			t.Errorf("%s invited a contributor: %v", by, err)
		}
	}
	want := map[string][]users.Kind{
		"u_own": {users.KindAdmin, users.KindContributor, users.KindMember, users.KindGuest},
		"u_adm": {users.KindContributor, users.KindMember, users.KindGuest},
		"u_con": nil,
		"u_lib": nil,
		"u_g":   nil,
		"u_dis": nil,
	}
	for id, w := range want {
		if got := ManageableInviteKinds(e.f.user(id)); strings.Join(kindStrings(got), ",") != strings.Join(kindStrings(w), ",") {
			t.Errorf("%s kinds = %v, want %v", id, got, w)
		}
	}
}

func kindStrings(ks []users.Kind) []string {
	out := make([]string, len(ks))
	for i, k := range ks {
		out[i] = string(k)
	}
	return out
}
