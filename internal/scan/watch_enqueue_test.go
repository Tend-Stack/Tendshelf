package scan

import (
	"io"
	"log/slog"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/media"
)

// TestWatchEnqueueJoins: the button, the folder watcher and the schedule all call Enqueue; while a scan of
// the library is waiting, a second call returns the same job instead of queueing another.
func TestWatchEnqueueJoins(t *testing.T) {
	e := newEnv(t, media.Tools{})
	r, err := jobs.New(e.st, jobs.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}) // never started: jobs stay queued
	if err != nil {
		t.Fatal(err)
	}
	e.sc = New(e.repo, media.Tools{}, r, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := e.sc.Register(r); err != nil {
		t.Fatal(err)
	}
	a, err := Enqueue(bg, r, "", "lib_1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := Enqueue(bg, r, "us_1", "lib_1")
	if err != nil || b != a {
		t.Fatalf("second enqueue = %q, %v; want the first job %q", b, err, a)
	}
	c, err := Enqueue(bg, r, "", "lib_2")
	if err != nil || c == a {
		t.Fatalf("another library = %q, %v; want its own job", c, err)
	}
}
