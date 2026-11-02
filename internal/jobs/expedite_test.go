package jobs_test

import (
	"context"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
)

func TestExpediteMovesAFutureJobAheadOfOlderOnes(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, t.TempDir())
	r := newRunner(t, st, quick("k")) // never started: ClaimOne claims by hand
	now := time.Now()

	older := enqueue(t, r, "k", nil, jobs.EnqueueOptions{NotBefore: now.Add(-time.Minute)})
	time.Sleep(2 * time.Millisecond)
	future := enqueue(t, r, "k", nil, jobs.EnqueueOptions{NotBefore: now.Add(-time.Second)})
	// Without Expedite the older job (earlier not_before) is claimed first.
	if err := r.Expedite(ctx, future); err != nil {
		t.Fatal(err)
	}
	if got := get(t, r, future); got.NotBefore.UnixMilli() > 0 {
		t.Errorf("not_before = %v after Expedite", got.NotBefore)
	}
	first, err := r.ClaimOne(ctx, "k")
	if err != nil || first != future {
		t.Fatalf("first claim = %q (older %q, expedited %q), %v", first, older, future, err)
	}
	if second, _ := r.ClaimOne(ctx, "k"); second != older {
		t.Errorf("second claim = %q, want %q", second, older)
	}
}

func TestExpediteIsANoOpOtherwise(t *testing.T) {
	ctx := context.Background()
	st := openStore(t, t.TempDir())
	started := make(chan *jobs.Task, 1)
	r := newRunner(t, st, blocking("k", started, false))
	start(t, r)

	// Unknown job.
	if err := r.Expedite(ctx, "jb_nope"); err != nil {
		t.Errorf("unknown job: %v", err)
	}
	// Running job: untouched.
	id := enqueue(t, r, "k", nil, jobs.EnqueueOptions{})
	<-started
	if err := r.Expedite(ctx, id); err != nil {
		t.Errorf("running job: %v", err)
	}
	if after := get(t, r, id); after.State != jobs.StateRunning {
		t.Errorf("running job changed: %+v", after)
	}
	// A job queued for later is moved to now.
	future := enqueue(t, r, "k", nil, jobs.EnqueueOptions{NotBefore: time.Now().Add(time.Hour)})
	if err := r.Expedite(ctx, future); err != nil {
		t.Fatal(err)
	}
	if got := get(t, r, future); got.NotBefore.UnixMilli() > 0 {
		t.Errorf("not_before = %v, want zero", got.NotBefore)
	}
}
