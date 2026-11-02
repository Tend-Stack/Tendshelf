package share

import (
	"context"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

var allActions = []Action{
	ViewLibrary, ViewBook, ListenBook, DownloadBook, EditBook, ReadText, DownloadText,
	ManageLibraries, ManageJobs, ManageUsers, ManageServer, UseShelves, ViewShelf,
}

// roleWant states what an owner or admin may do with an action on a target kind. Shelves of
// other people and user targets have their own rules.
func roleWant(role users.Role, a Action, tname string) bool {
	switch tname {
	case "other shelf":
		return false
	case "shared shelf":
		return a == ViewShelf // looking is all anyone but the owner may do
	case "member user", "guest user":
		return a == ManageUsers
	case "admin user", "owner user":
		return a == ManageUsers && role == users.RoleOwner
	}
	if role == users.RoleOwner {
		return true
	}
	return a != ManageServer
}

func TestCanMatrix(t *testing.T) {
	f := openFixture(t)
	ctx := context.Background()
	targets := map[string]Target{
		"server":       {Kind: TargetServer},
		"library":      {Kind: TargetLibrary, ID: "lib_a"},
		"library b":    {Kind: TargetLibrary, ID: "lib_b"},
		"book a1":      {Kind: TargetBook, ID: "bk_a1", LibraryID: "lib_a"},
		"book a2":      {Kind: TargetBook, ID: "bk_a2", LibraryID: "lib_a"},
		"book b1":      {Kind: TargetBook, ID: "bk_b1", LibraryID: "lib_b"},
		"book b2":      {Kind: TargetBook, ID: "bk_b2", LibraryID: "lib_b"},
		"own shelf":    {Kind: TargetShelf, ID: "sh1", OwnerID: "SELF"},
		"other shelf":  {Kind: TargetShelf, ID: "sh2", OwnerID: "u_other"},
		"shared shelf": {Kind: TargetShelf, ID: "sh3", OwnerID: "u_other", Shared: true},
		"owner user":   {Kind: TargetUser, ID: "x", Role: users.RoleOwner},
		"admin user":   {Kind: TargetUser, ID: "x", Role: users.RoleAdmin},
		"member user":  {Kind: TargetUser, ID: "x", Role: users.RoleMember},
		"guest user":   {Kind: TargetUser, ID: "x", Role: users.RoleGuest},
	}
	target := func(name string, u users.User) Target {
		tg := targets[name]
		if tg.OwnerID == "SELF" {
			tg.OwnerID = u.ID
		}
		return tg
	}

	// Owner, admin and a disabled owner decide by role alone, whatever the grants.
	for _, id := range []string{"u_own", "u_adm"} {
		u := f.user(id)
		for name := range targets {
			for _, a := range allActions {
				want := roleWant(u.Role, a, name)
				if name == "own shelf" {
					want = u.Role == users.RoleOwner || a != ManageServer
				}
				if got := f.c.Can(ctx, u, a, target(name, u)); got != want {
					t.Errorf("%s %s on %s: got %v want %v", id, a, name, got, want)
				}
			}
		}
	}
	off := f.user("u_own")
	off.Disabled = true
	for name := range targets {
		for _, a := range allActions {
			if f.c.Can(ctx, off, a, target(name, off)) {
				t.Errorf("disabled owner %s on %s allowed", a, name)
			}
		}
	}

	// A member or guest may do only what an active grant says; users, the server and
	// other people's shelves are closed, and the shelves of their own allow UseShelves
	// (members) and looking (members).
	books := func(ids ...string) map[string]bool {
		m := map[string]bool{}
		for _, id := range ids {
			m[id] = true
		}
		return m
	}
	type grantee struct {
		id        string
		books     map[string]bool
		libs      map[string]bool
		download  bool
		read      bool
		shelfOpen bool // may use and look at an own shelf (members)
	}
	for _, g := range []grantee{
		{"u_lib", books("bk_a1", "bk_a2"), books("lib_a"), true, false, true},
		{"u_all", books("bk_a1", "bk_a2", "bk_b1", "bk_b2"), books("lib_a", "lib_b"), false, true, true},
		{"u_shelf", books("bk_b2"), books("lib_b"), false, false, true},
		{"u_none", nil, nil, false, false, true},
		{"u_exp", nil, nil, false, false, true},
		{"u_memshelf", nil, nil, false, false, true},
		{"u_g", books("bk_b1"), books("lib_b"), true, true, false},
		{"u_dis", nil, nil, false, false, true},
	} {
		u := f.user(g.id)
		for name := range targets {
			tg := target(name, u)
			for _, a := range allActions {
				var want bool
				switch tg.Kind {
				case TargetLibrary:
					want = a == ViewLibrary && g.libs[tg.ID] && !u.Disabled
				case TargetBook:
					if g.books[tg.ID] && !u.Disabled {
						switch a {
						case ViewBook, ListenBook:
							want = true
						case DownloadBook:
							want = g.download
						case ReadText:
							want = g.read
						case DownloadText:
							want = g.download && g.read
						}
					}
				case TargetShelf:
					switch {
					case u.Disabled || !g.shelfOpen:
					case name == "own shelf":
						want = a == UseShelves || a == ViewShelf
					case name == "shared shelf":
						want = a == ViewShelf
					}
				}
				if got := f.c.Can(ctx, u, a, tg); got != want {
					t.Errorf("%s %s on %s: got %v want %v", g.id, a, name, got, want)
				}
			}
		}
	}
}

func TestCanAgreesWithFilter(t *testing.T) {
	f := openFixture(t)
	ctx := context.Background()
	for id, u := range f.users {
		books, libs := f.visibleBooks(t, u), f.visibleLibraries(t, u)
		for bid, lib := range fixtureBooks {
			if got := f.c.Can(ctx, u, ViewBook, Target{Kind: TargetBook, ID: bid, LibraryID: lib}); got != books[bid] {
				t.Errorf("%s on %s: Can %v, filter %v", id, bid, got, books[bid])
			}
		}
		for _, lib := range fixtureLibraries {
			if got := f.c.Can(ctx, u, ViewLibrary, Target{Kind: TargetLibrary, ID: lib}); got != libs[lib] {
				t.Errorf("%s on %s: Can %v, filter %v", id, lib, got, libs[lib])
			}
		}
	}
}

func TestGrantExpiryBoundary(t *testing.T) {
	f := openFixture(t)
	ctx := context.Background()
	now := f.t0.UnixMilli()
	u := f.user("u_exp")
	for _, c := range []struct {
		expires int64
		want    bool
	}{{now - 1, false}, {now, false}, {now + 1, true}} {
		f.exec(t, `UPDATE grants SET expires_at = ? WHERE user_id = 'u_exp'`, c.expires)
		got := f.c.Can(ctx, u, ViewBook, Target{Kind: TargetBook, ID: "bk_b1"})
		if got != c.want {
			t.Errorf("expires %+d: Can %v want %v", c.expires-now, got, c.want)
		}
		if seen := f.visibleBooks(t, u)["bk_b1"]; seen != c.want {
			t.Errorf("expires %+d: filter %v want %v", c.expires-now, seen, c.want)
		}
		if seen := f.visibleLibraries(t, u)["lib_b"]; seen != c.want {
			t.Errorf("expires %+d: library filter %v want %v", c.expires-now, seen, c.want)
		}
	}
	f.exec(t, `UPDATE grants SET expires_at = NULL WHERE user_id = 'u_exp'`)
	if !f.c.Can(ctx, u, ViewBook, Target{Kind: TargetBook, ID: "bk_b1"}) {
		t.Error("a grant without an end was refused")
	}
}

func TestShelfGrantFollowsShelf(t *testing.T) {
	ctx := context.Background()
	book := Target{Kind: TargetBook, ID: "bk_b2"}
	for _, c := range []struct {
		name string
		stmt string
	}{
		{"item removed", `DELETE FROM shelf_items WHERE shelf_id = 'sh_adm'`},
		{"owner disabled", `UPDATE users SET disabled = 1 WHERE id = 'u_adm'`},
		{"owner demoted", `UPDATE users SET role = 'member' WHERE id = 'u_adm'`},
		{"shelf deleted", `DELETE FROM shelves WHERE id = 'sh_adm'`},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := openFixture(t)
			u := f.user("u_shelf")
			if !f.c.Can(ctx, u, ViewBook, book) || !f.visibleBooks(t, u)["bk_b2"] || !f.visibleLibraries(t, u)["lib_b"] {
				t.Fatal("the shelf grant did not open the book before the change")
			}
			f.exec(t, c.stmt)
			if f.c.Can(ctx, u, ViewBook, book) || len(f.visibleBooks(t, u)) != 0 || len(f.visibleLibraries(t, u)) != 0 {
				t.Error("access remained after the shelf changed")
			}
		})
	}
	t.Run("member shelf grants nothing", func(t *testing.T) {
		f := openFixture(t)
		u := f.user("u_memshelf")
		if f.c.Can(ctx, u, ViewBook, Target{Kind: TargetBook, ID: "bk_a1"}) || len(f.visibleBooks(t, u)) != 0 || len(f.visibleLibraries(t, u)) != 0 {
			t.Error("a grant on a member's shelf opened a book")
		}
	})
	t.Run("only the shelf's books", func(t *testing.T) {
		f := openFixture(t)
		got := f.visibleBooks(t, f.user("u_shelf"))
		if len(got) != 1 || !got["bk_b2"] {
			t.Errorf("books = %v, want only bk_b2", got)
		}
	})
}

