package upload

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

// ownFolderEnv is an env whose uploads are built in the person's own folder; the person is "Ana".
func ownFolderEnv(t *testing.T, name string) *env {
	t.Helper()
	e := newEnv(t, func(c *Config) {
		c.MayPlace = func(context.Context, string, string) Placement { return PlaceOwnFolder }
	})
	e.setName(name)
	return e
}

func (e *env) setName(name string) {
	e.t.Helper()
	if _, err := e.st.Writer().Exec(`INSERT INTO users(id, username, display_name, role, created_at) VALUES (?, 'ana', ?, 'member', 1)
  ON CONFLICT(id) DO UPDATE SET display_name = excluded.display_name`, me, name); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.st.Reader().QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func tagged(t *testing.T, title string) book {
	tags := map[string]string{"album": title, "artist": "Ann Author"}
	return book{"B/01.mp3": mp3(t, tags), "B/02.mp3": mp3(t, tags)}
}

func TestContributorPlacesInOwnFolder(t *testing.T) {
	t.Run("created once, reused, kept when the name changes", func(t *testing.T) {
		e := ownFolderEnv(t, "Ana")
		e.mustPlace(e.send("B", tagged(t, "First")).ID)
		want := []string{"Added by Ana/", "Added by Ana/Ann Author/", "Added by Ana/Ann Author/First/", "Added by Ana/Ann Author/First/01.mp3", "Added by Ana/Ann Author/First/02.mp3"}
		if got := tree(t, e.root); !reflect.DeepEqual(got, want) {
			t.Fatalf("tree = %v", got)
		}
		e.setName("Bea")
		e.mustPlace(e.send("B", tagged(t, "Second")).ID)
		for _, p := range tree(t, e.root) {
			if strings.HasPrefix(p, "Added by Bea") {
				t.Fatalf("the folder was renamed or duplicated: %s", p)
			}
		}
		if _, err := os.Stat(filepath.Join(e.root, "Added by Ana", "Ann Author", "Second")); err != nil {
			t.Fatalf("second book is not in the first folder: %v", err)
		}
		if n := e.count(`SELECT count(*) FROM contributor_folders`); n != 1 {
			t.Errorf("contributor_folders = %d, want 1", n)
		}
		var kind, state string
		if err := e.st.Reader().QueryRow(`SELECT kind, state FROM upload_paths WHERE upload_id = ? AND path = ?`, personUploadID(me),
			filepath.Join(e.root, "Added by Ana")).Scan(&kind, &state); err != nil || kind != "dir" || state != "present" {
			t.Errorf("ledger row = %q %q, %v", kind, state, err)
		}
	})
	t.Run("an existing directory is never adopted", func(t *testing.T) {
		e := ownFolderEnv(t, "Ana")
		mk(t, e.root, "Added by Ana/theirs.txt")
		e.mustPlace(e.send("B", tagged(t, "First")).ID)
		if _, err := os.Stat(filepath.Join(e.root, "Added by Ana (2)", "Ann Author", "First", "01.mp3")); err != nil {
			t.Fatalf("not placed in the (2) folder: %v", err)
		}
		if _, err := os.Stat(filepath.Join(e.root, "Added by Ana", "Ann Author")); err == nil {
			t.Error("the existing folder was used")
		}
	})
	t.Run("a link is never used", func(t *testing.T) {
		e := ownFolderEnv(t, "Ana")
		outside := t.TempDir()
		if err := os.Symlink(outside, filepath.Join(e.root, "Added by Ana")); err != nil {
			t.Skip("no symlinks here")
		}
		e.mustPlace(e.send("B", tagged(t, "First")).ID)
		if got, _ := os.ReadDir(outside); len(got) != 0 {
			t.Errorf("something was written through the link: %v", got)
		}
		if _, err := os.Stat(filepath.Join(e.root, "Added by Ana (2)", "Ann Author")); err != nil {
			t.Errorf("not placed in the (2) folder: %v", err)
		}
	})
	t.Run("a stored link is refused, a stored folder that is gone is made again", func(t *testing.T) {
		e := ownFolderEnv(t, "Ana")
		e.mustPlace(e.send("B", tagged(t, "First")).ID)
		person := filepath.Join(e.root, "Added by Ana")
		if err := os.RemoveAll(person); err != nil {
			t.Fatal(err)
		}
		// What was inside is gone too, so its ledger rows end; the person's own row stays.
		if _, err := e.st.Writer().Exec(`UPDATE upload_paths SET state = 'removed' WHERE upload_id <> ?`, personUploadID(me)); err != nil {
			t.Fatal(err)
		}
		e.mustPlace(e.send("B", tagged(t, "Again")).ID)
		if _, err := os.Stat(filepath.Join(person, "Ann Author", "Again", "01.mp3")); err != nil {
			t.Fatalf("not placed in the recreated folder: %v", err)
		}
		if err := os.RemoveAll(person); err != nil {
			t.Fatal(err)
		}
		outside := t.TempDir()
		if err := os.Symlink(outside, person); err != nil {
			t.Skip("no symlinks here")
		}
		v := e.send("B", tagged(t, "Third"))
		if _, err := e.place(v.ID); err == nil {
			t.Fatal("a link where the folder was got used")
		}
		if got, _ := os.ReadDir(outside); len(got) != 0 {
			t.Errorf("something was written through the link: %v", got)
		}
	})
	t.Run("names are sanitised", func(t *testing.T) {
		for _, name := range []string{"../.Evil/x", ".hidden", "CON", "a/b\\c", strings.Repeat("é", 200)} {
			e := ownFolderEnv(t, name)
			e.mustPlace(e.send("B", tagged(t, "First")).ID)
			var p string
			if err := e.st.Reader().QueryRow(`SELECT path FROM contributor_folders`).Scan(&p); err != nil {
				t.Fatal(err)
			}
			rel, err := filepath.Rel(e.root, p)
			if err != nil || strings.ContainsAny(rel, `/\`) || strings.Contains(rel, "..") || !strings.HasPrefix(rel, "Added by ") || len(rel) > maxNameBytes {
				t.Errorf("%q became %q (%v)", name, rel, err)
			}
			if got := tree(t, e.root); len(got) == 0 || !strings.HasPrefix(got[0], "Added by ") {
				t.Errorf("%q: tree %v", name, got)
			}
		}
	})
	t.Run("the top of the folder for owner and admin", func(t *testing.T) {
		e := newEnv(t, func(c *Config) {
			c.MayPlace = func(context.Context, string, string) Placement { return PlaceTop }
		})
		e.mustPlace(e.send("B", tagged(t, "First")).ID)
		if got := tree(t, e.root); !reflect.DeepEqual(got[:2], []string{"Ann Author/", "Ann Author/First/"}) {
			t.Errorf("tree = %v", got)
		}
		if n := e.count(`SELECT count(*) FROM contributor_folders`); n != 0 {
			t.Errorf("contributor_folders = %d", n)
		}
	})
}

func TestPlaceRefusedNotAllowed(t *testing.T) {
	e := newEnv(t, func(c *Config) {
		c.MayPlace = func(context.Context, string, string) Placement { return PlaceRefused }
	})
	v := e.send("B", tagged(t, "First"))
	if _, err := os.Stat(e.svc.stagingDir(v.ID)); err != nil {
		t.Fatalf("no staged bytes to begin with: %v", err)
	}
	res := e.mustPlace(v.ID)
	if res.State != StateRefused {
		t.Fatalf("result = %+v", res)
	}
	up := e.upload(v.ID)
	if up.State != StateRefused || up.ErrorCode != "not_allowed" {
		t.Errorf("upload = %s / %s", up.State, up.ErrorCode)
	}
	if got := tree(t, e.root); len(got) != 0 {
		t.Errorf("something reached the library: %v", got)
	}
	if _, err := os.Stat(e.svc.stagingDir(v.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("staging is still there: %v", err)
	}
	if n := e.count(`SELECT count(*) FROM upload_books`) + e.count(`SELECT count(*) FROM upload_paths`) + e.count(`SELECT count(*) FROM contributor_folders`); n != 0 {
		t.Errorf("rows were written: %d", n)
	}
}

func TestUploadBooksRow(t *testing.T) {
	e := newEnv(t, nil)
	v := e.send("B", tagged(t, "First"))
	e.mustPlace(v.ID)
	var root, upload, lib, by string
	if err := e.st.Reader().QueryRow(`SELECT root_path, upload_id, library_id, added_by FROM upload_books`).Scan(&root, &upload, &lib, &by); err != nil {
		t.Fatal(err)
	}
	if root != filepath.Join(e.root, "Ann Author", "First") || upload != v.ID || lib != e.lib.ID || by != me {
		t.Errorf("row = %q %q %q %q", root, upload, lib, by)
	}
	// Placing again (a retry after the publish) leaves one row.
	e.mustPlace(v.ID)
	if n := e.count(`SELECT count(*) FROM upload_books`); n != 1 {
		t.Errorf("rows = %d", n)
	}
	// The row outlives the upload row.
	if _, err := e.st.Writer().Exec(`DELETE FROM uploads WHERE id = ?`, v.ID); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM upload_books`); n != 1 {
		t.Errorf("rows after the upload row went = %d", n)
	}
}

func TestRemovePlacedByRefusesOthersFile(t *testing.T) {
	for name, mutate := range map[string]string{
		"another person":           `UPDATE upload_books SET added_by = 'us_other'`,
		"a file outside the roots": `UPDATE upload_books SET root_path = root_path || '/nested'`,
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, nil)
			_, bookID := placedBook(t, e)
			if !e.svc.RemovableBy(bg, bookID, me) {
				t.Fatal("the person's own book is not removable by them")
			}
			if _, err := e.st.Writer().Exec(mutate); err != nil {
				t.Fatal(err)
			}
			before := tree(t, e.root)
			if e.svc.RemovableBy(bg, bookID, me) {
				t.Error("RemovableBy said yes")
			}
			_, err := e.svc.Remove(bg, me, bookID, me)
			var ue *Error
			if !errors.As(err, &ue) || ue.Status != 409 || ue.Code != "not_theirs" {
				t.Fatalf("err = %v", err)
			}
			if got := tree(t, e.root); !reflect.DeepEqual(got, before) {
				t.Errorf("files changed: %v", got)
			}
			if booksCount(t, e) != 1 {
				t.Error("the book row went")
			}
			// An admin (placedBy empty) still removes it.
			if _, err := e.svc.Remove(bg, "us_admin", bookID, ""); err != nil {
				t.Errorf("admin remove: %v", err)
			}
		})
	}
}

func TestRemoveDropsUploadBooks(t *testing.T) {
	e := newEnv(t, nil)
	_, bookID := placedBook(t, e)
	if _, err := e.svc.Remove(bg, me, bookID, me); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT count(*) FROM upload_books`); n != 0 {
		t.Errorf("upload_books = %d after the remove", n)
	}
}

// placeAndImport sends, places and imports one book and returns its id.
func placeAndImport(t *testing.T, e *env, title string) string {
	t.Helper()
	e.mustPlace(e.send("B", tagged(t, title)).ID)
	var p scan.ScanPayload
	q := e.enq.of(scan.KindScan)
	if err := json.Unmarshal(q[len(q)-1].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if _, err := e.scanner().ScanSubtree(bg, p.LibraryID, p.FolderID, p.Subdir, nil); err != nil {
		t.Fatal(err)
	}
	var id string
	if err := e.st.Reader().QueryRow(`SELECT id FROM books WHERE title = ?`, title).Scan(&id); err != nil {
		t.Fatalf("%s: %v", title, err)
	}
	return id
}

func TestRemoveAll(t *testing.T) {
	e := ownFolderEnv(t, "Ana")
	one, two, three := placeAndImport(t, e, "One"), placeAndImport(t, e, "Two"), placeAndImport(t, e, "Three")
	// Somebody else's book in the same library is never touched.
	mk(t, e.root, "Plain/Other/x.txt")
	// Book three changed on disk since it was placed.
	track := filepath.Join(e.root, "Added by Ana", "Ann Author", "Three", "01.mp3")
	data, _ := os.ReadFile(track)
	data[len(data)-1] ^= 0xff
	data[0] = 'T'
	future := time.Now().Add(time.Hour)
	if err := os.WriteFile(track, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(track, future, future); err != nil {
		t.Fatal(err)
	}

	payload, _ := json.Marshal(RemoveAllPayload{UserID: me, By: "us_admin"})
	out, err := e.svc.handleRemoveAll(bg, &jobs.Task{Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	res := out.(RemoveAllResult)
	if res.Removed != 2 || len(res.Kept) != 1 || res.Kept[0].BookID != three || res.Kept[0].Reason != KeptChanged || res.Kept[0].Title != "Three" {
		t.Fatalf("result = %+v", res)
	}
	for _, id := range []string{one, two} {
		if _, err := e.repo.Book(bg, id); err == nil {
			t.Errorf("book %s is still there", id)
		}
	}
	if _, err := e.repo.Book(bg, three); err != nil {
		t.Errorf("the changed book went: %v", err)
	}
	if _, err := os.Stat(track); err != nil {
		t.Errorf("the changed file went: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "Plain", "Other", "x.txt")); err != nil {
		t.Errorf("another file went: %v", err)
	}
	if _, err := os.Stat(filepath.Join(e.root, "Added by Ana")); err != nil {
		t.Errorf("the person's folder went: %v", err)
	}
}
