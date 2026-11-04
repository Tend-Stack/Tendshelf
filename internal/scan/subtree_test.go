package scan

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

func subtreeEnv(t *testing.T) (*env, *fixture) {
	t.Helper()
	e := newEnv(t, mediatest.FakeTools(t))
	fx := newFixture(t, e.root)
	for _, n := range []string{"One", "Two"} {
		fx.file("Au/"+n+"/b.m4b", mediatest.FakeFile{})
	}
	e.scan()
	e.probeAll()
	return e, fx
}

func (e *env) scanSub(subdir string) Result {
	e.t.Helper()
	res, err := e.sc.ScanSubtree(bg, e.lib.ID, e.folder.ID, subdir, nil)
	if err != nil {
		e.t.Fatalf("ScanSubtree: %v", err)
	}
	return res
}

func TestScanSubtreeAddsOnlyTheNewBook(t *testing.T) {
	e, fx := subtreeEnv(t)
	// A sibling book is renamed away: a full scan would mark it missing, a scoped one must not.
	if err := os.Rename(filepath.Join(e.root, "Au", "Two"), filepath.Join(t.TempDir(), "Two")); err != nil {
		t.Fatal(err)
	}
	fx.file("Au/Three/b.m4b", mediatest.FakeFile{})
	res := e.scanSub("Au/Three")
	if res.BooksAdded != 1 || res.FilesAdded != 1 || res.FilesMissing != 0 || res.BooksMissing != 0 {
		t.Fatalf("result = %+v", res)
	}
	if live, missing := liveFiles(t, e); live != 3 || missing != 0 {
		t.Errorf("live %d missing %d, want 3 and 0 (the renamed sibling stays live)", live, missing)
	}
	var books int
	if err := e.st.Reader().QueryRow(`SELECT count(*) FROM books WHERE missing_since IS NULL`).Scan(&books); err != nil || books != 3 {
		t.Errorf("live books = %d, %v", books, err)
	}
	if e.enq.count() < 3 {
		t.Errorf("the new file was not queued for a probe (%d probes)", e.enq.count())
	}
	// A full scan afterwards does see the vanished sibling.
	if res := e.scan(); res.FilesMissing != 1 {
		t.Errorf("full scan: %+v", res)
	}
}

func TestScanSubtreeDiscFolders(t *testing.T) {
	e, fx := subtreeEnv(t)
	fx.file("Au/Four/CD1/01.mp3", mediatest.FakeFile{Container: "mp3", Codec: "mp3"})
	fx.file("Au/Four/CD2/01.mp3", mediatest.FakeFile{Container: "mp3", Codec: "mp3"})
	res := e.scanSub("Au/Four")
	if res.BooksAdded != 1 || res.FilesAdded != 2 {
		t.Fatalf("result = %+v", res)
	}
	var root string
	if err := e.st.Reader().QueryRow(`SELECT root_path FROM books WHERE title = 'Four'`).Scan(&root); err != nil || root != filepath.Join(e.root, "Au", "Four") {
		t.Errorf("root = %q, %v", root, err)
	}
}

func TestScanSubtreeUnavailableOrEmptyChangesNothing(t *testing.T) {
	for _, how := range []string{"missing", "empty", "dots only"} {
		t.Run(how, func(t *testing.T) {
			e, _ := subtreeEnv(t)
			dir := filepath.Join(e.root, "Au", "Ghost")
			switch how {
			case "empty":
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
			case "dots only":
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(dir, ".hidden.mp3"), []byte("x"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			// Even with a sibling gone, nothing is marked missing.
			if err := os.RemoveAll(filepath.Join(e.root, "Au", "One")); err != nil {
				t.Fatal(err)
			}
			res := e.scanSub("Au/Ghost")
			if res.FoldersSkipped != 1 || res.BooksAdded != 0 || res.FilesMissing != 0 {
				t.Errorf("result = %+v", res)
			}
			if live, missing := liveFiles(t, e); live != 2 || missing != 0 {
				t.Errorf("live %d missing %d", live, missing)
			}
		})
	}
}

func TestScanSubtreeRejectsForeignFolder(t *testing.T) {
	e, _ := subtreeEnv(t)
	if _, err := e.sc.ScanSubtree(bg, e.lib.ID, "lf_nope", "Au/One", nil); err == nil {
		t.Error("unknown folder accepted")
	}
	// Traversal in the subdir is cleaned to stay below the folder.
	res, err := e.sc.ScanSubtree(bg, e.lib.ID, e.folder.ID, "../../etc", nil)
	if err != nil || res.FoldersSkipped != 1 {
		t.Errorf("traversal: %+v, %v", res, err)
	}
}
