package users

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// seedPeople inserts an owner, an admin, a member and a guest with raw rows.
func seedPeople(t *testing.T, r *Repo) {
	t.Helper()
	for _, u := range [][2]string{{"o", "owner"}, {"a", "admin"}, {"m", "member"}, {"g", "guest"}} {
		if _, err := r.st.Writer().Exec(`INSERT INTO users(id, username, role, disabled, created_at) VALUES (?, ?, ?, 0, 1)`, u[0], u[0], u[1]); err != nil {
			t.Fatal(err)
		}
	}
}

func inTx(t *testing.T, r *Repo, fn func(tx *sql.Tx) error) error {
	t.Helper()
	return r.st.WriteTx(context.Background(), fn)
}

func auditCount(t *testing.T, r *Repo, action string) int {
	t.Helper()
	var n int
	if err := r.st.Reader().QueryRow(`SELECT count(*) FROM audit_log WHERE action = ?`, action).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestSetRole(t *testing.T) {
	cases := []struct {
		name, by, id string
		to           Role
		want         error
		from         Role
		audit        int
	}{
		{"member to admin", "o", "m", RoleAdmin, nil, RoleMember, 1},
		{"guest to member", "o", "g", RoleMember, nil, RoleGuest, 1},
		{"unchanged writes nothing", "o", "m", RoleMember, nil, RoleMember, 0},
		{"owner never demoted", "a", "o", RoleMember, ErrOwnerFixed, "", 0},
		{"nobody becomes owner", "o", "m", RoleOwner, ErrOwnerFixed, "", 0},
		{"self", "o", "o", RoleAdmin, ErrSelf, "", 0},
		{"unknown role", "o", "m", Role("root"), ErrRole, "", 0},
		{"unknown person", "o", "zz", RoleGuest, ErrNotFound, "", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRepo(t, Options{})
			seedPeople(t, r)
			var from Role
			err := inTx(t, r, func(tx *sql.Tx) error {
				var e error
				from, e = r.SetRole(context.Background(), tx, c.by, c.id, c.to)
				return e
			})
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if err == nil && from != c.from {
				t.Fatalf("from = %q, want %q", from, c.from)
			}
			if n := auditCount(t, r, "user.role"); n != c.audit {
				t.Fatalf("audit rows = %d, want %d", n, c.audit)
			}
			if err == nil {
				u, _ := r.ByID(context.Background(), c.id)
				if u.Role != c.to {
					t.Fatalf("role = %q", u.Role)
				}
			}
		})
	}
}

func TestSetDisabled(t *testing.T) {
	cases := []struct {
		name, by, id string
		off          bool
		want         error
		audit        string
		n            int
	}{
		{"turn off", "o", "m", true, nil, "user.disable", 1},
		{"turn on a person already on", "o", "m", false, nil, "user.enable", 0},
		{"owner stays on", "a", "o", true, ErrOwnerFixed, "user.disable", 0},
		{"self", "o", "o", true, ErrSelf, "user.disable", 0},
		{"unknown", "o", "zz", true, ErrNotFound, "user.disable", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := newRepo(t, Options{})
			seedPeople(t, r)
			err := inTx(t, r, func(tx *sql.Tx) error { return r.SetDisabled(context.Background(), tx, c.by, c.id, c.off) })
			if !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if n := auditCount(t, r, c.audit); n != c.n {
				t.Fatalf("audit rows = %d, want %d", n, c.n)
			}
		})
	}
	t.Run("turn back on", func(t *testing.T) {
		r := newRepo(t, Options{})
		seedPeople(t, r)
		for _, off := range []bool{true, false} {
			if err := inTx(t, r, func(tx *sql.Tx) error { return r.SetDisabled(context.Background(), tx, "o", "m", off) }); err != nil {
				t.Fatal(err)
			}
		}
		u, _ := r.ByID(context.Background(), "m")
		if u.Disabled || auditCount(t, r, "user.enable") != 1 {
			t.Fatalf("disabled=%v enable rows=%d", u.Disabled, auditCount(t, r, "user.enable"))
		}
	})
}

