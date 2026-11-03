package metadata_test

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

// bulkEnv is a lookup environment whose bulk job runs on a fake clock with a recording fake sleep.
// The online client sleeps not at all; the fake network answers from canned files.
type bulkEnv struct {
	*lookupEnv
	mu      sync.Mutex
	clock   time.Time
	sleeps  []time.Duration
	onSleep func(ctx context.Context, n int, d time.Duration) error
	nbooks  int
}

func newBulkEnv(t *testing.T) *bulkEnv {
	t.Helper()
	le := newLookupEnvWith(t, func(o *online.Options) {
		o.Sleep = func(ctx context.Context, _ time.Duration) error { return ctx.Err() }
	})
	// The clock runs an hour ahead of the real one, so every row it stamps is newer than the job.
	e := &bulkEnv{lookupEnv: le, clock: time.Now().Add(time.Hour)}
	le.svc.SetBulkClock(e.now, e.sleep, func() float64 { return 0.5 })
	return e
}

func (e *bulkEnv) now() time.Time {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.clock
}

func (e *bulkEnv) sleep(ctx context.Context, d time.Duration) error {
	e.mu.Lock()
	e.sleeps = append(e.sleeps, d)
	n, hook := len(e.sleeps), e.onSleep
	e.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, n, d); err != nil {
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	e.mu.Lock()
	e.clock = e.clock.Add(d)
	e.mu.Unlock()
	return nil
}

func (e *bulkEnv) setHook(f func(ctx context.Context, n int, d time.Duration) error) {
	e.mu.Lock()
	e.onSleep = f
	e.mu.Unlock()
}

func (e *bulkEnv) slept() []time.Duration {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.sleeps)
}

func (e *bulkEnv) failWith(status int) {
	e.net.mu.Lock()
	e.net.fail = status
	e.net.mu.Unlock()
}

// override answers requests that start with prefix with body, ahead of the standard routes.
func (e *bulkEnv) override(prefix string, body []byte) {
	e.net.mu.Lock()
	e.net.routes = append([]lookupRoute{{prefix, body}}, e.net.routes...)
	e.net.mu.Unlock()
}

func (e *bulkEnv) urlsWith(sub string) int {
	e.net.mu.Lock()
	defer e.net.mu.Unlock()
	n := 0
	for _, u := range e.net.urls {
		if strings.Contains(u, sub) {
			n++
		}
	}
	return n
}

type bookSpec struct {
	Title string
	Tags  map[string]string
}

// addBooks scans one-file books by Ann Author with the given titles (the tag decides the title) and
// reads their details, as the metadata.local job does. It returns their ids in order.
func (e *bulkEnv) addBooks(specs ...bookSpec) []string {
	e.t.Helper()
	var rels []string
	for _, s := range specs {
		e.nbooks++
		tags := map[string]string{"album": s.Title, "artist": "Ann Author"}
		for k, v := range s.Tags {
			tags[k] = v
		}
		rel := fmt.Sprintf("Ann Author/%s %d", s.Title, e.nbooks)
		e.write(rel+"/p.m4b", mediatest.FakeFile{Tags: tags})
		rels = append(rels, rel)
	}
	e.scanAndProbe()
	var ids []string
	for _, rel := range rels {
		b := e.book(rel)
		e.local(b.ID)
		ids = append(ids, b.ID)
	}
	return ids
}

func titles(ts ...string) []bookSpec {
	var out []bookSpec
	for _, t := range ts {
		out = append(out, bookSpec{Title: t})
	}
	return out
}

