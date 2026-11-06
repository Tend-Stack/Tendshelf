package watch

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

type fakeSource struct {
	mu   sync.Mutex
	libs []library.Library
	dirs map[string][]string
}

func (f *fakeSource) Libraries(context.Context) ([]library.Library, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]library.Library(nil), f.libs...), nil
}

func (f *fakeSource) Folders(_ context.Context, id string) ([]library.Folder, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []library.Folder
	for _, p := range f.dirs[id] {
		out = append(out, library.Folder{ID: "lf_" + p, LibraryID: id, Path: p})
	}
	return out, nil
}

// harness runs a Service against a fake source with a real fsnotify watcher on a temp folder.
type harness struct {
	t     *testing.T
	root  string
	svc   *Service
	src   *fakeSource
	mu    sync.Mutex
	scans []string
	last  time.Time
	found bool
	mount []Mount
}

func newHarness(t *testing.T, lib library.Library) *harness {
	t.Helper()
	h := &harness{t: t, root: t.TempDir()}
	lib.ID = "lib_1"
	h.src = &fakeSource{libs: []library.Library{lib}, dirs: map[string][]string{"lib_1": {h.root}}}
	h.svc = New(Config{
		Source: h.src,
		Enqueue: func(_ context.Context, id string) error {
			h.mu.Lock()
			defer h.mu.Unlock()
			h.scans = append(h.scans, id)
			return nil
		},
		LastScan: func(context.Context, string) (time.Time, bool, error) {
			h.mu.Lock()
			defer h.mu.Unlock()
			return h.last, h.found, nil
		},
		Mounts: func() []Mount { return h.mount },
	})
	h.svc.Quiet, h.svc.Tick, h.svc.First = 300*time.Millisecond, 25*time.Millisecond, time.Hour
	h.svc.ReconcileEvery, h.svc.DueEvery, h.svc.StatTimeout = time.Hour, time.Hour, time.Second
	return h
}

func (h *harness) start() context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { h.svc.Run(ctx); close(done) }()
	h.t.Cleanup(func() { cancel(); <-done })
	h.svc.Nudge()
	h.waitFor("watching", func() bool { return h.svc.Mode("lib_1") != "" })
	return cancel
}

func (h *harness) count() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.scans)
}

func (h *harness) waitFor(what string, ok func() bool) {
	h.t.Helper()
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if ok() {
			return
		}
	}
	h.t.Fatalf("timed out waiting for %s", what)
}

func write(t *testing.T, path, text string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestWatchDebounce: a book still being copied (changes keep arriving) is not scanned; one scan is
// queued after the folder has been quiet for the quiet period.
func TestWatchDebounce(t *testing.T) {
	h := newHarness(t, library.Library{Watch: true, ScanIntervalMin: 360})
	h.start()
	if m := h.svc.Mode("lib_1"); m != ModeWatching {
		t.Fatalf("mode = %q, want watching", m)
	}
	file := filepath.Join(h.root, "Book", "part1.mp3")
	for i := 0; i < 12; i++ { // 12 writes, 60 ms apart: about 700 ms, longer than the quiet period
		write(t, file, string(rune('a'+i)))
		time.Sleep(60 * time.Millisecond)
		if n := h.count(); n != 0 {
			t.Fatalf("a scan was queued after %d writes, while the copy was still going", i+1)
		}
	}
	h.waitFor("one scan after the quiet period", func() bool { return h.count() == 1 })
	time.Sleep(2 * h.svc.Quiet)
	if n := h.count(); n != 1 {
		t.Fatalf("scans = %d, want exactly 1", n)
	}
}

// TestWatchNewSubfolder: a folder created after the watch started is watched too, even when its files
// are written at once.
func TestWatchNewSubfolder(t *testing.T) {
	h := newHarness(t, library.Library{Watch: true, ScanIntervalMin: 360})
	h.start()
	write(t, filepath.Join(h.root, "Author", "Series", "b1.m4b"), "x")
	h.waitFor("first scan", func() bool { return h.count() == 1 })
	write(t, filepath.Join(h.root, "Author", "Series", "b2.m4b"), "y") // deep: needs the new folders' watches
	h.waitFor("second scan", func() bool { return h.count() == 2 })
}

// TestWatchIgnoresHiddenAndOff: the hidden upload folder never starts a scan, and a library with watching
// off is not watched at all.
func TestWatchIgnoresHiddenAndOff(t *testing.T) {
	h := newHarness(t, library.Library{Watch: true, ScanIntervalMin: 360})
	h.start()
	write(t, filepath.Join(h.root, ".tendshelf-upload-abc", "part.mp3"), "x")
	time.Sleep(3 * h.svc.Quiet)
	if n := h.count(); n != 0 {
		t.Fatalf("hidden folder started %d scans", n)
	}

	off := newHarness(t, library.Library{Watch: false, ScanIntervalMin: 360})
	off.start()
	if m := off.svc.Mode("lib_1"); m != ModeOff {
		t.Fatalf("mode = %q, want off", m)
	}
	write(t, filepath.Join(off.root, "b.mp3"), "x")
	time.Sleep(3 * off.svc.Quiet)
	if n := off.count(); n != 0 {
		t.Fatalf("an unwatched library started %d scans", n)
	}
}

// TestWatchSchedule: the scheduled scan queues only when the last scan is older than the interval, never
// when it is off, and a network drive with no interval of its own gets six hours.
func TestWatchSchedule(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name    string
		lib     library.Library
		last    time.Time
		found   bool
		network bool
		want    int
	}{
		{"never scanned", library.Library{Watch: true, ScanIntervalMin: 360}, time.Time{}, false, false, 1},
		{"scanned recently", library.Library{Watch: true, ScanIntervalMin: 360}, now.Add(-time.Hour), true, false, 0},
		{"scanned long ago", library.Library{Watch: true, ScanIntervalMin: 360}, now.Add(-7 * time.Hour), true, false, 1},
		{"schedule off", library.Library{Watch: true, ScanIntervalMin: 0}, now.Add(-100 * time.Hour), true, false, 0},
		{"watch off", library.Library{Watch: false, ScanIntervalMin: 60}, now.Add(-100 * time.Hour), true, false, 0},
		{"network set to never, recent", library.Library{Watch: true}, now.Add(-time.Hour), true, true, 0},
		{"network set to never, old", library.Library{Watch: true}, now.Add(-700 * time.Hour), true, true, 0},
		{"network with interval, old", library.Library{Watch: true, ScanIntervalMin: 360}, now.Add(-7 * time.Hour), true, true, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t, c.lib)
			h.svc.cfg.Now = func() time.Time { return now }
			h.last, h.found = c.last, c.found
			if c.network {
				h.mount = []Mount{{Point: "/", Type: "ext4"}, {Point: h.root, Type: "cifs"}}
			}
			h.svc.Due(context.Background())
			if n := h.count(); n != c.want {
				t.Errorf("scans = %d, want %d", n, c.want)
			}
		})
	}
}