func TestCanTrustsBookLibrary(t *testing.T) {
	f := openFixture(t)
	u := f.user("u_lib") // library grant on lib_a only
	lie := Target{Kind: TargetBook, ID: "bk_b1", LibraryID: "lib_a"}
	if f.c.Can(context.Background(), u, ViewBook, lie) {
		t.Error("a Target.LibraryID the caller chose opened a book of another library")
	}
	if !f.c.Can(context.Background(), u, ViewBook, Target{Kind: TargetBook, ID: "bk_a1", LibraryID: "lib_b"}) {
		t.Error("a wrong Target.LibraryID closed a granted book")
	}
}

func TestCanStoreError(t *testing.T) {
	ctx := context.Background()
	f := openFixture(t)
	member, owner := f.user("u_all"), f.user("u_own")
	book := Target{Kind: TargetBook, ID: "bk_a1"}
	lib := Target{Kind: TargetLibrary, ID: "lib_a"}
	if err := f.st.Close(); err != nil {
		t.Fatal(err)
	}
	for _, tg := range []Target{book, lib} {
		a := ViewBook
		if tg.Kind == TargetLibrary {
			a = ViewLibrary
		}
		if f.c.Can(ctx, member, a, tg) {
			t.Errorf("closed store: member allowed %s", a)
		}
		if !f.c.Can(ctx, owner, a, tg) {
			t.Errorf("closed store: owner refused %s", a)
		}
		nilStore := New(nil)
		if nilStore.Can(ctx, member, a, tg) || nilStore.Can(ctx, f.user("u_g"), a, tg) {
			t.Errorf("nil store: member or guest allowed %s", a)
		}
		if !nilStore.Can(ctx, owner, a, tg) {
			t.Errorf("nil store: owner refused %s", a)
		}
	}
}

