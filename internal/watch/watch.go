// Package watch finds new books without a button press (S1). For each library that has "Watch for new
// books" on it does two things, both read-only on library files:
//
//   - It watches the folders for changes with fsnotify and, once nothing has changed for a quiet
//     period (so a book that is still being copied is not read half written), queues the library's
//     normal scan job.
//   - It queues the same scan on a schedule (every six hours by default), because folders on a network
//     share (NFS, CIFS and the like) give no change events. A folder on a network file system is not
//     watched at all, which also keeps a NAS's disks asleep; it relies on the schedule.
//
// There is no second scanner: both paths end in scan.Enqueue, which joins a scan that is already waiting.
// A folder that is not there (an unmounted drive or share) is skipped, never scanned; when it comes
// back the library is scanned once to catch up.
package watch

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

// Mode is how a library is being kept up to date, for the admin page.
type Mode string

// The modes. The empty Mode means the service has not looked at the library yet.
const (
	ModeOff         Mode = "off"         // Watch for new books is off
	ModeWatching    Mode = "watching"    // change events plus the schedule
	ModeSchedule    Mode = "schedule"    // the schedule only: a network drive, or too many folders to watch
	ModeUnavailable Mode = "unavailable" // a folder is not there right now; checked again when it returns
)

// Source is the part of the library repository the watcher reads.
type Source interface {
	Libraries(ctx context.Context) ([]library.Library, error)
	Folders(ctx context.Context, libraryID string) ([]library.Folder, error)
}

// Config wires the service to the rest of the program.
type Config struct {
	Source Source
	// Enqueue queues a scan of a whole library (scan.Enqueue), joining one that is already waiting.
	Enqueue func(ctx context.Context, libraryID string) error
	// LastScan returns when the library was last scanned; a scan that is queued or running counts as
	// now. found is false when no scan is known.
	LastScan func(ctx context.Context, libraryID string) (at time.Time, found bool, err error)
	// Running reports whether a scan of the library is running right now (not merely waiting). A change
	// seen while one runs is kept until it ends, because that scan may already have walked past the
	// folder. Nil means never running.
	Running func(ctx context.Context, libraryID string) (bool, error)
	// Mounts returns the mount table (ReadMounts); nil means every folder is local.
	Mounts func() []Mount
	Log    *slog.Logger
	Now    func() time.Time
}

// Service is the folder watcher and the scheduled scan.
type Service struct {
	cfg Config

	// Quiet is how long a library must be free of changes before it is scanned (default 45 seconds).
	// Tick is how often the quiet check runs (5 s), ReconcileEvery how often watches are matched to the
	// libraries and folders (5 min), DueEvery how often the schedule is checked (5 min), First the
	// wait before the first of those (30 s). StatTimeout bounds a look at one folder, so a dead
	// mount cannot stall the loop (5 s). MaxDirs caps the folders watched (inotify has a per-user
	// limit); past it the library falls back to the schedule. Tests shorten them.
	Quiet, Tick, ReconcileEvery, DueEvery, First, StatTimeout time.Duration
	MaxDirs                                                   int

	mu    sync.Mutex // guards modes
	modes map[string]Mode

	pmu      sync.Mutex // guards inflight
	inflight map[string]*probeCall
	nudge    chan struct{}

	// Owned by the Run goroutine.
	w     *fsnotify.Watcher
	dirs  map[string]string // watched directory -> library id
	roots map[string]root   // folder path -> state
	dirty map[string]time.Time
	gone  map[string]bool // folders that were not there at the last look
	quiet map[string]bool // libraries already logged as unavailable, so the log is not repeated
}

// probeCall is one look at a path that may still be running; later looks at the same path wait for it
// instead of starting another goroutine, so a hung mount costs one goroutine, not one per check.
type probeCall struct {
	done chan struct{}
	res  probeResult
}

type probeResult struct{ ok, net bool }

type root struct {
	real    string // the path with links resolved: what is actually watched
	library string
	limited bool // too many folders to watch: the schedule covers it
}

