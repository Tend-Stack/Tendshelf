package mediajobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"sync"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
)

// peaksFilePattern matches the cache file names: the quick hash and the format version.
var peaksFilePattern = regexp.MustCompile(`^([0-9a-f]{64})-v(\d+)\.peaks$`)

// peaksSweepEvery is how often a scan follower may sweep stale peaks files.
const peaksSweepEvery = 10 * time.Minute

// PeaksQueue is the part of jobs.Runner the waveform service uses.
type PeaksQueue interface {
	Enqueuer
	ActiveFor(ctx context.Context, kind, field, value string) (jobs.Job, bool, error)
	Expedite(ctx context.Context, id string) error
}

// Peaks makes and looks after waveform peaks: the loudness of each audio file in half-second bins
// (media.Peaks), one small file per quick hash under PeaksDir. Like the compatibility copies the
// cache holds nothing the database knows about: the name is the source's quick hash plus the format
// version, so a changed file gets a new name and a stale file can never be served. Losing the
// directory loses nothing; the files are rebuilt in the background.
//
// Jobs are cheap to queue and run one at a time at a low priority: settled books and the first
// start queue them at leisure, and a listener who plays a book without peaks moves its job to the
// front of the line (Request).
type Peaks struct {
	repo      *library.Repo
	tools     media.Tools
	dir       string
	jobs      PeaksQueue
	enabled   bool
	mu        sync.Mutex // guards the cache directory and the check-then-queue in Request
	log       *slog.Logger
	now       func() time.Time
	lastSweep time.Time
}

// NewPeaks returns the waveform service over the cache directory dir (PeaksDir of the data
// directory). With enabled false, or without ffmpeg, it is off: nothing is queued and Lookup finds
// nothing.
func NewPeaks(repo *library.Repo, tools media.Tools, dir string, q PeaksQueue, enabled bool, log *slog.Logger) *Peaks {
	if log == nil {
		log = slog.Default()
	}
	p := &Peaks{repo: repo, tools: tools, dir: dir, jobs: q, enabled: enabled && tools.FFmpeg != "", log: log, now: time.Now}
	sweepTemp(dir, p.now())
	return p
}

// Enabled reports whether waveforms are produced and served.
func (p *Peaks) Enabled() bool { return p != nil && p.enabled }

// Dir is the directory the peaks files live in.
func (p *Peaks) Dir() string { return p.dir }

// path is where the peaks of a file live, or an error if its quick hash is not a plain hash.
func (p *Peaks) path(f *library.AudioFile) (string, error) {
	if !quickHashPattern.MatchString(f.QuickHash) {
		return "", fmt.Errorf("mediajobs: audio file %s has no usable quick hash", f.ID)
	}
	return filepath.Join(p.dir, f.QuickHash+"-v"+strconv.Itoa(media.PeaksFormat)+".peaks"), nil
}

// Lookup returns the peaks file of an audio file. The bool is false when waveforms are off or the
// file does not exist yet.
func (p *Peaks) Lookup(f library.AudioFile) (string, bool, error) {
	if !p.Enabled() {
		return "", false, nil
	}
	path, err := p.path(&f)
	if err != nil {
		return "", false, err
	}
	return path, exists(path), nil
}

