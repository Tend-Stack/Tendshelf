package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand/v2"
	"slices"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

// KindOnlineBulk is the job that looks up the books of one library, one after another.
const KindOnlineBulk = "metadata.online_bulk"

// Modes of a bulk lookup: every book not looked at lately, or only books added since the owner
// turned on "look up details online for new books".
const (
	BulkAll = library.LookupModeAll
	BulkNew = library.LookupModeNew
)

const (
	skipWindow = 30 * 24 * time.Hour // a book looked at within this long is not asked about again
	autoDelay  = time.Minute         // a new book waits this long, so a scan can finish first
)

// Why a bulk job is paused.
const (
	PauseBusy     = "busy"
	PauseDailyCap = "daily_cap"
)

// Scopes of a started bulk lookup: books missing details (the default), books never looked up, every book
// not looked at lately.
const (
	ScopeMissing  = library.LookupModeMissing
	ScopeUnlooked = library.LookupModeUnlooked
	ScopeAll      = library.LookupModeAll
)

// BulkPayload is the payload of a metadata.online_bulk job. Scope narrows mode "all"; empty means all.
type BulkPayload struct {
	LibraryID string   `json:"library_id"`
	Mode      string   `json:"mode"`
	Scope     string   `json:"scope,omitempty"`
	Providers []string `json:"providers"`
}

// ValidScope reports whether s names a scope of a started lookup; empty is "all".
func ValidScope(s string) bool {
	return s == "" || s == ScopeMissing || s == ScopeUnlooked || s == ScopeAll
}

// lookupMode is the library mode the job asks NextLookupBook with.
func (p BulkPayload) lookupMode() string {
	if p.Mode == BulkAll && (p.Scope == ScopeMissing || p.Scope == ScopeUnlooked) {
		return p.Scope
	}
	return p.Mode
}

// BulkProgress is what a bulk job reports while it runs and returns when it ends. The counts come
// from the book_lookups rows the job wrote, so they stay true across a restart.
type BulkProgress struct {
	Done        int         `json:"done"`
	Total       int         `json:"total"`
	Matched     int         `json:"matched"`
	NeedsLook   int         `json:"needs_look"`
	NotFound    int         `json:"not_found"`
	Skipped     int         `json:"skipped"`
	Failed      int         `json:"failed"`
	Current     string      `json:"current,omitempty"`
	PausedUntil int64       `json:"paused_until,omitempty"` // Unix ms; 0 = not paused
	PauseReason string      `json:"pause_reason,omitempty"` // PauseBusy or PauseDailyCap
	Pace        online.Pace `json:"pace"`
}

// jobStore is the part of jobs.Runner the bulk lookups read and cancel with.
type jobStore interface {
	Get(ctx context.Context, id string) (jobs.Job, error)
	ActiveFor(ctx context.Context, kind, field, value string) (jobs.Job, bool, error)
	LatestFor(ctx context.Context, kind, field, value string) (jobs.Job, bool, error)
	Cancel(ctx context.Context, id string) error
}

func (s *Service) store() (jobStore, bool) {
	j, ok := s.jobs.(jobStore)
	return j, ok
}

// SetCoverFill sets how a matched book without a cover gets the match's picture (see
// mediajobs.Covers.FillOnline). Without it, bulk lookups fill no covers.
func (s *Service) SetCoverFill(f func(ctx context.Context, bookID, url string) error) {
	s.coverFill = f
}

func bulkProviders(more bool) []string {
	if more {
		return slices.Clone(online.Providers)
	}
	return []string{"audnexus"}
}

func (s *Service) onlineOn(ctx context.Context) (bool, error) {
	if s.online == nil {
		return false, nil
	}
	return OnlineEnabled(ctx, s.repo.Store().Reader())
}

func (s *Service) auditLibrary(ctx context.Context, action, libraryID, userID string, detail any) error {
	return s.repo.Update(ctx, func(tx *library.Tx) error {
		return tx.Audit(db.AuditEntry{At: s.now().UnixMilli(), UserID: userID, Action: action, Target: "library:" + libraryID, Detail: detail})
	})
}