// New returns the service with the default timings.
func New(cfg Config) *Service {
	if cfg.Log == nil {
		cfg.Log = slog.Default()
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Mounts == nil {
		cfg.Mounts = ReadMounts
	}
	return &Service{cfg: cfg, Quiet: 45 * time.Second, Tick: 5 * time.Second, ReconcileEvery: 5 * time.Minute,
		DueEvery: 5 * time.Minute, First: 30 * time.Second, StatTimeout: 5 * time.Second, MaxDirs: 20000,
		modes: map[string]Mode{}, inflight: map[string]*probeCall{}, nudge: make(chan struct{}, 1),
		dirs: map[string]string{}, roots: map[string]root{}, dirty: map[string]time.Time{},
		gone: map[string]bool{}, quiet: map[string]bool{}}
}

// Mode returns how the library is being kept up to date, as of the last look.
func (s *Service) Mode(libraryID string) Mode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.modes[libraryID]
}

// Nudge asks the service to match its watches to the libraries now, after a setting changed. It never blocks.
func (s *Service) Nudge() {
	select {
	case s.nudge <- struct{}{}:
	default:
	}
}

// Run watches and schedules until ctx ends. It is one goroutine; the rest of the program only reads Mode.
func (s *Service) Run(ctx context.Context) {
	var events <-chan fsnotify.Event
	var errs <-chan error
	if w, err := fsnotify.NewWatcher(); err != nil {
		s.cfg.Log.Warn("watch: folder watching is not available; libraries are checked on their schedule", "err", err)
	} else {
		s.w = w
		events, errs = w.Events, w.Errors
		defer w.Close()
	}
	tick := time.NewTicker(s.Tick)
	defer tick.Stop()
	start := s.cfg.Now()
	nextReconcile, nextDue := start.Add(s.First), start.Add(s.First)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			s.onEvent(ev)
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			// An overflowed event queue means changes were missed: scan every watched library.
			s.cfg.Log.Warn("watch: lost track of some changes; scanning to catch up", "err", err)
			now := s.cfg.Now()
			for _, r := range s.roots {
				s.dirty[r.library] = now
			}
		case <-s.nudge:
			s.Reconcile(ctx)
		case <-tick.C:
			s.flush(ctx)
			now := s.cfg.Now()
			if !now.Before(nextReconcile) {
				s.Reconcile(ctx)
				nextReconcile = now.Add(s.ReconcileEvery)
			}
			if !now.Before(nextDue) {
				s.Due(ctx)
				nextDue = now.Add(s.DueEvery)
			}
		}
	}
}

// hidden reports whether the entry is skipped by scans too: a name that starts with a dot. That covers
// Tendshelf's own hidden upload folders, which would otherwise start a second scan for every upload.
func hidden(name string) bool { return strings.HasPrefix(filepath.Base(name), ".") }

func (s *Service) onEvent(ev fsnotify.Event) {
	if ev.Op == fsnotify.Chmod || hidden(ev.Name) {
		return
	}
	lib, ok := s.dirs[filepath.Dir(ev.Name)]
	if !ok {
		return
	}
	if ev.Has(fsnotify.Create) {
		// A new folder may already hold files, created before its watch existed.
		if fi, err := os.Lstat(ev.Name); err == nil && fi.IsDir() {
			s.addTree(ev.Name, lib)
		}
	}
	if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
		s.forget(ev.Name)
	}
	s.dirty[lib] = s.cfg.Now()
}

// forget drops the bookkeeping for a directory and everything below it (the kernel already dropped the watches).
func (s *Service) forget(dir string) {
	if _, ok := s.dirs[dir]; !ok {
		return // a file, or a folder never watched: nothing below it to drop
	}
	for d := range s.dirs {
		if within(dir, d) {
			delete(s.dirs, d)
		}
	}
}

