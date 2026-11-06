package share

import (
	"context"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

// visibleFor lists the books the BookFilterFor fragment lets u through for action a.
func (f *fixture) visibleFor(t *testing.T, u users.User, a Action) map[string]bool {
	t.Helper()
	bf := f.c.BookFilterFor(u, "b", a)
	rows, err := f.st.Reader().Query(`SELECT b.id FROM books b WHERE `+bf.SQL, bf.Args...)
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

// TestBookFilterForAgreesWithCan: for every person in the fixture plus two people with split grants (one
// grant gives download, another gives read, so only the maximum across grants allows DownloadText), the
// SQL filter and Can give the same answer for every book and every action it supports.
func TestBookFilterForAgreesWithCan(t *testing.T) {
	f := openFixture(t)
	ctx := context.Background()
	for _, u := range []struct{ id, role string }{{"u_split", "member"}, {"u_expsplit", "member"}} {
		f.exec(t, `INSERT INTO users(id, username, role, disabled, created_at) VALUES (?, ?, ?, 0, 1)`, u.id, u.id, u.role)
		f.users[u.id] = users.User{ID: u.id, Username: u.id, Role: users.Role(u.role)}
	}
	// u_split: download-only on the library, read-only on one book of it: bk_a1 may be downloaded as text.
	f.exec(t, `INSERT INTO grants(id, user_id, scope, library_id, can_download, can_read, created_at, updated_at) VALUES ('gr_s1', 'u_split', 'library', 'lib_a', 1, 0, 1, 1)`)
	f.exec(t, `INSERT INTO grants(id, user_id, scope, book_id, can_download, can_read, created_at, updated_at) VALUES ('gr_s2', 'u_split', 'book', 'bk_a1', 0, 1, 1, 1)`)
	// u_expsplit: the read grant has ended, so nothing beyond viewing remains.
	f.exec(t, `INSERT INTO grants(id, user_id, scope, library_id, can_download, can_read, created_at, updated_at) VALUES ('gr_e1', 'u_expsplit', 'library', 'lib_a', 1, 0, 1, 1)`)
	f.exec(t, `INSERT INTO grants(id, user_id, scope, book_id, can_download, can_read, expires_at, created_at, updated_at) VALUES ('gr_e2', 'u_expsplit', 'book', 'bk_a1', 0, 1, 5, 1, 1)`)

	actions := []Action{ViewBook, ReadText, DownloadText}
	for id, u := range f.users {
		for _, a := range actions {
			got := f.visibleFor(t, u, a)
			for bid, lib := range fixtureBooks {
				want := f.c.Can(ctx, u, a, Target{Kind: TargetBook, ID: bid, LibraryID: lib})
				if got[bid] != want {
					t.Errorf("%s %s on %s: filter %v, Can %v", id, a, bid, got[bid], want)
				}
			}
		}
	}
	if !f.visibleFor(t, f.users["u_split"], DownloadText)["bk_a1"] {
		t.Error("split grants: bk_a1 should be downloadable as text")
	}
	if f.visibleFor(t, f.users["u_split"], DownloadText)["bk_a2"] {
		t.Error("split grants: bk_a2 has no read right")
	}
	if len(f.visibleFor(t, f.users["u_none"], ViewBook)) != 0 {
		t.Error("a person with no grants sees books")
	}
	if len(f.visibleFor(t, f.users["u_own"], DownloadText)) != len(fixtureBooks) {
		t.Error("the owner does not see every book")
	}
	if len(f.visibleFor(t, f.users["u_own"], DownloadBook)) != 0 {
		t.Error("an unsupported action matched books")
	}
}
