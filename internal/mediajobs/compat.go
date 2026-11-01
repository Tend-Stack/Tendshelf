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
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
)

// Outcomes of Compat.Run.
const (
	OutcomeReady = "ready" // the copy was already in the cache
	OutcomeBuilt = "built" // the copy was made now
)

var (
	quickHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	cacheFilePattern = regexp.MustCompile(`^[0-9a-f]{64}-r(\d+)\.m4a$`)
)

// Compat makes and looks after compatibility copies: AAC-LC in an .m4a, for browsers that cannot
// play a file as it is. The cache holds nothing the database knows about: the file name is the
// source's quick hash plus the recipe version, its modification time is the last use, and the
// oldest copies go first when the total passes the cap. Losing the cache loses nothing.
//
// Not done here: a free-space check before converting. The cap bounds the cache, and a full disk
// makes ffmpeg fail, which the job reports as ffmpeg_failed.
type Compat struct {
	repo     *library.Repo
	tools    media.Tools
	dir      string
	capBytes int64
	jobs     Enqueuer
	mu       sync.Mutex // guards the cache directory and the check-then-queue in Request
	log      *slog.Logger
	now      func() time.Time
}

// NewCompat returns the compatibility-copy service over the cache directory dir (CompatDir of the
// data directory) with a size cap in bytes.
func NewCompat(repo *library.Repo, tools media.Tools, dir string, capBytes int64, enq Enqueuer, log *slog.Logger) *Compat {
	if log == nil {
		log = slog.Default()
	}
	c := &Compat{repo: repo, tools: tools, dir: dir, capBytes: capBytes, jobs: enq, log: log, now: time.Now}
	sweepTemp(dir, c.now())
	return c
}

// cachePath is where the copy of a file lives, or an error if its quick hash is not a plain hash.
func (c *Compat) cachePath(f *library.AudioFile) (string, error) {
	if !quickHashPattern.MatchString(f.QuickHash) {
		return "", fmt.Errorf("mediajobs: audio file %s has no usable quick hash", f.ID)
	}
	return filepath.Join(c.dir, f.QuickHash+"-r"+strconv.Itoa(media.CompatRecipe)+".m4a"), nil
}

// refuse returns the plain error for a file that must never be converted.
func refuse(f *library.AudioFile) error {
	switch {
	case f.MissingSince != 0:
		return &media.Error{Code: media.CodeFileMissing, Text: media.ErrorText(media.CodeFileMissing)}
	case f.Encrypted || f.ProbeError == media.CodeDRM || media.Classify(f.Path) == media.KindProtected:
		return &media.Error{Code: media.CodeDRM, Text: media.ErrorText(media.CodeDRM), Err: media.ErrProtected}
	case f.ProbeError != "":
		return &media.Error{Code: media.CodeNotAudio, Text: media.ErrorText(media.CodeNotAudio), Detail: f.ProbeError}
	case f.ProbeJSON == nil:
		return &media.Error{Code: media.CodeProbeFailed, Text: media.ErrorText(media.CodeProbeFailed), Detail: "not probed yet"}
	}
	return nil
}