func (e *bulkEnv) enqueue(mode string, providers ...string) string {
	e.t.Helper()
	if len(providers) == 0 {
		providers = []string{"audnexus"}
	}
	id, err := e.runner.Enqueue(bg, metadata.KindOnlineBulk, metadata.BulkPayload{LibraryID: e.lib.ID, Mode: mode, Providers: providers}, jobs.EnqueueOptions{})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

// finished waits until the job is done, failed or cancelled.
func (e *bulkEnv) finished(id string) jobs.Job {
	e.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		j, err := e.runner.Get(bg, id)
		if err != nil {
			e.t.Fatal(err)
		}
		switch j.State {
		case jobs.StateDone, jobs.StateFailed, jobs.StateCancelled:
			return j
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatalf("bulk job %s did not finish", id)
	return jobs.Job{}
}

func (e *bulkEnv) result(j jobs.Job) metadata.BulkProgress {
	e.t.Helper()
	var p metadata.BulkProgress
	raw := j.Result
	if len(raw) == 0 {
		raw = j.Progress
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &p); err != nil {
			e.t.Fatal(err)
		}
	}
	return p
}

func (e *bulkEnv) outcome(bookID string) string {
	e.t.Helper()
	var o string
	err := e.st.Reader().QueryRow(`SELECT outcome FROM book_lookups WHERE book_id = ?`, bookID).Scan(&o)
	if err != nil {
		return ""
	}
	return o
}

func (e *bulkEnv) putLookup(bookID, outcome string, at time.Time) {
	e.t.Helper()
	if err := e.repo.Update(bg, func(tx *library.Tx) error {
		return tx.PutBookLookup(bookID, outcome, "audnexus", "old", at.UnixMilli())
	}); err != nil {
		e.t.Fatal(err)
	}
}

// putValue stores one metadata value for a book from the given source.
func (e *bulkEnv) putValue(bookID, field, source, jsonValue string) {
	e.t.Helper()
	if err := e.repo.Update(bg, func(tx *library.Tx) error {
		return tx.PutMetadataValue(bookID, library.MetadataValue{Field: field, Source: source, Value: json.RawMessage(jsonValue), ObservedAt: 1})
	}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *bulkEnv) values(bookID string) []library.MetadataValue {
	e.t.Helper()
	rows, err := e.repo.MetadataValues(bg, bookID)
	if err != nil {
		e.t.Fatal(err)
	}
	return rows
}

func (e *bulkEnv) hasValue(bookID, field, source string) bool {
	for _, r := range e.values(bookID) {
		if r.Field == field && r.Source == source {
			return true
		}
	}
	return false
}

// restart stops the job runner (a shutdown) and starts a new runner and service on the same
// database, as a restart of the server does. The interrupted job is queued again.
func (e *bulkEnv) restart() {
	e.t.Helper()
	ctx, cancel := context.WithTimeout(bg, 10*time.Second)
	defer cancel()
	if err := e.runner.Stop(ctx); err != nil {
		e.t.Fatal(err)
	}
	runner, err := jobs.New(e.st, jobs.Options{
		Lease: 5 * time.Second, RenewEvery: time.Second, ProgressEvery: 50 * time.Millisecond,
		SweepEvery: time.Second, PollEvery: 10 * time.Millisecond,
		Backoff: func(int) time.Duration { return 10 * time.Millisecond }, Logger: quiet(),
	})
	if err != nil {
		e.t.Fatal(err)
	}
	svc := metadata.New(e.repo, runner, quiet())
	svc.SetOnline(online.New(online.Options{Cache: metadata.NewOnlineCache(e.st), CoverDir: e.t.TempDir(), Transport: e.net,
		Sleep: func(ctx context.Context, _ time.Duration) error { return ctx.Err() }}), mediatest.FakeTools(e.t), e.dataDir)
	svc.SetBulkClock(e.now, e.sleep, func() float64 { return 0.5 })
	if err := svc.Register(runner); err != nil {
		e.t.Fatal(err)
	}
	if err := runner.Start(bg); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() {
		c, cancel := context.WithTimeout(bg, 5*time.Second)
		defer cancel()
		_ = runner.Stop(c)
	})
	e.runner, e.svc = runner, svc
}

// A confident book: tags say what the fixture's top match says.
func matchSpec() bookSpec { return bookSpec{Title: "The Test Match"} }

func TestBulkExactApplies(t *testing.T) {
	e := newBulkEnv(t)
	// The title matches nothing, but the book's own ASIN is the fixture's: an exact match.
	ids := e.addBooks(bookSpec{Title: "Something Else Entirely", Tags: map[string]string{"asin": "B0TESTBOOK"}})
	j := e.finished(e.enqueue(metadata.BulkAll))
	if j.State != jobs.StateDone {
		t.Fatalf("job: %s %s", j.State, j.ErrorCode)
	}
	if got := e.outcome(ids[0]); got != library.LookupMatched {
		t.Errorf("outcome = %q", got)
	}
	if !e.hasValue(ids[0], metadata.FieldPublisher, metadata.SourceOnline) {
		t.Error("the publisher was not filled")
	}
	if p := e.result(j); p.Matched != 1 || p.Done != 1 || p.Total != 1 {
		t.Errorf("progress: %+v", p)
	}
}

func TestBulkAmbiguousNeedsLook(t *testing.T) {
	e := newBulkEnv(t)
	// Two different books that both pass for the wanted one: a person must choose.
	e.override("https://api.audnex.us/books/B0OTHER001", []byte(`{"asin":"B0OTHER001","title":"The Test Match","authors":[{"name":"Ann Author"}],"narrators":[],"releaseDate":"2020-01-01"}`))
	ids := e.addBooks(matchSpec())
	j := e.finished(e.enqueue(metadata.BulkAll))
	if j.State != jobs.StateDone {
		t.Fatalf("job: %s %s", j.State, j.ErrorCode)
	}
	if got := e.outcome(ids[0]); got != library.LookupNeedsLook {
		t.Errorf("outcome = %q", got)
	}
	if e.hasValue(ids[0], metadata.FieldPublisher, metadata.SourceOnline) {
		t.Error("nothing may be written for a doubtful match")
	}
	if p := e.result(j); p.NeedsLook != 1 || p.Matched != 0 {
		t.Errorf("progress: %+v", p)
	}
	rows, total, err := e.repo.NeedsLook(bg, e.lib.ID, 10)
	if err != nil || total != 1 || rows[0].BookID != ids[0] {
		t.Errorf("needs a look: %+v %d %v", rows, total, err)
	}
}

func TestBulkConfidentNotAmbiguousWithOneEdition(t *testing.T) {
	e := newBulkEnv(t)
	ids := e.addBooks(matchSpec())
	j := e.finished(e.enqueue(metadata.BulkAll))
	if j.State != jobs.StateDone || e.outcome(ids[0]) != library.LookupMatched {
		t.Fatalf("job %s, outcome %q", j.State, e.outcome(ids[0]))
	}
}

func TestBulkNoMatch(t *testing.T) {
	e := newBulkEnv(t)
	e.override("https://api.audible.com/1.0/catalog/products", []byte(`{"products":[]}`))
	ids := e.addBooks(titles("Nothing Like It")...)
	j := e.finished(e.enqueue(metadata.BulkAll))
	if j.State != jobs.StateDone || e.outcome(ids[0]) != library.LookupNoMatch {
		t.Fatalf("job %s, outcome %q", j.State, e.outcome(ids[0]))
	}
	if p := e.result(j); p.NotFound != 1 || p.Failed != 0 {
		t.Errorf("progress: %+v", p)
	}
}

func TestBulkSiteDownIsFailedAndRetried(t *testing.T) {
	e := newBulkEnv(t)
	e.failWith(500)
	ids := e.addBooks(titles("Down Book")...)
	j := e.finished(e.enqueue(metadata.BulkAll))
	if e.outcome(ids[0]) != library.LookupFailed {
		t.Fatalf("outcome = %q (job %s %s)", e.outcome(ids[0]), j.State, j.ErrorCode)
	}
	if p := e.result(j); p.Failed != 1 {
		t.Errorf("progress: %+v", p)
	}
	e.failWith(0)
	// The fake clock runs ahead of the real one; put the failure before this second run began.
	e.putLookup(ids[0], library.LookupFailed, e.now().Add(-2*time.Hour))
	e.finished(e.enqueue(metadata.BulkAll))
	if got := e.outcome(ids[0]); got != library.LookupNeedsLook {
		t.Errorf("after the site came back: %q", got)
	}
}

func TestBulkOnlineRowsOnly(t *testing.T) {
	e := newBulkEnv(t)
	ids := e.addBooks(bookSpec{Title: "The Test Match", Tags: map[string]string{"narrator": "Tag Narrator"}})
	j := e.finished(e.enqueue(metadata.BulkAll))
	if j.State != jobs.StateDone || e.outcome(ids[0]) != library.LookupMatched {
		t.Fatalf("job %s, outcome %q", j.State, e.outcome(ids[0]))
	}
	rows := e.values(ids[0])
	for _, r := range rows {
		if r.Source == metadata.SourceUser {
			t.Errorf("a user row was written: %s", r.Field)
		}
	}
	d, from := metadata.Resolve(rows)
	if !slices.Equal(d.Narrators, []string{"Tag Narrator"}) || from[metadata.FieldNarrators] != metadata.SourceTags {
		t.Errorf("the tag narrator must win: %v from %q", d.Narrators, from[metadata.FieldNarrators])
	}
	if !strings.Contains(d.Description, "dual-narrator") || from[metadata.FieldDescription] != metadata.SourceOnline {
		t.Errorf("the empty description should be filled: %q from %q", d.Description, from[metadata.FieldDescription])
	}
	if !e.hasValue(ids[0], metadata.FieldNarrators, metadata.SourceOnline) {
		t.Error("the online narrators are kept as an online row")
	}
	if n := e.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'book.details.auto' AND target = ?`, "book:"+ids[0]); n != 1 {
		t.Errorf("audit rows = %d", n)
	}
	var asin string
	if err := e.st.Reader().QueryRow(`SELECT asin FROM people WHERE name = 'Ann Author'`).Scan(&asin); err != nil || asin != "B0AUTHOR01" {
		t.Errorf("author asin = %q %v", asin, err)
	}
}

func TestBulkKeepsChosenBooks(t *testing.T) {
	e := newBulkEnv(t)
	ids := e.addBooks(titles("Chosen One", "Has Online Rows", "Dismissed One")...)
	e.putLookup(ids[0], library.LookupChosen, e.now().Add(-90*24*time.Hour))
	if err := e.svc.DismissLookup(bg, ids[2], "us_1"); err != nil {
		t.Fatal(err)
	}
	e.putValue(ids[1], metadata.FieldPublisher, metadata.SourceOnline, `"Earlier Press"`)
	before := e.net.requests()
	j := e.finished(e.enqueue(metadata.BulkAll))
	if j.State != jobs.StateDone {
		t.Fatalf("job: %s %s", j.State, j.ErrorCode)
	}
	if e.net.requests() != before {
		t.Errorf("%d requests went out", e.net.requests()-before)
	}
	if e.outcome(ids[0]) != library.LookupChosen || e.outcome(ids[2]) != library.LookupDismissed {
		t.Error("a person's choice was overwritten")
	}
	if e.outcome(ids[1]) != library.LookupKept {
		t.Errorf("book with online rows: %q", e.outcome(ids[1]))
	}
	if p := e.result(j); p.Skipped != 1 || p.Total != 1 {
		t.Errorf("progress: %+v", p)
	}
	if n := e.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'book.details.dismiss'`); n != 1 {
		t.Errorf("dismiss audit rows = %d", n)
	}
}