// StartBulk queues a lookup of every book of the library not looked at in the last 30 days. Only
// Audnexus is asked unless more is true. When such a job is already waiting or running it returns
// that job's id. It returns ErrOnlineOff when lookups are off and ErrNotFound for an unknown
// library. The caller checks who may manage the library.
func (s *Service) StartBulk(ctx context.Context, libraryID, scope string, more bool, userID string) (string, error) {
	if !ValidScope(scope) {
		return "", library.ErrInvalid
	}
	if scope == "" {
		scope = ScopeAll
	}
	if on, err := s.onlineOn(ctx); err != nil {
		return "", err
	} else if !on {
		return "", ErrOnlineOff
	}
	if _, err := s.repo.Library(ctx, libraryID); err != nil {
		return "", err
	}
	if st, ok := s.store(); ok {
		j, found, err := st.ActiveFor(ctx, KindOnlineBulk, "library_id", libraryID)
		if err != nil {
			return "", err
		}
		var p BulkPayload
		if found && json.Unmarshal(j.Payload, &p) == nil && p.Mode == BulkAll {
			return j.ID, nil
		}
	}
	id, err := s.jobs.Enqueue(ctx, KindOnlineBulk, BulkPayload{LibraryID: libraryID, Mode: BulkAll, Scope: scope, Providers: bulkProviders(more)},
		jobs.EnqueueOptions{CreatedBy: userID})
	if err != nil {
		return "", err
	}
	if err := s.auditLibrary(ctx, "library.lookups_start", libraryID, userID, map[string]any{"job": id, "scope": scope, "more": more}); err != nil {
		s.log.Warn("audit lookups start", "err", err)
	}
	return id, nil
}

// ScopeCounts is how many books each scope of a started lookup would ask about right now.
type ScopeCounts struct{ Missing, Unlooked, All int }

// CountScopes counts the books of each scope for the library, with the rules the bulk job itself uses.
func (s *Service) CountScopes(ctx context.Context, libraryID string) (ScopeCounts, error) {
	skip := s.now().Add(-skipWindow).UnixMilli()
	var c ScopeCounts
	for scope, dst := range map[string]*int{ScopeMissing: &c.Missing, ScopeUnlooked: &c.Unlooked, ScopeAll: &c.All} {
		n, err := s.repo.CountLookupBooks(ctx, libraryID, scope, skip)
		if err != nil {
			return ScopeCounts{}, err
		}
		*dst = n
	}
	return c, nil
}

// StopBulk cancels every waiting or running bulk lookup of the library. Books already looked up
// keep their results.
func (s *Service) StopBulk(ctx context.Context, libraryID, userID string) error {
	st, ok := s.store()
	if !ok {
		return nil
	}
	for range 10 {
		j, found, err := st.ActiveFor(ctx, KindOnlineBulk, "library_id", libraryID)
		if err != nil {
			return err
		}
		if !found {
			break
		}
		if err := st.Cancel(ctx, j.ID); err != nil && !errors.Is(err, jobs.ErrAlreadyFinished) && !errors.Is(err, jobs.ErrNotFound) {
			return err
		}
	}
	return s.auditLibrary(ctx, "library.lookups_stop", libraryID, userID, nil)
}

// SetAutoLookup turns "look up details online for new books" on or off for a library. Turning it
// on when it is on keeps the moment it was first turned on. ErrNotFound for an unknown library.
func (s *Service) SetAutoLookup(ctx context.Context, libraryID string, on bool, userID string) error {
	l, err := s.repo.Library(ctx, libraryID)
	if err != nil {
		return err
	}
	at := int64(0)
	if on {
		at = l.AutoLookupAt
		if at == 0 {
			at = s.now().UnixMilli()
		}
	}
	return s.repo.Update(ctx, func(tx *library.Tx) error {
		if err := tx.SetAutoLookup(libraryID, at); err != nil {
			return err
		}
		return tx.Audit(db.AuditEntry{At: s.now().UnixMilli(), UserID: userID, Action: "library.auto_lookup",
			Target: "library:" + libraryID, Detail: map[string]bool{"on": on}})
	})
}

