package align_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/align"
	"github.com/Tend-Stack/tendshelf/internal/align/aligntest"
	alignrt "github.com/Tend-Stack/tendshelf/internal/align/runtime"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
)

// Job tests (helpers ajob*): a real jobs.Runner on a temp database, a fake emitter that slices a generated
// emissions matrix and a fake decoder that writes silence of the right length. No Python, no ffmpeg.

const (
	ajobFileAMs = 100000 // the first of two files; 4 windows
	ajobModel   = "ab"   // repeated to 64 hex digits
)

func ajobHex(s string) string { return strings.Repeat(s, 64/len(s)) }

type ajobFile struct {
	id, path, hash string
	idx            int
	offsetMs, dur  int64
}

// ajobEmitter plays the worker: it answers a request with the rows of the generated matrix.
type ajobEmitter struct {
	mu      sync.Mutex
	calls   int
	fail    int // the next fail calls return an error
	sha     string
	matrix  align.Matrix
	offsets map[string]int64 // file quick hash -> offset on the timeline
}

func (e *ajobEmitter) Identity() alignrt.Identity {
	e.mu.Lock()
	defer e.mu.Unlock()
	return alignrt.Identity{ModelSHA256: e.sha}
}

func (e *ajobEmitter) Emit(_ context.Context, req alignrt.Request) (alignrt.Emissions, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.calls++
	if e.fail > 0 {
		e.fail--
		return alignrt.Emissions{}, errors.New("worker crashed")
	}
	frames := (len(req.PCM)/2-400)/320 + 1
	V := e.matrix.V
	out := alignrt.Emissions{Frames: frames, Vocab: V, FrameMs: 20, LogProbs: make([]float32, frames*V)}
	start := int((e.offsets[req.FileQuickHash] + req.OffsetMs) / 20)
	for t := 0; t < frames; t++ {
		row := out.LogProbs[t*V : (t+1)*V]
		if g := start + t; g < e.matrix.T {
			copy(row, e.matrix.Row(g))
			continue
		}
		for i := range row {
			row[i] = -25
		}
		row[0] = 0
	}
	return out, nil
}

func (e *ajobEmitter) count() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.calls
}

// ajobRuntimes is the service's view of the runtime.
type ajobRuntimes struct {
	mu     sync.Mutex
	comp   alignrt.Component
	usable bool
	em     *ajobEmitter
}

func (r *ajobRuntimes) Usable(context.Context, string) (alignrt.Component, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.comp, r.usable, nil
}

func (r *ajobRuntimes) Emitter(context.Context, string) (alignrt.Emitter, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.usable {
		return nil, alignrt.ErrNotUsable
	}
	return r.em, nil
}

func (r *ajobRuntimes) setModel(sha string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.comp.ModelSHA256 = sha
	r.em.mu.Lock()
	r.em.sha = sha
	r.em.mu.Unlock()
}

// ajobDecoder writes silence for the requested span, clipped to the file.
type ajobDecoder struct{ durs map[string]int64 }

func (d ajobDecoder) PCM16k(ctx context.Context, src string, fromMs, durMs int64, w io.Writer) error {
	ms := min(durMs, d.durs[src]-fromMs)
	buf := make([]byte, 32000) // one second
	for left := max(ms, 0) * 32; left > 0; {
		if err := ctx.Err(); err != nil {
			return context.Cause(ctx)
		}
		n := min(int64(len(buf)), left)
		if _, err := w.Write(buf[:n]); err != nil {
			return err
		}
		left -= n
	}
	return nil
}

type ajobEnv struct {
	t      *testing.T
	st     *db.Store
	data   string
	files  []ajobFile
	em     *ajobEmitter
	rt     *ajobRuntimes
	dec    ajobDecoder
	doc    *ebook.Doc
	matrix align.Matrix
	script aligntest.Script

	mu     sync.Mutex
	hashes map[string]string // path -> the quick hash the "disk" reports
	hook   func(ctx context.Context, stage string, n int)

	svc    *align.Service
	runner *jobs.Runner
	cancel context.CancelFunc
}

