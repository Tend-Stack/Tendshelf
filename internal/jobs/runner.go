package jobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"runtime/debug"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

var codePattern = regexp.MustCompile(`^[a-z_]+$`)

// Runner claims queued jobs from the database and runs them.
type Runner struct {
	st  *db.Store
	o   Options
	log *slog.Logger

	mu       sync.Mutex
	kinds    map[string]Kind
	running  map[string]int     // handlers per kind; a slot frees when the goroutine returns
	active   map[string]*active // job id -> running handler
	started  bool
	stopping bool

	base     context.Context // parent of every handler context; cancelled with ErrShutdown
	cancel   context.CancelCauseFunc
	dbCtx    context.Context // for database calls that must outlive shutdown
	stopc    chan struct{}
	wake     chan struct{}
	wg       sync.WaitGroup
	progress atomic.Int64 // keeper progress writes (test hook)
}

type active struct {
	id       string
	kind     Kind
	attempts int
	max      int
	task     *Task
	ctx      context.Context
	cancel   context.CancelCauseFunc
	claimed  time.Time
	stop     chan struct{}
	done     chan struct{}
}

// New returns a runner over st. Register kinds, then Start.
func New(st *db.Store, o Options) (*Runner, error) {
	if o.Lease == 0 {
		o.Lease = 60 * time.Second
	}
	if o.RenewEvery == 0 {
		o.RenewEvery = 20 * time.Second
	}
	if o.ProgressEvery == 0 {
		o.ProgressEvery = time.Second
	}
	if o.SweepEvery == 0 {
		o.SweepEvery = 30 * time.Second
	}
	if o.PollEvery == 0 {
		o.PollEvery = time.Second
	}
	if o.Backoff == nil {
		o.Backoff = DefaultBackoff
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	for _, d := range []time.Duration{o.Lease, o.RenewEvery, o.ProgressEvery, o.SweepEvery, o.PollEvery} {
		if d <= 0 {
			return nil, fmt.Errorf("%w: durations must be positive", ErrBadOptions)
		}
	}
	if 2*o.RenewEvery > o.Lease {
		return nil, fmt.Errorf("%w: 2*RenewEvery must not exceed Lease", ErrBadOptions)
	}
	return &Runner{
		st:      st,
		o:       o,
		log:     o.Logger,
		kinds:   map[string]Kind{},
		running: map[string]int{},
		active:  map[string]*active{},
		stopc:   make(chan struct{}),
		wake:    make(chan struct{}, 1),
	}, nil
}

// Register adds a job kind. It must be called before Start.
func (r *Runner) Register(k Kind) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.started:
		return ErrStarted
	case k.Name == "" || k.Concurrency < 1 || k.Handler == nil || k.MaxAttempts < 0:
		return fmt.Errorf("%w: kind %q needs a name, Concurrency >= 1 and a Handler", ErrBadOptions, k.Name)
	}
	if _, ok := r.kinds[k.Name]; ok {
		return ErrDuplicateKind
	}
	if k.MaxAttempts == 0 {
		k.MaxAttempts = 3
	}
	r.kinds[k.Name] = k
	return nil
}

// Start recovers jobs left running by a stopped server, then starts the
// dispatcher and the periodic sweep. ctx only supplies values; call Stop (or
// cancel ctx, which does the same) to shut down.
func (r *Runner) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return ErrStarted
	}
	r.started = true
	r.dbCtx = context.WithoutCancel(ctx)
	r.base, r.cancel = context.WithCancelCause(r.dbCtx)
	r.mu.Unlock()

	if err := r.recoverExpired(r.dbCtx); err != nil {
		return fmt.Errorf("recover jobs: %w", err)
	}
	r.wg.Add(2)
	go r.dispatch()
	go r.sweep()
	go func() {
		select {
		case <-ctx.Done():
			r.beginStop()
		case <-r.stopc:
		}
	}()
	r.signal()
	return nil
}

func (r *Runner) beginStop() {
	r.mu.Lock()
	if r.stopping {
		r.mu.Unlock()
		return
	}
	r.stopping = true
	close(r.stopc)
	cancel := r.cancel
	r.mu.Unlock()
	if cancel != nil {
		cancel(ErrShutdown)
	}
}

