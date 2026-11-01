package scan

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

// twoFolders is a library with a healthy folder (the env's) and a second one that will go away.
func twoFolders(t *testing.T) (e *env, fx, fx2 *fixture, second string) {
	t.Helper()
	e = newEnv(t, mediatest.FakeTools(t))
	second, _ = filepath.EvalSymlinks(t.TempDir())
	if _, err := e.repo.AddFolder(bg, e.lib.ID, second); err != nil {
		t.Fatal(err)
	}
	fx = newFixture(t, e.root)
	fx2 = newFixture(t, second)
	for _, n := range []string{"One", "Two"} {
		rel := "Au/" + n + "/b.m4b"
		fx.file(rel, mediatest.FakeFile{})
		fx.book("Au/"+n, "", rel)
		rel2 := "Other/" + n + "/b.m4b"
		fx2.file(rel2, mediatest.FakeFile{})
		fx2.book("Other/"+n, "", rel2)
	}
	e.scan()
	e.probeAll()
	return
}

func liveFiles(t *testing.T, e *env) (live, missing int) {
	t.Helper()
	if err := e.st.Reader().QueryRow(`SELECT count(*) FILTER (WHERE missing_since IS NULL), count(*) FILTER (WHERE missing_since IS NOT NULL) FROM audio_files`).Scan(&live, &missing); err != nil {
		t.Fatal(err)
	}
	return
}

func TestEmptyOrRemovedFolderMarksNothingMissing(t *testing.T) {
	for _, how := range []string{"emptied", "removed"} {
		t.Run(how, func(t *testing.T) {
			e, _, _, second := twoFolders(t)
			held := t.TempDir()
			if how == "emptied" {
				entries, _ := os.ReadDir(second)
				for _, en := range entries {
					if err := os.Rename(filepath.Join(second, en.Name()), filepath.Join(held, en.Name())); err != nil {
						t.Fatal(err)
					}
				}
			} else if err := os.Rename(second, filepath.Join(held, "gone")); err != nil {
				t.Fatal(err)
			}
			before := e.changes()
			res := e.scan()
			if res.FoldersSkipped != 1 || res.FilesMissing != 0 || res.BooksMissing != 0 {
				t.Fatalf("result = %+v", res)
			}
			if e.changes() != before {
				t.Fatal("the scan wrote rows")
			}
			if live, missing := liveFiles(t, e); live != 4 || missing != 0 {
				t.Fatalf("live/missing = %d/%d", live, missing)
			}
			// The healthy folder is still scanned normally while the other is protected.
			if err := os.Remove(filepath.Join(e.root, "Au", "One", "b.m4b")); err != nil {
				t.Fatal(err)
			}
			res = e.scan()
			if res.FoldersSkipped != 1 || res.FilesMissing != 1 || res.BooksMissing != 1 {
				t.Fatalf("result = %+v", res)
			}
			// Bring the folder back: nothing was lost, nothing needs probing.
			if how == "emptied" {
				entries, _ := os.ReadDir(held)
				for _, en := range entries {
					if err := os.Rename(filepath.Join(held, en.Name()), filepath.Join(second, en.Name())); err != nil {
						t.Fatal(err)
					}
				}
			} else if err := os.Rename(filepath.Join(held, "gone"), second); err != nil {
				t.Fatal(err)
			}
			res = e.scan()
			if res.FoldersSkipped != 0 || res.FilesChanged != 0 || res.FilesMissing != 0 || res.ProbesEnqueued != 0 || res.FilesAdded != 0 {
				t.Fatalf("result after return = %+v", res)
			}
		})
	}
}

func TestUnreadableSubtreeKeepsItsFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can read a mode 000 directory")
	}
	e := newEnv(t, mediatest.FakeTools(t))
	fx := newFixture(t, e.root)
	for _, rel := range []string{"Au/Open/b.m4b", "Au/Locked/b.m4b"} {
		fx.file(rel, mediatest.FakeFile{})
		fx.book(filepath.ToSlash(filepath.Dir(rel)), "", rel)
	}
	e.scan()
	e.probeAll()
	locked := filepath.Join(e.root, "Au", "Locked")
	if err := os.Chmod(locked, 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o755) })
	before := e.changes()
	res := e.scan()
	if res.UnreadableDirs != 1 || res.FilesMissing != 0 || e.changes() != before {
		t.Fatalf("result = %+v", res)
	}
	fx.verify(e)
}

