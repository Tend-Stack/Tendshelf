package ebook

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

var bg = context.Background()

// noEnq stands in for the job runner on the scanner's side: probes are not under test here.
type noEnq struct{}

func (noEnq) Enqueue(context.Context, string, any, jobs.EnqueueOptions) (string, error) {
	return "", nil
}
func (noEnq) ActiveKeys(context.Context, string, string) (map[string]bool, error) {
	return map[string]bool{}, nil
}

type ienv struct {
	t      *testing.T
	st     *db.Store
	repo   *library.Repo
	svc    *Service
	sc     *scan.Scanner
	runner *jobs.Runner
	lib    library.Library
	folder library.Folder
	root   string
	data   string
}

// newIEnv builds a service over a fresh database and an empty library folder. The runner is started
// only when run is true; otherwise queued jobs stay queued.
func newIEnv(t *testing.T, run bool) *ienv {
	t.Helper()
	data := t.TempDir()
	st, err := db.Open(bg, data)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	runner, err := jobs.New(st, jobs.Options{Logger: log, PollEvery: 20 * time.Millisecond, ProgressEvery: 50 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
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
	svc := New(st, repo, runner, data, log)
	sc := scan.New(repo, mediatest.FakeTools(t), noEnq{}, log)
	sc.OnEbooks(svc.ScanSink())
	sc.OnScanFinished(svc.Finisher())
	if err := svc.Register(runner); err != nil {
		t.Fatal(err)
	}
	if run {
		if err := runner.Start(bg); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(bg, 5*time.Second)
		defer cancel()
		_ = runner.Stop(ctx)
	})
	return &ienv{t: t, st: st, repo: repo, svc: svc, sc: sc, runner: runner, lib: lib, folder: folder, root: root, data: data}
}

func (e *ienv) write(rel string, data []byte) string {
	e.t.Helper()
	p := filepath.Join(e.root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		e.t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		e.t.Fatal(err)
	}
	return p
}

func (e *ienv) scan() {
	e.t.Helper()
	if _, err := e.sc.ScanLibrary(bg, e.lib.ID, nil); err != nil {
		e.t.Fatalf("ScanLibrary: %v", err)
	}
}

func (e *ienv) fileAt(path string) File {
	e.t.Helper()
	var id string
	if err := e.st.Reader().QueryRow(`SELECT id FROM ebook_files WHERE path = ?`, path).Scan(&id); err != nil {
		e.t.Fatalf("no ebook row at %s: %v", path, err)
	}
	return e.file(id)
}

func (e *ienv) file(id string) File {
	e.t.Helper()
	f, ok, err := e.svc.Store().File(bg, id)
	if err != nil || !ok {
		e.t.Fatalf("file %s: ok=%v err=%v", id, ok, err)
	}
	return f
}

func (e *ienv) count(q string, args ...any) int {
	e.t.Helper()
	var n int
	if err := e.st.Reader().QueryRow(q, args...).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// waitState waits for a file to leave pending.
func (e *ienv) waitState(id string) File {
	e.t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if f := e.file(id); f.State != "pending" {
			return f
		}
		time.Sleep(20 * time.Millisecond)
	}
	e.t.Fatalf("file %s stayed pending", id)
	return File{}
}

func (e *ienv) setState(id, state string) {
	e.t.Helper()
	if _, err := e.st.Writer().Exec(`UPDATE ebook_files SET state=?, job_id=NULL WHERE id=?`, state, id); err != nil {
		e.t.Fatal(err)
	}
}

func (e *ienv) book(rel string) library.Book {
	e.t.Helper()
	b, err := e.repo.BookByRootPath(bg, filepath.Join(e.root, rel))
	if err != nil {
		e.t.Fatalf("book %s: %v", rel, err)
	}
	return b
}

func sampleEPUB(t *testing.T, title string) []byte {
	return ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: title, Authors: []string{"Ann Author"},
		Chapters: []ebooktest.EPUBChapter{
			{Title: "One", Paras: []string{"It was a quiet morning in the harbour town and nobody expected anything at all."}},
			{Title: "Two", Paras: []string{"The second chapter begins with a short line.", "Another paragraph follows it."}},
		}})
}

