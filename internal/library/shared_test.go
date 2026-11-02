package library_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// sharedEnv: library one holds b1..b3 and library two holds b4. Maya (member) and Rosa (guest)
// each hold a library grant on library one, which is a whole library and never a share.
// Daniel is an admin, Lena the owner.
type sharedEnv struct {
	t              *testing.T
	r              *library.Repo
	st             *db.Store
	b1, b2, b3, b4 library.Book
	checker        *share.Checker
	exec           func(q string, args ...any)
}

var (
	sharedLena   = users.User{ID: "us_lena", Role: users.RoleOwner}
	sharedDaniel = users.User{ID: "us_dan", Role: users.RoleAdmin}
	sharedMaya   = users.User{ID: "us_maya", Role: users.RoleMember}
	sharedRosa   = users.User{ID: "us_rosa", Role: users.RoleGuest}
)

func newSharedEnv(t *testing.T) *sharedEnv {
	t.Helper()
	r, st := newRepo(t)
	_, f1 := librarytest.NewLibrary(t, r, "One", "/one")
	l2, f2 := librarytest.NewLibrary(t, r, "Two", "/two")
	_ = l2
	e := &sharedEnv{t: t, r: r, st: st, checker: share.New(st)}
	add := func(f library.Folder, title string, at int64) library.Book {
		return librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: title, AddedAt: at, DurationMs: 1000})
	}
	e.b1, e.b2, e.b3, e.b4 = add(f1, "Book One", 1), add(f1, "Book Two", 2), add(f1, "Book Three", 3), add(f2, "Book Four", 4)
	e.exec = func(q string, args ...any) {
		t.Helper()
		if _, err := st.Writer().Exec(q, args...); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	e.exec(`INSERT INTO users(id, username, display_name, role, created_at) VALUES
		('us_lena', 'lena', 'Lena', 'owner', 1), ('us_dan', 'dan', 'Daniel', 'admin', 1),
		('us_maya', 'maya', 'Maya', 'member', 1), ('us_rosa', 'rosa', 'Rosa', 'guest', 1), ('us_mem2', 'mem2', '', 'member', 1)`)
	e.exec(`INSERT INTO grants(id, user_id, scope, library_id, created_at, updated_at)
		SELECT 'gl_' || u, u, 'library', ?, 1, 1 FROM (SELECT 'us_maya' AS u UNION SELECT 'us_rosa')`, e.b1.LibraryID)
	return e
}

func (e *sharedEnv) scope(u users.User) library.Scope {
	return library.Scope{Books: e.checker.BookFilter(u, "b"), Libraries: e.checker.LibraryFilter(u, "l"), Shared: e.checker.SharedBooks(u)}
}

// bookGrant shares a book with a user at time at, created by creator ("" for none).
func (e *sharedEnv) bookGrant(id, user, bookID, creator string, at int64, expires any) {
	var by any
	if creator != "" {
		by = creator
	}
	e.exec(`INSERT INTO grants(id, user_id, scope, book_id, created_by, created_at, updated_at, expires_at) VALUES (?, ?, 'book', ?, ?, ?, ?, ?)`,
		id, user, bookID, by, at, at, expires)
}

func (e *sharedEnv) shelfGrant(id, user, shelfID, creator string, at int64) {
	e.exec(`INSERT INTO grants(id, user_id, scope, shelf_id, created_by, created_at, updated_at) VALUES (?, ?, 'shelf', ?, ?, ?, ?)`,
		id, user, shelfID, creator, at, at)
}

func (e *sharedEnv) shelf(id, owner string, shared int, items map[string]int64) {
	e.exec(`INSERT INTO shelves(id, owner_id, name, shared, created_at, updated_at) VALUES (?, ?, ?, ?, 1, 1)`, id, owner, "Shelf "+id, shared)
	i := 0
	for book, at := range items {
		e.exec(`INSERT INTO shelf_items(shelf_id, book_id, idx, added_at) VALUES (?, ?, ?, ?)`, id, book, i, at)
		i++
	}
}