// ajobNew builds the database, the book with two audio files, an ebook with its edition, and the fakes.
// otherAudio makes the narration read a different text, so nothing matches.
func ajobNew(t *testing.T, chapters [][]string, otherAudio bool) *ajobEnv {
	t.Helper()
	ctx := context.Background()
	e := &ajobEnv{t: t, data: t.TempDir(), hashes: map[string]string{}}
	var err error
	if e.st, err = db.Open(ctx, e.data); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { e.stop(); e.st.Close() })

	e.doc = aligntest.Doc(chapters)
	spoken := e.doc
	if otherAudio {
		spoken = aligntest.Doc(aligntest.Chapters(99, 4, 150))
	}
	v := aligntest.Vocab()
	e.script = aligntest.NewScript(aligntest.Pieces(spoken), 5)
	e.matrix, _ = aligntest.Gen(e.script, v, 0.5, 9)
	durB := (e.script.TotalMS - ajobFileAMs) / 20 * 20
	e.files = []ajobFile{
		{id: "af_1", path: "/audio/a/01.mp3", hash: ajobHex("1"), idx: 0, offsetMs: 0, dur: ajobFileAMs},
		{id: "af_2", path: "/audio/a/02.mp3", hash: ajobHex("2"), idx: 1, offsetMs: ajobFileAMs, dur: durB},
	}
	offsets := map[string]int64{}
	e.dec.durs = map[string]int64{}
	for _, f := range e.files {
		offsets[f.hash] = f.offsetMs
		e.dec.durs[f.path] = f.dur
		e.hashes[f.path] = f.hash
	}
	e.em = &ajobEmitter{sha: ajobHex(ajobModel), matrix: e.matrix, offsets: offsets}
	ident, _ := json.Marshal(alignrt.Identity{Vocab: v.Symbols, Blank: v.Blank, Delimiter: "|", ModelSHA256: ajobHex(ajobModel)})
	e.rt = &ajobRuntimes{em: e.em, usable: true, comp: alignrt.Component{
		ID: "rc_1", Kind: "emissions-model", Language: "en", ModelSHA256: ajobHex(ajobModel), IdentityJSON: string(ident),
	}}

	docJSON, err := ebook.Encode(e.doc)
	if err != nil {
		t.Fatal(err)
	}
	docSHA, err := ebook.WriteDoc(filepath.Join(e.data, "text"), docJSON)
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`INSERT INTO libraries(id, name, created_at) VALUES ('lib_1', 'Books', 1)`,
		`INSERT INTO library_folders(id, library_id, path, created_at) VALUES ('lf_1', 'lib_1', '/audio', 1)`,
		`INSERT INTO books(id, library_id, folder_id, root_path, title, added_at, updated_at) VALUES ('bk_1', 'lib_1', 'lf_1', '/audio/a', 'A', 1, 1)`,
		`INSERT INTO ebook_files(id, origin, library_id, folder_id, path, book_id, ext, size, quick_hash, sha256, state, created_at, updated_at)
VALUES ('ef_1', 'library', 'lib_1', 'lf_1', '/audio/a/a.epub', 'bk_1', 'epub', 10, '` + ajobHex("e") + `', '` + ajobHex("a") + `', 'ready', 1, 1)`,
		`INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, language, chapter_count, word_count, char_count, created_at)
VALUES ('ed_1', 'ef_1', '` + ajobHex("a") + `', '` + ebook.Recipe + `', '` + docSHA + `', 5, 'epub', 'en-GB', 4, 600, 3000, 1)`,
		`INSERT INTO runtime_components(id, kind, name, language, revision, model_sha256, files_json, python_path, worker_sha256, licence_spdx, licence_url, source_url, checked_at)
VALUES ('rc_1', 'emissions-model', 'w2v', 'en', 'r', '` + ajobHex(ajobModel) + `', '[]', '/py', '` + ajobHex("w") + `', 'MIT', 'u', 'u', 1)`,
	}
	for _, f := range e.files {
		stmts = append(stmts, fmt.Sprintf(`INSERT INTO audio_files(id, book_id, path, idx, size, mtime_ns, quick_hash, container, codec, duration_ms, offset_ms, updated_at)
VALUES ('%s', 'bk_1', '%s', %d, 1, 1, '%s', 'mp3', 'mp3', %d, %d, 1)`, f.id, f.path, f.idx, f.hash, f.dur, f.offsetMs))
	}
	for _, q := range stmts {
		if _, err := e.st.Writer().Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return e
}

func ajobChapters() [][]string { return aligntest.Chapters(1, 4, 150) }

