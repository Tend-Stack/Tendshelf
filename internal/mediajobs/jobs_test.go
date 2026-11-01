package mediajobs

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

// jobsEnv is an env whose scanner and media services run on a real job runner: the cover follower
// is registered on the scanner, as cmd/tendshelf does.
func jobsEnv(t *testing.T, tools media.Tools, ffLog string) (*env, *jobs.Runner, *Covers, *Compat) {
	t.Helper()
	e := newEnvWith(t, tools, ffLog)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r, err := jobs.New(e.st, jobs.Options{
		Lease: 5 * time.Second, RenewEvery: time.Second, ProgressEvery: 50 * time.Millisecond,
		SweepEvery: time.Second, PollEvery: 10 * time.Millisecond,
		Backoff: func(int) time.Duration { return 10 * time.Millisecond }, Logger: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	covers := NewCovers(e.repo, tools, e.data, r, log)
	compat := NewCompat(e.repo, tools, CompatDir(e.data), 1<<30, r, log)
	e.sc = scan.New(e.repo, tools, r, log)
	e.sc.OnBookSettled(covers.Follower())
	if err := e.sc.Register(r); err != nil {
		t.Fatal(err)
	}
	if err := Register(r, covers, compat); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(bg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(bg, 5*time.Second)
		defer cancel()
		_ = r.Stop(ctx)
	})
	return e, r, covers, compat
}

func idle(t *testing.T, e *env) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		var n int
		if err := e.st.Reader().QueryRow(`SELECT count(*) FROM jobs WHERE state IN ('queued','running')`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%d jobs still active", n)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func countJobs(t *testing.T, e *env, where string, args ...any) int {
	t.Helper()
	var n int
	if err := e.st.Reader().QueryRow(`SELECT count(*) FROM jobs WHERE `+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestJobsCoverFollowerAndCompat(t *testing.T) {
	tools := mediatest.FakeTools(t)
	e, r, _, compat := jobsEnv(t, tools, "")
	e.fake("A/01.m4b", mediatest.FakeFile{})
	e.image("A/cover.jpg", "#2c6660", 100, 100)
	e.fake("B/01.m4b", mediatest.FakeFile{Cover: true})
	e.fake("C/01.m4b", mediatest.FakeFile{})
	e.fake("Opus/01.opus", opus)
	if err := os.MkdirAll(e.path("D"), 0o755); err != nil {
		t.Fatal(err)
	}
	mediatest.WriteProtected(t, e.path("D/01.aax"))

	if _, err := r.Enqueue(bg, scan.KindScan, scan.ScanPayload{LibraryID: e.lib.ID}, jobs.EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	idle(t, e)

	// One cover job per settled book (five books, the refused one included), all done.
	if n := countJobs(t, e, `kind = ? AND state = 'done'`, KindCover); n != 5 {
		t.Errorf("%d cover jobs done, want 5", n)
	}
	if n := countJobs(t, e, `kind = ?`, KindCover); n != 5 {
		t.Errorf("%d cover jobs in all, want 5", n)
	}
	for rel, want := range map[string]bool{"A": true, "B": true, "C": false, "Opus": false, "D": false} {
		if got := e.book(rel).CoverID != ""; got != want {
			t.Errorf("book %s: has cover = %v, want %v", rel, got, want)
		}
	}

	// A scan that finds nothing new queues no cover job.
	if _, err := r.Enqueue(bg, scan.KindScan, scan.ScanPayload{LibraryID: e.lib.ID}, jobs.EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	idle(t, e)
	if n := countJobs(t, e, `kind = ?`, KindCover); n != 5 {
		t.Errorf("%d cover jobs after a quiet rescan, want 5", n)
	}

	// A compat request asked for twice is converted once.
	id := e.file("Opus").ID
	for i := 0; i < 2; i++ {
		if _, err := compat.Request(bg, id); err != nil {
			t.Fatal(err)
		}
	}
	idle(t, e)
	if n := countJobs(t, e, `kind = ? AND state = 'done'`, KindCompat); n != 1 {
		t.Errorf("%d compat jobs done, want 1", n)
	}
	if n := countJobs(t, e, `kind = ?`, KindCompat); n != 1 {
		t.Errorf("%d compat jobs in all, want 1", n)
	}
	if p, ok, err := compat.Lookup(bg, id); err != nil || !ok {
		t.Errorf("Lookup after the job = %q, %v, %v", p, ok, err)
	}
	if n := countJobs(t, e, `state = 'failed'`); n != 0 {
		t.Errorf("%d failed jobs", n)
	}
}

func TestJobsMissingFFmpegFailsAtOnce(t *testing.T) {
	tools := mediatest.FakeTools(t)
	e, r, covers, _ := jobsEnv(t, tools, "")
	covers.tools.FFmpeg = "" // before any job exists, so nothing runs concurrently with this
	e.fake("B/01.m4b", mediatest.FakeFile{Cover: true})
	e.scanAll() // the follower queues the book's cover job, which fails too
	id, err := r.Enqueue(bg, KindCover, CoverPayload{BookID: e.book("B").ID}, jobs.EnqueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	idle(t, e)
	if n := countJobs(t, e, `kind = ? AND state = 'failed' AND error_code = ?`, KindCover, media.CodeFFmpegMissing); n != 2 {
		t.Errorf("%d cover jobs failed with ffmpeg_missing, want 2", n)
	}
	j, err := r.Get(bg, id)
	if err != nil {
		t.Fatal(err)
	}
	if j.State != jobs.StateFailed || j.ErrorCode != media.CodeFFmpegMissing || j.Attempts != 1 || j.ErrorText == "" {
		t.Errorf("job = %+v", j)
	}
}