func TestScanGuardStopsAllWrites(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	fx := newFixture(t, e.root)
	fx.file("A.m4b", mediatest.FakeFile{})
	before := e.changes()
	_, err := e.sc.ScanLibrary(bg, e.lib.ID, func(context.Context, *sql.Tx) error { return jobs.ErrLeaseLost })
	if !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatalf("err = %v", err)
	}
	if e.changes() != before || e.enq.count() != 0 {
		t.Fatal("a scan that lost its lease wrote or queued something")
	}
	if _, err := e.sc.ScanLibrary(bg, "lib_nope", nil); !errors.Is(err, library.ErrNotFound) {
		t.Fatalf("unknown library err = %v", err)
	}
}

func TestScanSkipsProbesAlreadyQueued(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	fx := newFixture(t, e.root)
	a := fx.file("A.m4b", mediatest.FakeFile{})
	fx.file("B.m4b", mediatest.FakeFile{})
	e.scan() // queues both
	e.enq.mu.Lock()
	queued := append([]string(nil), e.enq.ids...)
	e.enq.ids = nil
	e.enq.active = map[string]bool{queued[0]: true}
	e.enq.mu.Unlock()
	res := e.scan()
	if res.ProbesEnqueued != 1 || e.enq.count() != 1 || e.enq.ids[0] != queued[1] {
		t.Fatalf("result = %+v, queued %v, was %v (%s)", res, e.enq.ids, queued, a)
	}
}

func TestChangedFileKeepsItsIdentityAndProtectionStays(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	fx := newFixture(t, e.root)
	plain := fx.file("A.m4b", mediatest.FakeFile{})
	prot := fx.file("B.aax", mediatest.FakeFile{})
	e.scan()
	e.probeAll()
	idPlain, idProt := e.fileAt(plain).ID, e.fileAt(prot).ID
	if b := e.bookAt(prot); b.RefusedReason != library.RefusedDRM {
		t.Fatalf("protected book = %+v", b)
	}
	// Rewrite both with different content and size.
	mediatest.WriteFake(t, plain, mediatest.FakeFile{DurationMs: 9000, PadBytes: 10})
	mediatest.WriteFake(t, prot, mediatest.FakeFile{DurationMs: 9000, PadBytes: 10})
	res := e.scan()
	if res.FilesChanged != 2 || res.FilesAdded != 0 || res.FilesMoved != 0 || res.ProbesEnqueued != 1 {
		t.Fatalf("result = %+v", res)
	}
	p, q := e.fileAt(plain), e.fileAt(prot)
	if p.ID != idPlain || p.ProbeJSON != nil || p.ProbeError != "" || p.Encrypted {
		t.Fatalf("plain row = %+v", p)
	}
	if q.ID != idProt || !q.Encrypted || q.ProbeError != media.CodeDRM {
		t.Fatalf("protected row = %+v", q)
	}
	e.probeAll()
	if b := e.bookAt(plain); b.DurationMs != 9000 || b.RefusedReason != "" {
		t.Fatalf("book = %+v", b)
	}
}

func TestSwappedBookFoldersKeepTheirBooks(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	fx := newFixture(t, e.root)
	fx.file("Au/A/x.m4b", mediatest.FakeFile{})
	fx.file("Au/B/y.m4b", mediatest.FakeFile{})
	e.scan()
	e.probeAll()
	a, b := e.bookAt(fx.abs("Au/A")), e.bookAt(fx.abs("Au/B"))
	// Swap the two folders' contents: both roots stay group roots, so the books stay where they are and
	// the files follow their content to the other book.
	tmp := fx.abs("Au/tmp")
	for _, mv := range [][2]string{{"Au/A", "Au/tmp"}, {"Au/B", "Au/A"}, {"Au/tmp", "Au/B"}} {
		if err := os.Rename(fx.abs(mv[0]), fx.abs(mv[1])); err != nil {
			t.Fatal(err)
		}
	}
	_ = tmp
	res := e.scan()
	if res.BooksAdded != 0 || res.BooksMoved != 0 || res.FilesMoved != 2 || res.FilesAdded != 0 {
		t.Fatalf("result = %+v", res)
	}
	if got := e.bookAt(fx.abs("Au/A")); got.ID != a.ID {
		t.Fatal("book A changed identity")
	}
	if got := e.bookAt(fx.abs("Au/B")); got.ID != b.ID {
		t.Fatal("book B changed identity")
	}
	if f := e.fileAt(fx.abs("Au/A/y.m4b")); f.BookID != a.ID || f.MissingSince != 0 {
		t.Fatalf("file = %+v", f)
	}
}
