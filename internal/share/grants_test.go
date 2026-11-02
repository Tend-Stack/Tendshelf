package share

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

// newGrants returns the repository on the fixture with a fixed clock one second after t0.
func (f *fixture) newGrants() (*Grants, *time.Time) {
	now := f.t0.Add(time.Second)
	g := NewGrants(f.st)
	g.now = func() time.Time { return now }
	return g, &now
}

func (f *fixture) put(t *testing.T, g *Grants, by string, s GrantSpec) (Grant, error) {
	t.Helper()
	var out Grant
	err := f.st.WriteTx(context.Background(), func(tx *sql.Tx) (err error) {
		out, err = g.Put(context.Background(), tx, f.user(by), s)
		return err
	})
	return out, err
}

func TestGrantsPut(t *testing.T) {
	f := openFixture(t)
	g, now := f.newGrants()
	ctx := context.Background()
	ms := now.UnixMilli()

	first, err := f.put(t, g, "u_own", GrantSpec{UserID: "u_none", Scope: GrantLibrary, TargetID: "lib_b", CanDownload: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(first.ID, "gr_") || first.UserID != "u_none" || first.Scope != GrantLibrary || first.TargetID != "lib_b" ||
		!first.CanDownload || first.CanRead || first.ExpiresAt != 0 || first.CreatedBy != "u_own" || first.CreatedAt != ms || first.UpdatedAt != ms {
		t.Errorf("inserted grant = %+v", first)
	}
	if !f.c.Can(ctx, f.user("u_none"), DownloadBook, Target{Kind: TargetBook, ID: "bk_b1"}) {
		t.Error("the new grant does not open the book")
	}

	// Putting the same target again updates in place.
	*now = now.Add(time.Minute)
	exp := now.Add(time.Hour).UnixMilli()
	again, err := f.put(t, g, "u_adm", GrantSpec{UserID: "u_none", Scope: GrantLibrary, TargetID: "lib_b", CanRead: true, ExpiresAt: exp})
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != first.ID || again.CreatedAt != first.CreatedAt || again.CreatedBy != "u_own" || again.UpdatedAt != now.UnixMilli() ||
		again.CanDownload || !again.CanRead || again.ExpiresAt != exp {
		t.Errorf("updated grant = %+v (first %+v)", again, first)
	}
	if list, _ := g.ForUser(ctx, "u_none"); len(list) != 1 {
		t.Errorf("%d grants after an upsert, want 1", len(list))
	}

	// One all-scope grant per user, whatever is passed again.
	for i := 0; i < 2; i++ {
		if _, err := f.put(t, g, "u_own", GrantSpec{UserID: "u_none", Scope: GrantAll, CanRead: i == 1}); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := f.st.Reader().QueryRow(`SELECT count(*) FROM grants WHERE user_id = 'u_none' AND scope = 'all'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("all-scope grants = %d, %v", n, err)
	}

	// Every scope has a good target; a shelf of an admin works.
	for _, s := range []GrantSpec{
		{UserID: "u_g", Scope: GrantShelf, TargetID: "sh_adm"},
		{UserID: "u_g", Scope: GrantBook, TargetID: "bk_a1"},
	} {
		if _, err := f.put(t, g, "u_own", s); err != nil {
			t.Errorf("%+v: %v", s, err)
		}
	}

	// Each error, and no row for a refused spec.
	for _, c := range []struct {
		name string
		spec GrantSpec
		want error
	}{
		{"unknown scope", GrantSpec{UserID: "u_none", Scope: "galaxy", TargetID: "x"}, ErrGrantScope},
		{"empty scope", GrantSpec{UserID: "u_none"}, ErrGrantScope},
		{"all with a target", GrantSpec{UserID: "u_none", Scope: GrantAll, TargetID: "lib_a"}, ErrGrantScope},
		{"library without a target", GrantSpec{UserID: "u_none", Scope: GrantLibrary}, ErrGrantScope},
		{"grantee missing", GrantSpec{UserID: "nobody", Scope: GrantAll}, ErrGrantNotFound},
		{"grantee owner", GrantSpec{UserID: "u_own", Scope: GrantAll}, ErrGrantRole},
		{"grantee admin", GrantSpec{UserID: "u_adm", Scope: GrantAll}, ErrGrantRole},
		{"library missing", GrantSpec{UserID: "u_none", Scope: GrantLibrary, TargetID: "lib_x"}, ErrGrantTarget},
		{"book missing", GrantSpec{UserID: "u_none", Scope: GrantBook, TargetID: "bk_x"}, ErrGrantTarget},
		{"shelf missing", GrantSpec{UserID: "u_none", Scope: GrantShelf, TargetID: "sh_x"}, ErrGrantTarget},
		{"member's shelf", GrantSpec{UserID: "u_exp", Scope: GrantShelf, TargetID: "sh_mem"}, ErrGrantTarget},
		{"expiry now", GrantSpec{UserID: "u_none", Scope: GrantBook, TargetID: "bk_b1", ExpiresAt: now.UnixMilli()}, ErrGrantExpiry},
		{"expiry past", GrantSpec{UserID: "u_none", Scope: GrantBook, TargetID: "bk_b1", ExpiresAt: 5}, ErrGrantExpiry},
	} {
		if _, err := f.put(t, g, "u_own", c.spec); !errors.Is(err, c.want) {
			t.Errorf("%s: err = %v, want %v", c.name, err, c.want)
		}
	}
	if err := f.st.Reader().QueryRow(`SELECT count(*) FROM grants WHERE user_id = 'u_none'`).Scan(&n); err != nil || n != 2 {
		t.Errorf("u_none has %d grants after the refused specs, want 2 (library and all)", n)
	}

	// Audit rows: one per successful put, with the grant as target and no token anywhere.
	rows, err := f.st.Reader().Query(`SELECT user_id, target, detail_json FROM audit_log WHERE action = 'grant.put' ORDER BY at, id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var user, target, detail string
		if err := rows.Scan(&user, &target, &detail); err != nil {
			t.Fatal(err)
		}
		count++
		if !strings.HasPrefix(target, "grant:gr_") || !strings.Contains(detail, `"scope"`) || !strings.Contains(detail, `"can_download"`) {
			t.Errorf("audit row = %s %s %s", user, target, detail)
		}
	}
	if count != 6 {
		t.Errorf("%d put audit rows, want 6", count)
	}
}

func TestGrantsRevoke(t *testing.T) {
	f := openFixture(t)
	g, _ := f.newGrants()
	ctx := context.Background()
	u := f.user("u_lib")
	book := Target{Kind: TargetBook, ID: "bk_a1"}
	if !f.c.Can(ctx, u, ViewBook, book) {
		t.Fatal("fixture grant does not open the book")
	}
	revoke := func(id string) error {
		return f.st.WriteTx(ctx, func(tx *sql.Tx) error { return g.Revoke(ctx, tx, f.user("u_own"), id) })
	}
	if err := revoke("gr_u_lib"); err != nil {
		t.Fatal(err)
	}
	if f.c.Can(ctx, u, ViewBook, book) || len(f.visibleBooks(t, u)) != 0 {
		t.Error("access remained after revoke")
	}
	if _, err := g.Get(ctx, "gr_u_lib"); !errors.Is(err, ErrGrantNotFound) {
		t.Errorf("Get after revoke: %v", err)
	}
	if err := revoke("gr_u_lib"); !errors.Is(err, ErrGrantNotFound) {
		t.Errorf("second revoke: %v", err)
	}
	var detail string
	if err := f.st.Reader().QueryRow(`SELECT detail_json FROM audit_log WHERE action = 'grant.revoke' AND target = 'grant:gr_u_lib'`).Scan(&detail); err != nil {
		t.Fatalf("no revoke audit row: %v", err)
	}
	if !strings.Contains(detail, `"user":"u_lib"`) || !strings.Contains(detail, `"target":"lib_a"`) {
		t.Errorf("revoke detail = %s", detail)
	}
}

func TestGrantsGet(t *testing.T) {
	f := openFixture(t)
	g, _ := f.newGrants()
	got, err := g.Get(context.Background(), "gr_u_g")
	if err != nil {
		t.Fatal(err)
	}
	if got.UserID != "u_g" || got.Scope != GrantBook || got.TargetID != "bk_b1" || !got.CanDownload || !got.CanRead || got.ExpiresAt == 0 {
		t.Errorf("Get = %+v", got)
	}
	if !got.Active(f.t0.UnixMilli()) || got.Active(got.ExpiresAt) {
		t.Error("Active disagrees with the strict end")
	}
	if !(Grant{}).Active(1 << 60) {
		t.Error("a grant without an end must be active")
	}
}

func TestGrantsForUser(t *testing.T) {
	f := openFixture(t)
	g, now := f.newGrants()
	ctx := context.Background()
	for i, s := range []GrantSpec{
		{UserID: "u_exp", Scope: GrantLibrary, TargetID: "lib_b"},
		{UserID: "u_exp", Scope: GrantAll},
	} {
		*now = now.Add(time.Duration(i+1) * time.Second)
		if _, err := f.put(t, g, "u_own", s); err != nil {
			t.Fatal(err)
		}
	}
	list, err := g.ForUser(ctx, "u_exp")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("%d grants, want 3 (the expired one included)", len(list))
	}
	if list[0].ID != "gr_u_exp" || list[0].Scope != GrantBook || list[1].Scope != GrantLibrary || list[2].Scope != GrantAll {
		t.Errorf("order = %+v", list)
	}
	if list[0].Active(now.UnixMilli()) {
		t.Error("the expired grant reads as active")
	}
	if none, err := g.ForUser(ctx, "u_none"); err != nil || len(none) != 0 {
		t.Errorf("u_none: %v, %v", none, err)
	}
}

func TestGrantCascades(t *testing.T) {
	for _, c := range []struct {
		name, del, grantUser string
	}{
		{"user", `DELETE FROM users WHERE id = 'u_lib'`, "u_lib"},
		{"library", `DELETE FROM libraries WHERE id = 'lib_a'`, "u_lib"},
		{"shelf", `DELETE FROM shelves WHERE id = 'sh_adm'`, "u_shelf"},
		{"book", `DELETE FROM books WHERE id = 'bk_b1'`, "u_g"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := openFixture(t)
			f.exec(t, c.del)
			var n int
			if err := f.st.Reader().QueryRow(`SELECT count(*) FROM grants WHERE user_id = ?`, c.grantUser).Scan(&n); err != nil || n != 0 {
				t.Errorf("%d grants left, %v", n, err)
			}
		})
	}
	t.Run("creator deleted keeps the grant", func(t *testing.T) {
		f := openFixture(t)
		g, _ := f.newGrants()
		if _, err := f.put(t, g, "u_adm", GrantSpec{UserID: "u_none", Scope: GrantAll}); err != nil {
			t.Fatal(err)
		}
		f.exec(t, `DELETE FROM users WHERE id = 'u_adm'`)
		got, err := g.ForUser(context.Background(), "u_none")
		if err != nil || len(got) != 1 || got[0].CreatedBy != "" {
			t.Errorf("grants = %+v, %v", got, err)
		}
	})
}
