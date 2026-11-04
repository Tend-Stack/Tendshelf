// Package backups makes dated copies of the database on a schedule or on request. The copy itself is
// db.WriteCopy; this package owns the job kind, the schedule setting and the "is one due" decision.
package backups

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
)

// Kind is the job kind of one backup.
const Kind = "db.backup"

// Settings and schedule values.
const (
	SettingSchedule = "backup.schedule"
	Off             = "off"
	Daily           = "daily"
	Weekly          = "weekly"

	// slack lets a copy made a little early still count as the period's copy, so a run at the same
	// clock time every day is not skipped by a few minutes of drift.
	slack = 15 * time.Minute
	// retryAfter is how long a failed backup is left alone before the schedule tries again.
	retryAfter = time.Hour
)

// ErrBadSchedule is returned by SetSchedule for a value that is not off, daily or weekly.
var ErrBadSchedule = errors.New("backups: schedule must be off, daily or weekly")

// Service queues and runs backups.
type Service struct {
	st      *db.Store
	runner  *jobs.Runner
	dataDir string
	now     func() time.Time
	log     *slog.Logger

	// Every is the interval between due checks (15 minutes); First is the wait before the first one
	// (2 minutes). Tests shorten them.
	Every, First time.Duration

	mu sync.Mutex // makes "is one active, else queue one" a single step
}

// New returns the service. now may be nil for time.Now.
func New(st *db.Store, runner *jobs.Runner, dataDir string, now func() time.Time, log *slog.Logger) *Service {
	if now == nil {
		now = time.Now
	}
	if log == nil {
		log = slog.Default()
	}
	return &Service{st: st, runner: runner, dataDir: dataDir, now: now, log: log, Every: 15 * time.Minute, First: 2 * time.Minute}
}

// Register adds the job kind to the runner, before the runner starts.
func (s *Service) Register() error {
	return s.runner.Register(jobs.Kind{Name: Kind, Concurrency: 1, MaxAttempts: 1, Handler: s.handle})
}

func (s *Service) handle(ctx context.Context, _ *jobs.Task) (any, error) {
	c, err := db.WriteCopy(ctx, s.st, s.dataDir, s.now())
	if err != nil {
		s.log.Warn("backup failed", "err", err)
		return nil, &jobs.Error{Code: "backup_failed", Text: "The backup didn't finish. Check that the data folder has free space.", Permanent: true, Err: err}
	}
	return map[string]any{"name": c.Name, "bytes": c.Bytes}, nil
}

// Schedule returns the saved schedule; a missing setting is daily.
func (s *Service) Schedule(ctx context.Context) string {
	var v string
	if ok, err := db.GetSetting(ctx, s.st.Reader(), SettingSchedule, &v); err != nil || !ok {
		return Daily
	}
	switch v {
	case Off, Daily, Weekly:
		return v
	}
	return Daily
}

// SetSchedule saves the schedule.
func (s *Service) SetSchedule(ctx context.Context, v string) error {
	switch v {
	case Off, Daily, Weekly:
	default:
		return ErrBadSchedule
	}
	return db.PutSetting(ctx, s.st.Writer(), SettingSchedule, v)
}

// Period is how often the schedule asks for a copy; zero when it is off.
func Period(schedule string) time.Duration {
	switch schedule {
	case Daily:
		return 24 * time.Hour
	case Weekly:
		return 7 * 24 * time.Hour
	}
	return 0
}

// Start queues a backup and returns its job id. When one is already queued or running it returns that
// job instead. reason is "manual" or "scheduled"; by is the person's id or empty.
func (s *Service) Start(ctx context.Context, by, reason string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if j, ok, err := s.runner.ActiveFor(ctx, Kind, "target", "database"); err != nil {
		return "", err
	} else if ok {
		return j.ID, nil
	}
	return s.runner.Enqueue(ctx, Kind, map[string]string{"target": "database", "reason": reason}, jobs.EnqueueOptions{CreatedBy: by})
}

// Latest returns the newest copy, if any.
func (s *Service) Latest() (db.Copy, bool, error) {
	list, err := db.ListCopies(s.dataDir)
	if err != nil || len(list) == 0 {
		return db.Copy{}, false, err
	}
	return list[0], true, nil
}

// Next returns when the next scheduled copy is due, or false when the schedule is off.
func (s *Service) Next(ctx context.Context) (time.Time, bool) {
	p := Period(s.Schedule(ctx))
	if p == 0 {
		return time.Time{}, false
	}
	c, ok, err := s.Latest()
	if err != nil || !ok {
		return s.now(), true
	}
	return c.At.Add(p - slack), true
}

// Due reports whether a scheduled copy should be queued now.
func (s *Service) Due(ctx context.Context) (bool, error) {
	p := Period(s.Schedule(ctx))
	if p == 0 {
		return false, nil
	}
	if _, ok, err := s.runner.ActiveFor(ctx, Kind, "target", "database"); err != nil || ok {
		return false, err
	}
	if j, ok, err := s.runner.LatestFor(ctx, Kind, "target", "database"); err != nil {
		return false, err
	} else if ok && j.State == jobs.StateFailed && s.now().Sub(j.UpdatedAt) < retryAfter {
		return false, nil
	}
	c, ok, err := s.Latest()
	if err != nil {
		return false, err
	}
	return !ok || s.now().Sub(c.At) >= p-slack, nil
}

// Run checks whether a copy is due after First and then every Every, until ctx ends.
func (s *Service) Run(ctx context.Context) {
	wait := s.First
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = s.Every
		due, err := s.Due(ctx)
		if err != nil {
			s.log.Warn("backups: due check failed", "err", err)
			continue
		}
		if !due {
			continue
		}
		if _, err := s.Start(ctx, "", "scheduled"); err != nil {
			s.log.Warn("backups: could not queue a backup", "err", err)
		}
	}
}
