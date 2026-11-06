// Package backups makes dated copies of the database on a schedule or on request. The copy itself is
// db.WriteCopy; this package owns the job kind, the schedule setting and the "is one due" decision.
package backups

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"
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
	// SettingFolderFailed is the Unix ms of the last run that made its local copy but could not complete
	// the backup folder; it is cleared when a run completes it. Retries read it to avoid piling up copies.
	SettingFolderFailed = "backup.folder_failed_at"
	Off                 = "off"
	Daily               = "daily"
	Weekly              = "weekly"

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

	// LibraryFolders returns the library folder paths; a backup folder inside one is refused. Nil
	// means no library check (tests).
	LibraryFolders func(ctx context.Context) ([]string, error)

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

func (s *Service) handle(ctx context.Context, t *jobs.Task) (any, error) {
	var p struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(t.Payload, &p)
	res, err := s.Backup(ctx, p.Reason)
	if err == nil {
		return res, nil
	}
	s.log.Warn("backup failed", "err", err)
	switch {
	case errors.Is(err, ErrDiskFull):
		return res, &jobs.Error{Code: "backup_folder_full", Text: TextFolderFull, Permanent: true, Err: err}
	case errors.Is(err, ErrDriveMissing):
		return res, &jobs.Error{Code: "backup_drive_missing", Text: TextDriveMissing, Permanent: true, Err: err}
	case errors.Is(err, errFolderRefused):
		return res, &jobs.Error{Code: "backup_folder_refused", Text: TextFolderRefused, Permanent: true, Err: err}
	case errors.Is(err, errFolderStep):
		return res, &jobs.Error{Code: "backup_folder_failed", Text: TextFolderFailed, Permanent: true, Err: err}
	}
	return nil, &jobs.Error{Code: "backup_failed", Text: "The backup didn't finish. Check that the data folder has free space.", Permanent: true, Err: err}
}

// Plain sentences for a backup that made its local copy but could not complete the backup folder.
const (
	TextFolderFull    = "The backup folder is full, so the copy stopped. Free some space there or choose another folder. The database copy in the data folder was made."
	TextFolderRefused = "The backup folder can't be used any more: it is missing, can't be written to, or is now inside a library or the data folder. Choose it again. The database copy in the data folder was made."
	TextDriveMissing  = "The backup drive may be unplugged: the backup folder is there but the earlier backup is not. Plug the drive in, or choose the folder again to start a new backup there. The database copy in the data folder was made."
	TextFolderFailed  = "The copy to the backup folder didn't finish. Check that the folder is still there and has free space. The database copy in the data folder was made."
)

var (
	errFolderRefused = errors.New("backups: the backup folder was refused")
	errFolderStep    = errors.New("backups: copying to the backup folder failed")
)

// Result is what a finished backup job reports.
type Result struct {
	Name    string `json:"name"`
	Bytes   int64  `json:"bytes"`
	Folder  bool   `json:"folder"`            // a backup folder is set and was filled
	Files   int    `json:"files,omitempty"`   // files copied to it this time
	Same    int    `json:"same,omitempty"`    // files already there
	Gone    int    `json:"removed,omitempty"` // files removed after Grace
	Copies  int    `json:"copies,omitempty"`  // database copies placed in it
	Skipped int    `json:"skipped,omitempty"` // files or folders that could not be read or copied
	Note    string `json:"note,omitempty"`    // plain words about Skipped
}

// Backup makes a verified database copy in the data folder and, when the owner set a backup folder,
// mirrors the data folder's own files and the database copies into it. A scheduled run that follows a
// failed one within retryAfter does not make another local copy, so retries cannot push good older
// copies out of the newest-seven list. The failure is remembered in SettingFolderFailed, not guessed from
// the age of the copy: a copy over an hour old is still the one the failed run made.
func (s *Service) Backup(ctx context.Context, reason string) (Result, error) {
	var res Result
	folder := s.Folder(ctx)
	skipLocal := false
	if reason == "scheduled" && folder != "" {
		skipLocal = s.retryOfFailedFolder()
	}
	if !skipLocal {
		c, err := db.WriteCopy(ctx, s.st, s.dataDir, s.now())
		if err != nil {
			return res, err
		}
		res.Name, res.Bytes = c.Name, c.Bytes
	}
	if folder == "" {
		return res, nil
	}
	libs, err := s.libraryFolders(ctx)
	if err != nil {
		return res, fmt.Errorf("%w: %v", errFolderStep, err)
	}
	res, err = s.fillFolder(ctx, folder, libs, res)
	if err != nil {
		_ = db.PutSetting(ctx, s.st.Writer(), SettingFolderFailed, s.now().UnixMilli())
		return res, err
	}
	_ = db.DeleteSetting(ctx, s.st.Writer(), SettingFolderFailed)
	if err := db.PutSetting(ctx, s.st.Writer(), SettingFolderOK, s.now().UnixMilli()); err != nil {
		return res, err
	}
	return res, nil
}

