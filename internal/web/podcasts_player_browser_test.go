//go:build dev

package web

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/podcast"
)

// silentWAV is ten seconds of 8 kHz mono silence: small, and every browser decodes it.
func silentWAV() []byte {
	const rate, seconds = 8000, 10
	data := make([]byte, rate*seconds)
	for i := range data {
		data[i] = 0x80
	}
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+len(data)))
	copy(h[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], 1)
	binary.LittleEndian.PutUint32(h[24:], rate)
	binary.LittleEndian.PutUint32(h[28:], rate)
	binary.LittleEndian.PutUint16(h[32:], 1)
	binary.LittleEndian.PutUint16(h[34:], 8)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(len(data)))
	return append(h, data...)
}

// seenReq is one request the browser made to an episode route, with the status it got.
type seenReq struct {
	Method, Path, Range string
	Status              int
	Body                string // a progress save's form, so a failure shows what was sent
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (s *statusWriter) WriteHeader(c int) {
	s.status = c
	s.ResponseWriter.WriteHeader(c)
}

func (s *statusWriter) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *statusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (s *statusWriter) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// podPlayServer serves the app to the browser as Ben and records what the episode routes answered.
func podPlayServer(t *testing.T, w *podWorld) (url string, seen func() []seenReq) {
	t.Helper()
	var mu sync.Mutex
	var reqs []seenReq
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		r.AddCookie(w.ben)
		var body string
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/podcasts/progress/") {
			b, _ := io.ReadAll(r.Body)
			body = string(b)
			r.Body = io.NopCloser(bytes.NewReader(b))
		}
		sw := &statusWriter{ResponseWriter: rw}
		w.a.h.ServeHTTP(sw, r)
		if strings.HasPrefix(r.URL.Path, "/podcasts/audio/") || strings.HasPrefix(r.URL.Path, "/podcasts/progress/") {
			mu.Lock()
			reqs = append(reqs, seenReq{r.Method, r.URL.Path, r.Header.Get("Range"), sw.status, body})
			mu.Unlock()
		}
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() []seenReq {
		mu.Lock()
		defer mu.Unlock()
		return append([]seenReq(nil), reqs...)
	}
}

func podWavWorld(t *testing.T) *podWorld {
	t.Helper()
	w := newPodWorld(t)
	w.fx.audio = silentWAV()
	ct := "audio/wav"
	w.fx.audioCT.Store(&ct)
	return w
}

