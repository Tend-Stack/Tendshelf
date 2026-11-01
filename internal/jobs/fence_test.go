package jobs_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
)

func TestFence(t *testing.T) {
	st := openStore(t, t.TempDir())
	ctx := context.Background()
	started := make(chan *jobs.Task, 1)
	r := newRunner(t, st, blocking("k", started, false))
	start(t, r)
	id := enqueue(t, r, "k", nil, jobs.EnqueueOptions{})
	tk := <-started

	fence := func() error {
		return st.WriteTx(ctx, func(tx *sql.Tx) error { return tk.Fence(ctx, tx) })
	}
	if err := fence(); err != nil {
		t.Fatalf("running job: Fence = %v", err)
	}
	// A superseded attempt loses the fence.
	if _, err := st.Writer().Exec(`UPDATE jobs SET attempts = attempts + 1 WHERE id = ?`, id); err != nil {
		t.Fatal(err)
	}
	if err := fence(); !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatalf("superseded: Fence = %v", err)
	}
	// A failed fence rolls back the transaction's other writes.
	err := st.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE fence_probe (x INTEGER)`); err != nil {
			return err
		}
		return tk.Fence(ctx, tx)
	})
	if !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatalf("Fence = %v", err)
	}
	var n int
	if err := st.Reader().QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='fence_probe'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("rolled-back table exists: n=%d err=%v", n, err)
	}
}

func TestFenceCancelled(t *testing.T) {
	st := openStore(t, t.TempDir())
	ctx := context.Background()
	started := make(chan *jobs.Task, 1)
	r := newRunner(t, st, blocking("k", started, false))
	start(t, r)
	id := enqueue(t, r, "k", nil, jobs.EnqueueOptions{})
	tk := <-started
	if err := r.Cancel(ctx, id); err != nil {
		t.Fatal(err)
	}
	err := st.WriteTx(ctx, func(tx *sql.Tx) error { return tk.Fence(ctx, tx) })
	if !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatalf("Fence after cancel = %v", err)
	}
}

func TestActiveKeys(t *testing.T) {
	st := openStore(t, t.TempDir())
	ctx := context.Background()
	r := newRunner(t, st, quick("k"), quick("other"))
	// The runner is not started, so everything stays queued.
	enqueue(t, r, "k", map[string]string{"audio_file_id": "a1"}, jobs.EnqueueOptions{})
	enqueue(t, r, "k", map[string]string{"audio_file_id": "a2"}, jobs.EnqueueOptions{})
	enqueue(t, r, "k", map[string]string{"other_field": "x"}, jobs.EnqueueOptions{})
	enqueue(t, r, "other", map[string]string{"audio_file_id": "o1"}, jobs.EnqueueOptions{})
	done := enqueue(t, r, "k", map[string]string{"audio_file_id": "a3"}, jobs.EnqueueOptions{})
	if _, err := st.Writer().Exec(`UPDATE jobs SET state='done' WHERE id = ?`, done); err != nil {
		t.Fatal(err)
	}
	held := enqueue(t, r, "k", map[string]string{"audio_file_id": "a4"}, jobs.EnqueueOptions{})
	if _, err := st.Writer().Exec(`UPDATE jobs SET state='held' WHERE id = ?`, held); err != nil {
		t.Fatal(err)
	}
	got, err := r.ActiveKeys(ctx, "k", "audio_file_id")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"a1": true, "a2": true, "a4": true}
	if len(got) != len(want) {
		t.Fatalf("keys = %v, want %v", got, want)
	}
	for k := range want {
		if !got[k] {
			t.Fatalf("keys = %v, want %v", got, want)
		}
	}
	for _, bad := range []string{"", "a.b", "x'; DROP", "UPPER", "a b"} {
		if _, err := r.ActiveKeys(ctx, "k", bad); err == nil {
			t.Errorf("field %q accepted", bad)
		}
	}
}