// BulkStatus returns the library's newest bulk job and its progress (the result once it is done).
// The bool is false when the library never had one.
func (s *Service) BulkStatus(ctx context.Context, libraryID string) (jobs.Job, BulkProgress, bool, error) {
	st, ok := s.store()
	if !ok {
		return jobs.Job{}, BulkProgress{}, false, nil
	}
	j, found, err := st.LatestFor(ctx, KindOnlineBulk, "library_id", libraryID)
	if err != nil || !found {
		return jobs.Job{}, BulkProgress{}, false, err
	}
	p, err := ParseBulkProgress(j)
	return j, p, true, err
}

// ParseBulkProgress reads the progress of a bulk job (its result once it is done).
func ParseBulkProgress(j jobs.Job) (BulkProgress, error) {
	var p BulkProgress
	raw := j.Progress
	if j.State == jobs.StateDone && len(j.Result) > 0 {
		raw = j.Result
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			return BulkProgress{}, err
		}
	}
	return p, nil
}

func defaultSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	tm := time.NewTimer(d)
	defer tm.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-tm.C:
		return nil
	}
}

func defaultJitter() float64 { return rand.Float64() }

// bulkRun is the state of one run of the bulk handler.
type bulkRun struct {
	s        *Service
	t        *jobs.Task
	p        BulkPayload
	runStart int64
	prog     BulkProgress
	handled  map[string]bool // books this run recorded, as a guard against a clock that went back
}

func bulkBadPayload(err error) error {
	return &jobs.Error{Code: "bad_payload", Text: "This job was queued with settings Tendshelf cannot read.", Permanent: true, Err: err}
}

func (s *Service) handleBulk(ctx context.Context, t *jobs.Task) (any, error) {
	var p BulkPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.LibraryID == "" || (p.Mode != BulkAll && p.Mode != BulkNew) || !ValidScope(p.Scope) {
		return nil, bulkBadPayload(err)
	}
	if len(p.Providers) == 0 {
		p.Providers = []string{"audnexus"}
	}
	if _, err := s.repo.Library(ctx, p.LibraryID); errors.Is(err, library.ErrNotFound) {
		return BulkProgress{}, nil
	} else if err != nil {
		return nil, err
	}
	r := &bulkRun{s: s, t: t, p: p, runStart: s.now().UnixMilli(), handled: map[string]bool{}}
	if st, ok := s.store(); ok {
		if j, err := st.Get(ctx, t.ID); err == nil {
			r.runStart = j.CreatedAt.UnixMilli()
			if len(j.Progress) > 0 {
				var prior BulkProgress
				if json.Unmarshal(j.Progress, &prior) == nil {
					r.prog.Pace = prior.Pace
					r.prog.PausedUntil, r.prog.PauseReason = prior.PausedUntil, prior.PauseReason
				}
			}
		}
	}
	return r.run(ctx)
}

// publish refreshes the counts from the database and reports the progress.
func (r *bulkRun) publish(ctx context.Context) error {
	counts, err := r.s.repo.LookupCounts(ctx, r.t.ID)
	if err != nil {
		return err
	}
	pr := &r.prog
	pr.Matched, pr.NeedsLook, pr.NotFound = counts[library.LookupMatched], counts[library.LookupNeedsLook], counts[library.LookupNoMatch]
	pr.Skipped, pr.Failed = counts[library.LookupKept]+counts[library.LookupNoQuery], counts[library.LookupFailed]
	pr.Done = pr.Matched + pr.NeedsLook + pr.NotFound + pr.Skipped + pr.Failed
	_, remaining, err := r.s.repo.NextLookupBook(ctx, r.p.LibraryID, r.p.lookupMode(), r.runStart, r.skipBefore())
	if err != nil {
		return err
	}
	pr.Total = pr.Done + remaining
	r.t.Progress(*pr)
	return nil
}

func (r *bulkRun) skipBefore() int64 { return r.s.now().Add(-skipWindow).UnixMilli() }

