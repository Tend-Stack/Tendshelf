package mediajobs

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/media/mediatest"
)

var opus = mediatest.FakeFile{Container: "ogg", Codec: "opus", CodecTag: "opus", DurationMs: 7000}

func (e *env) compat(capBytes int64) *Compat {
	return NewCompat(e.repo, e.tools, CompatDir(e.data), capBytes, e.enq, nil)
}

// file returns the only audio file row of the book at rel.
func (e *env) file(rel string) library.AudioFile {
	e.t.Helper()
	fs, err := e.repo.AudioFiles(bg, e.book(rel).ID)
	if err != nil || len(fs) == 0 {
		e.t.Fatalf("files of %s: %v, %v", rel, fs, err)
	}
	return fs[0]
}

func mediaCode(t *testing.T, err error) string {
	t.Helper()
	var me *media.Error
	if !errors.As(err, &me) {
		t.Fatalf("err = %v (%T), want *media.Error", err, err)
	}
	return me.Code
}

func TestCompatRequestRefusals(t *testing.T) {
	e := newEnv(t)
	e.fake("Mp3/01.mp3", mediatest.FakeFile{Container: "mp3", Codec: "mp3", CodecTag: "mp3"})
	e.fake("Enc/01.m4a", mediatest.FakeFile{Encrypted: true})
	if err := os.MkdirAll(e.path("Prot"), 0o755); err != nil {
		t.Fatal(err)
	}
	mediatest.WriteProtected(t, e.path("Prot/01.aax"))
	e.fake("Opus/01.opus", opus)
	e.scanAll()
	c := e.compat(1 << 20)

	for rel, want := range map[string]string{"Mp3": media.CodeCompatNotNeeded, "Enc": media.CodeDRM, "Prot": media.CodeDRM} {
		_, err := c.Request(bg, e.file(rel).ID)
		if got := mediaCode(t, err); got != want {
			t.Errorf("%s: code %s, want %s", rel, got, want)
		}
	}
	if _, err := c.Request(bg, "af_nope"); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("unknown file: %v", err)
	}
	if n := e.enq.count(KindCompat); n != 0 {
		t.Errorf("%d jobs queued for refused files", n)
	}
	// Run refuses the same files, so a stale job cannot convert them.
	for _, rel := range []string{"Enc", "Prot", "Mp3"} {
		if _, err := c.Run(bg, e.file(rel).ID, nil); err == nil {
			t.Errorf("Run converted %s", rel)
		}
	}
	if e.ffmpegRuns() != 0 {
		t.Error("ffmpeg ran for a refused file")
	}

	id := e.file("Opus").ID
	if job, err := c.Request(bg, id); err != nil || job == "" {
		t.Fatalf("Request = %q, %v", job, err)
	}
	if job, err := c.Request(bg, id); err != nil || job != "" {
		t.Errorf("second Request = %q, %v; want nothing new", job, err)
	}
	if n := e.enq.count(KindCompat); n != 1 {
		t.Errorf("%d jobs queued, want 1", n)
	}
}

func TestCompatRunLookupAndReady(t *testing.T) {
	e := newEnv(t)
	e.fake("Opus/01.opus", opus)
	e.scanAll()
	c := e.compat(1 << 20)
	f := e.file("Opus")

	if p, ok, err := c.Lookup(bg, f.ID); err != nil || ok || p != "" {
		t.Fatalf("Lookup before = %q, %v, %v", p, ok, err)
	}
	if out, err := c.Run(bg, f.ID, nil); err != nil || out != OutcomeBuilt {
		t.Fatalf("Run = %q, %v", out, err)
	}
	want := filepath.Join(CompatDir(e.data), f.QuickHash+"-r1.m4a")
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(want, old, old); err != nil {
		t.Fatal(err)
	}
	p, ok, err := c.Lookup(bg, f.ID)
	if err != nil || !ok || p != want {
		t.Fatalf("Lookup = %q, %v, %v; want %s", p, ok, err, want)
	}
	if fi, _ := os.Stat(want); time.Since(fi.ModTime()) > time.Hour {
		t.Errorf("Lookup did not mark the copy as used: %s", fi.ModTime())
	}
	if job, err := c.Request(bg, f.ID); err != nil || job != "" {
		t.Errorf("Request of a ready copy = %q, %v", job, err)
	}
	runs := e.ffmpegRuns()
	if out, err := c.Run(bg, f.ID, nil); err != nil || out != OutcomeReady || e.ffmpegRuns() != runs {
		t.Errorf("second Run = %q, %v, ffmpeg %d -> %d", out, err, runs, e.ffmpegRuns())
	}
	if got := listDir(t, CompatDir(e.data)); len(got) != 1 {
		t.Errorf("cache = %v", got)
	}
	if out, err := c.Run(bg, "af_nope", nil); err != nil || out != OutcomeGone {
		t.Errorf("gone: %q, %v", out, err)
	}
}

