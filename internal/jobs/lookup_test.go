package jobs_test

import (
	"context"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
)

func TestActiveForAndLatestFor(t *testing.T) {
	st := openStore(t, t.TempDir())
	ctx := context.Background()
	r := newRunner(t, st, quick("k"), quick("other"))
	// The runner is not started, so jobs stay as written.
	set := func(id, state string, at int64) {
		t.Helper()
		if _, err := st.Writer().Exec(`UPDATE jobs SET state=?, created_at=? WHERE id=?`, state, at, id); err != nil {
			t.Fatal(err)
		}
	}
	p := map[string]string{"library_id": "L1"}
	old := enqueue(t, r, "k", p, jobs.EnqueueOptions{})
	set(old, "queued", 1000)
	done := enqueue(t, r, "k", p, jobs.EnqueueOptions{})
	set(done, "done", 3000)
	held := enqueue(t, r, "k", p, jobs.EnqueueOptions{})
	set(held, "held", 2000)
	enqueue(t, r, "k", map[string]string{"library_id": "L2"}, jobs.EnqueueOptions{})
	enqueue(t, r, "other", p, jobs.EnqueueOptions{})

	j, ok, err := r.ActiveFor(ctx, "k", "library_id", "L1")
	if err != nil || !ok || j.ID != held {
		t.Fatalf("ActiveFor = %s %v %v, want the held job %s", j.ID, ok, err, held)
	}
	j, ok, err = r.LatestFor(ctx, "k", "library_id", "L1")
	if err != nil || !ok || j.ID != done || j.State != jobs.StateDone {
		t.Fatalf("LatestFor = %s %v %v, want the done job %s", j.ID, ok, err, done)
	}
	if _, ok, err := r.ActiveFor(ctx, "k", "library_id", "none"); ok || err != nil {
		t.Fatalf("unknown value: ok=%v err=%v", ok, err)
	}
	set(held, "failed", 2000)
	set(old, "cancelled", 1000)
	if _, ok, err := r.ActiveFor(ctx, "k", "library_id", "L1"); ok || err != nil {
		t.Fatalf("no active job left: ok=%v err=%v", ok, err)
	}
	for _, bad := range []string{"", "a.b", "x'; DROP", "UPPER"} {
		if _, _, err := r.ActiveFor(ctx, "k", bad, "x"); err == nil {
			t.Errorf("ActiveFor accepted field %q", bad)
		}
		if _, _, err := r.LatestFor(ctx, "k", bad, "x"); err == nil {
			t.Errorf("LatestFor accepted field %q", bad)
		}
	}
}