// boot starts a fresh runner and service on the same database, as a restart would.
func (e *ajobEnv) boot(start bool) {
	e.t.Helper()
	runner, err := jobs.New(e.st, jobs.Options{PollEvery: 10 * time.Millisecond, ProgressEvery: 20 * time.Millisecond, SweepEvery: time.Hour})
	if err != nil {
		e.t.Fatal(err)
	}
	e.runner = runner
	e.svc = align.NewService(align.Deps{
		Store: e.st, Library: library.New(e.st), Ebooks: ebook.NewStore(e.st), Runner: runner, Runtimes: e.rt, Decoder: e.dec,
		DataDir: e.data, CacheMaxBytes: 1 << 30,
		Hash: func(path string) (string, int64, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			return e.hashes[path], 1, nil
		},
		Hook: func(ctx context.Context, stage string, n int) {
			e.mu.Lock()
			h := e.hook
			e.mu.Unlock()
			if h != nil {
				h(ctx, stage, n)
			}
		},
	})
	if err := e.svc.Register(runner); err != nil {
		e.t.Fatal(err)
	}
	if start {
		ctx, cancel := context.WithCancel(context.Background())
		e.cancel = cancel
		if err := runner.Start(ctx); err != nil {
			e.t.Fatal(err)
		}
	}
}

func (e *ajobEnv) stop() {
	if e.runner == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_ = e.runner.Stop(ctx)
	if e.cancel != nil {
		e.cancel()
	}
	e.runner = nil
}

func (e *ajobEnv) setHook(h func(ctx context.Context, stage string, n int)) {
	e.mu.Lock()
	e.hook = h
	e.mu.Unlock()
}

// windows is the number of cached windows a clean run writes.
func (e *ajobEnv) windows() int {
	n := 0
	for _, f := range e.files {
		n += int((f.dur + 29999) / 30000)
	}
	return n
}

func (e *ajobEnv) start() align.Alignment {
	e.t.Helper()
	a, err := e.svc.Start(context.Background(), "ed_1", "bk_1", "")
	if err != nil {
		e.t.Fatalf("Start: %v", err)
	}
	return a
}

