package share

import (
	"context"
	"database/sql"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

func peopleByID(t *testing.T, f *fixture, now, month int64) map[string]Person {
	t.Helper()
	list, err := NewGrants(f.st).People(context.Background(), now, month)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]Person{}
	for _, p := range list {
		out[p.User.ID] = p
	}
	if list[0].User.Role != users.RoleOwner {
		t.Fatalf("first person is %s, want the owner", list[0].User.ID)
	}
	return out
}

func TestPeople(t *testing.T) {
	f := openFixture(t)
	now := f.t0.UnixMilli()
	f.exec(t, `INSERT INTO listening_sessions(id, user_id, book_id, device_id, started_at, ended_at, start_ms, end_ms, listened_ms, speed_pct)
		VALUES ('ls1', 'u_lib', 'bk_a1', 'x', ?, ?, 0, 1, 3600000, 100), ('ls2', 'u_lib', 'bk_a2', 'x', ?, ?, 0, 1, 1800000, 100),
		       ('ls3', 'u_lib', 'bk_a2', 'x', 5, 6, 0, 1, 999, 100)`, now-5000, now-4000, now-3000, now-2000)
	f.exec(t, `INSERT INTO sessions(token_hash, user_id, created_at, expires_at, last_seen_at, user_agent) VALUES (zeroblob(32), 'u_lib', 1, ?, 1, '')`, now+1000)
	f.exec(t, `INSERT INTO sessions(token_hash, user_id, created_at, expires_at, last_seen_at, user_agent) VALUES (randomblob(32), 'u_lib', 1, ?, 1, '')`, now-1000)
	f.exec(t, `INSERT INTO devices(id, user_id, name, kind, token_hash, created_at) VALUES ('d1', 'u_lib', 'Phone', 'mobile', randomblob(32), 1)`)
	f.exec(t, `INSERT INTO devices(id, user_id, name, kind, created_at, revoked_at) VALUES ('d2', 'u_lib', 'Old', 'mobile', 1, 2)`)
	f.exec(t, `UPDATE users SET last_seen_at = ? WHERE id = 'u_lib'`, now-100)
	p := peopleByID(t, f, now, now-10_000)

	lib := p["u_lib"]
	if lib.MonthMs != 5_400_000 || lib.Devices != 2 || lib.LastListen != now-2000 || lib.LastActive != now-100 {
		t.Fatalf("u_lib month=%d devices=%d listen=%d active=%d", lib.MonthMs, lib.Devices, lib.LastListen, lib.LastActive)
	}
	if lib.Books != 2 || lib.ActiveGrants != 1 || lib.FirstScope != GrantLibrary || lib.FirstName != "A" || lib.Everything {
		t.Fatalf("u_lib %+v", lib)
	}
	if a := p["u_all"]; !a.Everything || a.Books != 4 || a.FirstScope != GrantAll {
		t.Fatalf("u_all %+v", a)
	}
	if s := p["u_shelf"]; s.Books != 1 || s.FirstScope != GrantShelf || s.FirstName != "Admin shelf" {
		t.Fatalf("u_shelf %+v", s)
	}
	if e := p["u_exp"]; e.Grants != 1 || e.ActiveGrants != 0 || e.Books != 0 || e.FirstName != "" {
		t.Fatalf("u_exp (expired only) %+v", e)
	}
	if n := p["u_none"]; n.Grants != 0 || n.LastActive != 0 || n.Devices != 0 {
		t.Fatalf("u_none %+v", n)
	}
	// A shelf of a member covers nothing: the grant is active but opens no book.
	if m := p["u_memshelf"]; m.ActiveGrants != 1 || m.Books != 0 {
		t.Fatalf("u_memshelf %+v", m)
	}
}

func TestPeopleSeveralGrantsUseTheOldest(t *testing.T) {
	f := openFixture(t)
	f.exec(t, `INSERT INTO grants(id, user_id, scope, book_id, can_download, can_read, created_at, updated_at) VALUES ('gr_x', 'u_lib', 'book', 'bk_b1', 0, 0, 5, 5)`)
	p := peopleByID(t, f, f.t0.UnixMilli(), 0)["u_lib"]
	if p.ActiveGrants != 2 || p.Books != 3 || p.FirstScope != GrantLibrary {
		t.Fatalf("%+v", p)
	}
}

func TestForUserNamed(t *testing.T) {
	f := openFixture(t)
	g := NewGrants(f.st)
	got, err := g.ForUserNamed(context.Background(), "u_shelf")
	if err != nil || len(got) != 1 {
		t.Fatalf("%v %v", got, err)
	}
	if n := got[0]; n.Name != "Admin shelf" || n.OwnerName != "u_adm" || n.Books != 1 || n.Scope != GrantShelf {
		t.Fatalf("%+v", n)
	}
	got, _ = g.ForUserNamed(context.Background(), "u_all")
	if got[0].Books != 4 || got[0].Name != "" || !got[0].CanRead {
		t.Fatalf("%+v", got[0])
	}
}

func TestShelfTargets(t *testing.T) {
	f := openFixture(t)
	f.exec(t, `INSERT INTO shelves(id, owner_id, name, created_at, updated_at) VALUES ('sh_own', 'u_own', 'Kids', 1, 1)`)
	got, err := NewGrants(f.st).ShelfTargets(context.Background())
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	if got[0].ID != "sh_adm" || got[0].Books != 1 || got[1].ID != "sh_own" {
		t.Fatalf("%+v", got) // u_adm sorts before u_own; the member's shelf is absent
	}
	f.exec(t, `UPDATE users SET disabled = 1 WHERE id = 'u_adm'`)
	got, _ = NewGrants(f.st).ShelfTargets(context.Background())
	if len(got) != 1 || got[0].ID != "sh_own" {
		t.Fatalf("disabled owner's shelf listed: %+v", got)
	}
}

func TestRevokeAll(t *testing.T) {
	f := openFixture(t)
	f.exec(t, `INSERT INTO grants(id, user_id, scope, book_id, can_download, can_read, created_at, updated_at) VALUES ('gr_x', 'u_lib', 'book', 'bk_b1', 0, 0, 5, 5)`)
	g := NewGrants(f.st)
	var n int
	err := f.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		n, err = g.RevokeAll(context.Background(), tx, f.user("u_own"), "u_lib")
		return err
	})
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	var left, audits int
	_ = f.st.Reader().QueryRow(`SELECT count(*) FROM grants WHERE user_id = 'u_lib'`).Scan(&left)
	_ = f.st.Reader().QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'grant.revoke'`).Scan(&audits)
	if left != 0 || audits != 2 {
		t.Fatalf("left=%d audits=%d", left, audits)
	}
}

func TestWaitingInvites(t *testing.T) {
	f := openFixture(t)
	now := f.t0.UnixMilli()
	ins := func(id string, exp any, max, used int, revoked any) {
		f.exec(t, `INSERT INTO invites(id, token_hash, created_by, name, role, expires_at, max_uses, use_count, revoked_at, created_at, updated_at)
			VALUES (?, randomblob(32), 'u_own', 'N', 'member', ?, ?, ?, ?, 1, 1)`, id, exp, max, used, revoked)
	}
	ins("live", now+5000, 1, 0, nil)
	ins("forever", nil, 3, 1, nil)
	ins("revoked", nil, 1, 0, 7)
	ins("used", nil, 1, 1, nil)
	ins("expired", now-1, 1, 0, nil)
	n, err := WaitingInvites(context.Background(), f.st, now)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}