// wait publishes the progress, then sleeps. until and reason describe a pause to show; both
// zero means a short pace or back-off sleep.
func (r *bulkRun) wait(ctx context.Context, d time.Duration, reason string) error {
	if reason != "" {
		r.prog.PausedUntil, r.prog.PauseReason = r.s.now().Add(d).UnixMilli(), reason
	}
	if err := r.publish(ctx); err != nil {
		return err
	}
	err := r.s.sleep(ctx, d)
	if err == nil {
		r.prog.PausedUntil, r.prog.PauseReason = 0, ""
	}
	return err
}

func (r *bulkRun) record(ctx context.Context, bookID, outcome, provider string) error {
	r.handled[bookID] = true
	return r.s.repo.UpdateGuarded(ctx, r.t.Fence, func(tx *library.Tx) error {
		return tx.PutBookLookup(bookID, outcome, provider, r.t.ID, r.s.now().UnixMilli())
	})
}

func (r *bulkRun) run(ctx context.Context) (any, error) {
	s := r.s
	if err := PruneOnlineUsage(ctx, s.repo.Store(), usageDay(s.now())-usagePruneDays); err != nil {
		return nil, err
	}
	// A restart in the middle of a pause carries on with the rest of it.
	if until := time.UnixMilli(r.prog.PausedUntil); r.prog.PausedUntil > 0 && until.After(s.now()) {
		if err := r.wait(ctx, until.Sub(s.now()), r.prog.PauseReason); err != nil {
			return nil, err
		}
	}
	r.prog.PausedUntil, r.prog.PauseReason = 0, ""
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if on, err := s.onlineOn(ctx); err != nil {
			return nil, err
		} else if !on {
			return nil, offError()
		}
		capped, err := r.capReached(ctx)
		if err != nil {
			return nil, err
		}
		if capped {
			r.prog.Pace = online.Pace{}
			d := online.NextDay(s.now(), s.jitter()).Sub(s.now())
			if err := r.wait(ctx, d, PauseDailyCap); err != nil {
				return nil, err
			}
			continue
		}
		bookID, _, err := s.repo.NextLookupBook(ctx, r.p.LibraryID, r.p.lookupMode(), r.runStart, r.skipBefore())
		if err != nil {
			return nil, err
		}
		if bookID == "" || r.handled[bookID] {
			break
		}
		outcome, err := r.book(ctx, bookID)
		if err != nil {
			return nil, err
		}
		w := r.prog.Pace.Next(outcome, s.jitter())
		if w.Stop {
			r.prog.Current = ""
			_ = r.publish(ctx)
			return nil, &jobs.Error{Code: "provider_refusing", Permanent: true,
				Text: "The book sites kept refusing requests, so Tendshelf stopped asking. Try again later."}
		}
		if w.For > 0 {
			// No pause after the last book: there is no next request to space out.
			if _, remaining, err := s.repo.NextLookupBook(ctx, r.p.LibraryID, r.p.lookupMode(), r.runStart, r.skipBefore()); err != nil {
				return nil, err
			} else if remaining == 0 {
				break
			}
			reason := ""
			if w.Reason == "pause" {
				reason = PauseBusy
			}
			if err := r.wait(ctx, w.For, reason); err != nil {
				return nil, err
			}
		}
	}
	r.prog.Current = ""
	if err := r.publish(ctx); err != nil {
		return nil, err
	}
	return r.prog, nil
}

// capReached says whether any provider of the job has used up its requests for today.
func (r *bulkRun) capReached(ctx context.Context) (bool, error) {
	day := usageDay(r.s.now())
	for _, p := range r.p.Providers {
		n, err := OnlineUsage(ctx, r.s.repo.Store().Reader(), day, p)
		if err != nil {
			return false, err
		}
		if n >= online.DailyCap {
			return true, nil
		}
	}
	return false, nil
}