// TestEpisodePlaysThroughTheProxy: an episode plays in the real player through /podcasts/audio (206 answers), a seek
// lands where asked, the place is saved to the episode's own route, and a reload brings the bar back to that place.
// The show's skip-intro and skip-outro apply, and an episode that reaches its outro is saved as played.
func TestEpisodePlaysThroughTheProxy(t *testing.T) {
	b := chromium(t)
	w := podWavWorld(t)
	ctx := t.Context()
	// Episode 2 skips 2 s at the start and stops 3 s before its end.
	if err := w.fx.svc.SetShowSettings(ctx, w.benID, w.show, podcast.ShowSettings{SkipIntroS: 2, SkipOutroS: 3, KeepLatest: 10}); err != nil {
		t.Fatal(err)
	}
	base, seen := podPlayServer(t, w)
	tab := b.NewTab(1280, 800, false, false)

	// Play episode 1: the intro is skipped, the length is learnt from the file, a seek lands at half.
	ep := w.eps[0]
	tab.Navigate(base + "/podcasts/episodes/" + ep)
	tab.WaitFor(`window.tsPlayer !== undefined`, 10*time.Second)
	playerClickSel(t, tab, `[data-ts-play="`+ep+`"]`)
	tab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().positionMs >= 2000`, 15*time.Second)
	tab.WaitFor(`Math.abs(window.tsPlayer.state().durationMs - 10000) < 500`, 5*time.Second)
	var st playerState
	tab.Eval(`window.tsPlayer.seek(window.tsPlayer.state().durationMs / 2)`, nil)
	tab.WaitFor(`Math.abs(window.tsPlayer.state().positionMs - 5000) < 1500`, 5*time.Second)
	tab.Eval(`window.tsPlayer.pause()`, nil)
	tab.WaitFor(`!window.tsPlayer.state().playing`, 5*time.Second)
	st = playerStateOf(tab)
	if st.BookID != ep {
		t.Fatalf("loaded %q, want %q", st.BookID, ep)
	}
	// The player asks for the whole file first (200). A seek past what the browser holds asks for a range; the same
	// request from the page must get a 206 with a Content-Range, and the bytes after the offset.
	var rng struct {
		Status       int    `json:"status"`
		ContentRange string `json:"contentRange"`
		Bytes        int    `json:"bytes"`
	}
	tab.Eval(`fetch("/podcasts/audio/`+ep+`", {headers: {Range: "bytes=40000-"}}).then(async (r) => ({status: r.status, contentRange: r.headers.get("Content-Range"), bytes: (await r.arrayBuffer()).byteLength}))`, &rng)
	if rng.Status != http.StatusPartialContent || !strings.HasPrefix(rng.ContentRange, "bytes 40000-") || rng.Bytes != len(w.fx.audio)-40000 {
		t.Errorf("range request: %+v", rng)
	}
	var got206 bool
	for _, r := range seen() {
		if r.Path == "/podcasts/audio/"+ep && r.Status == http.StatusPartialContent {
			got206 = true
		}
	}
	if !got206 {
		t.Errorf("no 206 recorded: %+v", seen())
	}
	// The pause saved the place to the episode route (and not to /progress). An earlier save (from the first
	// seconds of play) can answer first, so wait for the stored place itself to reach the paused position.
	var saved podcast.Episode
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(25 * time.Millisecond) {
		got, err := w.fx.svc.Episode(ctx, w.benID, ep)
		if err == nil {
			saved = got
			if got.State.PositionMs >= 3500 && got.State.PositionMs <= 6500 {
				break
			}
		}
		if time.Now().After(deadline) {
			var now playerState
			now = playerStateOf(tab)
			t.Fatalf("the paused place was never stored: stored %d (client %d); player %+v; requests %+v",
				saved.State.PositionMs, saved.State.ClientUpdated, now, seen())
		}
	}
	for _, r := range seen() {
		if r.Method == http.MethodPost && r.Path == "/progress/"+ep {
			t.Errorf("an episode save went to the book route: %+v", r)
		}
	}

	// A reload: the bar returns with this episode, paused at the saved place.
	tab.Navigate(base + "/podcasts")
	tab.WaitFor(`window.tsPlayer && window.tsPlayer.state().bookId !== ""`, 10*time.Second)
	var kind string
	tab.Eval(`window.tsPlayer.state().itemKind`, &kind)
	st = playerStateOf(tab)
	if st.BookID != ep || kind != "episode" || st.PositionMs < saved.State.PositionMs-1500 || st.PositionMs > saved.State.PositionMs+1500 || st.Playing {
		time.Sleep(time.Second)
		later := playerStateOf(tab)
		now, _ := w.fx.svc.Episode(ctx, w.benID, ep)
		t.Errorf("after reload: %+v kind %q, stored %d; a second later %+v, stored now %d at %d (saved at %d); requests %+v", st, kind, saved.State.PositionMs, later, now.State.PositionMs, now.State.ClientUpdated, saved.State.ClientUpdated, seen())
	}

	// Episode 2 reaches the outro (3 s before the end): it counts as ended and is saved as played.
	ep2 := w.eps[1]
	tab.Navigate(base + "/podcasts/episodes/" + ep2)
	tab.WaitFor(`window.tsPlayer !== undefined`, 10*time.Second)
	playerClickSel(t, tab, `[data-ts-play="`+ep2+`"]`)
	tab.WaitFor(`window.tsPlayer.state().bookId === `+fmt.Sprintf("%q", ep2)+` && window.tsPlayer.state().playing`, 15*time.Second)
	tab.WaitFor(`Math.abs(window.tsPlayer.state().durationMs - 10000) < 500`, 5*time.Second)
	tab.Eval(`window.tsPlayer.seek(6000)`, nil)
	waitFor(t, "the episode marked played", func() bool {
		e, err := w.fx.svc.Episode(ctx, w.benID, ep2)
		return err == nil && e.State.Played
	})
	tab.WaitFor(`!window.tsPlayer.state().playing`, 5*time.Second)
}

// TestEpisodePlayerBarVisuals captures the bar and the full player with an episode at 390, 768, 1024 and 1440 px and checks
// the bar's title and meta are cut, not wrapped, and nothing overflows sideways.
func TestEpisodePlayerBarVisuals(t *testing.T) {
	b := chromium(t)
	w := podWavWorld(t)
	out := visualDir(t, "chromium")
	// A long title and a long show name must truncate in the bar.
	long := "Why the foghorn fell silent and what the harbour master did about it the next morning, in full"
	if _, err := w.a.st.Writer().Exec(`UPDATE podcast_episodes SET title = ? WHERE id = ?`, long, w.eps[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := w.a.st.Writer().Exec(`UPDATE podcast_shows SET title = ? WHERE id = ?`, "A Very Long Podcast Title About Lighthouses and Fog Signals", w.show); err != nil {
		t.Fatal(err)
	}
	base, _ := podPlayServer(t, w)
	for _, size := range [][2]int{{390, 844}, {768, 1024}, {1024, 768}, {1440, 900}} {
		width, height := size[0], size[1]
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			tab := b.NewTab(width, height, width < 768, true)
			tab.Navigate(base + "/podcasts")
			tab.WaitFor(`window.tsPlayer !== undefined`, 10*time.Second)
			tab.Eval(`window.tsPlayer.load(`+fmt.Sprintf("%q", w.eps[0])+`, {positionMs: 4000})`, nil)
			tab.WaitFor(`window.tsPlayer.state().bookId !== "" && !document.getElementById("player").hidden`, 10*time.Second)
			time.Sleep(700 * time.Millisecond)
			var m struct {
				Over      bool    `json:"over"`
				TitleCut  bool    `json:"titleCut"`
				TitleRows float64 `json:"titleRows"`
				MetaCut   bool    `json:"metaCut"`
				MetaText  string  `json:"metaText"`
				Small     []string
			}
			tab.Eval(`(() => {
				const t = document.querySelector("#player [data-ts-title], #player .ts-player__title") || document.querySelector("#player [data-pbar=title]");
				const meta = document.querySelector("#player [data-ts-meta], #player [data-pbar=meta]");
				const lh = (e) => parseFloat(getComputedStyle(e).lineHeight) || 20;
				return {over: document.documentElement.scrollWidth > innerWidth + 1,
					titleCut: !!t && t.scrollWidth > t.clientWidth + 1 || !!t && t.getBoundingClientRect().height <= lh(t) * 2 + 1,
					titleRows: t ? t.getBoundingClientRect().height / lh(t) : 0,
					metaCut: !!meta && meta.getBoundingClientRect().height <= lh(meta) + 1, metaText: meta ? meta.textContent : ""};
			})()`, &m)
			if m.Over {
				t.Errorf("horizontal overflow")
			}
			if m.TitleRows > 2.1 || !m.MetaCut {
				t.Errorf("bar text wraps: title rows %.1f, meta on one line %v (%q)", m.TitleRows, m.MetaCut, m.MetaText)
			}
			if !strings.Contains(m.MetaText, "min left") {
				t.Errorf("meta line = %q", m.MetaText)
			}
			tab.Screenshot(filepath.Join(out, fmt.Sprintf("podcasts-player-bar-%d.png", width)))
			fullpOpenPlayer(t, tab)
			tab.Screenshot(filepath.Join(out, fmt.Sprintf("podcasts-player-full-%d.png", width)))
		})
	}
}