func TestBulkSkipWindow(t *testing.T) {
	e := newBulkEnv(t)
	ids := e.addBooks(titles("Skip Alpha", "Redo Beta", "Retry Gamma", "Fresh Delta")...)
	e.putLookup(ids[0], library.LookupNoMatch, e.now().Add(-10*24*time.Hour))
	e.putLookup(ids[1], library.LookupNoMatch, e.now().Add(-31*24*time.Hour))
	e.putLookup(ids[2], library.LookupFailed, e.now().Add(-2*time.Hour))
	j := e.finished(e.enqueue(metadata.BulkAll))
	if j.State != jobs.StateDone {
		t.Fatalf("job: %s %s", j.State, j.ErrorCode)
	}
	for i, want := range []int{0, 1, 1, 1} {
		word := []string{"Alpha", "Beta", "Gamma", "Delta"}[i]
		if got := e.urlsWith(word); got != want {
			t.Errorf("%s: %d catalogue requests, want %d", word, got, want)
		}
	}
	if e.outcome(ids[0]) != library.LookupNoMatch {
		t.Errorf("the skipped book's record changed to %q", e.outcome(ids[0]))
	}
	if p := e.result(j); p.Total != 3 || p.Done != 3 {
		t.Errorf("progress: %+v", p)
	}
}