// Lookup returns the cached copy of a file if there is one, and marks it as just used.
func (c *Compat) Lookup(ctx context.Context, fileID string) (string, bool, error) {
	f, err := c.repo.AudioFile(ctx, fileID)
	if err != nil {
		return "", false, err
	}
	p, err := c.cachePath(&f)
	if err != nil {
		return "", false, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !exists(p) {
		return "", false, nil
	}
	now := c.now()
	if err := os.Chtimes(p, now, now); err != nil && !errors.Is(err, os.ErrNotExist) {
		c.log.Warn("compat copy: cannot mark as used", "file", fileID, "err", err)
	}
	return p, true, nil
}

// Request asks for a copy of a file and returns the queued job's ID. It returns "" and no error
// when the copy is already there or a job for the file is already waiting. A file that must not be
// converted (missing, copy-protected, unreadable) or does not need it gives a *media.Error.
func (c *Compat) Request(ctx context.Context, fileID string) (string, error) {
	f, err := c.repo.AudioFile(ctx, fileID)
	if err != nil {
		return "", err
	}
	if err := refuse(&f); err != nil {
		return "", err
	}
	if !media.MayNeedCompat(f.Container, f.Codec) {
		return "", &media.Error{Code: media.CodeCompatNotNeeded, Text: media.ErrorText(media.CodeCompatNotNeeded)}
	}
	p, err := c.cachePath(&f)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if exists(p) {
		return "", nil
	}
	active, err := c.jobs.ActiveKeys(ctx, KindCompat, "audio_file_id")
	if err != nil {
		return "", err
	}
	if active[fileID] {
		return "", nil
	}
	return c.jobs.Enqueue(ctx, KindCompat, CompatPayload{AudioFileID: fileID}, jobs.EnqueueOptions{})
}

// Run makes the copy of one file. The conversion goes to a temporary file that is probed before it
// is renamed into the cache; a copy that is not AAC in MP4, or whose length differs from the
// source by more than 1 % (at least a second), is thrown away. The guard (a job's fence check; nil
// is allowed) runs just before the rename, so a run that lost its lease leaves nothing behind.
func (c *Compat) Run(ctx context.Context, fileID string, guard func(context.Context, *sql.Tx) error) (string, error) {
	f, err := c.repo.AudioFile(ctx, fileID)
	switch {
	case errors.Is(err, library.ErrNotFound):
		return OutcomeGone, nil
	case err != nil:
		return "", err
	case f.MissingSince != 0:
		return OutcomeMissing, nil
	}
	if err := refuse(&f); err != nil {
		return "", err
	}
	if !media.MayNeedCompat(f.Container, f.Codec) {
		return "", &media.Error{Code: media.CodeCompatNotNeeded, Text: media.ErrorText(media.CodeCompatNotNeeded)}
	}
	final, err := c.cachePath(&f)
	if err != nil {
		return "", err
	}
	if exists(final) {
		return OutcomeReady, nil
	}
	if err := os.MkdirAll(c.dir, dirMode); err != nil {
		return "", err
	}
	tmp := filepath.Join(c.dir, tmpPrefix+fileID+".m4a")
	defer os.Remove(tmp) // a no-op after a successful rename

	if err := c.tools.Compat(ctx, f.Path, tmp, f.Codec, f.DurationMs); err != nil {
		return "", err
	}
	if err := c.verify(ctx, tmp, f.DurationMs); err != nil {
		return "", err
	}
	if err := os.Chmod(tmp, fileMode); err != nil {
		return "", err
	}
	if err := c.repo.UpdateGuarded(ctx, guard, func(*library.Tx) error { return nil }); err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := os.Rename(tmp, final); err != nil {
		return "", err
	}
	now := c.now()
	os.Chtimes(final, now, now) // the new copy is the most recently used
	c.evict(final)
	return OutcomeBuilt, nil
}

// verify probes the converted file.
func (c *Compat) verify(ctx context.Context, tmp string, wantMs int64) error {
	fail := func(detail string) error {
		return &media.Error{Code: media.CodeFFmpegFailed, Text: media.ErrorText(media.CodeFFmpegFailed), Detail: detail}
	}
	p, err := c.tools.Probe(ctx, tmp)
	if err != nil {
		var me *media.Error
		if errors.As(err, &me) {
			return fail("converted file does not probe: " + me.Error())
		}
		return err
	}
	if p.Container != "mp4" || len(p.Audio) == 0 || p.Audio[0].Codec != "aac" {
		return fail(fmt.Sprintf("converted file is %s/%v, not mp4/aac", p.Container, p.Audio))
	}
	tol := max(1000, wantMs/100)
	if d := p.DurationMs - wantMs; d > tol || d < -tol {
		return fail(fmt.Sprintf("converted file is %d ms long, source %d ms", p.DurationMs, wantMs))
	}
	return nil
}

// evict removes copies of other recipes, then the least recently used copies until the cache fits
// the cap. keep is never removed. The caller holds mu.
func (c *Compat) evict(keep string) {
	entries, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	type entry struct {
		path  string
		size  int64
		mtime time.Time
	}
	var list []entry
	var total int64
	for _, e := range entries {
		m := cacheFilePattern.FindStringSubmatch(e.Name())
		if m == nil || !e.Type().IsRegular() {
			continue
		}
		p := filepath.Join(c.dir, e.Name())
		if r, _ := strconv.Atoi(m[1]); r != media.CompatRecipe {
			os.Remove(p)
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		list = append(list, entry{p, fi.Size(), fi.ModTime()})
		total += fi.Size()
	}
	sort.Slice(list, func(i, j int) bool {
		if !list[i].mtime.Equal(list[j].mtime) {
			return list[i].mtime.Before(list[j].mtime)
		}
		return list[i].path < list[j].path
	})
	for _, e := range list {
		if total <= c.capBytes {
			return
		}
		if e.path == keep {
			continue
		}
		if err := os.Remove(e.path); err == nil || errors.Is(err, os.ErrNotExist) {
			total -= e.size
		}
	}
}