// book looks one book up and records the result. The outcome says how the sites behaved, for pacing.
func (r *bulkRun) book(ctx context.Context, bookID string) (online.Outcome, error) {
	s := r.s
	q, err := s.LookupQuery(ctx, bookID)
	if errors.Is(err, library.ErrNotFound) {
		r.handled[bookID] = true // the book went away meanwhile
		return online.OutcomeCached, nil
	}
	if err != nil {
		return 0, err
	}
	if b, err := s.repo.Book(ctx, bookID); err == nil {
		r.prog.Current = b.Title
	}
	if q.Title == "" && cleanASIN(q.ASIN) == "" && cleanISBN(q.ISBN) == "" {
		return online.OutcomeCached, r.record(ctx, bookID, library.LookupNoQuery, "")
	}
	rows, err := s.repo.MetadataValues(ctx, bookID)
	if err != nil {
		return 0, err
	}
	for _, row := range rows {
		if row.Source == SourceOnline || row.Source == SourceUser {
			return online.OutcomeCached, r.record(ctx, bookID, library.LookupKept, "")
		}
	}

	res, err := s.online.LookupWith(ctx, q, r.p.Providers, online.LookupOptions{MaxAudnexusBooks: online.BulkAudnexusBooks})
	sent := res.SentBy()
	if uerr := AddOnlineUsage(context.WithoutCancel(ctx), s.repo.Store(), usageDay(s.now()), sent); uerr != nil {
		s.log.Warn("count online requests", "err", uerr)
	}
	if errors.Is(err, online.ErrEmptyQuery) {
		return online.OutcomeCached, r.record(ctx, bookID, library.LookupNoQuery, "")
	}
	if err != nil {
		return 0, err
	}
	troubled, busy := 0, false
	for _, code := range res.Errors {
		if online.Troubled(code) {
			troubled++
		}
		busy = busy || code == "busy"
	}
	if busy {
		return online.OutcomeTrouble, nil // not recorded: the same book is asked again after the wait
	}
	cands := toCandidates(q, res)
	if len(cands) == 0 {
		if troubled > 0 && troubled == len(res.Searched) {
			return online.OutcomeTrouble, r.record(ctx, bookID, library.LookupFailed, "")
		}
		return pacedOutcome(sent), r.record(ctx, bookID, library.LookupNoMatch, "")
	}
	out := pacedOutcome(sent)
	pick, ok := pickAuto(q, cands)
	if !ok {
		return out, r.record(ctx, bookID, library.LookupNeedsLook, cands[0].Provider)
	}
	applied, err := s.applyAuto(ctx, r.t.Fence, bookID, r.t.ID, pick)
	if err != nil {
		return 0, err
	}
	r.handled[bookID] = true
	if applied && s.coverFill != nil && pick.CoverURL != "" {
		if b, err := s.repo.Book(ctx, bookID); err == nil && b.CoverID == "" {
			if err := s.coverFill(ctx, bookID, pick.CoverURL); err != nil {
				s.log.Warn("fill cover from online match", "book", bookID, "err", err)
			}
		}
	}
	return out, nil
}

func pacedOutcome(sent map[string]int) online.Outcome {
	for _, n := range sent {
		if n > 0 {
			return online.OutcomeSent
		}
	}
	return online.OutcomeCached
}

// maybeAuto queues a lookup of the library's new books after a book's details were read, when the
// library asks for it and lookups are on. It leaves a waiting job alone and never returns an error.
func (s *Service) maybeAuto(ctx context.Context, bookID string) {
	libraryID, due, err := s.repo.AutoLookupDue(ctx, bookID)
	if err != nil || !due {
		return
	}
	if on, err := s.onlineOn(ctx); err != nil || !on {
		return
	}
	st, ok := s.store()
	if !ok {
		return
	}
	j, found, err := st.ActiveFor(ctx, KindOnlineBulk, "library_id", libraryID)
	if err != nil {
		s.log.Warn("check bulk lookups", "err", err)
		return
	}
	if found && j.State == jobs.StateQueued {
		return
	}
	_, err = s.jobs.Enqueue(ctx, KindOnlineBulk, BulkPayload{LibraryID: libraryID, Mode: BulkNew, Providers: bulkProviders(false)},
		jobs.EnqueueOptions{NotBefore: s.now().Add(autoDelay)})
	if err != nil {
		s.log.Warn("queue lookup of new books", "library", libraryID, "err", err)
	}
}
