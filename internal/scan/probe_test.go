package scan

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

func probeOne(t *testing.T, e *env, path string) (ProbeOutcome, error) {
	t.Helper()
	return e.sc.ProbeFile(bg, e.fileAt(path).ID, nil)
}

func asJobError(t *testing.T, err error) *jobs.Error {
	t.Helper()
	var je *jobs.Error
	if !errors.As(err, &je) {
		t.Fatalf("err = %v, want a *jobs.Error", err)
	}
	return je
}

func TestProbeSuccessRecordsAndFinalises(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	fx := newFixture(t, e.root)
	p := fx.file("Au/B/b.m4b", mediatest.FakeFile{DurationMs: 7000, Chapters: []mediatest.FakeChapter{
		mediatest.Chapter(0, 3000, "One"), mediatest.Chapter(3000, 7000, "Two")}})
	e.scan()
	out, err := probeOne(t, e, p)
	if err != nil || out != OutcomeProbed {
		t.Fatalf("outcome = %q, %v", out, err)
	}
	f := e.fileAt(p)
	if f.ProbeJSON == nil || f.Container != "mp4" || f.Codec != "aac" || f.DurationMs != 7000 || f.Channels != 1 || f.SampleRate != 22050 {
		t.Fatalf("row = %+v", f)
	}
	b := e.bookAt(fx.abs("Au/B"))
	if b.DurationMs != 7000 {
		t.Fatalf("book = %+v", b)
	}
	chs, _ := e.repo.Chapters(bg, b.ID)
	if len(chs) != 2 || chs[1].StartMs != 3000 || chs[1].Source != library.ChapterFile || chs[1].Title != "Two" {
		t.Fatalf("chapters = %+v", chs)
	}
	if out, err := probeOne(t, e, p); err != nil || out != OutcomeSettled {
		t.Fatalf("second probe = %q, %v", out, err)
	}
}

func TestProbeFinalisesOnlyAfterTheLastFile(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	fx := newFixture(t, e.root)
	var paths []string
	for _, n := range []string{"01.mp3", "02.mp3", "03.mp3"} {
		paths = append(paths, fx.file("Au/B/"+n, mp3))
	}
	e.scan()
	root := fx.abs("Au/B")
	for i, p := range paths {
		if _, err := probeOne(t, e, p); err != nil {
			t.Fatal(err)
		}
		b := e.bookAt(root)
		if i < 2 && b.DurationMs != 0 {
			t.Fatalf("book finalised after %d of 3 probes: %+v", i+1, b)
		}
	}
	b := e.bookAt(root)
	want := fx.dur[paths[0]] + fx.dur[paths[1]] + fx.dur[paths[2]]
	if b.DurationMs != want || b.RefusedReason != "" {
		t.Fatalf("book = %+v, want duration %d", b, want)
	}
	chs, _ := e.repo.Chapters(bg, b.ID)
	if len(chs) != 3 || chs[0].Source != library.ChapterFiles || chs[2].EndMs != want {
		t.Fatalf("chapters = %+v", chs)
	}
}