// waitFor polls the alignment until pred holds.
func (e *ajobEnv) waitFor(id string, what string, pred func(align.Alignment) bool) align.Alignment {
	e.t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	for {
		a, ok, err := e.svc.Store().Get(context.Background(), id)
		if err != nil || !ok {
			e.t.Fatalf("Get: %v %v", ok, err)
		}
		if pred(a) {
			return a
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("timed out waiting for %s; state %s code %q", what, a.State, a.ErrorCode)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func ajobState(states ...string) func(align.Alignment) bool {
	return func(a align.Alignment) bool {
		for _, s := range states {
			if a.State == s {
				return true
			}
		}
		return false
	}
}

func (e *ajobEnv) job(id string) jobs.Job {
	e.t.Helper()
	j, err := e.runner.Get(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return j
}

// interruptAt stops the runner (a shutdown) when the hook stage reaches n and holds the handler until its
// context ends, once.
func (e *ajobEnv) interruptAt(stage string, n int) <-chan struct{} {
	stopped := make(chan struct{})
	var once sync.Once
	runner := e.runner
	e.setHook(func(ctx context.Context, s string, got int) {
		if s != stage || got != n {
			return
		}
		once.Do(func() {
			go func() {
				sctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				defer cancel()
				_ = runner.Stop(sctx)
				close(stopped)
			}()
			<-ctx.Done()
		})
	})
	return stopped
}

// ajobCleanRun is the number of Viterbi segments a clean run needs, measured once.
func ajobCleanRun(t *testing.T, chapters [][]string) (viterbi int, keys []string) {
	t.Helper()
	e := ajobNew(t, chapters, false)
	e.boot(true)
	e.setHook(func(_ context.Context, s string, n int) {
		if s == "viterbi" {
			viterbi = n
		}
	})
	a := e.start()
	a = e.waitFor(a.ID, "done", ajobState(align.StateDone, align.StatePartial, align.StateFailed))
	if a.State != align.StateDone {
		t.Fatalf("clean run ended %s %s", a.State, a.ErrorCode)
	}
	rows, err := e.svc.Store().Chapters(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		keys = append(keys, r.ChapterKey)
	}
	return viterbi, keys
}

func TestAlignJobRuns(t *testing.T) {
	e := ajobNew(t, ajobChapters(), false)
	e.boot(true)
	a := e.start()
	if a.State != align.StateQueued && a.JobID == "" {
		t.Fatalf("started row = %+v", a)
	}
	a = e.waitFor(a.ID, "done", ajobState(align.StateDone, align.StatePartial, align.StateFailed))
	if a.State != align.StateDone || a.Coverage < 0.9 || a.SentencesTimed == 0 || a.SentencesTimed > a.SentencesTotal {
		t.Fatalf("alignment = %+v", a)
	}
	rows, err := e.svc.Store().Chapters(context.Background(), a.ID)
	if err != nil || len(rows) != 4 {
		t.Fatalf("chapters %d, %v", len(rows), err)
	}
	for _, r := range rows {
		if r.State != align.ChapterAligned || r.StartMS == nil || r.EndMS == nil {
			t.Errorf("chapter %+v", r)
		}
		if _, err := align.ReadTiming(filepath.Join(e.data, "align"), r.ChapterKey); err != nil {
			t.Errorf("timing file for chapter %d: %v", r.Idx, err)
		}
	}
	if got := e.em.count(); got != e.windows() {
		t.Errorf("Emit calls %d, windows %d", got, e.windows())
	}
	st, ok, err := e.svc.Status(context.Background(), "ed_1")
	if err != nil || !ok || st.ID != a.ID || len(st.Chapters) != 4 || st.Stale {
		t.Fatalf("Status = %+v %v %v", st, ok, err)
	}
	if st.Job == nil || st.Job.State != jobs.StateDone {
		t.Errorf("job = %+v", st.Job)
	}
	// Progress named the stage and the chapters.
	var pr struct {
		Stage         string `json:"stage"`
		ChaptersTotal int    `json:"chaptersTotal"`
	}
	if err := json.Unmarshal(st.Job.Progress, &pr); err != nil || pr.Stage != "aligning" || pr.ChaptersTotal != 4 {
		t.Errorf("progress %s: %v", st.Job.Progress, err)
	}
}

func TestAlignJobKindNoHold(t *testing.T) {
	e := ajobNew(t, ajobChapters(), false)
	e.boot(false)
	k := e.svc.BookKindForTest()
	if k.Name != align.KindBook || k.HoldOnRecovery || k.Concurrency != 1 || k.MaxAttempts != 3 {
		t.Errorf("kind = %+v", k)
	}
}

func TestAlignJobResumeEmitting(t *testing.T) {
	e := ajobNew(t, ajobChapters(), false)
	e.boot(true)
	stopped := e.interruptAt("window", 3)
	a := e.start()
	<-stopped
	if got := e.em.count(); got != 3 {
		t.Fatalf("Emit calls before the stop = %d, want 3", got)
	}
	e.runner = nil
	e.setHook(nil)
	e.boot(true)
	a = e.waitFor(a.ID, "done", ajobState(align.StateDone, align.StatePartial, align.StateFailed))
	if a.State != align.StateDone {
		t.Fatalf("state %s %s", a.State, a.ErrorCode)
	}
	if got := e.em.count(); got != e.windows() {
		t.Errorf("Emit calls %d over both runs, windows %d", got, e.windows())
	}
}

func TestAlignJobResumeAligning(t *testing.T) {
	baseline, keys := ajobCleanRun(t, ajobChapters())
	if baseline == 0 {
		t.Fatal("a clean run did no Viterbi")
	}
	e := ajobNew(t, ajobChapters(), false)
	e.boot(true)
	stopped := e.interruptAt("chapter", 2)
	a := e.start()
	<-stopped
	rows, err := e.svc.Store().Chapters(context.Background(), a.ID)
	if err != nil || len(rows) != 2 {
		t.Fatalf("chapters saved before the stop: %d, %v", len(rows), err)
	}
	callsBefore := e.em.count()
	e.runner = nil
	var second int
	e.setHook(func(_ context.Context, s string, n int) {
		if s == "viterbi" {
			second = n
		}
	})
	e.boot(true)
	a = e.waitFor(a.ID, "done", ajobState(align.StateDone, align.StatePartial, align.StateFailed))
	if a.State != align.StateDone {
		t.Fatalf("state %s %s", a.State, a.ErrorCode)
	}
	if got := e.em.count(); got != callsBefore || got != e.windows() {
		t.Errorf("Emit calls %d (before the stop %d), windows %d", got, callsBefore, e.windows())
	}
	if second == 0 || second >= baseline {
		t.Errorf("Viterbi runs after the restart = %d, a clean run needs %d", second, baseline)
	}
	rows, _ = e.svc.Store().Chapters(context.Background(), a.ID)
	for i, r := range rows {
		if r.ChapterKey != keys[i] {
			t.Errorf("chapter %d key differs from a clean run", r.Idx)
		}
	}
}

func TestAlignJobLeaseRecovery(t *testing.T) {
	e := ajobNew(t, ajobChapters(), false)
	e.boot(false)
	a := e.start()
	// A process died after caching two windows: the job is running with an expired lease.
	cache := align.Cache{Root: filepath.Join(e.data, "cache", "align")}
	for k := 0; k < 2; k++ {
		em := align.QuantiseMatrix(align.Matrix{T: 1500, V: e.matrix.V, LogP: e.matrix.LogP[k*1500*e.matrix.V : (k+1)*1500*e.matrix.V]})
		if err := cache.Write(e.files[0].hash, ajobHex(ajobModel), k, em); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.st.Writer().Exec(`UPDATE jobs SET state = 'running', attempts = 1, lease_until = 1 WHERE id = ?`, a.JobID); err != nil {
		t.Fatal(err)
	}
	e.stop()
	e.boot(true)
	a = e.waitFor(a.ID, "done", ajobState(align.StateDone, align.StatePartial, align.StateFailed))
	if a.State != align.StateDone {
		t.Fatalf("state %s %s", a.State, a.ErrorCode)
	}
	if got := e.em.count(); got != e.windows()-2 {
		t.Errorf("Emit calls %d, want %d (two windows were cached)", got, e.windows()-2)
	}
}

func TestAlignJobCancel(t *testing.T) {
	e := ajobNew(t, ajobChapters(), false)
	e.boot(true)
	idc := make(chan string, 1)
	var once sync.Once
	cancelled := make(chan error, 1)
	e.setHook(func(ctx context.Context, s string, n int) {
		if s != "window" || n != 2 {
			return
		}
		once.Do(func() {
			cancelled <- e.svc.Cancel(context.Background(), <-idc)
			<-ctx.Done()
		})
	})
	a := e.start()
	id := a.ID
	idc <- id
	if err := <-cancelled; err != nil {
		t.Fatal(err)
	}
	a = e.waitFor(id, "cancelled", ajobState(align.StateCancelled))
	time.Sleep(100 * time.Millisecond)
	if got := e.em.count(); got != 2 {
		t.Errorf("Emit calls after the cancel = %d, want 2", got)
	}
	if ok, err := e.svc.Store().PutChapter(context.Background(), id, a.JobID, align.ChapterRow{Idx: 1, State: align.ChapterPending}); err != nil || ok {
		t.Errorf("a cancelled job wrote a chapter: %v %v", ok, err)
	}
	if ok, _ := e.svc.Store().SetState(context.Background(), id, a.JobID, align.StateAligning, ""); ok {
		t.Error("a cancelled job moved its row")
	}
	if j := e.job(a.JobID); j.State != jobs.StateCancelled {
		t.Errorf("job %s", j.State)
	}
	if rows, _ := e.svc.Store().Chapters(context.Background(), id); len(rows) != 0 {
		t.Errorf("%d chapters left behind", len(rows))
	}
}

func TestAlignJobWorkerFailedHolds(t *testing.T) {
	e := ajobNew(t, ajobChapters(), false)
	e.em.fail = 3
	e.boot(true)
	a := e.start()
	a = e.waitFor(a.ID, "held", ajobState(align.StateHeld, align.StateFailed, align.StateDone))
	if a.State != align.StateHeld || a.ErrorCode != align.HoldWorkerFailed {
		t.Fatalf("state %s code %q", a.State, a.ErrorCode)
	}
	deadline := time.Now().Add(10 * time.Second)
	for e.job(a.JobID).State != jobs.StateHeld && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if j := e.job(a.JobID); j.State != jobs.StateHeld || j.Attempts != 0 {
		t.Fatalf("job state %s attempts %d, want held and 0", j.State, j.Attempts)
	}
	if got := e.em.count(); got != 3 {
		t.Errorf("Emit calls %d, want 3", got)
	}
	if err := e.svc.Resume(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	a = e.waitFor(a.ID, "done", ajobState(align.StateDone, align.StatePartial, align.StateFailed))
	if a.State != align.StateDone || a.ErrorCode != "" {
		t.Fatalf("after Resume: %s %q", a.State, a.ErrorCode)
	}
	if err := e.svc.Resume(context.Background(), a.ID); !errors.Is(err, align.ErrNotHeld) {
		t.Errorf("Resume of a finished alignment = %v", err)
	}
}

func TestAlignJobRuntimeChanged(t *testing.T) {
	e := ajobNew(t, ajobChapters(), false)
	e.boot(false)
	a := e.start()
	newSHA := ajobHex("cd")
	e.rt.setModel(newSHA)
	e.stop()
	e.boot(true)
	a = e.waitFor(a.ID, "held", ajobState(align.StateHeld, align.StateDone, align.StateFailed))
	if a.State != align.StateHeld || a.ErrorCode != align.HoldRuntimeChanged {
		t.Fatalf("state %s code %q", a.State, a.ErrorCode)
	}
	if e.em.count() != 0 {
		t.Error("the worker ran before the person decided")
	}
	deadline := time.Now().Add(10 * time.Second)
	for e.job(a.JobID).State != jobs.StateHeld && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := e.svc.Resume(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	a = e.waitFor(a.ID, "done", ajobState(align.StateDone, align.StatePartial, align.StateFailed))
	if a.State != align.StateDone || a.ModelSHA256 != newSHA {
		t.Fatalf("after Resume: %s model %s", a.State, a.ModelSHA256)
	}
}

func TestAlignJobAudioChanged(t *testing.T) {
	// A file replaced between Start and the run: the audio key differs.
	e := ajobNew(t, ajobChapters(), false)
	e.boot(false)
	a := e.start()
	newHash := ajobHex("3")
	if _, err := e.st.Writer().Exec(`UPDATE audio_files SET quick_hash = ? WHERE id = 'af_2'`, newHash); err != nil {
		t.Fatal(err)
	}
	e.hashes[e.files[1].path] = newHash
	e.em.offsets[newHash] = e.files[1].offsetMs
	e.stop()
	e.boot(true)
	a = e.waitFor(a.ID, "held", ajobState(align.StateHeld, align.StateDone, align.StateFailed))
	if a.State != align.StateHeld || a.ErrorCode != align.HoldAudioChanged {
		t.Fatalf("state %s code %q", a.State, a.ErrorCode)
	}
	deadline := time.Now().Add(10 * time.Second)
	for e.job(a.JobID).State != jobs.StateHeld && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if err := e.svc.Resume(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	a = e.waitFor(a.ID, "done", ajobState(align.StateDone, align.StatePartial, align.StateFailed))
	if a.State != align.StateDone || a.AudioKey == "" {
		t.Fatalf("after Resume: %s", a.State)
	}

	// A file that changes during the emitting stage is caught by the quick hash check at its end.
	e2 := ajobNew(t, ajobChapters(), false)
	e2.hashes[e2.files[0].path] = ajobHex("9")
	e2.boot(true)
	b := e2.start()
	b = e2.waitFor(b.ID, "held", ajobState(align.StateHeld, align.StateDone, align.StateFailed))
	if b.State != align.StateHeld || b.ErrorCode != align.HoldAudioChanged {
		t.Fatalf("end-of-stage check: state %s code %q", b.State, b.ErrorCode)
	}
	if got := e2.em.count(); got != e2.windows() {
		t.Errorf("windows were emitted before the check: %d of %d", got, e2.windows())
	}
}

func TestAlignJobRechapter(t *testing.T) {
	base, keys := ajobCleanRun(t, ajobChapters())
	e := ajobNew(t, ajobChapters(), false)
	e.boot(true)
	a := e.waitFor(e.start().ID, "done", ajobState(align.StateDone, align.StatePartial, align.StateFailed))
	if a.State != align.StateDone {
		t.Fatalf("first run %s", a.State)
	}
	calls := e.em.count()

	// The ebook is re-read with one word of chapter 3 changed: a new edition of the same file.
	edited := ajobChapters()
	words := strings.Fields(edited[2][0])
	edited[2][0] = strings.Replace(edited[2][0], words[2], "banana", 1)
	docJSON, err := ebook.Encode(aligntest.Doc(edited))
	if err != nil {
		t.Fatal(err)
	}
	sha, err := ebook.WriteDoc(filepath.Join(e.data, "text"), docJSON)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`UPDATE ebook_files SET sha256 = '` + ajobHex("b") + `' WHERE id = 'ef_1'`,
		`INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, language, chapter_count, word_count, char_count, created_at)
VALUES ('ed_2', 'ef_1', '` + ajobHex("b") + `', '` + ebook.Recipe + `', '` + sha + `', 5, 'epub', 'en-GB', 4, 600, 3000, 2)`,
	} {
		if _, err := e.st.Writer().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	var viterbi int
	e.setHook(func(_ context.Context, s string, n int) {
		if s == "viterbi" {
			viterbi = n
		}
	})
	b, err := e.svc.Start(context.Background(), "ed_2", "bk_1", "")
	if err != nil {
		t.Fatal(err)
	}
	b = e.waitFor(b.ID, "done", ajobState(align.StateDone, align.StatePartial, align.StateFailed))
	if b.State == align.StateFailed {
		t.Fatalf("second run failed: %s", b.ErrorCode)
	}
	if got := e.em.count(); got != calls {
		t.Errorf("a re-chaptered ebook called the worker %d more times", got-calls)
	}
	rows, _ := e.svc.Store().Chapters(context.Background(), b.ID)
	same := 0
	for i, r := range rows {
		if i < len(keys) && r.ChapterKey == keys[i] {
			same++
		}
	}
	if same != 3 {
		t.Errorf("%d chapters kept their key, want 3", same)
	}
	if viterbi == 0 || viterbi >= base {
		t.Errorf("Viterbi runs = %d, a full run needs %d", viterbi, base)
	}
}

func TestAlignJobStart(t *testing.T) {
	ctx := context.Background()
	t.Run("runtime missing", func(t *testing.T) {
		e := ajobNew(t, ajobChapters(), false)
		e.boot(false)
		e.rt.usable = false
		if _, err := e.svc.Start(ctx, "ed_1", "bk_1", ""); !errors.Is(err, align.ErrRuntimeMissing) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("audio unavailable", func(t *testing.T) {
		e := ajobNew(t, ajobChapters(), false)
		e.boot(false)
		if _, err := e.st.Writer().Exec(`UPDATE audio_files SET missing_since = 5 WHERE id = 'af_1'`); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.Start(ctx, "ed_1", "bk_1", ""); !errors.Is(err, align.ErrAudioUnavailable) {
			t.Errorf("err = %v", err)
		}
	})
	t.Run("edition not ready", func(t *testing.T) {
		e := ajobNew(t, ajobChapters(), false)
		e.boot(false)
		if _, err := e.svc.Start(ctx, "ed_1", "bk_other", ""); !errors.Is(err, align.ErrEditionNotReady) {
			t.Errorf("other book: %v", err)
		}
		if _, err := e.svc.Start(ctx, "ed_none", "bk_1", ""); !errors.Is(err, align.ErrEditionNotReady) {
			t.Errorf("no edition: %v", err)
		}
		if _, err := e.st.Writer().Exec(`UPDATE ebook_files SET state = 'pending' WHERE id = 'ef_1'`); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.Start(ctx, "ed_1", "bk_1", ""); !errors.Is(err, align.ErrEditionNotReady) {
			t.Errorf("pending file: %v", err)
		}
	})
	t.Run("active dedup and finished reuse", func(t *testing.T) {
		e := ajobNew(t, ajobChapters(), false)
		e.boot(false)
		a := e.start()
		if a.State != align.StateQueued || a.JobID == "" {
			t.Fatalf("row = %+v", a)
		}
		if b := e.start(); b.ID != a.ID {
			t.Errorf("second Start made %s, want %s", b.ID, a.ID)
		}
		var n int
		if err := e.st.Reader().QueryRow(`SELECT count(*) FROM jobs WHERE kind = 'align.book'`).Scan(&n); err != nil || n != 1 {
			t.Errorf("jobs = %d, %v", n, err)
		}
		e.stop()
		e.boot(true)
		a = e.waitFor(a.ID, "done", ajobState(align.StateDone, align.StatePartial, align.StateFailed))
		if b := e.start(); b.ID != a.ID {
			t.Errorf("Start after a finished run made %s, want %s", b.ID, a.ID)
		}
	})
}

func TestAlignJobReconcile(t *testing.T) {
	ctx := context.Background()
	e := ajobNew(t, ajobChapters(), false)
	e.boot(false)
	st := e.svc.Store()
	insert := func(id, jobID, state string) {
		t.Helper()
		if err := st.Insert(ctx, align.Alignment{ID: id, EditionID: "ed_1", BookID: "bk_1", AudioKey: ajobHex("a"), DocSHA256: ajobHex("b"), JobID: jobID, State: state}); err != nil {
			t.Fatal(err)
		}
	}
	check := func(id, state, code string) {
		t.Helper()
		if err := e.svc.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		a, _, _ := st.Get(ctx, id)
		if a.State != state || a.ErrorCode != code {
			t.Errorf("%s: state %s code %q, want %s %q", id, a.State, a.ErrorCode, state, code)
		}
	}
	enqueue := func() string {
		t.Helper()
		id, err := e.runner.Enqueue(ctx, align.KindBook, map[string]string{"alignmentId": "none"}, jobs.EnqueueOptions{})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}

	insert("al_nojob", "", align.StateQueued)
	check("al_nojob", align.StateCancelled, "")

	insert("al_missing", "jb_missing", align.StateEmitting)
	check("al_missing", align.StateCancelled, "")

	j := enqueue()
	insert("al_failed", j, align.StateAligning)
	if _, err := e.st.Writer().Exec(`UPDATE jobs SET state = 'failed', error_code = 'no_match' WHERE id = ?`, j); err != nil {
		t.Fatal(err)
	}
	check("al_failed", align.StateFailed, "no_match")

	j = enqueue()
	insert("al_cancelled", j, align.StateEmitting)
	if err := e.runner.Cancel(ctx, j); err != nil {
		t.Fatal(err)
	}
	check("al_cancelled", align.StateCancelled, "")

	j = enqueue() // a queued job: the row that says emitting goes back to queued
	insert("al_requeued", j, align.StateEmitting)
	check("al_requeued", align.StateQueued, "")
}

func TestAlignJobNoMatch(t *testing.T) {
	e := ajobNew(t, ajobChapters(), true)
	e.boot(true)
	a := e.start()
	a = e.waitFor(a.ID, "failed", ajobState(align.StateFailed, align.StateDone, align.StatePartial))
	if a.State != align.StateFailed || a.ErrorCode != align.CodeNoMatch {
		t.Fatalf("state %s code %q", a.State, a.ErrorCode)
	}
	deadline := time.Now().Add(10 * time.Second)
	for e.job(a.JobID).State != jobs.StateFailed && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if j := e.job(a.JobID); j.State != jobs.StateFailed || j.ErrorCode != align.CodeNoMatch || j.Attempts != 1 {
		t.Errorf("job = %s %q attempts %d", j.State, j.ErrorCode, j.Attempts)
	}
	// A new request after a failure starts again.
	if b := e.start(); b.ID == a.ID {
		t.Error("Start reused a failed alignment")
	}
}

// TestAlignJobBackupExcludesCache checks AGENTS.md's promise that a backup is the database only: the
// emissions cache and the timing files never enter it.
func TestAlignJobBackupExcludesCache(t *testing.T) {
	e := ajobNew(t, ajobChapters(), false)
	cache := align.Cache{Root: filepath.Join(e.data, "cache", "align")}
	big := align.Em{Frames: 1500, Vocab: 32, FrameMs: 20, Data: make([]uint8, 1500*32)}
	for k := 0; k < 60; k++ {
		if err := cache.Write(e.files[0].hash, ajobHex(ajobModel), k, big); err != nil {
			t.Fatal(err)
		}
	}
	c, err := db.WriteCopy(context.Background(), e.st, e.data, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if c.Bytes > 1<<20 {
		t.Errorf("backup copy is %d bytes: the cache leaked into it", c.Bytes)
	}
	entries, err := os.ReadDir(filepath.Join(e.data, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	for _, en := range entries {
		if !strings.HasSuffix(en.Name(), ".db") {
			t.Errorf("unexpected backup entry %s", en.Name())
		}
	}
}
