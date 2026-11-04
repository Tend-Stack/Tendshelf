package backups

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
)

var t0 = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

type env struct {
	st     *db.Store
	runner *jobs.Runner
	svc    *Service
	dir    string
	now    time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{dir: t.TempDir(), now: t0}
	st, err := db.Open(context.Background(), e.dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	e.st = st
	e.runner, err = jobs.New(st, jobs.Options{PollEvery: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	e.svc = New(st, e.runner, e.dir, func() time.Time { return e.now }, nil)
	if err := e.svc.Register(); err != nil {
		t.Fatal(err)
	}
	return e
}

// copyAt writes an empty file named like a copy made at the given time.
func (e *env) copyAt(t *testing.T, at time.Time) {
	t.Helper()
	dir := filepath.Join(e.dir, "backups")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	n := "tendshelf-copy-v13-" + at.UTC().Format("20060102T150405.000Z") + ".db"
	if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestBackupsDue(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name     string
		schedule string
		copy     time.Duration // age of the newest copy; 0 = none
		job      string        // "", "queued" or "failed-recent" or "failed-old"
		want     bool
	}{
		{"off", Off, 0, "", false},
		{"none yet", Daily, 0, "", true},
		{"fresh daily", Daily, 3 * time.Hour, "", false},
		{"stale daily", Daily, 25 * time.Hour, "", true},
		{"daily within slack", Daily, 24*time.Hour - 10*time.Minute, "", true},
		{"weekly not yet", Weekly, 3 * 24 * time.Hour, "", false},
		{"stale weekly", Weekly, 8 * 24 * time.Hour, "", true},
		{"active job", Daily, 30 * time.Hour, "queued", false},
		{"recent failure", Daily, 30 * time.Hour, "failed-recent", false},
		{"old failure", Daily, 30 * time.Hour, "failed-old", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			if err := e.svc.SetSchedule(ctx, tc.schedule); err != nil {
				t.Fatal(err)
			}
			if tc.copy > 0 {
				e.copyAt(t, e.now.Add(-tc.copy))
			}
			switch tc.job {
			case "queued":
				if _, err := e.svc.Start(ctx, "", "scheduled"); err != nil {
					t.Fatal(err)
				}
			case "failed-recent", "failed-old":
				id, err := e.svc.Start(ctx, "", "scheduled")
				if err != nil {
					t.Fatal(err)
				}
				at := e.now.Add(-10 * time.Minute)
				if tc.job == "failed-old" {
					at = e.now.Add(-3 * time.Hour)
				}
				if _, err := e.st.Writer().ExecContext(ctx, `UPDATE jobs SET state='failed', updated_at=? WHERE id=?`, at.UnixMilli(), id); err != nil {
					t.Fatal(err)
				}
			}
			got, err := e.svc.Due(ctx)
			if err != nil || got != tc.want {
				t.Fatalf("Due = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestBackupsDefaultSchedule(t *testing.T) {
	e := newEnv(t)
	if got := e.svc.Schedule(context.Background()); got != Daily {
		t.Fatalf("default = %q", got)
	}
}

func TestBackupsSetScheduleRejects(t *testing.T) {
	e := newEnv(t)
	if err := e.svc.SetSchedule(context.Background(), "hourly"); !errors.Is(err, ErrBadSchedule) {
		t.Fatalf("err = %v", err)
	}
	if err := e.svc.SetSchedule(context.Background(), Weekly); err != nil {
		t.Fatal(err)
	}
	if got := e.svc.Schedule(context.Background()); got != Weekly {
		t.Fatalf("schedule = %q", got)
	}
}

func TestBackupsStartReturnsActive(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a, err := e.svc.Start(ctx, "", "manual")
	if err != nil {
		t.Fatal(err)
	}
	b, err := e.svc.Start(ctx, "", "manual")
	if err != nil || a != b {
		t.Fatalf("second Start = %q, %v; want %q", b, err, a)
	}
}

func TestBackupsJobWritesCopy(t *testing.T) {
	e := newEnv(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.runner.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer e.runner.Stop(context.Background())
	id, err := e.svc.Start(ctx, "", "manual")
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		j, err := e.runner.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if j.State == jobs.StateDone {
			break
		}
		if j.State == jobs.StateFailed || time.Now().After(deadline) {
			t.Fatalf("job state %s (%s)", j.State, j.ErrorText)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if list, err := db.ListCopies(e.dir); err != nil || len(list) != 1 {
		t.Fatalf("copies = %v, %v", list, err)
	}
}

func TestBackupsRunEnqueuesOncePerPeriod(t *testing.T) {
	e := newEnv(t)
	e.svc.First, e.svc.Every = 5*time.Millisecond, 5*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { e.svc.Run(ctx); close(done) }()
	time.Sleep(200 * time.Millisecond)
	cancel()
	<-done
	var n int
	if err := e.st.Reader().QueryRow(`SELECT count(*) FROM jobs WHERE kind = ?`, Kind).Scan(&n); err != nil || n != 1 {
		t.Fatalf("jobs = %d, %v; want 1 (the first is still queued, so no second is added)", n, err)
	}
}
