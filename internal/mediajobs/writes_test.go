package mediajobs

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

// wenv is an env with a running job runner and a Writes service whose OnChanged is recorded.
type wenv struct {
	*env
	r       *jobs.Runner
	w       *Writes
	mu      sync.Mutex
	changed []string
}

func (e *wenv) changedLibraries() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.changed...)
}

func newWEnv(t *testing.T, tools media.Tools, ffLog string) *wenv {
	t.Helper()
	e := newEnvWith(t, tools, ffLog)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	r, err := jobs.New(e.st, jobs.Options{
		Lease: 5 * time.Second, RenewEvery: time.Second, ProgressEvery: 50 * time.Millisecond,
		SweepEvery: time.Second, PollEvery: 10 * time.Millisecond,
		Backoff: func(int) time.Duration { return 10 * time.Millisecond }, Logger: log,
	})
	if err != nil {
		t.Fatal(err)
	}
	we := &wenv{env: e, r: r}
	we.w = NewWrites(e.repo, tools, e.data, r, r, log)
	we.w.OnChanged = func(_ context.Context, libraryID string) {
		we.mu.Lock()
		we.changed = append(we.changed, libraryID)
		we.mu.Unlock()
	}
	covers := NewCovers(e.repo, tools, e.data, r, log)
	compat := NewCompat(e.repo, tools, CompatDir(e.data), 1<<30, r, log)
	peaks := NewPeaks(e.repo, tools, PeaksDir(e.data), r, false, log)
	if err := Register(r, covers, compat, peaks, we.w); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(bg); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(bg, 5*time.Second)
		defer cancel()
		_ = r.Stop(ctx)
	})
	return we
}

func newFakeWEnv(t *testing.T) *wenv {
	t.Helper()
	tools := mediatest.FakeTools(t)
	ffLog := filepath.Join(t.TempDir(), "ffmpeg.log")
	tools.Env = append(tools.Env, mediatest.FakeFFmpegLogEnv+"="+ffLog)
	return newWEnv(t, tools, ffLog)
}

// oldFile backdates a file so the 10 minute rule does not refuse it.
func oldFile(t *testing.T, path string) {
	t.Helper()
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(path, old, old); err != nil {
		t.Fatal(err)
	}
}

// fakeBook makes a book of n fake m4b files and scans it. Files are backdated unless recent is set.
func (e *wenv) fakeBook(rel string, n int, recent bool) library.Book {
	e.t.Helper()
	for i := 1; i <= n; i++ {
		name := rel + "/0" + string(rune('0'+i)) + ".m4b"
		if n > 1 { // the scan makes one book of a folder whose files all carry a part marker
			name = rel + "/Part " + string(rune('0'+i)) + ".m4b"
		}
		p := e.fake(name, mediatest.FakeFile{PadBytes: 3000 + 100*i,
			Tags: map[string]string{"album": "Old Title", "artist": "Ann Author", "comment": name}})
		if !recent {
			oldFile(e.t, p)
		}
	}
	e.scanAll()
	return e.book(rel)
}

// start queues the write and waits for its job; it returns the job.
func (e *wenv) start(book library.Book) (jobs.Job, error) {
	e.t.Helper()
	id, err := e.w.Start(bg, book.ID, "")
	if err != nil {
		return jobs.Job{}, err
	}
	return e.wait(id), nil
}

