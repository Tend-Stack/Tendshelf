package jobs_test

import (
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
)

// TestXOldAlignJobsDoNotBreakRunner: rows of the removed align.book and align.check kinds (migration 0020 deletes
// them, but a row could still arrive from a restored file) must not stop the runner. It starts, runs the kinds it
// knows, never claims the queued row, and holds the expired running one for a person to see.
func TestXOldAlignJobsDoNotBreakRunner(t *testing.T) {
	st := openStore(t, t.TempDir())
	now := time.Now().UnixMilli()
	if _, err := st.Writer().Exec(`INSERT INTO jobs(id, kind, state, created_at, updated_at) VALUES ('jb_aq', 'align.book', 'queued', ?, ?)`, now, now); err != nil {
		t.Fatal(err)
	}
	insertRunning(t, st, "jb_ar", "align.check", 1, 3, -time.Second)
	r := newRunner(t, st, quick("other"))
	start(t, r)

	if j := waitState(t, r, "jb_ar", jobs.StateHeld); j.ErrorCode != "interrupted" {
		t.Fatalf("running align row = %+v, want held as interrupted", j)
	}
	id := enqueue(t, r, "other", nil, jobs.EnqueueOptions{})
	waitState(t, r, id, jobs.StateDone)
	if j := get(t, r, "jb_aq"); j.State != jobs.StateQueued || j.Attempts != 0 {
		t.Fatalf("queued align row = %+v, want untouched", j)
	}
}
