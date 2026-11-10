package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestRadioHLSAddRefusals: an HLS station that is locked, video, in a format Tendshelf can't play, a finished
// recording or whose segments sit on a private host is refused in plain words, and nothing is stored.
func TestRadioHLSAddRefusals(t *testing.T) {
	w := newRadioWorld(t)
	w.fx.pub.Resolve["private.test"] = "127.0.0.1"
	for _, tc := range []struct{ name, path, text string }{
		{"locked", "/hls-enc/master.m3u8", "locked"},
		{"video only", "/hls-video/master.m3u8", "sends video"},
		{"bad codec", "/hls-ac3/master.m3u8", "sound format Tendshelf can't play"},
		{"recording", "/hls-rec/live.m3u8", "a recording, not a live station"},
		{"segment on a private address", "/hls-priv/live.m3u8", "public web address"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := w.post("/radio/stations", w.ben, url.Values{"stream_url": {w.fx.pub.URL("radio.test", tc.path)}})
			if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), tc.text) {
				t.Errorf("%d %q, want 422 with %q", rec.Code, rec.Body, tc.text)
			}
			if strings.Contains(rec.Body.String(), "127.0.0.1") || strings.Contains(rec.Body.String(), "private.test") {
				t.Errorf("answer repeats the address: %q", rec.Body)
			}
		})
	}
	var n int
	w.a.st.Reader().QueryRow(`SELECT count(*) FROM radio_stations`).Scan(&n)
	if n != 0 {
		t.Errorf("a refused address was stored (%d)", n)
	}
}

// adtsFrames walks b as back-to-back ADTS frames of size flen whose payload bytes are all one value and
// returns those values. It fails on any byte that is not part of a whole frame.
func adtsFrames(t *testing.T, b []byte, flen int) []byte {
	t.Helper()
	var seqs []byte
	for len(b) >= flen {
		if b[0] != 0xFF || b[1]&0xF0 != 0xF0 {
			t.Fatalf("not an ADTS sync: % x", b[:4])
		}
		if l := int(b[3]&3)<<11 | int(b[4])<<3 | int(b[5]>>5); l != flen {
			t.Fatalf("frame length %d, want %d", l, flen)
		}
		seqs = append(seqs, b[7])
		b = b[flen:]
	}
	return seqs
}

// readAtLeast reads from a live response until n bytes arrived (or the deadline).
func readAtLeast(t *testing.T, r io.Reader, n int, d time.Duration) []byte {
	t.Helper()
	var got []byte
	buf := make([]byte, 4096)
	deadline := time.Now().Add(d)
	for len(got) < n && time.Now().Before(deadline) {
		m, err := r.Read(buf)
		got = append(got, buf[:m]...)
		if err != nil {
			break
		}
	}
	return got
}