func TestBulkResume(t *testing.T) {
	e := newBulkEnv(t)
	e.addBooks(titles("Resume One", "Resume Two", "Resume Three", "Resume Four")...)
	reached := make(chan struct{})
	e.setHook(func(ctx context.Context, n int, d time.Duration) error {
		if n == 2 {
			close(reached)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	})
	id := e.enqueue(metadata.BulkAll)
	select {
	case <-reached:
	case <-time.After(15 * time.Second):
		t.Fatal("the job never got to its second pause")
	}
	if n := e.count(`SELECT COUNT(*) FROM book_lookups WHERE job_id = ?`, id); n != 2 {
		t.Fatalf("recorded before the restart: %d", n)
	}
	e.setHook(nil)
	e.restart()
	j := e.finished(id)
	if j.State != jobs.StateDone {
		t.Fatalf("job: %s %s", j.State, j.ErrorCode)
	}
	if got := e.urlsWith("catalog/products"); got != 4 {
		t.Errorf("catalogue requests = %d; the first two books must not be asked again", got)
	}
	p := e.result(j)
	if p.Done != 4 || p.Total != 4 || p.NeedsLook != 4 {
		t.Errorf("progress: %+v", p)
	}
}

func TestBulkCancel(t *testing.T) {
	e := newBulkEnv(t)
	ids := e.addBooks(titles("Cancel A", "Cancel B", "Cancel C")...)
	e.setHook(func(ctx context.Context, n int, d time.Duration) error {
		if n == 2 {
			if err := e.svc.StopBulk(bg, e.lib.ID, "us_1"); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	})
	id := e.enqueue(metadata.BulkAll)
	j := e.finished(id)
	if j.State != jobs.StateCancelled {
		t.Fatalf("state = %s", j.State)
	}
	if n := e.count(`SELECT COUNT(*) FROM book_lookups WHERE job_id = ?`, id); n != 2 {
		t.Errorf("recorded %d books of %d, want the 2 looked up before the stop", n, len(ids))
	}
	// The job reads as cancelled a moment before StopBulk writes its audit row.
	for range 200 {
		if e.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'library.lookups_stop'`) > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if n := e.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'library.lookups_stop'`); n != 1 {
		t.Errorf("stop audit rows = %d", n)
	}
	if _, _, found, err := e.svc.BulkStatus(bg, e.lib.ID); err != nil || !found {
		t.Errorf("status: %v %v", found, err)
	}
}

func TestBulkPausePersists(t *testing.T) {
	e := newBulkEnv(t)
	e.failWith(429)
	ids := e.addBooks(titles("Busy Book")...)
	reached := make(chan struct{})
	e.setHook(func(ctx context.Context, n int, d time.Duration) error {
		if n == 4 { // 30s, 60s, 120s, then the breaker's pause
			close(reached)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	})
	id := e.enqueue(metadata.BulkAll)
	select {
	case <-reached:
	case <-time.After(15 * time.Second):
		t.Fatal("the breaker never paused the job")
	}
	if got := e.slept(); !slices.Equal(got, []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 30 * time.Minute}) {
		t.Fatalf("sleeps = %v", got)
	}
	e.setHook(nil)
	e.failWith(0)
	e.restart()
	j := e.finished(id)
	if j.State != jobs.StateDone {
		t.Fatalf("job: %s %s", j.State, j.ErrorCode)
	}
	if got := e.slept()[4]; got < 30*time.Minute-time.Millisecond || got > 30*time.Minute {
		t.Errorf("the restarted job slept %v; it should carry on with the rest of the pause", got)
	}
	if p := e.result(j); p.Pace.Trips != 1 || p.PausedUntil != 0 || p.NeedsLook != 1 {
		t.Errorf("progress: %+v", p)
	}
	if e.outcome(ids[0]) != library.LookupNeedsLook {
		t.Errorf("outcome = %q", e.outcome(ids[0]))
	}
}

func TestBulkBreaker(t *testing.T) {
	e := newBulkEnv(t)
	e.failWith(429)
	ids := e.addBooks(titles("Refused Book")...)
	j := e.finished(e.enqueue(metadata.BulkAll))
	if j.State != jobs.StateFailed || j.ErrorCode != "provider_refusing" {
		t.Fatalf("job: %s %s", j.State, j.ErrorCode)
	}
	var want []time.Duration
	for _, pause := range []time.Duration{30 * time.Minute, 60 * time.Minute, 120 * time.Minute, 240 * time.Minute, 240 * time.Minute} {
		want = append(want, 30*time.Second, time.Minute, 2*time.Minute, pause)
	}
	want = append(want, 30*time.Second, time.Minute, 2*time.Minute)
	if got := e.slept(); !slices.Equal(got, want) {
		t.Errorf("sleeps = %v\nwant     %v", got, want)
	}
	if e.outcome(ids[0]) != "" {
		t.Errorf("a refused book must not be recorded, got %q", e.outcome(ids[0]))
	}
	if n := e.count(`SELECT COUNT(*) FROM book_lookups WHERE outcome = 'failed'`); n != 0 {
		t.Errorf("failed rows = %d", n)
	}
}

func TestBulkDailyCap(t *testing.T) {
	e := newBulkEnv(t)
	ids := e.addBooks(titles("Capped Book")...)
	day := e.now().UnixMilli() / int64(24*time.Hour/time.Millisecond)
	if _, err := e.st.Writer().Exec(`INSERT INTO online_usage(day, provider, requests) VALUES (?, 'audnexus', ?)`, day, online.DailyCap); err != nil {
		t.Fatal(err)
	}
	start := e.now()
	var sawPause bool
	var id string
	e.setHook(func(ctx context.Context, n int, d time.Duration) error {
		if n != 1 {
			return nil
		}
		for range 200 { // the keeper writes the progress within a moment
			j, err := e.runner.Get(bg, id)
			if err == nil && strings.Contains(string(j.Progress), `"pause_reason":"daily_cap"`) {
				sawPause = true
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		return nil
	})
	before := e.net.requests()
	id = e.enqueue(metadata.BulkAll)
	j := e.finished(id)
	if j.State != jobs.StateDone {
		t.Fatalf("job: %s %s", j.State, j.ErrorCode)
	}
	sleeps := e.slept()
	if len(sleeps) == 0 || sleeps[0] != online.NextDay(start, 0.5).Sub(start) {
		t.Errorf("sleeps = %v, want the wait for the next UTC day first", sleeps)
	}
	if !sawPause {
		t.Error("the progress never said daily_cap")
	}
	if e.outcome(ids[0]) == "" || e.net.requests() == before {
		t.Error("the book should be looked up after the day turned")
	}
	if p := e.result(j); p.Pace.Trips != 0 || p.PauseReason != "" {
		t.Errorf("progress: %+v", p)
	}
}

func TestBulkPace(t *testing.T) {
	gap := online.BulkGap + online.BulkJitter/2 // jitter 0.5
	e := newBulkEnv(t)
	e.addBooks(titles("Pace One", "Pace Two", "Pace Three")...)
	j := e.finished(e.enqueue(metadata.BulkAll))
	if j.State != jobs.StateDone {
		t.Fatalf("job: %s %s", j.State, j.ErrorCode)
	}
	if got := e.slept(); !slices.Equal(got, []time.Duration{gap, gap}) {
		t.Errorf("sent books: sleeps = %v, want two of %v (none after the last book)", got, gap)
	}

	// Books with the same title are answered from the saved copy, so nothing waits after them.
	e2 := newBulkEnv(t)
	e2.addBooks(titles("Same Title", "Same Title", "Same Title")...)
	if j := e2.finished(e2.enqueue(metadata.BulkAll)); j.State != jobs.StateDone {
		t.Fatalf("job: %s %s", j.State, j.ErrorCode)
	}
	if got := e2.slept(); !slices.Equal(got, []time.Duration{gap}) {
		t.Errorf("cached books: sleeps = %v, want only the one after the first", got)
	}
}

func TestBulkOnlineOff(t *testing.T) {
	e := newBulkEnv(t)
	e.addBooks(matchSpec())
	if err := metadata.SetOnlineEnabled(bg, e.st, false, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.StartBulk(bg, e.lib.ID, false, "us_1"); err == nil {
		t.Error("StartBulk must refuse while lookups are off")
	}
	j := e.finished(e.enqueue(metadata.BulkAll))
	if j.State != jobs.StateFailed || j.ErrorCode != "online_off" {
		t.Errorf("job: %s %s", j.State, j.ErrorCode)
	}
	if e.net.requests() != 0 {
		t.Errorf("%d requests went out", e.net.requests())
	}
	if n := e.count(`SELECT COUNT(*) FROM book_lookups`); n != 0 {
		t.Errorf("rows = %d", n)
	}
	// A new book does not queue a lookup either.
	if err := e.svc.SetAutoLookup(bg, e.lib.ID, true, "us_1"); err != nil {
		t.Fatal(err)
	}
	if err := e.repo.SetAutoLookup(bg, e.lib.ID, 1); err != nil {
		t.Fatal(err)
	}
	ids := e.addBooks(titles("Late Book")...)
	e.finished(e.queueLocal(ids[0]))
	if n := e.count(`SELECT COUNT(*) FROM jobs WHERE kind = ? AND created_by IS NULL AND payload_json LIKE '%"new"%'`, metadata.KindOnlineBulk); n != 0 {
		t.Errorf("a new-books job was queued while lookups are off (%d)", n)
	}
}

func (e *bulkEnv) queueLocal(bookID string) string {
	e.t.Helper()
	id, err := e.runner.Enqueue(bg, metadata.Kind, metadata.Payload{BookID: bookID}, jobs.EnqueueOptions{})
	if err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *bulkEnv) bulkJobs() int {
	return e.count(`SELECT COUNT(*) FROM jobs WHERE kind = ?`, metadata.KindOnlineBulk)
}

func TestBulkNewMode(t *testing.T) {
	e := newBulkEnv(t)
	ids := e.addBooks(titles("Old Book", "New One", "New Two")...)
	// The switch was turned on at 1000 ms; the first book was added before it.
	if _, err := e.st.Writer().Exec(`UPDATE books SET added_at = 5 WHERE id = ?`, ids[0]); err != nil {
		t.Fatal(err)
	}
	if err := e.repo.SetAutoLookup(bg, e.lib.ID, 1000); err != nil {
		t.Fatal(err)
	}
	// Turning it on again keeps the first moment.
	if err := e.svc.SetAutoLookup(bg, e.lib.ID, true, "us_1"); err != nil {
		t.Fatal(err)
	}
	if l, _ := e.repo.Library(bg, e.lib.ID); l.AutoLookupAt != 1000 {
		t.Fatalf("auto_lookup_at = %d", l.AutoLookupAt)
	}

	// A book whose details were read queues one job; the next one finds it waiting.
	e.finished(e.queueLocal(ids[1]))
	if e.bulkJobs() != 1 {
		t.Fatalf("bulk jobs = %d, want 1", e.bulkJobs())
	}
	e.finished(e.queueLocal(ids[2]))
	if e.bulkJobs() != 1 {
		t.Fatalf("a waiting job must not be queued twice: %d", e.bulkJobs())
	}
	j, found, err := e.runner.ActiveFor(bg, metadata.KindOnlineBulk, "library_id", e.lib.ID)
	if err != nil || !found || j.State != jobs.StateQueued {
		t.Fatalf("queued job: %+v %v %v", j, found, err)
	}
	var p metadata.BulkPayload
	if err := json.Unmarshal(j.Payload, &p); err != nil || p.Mode != metadata.BulkNew || !slices.Equal(p.Providers, []string{"audnexus"}) || j.CreatedBy != "" {
		t.Errorf("payload %+v by %q (%v)", p, j.CreatedBy, err)
	}
	if !j.NotBefore.After(time.Now().Add(30 * time.Minute)) {
		t.Errorf("not before %v: a new book should wait a minute", j.NotBefore)
	}
	// While one runs, the next book queues another.
	if _, err := e.st.Writer().Exec(`UPDATE jobs SET state = 'running', lease_until = ?, attempts = 1 WHERE id = ?`, time.Now().Add(time.Hour).UnixMilli(), j.ID); err != nil {
		t.Fatal(err)
	}
	e.finished(e.queueLocal(ids[2]))
	if e.bulkJobs() != 2 {
		t.Errorf("bulk jobs = %d, want 2 behind a running one", e.bulkJobs())
	}

	// Running it looks only at the new books.
	if _, err := e.st.Writer().Exec(`DELETE FROM jobs WHERE kind = ?`, metadata.KindOnlineBulk); err != nil {
		t.Fatal(err)
	}
	done := e.finished(e.enqueue(metadata.BulkNew))
	if done.State != jobs.StateDone {
		t.Fatalf("job: %s %s", done.State, done.ErrorCode)
	}
	if e.outcome(ids[0]) != "" || e.outcome(ids[1]) == "" || e.outcome(ids[2]) == "" {
		t.Errorf("outcomes: %q %q %q", e.outcome(ids[0]), e.outcome(ids[1]), e.outcome(ids[2]))
	}
	// Off clears the moment.
	if err := e.svc.SetAutoLookup(bg, e.lib.ID, false, "us_1"); err != nil {
		t.Fatal(err)
	}
	if l, _ := e.repo.Library(bg, e.lib.ID); l.AutoLookupAt != 0 {
		t.Errorf("auto_lookup_at = %d after off", l.AutoLookupAt)
	}
}

func TestBulkStartStop(t *testing.T) {
	e := newBulkEnv(t)
	e.addBooks(titles("Start One", "Start Two")...)
	// Held back so the job stays queued while we look at it.
	e.setHook(func(ctx context.Context, n int, d time.Duration) error { <-ctx.Done(); return ctx.Err() })
	if _, err := e.svc.StartBulk(bg, "lib_none", false, "us_1"); err == nil {
		t.Error("an unknown library must be refused")
	}
	id, err := e.svc.StartBulk(bg, e.lib.ID, false, "")
	if err != nil {
		t.Fatal(err)
	}
	again, err := e.svc.StartBulk(bg, e.lib.ID, true, "")
	if err != nil || again != id {
		t.Errorf("a second start gave %q, %v; want the running job %q", again, err, id)
	}
	if n := e.count(`SELECT COUNT(*) FROM audit_log WHERE action = 'library.lookups_start'`); n != 1 {
		t.Errorf("start audit rows = %d", n)
	}
	j, p, found, err := e.svc.BulkStatus(bg, e.lib.ID)
	if err != nil || !found || j.ID != id {
		t.Fatalf("status: %v %v %v", j.ID, found, err)
	}
	_ = p
	if err := e.svc.StopBulk(bg, e.lib.ID, "us_1"); err != nil {
		t.Fatal(err)
	}
	if got := e.finished(id); got.State != jobs.StateCancelled {
		t.Errorf("state = %s", got.State)
	}
}

func TestBulkCoverFill(t *testing.T) {
	e := newBulkEnv(t)
	var mu sync.Mutex
	var filled []string
	e.svc.SetCoverFill(func(_ context.Context, bookID, url string) error {
		mu.Lock()
		defer mu.Unlock()
		filled = append(filled, bookID+" "+url)
		return nil
	})
	ids := e.addBooks(matchSpec(), matchSpec())
	if err := e.repo.Update(bg, func(tx *library.Tx) error {
		cv := library.Cover{BookID: ids[1], Source: library.CoverEmbedded, Mime: "image/jpeg", Width: 10, Height: 10,
			SHA256: strings.Repeat("c", 64), Path: "covers/c.jpg"}
		if err := tx.PutCover(&cv); err != nil {
			return err
		}
		return tx.SetBookCover(ids[1], cv.ID)
	}); err != nil {
		t.Fatal(err)
	}
	j := e.finished(e.enqueue(metadata.BulkAll))
	if j.State != jobs.StateDone {
		t.Fatalf("job: %s %s", j.State, j.ErrorCode)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(filled) != 1 || !strings.HasPrefix(filled[0], ids[0]+" https://m.media-amazon.com/") {
		t.Errorf("cover fills = %v; only the book without a cover gets one", filled)
	}
	if e.outcome(ids[0]) != library.LookupMatched || e.outcome(ids[1]) != library.LookupMatched {
		t.Errorf("outcomes: %q %q", e.outcome(ids[0]), e.outcome(ids[1]))
	}
}

func TestBulkApplyMarksChosen(t *testing.T) {
	e := newBulkEnv(t)
	ids := e.addBooks(matchSpec())
	res := e.lookup(ids[0], online.Query{Title: "The Test Match", Author: "Ann Author"})
	c := candidateTitled(t, res, "The Test Match")
	if _, err := e.svc.ApplyOnline(bg, ids[0], c, []string{metadata.FieldTitle}, "us_1"); err != nil {
		t.Fatal(err)
	}
	if got := e.outcome(ids[0]); got != library.LookupChosen {
		t.Fatalf("outcome = %q", got)
	}
	if err := e.svc.ResetOnline(bg, ids[0], "us_1"); err != nil {
		t.Fatal(err)
	}
	if got := e.outcome(ids[0]); got != library.LookupChosen {
		t.Errorf("a reset must not touch the marker: %q", got)
	}
	before := e.net.requests()
	if j := e.finished(e.enqueue(metadata.BulkAll)); j.State != jobs.StateDone || e.net.requests() != before {
		t.Errorf("job %s; %d requests for a chosen book", j.State, e.net.requests()-before)
	}
}

func TestBulkUsageManual(t *testing.T) {
	e := newBulkEnv(t)
	ids := e.addBooks(matchSpec())
	day := e.now().UnixMilli() / int64(24*time.Hour/time.Millisecond)
	// Already at the cap: a person's click still works, and is counted.
	if _, err := e.st.Writer().Exec(`INSERT INTO online_usage(day, provider, requests) VALUES (?, 'audnexus', ?)`, day, online.DailyCap); err != nil {
		t.Fatal(err)
	}
	res := e.lookup(ids[0], online.Query{Title: "The Test Match", Author: "Ann Author"})
	if len(res.Candidates) == 0 {
		t.Fatal("the manual lookup was blocked")
	}
	n, err := metadata.OnlineUsage(bg, e.st.Reader(), day, "audnexus")
	if err != nil || n != online.DailyCap+3 {
		t.Errorf("usage = %d, %v; want %d (the catalogue and two books)", n, err, online.DailyCap+3)
	}
	if err := metadata.AddOnlineUsage(bg, e.st, day, map[string]int{"google": 2, "nowhere": 9}); err != nil {
		t.Fatal(err)
	}
	if n, _ := metadata.OnlineUsage(bg, e.st.Reader(), day, "google"); n != 2 {
		t.Errorf("google = %d", n)
	}
	if err := metadata.PruneOnlineUsage(bg, e.st, day+1); err != nil {
		t.Fatal(err)
	}
	if n := e.count(`SELECT COUNT(*) FROM online_usage`); n != 0 {
		t.Errorf("rows after prune = %d", n)
	}
}