// addTree watches dir and every folder below it, skipping hidden folders and symbolic links. It returns
// an error when the folder limit or the system's watch limit is reached.
func (s *Service) addTree(dir, lib string) error {
	if s.w == nil {
		return errors.New("watch: no watcher")
	}
	// A library folder that is itself a link is walked from where it points; WalkDir would not follow it.
	if real, err := filepath.EvalSymlinks(dir); err == nil {
		dir = real
	}
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return fs.SkipDir // a folder we cannot read: the scan reports it
			}
			return nil
		}
		if !d.IsDir() {
			return nil
		}
		if p != dir && hidden(p) {
			return fs.SkipDir
		}
		if _, ok := s.dirs[p]; ok {
			return nil
		}
		if len(s.dirs) >= s.MaxDirs {
			return errors.New("watch: too many folders to watch")
		}
		if err := s.w.Add(p); err != nil {
			return err
		}
		s.dirs[p] = lib
		return nil
	})
}

// removeTree stops watching a folder and everything below it.
func (s *Service) removeTree(dir string) {
	targets := []string{dir}
	if r, ok := s.roots[dir]; ok && r.real != "" && r.real != dir {
		targets = append(targets, r.real)
	}
	for d := range s.dirs {
		for _, t := range targets {
			if within(t, d) {
				if s.w != nil {
					s.w.Remove(d)
				}
				delete(s.dirs, d)
				break
			}
		}
	}
}

// probe looks at one folder with a deadline: whether it is there as a directory, and whether it sits on
// a network file system.
func (s *Service) probe(path string, mounts []Mount) (available, network bool) {
	s.pmu.Lock()
	c, running := s.inflight[path]
	if !running {
		c = &probeCall{done: make(chan struct{})}
		s.inflight[path] = c
		go func() {
			c.res = look(path, mounts)
			s.pmu.Lock()
			delete(s.inflight, path)
			s.pmu.Unlock()
			close(c.done)
		}()
	}
	s.pmu.Unlock()
	select {
	case <-c.done:
		return c.res.ok, c.res.net
	case <-time.After(s.StatTimeout):
		return false, false
	}
}

// look is the real look at a path; a test swaps it for one that hangs.
var look = lookAt

func lookAt(path string, mounts []Mount) probeResult {
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		return probeResult{}
	}
	fi, err := os.Stat(real)
	return probeResult{ok: err == nil && fi.IsDir(), net: IsNetwork(real, mounts)}
}

// Reconcile matches the watches to the libraries: it starts watching folders that are new, local and
// there, stops watching folders that were removed, switched off, moved to a network drive or lost, and
// records each library's mode. A folder that returns after being lost marks its library for a catch-up scan.
func (s *Service) Reconcile(ctx context.Context) {
	libs, err := s.cfg.Source.Libraries(ctx)
	if err != nil {
		s.cfg.Log.Warn("watch: could not list libraries", "err", err)
		return
	}
	mounts := s.cfg.Mounts()
	wanted := map[string]bool{}
	modes := map[string]Mode{}
	for _, lib := range libs {
		if !lib.Watch {
			modes[lib.ID] = ModeOff
			continue
		}
		folders, err := s.cfg.Source.Folders(ctx, lib.ID)
		if err != nil {
			s.cfg.Log.Warn("watch: could not list folders", "library", lib.ID, "err", err)
			continue
		}
		mode := ModeWatching
		for _, f := range folders {
			avail, network := s.probe(f.Path, mounts)
			switch {
			case !avail:
				s.removeTree(f.Path)
				delete(s.roots, f.Path)
				s.gone[f.Path] = true
				mode = ModeUnavailable
			case network:
				s.removeTree(f.Path)
				delete(s.roots, f.Path)
				if mode == ModeWatching {
					mode = ModeSchedule
				}
			default:
				wanted[f.Path] = true
				r, known := s.roots[f.Path]
				if !known {
					r = root{library: lib.ID, real: f.Path}
					if real, err := filepath.EvalSymlinks(f.Path); err == nil {
						r.real = real
					}
					s.roots[f.Path] = r
					if err := s.addTree(f.Path, lib.ID); err != nil {
						s.cfg.Log.Warn("watch: cannot watch this folder, so it is checked on its schedule", "folder", f.Path, "err", err)
						s.removeTree(f.Path)
						r.limited = true
					}
					s.roots[f.Path] = r
					if s.gone[f.Path] {
						s.dirty[lib.ID] = s.cfg.Now() // it came back: look once for what changed meanwhile
					}
				}
				delete(s.gone, f.Path)
				if r.limited && mode == ModeWatching {
					mode = ModeSchedule
				}
			}
		}
		modes[lib.ID] = mode
	}
	for path := range s.roots {
		if !wanted[path] {
			s.removeTree(path)
			delete(s.roots, path)
		}
	}
	s.mu.Lock()
	s.modes = modes
	s.mu.Unlock()
}