// TestRadioHLSRelayContinuous: a sliding-window HLS station is served as one continuous ADTS stream with its
// own type and the usual headers; the segments follow each other without a gap or a repeat, the EXTINF title
// reaches the now-playing line, and a client that leaves frees the live place and ends the session.
func TestRadioHLSRelayContinuous(t *testing.T) {
	w := newRadioWorld(t)
	id := w.add(w.ben, w.fx.pub.URL("radio.test", "/hls-ok/master.m3u8"))
	var format, codec string
	if err := w.a.st.Reader().QueryRow(`SELECT format, codec FROM radio_stations WHERE id = ?`, id).Scan(&format, &codec); err != nil || format != "hls" || codec != "aac" {
		t.Fatalf("stored %q %q %v", format, codec, err)
	}
	st := w.fx.hls["/hls-ok/"]
	flen := st.FrameLen()
	srv := httptest.NewServer(w.a.h)
	defer srv.Close()
	goroutines := runtime.NumGoroutine()
	req, _ := http.NewRequest("GET", srv.URL+"/radio/live/"+id, nil)
	req.AddCookie(w.ben)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	h := resp.Header
	if resp.StatusCode != 200 || h.Get("Content-Type") != "audio/aac" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Content-Security-Policy") != "default-src 'none'; sandbox" || h.Get("Cache-Control") != "private, no-store" || h.Get("Accept-Ranges") != "none" {
		t.Fatalf("%d headers %v", resp.StatusCode, h)
	}
	// Three segments at once, then the window moves: read past four segments' worth.
	got := readAtLeast(t, resp.Body, 5*8*flen, 15*time.Second)
	if len(got) < 5*8*flen {
		t.Fatalf("only %d bytes arrived", len(got))
	}
	got = got[:len(got)/flen*flen]
	seqs := adtsFrames(t, got, flen)
	for i := 1; i < len(seqs); i++ {
		if seqs[i] != seqs[i-1] && seqs[i] != seqs[i-1]+1 {
			t.Fatalf("segments are not continuous at frame %d: %v", i, seqs)
		}
	}
	if seqs[len(seqs)-1] <= seqs[0] {
		t.Errorf("the stream never moved on: %v", seqs)
	}
	if rec := w.get("/hx/radio/now/"+id, w.ben); !strings.Contains(rec.Body.String(), "Ann - Live Song") {
		t.Errorf("now playing: %q", rec.Body)
	}
	resp.Body.Close() // the listener leaves
	deadline := time.Now().Add(10 * time.Second)
	for {
		rel, err := w.fx.svc.Acquire(w.benID, id)
		if err == nil {
			rel()
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the live place was not freed: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	hits := w.fx.hlsHits.Load()
	time.Sleep(1500 * time.Millisecond)
	if more := w.fx.hlsHits.Load(); more > hits+1 { // at most one request already in flight
		t.Errorf("the session kept fetching after the client left: %d new requests", more-hits)
	}
	for i := 0; i < 50 && runtime.NumGoroutine() > goroutines+2; i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > goroutines+2 {
		t.Errorf("goroutines %d, were %d before the listener", n, goroutines)
	}
}

// TestRadioHLSRelayMP3: MPEG audio segments are served as audio/mpeg frames.
func TestRadioHLSRelayMP3(t *testing.T) {
	w := newRadioWorld(t)
	id := w.add(w.ben, w.fx.pub.URL("radio.test", "/hls-mp3/master.m3u8"))
	srv := httptest.NewServer(w.a.h)
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/radio/live/"+id, nil)
	req.AddCookie(w.ben)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.Header.Get("Content-Type") != "audio/mpeg" {
		t.Fatalf("type %q", resp.Header.Get("Content-Type"))
	}
	got := readAtLeast(t, resp.Body, 3*8*417, 10*time.Second)
	if len(got) < 3*8*417 {
		t.Fatalf("only %d bytes", len(got))
	}
	for i := 0; i+417 <= len(got); i += 417 {
		if !bytes.Equal(got[i:i+4], []byte{0xFF, 0xFB, 0x90, 0x00}) {
			t.Fatalf("frame at %d starts % x", i, got[i:i+4])
		}
	}
}

// TestRadioHLSMaxSession: the 12 hour limit is the caller's: the same session context that ends an Icecast relay
// ends an HLS one. The limit is shortened the way the existing relay test does it.
func TestRadioHLSMaxSession(t *testing.T) {
	w := newRadioWorld(t)
	w.fx.svc.MaxSession = 400 * time.Millisecond
	id := w.add(w.ben, w.fx.pub.URL("radio.test", "/hls-ok/master.m3u8"))
	_, cancel, done := w.liveGet(w.ben, "/radio/live/"+id)
	defer cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the HLS session did not end at its maximum")
	}
}

