package jobs_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
)

func openStore(t *testing.T, dir string) *db.Store {
	t.Helper()
	st, err := db.Open(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

func fastOptions() jobs.Options {
	return jobs.Options{
		Lease:         300 * time.Millisecond,
		RenewEvery:    100 * time.Millisecond,
		ProgressEvery: 50 * time.Millisecond,
		SweepEvery:    100 * time.Millisecond,
		PollEvery:     20 * time.Millisecond,
		Backoff:       func(int) time.Duration { return 50 * time.Millisecond },
		Logger:        slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// newRunner registers kinds on a fresh runner; Stop is part of the cleanup,
// which runs before the store is closed.
func newRunner(t *testing.T, st *db.Store, kinds ...jobs.Kind) *jobs.Runner {
	t.Helper()
	return newRunnerWith(t, st, fastOptions(), kinds...)
}

// newRunnerWith is newRunner with explicit options.
func newRunnerWith(t *testing.T, st *db.Store, o jobs.Options, kinds ...jobs.Kind) *jobs.Runner {
	t.Helper()
	r, err := jobs.New(st, o)
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range kinds {
		if err := r.Register(k); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.Stop(ctx)
	})
	return r
}

func start(t *testing.T, r *jobs.Runner) {
	t.Helper()
	if err := r.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func get(t *testing.T, r *jobs.Runner, id string) jobs.Job {
	t.Helper()
	j, err := r.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

func waitState(t *testing.T, r *jobs.Runner, id string, want jobs.State) jobs.Job {
	t.Helper()
	var j jobs.Job
	waitFor(t, "job "+id+" to be "+string(want), func() bool {
		j = get(t, r, id)
		return j.State == want
	})
	return j
}

func enqueue(t *testing.T, r *jobs.Runner, kind string, payload any, o jobs.EnqueueOptions) string {
	t.Helper()
	id, err := r.Enqueue(context.Background(), kind, payload, o)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// insertRunning writes a row as a crashed server would have left it.
func insertRunning(t *testing.T, st *db.Store, id, kind string, attempts, max int, leaseIn time.Duration) {
	t.Helper()
	now := time.Now()
	_, err := st.Writer().Exec(`INSERT INTO jobs(id, kind, state, attempts, max_attempts, lease_until, created_at, updated_at)
VALUES (?, ?, 'running', ?, ?, ?, ?, ?)`, id, kind, attempts, max, now.Add(leaseIn).UnixMilli(), now.UnixMilli(), now.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
}

func quick(name string) jobs.Kind {
	return jobs.Kind{Name: name, Concurrency: 1, Handler: func(ctx context.Context, tk *jobs.Task) (any, error) {
		return map[string]int{"attempt": tk.Attempt}, nil
	}}
}

// blocking returns a kind whose handler reports each call on started and then
// waits for ctx, returning ctx's cause.
func blocking(name string, started chan<- *jobs.Task, hold bool) jobs.Kind {
	return jobs.Kind{Name: name, Concurrency: 1, HoldOnRecovery: hold, Handler: func(ctx context.Context, tk *jobs.Task) (any, error) {
		started <- tk
		<-ctx.Done()
		return nil, context.Cause(ctx)
	}}
}

func TestEnqueueAndRun(t *testing.T) {
	st := openStore(t, t.TempDir())
	r := newRunner(t, st, jobs.Kind{Name: "echo", Concurrency: 1, Handler: func(ctx context.Context, tk *jobs.Task) (any, error) {
		var p struct{ N int }
		if err := json.Unmarshal(tk.Payload, &p); err != nil {
			return nil, err
		}
		return map[string]int{"double": p.N * 2}, nil
	}})
	start(t, r)
	id := enqueue(t, r, "echo", map[string]int{"N": 21}, jobs.EnqueueOptions{})
	j := waitState(t, r, id, jobs.StateDone)
	if string(j.Result) != `{"double":42}` || j.Attempts != 1 || !j.LeaseUntil.IsZero() || j.Kind != "echo" {
		t.Fatalf("job = %+v result=%s", j, j.Result)
	}
	if id[:3] != "jb_" {
		t.Fatalf("id %q", id)
	}
}

// slowLease is fastOptions with a lease no scheduler stall can use up. With the 300 ms lease a runner
// starved for that long on a loaded CI machine has its running job taken back by the sweep and run
// again, which is a second attempt that has nothing to do with shutdown accounting.
func slowLease() jobs.Options {
	o := fastOptions()
	o.Lease = 30 * time.Second
	o.RenewEvery = 10 * time.Second
	return o
}

func TestJobSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	st := openStore(t, dir)
	started := make(chan *jobs.Task, 4)
	r := newRunnerWith(t, st, slowLease(), blocking("slow", started, false))
	start(t, r)
	id := enqueue(t, r, "slow", nil, jobs.EnqueueOptions{})
	<-started
	if err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	j := get(t, r, id)
	if j.State != jobs.StateQueued || j.Attempts != 0 || !j.LeaseUntil.IsZero() {
		t.Fatalf("after Stop: %+v", j)
	}
	st.Close()

	st2 := openStore(t, dir)
	r2 := newRunnerWith(t, st2, slowLease(), quick("slow"))
	start(t, r2)
	j = waitState(t, r2, id, jobs.StateDone)
	if j.Attempts != 1 {
		t.Fatalf("attempts = %d", j.Attempts)
	}
}

func TestExpiredLeaseRequeuedOnStart(t *testing.T) {
	st := openStore(t, t.TempDir())
	insertRunning(t, st, "jb_old", "k", 1, 3, -time.Second)
	got := make(chan int, 1)
	r := newRunner(t, st, jobs.Kind{Name: "k", Concurrency: 1, Handler: func(ctx context.Context, tk *jobs.Task) (any, error) {
		got <- tk.Attempt
		return nil, nil
	}})
	start(t, r)
	select {
	case a := <-got:
		if a != 2 {
			t.Fatalf("attempt = %d, want 2", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("expired job was not rerun")
	}
	waitState(t, r, "jb_old", jobs.StateDone)
}

func TestUnexpiredLeaseRecoveredBySweep(t *testing.T) {
	st := openStore(t, t.TempDir())
	insertRunning(t, st, "jb_live", "k", 1, 3, 400*time.Millisecond)
	got := make(chan int, 1)
	r := newRunner(t, st, jobs.Kind{Name: "k", Concurrency: 1, Handler: func(ctx context.Context, tk *jobs.Task) (any, error) {
		got <- tk.Attempt
		return nil, nil
	}})
	start(t, r)
	if j := get(t, r, "jb_live"); j.State != jobs.StateRunning {
		t.Fatalf("a live lease must not be touched at start: %+v", j)
	}
	select {
	case a := <-got:
		if a != 2 {
			t.Fatalf("attempt = %d", a)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("sweep did not recover the job")
	}
}

func TestHoldOnRecoveryKindGoesToHeld(t *testing.T) {
	st := openStore(t, t.TempDir())
	insertRunning(t, st, "jb_h", "k", 1, 3, -time.Second)
	var calls atomic.Int32
	r := newRunner(t, st, jobs.Kind{Name: "k", Concurrency: 1, HoldOnRecovery: true, Handler: func(ctx context.Context, tk *jobs.Task) (any, error) {
		calls.Add(1)
		return "ok", nil
	}})
	start(t, r)
	j := waitState(t, r, "jb_h", jobs.StateHeld)
	if j.ErrorCode != "interrupted" || j.ErrorText == "" || !j.LeaseUntil.IsZero() {
		t.Fatalf("held job = %+v", j)
	}
	time.Sleep(200 * time.Millisecond)
	if calls.Load() != 0 {
		t.Fatal("a held job must not run on its own")
	}
	if err := r.Resume(context.Background(), "jb_h"); err != nil {
		t.Fatal(err)
	}
	j = waitState(t, r, "jb_h", jobs.StateDone)
	if j.ErrorCode != "" || calls.Load() != 1 {
		t.Fatalf("after resume: %+v calls=%d", j, calls.Load())
	}
}

func TestResumeErrors(t *testing.T) {
	st := openStore(t, t.TempDir())
	r := newRunner(t, st, quick("k"))
	id := enqueue(t, r, "k", nil, jobs.EnqueueOptions{NotBefore: time.Now().Add(time.Hour)})
	if err := r.Resume(context.Background(), id); !errors.Is(err, jobs.ErrNotHeld) {
		t.Fatalf("queued job: %v", err)
	}
	if err := r.Resume(context.Background(), "jb_nope"); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("unknown job: %v", err)
	}
}

func TestUnregisteredKindRecoveredToHeld(t *testing.T) {
	st := openStore(t, t.TempDir())
	insertRunning(t, st, "jb_ghost", "ghost", 1, 3, -time.Second)
	r := newRunner(t, st, quick("other"))
	start(t, r)
	j := waitState(t, r, "jb_ghost", jobs.StateHeld)
	if j.ErrorCode != "interrupted" {
		t.Fatalf("job = %+v", j)
	}
}

func TestRecoveryExhaustedAttemptsFails(t *testing.T) {
	st := openStore(t, t.TempDir())
	insertRunning(t, st, "jb_x", "k", 3, 3, -time.Second)
	r := newRunner(t, st, quick("k"))
	start(t, r)
	j := waitState(t, r, "jb_x", jobs.StateFailed)
	if j.ErrorCode != "interrupted" || j.ErrorText == "" || j.Attempts != 3 {
		t.Fatalf("job = %+v", j)
	}
}

func TestLostLeaseCancelsHandler(t *testing.T) {
	st := openStore(t, t.TempDir())
	started := make(chan struct{}, 4)
	causes := make(chan error, 4)
	var calls atomic.Int32
	r := newRunner(t, st, jobs.Kind{Name: "k", Concurrency: 1, MaxAttempts: 5, Handler: func(ctx context.Context, tk *jobs.Task) (any, error) {
		if calls.Add(1) == 1 {
			started <- struct{}{}
			<-ctx.Done()
			causes <- context.Cause(ctx)
			return "stale", nil // must never be written
		}
		return "fresh", nil
	}})
	start(t, r)
	id := enqueue(t, r, "k", nil, jobs.EnqueueOptions{})
	<-started
	if _, err := st.Writer().Exec(`UPDATE jobs SET attempts = attempts + 1 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-causes:
		if !errors.Is(c, jobs.ErrLeaseLost) {
			t.Fatalf("cause = %v", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler was not cancelled")
	}
	j := waitState(t, r, id, jobs.StateDone)
	if string(j.Result) != `"fresh"` || j.Attempts != 3 {
		t.Fatalf("job = %+v result=%s", j, j.Result)
	}
}

func TestRenewKeepsLongJob(t *testing.T) {
	st := openStore(t, t.TempDir())
	r := newRunner(t, st, jobs.Kind{Name: "long", Concurrency: 1, Handler: func(ctx context.Context, tk *jobs.Task) (any, error) {
		select {
		case <-time.After(900 * time.Millisecond): // three leases
			return "ok", nil
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}})
	start(t, r)
	id := enqueue(t, r, "long", nil, jobs.EnqueueOptions{})
	j := waitState(t, r, id, jobs.StateDone)
	if j.Attempts != 1 {
		t.Fatalf("attempts = %d: the lease was lost", j.Attempts)
	}
}

func TestPerKindConcurrencyBound(t *testing.T) {
	st := openStore(t, t.TempDir())
	var cur, peak, peakSQL atomic.Int32
	r := newRunner(t, st,
		jobs.Kind{Name: "a", Concurrency: 2, Handler: func(ctx context.Context, tk *jobs.Task) (any, error) {
			n := cur.Add(1)
			defer cur.Add(-1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			var running int
			if err := st.Reader().QueryRow(`SELECT count(*) FROM jobs WHERE kind='a' AND state='running'`).Scan(&running); err == nil {
				for {
					p := peakSQL.Load()
					if int32(running) <= p || peakSQL.CompareAndSwap(p, int32(running)) {
						break
					}
				}
			}
			time.Sleep(60 * time.Millisecond)
			return nil, nil
		}},
		quick("b"))
	start(t, r)
	var ids []string
	for range 10 {
		ids = append(ids, enqueue(t, r, "a", nil, jobs.EnqueueOptions{}))
	}
	bid := enqueue(t, r, "b", nil, jobs.EnqueueOptions{})
	waitState(t, r, bid, jobs.StateDone)
	if get(t, r, ids[9]).State == jobs.StateDone {
		t.Fatal("kind b should finish while kind a is still busy")
	}
	for _, id := range ids {
		waitState(t, r, id, jobs.StateDone)
	}
	if peak.Load() > 2 || peak.Load() < 2 || peakSQL.Load() > 2 {
		t.Fatalf("observed max %d, sql max %d; want 2 and <= 2", peak.Load(), peakSQL.Load())
	}
}

func TestClaimIsAtomic(t *testing.T) {
	dir := t.TempDir()
	st1, st2 := openStore(t, dir), openStore(t, dir)
	r1 := newRunner(t, st1, quick("k"))
	r2 := newRunner(t, st2, quick("k"))
	const total = 50
	for range total {
		enqueue(t, r1, "k", nil, jobs.EnqueueOptions{})
	}
	var mu sync.Mutex
	seen := map[string]int{}
	var wg sync.WaitGroup
	for i := range 8 {
		r := r1
		if i%2 == 1 {
			r = r2
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				id, err := r.ClaimOne(context.Background(), "k")
				if err != nil {
					t.Error(err)
					return
				}
				if id == "" {
					return
				}
				mu.Lock()
				seen[id]++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if len(seen) != total {
		t.Fatalf("claimed %d distinct jobs, want %d", len(seen), total)
	}
	for id, n := range seen {
		if n != 1 {
			t.Errorf("job %s claimed %d times", id, n)
		}
	}
}

func TestCancelQueued(t *testing.T) {
	st := openStore(t, t.TempDir())
	r := newRunner(t, st, quick("k"))
	id := enqueue(t, r, "k", nil, jobs.EnqueueOptions{NotBefore: time.Now().Add(time.Hour)})
	if err := r.Cancel(context.Background(), id); err != nil {
		t.Fatal(err)
	}
	if j := get(t, r, id); j.State != jobs.StateCancelled {
		t.Fatalf("state = %s", j.State)
	}
	if err := r.Cancel(context.Background(), id); !errors.Is(err, jobs.ErrAlreadyFinished) {
		t.Fatalf("second cancel: %v", err)
	}
}

func TestCancelRunning(t *testing.T) {
	st := openStore(t, t.TempDir())
	started := make(chan *jobs.Task, 4)
	causes := make(chan error, 4)
	r := newRunner(t, st, jobs.Kind{Name: "k", Concurrency: 1, Handler: func(ctx context.Context, tk *jobs.Task) (any, error) {
		started <- tk
		if tk.Attempt == 1 && string(tk.Payload) == `"first"` {
			<-ctx.Done()
			causes <- context.Cause(ctx)
			return "late", nil
		}
		return "second", nil
	}})
	start(t, r)
	first := enqueue(t, r, "k", "first", jobs.EnqueueOptions{})
	<-started
	second := enqueue(t, r, "k", "second", jobs.EnqueueOptions{})
	if err := r.Cancel(context.Background(), first); err != nil {
		t.Fatal(err)
	}
	if j := get(t, r, first); j.State != jobs.StateCancelled {
		t.Fatalf("Get right after Cancel: %s", j.State)
	}
	select {
	case c := <-causes:
		if !errors.Is(c, jobs.ErrCancelled) {
			t.Fatalf("cause = %v", c)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("handler was not cancelled")
	}
	waitState(t, r, second, jobs.StateDone) // the slot was reused
	j := get(t, r, first)
	if j.State != jobs.StateCancelled || j.Result != nil {
		t.Fatalf("cancelled job = %+v", j)
	}
}

func TestCancelUnknown(t *testing.T) {
	st := openStore(t, t.TempDir())
	r := newRunner(t, st, quick("k"))
	if err := r.Cancel(context.Background(), "jb_nope"); !errors.Is(err, jobs.ErrNotFound) {
		t.Fatalf("got %v", err)
	}
}

func TestCancelFinished(t *testing.T) {
	st := openStore(t, t.TempDir())
	r := newRunner(t, st, quick("k"))
	start(t, r)
	id := enqueue(t, r, "k", nil, jobs.EnqueueOptions{})
	waitState(t, r, id, jobs.StateDone)
	if err := r.Cancel(context.Background(), id); !errors.Is(err, jobs.ErrAlreadyFinished) {
		t.Fatalf("got %v", err)
	}
}

func TestRetryWithBackoff(t *testing.T) {
	st := openStore(t, t.TempDir())
	var mu sync.Mutex
	var times []time.Time
	r := newRunner(t, st, jobs.Kind{Name: "k", Concurrency: 1, Handler: func(ctx context.Context, tk *jobs.Task) (any, error) {
		mu.Lock()
		times = append(times, time.Now())
		mu.Unlock()
		if tk.Attempt < 3 {
			return nil, errors.New("temporary")
		}
		return "ok", nil
	}})
	start(t, r)
	id := enqueue(t, r, "k", nil, jobs.EnqueueOptions{})
	j := waitState(t, r, id, jobs.StateDone)
	if j.Attempts != 3 || j.ErrorCode != "" {
		t.Fatalf("job = %+v", j)
	}
	mu.Lock()
	defer mu.Unlock()
	for i := 1; i < len(times); i++ {
		if gap := times[i].Sub(times[i-1]); gap < 50*time.Millisecond {
			t.Errorf("retry %d came after %v, want >= 50ms", i, gap)
		}
	}
}

func TestDefaultBackoff(t *testing.T) {
	for a, want := range map[int]time.Duration{
		0: 10 * time.Second, 1: 10 * time.Second, 2: 20 * time.Second, 3: 40 * time.Second,
		7: 640 * time.Second, 8: 15 * time.Minute, 10: 15 * time.Minute, 100: 15 * time.Minute,
	} {
		if got := jobs.DefaultBackoff(a); got != want {
			t.Errorf("DefaultBackoff(%d) = %v, want %v", a, got, want)
		}
	}
}

func TestPermanentErrorFailsImmediately(t *testing.T) {
	st := openStore(t, t.TempDir())
	r := newRunner(t, st, jobs.Kind{Name: "k", Concurrency: 1, Handler: func(ctx context.Context, tk *jobs.Task) (any, error) {
		return nil, &jobs.Error{Code: "copy_protected", Text: "This file is copy-protected.", Permanent: true}
	}})
	start(t, r)
	id := enqueue(t, r, "k", nil, jobs.EnqueueOptions{})
	j := waitState(t, r, id, jobs.StateFailed)
	if j.Attempts != 1 || j.ErrorCode != "copy_protected" || j.ErrorText != "This file is copy-protected." {
		t.Fatalf("job = %+v", j)
	}
}

func TestMaxAttemptsExhausted(t *testing.T) {
	st := openStore(t, t.TempDir())
	r := newRunner(t, st, jobs.Kind{Name: "k", Concurrency: 1, Handler: func(ctx context.Context, tk *jobs.Task) (any, error) {
		return nil, errors.New("secret detail that must not reach the user")
	}})
	start(t, r)
	id := enqueue(t, r, "k", nil, jobs.EnqueueOptions{MaxAttempts: 2})
	j := waitState(t, r, id, jobs.StateFailed)
	if j.Attempts != 2 || j.ErrorCode != "internal" || j.ErrorText != "Something went wrong while running this job." {
		t.Fatalf("job = %+v", j)
	}
}

func TestHandlerPanicFailsJob(t *testing.T) {
	st := openStore(t, t.TempDir())
	r := newRunner(t, st, jobs.Kind{Name: "k", Concurrency: 1, Handler: func(ctx context.Context, tk *jobs.Task) (any, error) {
		panic("boom")
	}})
	start(t, r)
	id := enqueue(t, r, "k", nil, jobs.EnqueueOptions{})
	j := waitState(t, r, id, jobs.StateFailed)
	if j.Attempts != 1 || j.ErrorCode != "internal" {
		t.Fatalf("job = %+v", j)
	}
}

func TestProgressThrottled(t *testing.T) {
	st := openStore(t, t.TempDir())
	var elapsed time.Duration
	r := newRunner(t, st, jobs.Kind{Name: "k", Concurrency: 1, Handler: func(ctx context.Context, tk *jobs.Task) (any, error) {
		t0 := time.Now()
		for i := 1; i <= 1000; i++ {
			tk.Progress(map[string]int{"n": i})
			time.Sleep(350 * time.Microsecond)
		}
		elapsed = time.Since(t0)
		return nil, nil
	}})
	start(t, r)
	id := enqueue(t, r, "k", nil, jobs.EnqueueOptions{})
	j := waitState(t, r, id, jobs.StateDone)
	writes := r.ProgressWrites()
	limit := int64(elapsed/(50*time.Millisecond)) + 1
	if writes < 1 || writes > limit || writes >= 100 {
		t.Fatalf("progress writes = %d over %v, limit %d", writes, elapsed, limit)
	}
	if string(j.Progress) != `{"n":1000}` {
		t.Fatalf("final progress = %s", j.Progress)
	}
}

func TestStopRequeuesAndHolds(t *testing.T) {
	st := openStore(t, t.TempDir())
	started := make(chan *jobs.Task, 4)
	r := newRunner(t, st, blocking("q", started, false), blocking("h", started, true))
	start(t, r)
	q := enqueue(t, r, "q", nil, jobs.EnqueueOptions{})
	h := enqueue(t, r, "h", nil, jobs.EnqueueOptions{})
	<-started
	<-started
	if err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	jq, jh := get(t, r, q), get(t, r, h)
	if jq.State != jobs.StateQueued || jq.Attempts != 0 || !jq.LeaseUntil.IsZero() {
		t.Fatalf("requeued job = %+v", jq)
	}
	if jh.State != jobs.StateHeld || jh.ErrorCode != "interrupted" || jh.Attempts != 1 {
		t.Fatalf("held job = %+v", jh)
	}
}

func TestStopStopsClaiming(t *testing.T) {
	st := openStore(t, t.TempDir())
	r := newRunner(t, st, quick("k"))
	start(t, r)
	if err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	id := enqueue(t, r, "k", nil, jobs.EnqueueOptions{})
	time.Sleep(200 * time.Millisecond)
	if j := get(t, r, id); j.State != jobs.StateQueued || j.Attempts != 0 {
		t.Fatalf("job after Stop = %+v", j)
	}
}

func TestStopDeadlineWithStuckHandler(t *testing.T) {
	st := openStore(t, t.TempDir())
	started := make(chan struct{}, 1)
	release := make(chan struct{})
	r := newRunner(t, st, jobs.Kind{Name: "k", Concurrency: 1, Handler: func(ctx context.Context, tk *jobs.Task) (any, error) {
		started <- struct{}{}
		<-release // ignores ctx on purpose
		return nil, errors.New("late")
	}})
	start(t, r)
	id := enqueue(t, r, "k", nil, jobs.EnqueueOptions{})
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := r.Stop(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Stop = %v", err)
	}
	if j := get(t, r, id); j.State != jobs.StateRunning {
		t.Fatalf("state = %s, want running", j.State)
	}
	close(release)
	if err := r.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	if j := get(t, r, id); j.State != jobs.StateQueued {
		t.Fatalf("state after release = %s", j.State)
	}
}

func TestRegisterAfterStart(t *testing.T) {
	st := openStore(t, t.TempDir())
	r := newRunner(t, st, quick("k"))
	start(t, r)
	if err := r.Register(quick("late")); !errors.Is(err, jobs.ErrStarted) {
		t.Fatalf("Register = %v", err)
	}
	if err := r.Start(context.Background()); !errors.Is(err, jobs.ErrStarted) {
		t.Fatalf("second Start = %v", err)
	}
}

func TestRegisterValidation(t *testing.T) {
	st := openStore(t, t.TempDir())
	r := newRunner(t, st, quick("k"))
	if err := r.Register(quick("k")); !errors.Is(err, jobs.ErrDuplicateKind) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := r.Register(jobs.Kind{Name: "z", Concurrency: 0, Handler: quick("z").Handler}); !errors.Is(err, jobs.ErrBadOptions) {
		t.Fatalf("zero concurrency: %v", err)
	}
	if err := r.Register(jobs.Kind{Name: "z", Concurrency: 1}); !errors.Is(err, jobs.ErrBadOptions) {
		t.Fatalf("nil handler: %v", err)
	}
}

func TestEnqueueUnknownKind(t *testing.T) {
	st := openStore(t, t.TempDir())
	r := newRunner(t, st, quick("k"))
	if _, err := r.Enqueue(context.Background(), "nope", nil, jobs.EnqueueOptions{}); !errors.Is(err, jobs.ErrUnknownKind) {
		t.Fatalf("got %v", err)
	}
}

func TestNewBadOptions(t *testing.T) {
	st := openStore(t, t.TempDir())
	for name, o := range map[string]jobs.Options{
		"renew too slow": {Lease: time.Second, RenewEvery: 600 * time.Millisecond},
		"negative":       {PollEvery: -time.Second},
	} {
		if _, err := jobs.New(st, o); !errors.Is(err, jobs.ErrBadOptions) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := jobs.New(st, jobs.Options{}); err != nil {
		t.Fatalf("defaults: %v", err)
	}
}