// libraryThere reports whether every folder of the library is there; a library with none is not.
func (s *Service) libraryThere(ctx context.Context, libraryID string) bool {
	folders, err := s.cfg.Source.Folders(ctx, libraryID)
	if err != nil || len(folders) == 0 {
		return false
	}
	mounts := s.cfg.Mounts()
	for _, f := range folders {
		if ok, _ := s.probe(f.Path, mounts); !ok {
			return false
		}
	}
	return true
}

// flush queues a scan for each library whose folders have been quiet for Quiet.
func (s *Service) flush(ctx context.Context) {
	now := s.cfg.Now()
	for lib, last := range s.dirty {
		if now.Sub(last) < s.Quiet {
			continue
		}
		if s.cfg.Running != nil {
			// A scan that is already walking may have passed the folder where the change happened, so
			// joining it would lose the change. Keep it until that scan ends, then queue a fresh one.
			if running, err := s.cfg.Running(ctx, lib); err == nil && running {
				continue
			}
		}
		delete(s.dirty, lib)
		if !s.libraryThere(ctx, lib) {
			s.cfg.Log.Info("watch: a folder is not available, so no scan was queued", "library", lib)
			continue
		}
		if err := s.cfg.Enqueue(ctx, lib); err != nil {
			s.cfg.Log.Warn("watch: could not queue a scan", "library", lib, "err", err)
		}
	}
}

// Interval is the scheduled scan of a library: its own setting in minutes. Zero means Never, on a local
// drive and on a network drive alike (migration 0025 turned the old "unset" zeros into six hours, and a
// new library starts at six hours), so a network drive set to Never is left alone.
func Interval(lib library.Library) time.Duration {
	return time.Duration(lib.ScanIntervalMin) * time.Minute
}

// Due queues the scheduled scan of every watched library whose last scan is older than its interval.
func (s *Service) Due(ctx context.Context) {
	libs, err := s.cfg.Source.Libraries(ctx)
	if err != nil {
		s.cfg.Log.Warn("watch: could not list libraries", "err", err)
		return
	}
	mounts := s.cfg.Mounts()
	now := s.cfg.Now()
	for _, lib := range libs {
		if !lib.Watch {
			continue
		}
		network := false
		folders, err := s.cfg.Source.Folders(ctx, lib.ID)
		if err != nil || len(folders) == 0 {
			continue
		}
		there := true
		for _, f := range folders {
			ok, net := s.probe(f.Path, mounts)
			there = there && ok
			network = network || net
		}
		every := Interval(lib)
		if every == 0 {
			continue
		}
		if !there {
			if !s.quiet[lib.ID] {
				s.cfg.Log.Info("watch: a folder is not available, so the scheduled scan was skipped", "library", lib.ID)
				s.quiet[lib.ID] = true
			}
			continue
		}
		delete(s.quiet, lib.ID)
		at, found, err := s.cfg.LastScan(ctx, lib.ID)
		if err != nil {
			s.cfg.Log.Warn("watch: could not read the last scan", "library", lib.ID, "err", err)
			continue
		}
		if found && now.Sub(at) < every {
			continue
		}
		if err := s.cfg.Enqueue(ctx, lib.ID); err != nil {
			s.cfg.Log.Warn("watch: could not queue a scan", "library", lib.ID, "err", err)
		}
	}
}
