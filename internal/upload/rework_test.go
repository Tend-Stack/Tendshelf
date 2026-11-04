package upload

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

var tagsXA = map[string]string{"album": "T", "artist": "X"}

// A plain file system error (EACCES, EROFS, an unplugged NAS) must never leave the upload in
// placing: it becomes failed, with a message and a retryable job error.
func TestPlainErrorEndsInFailedAndRetries(t *testing.T) {
	e := newEnv(t, nil)
	v := e.send("A", book{"A/01.mp3": mp3(t, tagsXA)})
	denied := &os.PathError{Op: "mkdir", Path: e.root, Err: syscall.EACCES}
	e.svc.crash = func(p string) error {
		if p == "after_dirs" {
			return denied
		}
		return nil
	}
	_, err := e.place(v.ID)
	var je *jobs.Error
	if !errors.As(err, &je) || je.Permanent || je.Code != "place_failed" || !errors.Is(err, syscall.EACCES) {
		t.Fatalf("err = %v", err)
	}
	if u := e.upload(v.ID); u.State != StateFailed || u.ErrorCode != "place_failed" {
		t.Fatalf("upload = %+v", u)
	}
	if _, err := os.Stat(e.svc.partPath(v.ID, v.Files[0].ID)); err != nil {
		t.Errorf("staging lost: %v", err)
	}
	// The disk comes back; the runner's next attempt (or Try again) places the book.
	e.svc.crash = func(string) error { return nil }
	if res := e.mustPlace(v.ID); res.State != StatePlaced {
		t.Errorf("retry: %+v", res)
	}
}

func TestPlainErrorInInspectAndRecoveryEndsInFailed(t *testing.T) {
	e := newEnv(t, nil)
	v := e.send("A", book{"A/01.mp3": mp3(t, tagsXA)})
	// The library folder vanishes (an unplugged drive): opening it fails before anything else.
	gone := e.root + "-gone"
	if err := os.Rename(e.root, gone); err != nil {
		t.Fatal(err)
	}
	_, err := e.place(v.ID)
	if err == nil {
		t.Fatal("placed into a missing folder")
	}
	if u := e.upload(v.ID); u.State != StateFailed {
		t.Errorf("state = %s", u.State)
	}
	if err := os.Rename(gone, e.root); err != nil {
		t.Fatal(err)
	}
	if res := e.mustPlace(v.ID); res.State != StatePlaced {
		t.Errorf("retry: %+v", res)
	}
}