// Stop stops claiming, cancels every handler with cause ErrShutdown, and waits
// for them to return and write their state, or for ctx. On ctx expiry it
// returns ctx.Err(); rows of handlers still running stay running and are
// recovered by the next start.
func (r *Runner) Stop(ctx context.Context) error {
	r.beginStop()
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runner) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// Enqueue adds a queued job of a registered kind and returns its id.
func (r *Runner) Enqueue(ctx context.Context, kind string, payload any, o EnqueueOptions) (string, error) {
	r.mu.Lock()
	k, ok := r.kinds[kind]
	r.mu.Unlock()
	if !ok {
		return "", ErrUnknownKind
	}
	body := []byte("{}")
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return "", fmt.Errorf("jobs: encode payload: %w", err)
		}
		body = b
	}
	max := o.MaxAttempts
	if max <= 0 {
		max = k.MaxAttempts
	}
	var createdBy any
	if o.CreatedBy != "" {
		createdBy = o.CreatedBy
	}
	id := db.NewID("jb")
	now := time.Now().UnixMilli()
	_, err := r.st.Writer().ExecContext(ctx, sqlInsert, id, kind, string(body), max, toMS(o.NotBefore), createdBy, now, now)
	if err != nil {
		return "", err
	}
	r.signal()
	return id, nil
}

// Expedite makes a queued job that waits for a future time due now, so a job that is wanted at once
// (the waveform of the book being played) is claimed before the ones queued at leisure: the claim
// order is not_before, then created_at. It returns nil when no row changed (the job is running,
// finished, unknown, or already due).
func (r *Runner) Expedite(ctx context.Context, id string) error {
	_, err := r.st.Writer().ExecContext(ctx, sqlExpedite, time.Now().UnixMilli(), id)
	if err != nil {
		return err
	}
	r.signal()
	return nil
}

// Cancel marks a queued, held or running job cancelled. A running handler's
// context is cancelled with ErrCancelled and its result is never written.
func (r *Runner) Cancel(ctx context.Context, id string) error {
	return r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var state string
		err := tx.QueryRowContext(ctx, sqlStateOf, id).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		switch State(state) {
		case StateQueued, StateHeld, StateRunning:
		default:
			return ErrAlreadyFinished
		}
		if _, err := tx.ExecContext(ctx, sqlCancel, time.Now().UnixMilli(), id); err != nil {
			return err
		}
		// Cancelled before the commit: the writer is a single connection, so the
		// handler's keeper cannot see the fence fail (and report ErrLeaseLost)
		// before this cause is set.
		r.mu.Lock()
		a := r.active[id]
		r.mu.Unlock()
		if a != nil {
			a.cancel(ErrCancelled)
		}
		return nil
	})
}

// Resume moves a held job back to the queue.
func (r *Runner) Resume(ctx context.Context, id string) error {
	now := time.Now().UnixMilli()
	res, err := r.st.Writer().ExecContext(ctx, sqlResume, now, now, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var state string
		err := r.st.Writer().QueryRowContext(ctx, sqlStateOf, id).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		return ErrNotHeld
	}
	r.signal()
	return nil
}

// dispatch claims jobs while a kind has a free slot.
func (r *Runner) dispatch() {
	defer r.wg.Done()
	tick := time.NewTicker(r.o.PollEvery)
	defer tick.Stop()
	for {
		r.claimAll()
		select {
		case <-r.stopc:
			return
		case <-r.wake:
		case <-tick.C:
		}
	}
}

func (r *Runner) claimAll() {
	r.mu.Lock()
	names := make([]string, 0, len(r.kinds))
	for n := range r.kinds {
		names = append(names, n)
	}
	r.mu.Unlock()
	sort.Strings(names)
	for _, name := range names {
		for {
			r.mu.Lock()
			k := r.kinds[name]
			if r.stopping || r.running[name] >= k.Concurrency {
				r.mu.Unlock()
				break
			}
			r.running[name]++ // reserve the slot before claiming
			r.mu.Unlock()

			a, err := r.claim(r.dbCtx, k)
			if err != nil || a == nil {
				r.releaseSlot(name)
				if err != nil && !errors.Is(err, context.Canceled) {
					r.log.Error("claim job", "kind", name, "err", err)
				}
				break
			}
			r.wg.Add(1)
			go r.runJob(a)
		}
	}
}

func (r *Runner) releaseSlot(kind string) {
	r.mu.Lock()
	r.running[kind]--
	r.mu.Unlock()
}

// claim turns the oldest due queued job of kind k into a running one. It
// returns nil when there is none. The statement is atomic on the writer.
func (r *Runner) claim(ctx context.Context, k Kind) (*active, error) {
	t0 := time.Now()
	var (
		id, payload string
		attempts    int
		max         int
	)
	err := r.st.Writer().QueryRowContext(ctx, sqlClaim,
		t0.Add(r.o.Lease).UnixMilli(), t0.UnixMilli(), k.Name, t0.UnixMilli()).Scan(&id, &payload, &attempts, &max)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	hctx, hcancel := context.WithCancelCause(r.base)
	a := &active{
		id: id, kind: k, attempts: attempts, max: max, claimed: t0,
		task: &Task{ID: id, Kind: k.Name, Attempt: attempts, Payload: json.RawMessage(payload)},
		ctx:  hctx, cancel: hcancel,
		stop: make(chan struct{}), done: make(chan struct{}),
	}
	// Registered before the goroutine starts so Cancel and recovery always find it.
	r.mu.Lock()
	r.active[id] = a
	r.mu.Unlock()
	return a, nil
}

