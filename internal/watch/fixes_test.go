package watch

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

// TestProbeOneInFlightPerPath: a hung mount costs one goroutine, however many checks look at it.
func TestProbeOneInFlightPerPath(t *testing.T) {
	h := newHarness(t, library.Library{Watch: true})
	h.svc.StatTimeout = 5 * time.Millisecond
	release := make(chan struct{})
	var calls atomic.Int32
	old := look
	look = func(string, []Mount) probeResult {
		calls.Add(1)
		<-release
		return probeResult{ok: true}
	}
	defer func() { look = old }()
	for i := 0; i < 20; i++ {
		if ok, _ := h.svc.probe("/hung", nil); ok {
			t.Fatal("a hung folder was reported as there")
		}
	}
	if n := calls.Load(); n != 1 {
		t.Errorf("%d lookups started for one path, want 1", n)
	}
	close(release)
	h.svc.StatTimeout = time.Second
	if ok, _ := h.svc.probe("/hung", nil); !ok {
		t.Error("the folder was not seen once the lookup returned")
	}
}

// TestFlushKeepsChangeWhileScanRuns: a change seen while a scan is running is kept, and queues a fresh
// scan once that one ends.
func TestFlushKeepsChangeWhileScanRuns(t *testing.T) {
	h := newHarness(t, library.Library{Watch: true, ScanIntervalMin: 360})
	var running atomic.Bool
	running.Store(true)
	h.svc.cfg.Running = func(context.Context, string) (bool, error) { return running.Load(), nil }
	h.svc.dirty["lib_1"] = time.Now().Add(-time.Hour)
	h.svc.flush(context.Background())
	if h.count() != 0 {
		t.Fatal("a scan was queued while one was running")
	}
	if _, ok := h.svc.dirty["lib_1"]; !ok {
		t.Fatal("the change was forgotten while a scan was running")
	}
	running.Store(false)
	h.svc.flush(context.Background())
	if h.count() != 1 {
		t.Errorf("scans = %d after the running scan ended, want 1", h.count())
	}
}

// TestWatchSymlinkedRoot: a library folder that is a link is watched from where it points.
func TestWatchSymlinkedRoot(t *testing.T) {
	h := newHarness(t, library.Library{Watch: true, ScanIntervalMin: 360})
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(h.root, link); err != nil {
		t.Fatal(err)
	}
	h.src.dirs["lib_1"] = []string{link}
	h.start()
	if m := h.svc.Mode("lib_1"); m != ModeWatching {
		t.Fatalf("mode = %q, want watching", m)
	}
	write(t, filepath.Join(h.root, "New Book", "a.mp3"), "x")
	h.waitFor("scan after a drop", func() bool { return h.count() == 1 })
}

// TestForgetIgnoresUnwatchedNames: a name that is not a watched folder drops nothing.
func TestForgetIgnoresUnwatchedNames(t *testing.T) {
	s := New(Config{})
	s.dirs["/lib/a/b"] = "lib_1"
	s.forget("/lib/a")
	if len(s.dirs) != 1 {
		t.Errorf("forget of an unwatched name dropped %d watched folders", 1-len(s.dirs))
	}
	s.dirs["/lib/a"] = "lib_1"
	s.forget("/lib/a")
	if len(s.dirs) != 0 {
		t.Errorf("forget of a watched folder left %d behind", len(s.dirs))
	}
}
