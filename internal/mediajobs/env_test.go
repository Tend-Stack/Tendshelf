package mediajobs

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
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
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

var bg = context.Background()

func TestMain(m *testing.M) {
	mediatest.RunFakeIfRequested()
	os.Exit(m.Run())
}

// fakeEnq records the jobs a service asks for, in place of a job runner.
type fakeEnq struct {
	mu     sync.Mutex
	kinds  []string
	ids    []string
	active map[string]map[string]bool // kind -> payload value -> waiting
}

func (e *fakeEnq) Enqueue(_ context.Context, kind string, payload any, _ jobs.EnqueueOptions) (string, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var id string
	switch p := payload.(type) {
	case CoverPayload:
		id = p.BookID
	case CompatPayload:
		id = p.AudioFileID
	case scan.ProbePayload:
		id = p.AudioFileID
	}
	e.kinds = append(e.kinds, kind)
	e.ids = append(e.ids, id)
	if e.active == nil {
		e.active = map[string]map[string]bool{}
	}
	if e.active[kind] == nil {
		e.active[kind] = map[string]bool{}
	}
	e.active[kind][id] = true
	return "jb_fake", nil
}

func (e *fakeEnq) ActiveKeys(_ context.Context, kind, _ string) (map[string]bool, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := map[string]bool{}
	for k := range e.active[kind] {
		out[k] = true
	}
	return out, nil
}

func (e *fakeEnq) count(kind string) int {
	e.mu.Lock()
	defer e.mu.Unlock()
	n := 0
	for _, k := range e.kinds {
		if k == kind {
			n++
		}
	}
	return n
}

// env is a scanner and the media services over a fresh database, an empty library folder and a
// data directory. ffmpeg runs are logged to ffLog.
type env struct {
	t      *testing.T
	st     *db.Store
	repo   *library.Repo
	sc     *scan.Scanner
	tools  media.Tools
	lib    library.Library
	folder library.Folder
	root   string
	data   string
	ffLog  string
	enq    *fakeEnq
}

func newEnv(t *testing.T) *env {
	t.Helper()
	tools := mediatest.FakeTools(t)
	ffLog := filepath.Join(t.TempDir(), "ffmpeg.log")
	tools.Env = append(tools.Env, mediatest.FakeFFmpegLogEnv+"="+ffLog)
	return newEnvWith(t, tools, ffLog)
}

func newEnvWith(t *testing.T, tools media.Tools, ffLog string) *env {
	t.Helper()
	dbDir := t.TempDir()
	st, err := db.Open(bg, dbDir)
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
	sc := scan.New(repo, tools, enq, slog.New(slog.NewTextHandler(io.Discard, nil)))
	return &env{t: t, st: st, repo: repo, sc: sc, tools: tools, lib: lib, folder: folder, root: root,
		data: t.TempDir(), ffLog: ffLog, enq: enq}
}

func (e *env) covers() *Covers {
	return NewCovers(e.repo, e.tools, e.data, e.enq, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func (e *env) path(rel string) string { return filepath.Join(e.root, filepath.FromSlash(rel)) }

// fake writes a fake audio file under the library folder.
func (e *env) fake(rel string, f mediatest.FakeFile) string {
	e.t.Helper()
	p := e.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if f.Tags == nil {
		f.Tags = map[string]string{"comment": rel} // keeps every file's quick hash unique
	}
	mediatest.WriteFake(e.t, p, f)
	return p
}

// image writes a solid-colour JPEG or PNG (by extension) of w by h.
func (e *env) image(rel, hex string, w, h int) string {
	e.t.Helper()
	p := e.path(rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	var r, g, b uint8
	for i, dst := range []*uint8{&r, &g, &b} {
		var v int
		for _, c := range hex[1+2*i : 3+2*i] {
			v = v*16 + strings.IndexRune("0123456789abcdef", c)
		}
		*dst = uint8(v)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{r, g, b, 255})
		}
	}
	var buf bytes.Buffer
	var err error
	if strings.HasSuffix(strings.ToLower(rel), "png") {
		err = png.Encode(&buf, img)
	} else {
		err = jpeg.Encode(&buf, img, &jpeg.Options{Quality: 90})
	}
	if err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		e.t.Fatal(err)
	}
	return p
}

// scanAll scans the library and probes every file, so each book settles.
func (e *env) scanAll() {
	e.t.Helper()
	if _, err := e.sc.ScanLibrary(bg, e.lib.ID, nil); err != nil {
		e.t.Fatalf("ScanLibrary: %v", err)
	}
	ids, err := e.repo.UnsettledAudioFileIDs(bg, e.lib.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	for _, id := range ids {
		if _, err := e.sc.ProbeFile(bg, id, nil); err != nil {
			var je *jobs.Error
			if !errors.As(err, &je) {
				e.t.Fatalf("ProbeFile: %v", err)
			}
		}
	}
}

func (e *env) book(rel string) library.Book {
	e.t.Helper()
	b, err := e.repo.BookByRootPath(bg, e.path(rel))
	if err != nil {
		e.t.Fatalf("book %s: %v", rel, err)
	}
	return b
}

func (e *env) covers1(b library.Book) []library.Cover {
	e.t.Helper()
	cs, err := e.repo.Covers(bg, b.ID)
	if err != nil {
		e.t.Fatal(err)
	}
	return cs
}

func (e *env) run(c *Covers, rel string) string {
	e.t.Helper()
	out, err := c.Run(bg, e.book(rel).ID, nil)
	if err != nil {
		e.t.Fatalf("Run(%s): %v", rel, err)
	}
	return out
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

// ffmpegRuns counts the lines the fake ffmpeg logged.
func (e *env) ffmpegRuns() int {
	b, err := os.ReadFile(e.ffLog)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		e.t.Fatal(err)
	}
	return len(strings.Split(strings.TrimSpace(string(b)), "\n"))
}

// listDir returns the sorted names in dir, or nil if it does not exist.
func listDir(t *testing.T, dir string) []string {
	t.Helper()
	es, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range es {
		out = append(out, e.Name())
	}
	return out
}
