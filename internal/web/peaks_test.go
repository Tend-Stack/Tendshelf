package web

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
)

// peaksAttach gives the app an enabled waveform service on its real (never started) job runner.
// The runner only needs the media.peaks kind registered so that jobs can be queued; no job runs,
// and the ffmpeg path is never executed.
func peaksAttach(t *testing.T, a *testApp) *mediajobs.Peaks {
	t.Helper()
	err := a.runner.Register(jobs.Kind{Name: mediajobs.KindPeaks, Concurrency: 1, MaxAttempts: 2,
		Handler: func(context.Context, *jobs.Task) (any, error) { return nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	p := mediajobs.NewPeaks(a.lib, media.Tools{FFmpeg: "/nonexistent/ffmpeg"}, mediajobs.PeaksDir(a.dir), a.runner, true, nil)
	a.srv.peaks = p
	return p
}

// peaksWrite stores a valid peaks file for f where the service looks for it and returns its bytes.
func peaksWrite(t *testing.T, a *testApp, f library.AudioFile) []byte {
	t.Helper()
	b := media.Peaks{BinMs: media.PeaksBinMs, Values: []byte{0, 40, 170, 255, 90, 12}}.Encode()
	dir := mediajobs.PeaksDir(a.dir)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, f.QuickHash+"-v1.peaks"), b, 0o640); err != nil {
		t.Fatal(err)
	}
	return b
}

func peaksJobCount(t *testing.T, a *testApp) int {
	t.Helper()
	var n int
	if err := a.st.Reader().QueryRow(`SELECT count(*) FROM jobs WHERE kind = ?`, mediajobs.KindPeaks).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestPeaksServesFile(t *testing.T) {
	a, owner, _, _, _, files, _ := streamEnvWith(t, false)
	peaksAttach(t, a)
	want := peaksWrite(t, a, files[0])

	code, h, body := streamGet(a, http.MethodGet, "/peaks/"+files[0].ID, nil, owner)
	if code != http.StatusOK || !bytes.Equal(body, want) {
		t.Fatalf("GET: %d, %d bytes, want %d", code, len(body), len(want))
	}
	for k, v := range map[string]string{
		"Content-Type": "application/octet-stream", "ETag": `"pk1-` + files[0].QuickHash + `"`, "Cache-Control": "private, no-cache",
	} {
		if h.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, h.Get(k), v)
		}
	}
	// The same tag back is a 304 with no body; HEAD has the length and no body.
	code, _, body = streamGet(a, http.MethodGet, "/peaks/"+files[0].ID, map[string]string{"If-None-Match": h.Get("ETag")}, owner)
	if code != http.StatusNotModified || len(body) != 0 {
		t.Errorf("If-None-Match: %d, %d bytes", code, len(body))
	}
	code, h, body = streamGet(a, http.MethodHead, "/peaks/"+files[0].ID, nil, owner)
	if code != http.StatusOK || len(body) != 0 || h.Get("Content-Length") != "22" {
		t.Errorf("HEAD: %d, %d bytes, length %q", code, len(body), h.Get("Content-Length"))
	}
	if n := peaksJobCount(t, a); n != 0 {
		t.Errorf("%d jobs queued for peaks that exist", n)
	}
}

func TestPeaksNotReadyQueuesOneJob(t *testing.T) {
	a, owner, member, _, _, files, _ := streamEnvWith(t, false)
	peaksAttach(t, a)
	path := "/peaks/" + files[1].ID

	// The member has no grant: the same 404, and nothing queued on their behalf.
	if code, _, _ := streamGet(a, http.MethodGet, path, nil, member); code != http.StatusNotFound {
		t.Fatalf("member without a grant: %d", code)
	}
	if n := peaksJobCount(t, a); n != 0 {
		t.Fatalf("%d jobs queued by a person who may not listen", n)
	}
	for i := 0; i < 2; i++ {
		code, h, _ := streamGet(a, http.MethodGet, path, nil, owner)
		if code != http.StatusNotFound || h.Get("Retry-After") != "30" {
			t.Errorf("GET %d: %d, Retry-After %q", i, code, h.Get("Retry-After"))
		}
	}
	if n := peaksJobCount(t, a); n != 1 {
		t.Errorf("%d jobs after two requests, want 1", n)
	}
	var notBefore int64
	if err := a.st.Reader().QueryRow(`SELECT not_before FROM jobs WHERE kind = ?`, mediajobs.KindPeaks).Scan(&notBefore); err != nil || notBefore != 0 {
		t.Errorf("a request from playback queues with not_before %d, %v; want 0", notBefore, err)
	}
}

