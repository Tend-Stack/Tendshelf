package scan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

var bg = context.Background()

// fakeEnq records the probes a scan asks for, in place of a job runner.
type fakeEnq struct {
	mu     sync.Mutex
	ids    []string
	active map[string]bool
}

func (e *fakeEnq) Enqueue(_ context.Context, kind string, payload any, _ jobs.EnqueueOptions) (string, error) {
	if kind != KindProbe {
		return "", fmt.Errorf("unexpected kind %q", kind)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ids = append(e.ids, payload.(ProbePayload).AudioFileID)
	return "jb_fake", nil
}

func (e *fakeEnq) ActiveKeys(context.Context, string, string) (map[string]bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[string]bool{}
	for k := range e.active {
		out[k] = true
	}
	return out, nil
}

func (e *fakeEnq) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.ids)
}

// env is a scanner over a fresh database and an empty library folder.
type env struct {
	t      *testing.T
	st     *db.Store
	repo   *library.Repo
	sc     *Scanner
	enq    *fakeEnq
	lib    library.Library
	folder library.Folder
	root   string
}

func newEnv(t *testing.T, tools media.Tools) *env {
	t.Helper()
	st, err := db.Open(bg, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	repo := library.New(st)
	lib, err := repo.CreateLibrary(bg, "Books", 0)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	folder, err := repo.AddFolder(bg, lib.ID, root)
	if err != nil {
		t.Fatal(err)
	}
	enq := &fakeEnq{}
	sc := New(repo, tools, enq, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return &env{t: t, st: st, repo: repo, sc: sc, enq: enq, lib: lib, folder: folder, root: root}
}

func (e *env) scan() Result {
	e.t.Helper()
	res, err := e.sc.ScanLibrary(bg, e.lib.ID, nil)
	if err != nil {
		e.t.Fatalf("ScanLibrary: %v", err)
	}
	return res
}

// probeAll probes every unsettled file, four at a time, like the runner's probe workers.
func (e *env) probeAll() int {
	e.t.Helper()
	ids, err := e.repo.UnsettledAudioFileIDs(bg, e.lib.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	var wg sync.WaitGroup
	sem := make(chan struct{}, 4)
	for _, id := range ids {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if _, err := e.sc.ProbeFile(bg, id, nil); err != nil {
				var je *jobs.Error
				if !errors.As(err, &je) {
					e.t.Errorf("ProbeFile(%s): %v", id, err)
				}
			}
		}()
	}
	wg.Wait()
	return len(ids)
}

// changes is SQLite's total_changes() on the single writer connection.
func (e *env) changes() int64 {
	e.t.Helper()
	var n int64
	if err := e.st.Writer().QueryRow(`SELECT total_changes()`).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

func (e *env) bookAt(root string) library.Book {
	e.t.Helper()
	b, err := e.repo.BookByRootPath(bg, root)
	if err != nil {
		e.t.Fatalf("book at %s: %v", root, err)
	}
	return b
}

func (e *env) fileAt(path string) library.AudioFile {
	e.t.Helper()
	b, err := e.repo.BooksInFolder(bg, e.folder.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, bk := range b {
		fs, err := e.repo.AudioFiles(bg, bk.ID)
		if err != nil {
			e.t.Fatal(err)
		}
		for _, f := range fs {
			if f.Path == path {
				return f
			}
		}
	}
	e.t.Fatalf("no audio file row for %s", path)
	return library.AudioFile{}
}

// fixture writes fake audio files and remembers what each book must look like after a scan.
type fixture struct {
	t     *testing.T
	root  string
	n     int
	dur   map[string]int64 // playable file path -> duration
	books map[string]*wantBook
}

type wantBook struct {
	files   []string // expected play order, absolute paths of live files
	refused string
}

func newFixture(t *testing.T, root string) *fixture {
	return &fixture{t: t, root: root, dur: map[string]int64{}, books: map[string]*wantBook{}}
}

func (fx *fixture) abs(rel string) string { return filepath.Join(fx.root, filepath.FromSlash(rel)) }

// file writes one fake audio file with content unique to its path and returns its absolute path.
func (fx *fixture) file(rel string, f mediatest.FakeFile) string {
	fx.t.Helper()
	path := fx.abs(rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fx.t.Fatal(err)
	}
	fx.n++
	if f.DurationMs == 0 {
		f.DurationMs = int64(1000 + 10*(fx.n%97))
	}
	f.Tags = map[string]string{"comment": rel}
	mediatest.WriteFake(fx.t, path, f)
	if !f.Encrypted && media.Classify(path) != media.KindProtected {
		fx.dur[path] = f.DurationMs
	}
	return path
}

// book records the expected book at rootRel with its files (relative paths) in play order.
func (fx *fixture) book(rootRel, refused string, rels ...string) {
	w := &wantBook{refused: refused}
	for _, r := range rels {
		w.files = append(w.files, fx.abs(r))
	}
	fx.books[fx.abs(rootRel)] = w
}

func (fx *fixture) remove(rootRel string) { delete(fx.books, fx.abs(rootRel)) }

// verify checks every expected book against the database: nothing extra, play order, cumulative
// offsets, durations and refusals.
func (fx *fixture) verify(e *env) {
	e.t.Helper()
	t := e.t
	books, err := e.repo.BooksInFolder(bg, e.folder.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]library.Book{}
	for _, b := range books {
		got[b.RootPath] = b
	}
	for root := range got {
		if fx.books[root] == nil {
			t.Errorf("unexpected book at %s", root)
		}
	}
	for root, w := range fx.books {
		b, ok := got[root]
		if !ok {
			t.Errorf("no book at %s", root)
			continue
		}
		all, err := e.repo.AudioFiles(bg, b.ID)
		if err != nil {
			t.Fatal(err)
		}
		var live []library.AudioFile
		for _, f := range all {
			if f.MissingSince == 0 {
				live = append(live, f)
			}
		}
		var paths []string
		for _, f := range live {
			paths = append(paths, f.Path)
		}
		if strings.Join(paths, "\n") != strings.Join(w.files, "\n") {
			t.Errorf("%s: files =\n%s\nwant\n%s", root, strings.Join(paths, "\n"), strings.Join(w.files, "\n"))
			continue
		}
		var off int64
		for i, f := range live {
			if f.Idx != i {
				t.Errorf("%s: %s idx = %d, want %d", root, f.Path, f.Idx, i)
			}
			d, playable := fx.dur[f.Path]
			if playable {
				if f.OffsetMs != off || f.DurationMs != d {
					t.Errorf("%s: %s offset/duration = %d/%d, want %d/%d", root, f.Path, f.OffsetMs, f.DurationMs, off, d)
				}
				off += d
			}
		}
		if b.DurationMs != off {
			t.Errorf("%s: duration = %d, want %d", root, b.DurationMs, off)
		}
		if b.RefusedReason != w.refused {
			t.Errorf("%s: refused = %q, want %q", root, b.RefusedReason, w.refused)
		}
		if len(live) > 0 && b.MissingSince != 0 {
			t.Errorf("%s: live files but missing_since set", root)
		}
	}
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