func TestSweepFailsStuckUploadsAndExpiresOldFailedOnes(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	e := newEnv(t, func(c *Config) { c.Now = func() time.Time { return now } })
	stuck, _ := oneFile(t, e, 64)
	live, _ := oneFile(t, e, 64)
	failed, _ := oneFile(t, e, 64)
	old := now.Add(-25 * time.Hour).UnixMilli()
	if _, err := e.st.Writer().Exec(`INSERT INTO jobs(id, kind, state, payload_json, created_at, updated_at) VALUES ('jb_live', 'upload.place', 'queued', '{}', 1, 1)`); err != nil {
		t.Fatal(err)
	}
	for _, q := range []struct {
		id, state, job string
		at             int64
	}{
		{stuck.ID, "placing", "", old},
		{live.ID, "placing", "jb_live", old},
		{failed.ID, "failed", "", now.Add(-8 * 24 * time.Hour).UnixMilli()},
	} {
		if _, err := e.st.Writer().Exec(`UPDATE uploads SET state = ?, job_id = ?, updated_at = ? WHERE id = ?`, q.state, nullIfEmpty(q.job), q.at, q.id); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.svc.Sweep(bg); err != nil {
		t.Fatal(err)
	}
	if u := e.upload(stuck.ID); u.State != StateFailed || u.ErrorCode != "stuck" {
		t.Errorf("stuck upload = %+v", u)
	}
	if u := e.upload(live.ID); u.State != StatePlacing {
		t.Errorf("an upload with a live job was failed: %s", u.State)
	}
	if u := e.upload(failed.ID); u.State != StateExpired {
		t.Errorf("old failed upload is %s", u.State)
	}
	if _, err := os.Stat(e.svc.stagingDir(failed.ID)); !os.IsNotExist(err) {
		t.Errorf("old failed staging kept: %v", err)
	}
	if _, err := os.Stat(e.svc.stagingDir(stuck.ID)); err != nil {
		t.Errorf("a freshly failed upload lost its staging: %v", err)
	}
}

func TestChunkPartShorterThanOffsetRewinds(t *testing.T) {
	for _, how := range []string{"short", "deleted"} {
		t.Run(how, func(t *testing.T) {
			e := newEnv(t, func(c *Config) { c.ChunkSize = 1024 })
			v, data := oneFile(t, e, 2048)
			f := v.Files[0]
			if _, err := e.svc.WriteChunk(bg, me, v.ID, f.ID, 0, "", bytes.NewReader(data[:1024])); err != nil {
				t.Fatal(err)
			}
			part := e.svc.partPath(v.ID, f.ID)
			want := int64(100)
			if how == "deleted" {
				want = 0
				if err := os.Remove(part); err != nil {
					t.Fatal(err)
				}
			} else if err := os.Truncate(part, 100); err != nil {
				t.Fatal(err)
			}
			_, err := e.svc.WriteChunk(bg, me, v.ID, f.ID, 1024, "", bytes.NewReader(data[1024:]))
			if ue := wantErr(t, err, 409, "wrong_offset"); ue.Offset != want {
				t.Fatalf("offset = %d, want %d", ue.Offset, want)
			}
			if got := offsetOf(t, e, v); got != want {
				t.Errorf("stored offset = %d", got)
			}
			if u := e.upload(v.ID); u.BytesReceived != want {
				t.Errorf("upload total = %d", u.BytesReceived)
			}
			// Carrying on from the real offset gives the true bytes, with no zero-filled gap.
			g, _ := e.svc.Get(bg, me, v.ID)
			e.sendFrom(g, book{"a.mp3": data}, nil)
			got, _ := os.ReadFile(part)
			if !bytes.Equal(got, data) {
				t.Error("the staged file differs from the upload")
			}
		})
	}
}

func TestChunkConcurrentPutsToOneFile(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.ChunkSize = 1024 })
	v, data := oneFile(t, e, 2048)
	f := v.Files[0]
	var wg sync.WaitGroup
	results := make([]error, 6)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, results[i] = e.svc.WriteChunk(bg, me, v.ID, f.ID, 0, "", bytes.NewReader(data[:1024]))
		}()
	}
	wg.Wait()
	ok := 0
	for _, err := range results {
		if err == nil {
			ok++
			continue
		}
		wantErr(t, err, 409, "wrong_offset")
	}
	if ok != 1 {
		t.Errorf("%d writes won, want exactly one", ok)
	}
	if got := offsetOf(t, e, v); got != 1024 || partSize(t, e, v) != 1024 {
		t.Errorf("offset %d part %d", got, partSize(t, e, v))
	}
}

