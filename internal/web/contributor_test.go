package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/upload"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// contributorApp is an uploadApp with two libraries that take uploads (A, from newUploadApp, and B) and people
// of every kind (decision 0029): c1 holds a library grant on A, c2 has no grant, cAll holds a whole-server
// grant, cBook only a book grant on A, and member and guest come from the uploadApp.
type contributorApp struct {
	*uploadApp
	libB                library.Library
	folderB             library.Folder
	c1, c2, cAll, cBook *http.Cookie
	id                  map[*http.Cookie]string
}

func newContributorApp(t *testing.T) *contributorApp {
	t.Helper()
	u := newUploadApp(t)
	c := &contributorApp{uploadApp: u, id: map[*http.Cookie]string{}}
	c.libB, c.folderB = librarytest.NewLibrary(t, u.a.lib, "Second library", t.TempDir())
	if err := u.a.lib.SetUploadFolder(upCtx, c.libB.ID, c.folderB.ID); err != nil {
		t.Fatal(err)
	}
	for _, p := range []**http.Cookie{&c.c1, &c.c2, &c.cAll, &c.cBook} {
		ck := u.a.as(t, users.RoleMember)
		id := sweepUserID(t, u.a, ck)
		if _, err := u.a.st.Writer().Exec(`UPDATE users SET adds_books = 1 WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
		*p, c.id[ck] = ck, id
	}
	grant := func(ck *http.Cookie, scope, col, target string) {
		q := `INSERT INTO grants(id, user_id, scope, ` + col + `, can_download, can_read, created_at, updated_at) VALUES (?, ?, ?, ?, 0, 0, 1, 1)`
		if col == "" {
			q = `INSERT INTO grants(id, user_id, scope, can_download, can_read, created_at, updated_at) VALUES (?, ?, ?, 0, 0, 1, 1)`
		}
		args := []any{"gr_" + c.id[ck] + scope, c.id[ck], scope}
		if col != "" {
			args = append(args, target)
		}
		if _, err := u.a.st.Writer().Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	grant(c.c1, "library", "library_id", u.libID)
	grant(c.cAll, "all", "", "")
	return c
}

func (c *contributorApp) createIn(t *testing.T, libID string, ck *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(upload.Manifest{Label: "Book", Files: []upload.FileIn{{Path: "a.mp3", Size: 10}}})
	return c.req(t, http.MethodPost, "/libraries/"+libID+"/uploads", "application/json", b, ck, true)
}

// TestContributorUploadRoutes: only a contributor with an active whole-library or whole-server grant adds books,
// and only to a library the grant covers; every other kind of person gets 404.
func TestContributorUploadRoutes(t *testing.T) {
	c := newContributorApp(t)
	for _, tc := range []struct {
		name string
		ck   *http.Cookie
		lib  string
		want int
	}{
		{"owner A", c.owner, c.libID, http.StatusCreated},
		{"admin B", c.admin, c.libB.ID, http.StatusCreated},
		{"contributor with a library grant, that library", c.c1, c.libID, http.StatusCreated},
		{"contributor with a library grant, another library", c.c1, c.libB.ID, http.StatusNotFound},
		{"contributor with a whole-server grant A", c.cAll, c.libID, http.StatusCreated},
		{"contributor with a whole-server grant B", c.cAll, c.libB.ID, http.StatusCreated},
		{"contributor without a grant", c.c2, c.libID, http.StatusNotFound},
		{"contributor with a book grant", c.cBook, c.libID, http.StatusNotFound},
		{"member", c.member, c.libID, http.StatusNotFound},
		{"guest", c.guest, c.libID, http.StatusNotFound},
	} {
		if rec := c.createIn(t, tc.lib, tc.ck); rec.Code != tc.want {
			t.Errorf("%s: %d, want %d (%s)", tc.name, rec.Code, tc.want, rec.Body.String())
		}
	}
	// Another contributor cannot touch c1's upload, even one with the same grant.
	rec := c.createIn(t, c.libID, c.c1)
	var v upload.View
	_ = json.Unmarshal(rec.Body.Bytes(), &v)
	for name, ck := range map[string]*http.Cookie{"another contributor": c.cAll, "member": c.member, "guest": c.guest} {
		for _, path := range []string{"/uploads/" + v.ID, "/uploads/" + v.ID + "/finish", "/uploads/" + v.ID + "/cancel"} {
			method := http.MethodGet
			if strings.HasSuffix(path, "finish") || strings.HasSuffix(path, "cancel") {
				method = http.MethodPost
			}
			if rec := c.req(t, method, path, "", nil, ck, true); rec.Code != http.StatusNotFound {
				t.Errorf("%s %s %s: %d", name, method, path, rec.Code)
			}
		}
	}
	// Ungranted mid-upload: the next chunk and the finish get 404.
	if _, err := c.a.st.Writer().Exec(`DELETE FROM grants WHERE user_id = ?`, c.id[c.c1]); err != nil {
		t.Fatal(err)
	}
	chunk := "/uploads/" + v.ID + "/files/" + v.Files[0].ID + "?offset=0"
	if rec := c.req(t, http.MethodPut, chunk, "application/octet-stream", []byte("0123456789"), c.c1, true); rec.Code != http.StatusNotFound {
		t.Errorf("chunk after the grant ended: %d", rec.Code)
	}
	if rec := c.req(t, http.MethodPost, "/uploads/"+v.ID+"/finish", "", nil, c.c1, true); rec.Code != http.StatusNotFound {
		t.Errorf("finish after the grant ended: %d", rec.Code)
	}
	// Turned into a plain member (flag off): same.
	if _, err := c.a.st.Writer().Exec(`UPDATE users SET adds_books = 0 WHERE id = ?`, c.id[c.cAll]); err != nil {
		t.Fatal(err)
	}
	if rec := c.createIn(t, c.libID, c.cAll); rec.Code != http.StatusNotFound {
		t.Errorf("demoted contributor: %d", rec.Code)
	}
}

// TestContributorAddBooksPage: the Add books page and its library chips list only libraries the person may add to.
func TestContributorAddBooksPage(t *testing.T) {
	c := newContributorApp(t)
	get := func(path string, ck *http.Cookie) *httptest.ResponseRecorder {
		return c.a.do(http.MethodGet, path, nil, nil, ck)
	}
	rec := get("/libraries/"+c.libID+"/add", c.c1)
	if rec.Code != http.StatusOK {
		t.Fatalf("c1 add page: %d", rec.Code)
	}
	if body := rec.Body.String(); strings.Contains(body, "Second library") {
		t.Error("the chips name a library the contributor may not add to")
	}
	if rec := get("/libraries/"+c.libB.ID+"/add", c.c1); rec.Code != http.StatusNotFound {
		t.Errorf("c1 add page of B: %d", rec.Code)
	}
	if body := get("/libraries/"+c.libID+"/add", c.cAll).Body.String(); !strings.Contains(body, "Second library") {
		t.Error("a whole-server grant should list both libraries")
	}
	for name, ck := range map[string]*http.Cookie{"c2": c.c2, "cBook": c.cBook, "member": c.member, "guest": c.guest} {
		if rec := get("/libraries/"+c.libID+"/add", ck); rec.Code != http.StatusNotFound {
			t.Errorf("%s add page: %d", name, rec.Code)
		}
	}
	if body := get("/libraries/"+c.libID+"/add", c.owner).Body.String(); !strings.Contains(body, "Second library") {
		t.Error("the owner's chips lost a library")
	}
}

// addedBook adds a book to the folder, with its files in the ledger as Tendshelf's, and, when by is not empty, an
// upload_books row for it.
func (c *contributorApp) addedBook(t *testing.T, f library.Folder, title, by string) library.Book {
	t.Helper()
	b := librarytest.AddBook(t, c.a.lib, f, librarytest.BookSpec{Title: title, Files: 1})
	files, err := c.a.lib.AudioFiles(upCtx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, af := range files {
		if _, err := c.a.st.Writer().Exec(`INSERT INTO upload_paths(id, upload_id, library_folder_id, path, kind, state, size, mtime_ns, created_at, updated_at)
  VALUES (?, 'up_x', ?, ?, 'file', 'present', 1, 1, 1, 1)`, "upp_"+b.ID+string(rune('a'+i)), f.ID, af.Path); err != nil {
			t.Fatal(err)
		}
	}
	if by != "" {
		if _, err := c.a.st.Writer().Exec(`INSERT INTO upload_books(root_path, upload_id, library_id, added_by, created_at) VALUES (?, 'up_x', ?, ?, 1)`,
			b.RootPath, f.LibraryID, by); err != nil {
			t.Fatal(err)
		}
	}
	return b
}

// TestContributorBookRights: a contributor sees, opens and removes their own books (even in a library nobody
// granted them), and gets 404 for everyone else's, for the remove page, and for the books of an admin.
func TestContributorBookRights(t *testing.T) {
	c := newContributorApp(t)
	own := c.addedBook(t, c.folderB, "Zephyr Own", c.id[c.c1])           // A library c1 has no grant on
	theirs := c.addedBook(t, c.folderB, "Zephyr Theirs", c.id[c.c2])     // another contributor's, same library
	adminBook := c.addedBook(t, c.folder, "Zephyr Admin", "us_admin_id") // an admin's book in c1's granted library
	plain := librarytest.AddBook(t, c.a.lib, c.folder, librarytest.BookSpec{Title: "Zephyr Plain"})
	get := func(path string, ck *http.Cookie) int { return c.a.do(http.MethodGet, path, nil, nil, ck).Code }

	for _, tc := range []struct {
		name string
		ck   *http.Cookie
		book library.Book
		page int // GET /books/{id}
		rm   int // GET /books/{id}/remove
	}{
		{"c1 own book in an ungranted library", c.c1, own, 200, 200},
		{"c1 another contributor's book", c.c1, theirs, 404, 404},
		{"c1 an admin's book in a granted library", c.c1, adminBook, 200, 404},
		{"c1 a scanned book in a granted library", c.c1, plain, 200, 404},
		{"c2 c1's book", c.c2, own, 404, 404},
		{"c2 own book", c.c2, theirs, 200, 200},
		{"member c1's book", c.member, own, 404, 404},
		{"guest c1's book", c.guest, own, 404, 404},
		{"owner c1's book", c.owner, own, 200, 200},
		{"admin c1's book", c.admin, own, 200, 200},
	} {
		if got := get("/books/"+tc.book.ID, tc.ck); got != tc.page {
			t.Errorf("%s: page %d, want %d", tc.name, got, tc.page)
		}
		if got := get("/books/"+tc.book.ID+"/remove", tc.ck); got != tc.rm {
			t.Errorf("%s: remove page %d, want %d", tc.name, got, tc.rm)
		}
	}
	post := func(ck *http.Cookie, id string) int {
		return c.a.do(http.MethodPost, "/books/"+id+"/remove", url.Values{"_csrf": {c.a.tokenFor(t, ck)}}, nil, ck).Code
	}
	// Refusals first, nothing is removed by any of them.
	for name, tc := range map[string]struct {
		ck *http.Cookie
		id string
	}{
		"c1 removes another contributor's": {c.c1, theirs.ID}, "c1 removes an admin's": {c.c1, adminBook.ID}, "c1 removes a scanned book": {c.c1, plain.ID},
		"c2 removes c1's": {c.c2, own.ID}, "member removes": {c.member, own.ID}, "guest removes": {c.guest, own.ID},
	} {
		if got := post(tc.ck, tc.id); got != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", name, got)
		}
	}
	if n := c.a.count(t, `SELECT count(*) FROM books`); n != 4 {
		t.Fatalf("books = %d after the refusals, want 4", n)
	}
	if got := post(c.c1, own.ID); got != http.StatusSeeOther && got != http.StatusOK {
		t.Errorf("c1 removes their own: %d", got)
	}
	if n := c.a.count(t, `SELECT count(*) FROM books WHERE id = ?`, own.ID); n != 0 {
		t.Error("their own book is still there")
	}
	// An admin (placedBy empty) removes a contributor's book.
	if got := post(c.admin, theirs.ID); got != http.StatusSeeOther && got != http.StatusOK {
		t.Errorf("admin removes a contributor's: %d", got)
	}
	if n := c.a.count(t, `SELECT count(*) FROM books WHERE id = ?`, theirs.ID); n != 0 {
		t.Error("the admin's removal left the book")
	}
}

// TestContributorAddedByFilter: ?added=<id> narrows the library to that person's books for an owner or admin;
// for anyone else another person's id is dropped, and their own id narrows to their books.
func TestContributorAddedByFilter(t *testing.T) {
	c := newContributorApp(t)
	c.addedBook(t, c.folder, "Quillon Mine", c.id[c.c1])
	c.addedBook(t, c.folder, "Quillon Other", c.id[c.cAll])
	c.addedBook(t, c.folder, "Quillon Scanned", "")
	list := func(ck *http.Cookie, added string) (mine, other, scanned bool) {
		rec := c.a.do(http.MethodGet, "/libraries/"+c.libID+"?added="+added, nil, nil, ck)
		if rec.Code != http.StatusOK {
			t.Fatalf("list as %v: %d", added, rec.Code)
		}
		b := rec.Body.String()
		return strings.Contains(b, "Quillon Mine"), strings.Contains(b, "Quillon Other"), strings.Contains(b, "Quillon Scanned")
	}
	for _, tc := range []struct {
		name                 string
		ck                   *http.Cookie
		added                string
		mine, other, scanned bool
	}{
		{"owner, c1's id", c.owner, c.id[c.c1], true, false, false},
		{"admin, cAll's id", c.admin, c.id[c.cAll], false, true, false},
		{"owner, nobody's id", c.owner, "usr_nobody", false, false, false},
		{"c1, own id", c.c1, c.id[c.c1], true, false, false},
		{"c1, another person's id is ignored", c.c1, c.id[c.cAll], true, true, true},
		{"c1, no filter", c.c1, "", true, true, true},
	} {
		mine, other, scanned := list(tc.ck, tc.added)
		if mine != tc.mine || other != tc.other || scanned != tc.scanned {
			t.Errorf("%s: mine %v other %v scanned %v, want %v %v %v", tc.name, mine, other, scanned, tc.mine, tc.other, tc.scanned)
		}
	}
}

// TestContributorWriteSheet: "Write details into the file" belongs to owner and admin; a contributor gets 404
// for the sheet and the write routes even on a book they added.
func TestContributorWriteSheet(t *testing.T) {
	l := newWriteLab(t, 1, false)
	ck := l.a.as(t, users.RoleMember)
	id := sweepUserID(t, l.a, ck)
	if _, err := l.a.st.Writer().Exec(`UPDATE users SET adds_books = 1 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := l.a.st.Writer().Exec(`INSERT INTO upload_books(root_path, upload_id, library_id, added_by, created_at) VALUES (?, 'up_x', ?, ?, 1)`,
		l.book.RootPath, l.book.LibraryID, id); err != nil {
		t.Fatal(err)
	}
	if rec := l.get(l.owner, l.sheet(), false); rec.Code != http.StatusOK {
		t.Fatalf("owner sheet: %d", rec.Code)
	}
	if rec := l.get(l.admin, l.sheet(), false); rec.Code != http.StatusOK {
		t.Errorf("admin sheet: %d", rec.Code)
	}
	if rec := l.get(ck, l.sheet(), false); rec.Code != http.StatusNotFound {
		t.Errorf("contributor sheet: %d, want 404", rec.Code)
	}
	for _, p := range []string{"/write", "/write/confirm", "/write/undo"} {
		if rec := l.post(t, ck, "/books/"+l.book.ID+p); rec.Code != http.StatusNotFound {
			t.Errorf("contributor POST %s: %d, want 404", p, rec.Code)
		}
	}
}