func TestPeaksRequestExpeditesAQueuedBackfillJob(t *testing.T) {
	a, owner, _, _, _, files, _ := streamEnvWith(t, false)
	peaksAttach(t, a)
	id, err := a.runner.Enqueue(context.Background(), mediajobs.KindPeaks, mediajobs.PeaksPayload{AudioFileID: files[1].ID},
		jobs.EnqueueOptions{NotBefore: time.Now().Add(time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	code, h, _ := streamGet(a, http.MethodGet, "/peaks/"+files[1].ID, nil, owner)
	if code != http.StatusNotFound || h.Get("Retry-After") != "30" {
		t.Errorf("GET: %d, Retry-After %q", code, h.Get("Retry-After"))
	}
	if n := peaksJobCount(t, a); n != 1 {
		t.Errorf("%d jobs, want the one that was waiting", n)
	}
	var notBefore int64
	if err := a.st.Reader().QueryRow(`SELECT not_before FROM jobs WHERE id = ?`, id).Scan(&notBefore); err != nil || notBefore != 0 {
		t.Errorf("not_before = %d, %v; want 0", notBefore, err)
	}
}

func TestPeaksRefusals(t *testing.T) {
	a, owner, _, _, book, files, _ := streamEnvWith(t, false)
	peaksAttach(t, a)
	for _, f := range files {
		peaksWrite(t, a, f)
	}
	opens := streamOpens(t)

	for name, prep := range map[string]func(){
		"unknown file":  nil,
		"missing file":  func() { streamExec(t, a, `UPDATE audio_files SET missing_since = 5 WHERE id = ?`, files[0].ID) },
		"encrypted":     func() { streamExec(t, a, `UPDATE audio_files SET encrypted = 1 WHERE id = ?`, files[0].ID) },
		"probe failed":  func() { streamExec(t, a, `UPDATE audio_files SET probe_error = 'not_audio' WHERE id = ?`, files[0].ID) },
		"refused book":  func() { streamExec(t, a, `UPDATE books SET refused_reason = 'drm' WHERE id = ?`, book.ID) },
		"protected ext": func() { streamExec(t, a, `UPDATE audio_files SET path = path || '.m4p' WHERE id = ?`, files[0].ID) },
	} {
		path := "/peaks/" + files[0].ID
		if prep == nil {
			path = "/peaks/af_unknown"
		} else {
			prep()
		}
		if code, _, body := streamGet(a, http.MethodGet, path, nil, owner); code != http.StatusNotFound || bytes.Contains(body, []byte("TSPK")) {
			t.Errorf("%s: %d", name, code)
		}
		// Undo for the next case.
		streamExec(t, a, `UPDATE audio_files SET missing_since = NULL, encrypted = 0, probe_error = NULL, path = replace(path, '.m4p', '') WHERE id = ?`, files[0].ID)
		streamExec(t, a, `UPDATE books SET refused_reason = NULL WHERE id = ?`, book.ID)
	}
	if *opens != 0 {
		t.Errorf("openAudio ran %d times for the peaks route", *opens)
	}
	if n := peaksJobCount(t, a); n != 0 {
		t.Errorf("%d jobs queued for refused files", n)
	}
}

func TestPeaksOffAnswers404(t *testing.T) {
	a, owner, _, _, _, files, _ := streamEnvWith(t, false)
	peaksWrite(t, a, files[0])

	// No service at all.
	if code, _, _ := streamGet(a, http.MethodGet, "/peaks/"+files[0].ID, nil, owner); code != http.StatusNotFound {
		t.Errorf("nil service: %d", code)
	}
	// A service that is switched off, with the file in place.
	a.srv.peaks = mediajobs.NewPeaks(a.lib, media.Tools{FFmpeg: "/nonexistent/ffmpeg"}, mediajobs.PeaksDir(a.dir), a.runner, false, nil)
	if code, _, _ := streamGet(a, http.MethodGet, "/peaks/"+files[0].ID, nil, owner); code != http.StatusNotFound {
		t.Errorf("disabled service: %d", code)
	}
}

func TestPeaksMethodGuard(t *testing.T) {
	a, owner, _, _, _, files, _ := streamEnvWith(t, false)
	peaksAttach(t, a)
	tok := a.tokenFor(t, owner)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		rec := a.do(m, "/peaks/"+files[0].ID, url.Values{"_csrf": {tok}}, func(r *http.Request) { r.Header.Set(csrfHeader, tok) }, owner)
		if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
			t.Errorf("%s: %d, Allow %q", m, rec.Code, rec.Header().Get("Allow"))
		}
	}
}