// TestRadioHLSEndsWhenRadioTurnedOff: Off means off for an HLS relay too.
func TestRadioHLSEndsWhenRadioTurnedOff(t *testing.T) {
	old := radioEnabledEvery
	radioEnabledEvery = 50 * time.Millisecond
	defer func() { radioEnabledEvery = old }()
	w := newRadioWorld(t)
	id := w.add(w.ben, w.fx.pub.URL("radio.test", "/hls-ok/master.m3u8"))
	_, cancel, done := w.liveGet(w.ben, "/radio/live/"+id)
	defer cancel()
	time.Sleep(300 * time.Millisecond)
	w.fx.enable(t, w.a, false)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the HLS relay kept running after radio was turned off")
	}
}

// TestRadioHLSLiveForbidden: an HLS station is reached only by a person who keeps or played it; a guest is
// refused by role, and the API route answers the same.
func TestRadioHLSLiveForbidden(t *testing.T) {
	w := newRadioWorld(t)
	id := w.add(w.ben, w.fx.pub.URL("radio.test", "/hls-ok/master.m3u8"))
	for name, tc := range map[string]struct {
		path string
		ck   *http.Cookie
	}{
		"someone else":       {"/radio/live/" + id, w.cy},
		"guest":              {"/radio/live/" + id, w.guest},
		"someone else (API)": {"/api/v1/radio/live/" + id, w.cy},
	} {
		if rec := w.get(tc.path, tc.ck); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if rec := w.get("/radio/live/"+id, nil); rec.Code == http.StatusOK {
		t.Errorf("signed out: %d", rec.Code)
	}
}

// ---- Play on an unsaved station (POST /radio/play) ----

func (w *radioWorld) count(q string, args ...any) int {
	w.t.Helper()
	var n int
	if err := w.a.st.Reader().QueryRow(q, args...).Scan(&n); err != nil {
		w.t.Fatal(err)
	}
	return n
}

// TestRadioPlaySlug: Play on a search result keeps the station as played, not saved, and answers with the live
// stream (a redirect without JavaScript, the station id for the player script).
func TestRadioPlaySlug(t *testing.T) {
	w := newRadioWorld(t)
	rec := w.post("/radio/play", w.cy, url.Values{"slug": {"cool-fm"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("play: %d %q", rec.Code, rec.Body)
	}
	var id string
	if err := w.a.st.Reader().QueryRow(`SELECT id FROM radio_stations WHERE ondacast_slug = 'cool-fm'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	if loc := rec.Header().Get("Location"); loc != "/radio/live/"+id {
		t.Errorf("Location %q", loc)
	}
	if n := w.count(`SELECT count(*) FROM radio_favourites WHERE user_id = ?`, w.cyID); n != 0 {
		t.Errorf("the station was saved (%d favourites)", n)
	}
	if n := w.count(`SELECT count(*) FROM radio_plays WHERE user_id = ? AND station_id = ?`, w.cyID, id); n != 1 {
		t.Errorf("plays rows = %d", n)
	}
	// The link the redirect names plays; only its person reaches it.
	_, audio := radioAudio()
	if live := w.get("/radio/live/"+id, w.cy); live.Code != 200 || !bytes.Equal(live.Body.Bytes(), audio) {
		t.Errorf("live: %d", live.Code)
	}
	if live := w.get("/radio/live/"+id, w.ben); live.Code != http.StatusNotFound {
		t.Errorf("someone else: %d", live.Code)
	}
	// Played, not kept: it is among the recent stations with a Save control.
	page := w.get("/radio", w.cy).Body.String()
	if !strings.Contains(page, "Cool FM") || !strings.Contains(page, "/radio/stations/"+id+"/save") {
		t.Errorf("Radio page lacks the played station: %q", page)
	}
	// A second Play needs neither OndaCast nor a probe, and still makes one row.
	w.fx.ondaDown.Store(true)
	searches, hits := w.fx.ondaAll.Load(), w.fx.hits.Load()
	rec = w.post("/radio/play", w.cy, url.Values{"slug": {"cool-fm"}}, func(r *http.Request) { r.Header.Set("Accept", "application/json") })
	var ans struct{ ID string }
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &ans) != nil || ans.ID != id {
		t.Fatalf("json play: %d %q", rec.Code, rec.Body)
	}
	if w.fx.ondaAll.Load() != searches || w.fx.hits.Load() != hits {
		t.Error("a known station was looked up again")
	}
	if n := w.count(`SELECT count(*) FROM radio_stations`); n != 1 {
		t.Errorf("stations = %d", n)
	}
	// Save keeps it afterwards.
	if rec := w.post("/radio/stations/"+id+"/save", w.cy, nil); rec.Code != http.StatusSeeOther {
		t.Errorf("save after play: %d", rec.Code)
	}
	if n := w.count(`SELECT count(*) FROM radio_favourites WHERE user_id = ?`, w.cyID); n != 1 {
		t.Errorf("favourites = %d", n)
	}
}

// TestRadioPlaySlugHLS: Play works on an HLS station found by search, and the row says so.
func TestRadioPlaySlugHLS(t *testing.T) {
	w := newRadioWorld(t)
	if rec := w.post("/radio/play", w.cy, url.Values{"slug": {"hls-fm"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("play: %d %q", rec.Code, rec.Body)
	}
	var format string
	if err := w.a.st.Reader().QueryRow(`SELECT format FROM radio_stations WHERE ondacast_slug = 'hls-fm'`).Scan(&format); err != nil || format != "hls" {
		t.Errorf("format %q %v", format, err)
	}
}

// TestRadioPlaySlugRefusals: unknown and over-long slugs, OndaCast down, a missing token, a guest and a signed-out
// caller are refused; nothing is stored.
func TestRadioPlaySlugRefusals(t *testing.T) {
	w := newRadioWorld(t)
	for name, form := range map[string]url.Values{
		"empty":   {},
		"unknown": {"slug": {"nope"}},
		"long":    {"slug": {strings.Repeat("a", 201)}},
		"address": {"stream_url": {w.fx.liveURL()}},
	} {
		if rec := w.post("/radio/play", w.cy, form); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	w.fx.ondaDown.Store(true)
	if rec := w.post("/radio/play", w.cy, url.Values{"slug": {"cool-fm"}}); rec.Code != http.StatusBadGateway {
		t.Errorf("OndaCast down: %d", rec.Code)
	}
	if rec := w.post("/radio/play", w.guest, url.Values{"slug": {"cool-fm"}}); rec.Code != http.StatusNotFound {
		t.Errorf("guest: %d", rec.Code)
	}
	if rec := w.a.do(http.MethodPost, "/radio/play", url.Values{"slug": {"cool-fm"}}, nil, w.cy); rec.Code != http.StatusForbidden {
		t.Errorf("no token: %d", rec.Code)
	}
	if rec := w.a.do(http.MethodPost, "/radio/play", url.Values{"slug": {"cool-fm"}}, nil); rec.Code == http.StatusOK || rec.Code == http.StatusSeeOther {
		t.Errorf("signed out: %d", rec.Code)
	}
	if n := w.count(`SELECT count(*) FROM radio_stations`); n != 0 {
		t.Errorf("a refused play stored %d stations", n)
	}
	// Radio off: the plain 404.
	w.fx.ondaDown.Store(false)
	w.fx.enable(t, w.a, false)
	if rec := w.post("/radio/play", w.cy, url.Values{"slug": {"cool-fm"}}); rec.Code != http.StatusNotFound {
		t.Errorf("radio off: %d", rec.Code)
	}
}

// TestRadioPlaySlugControls: search results and the popular suggestions carry a Play form next to Save.
func TestRadioPlaySlugControls(t *testing.T) {
	w := newRadioWorld(t)
	for _, path := range []string{"/radio/search?q=cool", "/radio"} {
		body := w.get(path, w.cy).Body.String()
		if !strings.Contains(body, `action="/radio/play"`) || !strings.Contains(body, "data-ts-play-slug") || !strings.Contains(body, `action="/radio/stations"`) {
			t.Errorf("%s lacks the Play form beside Save", path)
		}
	}
}
