package backups

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
)

func put(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func exists(path string) bool { _, err := os.Stat(path); return err == nil }

// seedData fills the data folder the way the program does: the mirrored folders, plus things that must
// never be copied.
func seedData(t *testing.T, dir string) {
	t.Helper()
	put(t, filepath.Join(dir, "ebooks", "ab", "book.epub"), "epub bytes")
	put(t, filepath.Join(dir, "covers", "c1.jpg"), "cover bytes")
	put(t, filepath.Join(dir, "people", "p1.jpg"), "person bytes")
	put(t, filepath.Join(dir, "text", "t1.json"), "text bytes")
	put(t, filepath.Join(dir, "dictionaries", "en.dict"), "dictionary bytes")
	put(t, filepath.Join(dir, "cache", "peaks", "x.peaks"), "cache")
	put(t, filepath.Join(dir, "cache", "compat", "y.m4a"), "cache")
	put(t, filepath.Join(dir, "uploads", "incoming", "half.part"), "half an upload")
}

func newBackupTarget(t *testing.T) string { return t.TempDir() }

// TestWholeBackupMirror: the data folder's own files are copied, caches and uploads are not, and a second
// run copies nothing that has not changed.
func TestWholeBackupMirror(t *testing.T) {
	e := newEnv(t)
	seedData(t, e.dir)
	target := newBackupTarget(t)
	ctx := context.Background()
	if _, err := db.WriteCopy(ctx, e.st, e.dir, e.now); err != nil {
		t.Fatal(err)
	}
	rep, err := Mirror(ctx, e.dir, target, e.now)
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(target, RootName)
	if rep.Copied != 5 || rep.Copies != 1 {
		t.Errorf("first run = %+v, want 5 files and 1 database copy", rep)
	}
	for rel, want := range map[string]string{"ebooks/ab/book.epub": "epub bytes", "covers/c1.jpg": "cover bytes",
		"people/p1.jpg": "person bytes", "text/t1.json": "text bytes", "dictionaries/en.dict": "dictionary bytes"} {
		if got := read(t, filepath.Join(root, filepath.FromSlash(rel))); got != want {
			t.Errorf("%s = %q, want %q", rel, got, want)
		}
	}
	for _, skipped := range []string{"cache", "uploads", "tendshelf.db"} {
		if exists(filepath.Join(root, skipped)) {
			t.Errorf("%s was copied", skipped)
		}
	}
	if !strings.Contains(read(t, filepath.Join(root, "RESTORE.txt")), "Stop Tendshelf") {
		t.Error("RESTORE.txt is missing")
	}
	copies, err := db.ListCopies(root)
	if err != nil || len(copies) != 1 {
		t.Fatalf("database copies in the backup = %v, %v", copies, err)
	}
	if err := db.VerifyCopy(ctx, filepath.Join(root, "backups", copies[0].Name), copies[0].Version); err != nil {
		t.Errorf("database copy does not verify: %v", err)
	}

	// Nothing changed: nothing is copied.
	rep, err = Mirror(ctx, e.dir, target, e.now.Add(time.Hour))
	if err != nil || rep.Copied != 0 || rep.Same != 5 {
		t.Fatalf("second run = %+v, %v; want 5 unchanged", rep, err)
	}
	// One file changes (size and time): only it is copied again.
	put(t, filepath.Join(e.dir, "covers", "c1.jpg"), "a new, longer cover")
	rep, err = Mirror(ctx, e.dir, target, e.now.Add(2*time.Hour))
	if err != nil || rep.Copied != 1 || rep.Same != 4 {
		t.Fatalf("third run = %+v, %v; want 1 copied", rep, err)
	}
	if got := read(t, filepath.Join(root, "covers", "c1.jpg")); got != "a new, longer cover" {
		t.Errorf("changed file = %q", got)
	}
}

// TestWholeBackupRetention: database copies in the backup folder stay at the newest seven, and a file
// that left the data folder is kept for the grace period and then removed.
func TestWholeBackupRetention(t *testing.T) {
	e := newEnv(t)
	seedData(t, e.dir)
	target := newBackupTarget(t)
	ctx := context.Background()
	for i := 0; i < db.CopyKeep+3; i++ {
		if _, err := db.WriteCopy(ctx, e.st, e.dir, e.now.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Mirror(ctx, e.dir, target, e.now); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(target, RootName)
	if copies, _ := db.ListCopies(root); len(copies) != db.CopyKeep {
		t.Errorf("%d database copies kept, want %d", len(copies), db.CopyKeep)
	}
	// Add one more: the oldest in the backup folder goes.
	if _, err := db.WriteCopy(ctx, e.st, e.dir, e.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := Mirror(ctx, e.dir, target, e.now); err != nil {
		t.Fatal(err)
	}
	if copies, _ := db.ListCopies(root); len(copies) != db.CopyKeep || !copies[0].At.Equal(e.now.Add(time.Hour)) {
		t.Errorf("copies after rotation: %d, newest %v", len(copies), copies[0].At)
	}

	gone := filepath.Join(root, "covers", "c1.jpg")
	if err := os.Remove(filepath.Join(e.dir, "covers", "c1.jpg")); err != nil {
		t.Fatal(err)
	}
	if rep, err := Mirror(ctx, e.dir, target, e.now.Add(24*time.Hour)); err != nil || rep.Removed != 0 || !exists(gone) {
		t.Fatalf("a day after the delete: %+v, %v, kept %v", rep, err, exists(gone))
	}
	if rep, err := Mirror(ctx, e.dir, target, e.now.Add(24*time.Hour+Grace)); err != nil || rep.Removed != 1 || exists(gone) {
		t.Fatalf("after the grace period: %+v, %v, kept %v", rep, err, exists(gone))
	}
}

// TestWholeBackupDiskFull: a full backup folder stops the copy, leaves no half file, says so, and keeps
// what was already copied and recorded.
func TestWholeBackupDiskFull(t *testing.T) {
	e := newEnv(t)
	seedData(t, e.dir)
	put(t, filepath.Join(e.dir, "ebooks", "zz", "big.epub"), strings.Repeat("x", 1000))
	target := newBackupTarget(t)
	ctx := context.Background()
	old := wrapDest
	defer func() { wrapDest = old }()
	wrapDest = func(w io.Writer) io.Writer { return &fullAfter{w: w, left: 500} }
	_, err := Mirror(ctx, e.dir, target, e.now)
	if !errors.Is(err, ErrDiskFull) {
		t.Fatalf("err = %v, want ErrDiskFull", err)
	}
	root := filepath.Join(target, RootName)
	if exists(filepath.Join(root, "ebooks", "zz", "big.epub")) {
		t.Error("a half-written file took its place")
	}
	var parts []string
	filepath.WalkDir(root, func(p string, d os.DirEntry, _ error) error {
		if strings.HasSuffix(p, partSuffix) {
			parts = append(parts, p)
		}
		return nil
	})
	if len(parts) != 0 {
		t.Errorf("temporary files left: %v", parts)
	}
	var m manifest
	if b, rerr := os.ReadFile(filepath.Join(root, manifestName)); rerr != nil || json.Unmarshal(b, &m) != nil {
		t.Fatalf("manifest not written after a stop: %v", rerr)
	}
	// Space comes back: the next run finishes.
	wrapDest = old
	rep, err := Mirror(ctx, e.dir, target, e.now.Add(time.Hour))
	if err != nil || read(t, filepath.Join(root, "ebooks", "zz", "big.epub")) != strings.Repeat("x", 1000) {
		t.Fatalf("after space returned: %+v, %v", rep, err)
	}
	if rep.Same+rep.Copied != 6 {
		t.Errorf("run covered %d files, want 6", rep.Same+rep.Copied)
	}
}

type fullAfter struct {
	w    io.Writer
	left int
}

func (f *fullAfter) Write(p []byte) (int, error) {
	if len(p) > f.left {
		n, _ := f.w.Write(p[:f.left])
		f.left = 0
		return n, &os.PathError{Op: "write", Path: "backup", Err: syscall.ENOSPC}
	}
	f.left -= len(p)
	return f.w.Write(p)
}

// dropsLast writes everything but the last byte and says it wrote it all: a copy that does not read back.
type dropsLast struct{ w io.Writer }

func (d dropsLast) Write(p []byte) (int, error) {
	if len(p) > 1 {
		if _, err := d.w.Write(p[:len(p)-1]); err != nil {
			return 0, err
		}
		return len(p), nil
	}
	return d.w.Write(p)
}

// TestWholeBackupVerify: a copy that does not read back the same is refused and removed.
func TestWholeBackupVerify(t *testing.T) {
	e := newEnv(t)
	seedData(t, e.dir)
	target := newBackupTarget(t)
	old := wrapDest
	defer func() { wrapDest = old }()
	wrapDest = func(w io.Writer) io.Writer { return dropsLast{w} }
	_, err := Mirror(context.Background(), e.dir, target, e.now)
	if err == nil || !strings.Contains(err.Error(), "did not read back") {
		t.Fatalf("err = %v, want a read-back failure", err)
	}
	root := filepath.Join(target, RootName)
	if exists(filepath.Join(root, "ebooks", "ab", "book.epub")) || exists(filepath.Join(root, "ebooks", "ab", "book.epub"+partSuffix)) {
		t.Error("an unverified copy was left behind")
	}
}

// TestWholeBackupFolderRefused: the folder must exist, be a folder, accept writes, and sit outside the
// data folder and every library folder.
func TestWholeBackupFolderRefused(t *testing.T) {
	data := t.TempDir()
	lib := t.TempDir()
	elsewhere := t.TempDir()
	file := filepath.Join(elsewhere, "afile")
	put(t, file, "x")
	link := filepath.Join(elsewhere, "link")
	if err := os.Symlink(filepath.Join(data, "backups"), link); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(data, "backups"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(lib, "Author"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		target string
		want   error
	}{
		{"fine", elsewhere, nil},
		{"relative", "backups", ErrFolderRelative},
		{"missing", filepath.Join(elsewhere, "nope"), ErrFolderMissing},
		{"a file", file, ErrFolderNotDir},
		{"the data folder", data, ErrFolderInData},
		{"inside the data folder", filepath.Join(data, "backups"), ErrFolderInData},
		{"a link into the data folder", link, ErrFolderInData},
		{"a library folder", lib, ErrFolderInLibrary},
		{"inside a library folder", filepath.Join(lib, "Author"), ErrFolderInLibrary},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckFolder(c.target, data, []string{lib})
			if !errors.Is(err, c.want) || (c.want == nil && err != nil) {
				t.Errorf("CheckFolder = %v, want %v", err, c.want)
			}
		})
	}
	if os.Geteuid() != 0 {
		ro := t.TempDir()
		if err := os.Chmod(ro, 0o555); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(ro, 0o755)
		if err := CheckFolder(ro, data, nil); !errors.Is(err, ErrFolderNotWritten) {
			t.Errorf("read-only folder: %v", err)
		}
	}
}

// TestWholeBackupService: the service saves only an accepted folder, a backup fills it and reports the
// time, the schedule counts the folder, and a full folder becomes a plain job error while the local copy stays.
func TestWholeBackupService(t *testing.T) {
	e := newEnv(t)
	seedData(t, e.dir)
	ctx := context.Background()
	lib := t.TempDir()
	e.svc.LibraryFolders = func(context.Context) ([]string, error) { return []string{lib}, nil }
	if err := e.svc.SetFolder(ctx, filepath.Join(lib, "x")); err == nil {
		t.Fatal("a folder inside a library was saved")
	}
	if got := e.svc.Folder(ctx); got != "" {
		t.Fatalf("folder = %q after a refusal", got)
	}
	target := newBackupTarget(t)
	if err := e.svc.SetFolder(ctx, target); err != nil {
		t.Fatal(err)
	}
	// With a folder set and never filled, a day-old local copy is not enough: a backup is due.
	if _, err := db.WriteCopy(ctx, e.st, e.dir, e.now); err != nil {
		t.Fatal(err)
	}
	if due, err := e.svc.Due(ctx); err != nil || !due {
		t.Fatalf("Due = %v, %v; want due while the folder was never filled", due, err)
	}
	e.now = e.now.Add(time.Minute)
	res, err := e.svc.Backup(ctx, "manual")
	if err != nil || !res.Folder || res.Files != 5 {
		t.Fatalf("Backup = %+v, %v", res, err)
	}
	if _, ok := e.svc.FolderDone(ctx); !ok {
		t.Error("the time of the last whole backup was not recorded")
	}
	if due, _ := e.svc.Due(ctx); due {
		t.Error("due right after a whole backup")
	}

	// A full folder: the job error is plain, and the local copy was still made.
	old := wrapDest
	defer func() { wrapDest = old }()
	wrapDest = func(w io.Writer) io.Writer { return &fullAfter{w: w, left: 0} }
	put(t, filepath.Join(e.dir, "covers", "new.jpg"), "new cover")
	e.now = e.now.Add(2 * time.Hour)
	_, herr := e.svc.handle(ctx, &jobs.Task{Payload: json.RawMessage(`{"reason":"manual"}`)})
	var je *jobs.Error
	if !errors.As(herr, &je) || je.Code != "backup_folder_full" || je.Text != TextFolderFull {
		t.Fatalf("handle error = %v, want backup_folder_full", herr)
	}
	copies, _ := db.ListCopies(e.dir)
	if len(copies) == 0 || !copies[0].At.Equal(e.now.UTC()) {
		t.Error("the local database copy was not made before the folder filled up")
	}
	// A scheduled retry soon after does not pile up another local copy.
	n := len(copies)
	e.now = e.now.Add(10 * time.Minute)
	e.svc.handle(ctx, &jobs.Task{Payload: json.RawMessage(`{"reason":"scheduled"}`)})
	if again, _ := db.ListCopies(e.dir); len(again) != n {
		t.Errorf("a quick scheduled retry made another local copy (%d -> %d)", n, len(again))
	}

	// The folder is later put inside a library: the backup refuses and says so.
	wrapDest = old
	e.svc.LibraryFolders = func(context.Context) ([]string, error) { return []string{filepath.Dir(target)}, nil }
	_, herr = e.svc.handle(ctx, &jobs.Task{Payload: json.RawMessage(`{"reason":"manual"}`)})
	if !errors.As(herr, &je) || je.Code != "backup_folder_refused" {
		t.Fatalf("handle error = %v, want backup_folder_refused", herr)
	}
	if err := e.svc.SetFolder(ctx, ""); err != nil || e.svc.Folder(ctx) != "" {
		t.Errorf("clearing the folder: %v", err)
	}
}
