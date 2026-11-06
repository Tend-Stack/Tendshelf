package backups

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
)

func skipIfRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permissions do not stop root")
	}
}

// TestBackupDriveUnplugged: after the folder was filled, an empty folder (the mount point of a drive that
// went away) is refused with nothing written; saving the folder again starts a new backup on purpose.
func TestBackupDriveUnplugged(t *testing.T) {
	e := newEnv(t)
	seedData(t, e.dir)
	ctx := context.Background()
	target := newBackupTarget(t)
	if err := e.svc.SetFolder(ctx, target); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Backup(ctx, "manual"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(target, RootName)); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(time.Hour)
	_, err := e.svc.Backup(ctx, "manual")
	if !errors.Is(err, ErrDriveMissing) {
		t.Fatalf("err = %v, want ErrDriveMissing", err)
	}
	if exists(filepath.Join(target, RootName)) {
		t.Error("the empty folder was filled")
	}
	e.now = e.now.Add(time.Hour)
	_, herr := e.svc.handle(ctx, &jobs.Task{Payload: json.RawMessage(`{"reason":"manual"}`)})
	var je *jobs.Error
	if !errors.As(herr, &je) || je.Code != "backup_drive_missing" || je.Text != TextDriveMissing {
		t.Fatalf("handle error = %v", herr)
	}
	if err := e.svc.SetFolder(ctx, target); err != nil {
		t.Fatal(err)
	}
	e.now = e.now.Add(time.Hour)
	if _, err := e.svc.Backup(ctx, "manual"); err != nil || !HasBackup(target) {
		t.Fatalf("starting over: %v", err)
	}
}

// TestMirrorUnreadableFolderKeepsBackup: a data folder that cannot be listed is not "gone".
func TestMirrorUnreadableFolderKeepsBackup(t *testing.T) {
	skipIfRoot(t)
	e := newEnv(t)
	seedData(t, e.dir)
	target := newBackupTarget(t)
	ctx := context.Background()
	if _, err := Mirror(ctx, e.dir, target, e.now); err != nil {
		t.Fatal(err)
	}
	cov := filepath.Join(e.dir, "covers")
	os.Chmod(cov, 0)
	t.Cleanup(func() { os.Chmod(cov, 0o755) })
	var last Report
	for _, d := range []time.Duration{24 * time.Hour, Grace + 48*time.Hour, 2*Grace + 72*time.Hour} {
		rep, err := Mirror(ctx, e.dir, target, e.now.Add(d))
		if err != nil {
			t.Fatal(err)
		}
		last = rep
	}
	if last.Problems == 0 || last.Removed != 0 {
		t.Errorf("report = %+v, want the problem counted and nothing removed", last)
	}
	if !exists(filepath.Join(target, RootName, "covers", "c1.jpg")) {
		t.Error("the good backup of an unreadable folder was removed")
	}
}

// TestMirrorBadFileDoesNotBlockCopies: one unreadable cover is counted, the rest and the database copies
// still go into the backup.
func TestMirrorBadFileDoesNotBlockCopies(t *testing.T) {
	skipIfRoot(t)
	e := newEnv(t)
	seedData(t, e.dir)
	ctx := context.Background()
	if _, err := db.WriteCopy(ctx, e.st, e.dir, e.now); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(e.dir, "covers", "c1.jpg")
	os.Chmod(bad, 0)
	t.Cleanup(func() { os.Chmod(bad, 0o644) })
	target := newBackupTarget(t)
	rep, err := Mirror(ctx, e.dir, target, e.now)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Problems != 1 || rep.Copies != 1 || rep.Copied != 4 {
		t.Errorf("report = %+v", rep)
	}
}

// TestMirrorCopiesBeforeDiskFull: a full disk still leaves the database copies in the backup.
func TestMirrorCopiesBeforeDiskFull(t *testing.T) {
	e := newEnv(t)
	seedData(t, e.dir)
	ctx := context.Background()
	if _, err := db.WriteCopy(ctx, e.st, e.dir, e.now); err != nil {
		t.Fatal(err)
	}
	target := newBackupTarget(t)
	// Let the database copy (the first write) through, then fill up on the mirrored files.
	old := wrapDest
	defer func() { wrapDest = old }()
	calls := 0
	wrapDest = func(w io.Writer) io.Writer {
		calls++
		if calls == 1 {
			return w
		}
		return &fullAfter{w: w, left: 0}
	}
	rep, err := Mirror(ctx, e.dir, target, e.now)
	if !errors.Is(err, ErrDiskFull) || rep.Copies != 1 {
		t.Fatalf("rep = %+v, err = %v; want the database copy placed before the disk filled", rep, err)
	}
}

// TestMirrorSymlinkedFolder: a mirrored folder that is a link is followed, and swapping a real folder for
// a link later does not make its files look gone.
func TestMirrorSymlinkedFolder(t *testing.T) {
	e := newEnv(t)
	seedData(t, e.dir)
	target := newBackupTarget(t)
	ctx := context.Background()
	if _, err := Mirror(ctx, e.dir, target, e.now); err != nil {
		t.Fatal(err)
	}
	elsewhere := filepath.Join(t.TempDir(), "covers-real")
	if err := os.Rename(filepath.Join(e.dir, "covers"), elsewhere); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(elsewhere, filepath.Join(e.dir, "covers")); err != nil {
		t.Fatal(err)
	}
	for _, d := range []time.Duration{time.Hour, Grace + 48*time.Hour, 2*Grace + 72*time.Hour} {
		rep, err := Mirror(ctx, e.dir, target, e.now.Add(d))
		if err != nil || rep.Removed != 0 || rep.Problems != 0 {
			t.Fatalf("rep = %+v, err = %v", rep, err)
		}
	}
	if !exists(filepath.Join(target, RootName, "covers", "c1.jpg")) {
		t.Error("the covers were removed from the backup")
	}
}

// TestRetryAfterFailedFolderKeepsHistory: hourly retries of a failing backup folder must not rotate the
// older database copies out, even when the failed run's copy is more than an hour old.
func TestRetryAfterFailedFolderKeepsHistory(t *testing.T) {
	e := newEnv(t)
	seedData(t, e.dir)
	ctx := context.Background()
	if err := e.svc.SetFolder(ctx, newBackupTarget(t)); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Backup(ctx, "manual"); err != nil {
		t.Fatal(err)
	}
	old := wrapDest
	defer func() { wrapDest = old }()
	wrapDest = func(w io.Writer) io.Writer { return &fullAfter{w: w, left: 0} }
	put(t, filepath.Join(e.dir, "covers", "new.jpg"), "new cover")
	e.now = e.now.Add(48 * time.Hour)
	if _, err := e.svc.Backup(ctx, "scheduled"); err == nil {
		t.Fatal("expected the folder to fail")
	}
	before, _ := db.ListCopies(e.dir)
	for i := 0; i < 8; i++ {
		e.now = e.now.Add(time.Hour + time.Minute)
		e.svc.Backup(ctx, "scheduled")
	}
	after, _ := db.ListCopies(e.dir)
	if len(after) != len(before) {
		t.Errorf("copies %d -> %d across retries", len(before), len(after))
	}
}