func TestIngestReconcile(t *testing.T) {
	e := newIEnv(t, false)
	a := e.write("a.epub", []byte("alpha bytes alpha bytes"))
	b := e.write("b.epub", []byte("bravo bytes"))
	e.write("skipme/c.epub", []byte("charlie"))
	e.scan()
	fa, fb := e.fileAt(a), e.fileAt(b)
	if fa.State != "pending" || fa.Origin != "library" || fa.Ext != "epub" || fa.FolderID != e.folder.ID || fa.JobID == "" {
		t.Fatalf("new row = %+v", fa)
	}

	// Unchanged: nothing is touched.
	e.scan()
	if again := e.fileAt(a); again.UpdatedAt != fa.UpdatedAt || again.JobID != fa.JobID {
		t.Fatalf("unchanged row was rewritten: %+v -> %+v", fa, again)
	}

	// Changed bytes after a read: pending again, document hash cleared.
	e.setState(fa.ID, "ready")
	if _, err := e.st.Writer().Exec(`UPDATE ebook_files SET sha256=? WHERE id=?`, strings.Repeat("a", 64), fa.ID); err != nil {
		t.Fatal(err)
	}
	e.write("a.epub", []byte("alpha bytes changed and longer now"))
	e.scan()
	if got := e.fileAt(a); got.State != "pending" || got.SHA256 != "" || got.QuickHash == fa.QuickHash || got.ID != fa.ID {
		t.Fatalf("changed row = %+v", got)
	}

	// Moved: same bytes under a new name keep the row.
	if err := os.MkdirAll(filepath.Join(e.root, "moved"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(b, filepath.Join(e.root, "moved", "b2.epub")); err != nil {
		t.Fatal(err)
	}
	e.scan()
	if got := e.file(fb.ID); got.Path != filepath.Join(e.root, "moved", "b2.epub") || got.MissingSince != 0 {
		t.Fatalf("moved row = %+v", got)
	}
	if n := e.count(`SELECT count(*) FROM ebook_files`); n != 3 {
		t.Fatalf("rows = %d, want 3 (a move adds no row)", n)
	}

	// Missing, then restored.
	if err := os.Remove(a); err != nil {
		t.Fatal(err)
	}
	e.scan()
	if got := e.file(fa.ID); got.MissingSince == 0 {
		t.Fatal("a vanished file was not marked missing")
	}
	e.write("a.epub", []byte("alpha bytes changed and longer now"))
	e.scan()
	if got := e.file(fa.ID); got.MissingSince != 0 {
		t.Fatal("a restored file is still missing")
	}

	// A skipped folder proves nothing; a scoped scan only touches rows under its scope.
	c := e.fileAt(filepath.Join(e.root, "skipme", "c.epub"))
	empty := scan.EbookScan{LibraryID: e.lib.ID, Skip: []string{filepath.Join(e.root, "skipme")},
		Folders: []scan.EbookFolder{{FolderID: e.folder.ID, Path: e.root}}}
	if err := e.svc.Reconcile(bg, empty); err != nil {
		t.Fatal(err)
	}
	if got := e.file(c.ID); got.MissingSince != 0 {
		t.Fatal("a row under a skipped path was marked missing")
	}
	if got := e.file(fa.ID); got.MissingSince == 0 {
		t.Fatal("a row outside every skip path should be missing when not seen")
	}
	if _, err := e.st.Writer().Exec(`UPDATE ebook_files SET missing_since=NULL`); err != nil {
		t.Fatal(err)
	}
	scoped := scan.EbookScan{LibraryID: e.lib.ID, Scope: filepath.Join(e.root, "skipme"),
		Folders: []scan.EbookFolder{{FolderID: e.folder.ID, Path: e.root}}}
	if err := e.svc.Reconcile(bg, scoped); err != nil {
		t.Fatal(err)
	}
	if got := e.file(c.ID); got.MissingSince == 0 {
		t.Fatal("a row under the scope was not marked missing")
	}
	if got := e.file(fa.ID); got.MissingSince != 0 {
		t.Fatal("a row outside the scope changed")
	}
}

func TestIngestPairing(t *testing.T) {
	e := newIEnv(t, false)
	e.write("Folder Book/01.mp3", []byte("audio one"))
	e.write("Folder Book/02.mp3", []byte("audio two"))
	e.write("Folder Book/folder.epub", []byte("e1"))
	e.write("Series/One/01.mp3", []byte("audio three"))
	e.write("Series/One/Extras/extra.epub", []byte("e2"))
	e.write("Deep/Lonely/lonely.epub", []byte("e3"))
	e.write("single.mp3", []byte("audio single"))
	e.write("single.epub", []byte("e4"))
	e.scan()
	want := map[string]string{
		"Folder Book/folder.epub":      e.book("Folder Book").ID,
		"Series/One/Extras/extra.epub": e.book("Series/One").ID,
		"single.epub":                  e.book("single.mp3").ID,
		"Deep/Lonely/lonely.epub":      "",
	}
	for rel, book := range want {
		f := e.fileAt(filepath.Join(e.root, rel))
		if f.BookID != book {
			t.Errorf("%s paired with %q, want %q", rel, f.BookID, book)
		}
		if book != "" && f.PairedBy != "folder" {
			t.Errorf("%s paired_by = %q", rel, f.PairedBy)
		}
	}

	// Two candidates in one directory: left unpaired.
	e2 := newIEnv(t, false)
	e2.write("one.mp3", []byte("audio a"))
	e2.write("two.mp3", []byte("audio b"))
	e2.write("both.epub", []byte("e5"))
	e2.scan()
	if f := e2.fileAt(filepath.Join(e2.root, "both.epub")); f.BookID != "" {
		t.Fatalf("ambiguous ebook was paired with %q", f.BookID)
	}

	// A pairing made another way is never overridden.
	f := e.fileAt(filepath.Join(e.root, "Deep", "Lonely", "lonely.epub"))
	other := e.book("Folder Book")
	if _, err := e.st.Writer().Exec(`UPDATE ebook_files SET paired_by='person' WHERE id=?`, f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Writer().Exec(`UPDATE ebook_files SET book_id=NULL, paired_by='person' WHERE id=?`,
		e.fileAt(filepath.Join(e.root, "single.epub")).ID); err != nil {
		t.Fatal(err)
	}
	e.scan()
	if got := e.fileAt(filepath.Join(e.root, "single.epub")); got.BookID != "" || got.PairedBy != "person" {
		t.Fatalf("a person's choice was overridden: %+v", got)
	}
	_ = other
}

func TestIngestJob(t *testing.T) {
	e := newIEnv(t, true)
	good := e.write("Book/good.epub", sampleEPUB(t, "A Harbour Tale"))
	drm := e.write("Book/locked.epub", ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "Locked", Rights: true,
		Chapters: []ebooktest.EPUBChapter{{Title: "One", Paras: []string{"hidden"}}}}))
	e.write("Book/01.mp3", []byte("audio"))
	e.scan()

	g := e.waitState(e.fileAt(good).ID)
	if g.State != "ready" || g.Format != "epub" || len(g.SHA256) != 64 {
		t.Fatalf("good file = %+v", g)
	}
	ed, ok, err := e.svc.Store().CurrentEdition(bg, g.ID)
	if err != nil || !ok {
		t.Fatalf("CurrentEdition ok=%v err=%v", ok, err)
	}
	if ed.Title != "A Harbour Tale" || ed.ChapterCount < 2 || ed.WordCount == 0 || ed.AuthorsJSON != `["Ann Author"]` {
		t.Fatalf("edition = %+v", ed)
	}
	chs, err := e.svc.Store().Chapters(bg, ed.ID)
	if err != nil || len(chs) != ed.ChapterCount {
		t.Fatalf("chapters = %d err=%v", len(chs), err)
	}
	var withLine int
	for _, c := range chs {
		if c.FirstLine != "" && c.FirstBlockID != "" {
			withLine++
		}
	}
	if withLine < 2 || !strings.HasPrefix(chs[len(chs)-1].FirstLine, "The second chapter") {
		t.Fatalf("first lines = %+v", chs)
	}
	if _, err := os.Stat(DocPath(filepath.Join(e.data, "text"), ed.DocSHA256)); err != nil {
		t.Fatalf("document missing: %v", err)
	}

	d := e.waitState(e.fileAt(drm).ID)
	if d.State != "refused" || d.RefusedReason != ReasonDRM {
		t.Fatalf("drm file = %+v", d)
	}
	if ok, _ := e.svc.Store().ReadyForBook(bg, e.book("Book").ID); !ok {
		t.Error("the book's ready ebook is not reported")
	}

	// A file that vanished before the read fails with code missing.
	gone := e.write("Book/gone.epub", sampleEPUB(t, "Gone"))
	e.scan()
	gf := e.fileAt(gone)
	_ = os.Remove(gone)
	if got := e.waitState(gf.ID); got.State != "failed" && got.State != "ready" {
		t.Fatalf("vanished file = %+v", got)
	}
}

func TestIngestJobStale(t *testing.T) {
	e := newIEnv(t, true)
	p := e.write("a.epub", sampleEPUB(t, "Stale"))
	// While the job reads the file, a newer scan changes the row's quick hash: the result is discarded.
	parseHook = func(string) {
		_, _ = e.st.Writer().Exec(`UPDATE ebook_files SET quick_hash=? WHERE path=?`, strings.Repeat("b", 64), p)
	}
	t.Cleanup(func() { parseHook = nil })
	e.scan()
	id := e.fileAt(p).ID
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if e.count(`SELECT count(*) FROM jobs WHERE kind=? AND state NOT IN ('queued','running')`, KindIngest) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got := e.file(id); got.State != "pending" || got.SHA256 != "" {
		t.Fatalf("a stale result was written: %+v", got)
	}
	if n := e.count(`SELECT count(*) FROM editions`); n != 0 {
		t.Fatalf("editions = %d, want 0", n)
	}
}

func TestIngestNotYetReset(t *testing.T) {
	e := newIEnv(t, false)
	p := e.write("a.epub", sampleEPUB(t, "Retry"))
	e.scan()
	f := e.fileAt(p)
	if _, err := e.st.Writer().Exec(`UPDATE ebook_files SET state='refused', refused_reason='unsupported', error_code='not_yet', job_id=NULL WHERE id=?`, f.ID); err != nil {
		t.Fatal(err)
	}
	// The first read finished (as refused); its job is no longer waiting.
	if _, err := e.st.Writer().Exec(`UPDATE jobs SET state='done'`); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.EnqueuePending(bg); err != nil {
		t.Fatal(err)
	}
	if got := e.file(f.ID); got.State != "pending" || got.RefusedReason != "" || got.JobID == "" {
		t.Fatalf("not_yet row = %+v", got)
	}
	// A refusal for a real reason stays refused.
	if _, err := e.st.Writer().Exec(`UPDATE ebook_files SET state='refused', refused_reason='drm', error_code='adept' WHERE id=?`, f.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.EnqueuePending(bg); err != nil {
		t.Fatal(err)
	}
	if got := e.file(f.ID); got.State != "refused" {
		t.Fatalf("drm row = %+v", got)
	}
}

func uploadBook(t *testing.T, e *ienv) library.Book {
	t.Helper()
	e.write("Up Book/01.mp3", []byte("audio"))
	e.scan()
	return e.book("Up Book")
}

func TestIngestUpload(t *testing.T) {
	e := newIEnv(t, true)
	book := uploadBook(t, e)
	data := sampleEPUB(t, "Uploaded")

	f, err := e.svc.AddUpload(bg, book, "", "My Book.EPUB", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	sha := hex.EncodeToString(sum[:])
	if f.Origin != "upload" || f.SHA256 != sha || f.PairedBy != "upload" || f.BookID != book.ID || f.OriginalName != "My Book.EPUB" {
		t.Fatalf("row = %+v", f)
	}
	stored := filepath.Join(e.data, "ebooks", sha+".epub")
	got, err := os.ReadFile(stored)
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("stored bytes differ or missing: %v", err)
	}
	if fi, _ := os.Stat(stored); fi.Mode().Perm() != 0o600 {
		t.Errorf("stored mode = %v", fi.Mode().Perm())
	}
	if di, _ := os.Stat(filepath.Join(e.data, "ebooks")); di.Mode().Perm() != 0o700 {
		t.Errorf("ebooks dir mode = %v", di.Mode().Perm())
	}
	if r := e.waitState(f.ID); r.State != "ready" {
		t.Fatalf("after job: %+v", r)
	}
	if ents, _ := os.ReadDir(filepath.Join(e.data, "ebooks", ".incoming")); len(ents) != 0 {
		t.Errorf("temp files left: %d", len(ents))
	}

	// Nothing was written into the library folder.
	if _, err := os.Stat(filepath.Join(e.root, "Up Book", "My Book.EPUB")); err == nil {
		t.Error("an upload reached the library folder")
	}

	cases := []struct {
		name, file string
		body       []byte
		status     int
		code       string
	}{
		{"no extension", "notes", []byte("x"), http.StatusUnprocessableEntity, "unsupported"},
		{"kfx", "book.kfx", []byte("x"), http.StatusUnprocessableEntity, "copy_protected"},
		{"kfx-zip", "book.kfx-zip", []byte("x"), http.StatusUnprocessableEntity, "copy_protected"},
		{"empty", "empty.epub", nil, http.StatusUnprocessableEntity, "empty"},
		{"duplicate", "again.epub", data, http.StatusConflict, "duplicate"},
	}
	for _, c := range cases {
		_, err := e.svc.AddUpload(bg, book, "", c.file, bytes.NewReader(c.body))
		var ue *UploadError
		if !errors.As(err, &ue) || ue.Status != c.status || ue.Code != c.code || ue.Message == "" {
			t.Errorf("%s: err = %v, want %d %s", c.name, err, c.status, c.code)
		}
	}
	if n := e.count(`SELECT count(*) FROM ebook_files WHERE origin='upload'`); n != 1 {
		t.Errorf("upload rows = %d, want 1", n)
	}

	// Too large: a body over the cap is cut and refused; nothing stays on disk.
	small := e.svc
	small.limits.MaxFileBytes = 1024
	_, err = small.AddUpload(bg, book, "", "big.txt", bytes.NewReader(bytes.Repeat([]byte("x"), 4096)))
	var ue *UploadError
	if !errors.As(err, &ue) || ue.Status != http.StatusRequestEntityTooLarge || ue.Code != "too_large" {
		t.Errorf("too large: err = %v", err)
	}
	if ents, _ := os.ReadDir(filepath.Join(e.data, "ebooks", ".incoming")); len(ents) != 0 {
		t.Errorf("temp files left after a refusal: %d", len(ents))
	}
}

func TestIngestUploadRefusalDeletesBytes(t *testing.T) {
	e := newIEnv(t, true)
	book := uploadBook(t, e)
	locked := ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "Locked", Rights: true,
		Chapters: []ebooktest.EPUBChapter{{Title: "One", Paras: []string{"hidden"}}}})
	f, err := e.svc.AddUpload(bg, book, "", "locked.epub", bytes.NewReader(locked))
	if err != nil {
		t.Fatal(err)
	}
	r := e.waitState(f.ID)
	if r.State != "refused" || r.RefusedReason != ReasonDRM {
		t.Fatalf("row = %+v", r)
	}
	if _, err := os.Stat(e.svc.storedPath(f.SHA256, "epub")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refused upload bytes still stored: %v", err)
	}
}