func (e *wenv) wait(id string) jobs.Job {
	e.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		j, err := e.r.Get(bg, id)
		if err != nil {
			e.t.Fatal(err)
		}
		if j.State != jobs.StateQueued && j.State != jobs.StateRunning {
			return j
		}
		if time.Now().After(deadline) {
			e.t.Fatalf("job %s still %s", id, j.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (e *wenv) rows(book library.Book, states ...string) []library.FileWrite {
	e.t.Helper()
	rows, err := e.repo.BookFileWrites(bg, book.ID, states...)
	if err != nil {
		e.t.Fatal(err)
	}
	return rows
}

func (e *wenv) allRows() int {
	e.t.Helper()
	var n int
	if err := e.st.Reader().QueryRow(`SELECT count(*) FROM file_writes`).Scan(&n); err != nil {
		e.t.Fatal(err)
	}
	return n
}

// snapshot maps every entry of a folder, hidden ones included, to the SHA-256 of its bytes.
func snapshot(t *testing.T, dir string) map[string]string {
	t.Helper()
	out := map[string]string{}
	es, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range es {
		sha, err := media.FileSHA256(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		out[e.Name()] = sha
	}
	return out
}

func names(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func fileSHA(t *testing.T, path string) string {
	t.Helper()
	s, err := media.FileSHA256(path)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestFileWritesHappy(t *testing.T) {
	e := newFakeWEnv(t)
	book := e.fakeBook("Book", 2, false)
	dir := e.path("Book")
	before := snapshot(t, dir)
	hashes := map[string]string{}
	for _, n := range []string{"Part 1.m4b", "Part 2.m4b"} {
		h, err := e.tools.AudioHash(bg, filepath.Join(dir, n))
		if err != nil {
			t.Fatal(err)
		}
		hashes[n] = h
	}

	j, err := e.start(book)
	if err != nil || j.State != jobs.StateDone {
		t.Fatalf("job = %+v, %v", j, err)
	}
	after := snapshot(t, dir)
	want := []string{".Part 1.m4b.tendshelf-before", ".Part 2.m4b.tendshelf-before", "Part 1.m4b", "Part 2.m4b"}
	if got := names(after); !sameStrings(got, want) {
		t.Fatalf("folder = %v, want %v", got, want)
	}
	for _, n := range []string{"Part 1.m4b", "Part 2.m4b"} {
		if after["."+n+".tendshelf-before"] != before[n] {
			t.Errorf("backup of %s is not the original", n)
		}
		if after[n] == before[n] {
			t.Errorf("%s was not rewritten", n)
		}
		h, err := e.tools.AudioHash(bg, filepath.Join(dir, n))
		if err != nil || h != hashes[n] {
			t.Errorf("audio hash of %s = %q (%v), want %q", n, h, err, hashes[n])
		}
		p, err := e.tools.Probe(bg, filepath.Join(dir, n))
		if err != nil || p.Tags["album"] != "Book" || p.Tags["comment"] == "" {
			t.Errorf("tags of %s = %v (%v)", n, p.Tags, err)
		}
		if p.Tags["title"] != "" {
			t.Errorf("a multi-file part got the book title: %v", p.Tags)
		}
	}
	rows := e.rows(book)
	if len(rows) != 2 {
		t.Fatalf("rows = %+v", rows)
	}
	for _, r := range rows {
		if r.State != library.WriteSwapped || r.AfterSHA256 == "" || r.AfterSHA256 == r.BeforeSHA256 {
			t.Errorf("row = %+v", r)
		}
	}
	if got := e.changedLibraries(); len(got) != 1 || got[0] != e.lib.ID {
		t.Errorf("OnChanged = %v", got)
	}
	// The scan never picks up the hidden names: the book keeps its two parts.
	e.scanAll()
	files, err := e.repo.AudioFiles(bg, e.book("Book").ID)
	if err != nil || len(files) != 2 {
		t.Errorf("after a scan: %d files, %v", len(files), err)
	}
	if left, _ := os.ReadDir(WritesDir(e.data)); len(left) != 0 {
		t.Errorf("chapter files left behind: %v", left)
	}
}

func TestFileWritesPreflight(t *testing.T) {
	cases := []struct {
		name  string
		code  string
		setup func(e *wenv, book library.Book)
		make  func(e *wenv) library.Book
	}{
		{name: "unsupported container", code: WriteUnsupported, make: func(e *wenv) library.Book {
			p := e.fake("Book/01.ogg", mediatest.FakeFile{Container: "ogg", Codec: "vorbis", PadBytes: 500})
			oldFile(e.t, p)
			e.scanAll()
			return e.book("Book")
		}},
		{name: "encrypted", code: WriteProtected, make: func(e *wenv) library.Book {
			p := e.fake("Book/01.m4b", mediatest.FakeFile{Encrypted: true, PadBytes: 500})
			oldFile(e.t, p)
			e.scanAll()
			return e.book("Book")
		}},
		{name: "missing", code: WriteMissing, setup: func(e *wenv, _ library.Book) {
			if err := os.Remove(e.path("Book/01.m4b")); err != nil {
				e.t.Fatal(err)
			}
		}},
		{name: "recent", code: WriteRecent, make: func(e *wenv) library.Book { return e.fakeBook("Book", 1, true) }},
		{name: "no space", code: WriteNoSpace, setup: func(e *wenv, _ library.Book) {
			e.w.free = func(string) (uint64, error) { return 10, nil }
		}},
		{name: "temp file present", code: WriteLeftover, setup: func(e *wenv, _ library.Book) {
			if err := os.WriteFile(e.path("Book/.01.m4b.tendshelf-new"), []byte("x"), 0o600); err != nil {
				e.t.Fatal(err)
			}
		}},
		{name: "backup present", code: WriteLeftover, setup: func(e *wenv, _ library.Book) {
			if err := os.WriteFile(e.path("Book/.01.m4b.tendshelf-before"), []byte("x"), 0o600); err != nil {
				e.t.Fatal(err)
			}
		}},
		{name: "open ledger row", code: WriteLeftover, setup: func(e *wenv, b library.Book) {
			row := library.FileWrite{JobID: "jb_x", BookID: b.ID, AudioFileID: "af_x", State: library.WriteSwapped,
				OriginalPath: e.path("Book/01.m4b"), BackupPath: "b", TempPath: "t", BeforeSHA256: "x", AudioHash: "y"}
			if err := e.repo.Update(bg, func(tx *library.Tx) error { return tx.InsertFileWrite(&row) }); err != nil {
				e.t.Fatal(err)
			}
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := newFakeWEnv(t)
			var book library.Book
			if c.make != nil {
				book = c.make(e)
			} else {
				book = e.fakeBook("Book", 1, false)
			}
			if c.setup != nil {
				c.setup(e, book)
			}
			rowsBefore := e.allRows()
			before := snapshot(t, e.path("Book"))
			refs, err := e.w.Preflight(bg, book.ID)
			if err != nil || len(refs) == 0 || refs[0].Code != c.code || refs[0].Text == "" {
				t.Fatalf("Preflight = %+v, %v; want code %s", refs, err, c.code)
			}
			_, err = e.w.Start(bg, book.ID, "")
			var ref *WriteRefusal
			if !errors.As(err, &ref) || ref.Code != c.code {
				t.Fatalf("Start error = %v, want code %s", err, c.code)
			}
			if got := e.allRows(); got != rowsBefore {
				t.Errorf("ledger rows %d -> %d", rowsBefore, got)
			}
			after := snapshot(t, e.path("Book"))
			if len(after) != len(before) {
				t.Errorf("folder changed: %v -> %v", names(before), names(after))
			}
			for n, h := range before {
				if after[n] != h {
					t.Errorf("%s changed", n)
				}
			}
			if e.enq.count(KindWriteDetails) != 0 || countJobs(t, e.env, "kind = ?", KindWriteDetails) != 0 {
				t.Error("a job was queued")
			}
		})
	}
}

func TestFileWritesVerifyFails(t *testing.T) {
	e := newFakeWEnv(t)
	book := e.fakeBook("Book", 1, false)
	orig := e.path("Book/01.m4b")
	before := fileSHA(t, orig)
	e.w.crash = func(point string) error {
		if point == "verifying" { // the copy's audio no longer matches
			tmp := e.path("Book/.01.m4b.tendshelf-new")
			fh, err := os.OpenFile(tmp, os.O_APPEND|os.O_WRONLY, 0)
			if err != nil {
				return err
			}
			defer fh.Close()
			_, err = fh.Write([]byte{1, 2, 3})
			return err
		}
		return nil
	}
	j, err := e.start(book)
	if err != nil || j.State != jobs.StateFailed || j.ErrorCode != VerifyAudio {
		t.Fatalf("job = %+v, %v", j, err)
	}
	rows := e.rows(book)
	if len(rows) != 1 || rows[0].State != library.WriteFailed || rows[0].ErrorCode != VerifyAudio {
		t.Fatalf("rows = %+v", rows)
	}
	if got := names(snapshot(t, e.path("Book"))); !sameStrings(got, []string{"01.m4b"}) {
		t.Errorf("folder = %v", got)
	}
	if fileSHA(t, orig) != before {
		t.Error("the original changed")
	}
	if len(e.changedLibraries()) != 0 {
		t.Error("OnChanged called though nothing changed")
	}
}

var errCrash = errors.New("injected crash")

func crashAt(point string) func(string) error {
	return func(p string) error {
		if p == point {
			return errCrash
		}
		return nil
	}
}

// layout reports which of the three paths of a file exist.
func layout(orig string) (hasOrig, hasBackup, hasTemp bool) {
	temp, backup := hiddenNames(orig)
	return present(orig), present(backup), present(temp)
}

func TestFileWritesCrash(t *testing.T) {
	cases := []struct {
		point string
		// phase says where the crash is injected: "write", "confirm" or "undo".
		phase string
		// what the row and the folder look like right after the crash
		crashState                  string
		crashOrig, crashBak, crashT bool
		// what recovery makes of it
		state, code        string
		orig, bak, tmp     bool
		origIsAfter        bool
		wantChanged, ffRun bool
	}{
		{point: "copied0", phase: "write", crashState: "copying", crashOrig: true,
			state: "failed", code: WriteInterrupted, orig: true},
		{point: "verifying", phase: "write", crashState: "verifying", crashOrig: true, crashT: true,
			state: "failed", code: WriteInterrupted, orig: true},
		{point: "swapping", phase: "write", crashState: "swapping", crashOrig: true, crashT: true,
			state: "failed", code: WriteInterrupted, orig: true},
		{point: "swap1", phase: "write", crashState: "swapping", crashBak: true, crashT: true,
			state: "failed", code: WriteInterrupted, orig: true},
		{point: "swap2", phase: "write", crashState: "swapping", crashOrig: true, crashBak: true,
			state: "swapped", orig: true, bak: true, origIsAfter: true},
		{point: "confirm1", phase: "confirm", crashState: "confirming", crashOrig: true, crashBak: true,
			state: "confirmed", orig: true, origIsAfter: true},
		{point: "undoing", phase: "undo", crashState: "undoing", crashOrig: true, crashBak: true,
			state: "undone", orig: true},
		{point: "undo1", phase: "undo", crashState: "undoing", crashBak: true, crashT: true,
			state: "undone", orig: true},
		{point: "undo2", phase: "undo", crashState: "undoing", crashOrig: true, crashT: true,
			state: "undone", orig: true},
	}
	for _, c := range cases {
		t.Run(c.point, func(t *testing.T) {
			e := newFakeWEnv(t)
			book := e.fakeBook("Book", 1, false)
			orig := e.path("Book/01.m4b")
			before := fileSHA(t, orig)

			if c.phase == "write" {
				e.w.crash = crashAt(c.point)
				j, err := e.start(book)
				if err != nil || j.State != jobs.StateFailed {
					t.Fatalf("job = %+v, %v", j, err)
				}
			} else {
				if j, err := e.start(book); err != nil || j.State != jobs.StateDone {
					t.Fatalf("job = %+v, %v", j, err)
				}
				e.w.crash = crashAt(c.point)
				var err error
				if c.phase == "confirm" {
					_, err = e.w.Confirm(bg, book.ID, "")
				} else {
					_, err = e.w.Undo(bg, book.ID, "")
				}
				if !errors.Is(err, errCrash) {
					t.Fatalf("operation error = %v", err)
				}
			}
			rows := e.rows(book)
			if len(rows) != 1 || rows[0].State != c.crashState {
				t.Fatalf("rows after the crash = %+v, want state %s", rows, c.crashState)
			}
			if o, b, tm := layout(orig); o != c.crashOrig || b != c.crashBak || tm != c.crashT {
				t.Fatalf("files after the crash: orig=%v backup=%v temp=%v", o, b, tm)
			}
			after := rows[0].AfterSHA256

			// A fresh process: new service, no crash hook, nothing active.
			w2 := NewWrites(e.repo, e.tools, e.data, e.r, e.r, slog.New(slog.NewTextHandler(io.Discard, nil)))
			var changed int
			w2.OnChanged = func(context.Context, string) { changed++ }
			runsBefore := e.ffmpegRuns()
			if err := w2.Recover(bg); err != nil {
				t.Fatalf("Recover: %v", err)
			}
			if got := e.ffmpegRuns(); got != runsBefore {
				t.Errorf("recovery ran ffmpeg (%d -> %d runs)", runsBefore, got)
			}
			rows = e.rows(book)
			if len(rows) != 1 || rows[0].State != c.state || rows[0].ErrorCode != c.code {
				t.Fatalf("rows after recovery = %+v, want %s/%q", rows, c.state, c.code)
			}
			if o, b, tm := layout(orig); o != c.orig || b != c.bak || tm != c.tmp {
				t.Errorf("files after recovery: orig=%v backup=%v temp=%v", o, b, tm)
			}
			want := before
			if c.origIsAfter {
				want = after
				if want == "" {
					t.Fatal("no after_sha256 was recorded")
				}
			}
			if got := fileSHA(t, orig); got != want {
				t.Errorf("original sha = %s, want %s (after=%v)", got, want, c.origIsAfter)
			}
			if changed != 1 {
				t.Errorf("OnChanged called %d times", changed)
			}
			// A second pass finds nothing to do.
			if err := w2.Recover(bg); err != nil {
				t.Fatal(err)
			}
			if again := e.rows(book); len(again) != 1 || again[0].State != c.state {
				t.Errorf("second pass changed the row: %+v", again)
			}
		})
	}
}

// fakeRunner answers Recover's questions about jobs.
type fakeRunner struct {
	state jobs.State
}

func (f *fakeRunner) Cancel(context.Context, string) error { return nil }
func (f *fakeRunner) Get(_ context.Context, id string) (jobs.Job, error) {
	return jobs.Job{ID: id, State: f.state}, nil
}
func (f *fakeRunner) ActiveFor(context.Context, string, string, string) (jobs.Job, bool, error) {
	return jobs.Job{}, false, nil
}

func TestFileWritesRecoverSkipsRunning(t *testing.T) {
	e := newFakeWEnv(t)
	book := e.fakeBook("Book", 1, false)
	orig := e.path("Book/01.m4b")
	temp, backup := hiddenNames(orig)
	if err := os.WriteFile(temp, []byte("half"), 0o600); err != nil {
		t.Fatal(err)
	}
	row := library.FileWrite{JobID: "jb_run", BookID: book.ID, AudioFileID: "af_x", State: library.WriteCopying,
		OriginalPath: orig, BackupPath: backup, TempPath: temp, BeforeSHA256: fileSHA(t, orig), AudioHash: "h"}
	if err := e.repo.Update(bg, func(tx *library.Tx) error { return tx.InsertFileWrite(&row) }); err != nil {
		t.Fatal(err)
	}
	fr := &fakeRunner{state: jobs.StateRunning}
	w := NewWrites(e.repo, e.tools, e.data, e.enq, fr, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := w.Recover(bg); err != nil {
		t.Fatal(err)
	}
	if rows := e.rows(book); len(rows) != 1 || rows[0].State != library.WriteCopying || !present(temp) {
		t.Fatalf("a running job's row was touched: %+v, temp=%v", rows, present(temp))
	}
	// A book this process is working on is skipped too, whatever the job says.
	fr.state = jobs.StateFailed
	end, err := w.begin(book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Recover(bg); err != nil {
		t.Fatal(err)
	}
	if rows := e.rows(book); rows[0].State != library.WriteCopying {
		t.Fatalf("an active book's row was touched: %+v", rows)
	}
	end()
	if err := w.Recover(bg); err != nil {
		t.Fatal(err)
	}
	if rows := e.rows(book); rows[0].State != library.WriteFailed || present(temp) {
		t.Fatalf("a finished job's row was not settled: %+v", rows)
	}
}

func TestFileWritesUndoChanged(t *testing.T) {
	e := newFakeWEnv(t)
	book := e.fakeBook("Book", 1, false)
	orig := e.path("Book/01.m4b")
	if j, err := e.start(book); err != nil || j.State != jobs.StateDone {
		t.Fatalf("job = %+v, %v", j, err)
	}
	_, backup := hiddenNames(orig)
	backupSHA := fileSHA(t, backup)
	f, err := os.OpenFile(orig, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.Write([]byte("edited elsewhere"))
	f.Close()
	changedSHA := fileSHA(t, orig)

	n, err := e.w.Undo(bg, book.ID, "")
	if n != 0 || !errors.Is(err, ErrNeedsAttention) {
		t.Fatalf("Undo = %d, %v", n, err)
	}
	rows := e.rows(book)
	if len(rows) != 1 || rows[0].State != library.WriteFailed || rows[0].ErrorCode != WriteAttention {
		t.Fatalf("rows = %+v", rows)
	}
	if fileSHA(t, orig) != changedSHA || fileSHA(t, backup) != backupSHA {
		t.Error("Undo touched the changed file or its backup")
	}
}

func TestFileWritesConfirmAndUndo(t *testing.T) {
	e := newFakeWEnv(t)
	book := e.fakeBook("Book", 2, false)
	dir := e.path("Book")
	before := snapshot(t, dir)

	if j, err := e.start(book); err != nil || j.State != jobs.StateDone {
		t.Fatalf("job = %+v, %v", j, err)
	}
	n, err := e.w.Undo(bg, book.ID, "")
	if err != nil || n != 2 {
		t.Fatalf("Undo = %d, %v", n, err)
	}
	after := snapshot(t, dir)
	if len(after) != len(before) {
		t.Fatalf("folder after Undo = %v", names(after))
	}
	for name, h := range before {
		if after[name] != h {
			t.Errorf("%s is not byte-identical after Undo", name)
		}
	}
	if len(e.changedLibraries()) != 2 { // once after the write, once after Undo
		t.Errorf("OnChanged = %v", e.changedLibraries())
	}

	// Write again, then "Looks good".
	if j, err := e.start(book); err != nil || j.State != jobs.StateDone {
		t.Fatalf("second job = %+v, %v", j, err)
	}
	n, err = e.w.Confirm(bg, book.ID, "")
	if err != nil || n != 2 {
		t.Fatalf("Confirm = %d, %v", n, err)
	}
	if got := names(snapshot(t, dir)); !sameStrings(got, []string{"Part 1.m4b", "Part 2.m4b"}) {
		t.Errorf("folder after Confirm = %v", got)
	}
	if n2, err := e.w.Confirm(bg, book.ID, ""); err != nil || n2 != 0 {
		t.Errorf("a second Confirm = %d, %v", n2, err)
	}
	var undone, confirmed int
	for _, r := range e.rows(book) {
		switch r.State {
		case library.WriteUndone:
			undone++
		case library.WriteConfirmed:
			confirmed++
		}
	}
	if undone != 2 || confirmed != 2 {
		t.Errorf("rows: %d undone, %d confirmed", undone, confirmed)
	}
	var audits int
	if err := e.st.Reader().QueryRow(`SELECT count(*) FROM audit_log WHERE action IN ('book.write', 'book.write.undo', 'book.write.confirm')`).Scan(&audits); err != nil || audits != 8 {
		t.Errorf("audit rows = %d, %v", audits, err)
	}
}

// TestFileWritesBusy: Confirm and Undo refuse while the book is being written in this process.
func TestFileWritesBusy(t *testing.T) {
	e := newFakeWEnv(t)
	book := e.fakeBook("Book", 1, false)
	end, err := e.w.begin(book.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer end()
	var ref *WriteRefusal
	if _, err := e.w.Confirm(bg, book.ID, ""); !errors.As(err, &ref) || ref.Code != WriteBusy {
		t.Errorf("Confirm = %v", err)
	}
	if _, err := e.w.Start(bg, book.ID, ""); !errors.As(err, &ref) || ref.Code != WriteBusy {
		t.Errorf("Start = %v", err)
	}
}

// chedUserChapters stores an edited list on a book (source user).
func chedUserChapters(t *testing.T, e *wenv, book library.Book, chs []library.Chapter) {
	t.Helper()
	if err := e.repo.Update(bg, func(tx *library.Tx) error { return tx.ReplaceChapters(book.ID, chs) }); err != nil {
		t.Fatal(err)
	}
}

func TestChapterEditWriteFit(t *testing.T) {
	e := newFakeWEnv(t)
	book := e.fakeBook("Solo", 1, false)
	chedUserChapters(t, e, book, []library.Chapter{
		{Idx: 0, StartMs: 0, EndMs: 3000, Title: "A", Source: "user"},
		{Idx: 1, StartMs: 3000, EndMs: book.DurationMs + 1001, Title: "B", Source: "user"}})
	refs, err := e.w.Preflight(bg, book.ID)
	if err != nil || len(refs) != 1 || refs[0].Code != WriteChapterFit {
		t.Fatalf("preflight = %+v, %v", refs, err)
	}
	if _, err := e.w.Start(bg, book.ID, ""); err == nil {
		t.Error("Start accepted a list that runs past the end")
	}
	// One second past is still fine.
	chedUserChapters(t, e, book, []library.Chapter{
		{Idx: 0, StartMs: 0, EndMs: 3000, Title: "A", Source: "user"},
		{Idx: 1, StartMs: 3000, EndMs: book.DurationMs + 1000, Title: "B", Source: "user"}})
	if refs, err := e.w.Preflight(bg, book.ID); err != nil || len(refs) != 0 {
		t.Errorf("preflight at one second = %+v, %v", refs, err)
	}
}

func TestChapterEditWriteCarriesTitles(t *testing.T) {
	e := newFakeWEnv(t)
	book := e.fakeBook("Solo", 1, false)
	chedUserChapters(t, e, book, []library.Chapter{
		{Idx: 0, StartMs: 0, EndMs: 2000, Title: "Mine one", Source: "user"},
		{Idx: 1, StartMs: 2000, EndMs: book.DurationMs, Title: "Mine two", Source: "user"}})
	j, err := e.start(book)
	if err != nil || j.State != jobs.StateDone {
		t.Fatalf("job = %+v, %v", j, err)
	}
	p, err := e.tools.Probe(bg, e.path("Solo/01.m4b"))
	if err != nil || len(p.Chapters) != 2 || p.Chapters[0].Title != "Mine one" || p.Chapters[1].Title != "Mine two" {
		t.Fatalf("written chapters = %+v, %v", p.Chapters, err)
	}
	e.scanAll()
	got, err := e.repo.Chapters(bg, book.ID)
	if err != nil || len(got) != 2 || got[0].Source != "user" || got[0].Title != "Mine one" {
		t.Errorf("after the rescan: %+v, %v", got, err)
	}
}
