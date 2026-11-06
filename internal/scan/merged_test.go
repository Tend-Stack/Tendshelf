package scan

import (
	"crypto/sha256"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

// mergedEnv scans two byte-identical copies of one audiobook and merges the second into the first.
func mergedEnv(t *testing.T) (e *env, keepRoot, dropRoot string) {
	t.Helper()
	e = newEnv(t, mediatest.FakeTools(t))
	fx := newFixture(t, e.root)
	fx.file("Au/Keep/b.m4b", mediatest.FakeFile{DurationMs: 5000})
	raw, err := os.ReadFile(filepath.Join(e.root, "Au", "Keep", "b.m4b"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.root, "Au", "Drop"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.root, "Au", "Drop", "b.m4b"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if res := e.scan(); res.BooksAdded != 2 {
		t.Fatalf("first scan = %+v", res)
	}
	e.probeAll()
	keepRoot, dropRoot = filepath.Join(e.root, "Au", "Keep"), filepath.Join(e.root, "Au", "Drop")
	if err := e.repo.MergeBooks(bg, e.bookAt(keepRoot).ID, e.bookAt(dropRoot).ID, ""); err != nil {
		t.Fatal(err)
	}
	return e, keepRoot, dropRoot
}

func liveBooks(t *testing.T, e *env) int {
	t.Helper()
	var n int
	if err := e.st.Reader().QueryRow(`SELECT count(*) FROM books WHERE missing_since IS NULL`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// treeHash is a hash of every path, size and content under dir: it changes if anything is written, renamed or removed.
func treeHash(t *testing.T, dir string) [32]byte {
	t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		h.Write([]byte(p))
		if !d.IsDir() {
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			h.Write(b)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func TestMergedRescanKeepsFolderOut(t *testing.T) {
	e, _, _ := mergedEnv(t)
	before := treeHash(t, e.root)
	res := e.scan()
	if res.BooksAdded != 0 || res.FilesAdded != 0 || res.BooksMerged != 1 {
		t.Fatalf("rescan = %+v, want nothing added and one merged folder", res)
	}
	if n := liveBooks(t, e); n != 1 {
		t.Errorf("live books = %d, want 1", n)
	}
	if treeHash(t, e.root) != before {
		t.Error("a library file changed")
	}
}

func TestMergedFilesAreNotMovedFilesOfAnotherBook(t *testing.T) {
	e, keepRoot, _ := mergedEnv(t)
	// The kept copy's folder disappears while the identical, merged-away copy is still on disk: the kept book
	// goes missing, and the merged folder's file must not adopt its row as a "moved" file.
	if err := os.RemoveAll(keepRoot); err != nil {
		t.Fatal(err)
	}
	res := e.scan()
	if res.FilesMoved != 0 || res.BooksMoved != 0 || res.FilesMissing != 1 || res.BooksAdded != 0 {
		t.Fatalf("scan after the kept folder vanished = %+v", res)
	}
	if live, missing := liveFiles(t, e); live != 0 || missing != 1 {
		t.Errorf("live %d missing %d, want the kept file missing and nothing else", live, missing)
	}
}

func TestMergedRestoreReturnsTheFolder(t *testing.T) {
	e, _, dropRoot := mergedEnv(t)
	lib, err := e.repo.RestoreMergedFolder(bg, dropRoot, "")
	if err != nil || lib != e.lib.ID {
		t.Fatalf("restore = %q, %v", lib, err)
	}
	res := e.scan()
	if res.BooksAdded != 1 || res.BooksMerged != 0 {
		t.Fatalf("scan after restore = %+v", res)
	}
	if n := liveBooks(t, e); n != 2 {
		t.Errorf("live books = %d, want 2", n)
	}
}

func TestMergedKeptBookDeletedFreesTheFolder(t *testing.T) {
	e, keepRoot, _ := mergedEnv(t)
	if _, err := e.st.Writer().Exec(`DELETE FROM books WHERE id = ?`, e.bookAt(keepRoot).ID); err != nil {
		t.Fatal(err)
	}
	res := e.scan()
	if res.BooksMerged != 0 || res.BooksAdded != 2 {
		t.Fatalf("scan after the kept book was deleted = %+v, want both folders added", res)
	}
}

func TestMergedSubtreeScanSkipsTheFolder(t *testing.T) {
	e, _, _ := mergedEnv(t)
	res := e.scanSub("Au/Drop")
	if res.BooksAdded != 0 {
		t.Errorf("subtree scan of a merged folder = %+v", res)
	}
}

// A merge that lands after the walk but before the diff is written must still keep the dropped copy out.
func TestMergedDuringScanKeepsFolderOut(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	fx := newFixture(t, e.root)
	fx.file("Au/Keep/b.m4b", mediatest.FakeFile{DurationMs: 5000})
	raw, err := os.ReadFile(filepath.Join(e.root, "Au", "Keep", "b.m4b"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(e.root, "Au", "Drop"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.root, "Au", "Drop", "b.m4b"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	if res := e.scan(); res.BooksAdded != 2 {
		t.Fatalf("first scan = %+v", res)
	}
	e.probeAll()
	keepRoot, dropRoot := filepath.Join(e.root, "Au", "Keep"), filepath.Join(e.root, "Au", "Drop")
	e.sc.beforeApply = func() {
		e.sc.beforeApply = nil
		if err := e.repo.MergeBooks(bg, e.bookAt(keepRoot).ID, e.bookAt(dropRoot).ID, ""); err != nil {
			t.Error(err)
		}
	}
	e.scan()
	if n := liveBooks(t, e); n != 1 {
		t.Errorf("live books after a merge during the scan = %d, want 1", n)
	}
	if res := e.scan(); res.BooksAdded != 0 || res.BooksMerged != 1 {
		t.Errorf("next scan = %+v", res)
	}
}
