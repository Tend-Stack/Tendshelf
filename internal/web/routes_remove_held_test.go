package web

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/media"
)

// Removing a book Add books placed moves its files into the holding folder (decision 0045 addendum); the owner or an
// admin puts them back from the removal's page.

// addedRealBook is addedBook with real files on disk and a ledger that matches them (size, time and quick hash),
// so the removal really moves them.
func (c *contributorApp) addedRealBook(t *testing.T, f library.Folder, title, by string) (library.Book, []string) {
	t.Helper()
	b := librarytest.AddBook(t, c.a.lib, f, librarytest.BookSpec{Title: title, Files: 2})
	files, err := c.a.lib.AudioFiles(upCtx, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	var paths []string
	for i, af := range files {
		if err := os.MkdirAll(filepath.Dir(af.Path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(af.Path, []byte(fmt.Sprintf("audio %s %d", title, i)), 0o644); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(af.Path)
		if err != nil {
			t.Fatal(err)
		}
		h, err := os.Open(af.Path)
		if err != nil {
			t.Fatal(err)
		}
		quick, _, err := media.QuickHashFile(h)
		h.Close()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.a.st.Writer().Exec(`INSERT INTO upload_paths(id, upload_id, library_folder_id, path, kind, state, size, mtime_ns, quick_hash, created_at, updated_at)
  VALUES (?, 'up_x', ?, ?, 'file', 'present', ?, ?, ?, 1, 1)`, "upp_"+b.ID+string(rune('a'+i)), f.ID, af.Path, fi.Size(), fi.ModTime().UnixNano(), quick); err != nil {
			t.Fatal(err)
		}
		paths = append(paths, af.Path)
	}
	if by != "" {
		if _, err := c.a.st.Writer().Exec(`INSERT INTO upload_books(root_path, upload_id, library_id, added_by, created_at) VALUES (?, 'up_x', ?, ?, 1)`,
			b.RootPath, f.LibraryID, by); err != nil {
			t.Fatal(err)
		}
	}
	return b, paths
}

// POST /books/{id}/remove per role: the owner, an admin and the contributor who added it move the files (one
// removal each); another contributor, a member and a guest get 404 and nothing moves.
func TestRemoveHeldRoleMatrix(t *testing.T) {
	c := newContributorApp(t)
	var location string
	post := func(ck *http.Cookie, id string) int {
		rec := c.a.do(http.MethodPost, "/books/"+id+"/remove", url.Values{"_csrf": {c.a.tokenFor(t, ck)}}, nil, ck)
		location = rec.Header().Get("Location")
		return rec.Code
	}
	type tc struct {
		name     string
		ck       *http.Cookie
		by       string
		wantCode int
		removals int
	}
	for _, x := range []tc{
		{"owner", c.owner, "", http.StatusSeeOther, 1},
		{"admin", c.admin, "", http.StatusSeeOther, 1},
		{"contributor own", c.c1, c.id[c.c1], http.StatusSeeOther, 1},
		{"contributor other", c.c1, c.id[c.c2], http.StatusNotFound, 0},
		{"member", c.member, c.id[c.c1], http.StatusNotFound, 0},
		{"guest", c.guest, c.id[c.c1], http.StatusNotFound, 0},
	} {
		b, paths := c.addedRealBook(t, c.folderB, "Held "+x.name, x.by)
		if got := post(x.ck, b.ID); got != x.wantCode {
			t.Errorf("%s: %d, want %d", x.name, got, x.wantCode)
		}
		if x.wantCode == http.StatusSeeOther {
			var rid string
			_ = c.a.st.Reader().QueryRow(`SELECT id FROM removals WHERE book_id = ?`, b.ID).Scan(&rid)
			want := "/libraries/" + b.LibraryID // a contributor lands where they always did
			if x.by == "" {
				want = "/removals/" + rid // the owner and admins land on the removal, where Put back is
			}
			if location != want {
				t.Errorf("%s: redirected to %q, want %q", x.name, location, want)
			}
		}
		if n := c.a.count(t, `SELECT count(*) FROM removals WHERE book_id = ?`, b.ID); n != x.removals {
			t.Errorf("%s: removals = %d, want %d", x.name, n, x.removals)
		}
		for _, p := range paths {
			_, err := os.Stat(p)
			if moved := os.IsNotExist(err); moved != (x.removals == 1) {
				t.Errorf("%s: file moved = %v", x.name, moved)
			}
		}
	}
}

// A contributor's removal can be undone by the owner or an admin only: the contributor has no page for it, and
// Put back returns the files and gives the contributor their rights over the book again.
func TestRemoveHeldPutBackByAdminOnly(t *testing.T) {
	c := newContributorApp(t)
	b, paths := c.addedRealBook(t, c.folderB, "Held Back", c.id[c.c1])
	if got := c.a.do(http.MethodPost, "/books/"+b.ID+"/remove", url.Values{"_csrf": {c.a.tokenFor(t, c.c1)}}, nil, c.c1).Code; got != http.StatusSeeOther {
		t.Fatalf("remove: %d", got)
	}
	var rid string
	if err := c.a.st.Reader().QueryRow(`SELECT id FROM removals WHERE book_id = ?`, b.ID).Scan(&rid); err != nil {
		t.Fatal(err)
	}
	var source, state string
	if err := c.a.st.Reader().QueryRow(`SELECT source, state FROM removals WHERE id = ?`, rid).Scan(&source, &state); err != nil || source != "upload" || state != "held" {
		t.Fatalf("removal = %s %s %v", source, state, err)
	}
	holding := filepath.Join(c.folderB.Path, ".tendshelf-removed", rid)
	if _, err := os.Stat(holding); err != nil {
		t.Errorf("holding folder: %v", err)
	}
	for _, ck := range []*http.Cookie{c.c1, c.member, c.guest} {
		if got := c.a.do(http.MethodGet, "/removals/"+rid, nil, nil, ck).Code; got != http.StatusNotFound {
			t.Errorf("GET /removals/%s as a non-admin: %d, want 404", rid, got)
		}
		if got := c.a.do(http.MethodPost, "/removals/"+rid+"/restore", url.Values{"_csrf": {c.a.tokenFor(t, ck)}}, nil, ck).Code; got != http.StatusNotFound {
			t.Errorf("restore as a non-admin: %d, want 404", got)
		}
	}
	if got := c.a.do(http.MethodGet, "/removals/"+rid, nil, nil, c.admin).Code; got != http.StatusOK {
		t.Errorf("GET /removals/%s as admin: %d", rid, got)
	}
	rec := c.a.do(http.MethodPost, "/removals/"+rid+"/restore", url.Values{"_csrf": {c.a.tokenFor(t, c.admin)}}, nil, c.admin)
	if rec.Code != http.StatusSeeOther && rec.Code != http.StatusOK {
		t.Fatalf("restore: %d %s", rec.Code, strings.TrimSpace(rec.Body.String()))
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("not back: %v", err)
		}
	}
	if n := c.a.count(t, `SELECT count(*) FROM upload_books WHERE added_by = ?`, c.id[c.c1]); n != 1 {
		t.Errorf("who-added-it rows after Put back = %d, want 1", n)
	}
	if n := c.a.count(t, `SELECT count(*) FROM upload_paths WHERE state = 'present' AND path = ?`, paths[0]); n != 1 {
		t.Errorf("ledger row for the first file present = %d", n)
	}
}
