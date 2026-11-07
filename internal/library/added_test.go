package library_test

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// TestAddedBy covers the who-added-it reads of decision 0029: matching by path (a nested root counts, a
// sibling with the same name prefix does not), the list filter, the facet and the contributor folders.
func TestAddedBy(t *testing.T) {
	ctx := context.Background()
	r, st := newRepo(t)
	l, f := librarytest.NewLibrary(t, r, "L", t.TempDir())
	mine := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Mine"})
	other := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Other"})
	plain := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Plain"})
	for _, q := range []string{
		`INSERT INTO users(id, username, display_name, role, created_at) VALUES ('u1', 'ana', 'Ana', 'member', 1), ('u2', 'bo', '', 'member', 1)`,
	} {
		if _, err := st.Writer().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	add := func(root, by string) {
		if _, err := st.Writer().Exec(`INSERT INTO upload_books(root_path, upload_id, library_id, added_by, created_at) VALUES (?, 'up', ?, ?, 1)`, root, l.ID, by); err != nil {
			t.Fatal(err)
		}
	}
	add(mine.RootPath, "u1")
	add(other.RootPath, "u2")
	// A prefix sibling of Mine's root must not match: "…/mine-1x" is not inside "…/mine-1".
	sib := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Mine"}) // another root, same slug stem

	books, err := r.BooksAddedBy(ctx, "u1")
	if err != nil || len(books) != 1 || books[0].ID != mine.ID {
		t.Fatalf("BooksAddedBy = %+v, %v", books, err)
	}
	if n, err := r.CountAddedBy(ctx, "u1"); err != nil || n != 1 {
		t.Errorf("CountAddedBy = %d, %v", n, err)
	}
	for id, want := range map[string]string{mine.ID: "u1", other.ID: "u2", plain.ID: "", sib.ID: "", "bk_none": ""} {
		if got, err := r.AddedBy(ctx, id); err != nil || got != want {
			t.Errorf("AddedBy(%s) = %q, %v, want %q", id, got, err, want)
		}
	}

	all := library.Scope{Books: share.Filter{SQL: "1=1"}, Libraries: share.Filter{SQL: "1=1"}}
	page, err := r.ListBooks(ctx, all, library.ListQuery{AddedBy: "u2"})
	if err != nil || len(page.Books) != 1 || page.Books[0].ID != other.ID {
		t.Fatalf("ListBooks AddedBy = %+v, %v", page.Books, err)
	}
	if page, _ = r.ListBooks(ctx, all, library.ListQuery{AddedBy: "u_nobody"}); len(page.Books) != 0 {
		t.Errorf("an unknown person matched %d books", len(page.Books))
	}
	facet, err := r.AddedByFacet(ctx, all, library.ListQuery{}, 0)
	if err != nil || len(facet) != 2 || facet[0].Name != "Ana" || facet[1].Name != "bo" || facet[0].Count != 1 {
		t.Fatalf("AddedByFacet = %+v, %v", facet, err)
	}
	// A scope that hides Other's book hides its person from the facet.
	hide := library.Scope{Books: share.Filter{SQL: "b.id <> ?", Args: []any{other.ID}}, Libraries: share.Filter{SQL: "1=1"}}
	if facet, _ = r.AddedByFacet(ctx, hide, library.ListQuery{}, 0); len(facet) != 1 || facet[0].Name != "Ana" {
		t.Errorf("hidden person leaked: %+v", facet)
	}

	person := filepath.Join(f.Path, "Added by Ana")
	if _, err := st.Writer().Exec(`INSERT INTO contributor_folders(folder_id, user_id, path, created_at) VALUES (?, 'u1', ?, 1)`, f.ID, person); err != nil {
		t.Fatal(err)
	}
	for root, want := range map[string]string{
		filepath.Join(person, "A", "B"): person,
		person + "x/A":                  "",
		person:                          "",
		filepath.Join(f.Path, "Other"):  "",
	} {
		if got, err := r.ContributorFolderOf(ctx, f.ID, root); err != nil || got != want {
			t.Errorf("ContributorFolderOf(%s) = %q, %v, want %q", root, got, err, want)
		}
	}
	if got, err := r.ContributorFolders(ctx, "u1"); err != nil || !reflect.DeepEqual(got, []string{person}) {
		t.Errorf("ContributorFolders = %v, %v", got, err)
	}
}
