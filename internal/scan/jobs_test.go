package scan

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

// jobsEnv is an env whose scanner queues real jobs on a running runner.
func jobsEnv(t *testing.T, tools media.Tools) (*env, *jobs.Runner) {
	t.Helper()
	e := newEnv(t, tools)
	r, err := jobs.New(e.st, jobs.Options{
		Lease: 5 * time.Second, RenewEvery: time.Second, ProgressEvery: 50 * time.Millisecond,
		SweepEvery: time.Second, PollEvery: 10 * time.Millisecond,
		Backoff: func(int) time.Duration { return 10 * time.Millisecond },
		Logger:  slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatal(err)
	}
	e.sc = New(e.repo, tools, r, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := e.sc.Register(r); err != nil {
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
	return e, r
}

// idle waits until no job is queued or running.
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

func smallFixture(t *testing.T, e *env) *fixture {
	fx := newFixture(t, e.root)
	fx.file("A.m4b", mediatest.FakeFile{})
	fx.book("A.m4b", "", "A.m4b")
	for _, n := range []string{"01.mp3", "02.mp3", "03.mp3"} {
		fx.file("Au/B/"+n, mp3)
	}
	fx.book("Au/B", "", "Au/B/01.mp3", "Au/B/02.mp3", "Au/B/03.mp3")
	fx.file("Au/C/x.aax", mediatest.FakeFile{})
	fx.book("Au/C", "drm", "Au/C/x.aax")
	return fx
}

func TestJobsScanAndProbeEndToEnd(t *testing.T) {
	e, r := jobsEnv(t, mediatest.FakeTools(t))
	fx := smallFixture(t, e)
	id, err := r.Enqueue(bg, KindScan, ScanPayload{LibraryID: e.lib.ID}, jobs.EnqueueOptions{})
	if err != nil {
		t.Fatal(err)
	}
	idle(t, e)
	j, err := r.Get(bg, id)
	if err != nil || j.State != jobs.StateDone || len(j.Result) == 0 {
		t.Fatalf("scan job = %+v, %v", j, err)
	}
	fx.verify(e)
	if n := countJobs(t, e, `kind = 'media.probe' AND state = 'done'`); n != 4 {
		t.Fatalf("%d probe jobs done, want 4", n)
	}
}

func TestJobsBackToBackScansQueueNoDuplicateProbes(t *testing.T) {
	e, r := jobsEnv(t, mediatest.FakeTools(t))
	fx := smallFixture(t, e)
	for i := 0; i < 3; i++ {
		if _, err := r.Enqueue(bg, KindScan, ScanPayload{LibraryID: e.lib.ID}, jobs.EnqueueOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	idle(t, e)
	if n := countJobs(t, e, `kind = 'media.probe'`); n != 4 {
		t.Fatalf("%d probe jobs created for 4 files", n)
	}
	if n := countJobs(t, e, `kind = 'scan.library' AND state = 'done'`); n != 3 {
		t.Fatalf("%d scans done", n)
	}
	fx.verify(e)
}

func TestJobsWithoutFFprobeStillGroupBooks(t *testing.T) {
	e, r := jobsEnv(t, media.Tools{})
	smallFixture(t, e)
	if _, err := r.Enqueue(bg, KindScan, ScanPayload{LibraryID: e.lib.ID}, jobs.EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	idle(t, e)
	books, err := e.repo.BooksInFolder(bg, e.folder.ID)
	if err != nil || len(books) != 3 {
		t.Fatalf("books = %d, %v", len(books), err)
	}
	if n := countJobs(t, e, `kind = 'media.probe' AND state = 'failed' AND error_code = 'ffprobe_missing'`); n != 4 {
		t.Fatalf("%d probe jobs failed with ffprobe_missing, want 4", n)
	}
	// The protected book is refused without ffprobe: it is judged by its extension.
	if b := e.bookAt(e.root + "/Au/C"); b.RefusedReason != "drm" {
		t.Fatalf("protected book = %+v", b)
	}
}

func TestJobsBadPayloads(t *testing.T) {
	e, r := jobsEnv(t, mediatest.FakeTools(t))
	cases := []struct {
		kind    string
		payload any
		code    string
	}{
		{KindScan, map[string]string{}, "bad_payload"},
		{KindScan, ScanPayload{LibraryID: "lib_nope"}, "not_found"},
		{KindProbe, map[string]string{}, "bad_payload"},
	}
	for _, c := range cases {
		id, err := r.Enqueue(bg, c.kind, c.payload, jobs.EnqueueOptions{})
		if err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(10 * time.Second)
		for {
			j, err := r.Get(bg, id)
			if err != nil {
				t.Fatal(err)
			}
			if j.State == jobs.StateFailed {
				if j.ErrorCode != c.code {
					t.Errorf("%s %v: code = %q, want %q", c.kind, c.payload, j.ErrorCode, c.code)
				}
				if j.Attempts != 1 {
					t.Errorf("%s: attempts = %d, a permanent error must not retry", c.kind, j.Attempts)
				}
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("%s %v: state %q", c.kind, c.payload, j.State)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	// An unknown file is not an error: the row may have been deleted after the job was queued.
	id, _ := r.Enqueue(bg, KindProbe, ProbePayload{AudioFileID: "af_nope"}, jobs.EnqueueOptions{})
	idle(t, e)
	if j, _ := r.Get(bg, id); j.State != jobs.StateDone || string(j.Result) != `{"outcome":"gone"}` {
		t.Fatalf("probe of a vanished row = %+v", j)
	}
}