func sharedIDs(row library.SharedRow) []string {
	var out []string
	for _, b := range row.Books {
		out = append(out, b.Book.ID)
	}
	return out
}

func TestSharedWithYou(t *testing.T) {
	t.Run("book grant", func(t *testing.T) {
		e := newSharedEnv(t)
		e.bookGrant("g1", "us_maya", e.b1.ID, "us_dan", 100, nil)
		row, err := e.r.SharedWithYou(ctx, e.scope(sharedMaya), 3)
		if err != nil || row.Total != 1 || !reflect.DeepEqual(sharedIDs(row), []string{e.b1.ID}) {
			t.Fatalf("row = %+v, %v", row, err)
		}
		if b := row.Books[0]; b.SharedAt != 100 || b.SharerName != "Daniel" || !reflect.DeepEqual(row.Sharers, []string{"Daniel"}) {
			t.Errorf("book = %+v, sharers %v", b, row.Sharers)
		}
	})
	t.Run("order is newest first, a shelf grant counts from the later of grant and add", func(t *testing.T) {
		e := newSharedEnv(t)
		e.shelf("sh1", "us_dan", 0, map[string]int64{e.b2.ID: 500})
		e.shelfGrant("g1", "us_maya", "sh1", "us_dan", 300)
		e.bookGrant("g2", "us_maya", e.b1.ID, "us_lena", 400, nil)
		e.bookGrant("g3", "us_maya", e.b3.ID, "us_lena", 450, nil)
		row, err := e.r.SharedWithYou(ctx, e.scope(sharedMaya), 3)
		if err != nil || !reflect.DeepEqual(sharedIDs(row), []string{e.b2.ID, e.b3.ID, e.b1.ID}) || row.Books[0].SharedAt != 500 {
			t.Fatalf("row = %+v, %v", row, err)
		}
		if !reflect.DeepEqual(row.Sharers, []string{"Daniel", "Lena"}) {
			t.Errorf("sharers = %v", row.Sharers)
		}
		limited, err := e.r.SharedWithYou(ctx, e.scope(sharedMaya), 1)
		if err != nil || len(limited.Books) != 1 || limited.Total != 3 {
			t.Errorf("limit 1: %+v, %v", limited, err)
		}
	})
	t.Run("a shared admin shelf reaches members and staff, never guests", func(t *testing.T) {
		e := newSharedEnv(t)
		e.shelf("sh1", "us_dan", 1, map[string]int64{e.b3.ID: 600})
		for u, want := range map[string]int{"maya": 1, "rosa": 0} {
			sc := e.scope(map[string]users.User{"maya": sharedMaya, "rosa": sharedRosa}[u])
			row, err := e.r.SharedWithYou(ctx, sc, 3)
			if err != nil || row.Total != want {
				t.Errorf("%s: %+v, %v", u, row, err)
			}
		}
		own, err := e.r.SharedWithYou(ctx, e.scope(sharedDaniel), 3)
		if err != nil || own.Total != 0 {
			t.Errorf("the sharer's own shelf is not shared with them: %+v, %v", own, err)
		}
		lena, err := e.r.SharedWithYou(ctx, e.scope(sharedLena), 3)
		if err != nil || lena.Total != 1 || lena.Books[0].SharerName != "Daniel" {
			t.Errorf("owner: %+v, %v", lena, err)
		}
	})
	t.Run("one card per book, the newest source wins", func(t *testing.T) {
		e := newSharedEnv(t)
		e.shelf("sh1", "us_dan", 1, map[string]int64{e.b1.ID: 100})
		e.bookGrant("g1", "us_maya", e.b1.ID, "us_lena", 700, nil)
		row, err := e.r.SharedWithYou(ctx, e.scope(sharedMaya), 3)
		if err != nil || row.Total != 1 || len(row.Books) != 1 || row.Books[0].SharerName != "Lena" || row.Books[0].SharedAt != 700 {
			t.Fatalf("row = %+v, %v", row, err)
		}
		if !reflect.DeepEqual(row.Sharers, []string{"Lena", "Daniel"}) {
			t.Errorf("sharers = %v", row.Sharers)
		}
	})
	t.Run("expired grant, unknown creator", func(t *testing.T) {
		e := newSharedEnv(t)
		e.bookGrant("g1", "us_maya", e.b1.ID, "us_dan", 100, 150) // ended long ago
		e.bookGrant("g2", "us_maya", e.b2.ID, "", 100, nil)
		row, err := e.r.SharedWithYou(ctx, e.scope(sharedMaya), 3)
		if err != nil || !reflect.DeepEqual(sharedIDs(row), []string{e.b2.ID}) || row.Books[0].SharerName != "" || len(row.Sharers) != 0 {
			t.Fatalf("row = %+v, %v", row, err)
		}
	})
	t.Run("a disabled or demoted owner shares nothing", func(t *testing.T) {
		e := newSharedEnv(t)
		e.shelf("sh1", "us_dan", 1, map[string]int64{e.b1.ID: 100})
		e.shelf("sh2", "us_dan", 0, map[string]int64{e.b2.ID: 100})
		e.shelfGrant("g1", "us_maya", "sh2", "us_dan", 100)
		if row, _ := e.r.SharedWithYou(ctx, e.scope(sharedMaya), 3); row.Total != 2 {
			t.Fatalf("before: %+v", row)
		}
		e.exec(`UPDATE users SET disabled = 1 WHERE id = 'us_dan'`)
		if row, err := e.r.SharedWithYou(ctx, e.scope(sharedMaya), 3); err != nil || row.Total != 0 {
			t.Errorf("disabled: %+v, %v", row, err)
		}
		e.exec(`UPDATE users SET disabled = 0, role = 'member' WHERE id = 'us_dan'`)
		if row, err := e.r.SharedWithYou(ctx, e.scope(sharedMaya), 3); err != nil || row.Total != 0 {
			t.Errorf("demoted: %+v, %v", row, err)
		}
	})
	t.Run("a member's shared flag is no source", func(t *testing.T) {
		e := newSharedEnv(t)
		e.shelf("sh1", "us_mem2", 1, map[string]int64{e.b1.ID: 100})
		if row, err := e.r.SharedWithYou(ctx, e.scope(sharedMaya), 3); err != nil || row.Total != 0 {
			t.Errorf("row = %+v, %v", row, err)
		}
	})
	t.Run("hidden, refused and missing books are absent from books, total and sharers", func(t *testing.T) {
		e := newSharedEnv(t)
		// Daniel shares b1 (Maya may open it) and b4 (she may not); Lena shares only b4.
		e.shelf("sh1", "us_dan", 1, map[string]int64{e.b1.ID: 100, e.b4.ID: 200})
		e.shelf("sh2", "us_lena", 1, map[string]int64{e.b4.ID: 300})
		_, f1 := librarytest.NewLibrary(t, e.r, "Three", "/three")
		refused := librarytest.AddBook(t, e.r, f1, librarytest.BookSpec{Title: "Locked", Refused: "drm"})
		missing := librarytest.AddBook(t, e.r, f1, librarytest.BookSpec{Title: "Gone", MissingSince: 5})
		e.bookGrant("g1", "us_maya", refused.ID, "us_lena", 400, nil)
		e.bookGrant("g2", "us_maya", missing.ID, "us_lena", 400, nil)
		row, err := e.r.SharedWithYou(ctx, e.scope(sharedMaya), 3)
		if err != nil || row.Total != 1 || !reflect.DeepEqual(sharedIDs(row), []string{e.b1.ID}) || !reflect.DeepEqual(row.Sharers, []string{"Daniel"}) {
			t.Fatalf("row = %+v, %v", row, err)
		}
		for _, b := range row.Books {
			if strings.Contains(b.Book.Title, "Four") {
				t.Error("a hidden book was returned")
			}
		}
	})
	t.Run("the limit is clamped and the empty filter shares nothing", func(t *testing.T) {
		e := newSharedEnv(t)
		e.bookGrant("g1", "us_maya", e.b1.ID, "us_dan", 100, nil)
		if row, err := e.r.SharedWithYou(ctx, e.scope(sharedMaya), 0); err != nil || len(row.Books) != 1 {
			t.Errorf("limit 0: %+v, %v", row, err)
		}
		if row, err := e.r.SharedWithYou(ctx, e.scope(sharedMaya), 1000); err != nil || len(row.Books) != 1 {
			t.Errorf("limit 1000: %+v, %v", row, err)
		}
		sc := e.scope(sharedMaya)
		sc.Shared = share.Filter{}
		if row, err := e.r.SharedWithYou(ctx, sc, 3); err != nil || row.Total != 0 || row.Books != nil || row.Sharers != nil {
			t.Errorf("zero filter: %+v, %v", row, err)
		}
	})
}