// TestWatchNetworkFolderNotWatched: a folder on a network drive is not watched; the library shows the
// schedule-only mode.
func TestWatchNetworkFolderNotWatched(t *testing.T) {
	h := newHarness(t, library.Library{Watch: true, ScanIntervalMin: 360})
	h.mount = []Mount{{Point: "/", Type: "ext4"}, {Point: h.root, Type: "nfs4"}}
	h.start()
	if m := h.svc.Mode("lib_1"); m != ModeSchedule {
		t.Fatalf("mode = %q, want schedule", m)
	}
	if len(h.svc.dirs) != 0 {
		t.Fatalf("%d folders watched on a network drive", len(h.svc.dirs))
	}
}

// TestWatchTooManyFolders: past the folder limit the library falls back to its schedule.
func TestWatchTooManyFolders(t *testing.T) {
	h := newHarness(t, library.Library{Watch: true, ScanIntervalMin: 360})
	for i := 0; i < 5; i++ {
		write(t, filepath.Join(h.root, string(rune('a'+i)), "f"), "x")
	}
	h.svc.MaxDirs = 3
	h.start()
	if m := h.svc.Mode("lib_1"); m != ModeSchedule {
		t.Fatalf("mode = %q, want schedule", m)
	}
}

// TestWatchMountGone: a folder that disappears is skipped by the schedule and by change events, and
// when it returns the library is scanned once to catch up.
func TestWatchMountGone(t *testing.T) {
	h := newHarness(t, library.Library{Watch: true, ScanIntervalMin: 1})
	write(t, filepath.Join(h.root, "Book", "a.mp3"), "x")
	h.start()
	hidden := h.root + ".away"
	if err := os.Rename(h.root, hidden); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	h.svc.Nudge()
	h.waitFor("unavailable", func() bool { return h.svc.Mode("lib_1") == ModeUnavailable })
	h.svc.Due(ctx) // would queue (never scanned) if the folder were there
	time.Sleep(3 * h.svc.Quiet)
	if n := h.count(); n != 0 {
		t.Fatalf("%d scans queued while the folder was gone", n)
	}
	if err := os.Rename(hidden, h.root); err != nil {
		t.Fatal(err)
	}
	h.svc.Nudge()
	h.waitFor("catch-up scan", func() bool { return h.count() == 1 })
	if m := h.svc.Mode("lib_1"); m != ModeWatching {
		t.Fatalf("mode = %q after return, want watching", m)
	}
}

// TestWatchNetworkFS: mount table parsing and the longest mount point wins.
func TestWatchNetworkFS(t *testing.T) {
	table := `22 28 0:21 / /sys rw,nosuid - sysfs sysfs rw
28 1 8:2 / / rw,relatime shared:1 - ext4 /dev/sda2 rw
90 28 0:50 / /mnt/synology rw,relatime shared:99 - cifs //nas/books rw,vers=3.1.1
91 90 8:17 / /mnt/synology/local\040disk rw - ext4 /dev/sdb1 rw
92 28 0:51 / /mnt/nfs rw - nfs4 nas:/export rw
93 28 0:52 / /mnt/rc rw - fuse.rclone remote: rw`
	mounts := ParseMountInfo(table)
	if len(mounts) != 6 {
		t.Fatalf("parsed %d mounts, want 6", len(mounts))
	}
	for path, want := range map[string]bool{
		"/home/me/Books":                 false,
		"/mnt/synology":                  true,
		"/mnt/synology/Audiobooks":       true,
		"/mnt/synology/local disk/Books": false, // a local disk mounted inside the share
		"/mnt/synology2":                 false, // not inside /mnt/synology
		"/mnt/nfs/a":                     true,
		"/mnt/rc/x":                      true,
	} {
		if got := IsNetwork(path, mounts); got != want {
			t.Errorf("IsNetwork(%q) = %v, want %v", path, got, want)
		}
	}
	if IsNetwork("/anything", nil) {
		t.Error("no mount table must mean local")
	}
}