// fillFolder checks the backup folder and mirrors into it.
func (s *Service) fillFolder(ctx context.Context, folder string, libs []string, res Result) (Result, error) {
	if err := CheckFolder(folder, s.dataDir, libs); err != nil {
		if errors.Is(err, ErrDiskFull) {
			return res, err
		}
		return res, fmt.Errorf("%w: %v", errFolderRefused, err)
	}
	// Once filled, the folder must still hold its backup. A drive that lost its disk leaves an empty mount
	// point on the system disk, and copying into it would fill that disk and then record success.
	if _, filled := s.FolderDone(ctx); filled && !HasBackup(folder) {
		return res, ErrDriveMissing
	}
	rep, err := Mirror(ctx, s.dataDir, folder, s.now())
	res.Folder, res.Files, res.Same, res.Gone, res.Copies, res.Skipped = true, rep.Copied, rep.Same, rep.Removed, rep.Copies, rep.Problems
	if rep.Problems == 1 {
		res.Note = "1 file could not be read, so it was left out. The rest was backed up."
	} else if rep.Problems > 1 {
		res.Note = fmt.Sprintf("%d files could not be read, so they were left out. The rest was backed up.", rep.Problems)
	}
	if err != nil {
		if errors.Is(err, ErrDiskFull) {
			return res, err
		}
		return res, fmt.Errorf("%w: %v", errFolderStep, err)
	}
	return res, nil
}

// retryOfFailedFolder reports whether the last run made its local copy and failed at the backup folder,
// with that copy still fresh (under a day), so this retry leaves the copy history alone.
func (s *Service) retryOfFailedFolder() bool {
	var ms int64
	if ok, err := db.GetSetting(context.Background(), s.st.Reader(), SettingFolderFailed, &ms); err != nil || !ok || ms <= 0 {
		return false
	}
	c, ok, err := s.Latest()
	return err == nil && ok && s.now().Sub(c.At) < 24*time.Hour
}

func (s *Service) libraryFolders(ctx context.Context) ([]string, error) {
	if s.LibraryFolders == nil {
		return nil, nil
	}
	return s.LibraryFolders(ctx)
}

// Folder returns the backup folder the owner chose, or "" when there is none.
func (s *Service) Folder(ctx context.Context) string {
	var v string
	if ok, err := db.GetSetting(ctx, s.st.Reader(), SettingFolder, &v); err != nil || !ok {
		return ""
	}
	return v
}

// SetFolder saves the backup folder after CheckFolder accepts it; "" turns it off. The error is one of
// the ErrFolder* values (or ErrDiskFull) for a folder that is refused, and nothing is saved then.
func (s *Service) SetFolder(ctx context.Context, path string) error {
	path = strings.TrimSpace(path)
	if path == "" {
		return db.DeleteSetting(ctx, s.st.Writer(), SettingFolder)
	}
	libs, err := s.libraryFolders(ctx)
	if err != nil {
		return err
	}
	path = filepath.Clean(path)
	if err := CheckFolder(path, s.dataDir, libs); err != nil {
		return err
	}
	// Saving a folder, even the same one again, starts it fresh: the next run fills it without asking for
	// the earlier backup. This is how the owner starts over on purpose.
	if err := db.DeleteSetting(ctx, s.st.Writer(), SettingFolderOK); err != nil {
		return err
	}
	_ = db.DeleteSetting(ctx, s.st.Writer(), SettingFolderFailed)
	return db.PutSetting(ctx, s.st.Writer(), SettingFolder, path)
}

// FolderDone returns when the backup folder was last filled and verified; false if never.
func (s *Service) FolderDone(ctx context.Context) (time.Time, bool) {
	var ms int64
	if ok, err := db.GetSetting(ctx, s.st.Reader(), SettingFolderOK, &ms); err != nil || !ok || ms <= 0 {
		return time.Time{}, false
	}
	return time.UnixMilli(ms), true
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

// lastDone is when the backup the schedule counts from was made: the newest database copy and, when a
// backup folder is set, the last time that folder was filled, whichever is older.
func (s *Service) lastDone(ctx context.Context) (time.Time, bool) {
	c, ok, err := s.Latest()
	if err != nil || !ok {
		return time.Time{}, false
	}
	at := c.At
	if s.Folder(ctx) != "" {
		done, ok := s.FolderDone(ctx)
		if !ok {
			return time.Time{}, false
		}
		if done.Before(at) {
			at = done
		}
	}
	return at, true
}

// Next returns when the next scheduled copy is due, or false when the schedule is off.
func (s *Service) Next(ctx context.Context) (time.Time, bool) {
	p := Period(s.Schedule(ctx))
	if p == 0 {
		return time.Time{}, false
	}
	at, ok := s.lastDone(ctx)
	if !ok {
		return s.now(), true
	}
	return at.Add(p - slack), true
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
	at, ok := s.lastDone(ctx)
	return !ok || s.now().Sub(at) >= p-slack, nil
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
