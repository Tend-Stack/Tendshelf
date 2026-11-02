package share

import (
	"context"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// fixture is a database with every kind of person and grant, written with raw
// INSERTs so the tests do not depend on the Grants repository.
//
//	libraries lib_a (bk_a1, bk_a2) and lib_b (bk_b1, bk_b2)
//	shelf sh_adm (owner u_adm) holds bk_b2; shelf sh_mem (owner u_none) holds bk_a1
//	u_lib: library lib_a, download   u_all: everything, read
//	u_shelf: shelf sh_adm            u_none: nothing
//	u_exp: book bk_b1, expired       u_memshelf: shelf sh_mem (raw; Put refuses it)
//	u_g (guest): book bk_b1, download and read   u_dis (disabled): library lib_a
type fixture struct {
	st    *db.Store
	c     *Checker
	t0    time.Time
	users map[string]users.User
}

var fixtureBooks = map[string]string{"bk_a1": "lib_a", "bk_a2": "lib_a", "bk_b1": "lib_b", "bk_b2": "lib_b"}

var fixtureLibraries = []string{"lib_a", "lib_b"}

func openFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := db.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	f := &fixture{st: st, t0: time.UnixMilli(1_000_000_000), users: map[string]users.User{}}
	f.c = New(st)
	f.c.now = func() time.Time { return f.t0 }
	for _, u := range []struct {
		id       string
		role     users.Role
		disabled bool
	}{
		{"u_own", users.RoleOwner, false}, {"u_adm", users.RoleAdmin, false},
		{"u_lib", users.RoleMember, false}, {"u_all", users.RoleMember, false}, {"u_shelf", users.RoleMember, false},
		{"u_none", users.RoleMember, false}, {"u_exp", users.RoleMember, false}, {"u_memshelf", users.RoleMember, false},
		{"u_g", users.RoleGuest, false}, {"u_dis", users.RoleMember, true},
	} {
		d := 0
		if u.disabled {
			d = 1
		}
		f.exec(t, `INSERT INTO users(id, username, role, disabled, created_at) VALUES (?, ?, ?, ?, 1)`, u.id, u.id, string(u.role), d)
		f.users[u.id] = users.User{ID: u.id, Username: u.id, Role: u.role, Disabled: u.disabled}
	}
	f.exec(t, `INSERT INTO libraries(id, name, created_at) VALUES ('lib_a', 'A', 1), ('lib_b', 'B', 1)`)
	f.exec(t, `INSERT INTO library_folders(id, library_id, path, created_at) VALUES ('lf_a', 'lib_a', '/a', 1), ('lf_b', 'lib_b', '/b', 1)`)
	for id, lib := range fixtureBooks {
		folder := "lf_a"
		if lib == "lib_b" {
			folder = "lf_b"
		}
		f.exec(t, `INSERT INTO books(id, library_id, folder_id, root_path, title, added_at, updated_at) VALUES (?, ?, ?, ?, ?, 1, 1)`,
			id, lib, folder, "/"+id, id)
	}
	f.exec(t, `INSERT INTO shelves(id, owner_id, name, created_at, updated_at) VALUES ('sh_adm', 'u_adm', 'Admin shelf', 1, 1), ('sh_mem', 'u_none', 'Member shelf', 1, 1)`)
	f.exec(t, `INSERT INTO shelf_items(shelf_id, book_id, idx, added_at) VALUES ('sh_adm', 'bk_b2', 0, 1), ('sh_mem', 'bk_a1', 0, 1)`)
	past, future := f.t0.UnixMilli()-1000, f.t0.UnixMilli()+1000
	f.grant(t, "u_lib", "library", "lib_a", 1, 0, 0)
	f.grant(t, "u_all", "all", "", 0, 1, 0)
	f.grant(t, "u_shelf", "shelf", "sh_adm", 0, 0, 0)
	f.grant(t, "u_exp", "book", "bk_b1", 0, 0, past)
	f.grant(t, "u_memshelf", "shelf", "sh_mem", 0, 0, 0)
	f.grant(t, "u_g", "book", "bk_b1", 1, 1, future)
	f.grant(t, "u_dis", "library", "lib_a", 0, 0, 0)
	return f
}

func (f *fixture) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := f.st.Writer().Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

// grant writes one raw grant row; expires 0 means never.
func (f *fixture) grant(t *testing.T, user, scope, target string, download, read int, expires int64) {
	t.Helper()
	cols := map[string]string{"library": "library_id", "shelf": "shelf_id", "book": "book_id"}
	var exp any
	if expires != 0 {
		exp = expires
	}
	if scope == "all" {
		f.exec(t, `INSERT INTO grants(id, user_id, scope, can_download, can_read, expires_at, created_at, updated_at) VALUES (?, ?, 'all', ?, ?, ?, 1, 1)`,
			"gr_"+user, user, download, read, exp)
		return
	}
	f.exec(t, `INSERT INTO grants(id, user_id, scope, `+cols[scope]+`, can_download, can_read, expires_at, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, 1, 1)`,
		"gr_"+user, user, scope, target, download, read, exp)
}

// user returns the fixture user with the given id, with the current role and disabled flag.
func (f *fixture) user(id string) users.User { return f.users[id] }

// visibleBooks returns the set of books the filters let u see, the same query the library package runs.
func (f *fixture) visibleBooks(t *testing.T, u users.User) map[string]bool {
	t.Helper()
	bf, lf := f.c.BookFilter(u, "b"), f.c.LibraryFilter(u, "l")
	rows, err := f.st.Reader().Query(`SELECT b.id FROM books b JOIN libraries l ON l.id = b.library_id WHERE (`+bf.SQL+`) AND (`+lf.SQL+`)`,
		append(append([]any{}, bf.Args...), lf.Args...)...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out[id] = true
	}
	return out
}

// visibleLibraries returns the set of libraries the filter lets u see.
func (f *fixture) visibleLibraries(t *testing.T, u users.User) map[string]bool {
	t.Helper()
	lf := f.c.LibraryFilter(u, "l")
	rows, err := f.st.Reader().Query(`SELECT l.id FROM libraries l WHERE `+lf.SQL, lf.Args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out[id] = true
	}
	return out
}