func TestListBooksShowShared(t *testing.T) {
	e := newSharedEnv(t)
	e.shelf("sh1", "us_dan", 1, map[string]int64{e.b3.ID: 600, e.b4.ID: 650})
	e.bookGrant("g1", "us_maya", e.b1.ID, "us_lena", 400, nil)
	row, err := e.r.SharedWithYou(ctx, e.scope(sharedMaya), 24)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, id := range sharedIDs(row) {
		want[id] = true
	}
	for _, sort := range []library.Sort{library.SortAdded, library.SortTitle, library.SortAuthor, library.SortDuration} {
		// No UserID: the shared filter needs no user.
		page, err := e.r.ListBooks(ctx, e.scope(sharedMaya), library.ListQuery{Show: library.ShowShared, Sort: sort})
		if err != nil || page.Total != len(want) {
			t.Fatalf("%s: %+v, %v", sort, page, err)
		}
		for _, b := range page.Books {
			if !want[b.ID] {
				t.Errorf("%s: %s is not in the shared row", sort, b.ID)
			}
		}
	}
	sc := e.scope(sharedMaya)
	sc.Shared = share.Filter{}
	if page, err := e.r.ListBooks(ctx, sc, library.ListQuery{Show: library.ShowShared}); err != nil || page.Total != 0 {
		t.Errorf("zero filter: %+v, %v", page, err)
	}
}