func TestCompatEviction(t *testing.T) {
	e := newEnv(t)
	for _, n := range []string{"A", "B", "C"} {
		e.fake(n+"/01.opus", opus)
	}
	e.scanAll()
	c := e.compat(1 << 20)
	tick := time.Now().Add(-time.Hour)
	c.now = func() time.Time { tick = tick.Add(time.Minute); return tick }

	if _, err := c.Run(bg, e.file("A").ID, nil); err != nil {
		t.Fatal(err)
	}
	a := filepath.Join(CompatDir(e.data), e.file("A").QuickHash+"-r1.m4a")
	fi, err := os.Stat(a)
	if err != nil {
		t.Fatal(err)
	}
	size := fi.Size()
	c.capBytes = size*2 + size/2 // room for two copies

	// A copy of another recipe goes at the next conversion.
	otherRecipe := filepath.Join(CompatDir(e.data), strings.Repeat("a", 64)+"-r0.m4a")
	stray := filepath.Join(CompatDir(e.data), "notes.txt")
	for _, p := range []string{otherRecipe, stray} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.Run(bg, e.file("B").ID, nil); err != nil {
		t.Fatal(err)
	}
	if exists(otherRecipe) || !exists(stray) {
		t.Errorf("other recipe kept: %v, unrelated file kept: %v", exists(otherRecipe), exists(stray))
	}
	if !exists(a) {
		t.Fatal("A evicted while the cache still fits")
	}

	// A third copy passes the cap: the least recently used (A) goes, the newest stays.
	if _, err := c.Run(bg, e.file("C").ID, nil); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(CompatDir(e.data), e.file("B").QuickHash+"-r1.m4a")
	cc := filepath.Join(CompatDir(e.data), e.file("C").QuickHash+"-r1.m4a")
	if exists(a) || !exists(b) || !exists(cc) {
		t.Errorf("after eviction: A %v, B %v, C %v; want false, true, true", exists(a), exists(b), exists(cc))
	}

	// Using B makes C the oldest; with a cap of one copy, the newest conversion is never removed.
	if _, ok, _ := c.Lookup(bg, e.file("B").ID); !ok {
		t.Fatal("B not found")
	}
	c.capBytes = 1
	if err := os.Remove(cc); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Run(bg, e.file("A").ID, nil); err != nil {
		t.Fatal(err)
	}
	if !exists(a) || exists(b) {
		t.Errorf("a cap below one copy: A %v (want kept), B %v (want evicted)", exists(a), exists(b))
	}
}

func TestCompatRejectsBadOutputAndLostLease(t *testing.T) {
	e := newEnv(t)
	e.fake("Opus/01.opus", opus)
	e.scanAll()
	c := e.compat(1 << 20)
	f := e.file("Opus")

	// The row says 20 s but the fake converts 7 s: the length check refuses the copy.
	if _, err := e.st.Writer().Exec(`UPDATE audio_files SET duration_ms = 20000 WHERE id = ?`, f.ID); err != nil {
		t.Fatal(err)
	}
	_, err := c.Run(bg, f.ID, nil)
	if got := mediaCode(t, err); got != media.CodeFFmpegFailed {
		t.Errorf("code = %s", got)
	}
	if got := listDir(t, CompatDir(e.data)); len(got) != 0 {
		t.Errorf("cache after a rejected copy = %v", got)
	}

	if _, err := e.st.Writer().Exec(`UPDATE audio_files SET duration_ms = 7000 WHERE id = ?`, f.ID); err != nil {
		t.Fatal(err)
	}
	lost := errors.New("lease lost")
	if _, err := c.Run(bg, f.ID, func(context.Context, *sql.Tx) error { return lost }); !errors.Is(err, lost) {
		t.Errorf("guard error = %v", err)
	}
	if got := listDir(t, CompatDir(e.data)); len(got) != 0 {
		t.Errorf("cache after a lost lease = %v", got)
	}

	// A failing ffmpeg is retryable; a missing one is not.
	if je := jobError(&media.Error{Code: media.CodeFFmpegFailed}); isPermanent(je) {
		t.Error("ffmpeg_failed should be retried")
	}
	for _, code := range []string{media.CodeFFmpegMissing, media.CodeDRM} {
		if je := jobError(&media.Error{Code: code}); !isPermanent(je) {
			t.Errorf("%s should fail at once", code)
		}
	}
	if isPermanent(jobError(&media.Error{Code: media.CodeFFmpegTimeout})) {
		t.Error("ffmpeg_timeout should be retried")
	}
}

func isPermanent(err error) bool {
	var je *jobs.Error
	return errors.As(err, &je) && je.Permanent
}