func TestCancelNeverOverwrittenByPlacement(t *testing.T) {
	// A retry arriving after a cancel stops at once and leaves the upload cancelled.
	e := newEnv(t, nil)
	v := e.send("A", book{"A/01.mp3": mp3(t, tagsXA)})
	if _, err := e.st.Writer().Exec(`UPDATE uploads SET state = 'failed' WHERE id = ?`, v.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.svc.Cancel(bg, me, v.ID); err != nil {
		t.Fatal(err)
	}
	res := e.mustPlace(v.ID)
	if res.State != StateCancelled || e.upload(v.ID).State != StateCancelled {
		t.Errorf("result %+v, upload %s", res, e.upload(v.ID).State)
	}
	if got := tree(t, e.root); len(got) != 0 {
		t.Errorf("a cancelled upload was placed: %v", got)
	}
	if len(e.enq.of(scan.KindScan)) != 0 {
		t.Error("a cancelled upload queued an import")
	}
}

func TestCancelRacesFinish(t *testing.T) {
	for i := 0; i < 20; i++ {
		e := newEnv(t, func(c *Config) { c.ChunkSize = 1024 })
		v, data := oneFile(t, e, 1024)
		e.sendFrom(v, book{"a.mp3": data}, nil)
		var wg sync.WaitGroup
		var finErr, canErr error
		wg.Add(2)
		go func() { defer wg.Done(); _, finErr = e.svc.Finish(bg, me, v.ID) }()
		go func() { defer wg.Done(); canErr = e.svc.Cancel(bg, me, v.ID) }()
		wg.Wait()
		u := e.upload(v.ID)
		switch u.State {
		case StateQueued:
			if finErr != nil || canErr == nil {
				t.Fatalf("queued but finish=%v cancel=%v", finErr, canErr)
			}
		case StateCancelled:
			if canErr != nil || finErr == nil {
				t.Fatalf("cancelled but finish=%v cancel=%v", finErr, canErr)
			}
			if _, err := os.Stat(e.svc.stagingDir(v.ID)); !os.IsNotExist(err) {
				t.Fatalf("cancelled but staging kept")
			}
			if res := e.mustPlace(v.ID); res.State != StateCancelled {
				t.Fatalf("placement of a cancelled upload: %+v", res)
			}
		default:
			t.Fatalf("state %s", u.State)
		}
	}
}

func TestLibraryFilesAreIndependentOfStaging(t *testing.T) {
	e := newEnv(t, nil)
	b := greatBook(t)
	v := e.send("My Book", b)
	partFi, err := os.Stat(e.svc.partPath(v.ID, v.Files[0].ID))
	if err != nil {
		t.Fatal(err)
	}
	e.mustPlace(v.ID)
	if _, err := os.Stat(e.svc.stagingDir(v.ID)); !os.IsNotExist(err) {
		t.Fatal("staging kept")
	}
	for name, data := range map[string][]byte{"01.mp3": b["My Book/01.mp3"], "02.mp3": b["My Book/02.mp3"], "cover.jpg": b["My Book/cover.jpg"]} {
		p := filepath.Join(e.root, "Ann Author", "Great Title", name)
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		st := fi.Sys().(*syscall.Stat_t)
		if st.Nlink != 1 {
			t.Errorf("%s has %d links: it shares an inode with something", name, st.Nlink)
		}
		if name == "01.mp3" && st.Ino == partFi.Sys().(*syscall.Stat_t).Ino {
			t.Errorf("%s kept the staging inode", name)
		}
		got, _ := os.ReadFile(p)
		if !bytes.Equal(got, data) {
			t.Errorf("%s content differs after staging was removed", name)
		}
	}
}

// Someone with write access to the folder swaps a parent for a symbolic link between the check and
// the create. Nothing may be created outside the upload folder.
func TestSymlinkedParentSwappedAfterCheck(t *testing.T) {
	for _, point := range []string{"after_dirs", "after_files"} {
		t.Run(point, func(t *testing.T) {
			e := newEnv(t, nil)
			outside := t.TempDir()
			v := e.send("A", book{"A/01.mp3": mp3(t, tagsXA)})
			swapped := false
			e.svc.crash = func(p string) error {
				if p != point || swapped {
					return nil
				}
				swapped = true
				aside := e.root + "-aside"
				if err := os.Rename(filepath.Join(e.root, "X"), aside); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = os.RemoveAll(aside) })
				if err := os.Symlink(outside, filepath.Join(e.root, "X")); err != nil {
					t.Fatal(err)
				}
				return nil
			}
			_, err := e.place(v.ID)
			if !swapped {
				t.Fatal("the hook never ran")
			}
			if err == nil {
				t.Fatal("placement went through a swapped link")
			}
			if entries, _ := os.ReadDir(outside); len(entries) != 0 {
				t.Errorf("something was created outside the upload folder: %v", entries)
			}
			if u := e.upload(v.ID); u.State != StateFailed {
				t.Errorf("state = %s", u.State)
			}
		})
	}
}

