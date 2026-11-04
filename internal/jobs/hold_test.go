package jobs_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
)

// TestJobsHoldFromHandler: a handler error with Hold sends the job to held, keeps the code and gives the
// attempt back; Resume runs it again as attempt 1.
func TestJobsHoldFromHandler(t *testing.T) {
	st := openStore(t, t.TempDir())
	var calls atomic.Int32
	attempts := make(chan int, 4)
	r := newRunner(t, st, jobs.Kind{Name: "k", Concurrency: 1, Handler: func(ctx context.Context, tk *jobs.Task) (any, error) {
		attempts <- tk.Attempt
		if calls.Add(1) == 1 {
			return nil, &jobs.Error{Code: "runtime_changed", Text: "The model changed.", Hold: true, Permanent: true}
		}
		return "ok", nil
	}})
	start(t, r)
	id := enqueue(t, r, "k", nil, jobs.EnqueueOptions{})
	j := waitState(t, r, id, jobs.StateHeld)
	if j.ErrorCode != "runtime_changed" || j.ErrorText != "The model changed." || j.Attempts != 0 || !j.LeaseUntil.IsZero() {
		t.Fatalf("held job = %+v", j)
	}
	if a := <-attempts; a != 1 {
		t.Fatalf("first attempt = %d", a)
	}
	if err := r.Resume(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	j = waitState(t, r, id, jobs.StateDone)
	if a := <-attempts; a != 1 || j.ErrorCode != "" {
		t.Fatalf("after resume: attempt %d, job %+v", a, j)
	}
}

// TestJobsHoldNotForWriteDetails: the shutdown path and plain errors are unchanged by Hold.
func TestJobsHoldNotForWriteDetails(t *testing.T) {
	st := openStore(t, t.TempDir())
	started := make(chan *jobs.Task, 4)
	r := newRunner(t, st,
		jobs.Kind{Name: "q", Concurrency: 1, Handler: func(ctx context.Context, tk *jobs.Task) (any, error) {
			started <- tk
			<-ctx.Done()
			return nil, &jobs.Error{Code: "x", Hold: true, Err: context.Cause(ctx)}
		}},
		jobs.Kind{Name: "p", Concurrency: 1, Handler: func(ctx context.Context, tk *jobs.Task) (any, error) {
			return nil, &jobs.Error{Code: "bad", Permanent: true, Err: errors.New("boom")}
		}},
	)
	start(t, r)
	p := enqueue(t, r, "p", nil, jobs.EnqueueOptions{})
	waitState(t, r, p, jobs.StateFailed)
	q := enqueue(t, r, "q", nil, jobs.EnqueueOptions{})
	<-started
	if err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	jq := get(t, r, q)
	if jq.State != jobs.StateQueued || jq.Attempts != 0 {
		t.Fatalf("shutdown with a Hold error = %+v, want queued with 0 attempts", jq)
	}
}
