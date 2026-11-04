package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// State is the lifecycle state of a job row.
type State string

// Job states. A row is running if and only if it holds a lease.
const (
	StateQueued    State = "queued"
	StateRunning   State = "running"
	StateDone      State = "done"
	StateFailed    State = "failed"
	StateCancelled State = "cancelled"
	StateHeld      State = "held"
)

// Job is a read-only view of a job row. NULL columns read as zero values.
type Job struct {
	ID, Kind                  string
	State                     State
	Payload, Result, Progress json.RawMessage
	ErrorCode, ErrorText      string
	CreatedBy                 string
	Attempts, MaxAttempts     int
	LeaseUntil, NotBefore     time.Time
	CreatedAt, UpdatedAt      time.Time
}

// Task is what a handler receives for one run of a job.
type Task struct {
	ID      string
	Kind    string
	Attempt int // 1 on the first run; also the fencing token
	Payload json.RawMessage

	mu      sync.Mutex
	pending json.RawMessage // latest progress value, JSON
	dirty   bool            // pending has not been written by the keeper yet
}

// Progress records the latest progress value. It never touches the database:
// the job's keeper goroutine writes it, at most once per ProgressEvery, and the
// last value is written when the job finishes.
func (t *Task) Progress(v any) {
	b, err := json.Marshal(v)
	if err != nil {
		return
	}
	t.mu.Lock()
	t.pending, t.dirty = b, true
	t.mu.Unlock()
}

func (t *Task) takeDirty() (json.RawMessage, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.dirty {
		return nil, false
	}
	t.dirty = false
	return t.pending, true
}

func (t *Task) markDirty() {
	t.mu.Lock()
	t.dirty = true
	t.mu.Unlock()
}

func (t *Task) latest() json.RawMessage {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pending
}

// Handler does the work of one job. A nil result stores no result. Return
// *Error with Permanent set to fail at once; any other error is retried with
// backoff until MaxAttempts. A handler must stop when ctx is cancelled (the
// cause is ErrLeaseLost, ErrCancelled or ErrShutdown).
type Handler func(ctx context.Context, t *Task) (result any, err error)

// Kind registers one type of job.
type Kind struct {
	Name           string
	Concurrency    int // at most this many handlers of this kind run at once; must be >= 1
	MaxAttempts    int // 0 means 3
	HoldOnRecovery bool
	Handler        Handler
}

// Error is a handler failure with a stable code and plain-words text for the
// interface. Code matches ^[a-z_]+$.
type Error struct {
	Code, Text string
	Permanent  bool
	Err        error
}

func (e *Error) Error() string {
	switch {
	case e.Err != nil:
		return fmt.Sprintf("%s: %v", e.Code, e.Err)
	case e.Text != "":
		return e.Code + ": " + e.Text
	}
	return e.Code
}

func (e *Error) Unwrap() error { return e.Err }

// Options tune the runner. Zero values take the defaults noted.
type Options struct {
	Lease         time.Duration // 60s
	RenewEvery    time.Duration // 20s
	ProgressEvery time.Duration // 1s
	SweepEvery    time.Duration // 30s
	PollEvery     time.Duration // 1s
	Backoff       func(attempt int) time.Duration
	Logger        *slog.Logger
}

// EnqueueOptions are the optional parts of Enqueue.
type EnqueueOptions struct {
	NotBefore   time.Time
	CreatedBy   string // a user id, or empty
	MaxAttempts int    // 0 takes the kind's value
}

// Errors returned by the runner. ErrLeaseLost, ErrCancelled and ErrShutdown are
// also the cancellation causes a handler sees through context.Cause.
var (
	ErrNotFound        = errors.New("jobs: job not found")
	ErrAlreadyFinished = errors.New("jobs: job already finished")
	ErrNotHeld         = errors.New("jobs: job is not held")
	ErrUnknownKind     = errors.New("jobs: unknown job kind")
	ErrDuplicateKind   = errors.New("jobs: kind already registered")
	ErrStarted         = errors.New("jobs: runner already started")
	ErrBadOptions      = errors.New("jobs: bad options")
	ErrLeaseLost       = errors.New("jobs: lease lost")
	ErrCancelled       = errors.New("jobs: job cancelled")
	ErrShutdown        = errors.New("jobs: server shutting down")
)

// DefaultBackoff is 10s, doubling with each attempt, capped at 15 minutes.
func DefaultBackoff(attempt int) time.Duration {
	const base, ceiling = 10 * time.Second, 15 * time.Minute
	if attempt < 1 {
		attempt = 1
	}
	if attempt-1 >= 7 {
		return ceiling
	}
	return min(base<<(attempt-1), ceiling)
}

func msToTime(ms sql.NullInt64) time.Time {
	if !ms.Valid || ms.Int64 == 0 {
		return time.Time{}
	}
	return time.UnixMilli(ms.Int64)
}

func toMS(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}

// rowScanner is what scanJob reads: *sql.Row and *sql.Rows.
type rowScanner interface{ Scan(dest ...any) error }

// scanJob reads one jobs row in the column order of sqlGet.
func scanJob(s rowScanner) (Job, error) {
	var (
		j                                      Job
		state                                  string
		payload, result, progress              sql.NullString
		code, text, createdBy                  sql.NullString
		lease, notBefore, createdAt, updatedAt sql.NullInt64
	)
	err := s.Scan(&j.ID, &j.Kind, &state, &payload, &result, &progress,
		&code, &text, &j.Attempts, &j.MaxAttempts, &lease, &notBefore, &createdBy, &createdAt, &updatedAt)
	if err != nil {
		return Job{}, err
	}
	j.State = State(state)
	if payload.Valid {
		j.Payload = json.RawMessage(payload.String)
	}
	if result.Valid {
		j.Result = json.RawMessage(result.String)
	}
	if progress.Valid {
		j.Progress = json.RawMessage(progress.String)
	}
	j.ErrorCode, j.ErrorText, j.CreatedBy = code.String, text.String, createdBy.String
	j.LeaseUntil, j.NotBefore = msToTime(lease), msToTime(notBefore)
	j.CreatedAt, j.UpdatedAt = msToTime(createdAt), msToTime(updatedAt)
	return j, nil
}

// Get returns a job by id, read from the reader pool.
func (r *Runner) Get(ctx context.Context, id string) (Job, error) {
	j, err := scanJob(r.st.Reader().QueryRowContext(ctx, sqlGet, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, ErrNotFound
	}
	return j, err
}