func TestProbeTimeoutFailsPlacement(t *testing.T) {
	e := newEnv(t, nil)
	e.svc.tools = mediatest.FakeTools(t)
	e.svc.tools.ProbeTimeout = 100 * time.Millisecond
	slow := fakeAudio(t, mediatest.FakeFile{Container: "mp3", Codec: "mp3", SleepMs: 3000})
	v := e.send("A", book{"A/01.mp3": mp3(t, tagsXA), "A/02.mp3": slow})
	_, err := e.place(v.ID)
	var je *jobs.Error
	if !errors.As(err, &je) || je.Code != "probe_timeout" || je.Permanent || je.Text == "" {
		t.Fatalf("err = %v", err)
	}
	if got := tree(t, e.root); len(got) != 0 {
		t.Errorf("a file that did not probe was placed: %v", got)
	}
	if u := e.upload(v.ID); u.State != StateFailed || u.ErrorCode != "probe_timeout" {
		t.Errorf("upload = %+v", u)
	}
	if _, err := os.Stat(e.svc.partPath(v.ID, v.Files[0].ID)); err != nil {
		t.Errorf("staging lost: %v", err)
	}
}

func TestPlacementChecksPromisedSHA256(t *testing.T) {
	e := newEnv(t, nil)
	data := mp3(t, tagsXA)
	for name, c := range map[string]struct {
		sum  string
		want string
	}{
		"right": {hexSum(data), StatePlaced},
		"wrong": {hexSum([]byte("other")), StateFailed},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, nil)
			m := manifestOf("A", book{"A/01.mp3": data})
			m.Files[0].SHA256 = c.sum
			v, err := e.svc.Create(bg, me, e.lib.ID, m)
			if err != nil {
				t.Fatal(err)
			}
			e.sendFrom(v, book{"A/01.mp3": data}, nil)
			if _, err := e.svc.Finish(bg, me, v.ID); err != nil {
				t.Fatal(err)
			}
			_, err = e.place(v.ID)
			if got := e.upload(v.ID).State; got != c.want {
				t.Errorf("state = %s (err %v), want %s", got, err, c.want)
			}
			if c.want == StateFailed && len(tree(t, e.root)) != 0 {
				t.Errorf("a damaged file was placed")
			}
		})
	}
	m := manifestOf("A", book{"A/01.mp3": data})
	m.Files[0].SHA256 = "zz"
	_, err := e.svc.Create(bg, me, e.lib.ID, m)
	wantErr(t, err, 422, "bad_checksum")
}

// A crash left temp_path and book_path set, the build folder gone and the book folder in place but
// with no staged rows: recovery clears both and carries on instead of failing on every retry.
func TestRecoveryWithoutStagedRowsDoesNotLoop(t *testing.T) {
	e := newEnv(t, nil)
	v := e.send("A", book{"A/01.mp3": mp3(t, tagsXA)})
	mk(t, e.root, "X/T/theirs.txt")
	if _, err := e.st.Writer().Exec(`UPDATE uploads SET temp_path = ?, book_path = ? WHERE id = ?`,
		filepath.Join(e.root, "X", tempPrefix+v.ID), filepath.Join(e.root, "X", "T"), v.ID); err != nil {
		t.Fatal(err)
	}
	res := e.mustPlace(v.ID)
	if res.State != StatePlaced {
		t.Fatalf("result = %+v", res)
	}
	if b, _ := os.ReadFile(filepath.Join(e.root, "X", "T", "theirs.txt")); string(b) != "x" {
		t.Error("their folder changed")
	}
	if _, err := os.Stat(filepath.Join(e.root, "X", "T (2)", "01.mp3")); err != nil {
		t.Errorf("book not at (2): %v", tree(t, e.root))
	}
}

func TestPerPersonQuotaCountsFailedUploads(t *testing.T) {
	e := newEnv(t, func(c *Config) { c.PerPerson = 250 })
	one := func() Manifest {
		return Manifest{Label: "B", Files: []FileIn{{Path: "a.mp3", Size: 100}}}
	}
	v, err := e.svc.Create(bg, me, e.lib.ID, one())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.st.Writer().Exec(`UPDATE uploads SET state = 'failed' WHERE id = ?`, v.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Create(bg, me, e.lib.ID, one()); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Create(bg, me, e.lib.ID, one())
	if ue := wantErr(t, err, 507, "quota"); !strings.Contains(ue.Message, "waiting") {
		t.Errorf("message = %q", ue.Message)
	}
	// Another person has their own allowance.
	if _, err := e.svc.Create(bg, "us_other", e.lib.ID, one()); err != nil {
		t.Errorf("another person: %v", err)
	}
}