func TestIngestDismiss(t *testing.T) {
	e := newIEnv(t, true)
	book := uploadBook(t, e)
	locked := ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "Locked", Rights: true,
		Chapters: []ebooktest.EPUBChapter{{Title: "One", Paras: []string{"hidden"}}}})
	f, err := e.svc.AddUpload(bg, book, "", "locked.epub", bytes.NewReader(locked))
	if err != nil {
		t.Fatal(err)
	}
	e.waitState(f.ID)
	good, err := e.svc.AddUpload(bg, book, "", "ok.epub", bytes.NewReader(sampleEPUB(t, "Fine")))
	if err != nil {
		t.Fatal(err)
	}
	e.waitState(good.ID)

	if err := e.svc.Dismiss(bg, "wrong-book", f.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("wrong book: %v", err)
	}
	if err := e.svc.Dismiss(bg, book.ID, good.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a ready file must not be dismissable: %v", err)
	}
	libFile := e.write("Up Book/lib.epub", []byte("not an epub"))
	e.scan()
	lf := e.waitState(e.fileAt(libFile).ID)
	if err := e.svc.Dismiss(bg, book.ID, lf.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("a library file must not be dismissable: %v", err)
	}
	if err := e.svc.Dismiss(bg, book.ID, f.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := e.svc.Store().File(bg, f.ID); ok {
		t.Error("dismissed row still exists")
	}
	if err := e.svc.Dismiss(bg, book.ID, f.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("second dismiss: %v", err)
	}
}

func TestIngestReadOnly(t *testing.T) {
	e := newIEnv(t, true)
	dir := filepath.Join(e.root, "Locked Dir")
	p := e.write("Locked Dir/book.epub", sampleEPUB(t, "Read Only"))
	if err := os.Chmod(p, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	before, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	e.scan()
	if r := e.waitState(e.fileAt(p).ID); r.State != "ready" {
		t.Fatalf("read-only file = %+v", r)
	}
	after, err := os.Stat(p)
	if err != nil || !after.ModTime().Equal(before.ModTime()) || after.Mode() != before.Mode() {
		t.Fatalf("library file changed: %v %v", before.ModTime(), after.ModTime())
	}
	ents, _ := os.ReadDir(dir)
	if len(ents) != 1 {
		t.Fatalf("library dir has %d entries, want 1", len(ents))
	}
}