func TestSetKind(t *testing.T) {
	ctx := context.Background()
	set := func(t *testing.T, r *Repo, by, id string, to Kind) (Kind, error) {
		var from Kind
		err := inTx(t, r, func(tx *sql.Tx) error {
			var e error
			from, e = r.SetKind(ctx, tx, by, id, to)
			return e
		})
		return from, err
	}
	t.Run("the whole round", func(t *testing.T) {
		r := newRepo(t, Options{})
		seedPeople(t, r)
		steps := []struct {
			to, from Kind
			role     Role
			adds     bool
		}{
			{KindContributor, KindMember, RoleMember, true},
			{KindContributor, KindContributor, RoleMember, true}, // unchanged: no audit row
			{KindMember, KindContributor, RoleMember, false},
			{KindContributor, KindMember, RoleMember, true},
			{KindAdmin, KindContributor, RoleAdmin, false},
			{KindContributor, KindAdmin, RoleMember, true},
			{KindGuest, KindContributor, RoleGuest, false},
		}
		for _, s := range steps {
			from, err := set(t, r, "o", "m", s.to)
			if err != nil || from != s.from {
				t.Fatalf("to %s: from %q, err %v, want from %q", s.to, from, err, s.from)
			}
			u, _ := r.ByID(ctx, "m")
			if u.Role != s.role || u.AddsBooks != s.adds {
				t.Fatalf("to %s: role %q adds %v", s.to, u.Role, u.AddsBooks)
			}
		}
		if n := auditCount(t, r, "user.role"); n != 6 {
			t.Fatalf("audit rows = %d, want 6", n)
		}
	})
	refused := []struct {
		name, by, id string
		to           Kind
		want         error
	}{
		{"owner fixed", "a", "o", KindMember, ErrOwnerFixed},
		{"nobody becomes owner", "o", "m", KindOwner, ErrOwnerFixed},
		{"self", "o", "o", KindAdmin, ErrSelf},
		{"unknown kind", "o", "m", Kind("root"), ErrRole},
		{"unknown person", "o", "zz", KindContributor, ErrNotFound},
	}
	for _, c := range refused {
		t.Run(c.name, func(t *testing.T) {
			r := newRepo(t, Options{})
			seedPeople(t, r)
			if _, err := set(t, r, c.by, c.id, c.to); !errors.Is(err, c.want) {
				t.Fatalf("err = %v, want %v", err, c.want)
			}
			if n := auditCount(t, r, "user.role"); n != 0 {
				t.Fatalf("audit rows = %d", n)
			}
		})
	}
	t.Run("SetRole keeps the flag only for a member", func(t *testing.T) {
		r := newRepo(t, Options{})
		seedPeople(t, r)
		set(t, r, "o", "m", KindContributor)
		if err := inTx(t, r, func(tx *sql.Tx) error { _, e := r.SetRole(ctx, tx, "o", "m", RoleGuest); return e }); err != nil {
			t.Fatal(err)
		}
		if u, _ := r.ByID(ctx, "m"); u.AddsBooks || u.Kind() != KindGuest {
			t.Fatalf("after SetRole guest: %+v", u)
		}
	})
	t.Run("Contributor needs member, flag and enabled", func(t *testing.T) {
		for _, u := range []struct {
			u    User
			want bool
		}{
			{User{Role: RoleMember, AddsBooks: true}, true},
			{User{Role: RoleMember, AddsBooks: true, Disabled: true}, false},
			{User{Role: RoleMember}, false},
			{User{Role: RoleAdmin, AddsBooks: true}, false},
		} {
			if got := u.u.Contributor(); got != u.want {
				t.Errorf("%+v = %v", u.u, got)
			}
		}
	})
}
