package library_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// shelfUser inserts a person so shelves can reference it.
func shelfUser(t *testing.T, st *db.Store, id string) string {
	t.Helper()
	role := "member"
	if id == "us_a" {
		role = "owner"
	}
	if _, err := st.Writer().Exec(`INSERT INTO users(id, username, role, created_at) VALUES (?, ?, ?, 1)`, id, id, role); err != nil {
		t.Fatal(err)
	}
	return id
}

// shelfSeed is a repo with two people (us_a, us_b) and n visible books titled B0..Bn-1.
func shelfSeed(t *testing.T, n int) (*library.Repo, *db.Store, []library.Book, library.Folder) {
	t.Helper()
	r, st := newRepo(t)
	shelfUser(t, st, "us_a")
	shelfUser(t, st, "us_b")
	_, f := librarytest.NewLibrary(t, r, "Main", "/audio")
	var books []library.Book
	for i := 0; i < n; i++ {
		books = append(books, librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: fmt.Sprintf("B%d", i)}))
	}
	return r, st, books, f
}

func shelfIDs(t *testing.T, r *library.Repo, sc library.Scope, owner, id string) []string {
	t.Helper()
	var out []string
	cursor := ""
	for {
		p, err := r.ShelfBooks(ctx, sc, owner, id, cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range p.Books {
			out = append(out, b.Title)
		}
		if p.Next == "" {
			return out
		}
		cursor = p.Next
	}
}

func TestShelfCRUD(t *testing.T) {
	r, _, books, _ := shelfSeed(t, 2)
	s, err := r.CreateShelf(ctx, "us_a", "  Sleep  ")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(s.ID, "shf_") || s.Name != "Sleep" || s.OwnerID != "us_a" {
		t.Fatalf("shelf = %+v", s)
	}
	if err := r.RenameShelf(ctx, "us_a", s.ID, "Trip"); err != nil {
		t.Fatal(err)
	}
	got, err := r.Shelf(ctx, "us_a", s.ID)
	if err != nil || got.Name != "Trip" || got.UpdatedAt <= s.UpdatedAt {
		t.Fatalf("after rename = %+v, %v", got, err)
	}
	if err := r.AddToShelf(ctx, "us_a", s.ID, books[0].ID); err != nil {
		t.Fatal(err)
	}
	list, err := r.Shelves(ctx, allScope, "us_a")
	if err != nil || len(list) != 1 || list[0].Books != 1 || list[0].Name != "Trip" {
		t.Fatalf("shelves = %+v, %v", list, err)
	}
	with, err := r.ShelvesWithBook(ctx, "us_a", books[0].ID)
	if err != nil || !with[s.ID] || len(with) != 1 {
		t.Fatalf("with = %v, %v", with, err)
	}
	if err := r.RemoveFromShelf(ctx, "us_a", s.ID, books[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := shelfIDs(t, r, allScope, "us_a", s.ID); len(got) != 0 {
		t.Fatalf("books after remove = %v", got)
	}
	if err := r.DeleteShelf(ctx, "us_a", s.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Shelf(ctx, "us_a", s.ID); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("after delete: %v", err)
	}
	if err := r.DeleteShelf(ctx, "us_a", s.ID); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
}

func TestShelfNameRules(t *testing.T) {
	r, _, _, _ := shelfSeed(t, 0)
	for _, c := range []struct {
		name string
		want error
	}{
		{"", library.ErrInvalid},
		{"   ", library.ErrInvalid},
		{strings.Repeat("a", 81), library.ErrInvalid},
		{"bad\x00name", library.ErrInvalid},
		{"tab\there", library.ErrInvalid},
		{strings.Repeat("é", 80), nil},
		{"  Road trip  ", nil},
		{"road TRIP", library.ErrConflict},
		{"Road trip", library.ErrConflict},
		{" ROAD trip", library.ErrConflict},
		{"日本語の棚", nil},
	} {
		_, err := r.CreateShelf(ctx, "us_a", c.name)
		if !errors.Is(err, c.want) && !(c.want == nil && err == nil) {
			t.Errorf("CreateShelf(%q) = %v, want %v", c.name, err, c.want)
		}
	}
	// The same name for another person is fine.
	if _, err := r.CreateShelf(ctx, "us_b", "road trip"); err != nil {
		t.Fatalf("other owner: %v", err)
	}
	// Rename into a taken name, ignoring case, is a conflict; to its own name is fine.
	s, _ := r.CreateShelf(ctx, "us_a", "Other")
	if err := r.RenameShelf(ctx, "us_a", s.ID, "ROAD TRIP"); !errors.Is(err, library.ErrConflict) {
		t.Fatalf("rename conflict: %v", err)
	}
	if err := r.RenameShelf(ctx, "us_a", s.ID, "other"); err != nil {
		t.Fatalf("rename to own name: %v", err)
	}
	if err := r.RenameShelf(ctx, "us_a", s.ID, " "); !errors.Is(err, library.ErrInvalid) {
		t.Fatalf("rename empty: %v", err)
	}
}

func TestShelfOwnerIsolation(t *testing.T) {
	r, _, books, _ := shelfSeed(t, 2)
	s, _ := r.CreateShelf(ctx, "us_a", "Mine")
	if err := r.AddToShelf(ctx, "us_a", s.ID, books[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := r.AddToShelf(ctx, "us_a", s.ID, books[1].ID); err != nil {
		t.Fatal(err)
	}
	notFound := func(name string, err error) {
		t.Helper()
		if !errors.Is(err, library.ErrNotFound) {
			t.Errorf("%s as another person = %v, want ErrNotFound", name, err)
		}
	}
	_, err := r.Shelf(ctx, "us_b", s.ID)
	notFound("Shelf", err)
	notFound("RenameShelf", r.RenameShelf(ctx, "us_b", s.ID, "Taken"))
	notFound("DeleteShelf", r.DeleteShelf(ctx, "us_b", s.ID))
	_, err = r.ShelfBooks(ctx, allScope, "us_b", s.ID, "", 0)
	notFound("ShelfBooks", err)
	notFound("AddToShelf", r.AddToShelf(ctx, "us_b", s.ID, books[0].ID))
	notFound("RemoveFromShelf", r.RemoveFromShelf(ctx, "us_b", s.ID, books[0].ID))
	notFound("MoveInShelf", r.MoveInShelf(ctx, allScope, "us_b", s.ID, books[1].ID, -1))
	if list, err := r.Shelves(ctx, allScope, "us_b"); err != nil || len(list) != 0 {
		t.Errorf("Shelves(us_b) = %v, %v", list, err)
	}
	if with, err := r.ShelvesWithBook(ctx, "us_b", books[0].ID); err != nil || len(with) != 0 {
		t.Errorf("ShelvesWithBook(us_b) = %v, %v", with, err)
	}
	// Nothing changed for the owner.
	got, _ := r.Shelf(ctx, "us_a", s.ID)
	if got.Name != "Mine" || len(shelfIDs(t, r, allScope, "us_a", s.ID)) != 2 {
		t.Errorf("owner's shelf changed: %+v", got)
	}
}

func TestShelfBooksScoped(t *testing.T) {
	r, _, _, f := shelfSeed(t, 0)
	visible1 := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Open one", Authors: []string{"A"}})
	visible2 := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Open two"})
	refused := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Refused", Refused: library.RefusedDRM})
	missing := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Missing", MissingSince: 5})
	hidden := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Hidden"})
	s, _ := r.CreateShelf(ctx, "us_a", "Mix")
	for _, b := range []library.Book{refused, visible1, missing, hidden, visible2} {
		if err := r.AddToShelf(ctx, "us_a", s.ID, b.ID); err != nil {
			t.Fatal(err)
		}
	}
	sc := library.Scope{
		Books:     share.Filter{SQL: "b.id <> ?", Args: []any{hidden.ID}},
		Libraries: share.Filter{SQL: "1=1"},
	}
	if got := shelfIDs(t, r, sc, "us_a", s.ID); strings.Join(got, ",") != "Open one,Open two" {
		t.Fatalf("books = %v", got)
	}
	p, err := r.ShelfBooks(ctx, sc, "us_a", s.ID, "", 0)
	if err != nil || p.Total != 2 || len(p.Books) != 2 {
		t.Fatalf("page = %+v, %v", p, err)
	}
	list, err := r.Shelves(ctx, sc, "us_a")
	if err != nil || len(list) != 1 || list[0].Books != 2 {
		t.Fatalf("shelves = %+v, %v", list, err)
	}
	if n := len(list[0].Covers); n > 3 {
		t.Fatalf("covers = %d", n)
	}
	counts, _ := r.ShelfCounts(ctx, sc, "us_a")
	if counts[0].Books != 2 {
		t.Fatalf("counts = %+v", counts)
	}
	// Nothing visible: the shelf stays, empty.
	list, err = r.Shelves(ctx, noneScope, "us_a")
	if err != nil || len(list) != 1 || list[0].Books != 0 || len(list[0].Covers) != 0 {
		t.Fatalf("none scope shelves = %+v, %v", list, err)
	}
	p, err = r.ShelfBooks(ctx, noneScope, "us_a", s.ID, "", 0)
	if err != nil || p.Total != 0 || len(p.Books) != 0 {
		t.Fatalf("none scope page = %+v, %v", p, err)
	}
	// The hidden book reappears with a wider scope; the rows were never removed.
	if got := shelfIDs(t, r, allScope, "us_a", s.ID); strings.Join(got, ",") != "Open one,Hidden,Open two" {
		t.Fatalf("books with all scope = %v", got)
	}
	// Moving a hidden book is refused.
	if err := r.MoveInShelf(ctx, sc, "us_a", s.ID, hidden.ID, 1); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("move hidden = %v", err)
	}
}

func TestShelfCoversFollowShelfOrder(t *testing.T) {
	r, _, _, f := shelfSeed(t, 0)
	s, _ := r.CreateShelf(ctx, "us_a", "Covers")
	var hidden library.Book
	for i := 0; i < 5; i++ {
		b := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: fmt.Sprintf("C%d", i)})
		c := newCover(b.ID, byte('a'+i))
		changes(t, r, func(tx *library.Tx) error {
			if err := tx.PutCover(&c); err != nil {
				return err
			}
			return tx.SetBookCover(b.ID, c.ID)
		})
		if i == 1 {
			hidden = b
		}
		if err := r.AddToShelf(ctx, "us_a", s.ID, b.ID); err != nil {
			t.Fatal(err)
		}
	}
	sc := library.Scope{Books: share.Filter{SQL: "b.id <> ?", Args: []any{hidden.ID}}, Libraries: share.Filter{SQL: "1=1"}}
	list, err := r.Shelves(ctx, sc, "us_a")
	if err != nil || list[0].Books != 4 || len(list[0].Covers) != 3 {
		t.Fatalf("shelves = %+v, %v", list, err)
	}
	for i, want := range []byte{'a', 'c', 'd'} { // the hidden book's cover is skipped
		if list[0].Covers[i].SHA != hash(want) {
			t.Errorf("cover %d = %s, want %c", i, list[0].Covers[i].SHA[:1], want)
		}
	}
}

func TestShelfCursorWalk(t *testing.T) {
	r, _, books, _ := shelfSeed(t, 7)
	s, _ := r.CreateShelf(ctx, "us_a", "Walk")
	for i := len(books) - 1; i >= 0; i-- { // shelf order is the reverse of creation
		if err := r.AddToShelf(ctx, "us_a", s.ID, books[i].ID); err != nil {
			t.Fatal(err)
		}
	}
	got := shelfIDs(t, r, allScope, "us_a", s.ID)
	if strings.Join(got, ",") != "B6,B5,B4,B3,B2,B1,B0" {
		t.Fatalf("walk = %v", got)
	}
	p, _ := r.ShelfBooks(ctx, allScope, "us_a", s.ID, "", 3)
	if p.Total != 7 || len(p.Books) != 3 || p.Next == "" {
		t.Fatalf("first page = %+v", p)
	}
	for _, bad := range []string{"!!!", "e30", "bm90LWpzb24"} {
		if _, err := r.ShelfBooks(ctx, allScope, "us_a", s.ID, bad, 3); !errors.Is(err, library.ErrInvalid) {
			t.Errorf("cursor %q = %v, want ErrInvalid", bad, err)
		}
	}
	// Limits: 0 is the default, above the maximum is clamped.
	if p, err := r.ShelfBooks(ctx, allScope, "us_a", s.ID, "", 1000); err != nil || len(p.Books) != 7 {
		t.Fatalf("big limit = %+v, %v", p, err)
	}
}

func TestAddIdempotentAndOrder(t *testing.T) {
	r, _, books, _ := shelfSeed(t, 3)
	s, _ := r.CreateShelf(ctx, "us_a", "Order")
	for _, i := range []int{2, 0, 2, 1, 0} {
		if err := r.AddToShelf(ctx, "us_a", s.ID, books[i].ID); err != nil {
			t.Fatal(err)
		}
	}
	if got := shelfIDs(t, r, allScope, "us_a", s.ID); strings.Join(got, ",") != "B2,B0,B1" {
		t.Fatalf("order = %v", got)
	}
	if err := r.AddToShelf(ctx, "us_a", s.ID, "bk_unknown"); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("unknown book = %v", err)
	}
	if err := r.RemoveFromShelf(ctx, "us_a", s.ID, books[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := r.RemoveFromShelf(ctx, "us_a", s.ID, books[0].ID); err != nil {
		t.Fatalf("second remove: %v", err)
	}
	if err := r.AddToShelf(ctx, "us_a", s.ID, books[0].ID); err != nil {
		t.Fatal(err)
	}
	if got := shelfIDs(t, r, allScope, "us_a", s.ID); strings.Join(got, ",") != "B2,B1,B0" {
		t.Fatalf("order after re-add = %v", got)
	}
}

func TestMoveSkipsHiddenNeighbours(t *testing.T) {
	r, _, books, _ := shelfSeed(t, 4) // B0..B3
	s, _ := r.CreateShelf(ctx, "us_a", "Move")
	for _, b := range books {
		_ = r.AddToShelf(ctx, "us_a", s.ID, b.ID)
	}
	sc := library.Scope{Books: share.Filter{SQL: "b.id <> ?", Args: []any{books[1].ID}}, Libraries: share.Filter{SQL: "1=1"}}
	// B2 up steps over hidden B1 and swaps with B0.
	if err := r.MoveInShelf(ctx, sc, "us_a", s.ID, books[2].ID, -1); err != nil {
		t.Fatal(err)
	}
	if got := shelfIDs(t, r, allScope, "us_a", s.ID); strings.Join(got, ",") != "B2,B1,B0,B3" {
		t.Fatalf("after up = %v", got)
	}
	// At the top and at the bottom nothing happens.
	if err := r.MoveInShelf(ctx, sc, "us_a", s.ID, books[2].ID, -1); err != nil {
		t.Fatal(err)
	}
	if err := r.MoveInShelf(ctx, sc, "us_a", s.ID, books[3].ID, 1); err != nil {
		t.Fatal(err)
	}
	if got := shelfIDs(t, r, allScope, "us_a", s.ID); strings.Join(got, ",") != "B2,B1,B0,B3" {
		t.Fatalf("after end moves = %v", got)
	}
	if err := r.MoveInShelf(ctx, allScope, "us_a", s.ID, books[0].ID, 1); err != nil {
		t.Fatal(err)
	}
	if got := shelfIDs(t, r, allScope, "us_a", s.ID); strings.Join(got, ",") != "B2,B1,B3,B0" {
		t.Fatalf("after down = %v", got)
	}
	if err := r.MoveInShelf(ctx, allScope, "us_a", s.ID, books[0].ID, 2); !errors.Is(err, library.ErrInvalid) {
		t.Fatalf("delta 2 = %v", err)
	}
	if err := r.MoveInShelf(ctx, allScope, "us_a", s.ID, "bk_none", 1); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("not on shelf = %v", err)
	}
}

func TestShelfLimits(t *testing.T) {
	r, _, books, _ := shelfSeed(t, 3)
	defer library.SetShelfLimits(2, 2)()
	a, err := r.CreateShelf(ctx, "us_a", "One")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateShelf(ctx, "us_a", "Two"); err != nil {
		t.Fatal(err)
	}
	if _, err := r.CreateShelf(ctx, "us_a", "Three"); !errors.Is(err, library.ErrLimit) {
		t.Fatalf("third shelf = %v", err)
	}
	if _, err := r.CreateShelf(ctx, "us_b", "One"); err != nil {
		t.Fatalf("limit is per person: %v", err)
	}
	for _, b := range books[:2] {
		if err := r.AddToShelf(ctx, "us_a", a.ID, b.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := r.AddToShelf(ctx, "us_a", a.ID, books[2].ID); !errors.Is(err, library.ErrLimit) {
		t.Fatalf("third book = %v", err)
	}
	if err := r.AddToShelf(ctx, "us_a", a.ID, books[0].ID); err != nil {
		t.Fatalf("re-adding a book on a full shelf: %v", err)
	}
}

func TestShelfCascade(t *testing.T) {
	r, st, books, _ := shelfSeed(t, 2)
	s, _ := r.CreateShelf(ctx, "us_a", "Gone")
	for _, b := range books {
		_ = r.AddToShelf(ctx, "us_a", s.ID, b.ID)
	}
	count := func(q string) int {
		var n int
		if err := st.Reader().QueryRow(q).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	// Deleting a book (its audio file rows first) removes its shelf rows.
	if _, err := st.Writer().Exec(`DELETE FROM audio_files WHERE book_id = ?`, books[0].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Writer().Exec(`DELETE FROM books WHERE id = ?`, books[0].ID); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT COUNT(*) FROM shelf_items`); n != 1 {
		t.Fatalf("items after book delete = %d", n)
	}
	// Deleting the owner removes the shelf and the rest.
	if _, err := st.Writer().Exec(`DELETE FROM users WHERE id = 'us_a'`); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT COUNT(*) FROM shelves`) + count(`SELECT COUNT(*) FROM shelf_items`); n != 0 {
		t.Fatalf("rows after owner delete = %d", n)
	}
}
