package mediajobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

func (e *env) newPeaks(enabled bool) *Peaks {
	return NewPeaks(e.repo, e.tools, PeaksDir(e.data), e.enq, enabled, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func TestPeaksRunBuildsOnceAndRefuses(t *testing.T) {
	e := newEnv(t)
	e.fake("Ok/01.m4b", mediatest.FakeFile{DurationMs: 6000})
	e.fake("Enc/01.m4a", mediatest.FakeFile{Encrypted: true})
	if err := os.MkdirAll(e.path("Prot"), 0o755); err != nil {
		t.Fatal(err)
	}
	mediatest.WriteProtected(t, e.path("Prot/01.m4p"))
	e.scanAll()
	p := e.newPeaks(true)
	ok := e.file("Ok")

	if out, err := p.Run(bg, ok.ID, nil); err != nil || out != OutcomeBuilt {
		t.Fatalf("first Run = %q, %v", out, err)
	}
	path, found, err := p.Lookup(ok)
	if err != nil || !found || filepath.Base(path) != ok.QuickHash+"-v1.peaks" {
		t.Fatalf("Lookup = %q, %v, %v", path, found, err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	pk, err := media.DecodePeaks(b)
	if err != nil || len(pk.Values) != 12 { // the fake's default 6 s
		t.Fatalf("peaks = %d values, %v", len(pk.Values), err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
	runs := e.ffmpegRuns()
	if out, err := p.Run(bg, ok.ID, nil); err != nil || out != OutcomeReady || e.ffmpegRuns() != runs {
		t.Errorf("second Run = %q, %v, ffmpeg runs %d -> %d", out, err, runs, e.ffmpegRuns())
	}

	// Encrypted and copy-protected files are never decoded.
	for _, rel := range []string{"Enc", "Prot"} {
		f := e.file(rel)
		if _, err := p.Run(bg, f.ID, nil); mediaCode(t, err) != media.CodeDRM {
			t.Errorf("%s: err = %v", rel, err)
		}
		if _, err := p.Request(bg, f); mediaCode(t, err) != media.CodeDRM {
			t.Errorf("%s Request: err = %v", rel, err)
		}
	}
	if e.ffmpegRuns() != runs {
		t.Error("ffmpeg ran for a refused file")
	}
	if n := e.enq.count(KindPeaks); n != 0 {
		t.Errorf("%d jobs queued for refused files", n)
	}

	// A file that went missing and a row that is gone.
	if _, err := e.st.Writer().Exec(`UPDATE audio_files SET missing_since = 5 WHERE id = ?`, ok.ID); err != nil {
		t.Fatal(err)
	}
	if out, err := p.Run(bg, ok.ID, nil); err != nil || out != OutcomeMissing {
		t.Errorf("missing: %q, %v", out, err)
	}
	if out, err := p.Run(bg, "af_nope", nil); err != nil || out != OutcomeGone {
		t.Errorf("unknown: %q, %v", out, err)
	}
}

func TestPeaksLostLeaseWritesNothing(t *testing.T) {
	e := newEnv(t)
	e.fake("Ok/01.m4b", mediatest.FakeFile{})
	e.scanAll()
	p := e.newPeaks(true)
	lost := errors.New("lease lost")
	if _, err := p.Run(bg, e.file("Ok").ID, func(context.Context, *sql.Tx) error { return lost }); !errors.Is(err, lost) {
		t.Errorf("guard error = %v", err)
	}
	if got := listDir(t, PeaksDir(e.data)); len(got) != 0 {
		t.Errorf("cache after a lost lease = %v", got)
	}
}

func TestPeaksDisabled(t *testing.T) {
	e := newEnv(t)
	e.fake("Ok/01.m4b", mediatest.FakeFile{})
	e.scanAll()
	f := e.file("Ok")

	for name, p := range map[string]*Peaks{
		"flag off":   e.newPeaks(false),
		"no ffmpeg":  NewPeaks(e.repo, media.Tools{}, PeaksDir(e.data), e.enq, true, nil),
		"nil":        nil,
		"flag off 2": NewPeaks(e.repo, e.tools, PeaksDir(e.data), e.enq, false, nil),
	} {
		if p.Enabled() {
			t.Errorf("%s: enabled", name)
		}
		if p == nil {
			continue
		}
		if _, found, err := p.Lookup(f); found || err != nil {
			t.Errorf("%s: Lookup = %v, %v", name, found, err)
		}
		if queued, err := p.Request(bg, f); queued || err != nil {
			t.Errorf("%s: Request = %v, %v", name, queued, err)
		}
		if n, err := p.Backfill(bg); n != 0 || err != nil {
			t.Errorf("%s: Backfill = %d, %v", name, n, err)
		}
		p.Follower()(bg, []string{f.BookID})
	}
	if n := e.enq.count(KindPeaks); n != 0 {
		t.Errorf("%d jobs queued while off", n)
	}
	// A job that was queued before the switch fails at once with the plain sentence.
	off := e.newPeaks(false)
	payload, _ := json.Marshal(PeaksPayload{AudioFileID: f.ID})
	_, err := off.handle(bg, &jobs.Task{Payload: payload})
	var je *jobs.Error
	if !errors.As(err, &je) || je.Code != "disabled" || !je.Permanent || je.Text == "" {
		t.Errorf("handle while off = %v", err)
	}
	if e.ffmpegRuns() != 0 {
		t.Error("ffmpeg ran while off")
	}
}

func TestPeaksRequest(t *testing.T) {
	e := newEnv(t)
	e.fake("Ok/01.m4b", mediatest.FakeFile{})
	e.scanAll()
	p := e.newPeaks(true)
	f := e.file("Ok")

	if queued, err := p.Request(bg, f); !queued || err != nil {
		t.Fatalf("Request = %v, %v", queued, err)
	}
	if n := e.enq.count(KindPeaks); n != 1 || !e.enq.opts[len(e.enq.opts)-1].NotBefore.IsZero() {
		t.Errorf("%d jobs, options %+v; want one with no delay", n, e.enq.opts)
	}
	// A waiting job is moved to the front, not queued twice.
	if queued, err := p.Request(bg, f); !queued || err != nil {
		t.Fatalf("second Request = %v, %v", queued, err)
	}
	if n := e.enq.count(KindPeaks); n != 1 {
		t.Errorf("%d jobs after a second request", n)
	}
	if len(e.enq.expedited) != 1 || e.enq.expedited[0] != "jb_"+f.ID {
		t.Errorf("expedited = %v", e.enq.expedited)
	}
	// Built: nothing to wait for.
	if _, err := p.Run(bg, f.ID, nil); err != nil {
		t.Fatal(err)
	}
	if queued, err := p.Request(bg, f); queued || err != nil {
		t.Errorf("Request for built peaks = %v, %v", queued, err)
	}
}

func TestPeaksFollowerAndBackfill(t *testing.T) {
	e := newEnv(t)
	e.fake("A/01.m4b", mediatest.FakeFile{})
	e.fake("B/01.m4b", mediatest.FakeFile{})
	e.fake("C/01.m4b", mediatest.FakeFile{})
	e.fake("Enc/01.m4a", mediatest.FakeFile{Encrypted: true})
	e.scanAll()
	p := e.newPeaks(true)
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return now }
	a, b, c, enc := e.file("A"), e.file("B"), e.file("C"), e.file("Enc")

	// A has its peaks, B has a waiting job, Enc is refused: only C is queued, at leisure.
	if _, err := p.Run(bg, a.ID, nil); err != nil {
		t.Fatal(err)
	}
	e.enq.active = map[string]map[string]bool{KindPeaks: {b.ID: true}}
	p.Follower()(bg, []string{a.BookID, b.BookID, c.BookID, enc.BookID})
	var queued []string
	for i, kind := range e.enq.kinds {
		if kind == KindPeaks {
			queued = append(queued, e.enq.ids[i])
			if !e.enq.opts[i].NotBefore.Equal(now) {
				t.Errorf("NotBefore = %v, want %v", e.enq.opts[i].NotBefore, now)
			}
		}
	}
	if len(queued) != 1 || queued[0] != c.ID {
		t.Fatalf("queued %v, want only %s", queued, c.ID)
	}

	// Backfill: the same rule over every probed file; C now has a waiting job too.
	if n, err := p.Backfill(bg); err != nil || n != 0 {
		t.Errorf("Backfill = %d, %v; want 0", n, err)
	}
	e.enq.active = nil
	if n, err := p.Backfill(bg); err != nil || n != 2 { // B and C; A is built
		t.Errorf("Backfill = %d, %v; want 2", n, err)
	}
}

func TestPeaksSweep(t *testing.T) {
	e := newEnv(t)
	e.fake("Live/01.m4b", mediatest.FakeFile{})
	e.fake("Gone/01.m4b", mediatest.FakeFile{})
	e.scanAll()
	p := e.newPeaks(true)
	live, gone := e.file("Live"), e.file("Gone")
	for _, f := range []library.AudioFile{live, gone} {
		if _, err := p.Run(bg, f.ID, nil); err != nil {
			t.Fatal(err)
		}
	}
	// The row of a missing file keeps its peaks, so a restored file needs no rebuild.
	if _, err := e.st.Writer().Exec(`UPDATE audio_files SET missing_since = 5 WHERE id = ?`, gone.ID); err != nil {
		t.Fatal(err)
	}
	dir := PeaksDir(e.data)
	stale := []string{
		strings.Repeat("a", 64) + "-v1.peaks", // a hash no row has
		live.QuickHash + "-v0.peaks",          // an older format
		live.QuickHash + "-v2.peaks",          // a newer format
	}
	other := []string{"notes.txt", strings.Repeat("b", 63) + "-v1.peaks"} // not ours: left alone
	for _, n := range append(append([]string{}, stale...), other...) {
		if err := os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o640); err != nil {
			t.Fatal(err)
		}
	}
	if err := p.Sweep(bg); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(listDir(t, dir), " ")
	for _, n := range stale {
		if strings.Contains(got, n) {
			t.Errorf("%s survived the sweep", n)
		}
	}
	for _, n := range append(other, live.QuickHash+"-v1.peaks", gone.QuickHash+"-v1.peaks") {
		if !strings.Contains(got, n) {
			t.Errorf("%s was removed; dir = %s", n, got)
		}
	}

	// A hash set that cannot be read removes nothing.
	if _, err := e.st.Writer().Exec(`DROP TABLE audio_files`); err != nil {
		t.Skipf("cannot break the table: %v", err)
	}
	before := listDir(t, dir)
	if err := p.Sweep(bg); err == nil {
		t.Error("Sweep succeeded without audio_files")
	}
	if after := listDir(t, dir); strings.Join(after, " ") != strings.Join(before, " ") {
		t.Errorf("sweep removed files without knowing the hashes: %v -> %v", before, after)
	}
}

func TestPeaksStaleTempFilesAreSwept(t *testing.T) {
	e := newEnv(t)
	dir := PeaksDir(e.data)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, ".tmp-old")
	if err := os.WriteFile(old, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(old, past, past); err != nil {
		t.Fatal(err)
	}
	e.newPeaks(true)
	if got := listDir(t, dir); len(got) != 0 {
		t.Errorf("leftover temp files = %v", got)
	}
}

// TestPeaksJobsEndToEnd runs the follower and the job on a real runner: new books get peaks, a
// refused one does not, and a request for a book that has none is answered by the same job kind.
func TestPeaksJobsEndToEnd(t *testing.T) {
	tools := mediatest.FakeTools(t)
	e, r, _, _ := jobsEnv(t, tools, "")
	e.sc.OnBookSettled(e.peaks.Follower())
	e.fake("A/01.m4b", mediatest.FakeFile{})
	e.fake("B/01.m4b", mediatest.FakeFile{Encrypted: true})
	if _, err := r.Enqueue(bg, scan.KindScan, scan.ScanPayload{LibraryID: e.lib.ID}, jobs.EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	idle(t, e)

	if n := countJobs(t, e, `kind = ? AND state = 'done'`, KindPeaks); n != 1 {
		t.Errorf("%d peaks jobs done, want 1", n)
	}
	if n := countJobs(t, e, `kind = ?`, KindPeaks); n != 1 {
		t.Errorf("%d peaks jobs in all, want 1 (the encrypted book is refused)", n)
	}
	if _, found, err := e.peaks.Lookup(e.file("A")); err != nil || !found {
		t.Errorf("A has no peaks: %v, %v", found, err)
	}

	// Deleting the cache loses nothing: a request queues a job at the front, and it rebuilds.
	if err := os.RemoveAll(PeaksDir(e.data)); err != nil {
		t.Fatal(err)
	}
	if queued, err := e.peaks.Request(bg, e.file("A")); err != nil || !queued {
		t.Fatalf("Request = %v, %v", queued, err)
	}
	idle(t, e)
	if _, found, _ := e.peaks.Lookup(e.file("A")); !found {
		t.Error("the requested peaks were not rebuilt")
	}
	if n, err := e.peaks.Backfill(bg); err != nil || n != 0 {
		t.Errorf("Backfill after the rebuild = %d, %v", n, err)
	}
}
