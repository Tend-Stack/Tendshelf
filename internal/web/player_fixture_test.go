//go:build dev

package web

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/testutil/wav"
)

// playerEnv is the fixture of the player's browser tests: one signed-in server with a book of two
// 90 s WAV files (offsets 0 and 90 000), six chapters of 30 s and a cover.
type playerEnv struct {
	a       *testApp
	book    library.Book
	files   []library.AudioFile
	enq     *streamEnq
	spy     *playerSpy
	srv     *httptest.Server
	URL     string
	ownerID string
	folder  library.Folder
}

// playerReq is one request the spy saw on /progress or /stream.
type playerReq struct {
	Method, Path, Query, ContentType, CSRFHeader, Body string
	At                                                 time.Time
}

// playerSpy wraps the app: it adds the owner's session cookie (unless asked to leave it off for
// /progress, the signed-out test) and records every /progress and /stream request.
type playerSpy struct {
	mu       sync.Mutex
	reqs     []playerReq
	signedIn bool // false: no session cookie on /progress
	cookie   *http.Cookie
	next     http.Handler
}

func (s *playerSpy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	strip := false
	if strings.HasPrefix(r.URL.Path, "/progress/") || strings.HasPrefix(r.URL.Path, "/stream/") {
		var body []byte
		if r.Body != nil && r.Method == http.MethodPost {
			body, _ = io.ReadAll(io.LimitReader(r.Body, 8192))
			r.Body = io.NopCloser(bytes.NewReader(body))
		}
		s.mu.Lock()
		s.reqs = append(s.reqs, playerReq{r.Method, r.URL.Path, r.URL.RawQuery, r.Header.Get("Content-Type"), r.Header.Get("X-CSRF-Token"), string(body), time.Now()})
		strip = !s.signedIn && strings.HasPrefix(r.URL.Path, "/progress/")
		s.mu.Unlock()
	}
	if !strip {
		r.AddCookie(&http.Cookie{Name: s.cookie.Name, Value: s.cookie.Value})
	}
	s.next.ServeHTTP(w, r)
}

// posts returns the recorded POSTs to /progress.
func (s *playerSpy) posts() []playerReq {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []playerReq
	for _, v := range s.reqs {
		if v.Method == http.MethodPost && strings.HasPrefix(v.Path, "/progress/") {
			out = append(out, v)
		}
	}
	return out
}

// count returns how many recorded requests used the method on a path or query containing part.
func (s *playerSpy) count(method, part string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, v := range s.reqs {
		if v.Method == method && strings.Contains(v.Path+"?"+v.Query, part) {
			n++
		}
	}
	return n
}

func (s *playerSpy) setSignedIn(v bool) {
	s.mu.Lock()
	s.signedIn = v
	s.mu.Unlock()
}

// playerApp builds the fixture.
func playerApp(t *testing.T) *playerEnv {
	t.Helper()
	return playerAppWith(t, nil)
}

// playerAppWith is playerApp with the book's spec changed first (the Now playing panel tests give it a
// summary, a series and genres).
func playerAppWith(t *testing.T, tweak func(*librarytest.BookSpec)) *playerEnv {
	t.Helper()
	return playerAppMods(t, tweak)
}

// playerAppMods is playerAppWith that also applies more options to the app (a lookup service, for example).
func playerAppMods(t *testing.T, tweak func(*librarytest.BookSpec), mods ...func(*Options)) *playerEnv {
	t.Helper()
	enq := &streamEnq{}
	a := newTestApp(t, append([]func(*Options){func(o *Options) {
		o.Compat = mediajobs.NewCompat(o.Library, media.Tools{}, mediajobs.CompatDir(o.DataDir), 1<<30, enq, nil)
	}}, mods...)...)
	owner := a.owner(t)
	_, f := librarytest.NewLibrary(t, a.lib, "Player library", t.TempDir())
	spec := librarytest.BookSpec{
		Title: "Harbour Lamp", Authors: []string{"Imogen Hale"}, Narrators: []string{"Theo Marsh"}, DurationMs: 180_000, Files: 2,
	}
	if tweak != nil {
		tweak(&spec)
	}
	book := librarytest.AddBook(t, a.lib, f, spec)
	bookVisualCover(t, a, book, 0)
	// the cover's colours for the full player (P1c-5): tint #1f4a52 from the palette, teal accents
	streamExec(t, a, `UPDATE covers SET accent_json=? WHERE book_id=?`, `{"dark":"#9ccbc4","light":"#2c6660","sepia":"#2c6660"}`, book.ID)
	files, err := a.lib.AudioFiles(context.Background(), book.ID)
	if err != nil || len(files) != 2 {
		t.Fatalf("audio files = %d, %v", len(files), err)
	}
	for i, af := range files {
		if err := wav.Write(af.Path, 90_000, 440+i*100); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(af.Path)
		if err != nil {
			t.Fatal(err)
		}
		streamExec(t, a, `UPDATE audio_files SET container='wav', codec='pcm_s16le', duration_ms=90000, offset_ms=?, size=?, mtime_ns=? WHERE id=?`,
			int64(i)*90_000, fi.Size(), fi.ModTime().UnixNano(), af.ID)
	}
	files, _ = a.lib.AudioFiles(context.Background(), book.ID)
	var chs []library.Chapter
	for i := 0; i < 6; i++ {
		chs = append(chs, library.Chapter{StartMs: int64(i) * 30_000, EndMs: int64(i+1) * 30_000, Title: "Chapter " + string(rune('1'+i)), Source: library.ChapterFile})
	}
	if err := a.lib.Update(context.Background(), func(tx *library.Tx) error { return tx.ReplaceChapters(book.ID, chs) }); err != nil {
		t.Fatal(err)
	}
	spy := &playerSpy{next: a.h, signedIn: true, cookie: owner}
	srv := httptest.NewServer(spy)
	t.Cleanup(srv.Close)
	return &playerEnv{folder: f, a: a, book: book, files: files, enq: enq, spy: spy, srv: srv, URL: srv.URL, ownerID: sweepUserID(t, a, owner)}
}

// store writes the owner's progress for the book as another device would have: at is the save's
// client time (0 = now), force makes it win over a newer row.
func (e *playerEnv) store(t *testing.T, posMs int64, device, label string, at int64, force bool) {
	t.Helper()
	if at == 0 {
		at = time.Now().UnixMilli()
	}
	if _, err := e.a.srv.progress.Save(context.Background(), progress.Update{UserID: e.ownerID, BookID: e.book.ID, PositionMs: posMs,
		DurationMs: e.book.DurationMs, ClientUpdatedAt: at, DeviceID: device, DeviceLabel: label, Force: force}); err != nil {
		t.Fatal(err)
	}
}

// setCodec changes a file's container and codec (the compat test).
func (e *playerEnv) setCodec(t *testing.T, idx int, container, codec string) {
	t.Helper()
	streamExec(t, e.a, `UPDATE audio_files SET container=?, codec=? WHERE id=?`, container, codec, e.files[idx].ID)
	e.files, _ = e.a.lib.AudioFiles(context.Background(), e.book.ID)
}