// runJob runs the handler, with a keeper goroutine that renews the lease and
// writes progress, then writes the outcome.
func (r *Runner) runJob(a *active) {
	defer r.wg.Done()
	hctx, hcancel := a.ctx, a.cancel

	go r.keep(a)
	result, err := r.call(hctx, a)
	close(a.stop)
	<-a.done

	var cause error
	if hctx.Err() != nil {
		cause = context.Cause(hctx)
	}
	hcancel(nil)
	r.finish(a, cause, result, err)

	r.mu.Lock()
	delete(r.active, a.id)
	r.running[a.kind.Name]--
	r.mu.Unlock()
	r.signal()
}

func (r *Runner) call(ctx context.Context, a *active) (result any, err error) {
	defer func() {
		if p := recover(); p != nil {
			r.log.Error("job handler panicked", "job", a.id, "kind", a.kind.Name, "panic", fmt.Sprint(p), "stack", string(debug.Stack()))
			result, err = nil, &Error{Code: codeInternal, Text: textInternal, Permanent: true, Err: fmt.Errorf("panic: %v", p)}
		}
	}()
	return a.kind.Handler(ctx, a.task)
}

// keep is the only writer of progress while a job runs. Every ProgressEvery it
// flushes new progress and, once RenewEvery has passed, renews the lease. A
// failed fence cancels the handler with ErrLeaseLost.
func (r *Runner) keep(a *active) {
	defer close(a.done)
	tick := time.NewTicker(r.o.ProgressEvery)
	defer tick.Stop()
	lastRenew, lastOK := a.claimed, a.claimed
	for {
		select {
		case <-a.stop:
			return
		case <-tick.C:
		}
		if p, ok := a.task.takeDirty(); ok {
			res, err := r.st.Writer().ExecContext(r.dbCtx, sqlProgress, string(p), time.Now().UnixMilli(), a.id, a.attempts)
			r.progress.Add(1)
			switch {
			case err != nil:
				r.log.Warn("write job progress", "job", a.id, "err", err)
				a.task.markDirty()
			case rows(res) == 0:
				a.cancel(ErrLeaseLost)
				return
			}
		}
		if time.Since(lastRenew) < r.o.RenewEvery {
			continue
		}
		t0 := time.Now()
		res, err := r.st.Writer().ExecContext(r.dbCtx, sqlRenew,
			t0.Add(r.o.Lease).UnixMilli(), t0.UnixMilli(), a.id, a.attempts, t0.UnixMilli())
		switch {
		case err != nil:
			r.log.Warn("renew job lease", "job", a.id, "err", err)
			if time.Since(lastOK) >= r.o.Lease {
				a.cancel(ErrLeaseLost)
				return
			}
		case rows(res) == 0:
			a.cancel(ErrLeaseLost)
			return
		default:
			lastRenew, lastOK = t0, t0
		}
	}
}

func rows(res sql.Result) int64 {
	n, _ := res.RowsAffected()
	return n
}

// progressArg is the pending progress as a SQL argument (NULL when never set).
func progressArg(t *Task) any {
	if p := t.latest(); p != nil {
		return string(p)
	}
	return nil
}