func TestShelfByIDEffectiveShared(t *testing.T) {
	e := newSharedEnv(t)
	e.shelf("sh1", "us_dan", 1, map[string]int64{e.b1.ID: 1})
	e.shelf("sh2", "us_mem2", 1, nil)
	e.shelf("sh3", "us_dan", 0, nil)
	for id, want := range map[string]bool{"sh1": true, "sh2": false, "sh3": false} {
		sh, err := e.r.ShelfByID(ctx, id)
		if err != nil || sh.Shared != want {
			t.Errorf("%s: %+v, %v (want shared %v)", id, sh, err, want)
		}
	}
	if sh, _ := e.r.ShelfByID(ctx, "sh1"); sh.OwnerName != "Daniel" {
		t.Errorf("owner name = %q", sh.OwnerName)
	}
	if sh, _ := e.r.ShelfByID(ctx, "sh2"); sh.OwnerName != "mem2" {
		t.Errorf("owner name falls back to the username: %q", sh.OwnerName)
	}
	e.exec(`UPDATE users SET disabled = 1 WHERE id = 'us_dan'`)
	if sh, _ := e.r.ShelfByID(ctx, "sh1"); sh.Shared {
		t.Error("a disabled owner's shelf is shared")
	}
	if _, err := e.r.ShelfByID(ctx, "nope"); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("unknown: %v", err)
	}
	if _, err := e.r.Shelf(ctx, "us_maya", "sh3"); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("another's shelf through Shelf: %v", err)
	}
}

