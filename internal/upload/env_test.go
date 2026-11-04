package upload

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

func TestMain(m *testing.M) {
	mediatest.RunFakeIfRequested()
	os.Exit(m.Run())
}

var bg = context.Background()

const me = "us_owner"

// queued is one job a fake runner was asked to run.
type queued struct {
	ID, Kind string
	Payload  []byte
}

// fakeEnq records jobs instead of running them.
type fakeEnq struct {
	mu   sync.Mutex
	jobs []queued
	fail error
}

func (e *fakeEnq) Enqueue(_ context.Context, kind string, payload any, _ jobs.EnqueueOptions) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.fail != nil {
		return "", e.fail
	}
	b, _ := json.Marshal(payload)
	id := fmt.Sprintf("jb_%d", len(e.jobs)+1)
	e.jobs = append(e.jobs, queued{ID: id, Kind: kind, Payload: b})
	return id, nil
}

func (e *fakeEnq) ActiveKeys(context.Context, string, string) (map[string]bool, error) {
	return map[string]bool{}, nil
}

func (e *fakeEnq) of(kind string) []queued {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []queued
	for _, j := range e.jobs {
		if j.Kind == kind {
			out = append(out, j)
		}
	}
	return out
}

// env is a service over a fresh database, a data folder and a library folder that takes uploads.
type env struct {
	t      *testing.T
	st     *db.Store
	repo   *library.Repo
	svc    *Service
	enq    *fakeEnq
	lib    library.Library
	folder library.Folder
	root   string // the library folder
	data   string
}

func newEnv(t *testing.T, mod func(*Config)) *env {
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
	if err := repo.SetUploadFolder(bg, lib.ID, folder.ID); err != nil {
		t.Fatal(err)
	}
	data := t.TempDir()
	cfg := Config{DataDir: data, ChunkSize: 64 << 10, MaxFile: 1 << 20, MaxBook: 4 << 20, MaxFiles: 20, Margin: 0}
	if mod != nil {
		mod(&cfg)
	}
	enq := &fakeEnq{}
	svc := New(cfg, st, repo, mediatest.FakeTools(t), enq, slog.New(slog.NewTextHandler(io.Discard, nil)))
	svc.free = func(string) (uint64, error) { return 1 << 40, nil }
	return &env{t: t, st: st, repo: repo, svc: svc, enq: enq, lib: lib, folder: folder, root: root, data: data}
}

// fakeAudio returns the bytes of a fake audio file.
func fakeAudio(t *testing.T, f mediatest.FakeFile) []byte {
	t.Helper()
	p := filepath.Join(t.TempDir(), "x")
	mediatest.WriteFake(t, p, f)
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// book is a set of files to upload: path (slash form) to content.
type book map[string][]byte

func manifestOf(label string, b book) Manifest {
	m := Manifest{Label: label}
	paths := make([]string, 0, len(b))
	for p := range b {
		paths = append(paths, p)
	}
	sortStrings(paths)
	for _, p := range paths {
		m.Files = append(m.Files, FileIn{Path: p, Size: int64(len(b[p])), ModifiedMs: 1})
	}
	return m
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// send creates an upload and sends every byte, chunk by chunk.
func (e *env) send(label string, b book) View {
	e.t.Helper()
	v, err := e.svc.Create(bg, me, e.lib.ID, manifestOf(label, b))
	if err != nil {
		e.t.Fatalf("Create: %v", err)
	}
	e.sendFrom(v, b, nil)
	if _, err := e.svc.Finish(bg, me, v.ID); err != nil {
		e.t.Fatalf("Finish: %v", err)
	}
	return v
}

// sendFrom sends the bytes after each file's offset in v.
func (e *env) sendFrom(v View, b book, sum func([]byte) string) {
	e.t.Helper()
	for _, f := range v.Files {
		data := b[f.Path]
		off := f.Offset
		for off < int64(len(data)) {
			end := off + v.ChunkSize
			if end > int64(len(data)) {
				end = int64(len(data))
			}
			chunk := data[off:end]
			s := ""
			if sum != nil {
				s = sum(chunk)
			}
			n, err := e.svc.WriteChunk(bg, me, v.ID, f.ID, off, s, strings.NewReader(string(chunk)))
			if err != nil {
				e.t.Fatalf("WriteChunk %s at %d: %v", f.Path, off, err)
			}
			off = n
		}
	}
}

func hexSum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// place runs the placement job body without a runner.
func (e *env) place(id string) (any, error) {
	return e.svc.place(bg, nil, func(any) {}, id)
}

func (e *env) mustPlace(id string) PlaceResult {
	e.t.Helper()
	res, err := e.place(id)
	if err != nil {
		e.t.Fatalf("place: %v", err)
	}
	return res.(PlaceResult)
}

func (e *env) upload(id string) uploadRow {
	e.t.Helper()
	u, err := e.svc.loadUpload(bg, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return u
}

func (e *env) ledger(id string) []pathRow {
	e.t.Helper()
	rows, err := e.svc.pathsOf(bg, id)
	if err != nil {
		e.t.Fatal(err)
	}
	return rows
}

// tree lists every path below dir (relative, slash form, directories end with /), sorted.
func tree(t *testing.T, dir string) []string {
	t.Helper()
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			rel += "/"
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func mp3(t *testing.T, tags map[string]string) []byte {
	return fakeAudio(t, mediatest.FakeFile{Container: "mp3", Codec: "mp3", Tags: tags, PadBytes: 100})
}

// scanner returns a scanner over the same database, to import what a placement queued.
func (e *env) scanner() *scan.Scanner {
	return scan.New(e.repo, mediatest.FakeTools(e.t), &scanEnq{}, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

type scanEnq struct{}

func (scanEnq) Enqueue(context.Context, string, any, jobs.EnqueueOptions) (string, error) {
	return "jb_x", nil
}
func (scanEnq) ActiveKeys(context.Context, string, string) (map[string]bool, error) {
	return map[string]bool{}, nil
}
