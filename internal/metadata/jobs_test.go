package metadata_test

import (
	"context"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

func TestFollowerQueuesOneJobPerSettledBook(t *testing.T) {
	tools := mediatest.FakeTools(t)
	e := newEnv(t, tools)
	r, err := jobs.New(e.st, jobs.Options{
		Lease: 5 * time.Second, RenewEvery: time.Second, ProgressEvery: 50 * time.Millisecond,
		SweepEvery: time.Second, PollEvery: 10 * time.Millisecond,
		Backoff: func(int) time.Duration { return 10 * time.Millisecond }, Logger: quiet(),
	})
	if err != nil {
		t.Fatal(err)
	}
	e.svc = metadata.New(e.repo, r, quiet())
	e.sc = scan.New(e.repo, tools, r, quiet())
	e.sc.OnBookSettled(e.svc.Follower())
	if err := e.svc.Register(r); err != nil {
		t.Fatal(err)
	}
	if err := e.sc.Register(r); err != nil {
		t.Fatal(err)
	}

	e.write("Ann Author/One/x.m4b", mediatest.FakeFile{Tags: map[string]string{"album": "First Tagged"}})
	e.write("Ann Author/Two/y.m4b", mediatest.FakeFile{})
	e.write("Three.m4b", mediatest.FakeFile{})

	// The runner is not started yet, so the jobs stay queued and can be counted.
	e.scanAndProbe()
	queued := func() int {
		return e.count(`SELECT count(*) FROM jobs WHERE kind = ? AND state = 'queued'`, metadata.Kind)
	}
	if n := queued(); n != 3 {
		t.Fatalf("%d details jobs after scan and probes, want 3", n)
	}
	ids := []string{e.book("Ann Author/One").ID, e.book("Ann Author/Two").ID, e.book("Three.m4b").ID}
	e.svc.Follower()(bg, ids)
	e.svc.Follower()(bg, ids)
	if n := queued(); n != 3 {
		t.Fatalf("%d details jobs after the follower ran twice more, want 3", n)
	}

	if err := r.Start(bg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(bg, 5*time.Second)
		defer cancel()
		_ = r.Stop(ctx)
	})
	deadline := time.Now().Add(20 * time.Second)
	for e.count(`SELECT count(*) FROM jobs WHERE state IN ('queued','running')`) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("jobs did not finish")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := e.count(`SELECT count(*) FROM jobs WHERE kind = ? AND state = 'done'`, metadata.Kind); n != 3 {
		t.Fatalf("%d details jobs succeeded, want 3", n)
	}
	if got := e.book("Ann Author/One"); got.Title != "First Tagged" {
		t.Fatalf("title = %q", got.Title)
	}
	if got := e.book("Ann Author/Two"); got.Title != "Two" {
		t.Fatalf("title = %q", got.Title)
	}
	if n := e.count(`SELECT count(*) FROM book_people WHERE role = 'author'`); n != 2 {
		t.Fatalf("%d author links, want 2", n)
	}

	// EnqueueLibrary refreshes every book and skips those already waiting.
	n, err := e.svc.EnqueueLibrary(bg, e.lib.ID)
	if err != nil || n != 3 {
		t.Fatalf("EnqueueLibrary = %d, %v", n, err)
	}
	if _, err := e.svc.EnqueueLibrary(bg, "lib_nope"); err == nil {
		t.Fatal("EnqueueLibrary accepted an unknown library")
	}
}

func TestHandlerRejectsBadPayload(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	r, err := jobs.New(e.st, jobs.Options{
		Lease: 5 * time.Second, RenewEvery: time.Second, ProgressEvery: 50 * time.Millisecond,
		SweepEvery: time.Second, PollEvery: 10 * time.Millisecond,
		Backoff: func(int) time.Duration { return 10 * time.Millisecond }, Logger: quiet(),
	})
	if err != nil {
		t.Fatal(err)
	}
	svc := metadata.New(e.repo, r, quiet())
	if err := svc.Register(r); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Enqueue(bg, metadata.Kind, map[string]string{"book_id": ""}, jobs.EnqueueOptions{}); err != nil {
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
	deadline := time.Now().Add(10 * time.Second)
	for e.count(`SELECT count(*) FROM jobs WHERE kind = ? AND state = 'failed'`, metadata.Kind) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("bad payload job did not fail")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := e.count(`SELECT count(*) FROM jobs WHERE kind = ? AND attempts > 1`, metadata.Kind); n != 0 {
		t.Fatalf("bad payload was retried (%d)", n)
	}
}