// Request asks for the peaks of a file because someone is listening to it. It returns true when the
// peaks are on their way (a job waits, now moved to the front, or was just queued), false when
// there is nothing to wait for (off, or already built). A file that must not be decoded gives the
// *media.Error of Refusal.
func (p *Peaks) Request(ctx context.Context, f library.AudioFile) (bool, error) {
	if !p.Enabled() {
		return false, nil
	}
	if err := Refusal(&f); err != nil {
		return false, err
	}
	path, err := p.path(&f)
	if err != nil {
		return false, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if exists(path) {
		return false, nil
	}
	job, found, err := p.jobs.ActiveFor(ctx, KindPeaks, "audio_file_id", f.ID)
	if err != nil {
		return false, err
	}
	if found {
		if job.State == jobs.StateQueued {
			if err := p.jobs.Expedite(ctx, job.ID); err != nil {
				return false, err
			}
		}
		return true, nil
	}
	if _, err := p.jobs.Enqueue(ctx, KindPeaks, PeaksPayload{AudioFileID: f.ID}, jobs.EnqueueOptions{}); err != nil {
		return false, err
	}
	return true, nil
}

// Run makes the peaks of one file. The guard (a job's fence check; nil is allowed) runs just before
// the write, so a run that lost its lease leaves nothing behind. The bytes depend only on the
// source, so two runs that race write the same file.
func (p *Peaks) Run(ctx context.Context, fileID string, guard func(context.Context, *sql.Tx) error) (string, error) {
	f, err := p.repo.AudioFile(ctx, fileID)
	switch {
	case errors.Is(err, library.ErrNotFound):
		return OutcomeGone, nil
	case err != nil:
		return "", err
	case f.MissingSince != 0:
		return OutcomeMissing, nil
	}
	if err := Refusal(&f); err != nil {
		return "", err
	}
	path, err := p.path(&f)
	if err != nil {
		return "", err
	}
	if exists(path) {
		return OutcomeReady, nil
	}
	peaks, err := p.tools.Peaks(ctx, f.Path, f.DurationMs)
	if err != nil {
		return "", err
	}
	if err := p.repo.UpdateGuarded(ctx, guard, func(*library.Tx) error { return nil }); err != nil {
		return "", err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := writeAtomic(p.dir, filepath.Base(path), peaks.Encode()); err != nil {
		return "", err
	}
	return OutcomeBuilt, nil
}

// wanted returns the files of the list that have no peaks and no waiting job, and are fit to decode.
func (p *Peaks) wanted(ctx context.Context, files []library.AudioFile) ([]library.AudioFile, error) {
	active, err := p.jobs.ActiveKeys(ctx, KindPeaks, "audio_file_id")
	if err != nil {
		return nil, err
	}
	var out []library.AudioFile
	for _, f := range files {
		if active[f.ID] || Refusal(&f) != nil {
			continue
		}
		path, err := p.path(&f)
		if err != nil || exists(path) {
			continue
		}
		out = append(out, f)
	}
	return out, nil
}

// queue puts a job for each file at the back of the line.
func (p *Peaks) queue(ctx context.Context, files []library.AudioFile) int {
	n := 0
	for _, f := range files {
		_, err := p.jobs.Enqueue(ctx, KindPeaks, PeaksPayload{AudioFileID: f.ID}, jobs.EnqueueOptions{NotBefore: p.now()})
		if err != nil {
			p.log.Error("peaks jobs: cannot queue", "file", f.ID, "err", err)
			continue
		}
		n++
	}
	return n
}

// Follower returns the scan follower: it queues peaks jobs for the new and changed files of each
// settled book, and now and then sweeps the cache.
func (p *Peaks) Follower() func(context.Context, []string) {
	return func(ctx context.Context, bookIDs []string) {
		if !p.Enabled() {
			return
		}
		for _, id := range bookIDs {
			files, err := p.repo.AudioFiles(ctx, id)
			if err != nil {
				p.log.Error("peaks jobs: cannot list files", "book", id, "err", err)
				continue
			}
			want, err := p.wanted(ctx, files)
			if err != nil {
				p.log.Error("peaks jobs: cannot list waiting jobs", "err", err)
				return
			}
			p.queue(ctx, want)
		}
		p.maybeSweep(ctx)
	}
}

// Backfill queues peaks jobs for every existing file that has none, and sweeps the cache. The
// scanner reports only the books it touched, so this is how a library that was scanned before
// waveforms existed gets them. It returns the number of jobs queued.
func (p *Peaks) Backfill(ctx context.Context) (int, error) {
	if !p.Enabled() {
		return 0, nil
	}
	files, err := p.repo.PeaksCandidates(ctx)
	if err != nil {
		return 0, err
	}
	want, err := p.wanted(ctx, files)
	if err != nil {
		return 0, err
	}
	n := p.queue(ctx, want)
	return n, p.Sweep(ctx)
}

func (p *Peaks) maybeSweep(ctx context.Context) {
	p.mu.Lock()
	due := p.now().Sub(p.lastSweep) >= peaksSweepEvery
	if due {
		p.lastSweep = p.now()
	}
	p.mu.Unlock()
	if due {
		if err := p.Sweep(ctx); err != nil {
			p.log.Warn("peaks cache: sweep failed", "err", err)
		}
	}
}

// Sweep removes the peaks files of other format versions and those of a quick hash no audio file
// row carries. Rows of missing files keep their files, so a restored file needs no rebuild. If the
// hashes cannot be read, nothing is removed.
func (p *Peaks) Sweep(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	hashes, err := p.repo.QuickHashes(ctx)
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(p.dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		m := peaksFilePattern.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		if v, _ := strconv.Atoi(m[2]); v == media.PeaksFormat && hashes[m[1]] {
			continue
		}
		if err := os.Remove(filepath.Join(p.dir, e.Name())); err != nil && !errors.Is(err, os.ErrNotExist) {
			p.log.Warn("peaks cache: cannot remove", "file", e.Name(), "err", err)
		}
	}
	return nil
}