func TestSharedShelves(t *testing.T) {
	e := newSharedEnv(t)
	e.shelf("sh1", "us_dan", 1, map[string]int64{e.b1.ID: 1, e.b2.ID: 2, e.b4.ID: 3}) // b4 hidden from Maya
	e.shelf("sh2", "us_dan", 1, map[string]int64{e.b4.ID: 1})                         // nothing Maya may open
	e.shelf("sh3", "us_dan", 0, map[string]int64{e.b1.ID: 1})                         // not shared
	e.shelf("sh4", "us_mem2", 1, map[string]int64{e.b1.ID: 1})                        // member-owned
	e.shelf("sh5", "us_maya", 1, map[string]int64{e.b1.ID: 1})                        // her own
	got, err := e.r.SharedShelves(ctx, e.scope(sharedMaya), "us_maya")
	if err != nil || len(got) != 1 {
		t.Fatalf("Maya: %+v, %v", got, err)
	}
	if s := got[0]; s.ID != "sh1" || s.Books != 2 || s.OwnerName != "Daniel" || !s.Shared || len(s.Covers) != 2 {
		t.Errorf("shelf = %+v", s)
	}
	if got, err := e.r.SharedShelves(ctx, e.scope(sharedLena), "us_lena"); err != nil || len(got) != 2 {
		// The owner sees every enabled admin's shared shelf, with all books.
		t.Errorf("Lena: %d shelves, %v", len(got), err)
	}
	if got, err := e.r.SharedShelves(ctx, e.scope(sharedDaniel), "us_dan"); err != nil || len(got) != 0 {
		t.Errorf("Daniel (own shelves excluded): %+v, %v", got, err)
	}
	// Own shelves keep their old behaviour.
	if got, err := e.r.Shelves(ctx, e.scope(sharedMaya), "us_maya"); err != nil || len(got) != 1 || got[0].Books != 1 {
		t.Errorf("Shelves: %+v, %v", got, err)
	}
}

func TestSetShelfShared(t *testing.T) {
	e := newSharedEnv(t)
	e.shelf("sh1", "us_dan", 0, nil)
	var before int64
	e.exec(`UPDATE shelves SET updated_at = 77 WHERE id = 'sh1'`)
	before = 77
	if err := e.r.SetShelfShared(ctx, "us_dan", "sh1", true); err != nil {
		t.Fatal(err)
	}
	sh, err := e.r.ShelfByID(ctx, "sh1")
	if err != nil || !sh.Shared || sh.UpdatedAt != before {
		t.Errorf("after share: %+v, %v", sh, err)
	}
	if err := e.r.SetShelfShared(ctx, "us_lena", "sh1", false); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("another's shelf: %v", err)
	}
	if sh, _ := e.r.ShelfByID(ctx, "sh1"); !sh.Shared {
		t.Error("another person changed the flag")
	}
	if err := e.r.SetShelfShared(ctx, "us_dan", "sh1", false); err != nil {
		t.Fatal(err)
	}
	if sh, _ := e.r.ShelfByID(ctx, "sh1"); sh.Shared {
		t.Error("flag still set")
	}
	var n int
	var detail string
	if err := e.st.Reader().QueryRow(`SELECT COUNT(*), coalesce(max(detail_json), '') FROM audit_log WHERE action = 'shelf.share' AND target = 'shelf:sh1' AND user_id = 'us_dan'`).Scan(&n, &detail); err != nil {
		t.Fatal(err)
	}
	if n != 2 || !strings.Contains(detail, `"shared"`) {
		t.Errorf("audit rows = %d, detail %q", n, detail)
	}
	// A refused write leaves no audit row.
	e.exec(`DELETE FROM audit_log`)
	_ = e.r.SetShelfShared(ctx, "us_lena", "sh1", true)
	if err := e.st.Reader().QueryRow(`SELECT COUNT(*) FROM audit_log WHERE action = 'shelf.share'`).Scan(&n); err != nil || n != 0 {
		t.Errorf("audit rows after a refused write = %d, %v", n, err)
	}
}
