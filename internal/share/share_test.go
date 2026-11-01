package share

import (
	"context"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

var allActions = []Action{
	ViewLibrary, ViewBook, ListenBook, DownloadBook, EditBook, ReadText, DownloadText,
	ManageLibraries, ManageJobs, ManageUsers, ManageServer, UseShelves, UseResearch,
}

func TestCanMatrix(t *testing.T) {
	c := New(nil)
	ctx := context.Background()
	mk := func(role users.Role, disabled bool) users.User {
		return users.User{ID: "u1", Role: role, Disabled: disabled}
	}
	targets := map[string]Target{
		"server":      {Kind: TargetServer},
		"library":     {Kind: TargetLibrary, ID: "lib1"},
		"book":        {Kind: TargetBook, ID: "bk1", LibraryID: "lib1"},
		"own shelf":   {Kind: TargetShelf, ID: "sh1", OwnerID: "u1"},
		"other shelf": {Kind: TargetShelf, ID: "sh2", OwnerID: "u2"},
	}
	for tname, tg := range targets {
		for _, a := range allActions {
			otherShelf := tname == "other shelf"
			ownShelf := tname == "own shelf"
			want := map[string]bool{
				"owner":    !otherShelf,
				"admin":    !otherShelf && a != ManageServer,
				"member":   ownShelf && a == UseShelves,
				"guest":    false,
				"disabled": false,
			}
			for role, w := range want {
				u := mk(users.Role(role), false)
				if role == "disabled" {
					u = mk(users.RoleOwner, true)
				}
				if got := c.Can(ctx, u, a, tg); got != w {
					t.Errorf("%s %s on %s: got %v want %v", role, a, tname, got, w)
				}
			}
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
		want string
	}{
		{users.User{Role: users.RoleOwner}, "1=1"},
		{users.User{Role: users.RoleAdmin}, "1=1"},
		{users.User{Role: users.RoleMember}, "0=1"},
		{users.User{Role: users.RoleGuest}, "0=1"},
		{users.User{Role: users.RoleOwner, Disabled: true}, "0=1"},
	} {
		if f := c.BookFilter(tc.u, "b"); f.SQL != tc.want || len(f.Args) != 0 {
			t.Errorf("BookFilter(%v) = %+v", tc.u.Role, f)
		}
		if f := c.LibraryFilter(tc.u, "lib"); f.SQL != tc.want {
			t.Errorf("LibraryFilter(%v) = %+v", tc.u.Role, f)
		}
	}
}

func TestFilterAliasPanics(t *testing.T) {
	c := New(nil)
	for _, alias := range []string{"", "b; DROP TABLE users", "B", "b1", "a.b", "abcdefghijklmnopq", "b b"} {
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