func TestProbeOutcomesPerMediaError(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	fx := newFixture(t, e.root)
	drm := fx.file("Drm/x.m4b", mediatest.FakeFile{Encrypted: true})
	bad := fx.abs("Bad/x.mp3")
	if err := os.MkdirAll(filepath.Dir(bad), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(bad, []byte("this is not audio"), 0o644); err != nil {
		t.Fatal(err)
	}
	gone := fx.file("Gone/x.m4b", mediatest.FakeFile{})
	e.scan()

	t.Run("drm", func(t *testing.T) {
		out, err := probeOne(t, e, drm)
		if err != nil || out != "drm" {
			t.Fatalf("outcome = %q, %v", out, err)
		}
		f := e.fileAt(drm)
		if !f.Encrypted || f.ProbeError != "drm" || f.ProbeJSON == nil {
			t.Fatalf("row = %+v", f)
		}
		if b := e.bookAt(fx.abs("Drm")); b.RefusedReason != library.RefusedDRM || b.DurationMs != 0 {
			t.Fatalf("book = %+v", b)
		}
	})

	t.Run("not audio is permanent and refuses the book", func(t *testing.T) {
		out, err := probeOne(t, e, bad)
		je := asJobError(t, err)
		if out != "not_audio" || je.Code != "not_audio" || !je.Permanent || je.Text != media.ErrorText(media.CodeNotAudio) {
			t.Fatalf("outcome %q, error %+v", out, je)
		}
		if f := e.fileAt(bad); f.ProbeError != "not_audio" || f.ProbeJSON != nil {
			t.Fatalf("row = %+v", f)
		}
		b := e.bookAt(fx.abs("Bad"))
		if b.RefusedReason != library.RefusedDamaged || RefusalText(b.RefusedReason) != media.ErrorText(media.CodeNotAudio) {
			t.Fatalf("book = %+v", b)
		}
	})

	t.Run("a missing file is marked missing", func(t *testing.T) {
		if err := os.Remove(gone); err != nil {
			t.Fatal(err)
		}
		out, err := probeOne(t, e, gone)
		if err != nil || out != "file_missing" {
			t.Fatalf("outcome = %q, %v", out, err)
		}
		if f := e.fileAt(gone); f.MissingSince == 0 || f.ProbeJSON != nil {
			t.Fatalf("row = %+v", f)
		}
		if out, err := probeOne(t, e, gone); err != nil || out != OutcomeMissing {
			t.Fatalf("second probe = %q, %v", out, err)
		}
	})
}

func TestProbeRetryableAndPermanentToolErrors(t *testing.T) {
	slow := mediatest.FakeTools(t)
	slow.ProbeTimeout = 150 * time.Millisecond
	e := newEnv(t, slow)
	fx := newFixture(t, e.root)
	p := fx.file("Au/B/x.m4b", mediatest.FakeFile{SleepMs: 2000})
	e.scan()
	_, err := probeOne(t, e, p)
	je := asJobError(t, err)
	if je.Code != media.CodeProbeTimeout || je.Permanent || je.Text != media.ErrorText(media.CodeProbeTimeout) {
		t.Fatalf("timeout error = %+v", je)
	}
	if f := e.fileAt(p); f.ProbeJSON != nil || f.ProbeError != "" {
		t.Fatalf("a retryable failure must leave the file unsettled: %+v", f)
	}

	none := newEnv(t, media.Tools{})
	fx2 := newFixture(t, none.root)
	q := fx2.file("Au/B/x.m4b", mediatest.FakeFile{})
	res := none.scan()
	if res.BooksAdded != 1 || res.ProbesEnqueued != 1 {
		t.Fatalf("books are still grouped without ffprobe: %+v", res)
	}
	_, err = probeOne(t, none, q)
	je = asJobError(t, err)
	if je.Code != media.CodeFFprobeMissing || !je.Permanent {
		t.Fatalf("missing ffprobe error = %+v", je)
	}
}

func TestProbeDiscardsStaleResult(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	fx := newFixture(t, e.root)
	p := fx.file("Au/B/x.m4b", mediatest.FakeFile{})
	e.scan()
	e.sc.afterProbe = func() { // the file is replaced while ffprobe was reading it
		mediatest.WriteFake(t, p, mediatest.FakeFile{DurationMs: 9999, PadBytes: 5})
	}
	before := e.changes()
	out, err := probeOne(t, e, p)
	e.sc.afterProbe = nil
	if err != nil || out != OutcomeStale {
		t.Fatalf("outcome = %q, %v", out, err)
	}
	if e.changes() != before {
		t.Fatal("a stale probe wrote rows")
	}
	if f := e.fileAt(p); f.ProbeJSON != nil {
		t.Fatalf("stale result stored: %+v", f)
	}
	// The next scan sees the change and the file is probed afresh.
	res := e.scan()
	if res.FilesChanged != 1 || res.ProbesEnqueued != 1 {
		t.Fatalf("result = %+v", res)
	}
	e.probeAll()
	if b := e.bookAt(fx.abs("Au/B")); b.DurationMs != 9999 {
		t.Fatalf("book = %+v", b)
	}
}

func TestProbeStaleWhenDatabaseRowChangedMeanwhile(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	fx := newFixture(t, e.root)
	p := fx.file("Au/B/x.m4b", mediatest.FakeFile{})
	e.scan()
	id := e.fileAt(p).ID
	e.sc.afterProbe = func() { // a scan marks the file missing between ffprobe and the write
		if _, err := e.st.Writer().Exec(`UPDATE audio_files SET missing_since = 5 WHERE id = ?`, id); err != nil {
			t.Error(err)
		}
	}
	out, err := e.sc.ProbeFile(bg, id, nil)
	e.sc.afterProbe = nil
	if err != nil || out != OutcomeStale {
		t.Fatalf("outcome = %q, %v", out, err)
	}
	if f := e.fileAt(p); f.ProbeJSON != nil {
		t.Fatalf("probe stored for a missing file: %+v", f)
	}
}

func TestProbeLostLeaseWritesNothing(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	fx := newFixture(t, e.root)
	p := fx.file("Au/B/x.m4b", mediatest.FakeFile{})
	e.scan()
	id := e.fileAt(p).ID
	before := e.changes()
	_, err := e.sc.ProbeFile(bg, id, func(context.Context, *sql.Tx) error { return jobs.ErrLeaseLost })
	if !errors.Is(err, jobs.ErrLeaseLost) {
		t.Fatalf("err = %v", err)
	}
	if e.changes() != before {
		t.Fatal("the probe wrote after losing its lease")
	}
	if f := e.fileAt(p); f.ProbeJSON != nil {
		t.Fatalf("row = %+v", f)
	}
}

func TestProbeGoneAndCancelled(t *testing.T) {
	e := newEnv(t, mediatest.FakeTools(t))
	if out, err := e.sc.ProbeFile(bg, "af_nope", nil); err != nil || out != OutcomeGone {
		t.Fatalf("outcome = %q, %v", out, err)
	}
	fx := newFixture(t, e.root)
	p := fx.file("Au/B/x.m4b", mediatest.FakeFile{})
	e.scan()
	ctx, cancel := context.WithCancel(bg)
	cancel()
	_, err := e.sc.ProbeFile(ctx, e.fileAt(p).ID, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the context error unchanged", err)
	}
	if f := e.fileAt(p); f.ProbeJSON != nil || f.ProbeError != "" {
		t.Fatalf("row = %+v", f)
	}
}