// finish writes the outcome of one run, fenced. cause is the handler context's
// cancellation cause, if it was cancelled.
func (r *Runner) finish(a *active, cause error, result any, herr error) {
	if errors.Is(cause, ErrLeaseLost) || errors.Is(cause, ErrCancelled) {
		r.log.Info("job result discarded", "job", a.id, "kind", a.kind.Name, "cause", cause)
		return
	}
	ctx, cancel := context.WithTimeout(r.dbCtx, 10*time.Second)
	defer cancel()
	now := time.Now()
	nowMS := now.UnixMilli()
	prog := progressArg(a.task)

	var (
		res sql.Result
		err error
	)
	switch {
	case herr == nil:
		var body any
		if result != nil {
			b, merr := json.Marshal(result)
			if merr != nil {
				r.log.Error("encode job result", "job", a.id, "err", merr)
				res, err = r.st.Writer().ExecContext(ctx, sqlFail, codeInternal, textInternal, prog, nowMS, a.id, a.attempts)
				break
			}
			body = string(b)
		}
		res, err = r.st.Writer().ExecContext(ctx, sqlDone, body, prog, nowMS, a.id, a.attempts)

	case errors.Is(cause, ErrShutdown):
		if a.kind.HoldOnRecovery {
			res, err = r.st.Writer().ExecContext(ctx, sqlHold, codeInterrupted, textInterrupted, prog, nowMS, a.id, a.attempts)
		} else {
			res, err = r.st.Writer().ExecContext(ctx, sqlRequeueShutdown, nowMS, prog, nowMS, a.id, a.attempts)
		}

	default:
		code, text, permanent := classify(herr)
		var je *Error
		hold := errors.As(herr, &je) && je.Hold
		r.log.Warn("job failed", "job", a.id, "kind", a.kind.Name, "attempt", a.attempts, "hold", hold, "err", herr)
		if hold {
			// The handler asked for a person: held, and the attempt is given back like a shutdown requeue.
			res, err = r.st.Writer().ExecContext(ctx, sqlHoldHandler, code, text, prog, nowMS, a.id, a.attempts)
		} else if permanent || a.attempts >= a.max {
			res, err = r.st.Writer().ExecContext(ctx, sqlFail, code, text, prog, nowMS, a.id, a.attempts)
		} else {
			nb := now.Add(r.o.Backoff(a.attempts)).UnixMilli()
			res, err = r.st.Writer().ExecContext(ctx, sqlRetry, nb, code, text, prog, nowMS, a.id, a.attempts)
		}
	}
	switch {
	case err != nil:
		r.log.Error("write job outcome", "job", a.id, "err", err)
	case rows(res) == 0:
		r.log.Warn("job outcome not written: the job no longer holds its lease", "job", a.id, "kind", a.kind.Name)
	}
}

func classify(err error) (code, text string, permanent bool) {
	var je *Error
	if errors.As(err, &je) {
		code, text, permanent = je.Code, je.Text, je.Permanent
		if !codePattern.MatchString(code) {
			code = codeInternal
		}
		if text == "" {
			text = textInternal
		}
		return code, text, permanent
	}
	return codeInternal, textInternal, false
}

// sweep recovers expired leases every SweepEvery.
func (r *Runner) sweep() {
	defer r.wg.Done()
	tick := time.NewTicker(r.o.SweepEvery)
	defer tick.Stop()
	for {
		select {
		case <-r.stopc:
			return
		case <-tick.C:
		}
		if err := r.recoverExpired(r.dbCtx); err != nil {
			r.log.Error("recover expired jobs", "err", err)
		}
	}
}

type expired struct {
	id, kind      string
	attempts, max int
}

// recoverExpired handles running rows whose lease has run out. A row goes to
// held for a kind that is HoldOnRecovery or not registered, to failed when it
// used all its attempts, and back to the queue otherwise.
func (r *Runner) recoverExpired(ctx context.Context) error {
	now := time.Now().UnixMilli()
	var recovered []expired
	err := r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		recovered = recovered[:0]
		rs, err := tx.QueryContext(ctx, sqlExpired, now)
		if err != nil {
			return err
		}
		var list []expired
		for rs.Next() {
			var e expired
			if err := rs.Scan(&e.id, &e.kind, &e.attempts, &e.max); err != nil {
				rs.Close()
				return err
			}
			list = append(list, e)
		}
		if err := rs.Close(); err != nil {
			return err
		}
		if err := rs.Err(); err != nil {
			return err
		}
		for _, e := range list {
			r.mu.Lock()
			k, known := r.kinds[e.kind]
			r.mu.Unlock()
			var res sql.Result
			switch {
			case !known || k.HoldOnRecovery:
				res, err = tx.ExecContext(ctx, sqlHold, codeInterrupted, textInterrupted, nil, now, e.id, e.attempts)
			case e.attempts >= e.max:
				res, err = tx.ExecContext(ctx, sqlFail, codeInterrupted, textExhausted, nil, now, e.id, e.attempts)
			default:
				res, err = tx.ExecContext(ctx, sqlRecoverQueue, now, now, e.id, e.attempts)
			}
			if err != nil {
				return err
			}
			if rows(res) == 1 {
				recovered = append(recovered, e)
			}
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, e := range recovered {
		r.log.Info("recovered job with an expired lease", "job", e.id, "kind", e.kind, "attempts", e.attempts)
		r.mu.Lock()
		a := r.active[e.id]
		r.mu.Unlock()
		if a != nil && a.attempts == e.attempts {
			a.cancel(ErrLeaseLost)
		}
	}
	if len(recovered) > 0 {
		r.signal()
	}
	return nil
}