func TestCanUnknownActionFalse(t *testing.T) {
	c := New(nil)
	owner := users.User{ID: "u1", Role: users.RoleOwner}
	for _, a := range []Action{"", "book.delete", "BOOK.VIEW", "server.manage "} {
		if c.Can(context.Background(), owner, a, Target{Kind: TargetServer}) {
			t.Errorf("unknown action %q allowed", a)
		}
	}
	if c.Can(context.Background(), users.User{Role: "root"}, ViewBook, Target{Kind: TargetBook}) {
		t.Error("unknown role allowed")
	}
}

func TestFilters(t *testing.T) {
	c := New(nil)
	for _, tc := range []struct {
		u    users.User
		want string // "exists" for the grant subquery
	}{
		{users.User{ID: "u1", Role: users.RoleOwner}, "1=1"},
		{users.User{ID: "u1", Role: users.RoleAdmin}, "1=1"},
		{users.User{ID: "u1", Role: users.RoleMember}, "exists"},
		{users.User{ID: "u1", Role: users.RoleGuest}, "exists"},
		{users.User{ID: "u1", Role: users.RoleOwner, Disabled: true}, "0=1"},
		{users.User{ID: "u1", Role: users.RoleMember, Disabled: true}, "0=1"},
		{users.User{ID: "u1", Role: "root"}, "0=1"},
	} {
		for name, f := range map[string]Filter{"book": c.BookFilter(tc.u, "b"), "library": c.LibraryFilter(tc.u, "lib")} {
			if tc.want != "exists" {
				if f.SQL != tc.want || len(f.Args) != 0 {
					t.Errorf("%s filter for %v = %+v", name, tc.u.Role, f)
				}
				continue
			}
			if !strings.HasPrefix(f.SQL, "EXISTS (SELECT 1 FROM grants share_g WHERE ") || len(f.Args) != 2 || f.Args[0] != "u1" {
				t.Errorf("%s filter for %v = %+v", name, tc.u.Role, f)
			}
			if got := strings.Count(f.SQL, "?"); got != 2 {
				t.Errorf("%s filter for %v has %d placeholders, want 2", name, tc.u.Role, got)
			}
		}
	}
}

func TestFilterAliasPanics(t *testing.T) {
	c := New(nil)
	for _, alias := range []string{"", "b; DROP TABLE users", "B", "b1", "a.b", "abcdefghijklmnopq", "b b", "share_g", "share_b", "share_"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("alias %q did not panic", alias)
				}
			}()
			c.BookFilter(users.User{Role: users.RoleOwner}, alias)
		}()
	}
}
