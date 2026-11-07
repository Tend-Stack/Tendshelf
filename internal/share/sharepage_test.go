package share

import (
	"context"
	"reflect"
	"sort"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

func sortedKeys(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestCoveringBook(t *testing.T) {
	f := openFixture(t)
	g := NewGrants(f.st)
	now := f.t0.UnixMilli()
	for _, c := range []struct {
		book string
		want []string
	}{
		{"bk_a1", []string{"u_all", "u_con", "u_dis", "u_lib", "u_stale"}}, // a member's raw shelf holds bk_a1 but covers nothing
		{"bk_b1", []string{"u_all", "u_g"}},            // u_exp's grant has ended
		{"bk_b2", []string{"u_all", "u_shelf"}},
	} {
		got, err := g.CoveringBook(context.Background(), c.book, now)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(sortedKeys(got), c.want) {
			t.Errorf("%s: %v, want %v", c.book, sortedKeys(got), c.want)
		}
	}
	f.exec(t, `UPDATE users SET disabled = 1 WHERE id = 'u_adm'`)
	got, _ := g.CoveringBook(context.Background(), "bk_b2", now)
	if !reflect.DeepEqual(sortedKeys(got), []string{"u_all"}) {
		t.Errorf("disabled shelf owner still covers: %v", sortedKeys(got))
	}
	if got, err := g.CoveringBook(context.Background(), "bk_none", now); err != nil || len(got) != 0 {
		t.Errorf("unknown book: %v %v", got, err)
	}
}

func TestTargetNames(t *testing.T) {
	f := openFixture(t)
	got, err := NewGrants(f.st).TargetNames(context.Background(), []InviteGrant{
		{Scope: GrantAll}, {Scope: GrantLibrary, Target: "lib_a"}, {Scope: GrantShelf, Target: "sh_adm"},
		{Scope: GrantBook, Target: "bk_b1"}, {Scope: GrantBook, Target: "bk_missing"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"library:lib_a": "A", "shelf:sh_adm": "Admin shelf", "book:bk_b1": "bk_b1"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v, want %v", got, want)
	}
	if got, err := NewGrants(f.st).TargetNames(context.Background(), nil); err != nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
}

func TestShelfTargetsOwnerID(t *testing.T) {
	f := openFixture(t)
	got, err := NewGrants(f.st).ShelfTargets(context.Background())
	if err != nil || len(got) != 1 || got[0].OwnerID != "u_adm" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestPendingCovering(t *testing.T) {
	e := newInviteEnv(t)
	ctx := context.Background()
	mk := func(name string, role users.Role, gs ...InviteGrant) string {
		inv, _ := e.mustCreate(t, "u_own", InviteSpec{Name: name, Role: role, Grants: gs})
		return inv.ID
	}
	adm := mk("Adm", users.RoleAdmin)
	all := mk("All", users.RoleGuest, InviteGrant{Scope: GrantAll})
	lib := mk("Lib", users.RoleGuest, InviteGrant{Scope: GrantLibrary, Target: "lib_b"})
	shelf := mk("Shelf", users.RoleGuest, InviteGrant{Scope: GrantShelf, Target: "sh_adm"})
	book := mk("Book", users.RoleGuest, InviteGrant{Scope: GrantBook, Target: "bk_b2"})
	other := mk("Other", users.RoleGuest, InviteGrant{Scope: GrantBook, Target: "bk_a1"})
	otherLib := mk("OtherLib", users.RoleGuest, InviteGrant{Scope: GrantLibrary, Target: "lib_a"})
	_, _, _ = adm, other, otherLib
	roles := ManageableInviteRoles(e.f.users["u_own"])
	list, err := e.inv.PendingCovering(ctx, e.f.t0.UnixMilli(), roles, "bk_b2")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, i := range list {
		got[i.ID] = true
	}
	want := map[string]bool{all: true, lib: true, shelf: true, book: true}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%v, want %v", ids(list), want)
	}
	if list, _ := e.inv.PendingCovering(ctx, e.f.t0.UnixMilli(), roles, "bk_none"); len(list) != 0 {
		t.Errorf("unknown book: %v", ids(list))
	}
	if list, _ := e.inv.PendingCovering(ctx, e.f.t0.UnixMilli(), nil, "bk_b2"); len(list) != 0 {
		t.Errorf("no roles: %v", ids(list))
	}
}
