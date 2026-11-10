package web

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/podcast"
	"github.com/Tend-Stack/tendshelf/internal/podcast/podtest"
	"github.com/Tend-Stack/tendshelf/internal/radio"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// radioFix is a fake station host and a fake OndaCast on one httptest server, reached through the real guarded
// client (podtest). No test here touches the network.
type radioFix struct {
	pub  *podtest.Publisher
	pod  *podcast.Service
	svc  *radio.Service
	hits atomic.Int64 // requests that reached a station path
	// searches counts station searches OndaCast answered.
	searches atomic.Int64
	// conns counts connections to /stream.mp3 and active the ones still open.
	conns, active atomic.Int64

	ondaDown atomic.Bool
	hold     chan struct{} // when set, /live.mp3 stays open until it is closed
	mu       sync.Mutex
	reqs     []http.Header
	liveCT   string // Content-Type of /live.mp3, default audio/mpeg
}

const radioMetaint = 32

// radioAudio is what /live.mp3 sends: two chunks of audio with titles between.
func radioAudio() (wire, audio []byte) {
	blk := func(s string) []byte {
		n := (len(s) + 15) / 16
		b := make([]byte, 1+n*16)
		b[0] = byte(n)
		copy(b[1:], s)
		return b
	}
	for i, title := range []string{"StreamTitle='Ann - First Song';", "", "StreamTitle='Ann - Second <b>Song</b>';"} {
		chunk := bytes.Repeat([]byte{byte('a' + i)}, radioMetaint)
		audio = append(audio, chunk...)
		wire = append(wire, chunk...)
		if title == "" {
			wire = append(wire, 0)
		} else {
			wire = append(wire, blk(title)...)
		}
	}
	return
}

func newRadioFix(t *testing.T) *radioFix {
	t.Helper()
	f := &radioFix{liveCT: "audio/mpeg"}
	f.pub = podtest.New(t, f.serve)
	return f
}

func (f *radioFix) setHold(c chan struct{}) {
	f.mu.Lock()
	f.hold = c
	f.mu.Unlock()
}

func (f *radioFix) holdCh() chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.hold
}

func (f *radioFix) liveURL() string { return f.pub.URL("radio.test", "/live.mp3") }

func (f *radioFix) serve(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.Host, "ondacast.test") {
		if f.ondaDown.Load() {
			http.Error(w, "down", http.StatusServiceUnavailable)
			return
		}
		if r.URL.Path == "/api/v2/search" && r.URL.Query().Get("type") == "stations" {
			f.searches.Add(1)
			fmt.Fprint(w, `{"success":true,"data":[{"title":"Cool FM","subtitle":"Pop, NL","slug":"cool-fm","image_url":"https://logo.test/x.png"}]}`)
			return
		}
		if r.URL.Path == "/api/v2/embed/stations/cool-fm" {
			fmt.Fprintf(w, `{"success":true,"data":{"name":"Cool FM","streamUrl":%q,"logo":%q,"genre":"Pop","country":"NL"}}`, f.liveURL(), f.pub.URL("logo.test", "/x.png"))
			return
		}
		http.NotFound(w, r)
		return
	}
	f.mu.Lock()
	f.reqs = append(f.reqs, r.Header.Clone())
	ct, hold := f.liveCT, f.hold
	f.mu.Unlock()
	switch r.URL.Path {
	case "/live.mp3":
		f.hits.Add(1)
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Icy-Metaint", fmt.Sprint(radioMetaint))
		w.Header().Set("Icy-Name", "Secret Upstream Name")
		w.Header().Set("Set-Cookie", "tracker=1")
		w.WriteHeader(200)
		wire, _ := radioAudio()
		w.Write(wire)
		w.(http.Flusher).Flush()
		if hold != nil {
			select {
			case <-hold:
			case <-r.Context().Done():
			}
		}
	case "/endless.mp3": // audio without end, for clients that stop reading
		f.hits.Add(1)
		w.Header().Set("Content-Type", "audio/mpeg")
		chunk := make([]byte, 16<<10)
		for r.Context().Err() == nil {
			if _, err := w.Write(chunk); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	case "/x.png": // the logo OndaCast names for Cool FM
		var b bytes.Buffer
		png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 8)))
		w.Header().Set("Content-Type", "image/png")
		w.Write(b.Bytes())
	case "/stream.mp3":
		f.serveSilence(w, r)
	case "/plain.mp3":
		w.Header().Set("Content-Type", "audio/mpeg")
		io.WriteString(w, "no titles here")
	case "/radio.pls":
		w.Header().Set("Content-Type", "audio/x-scpls")
		fmt.Fprintf(w, "[playlist]\nFile1=%s\n", f.liveURL())
	case "/hls":
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		io.WriteString(w, "#EXTM3U\n#EXT-X-VERSION:3\n")
	case "/page":
		w.Header().Set("Content-Type", "text/html")
		io.WriteString(w, "<html>sign in</html>")
	default:
		http.NotFound(w, r)
	}
}

func (f *radioFix) mod(t *testing.T) func(*Options) {
	return func(o *Options) {
		f.pod = podcast.New(o.Store, f.pub.Client, o.Jobs, o.DataDir, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		if err := f.pod.Register(); err != nil {
			t.Fatal(err)
		}
		f.svc = radio.New(o.Store, f.pub.Client, f.pod, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
		o.Podcasts, o.Radio = f.pod, f.svc
	}
}

func (f *radioFix) enable(t *testing.T, a *testApp, on bool) {
	t.Helper()
	s := podcast.Defaults()
	s.RadioEnabled = on
	if err := podcast.SaveSettings(context.Background(), a.st, "", s, time.Now()); err != nil {
		t.Fatal(err)
	}
}

type radioWorld struct {
	t              *testing.T
	fx             *radioFix
	a              *testApp
	owner, ben, cy *http.Cookie
	guest          *http.Cookie
	ownerID, benID string
	cyID           string
}

func newRadioWorld(t *testing.T) *radioWorld {
	t.Helper()
	fx := newRadioFix(t)
	a := newTestApp(t, fx.mod(t))
	fx.enable(t, a, true)
	w := &radioWorld{t: t, fx: fx, a: a, owner: a.owner(t), ben: a.as(t, users.RoleMember), cy: a.as(t, users.RoleMember), guest: a.as(t, users.RoleGuest)}
	w.ownerID, w.benID, w.cyID = sweepUserID(t, a, w.owner), sweepUserID(t, a, w.ben), sweepUserID(t, a, w.cy)
	return w
}

func (w *radioWorld) get(path string, ck *http.Cookie, mod ...func(*http.Request)) *httptest.ResponseRecorder {
	var m func(*http.Request)
	if len(mod) > 0 {
		m = mod[0]
	}
	return w.a.do(http.MethodGet, path, nil, m, ck)
}

func (w *radioWorld) post(path string, ck *http.Cookie, form url.Values, mod ...func(*http.Request)) *httptest.ResponseRecorder {
	w.t.Helper()
	f := url.Values{}
	for k, v := range form {
		f[k] = v
	}
	f.Set("_csrf", w.a.tokenFor(w.t, ck))
	var m func(*http.Request)
	if len(mod) > 0 {
		m = mod[0]
	}
	return w.a.do(http.MethodPost, path, f, m, ck)
}

// add saves the fake station for a person and returns its id.
func (w *radioWorld) add(ck *http.Cookie, streamURL string) string {
	w.t.Helper()
	rec := w.post("/radio/stations", ck, url.Values{"stream_url": {streamURL}})
	if rec.Code != http.StatusSeeOther {
		w.t.Fatalf("add: %d %q", rec.Code, rec.Body)
	}
	var id string
	if err := w.a.st.Reader().QueryRow(`SELECT id FROM radio_stations WHERE stream_url = ?`, streamURL).Scan(&id); err != nil {
		w.t.Fatal(err)
	}
	return id
}

func TestRadioOffIsOff(t *testing.T) {
	for _, name := range []string{"switch off", "no service"} {
		t.Run(name, func(t *testing.T) {
			fx := newRadioFix(t)
			var a *testApp
			if name == "no service" {
				a = newTestApp(t)
			} else {
				a = newTestApp(t, fx.mod(t))
				fx.enable(t, a, false)
			}
			owner := a.as(t, users.RoleOwner)
			for _, p := range []string{"/radio", "/radio/search?q=x", "/hx/player/radio/rs_x", "/radio/live/rs_x", "/hx/radio/now/rs_x", "/api/v1/radio/stations", "/api/v1/radio/recent",
				"/api/v1/radio/live/rs_x", "/api/v1/radio/now/rs_x"} {
				if rec := a.do(http.MethodGet, p, nil, nil, owner); rec.Code != http.StatusNotFound {
					t.Errorf("GET %s: %d, want 404", p, rec.Code)
				}
			}
			tok := a.tokenFor(t, owner)
			for _, p := range []string{"/radio/stations", "/radio/stations/rs_x/remove"} {
				if rec := a.do(http.MethodPost, p, url.Values{"_csrf": {tok}, "stream_url": {fx.liveURL()}}, nil, owner); rec.Code != http.StatusNotFound {
					t.Errorf("POST %s: %d, want 404", p, rec.Code)
				}
			}
			if fx.pub.Hits.Load() != 0 {
				t.Errorf("the station host was reached %d times", fx.pub.Hits.Load())
			}
		})
	}
}

func TestRadioAddAndRoles(t *testing.T) {
	w := newRadioWorld(t)
	live := w.fx.liveURL()
	// A guest is refused (404) and nothing is fetched.
	if rec := w.post("/radio/stations", w.guest, url.Values{"stream_url": {live}}); rec.Code != http.StatusNotFound {
		t.Errorf("guest add: %d", rec.Code)
	}
	if w.fx.pub.Hits.Load() != 0 {
		t.Error("a refused guest caused a fetch")
	}
	id := w.add(w.ben, live)
	if rec := w.post("/radio/stations", w.ben, url.Values{"stream_url": {live}}); rec.Code != http.StatusSeeOther {
		t.Errorf("repeat add: %d", rec.Code)
	}
	var n int
	w.a.st.Reader().QueryRow(`SELECT count(*) FROM radio_stations`).Scan(&n)
	if n != 1 {
		t.Errorf("stations = %d", n)
	}
	// A playlist is stored as the stream it names (same row), and the codec is recorded.
	if rec := w.post("/radio/stations", w.cy, url.Values{"stream_url": {w.fx.pub.URL("radio.test", "/radio.pls")}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("playlist add: %d %q", rec.Code, rec.Body)
	}
	var shared string
	w.a.st.Reader().QueryRow(`SELECT station_id FROM radio_favourites WHERE user_id = ?`, w.cyID).Scan(&shared)
	if shared != id {
		t.Errorf("a playlist naming a known stream made a new station")
	}
	// Strangers, guests and anonymous visitors are refused with the unknown-id answer.
	other := w.post("/radio/stations", w.owner, url.Values{"stream_url": {w.fx.pub.URL("radio.test", "/plain.mp3")}})
	if other.Code != http.StatusSeeOther {
		t.Fatalf("owner add: %d", other.Code)
	}
	var ownerStation string
	w.a.st.Reader().QueryRow(`SELECT station_id FROM radio_favourites WHERE user_id = ?`, w.ownerID).Scan(&ownerStation)
	for _, p := range []string{"/radio/live/", "/hx/radio/now/", "/api/v1/radio/live/", "/api/v1/radio/now/"} {
		if rec := w.get(p+ownerStation, w.ben); rec.Code != http.StatusNotFound {
			t.Errorf("stranger GET %s: %d", p, rec.Code)
		}
		if rec := w.get(p+id, w.guest); rec.Code != http.StatusNotFound {
			t.Errorf("guest GET %s: %d", p, rec.Code)
		}
	}
	if rec := w.get("/radio/live/"+id, nil); rec.Code == http.StatusOK {
		t.Errorf("anonymous live: %d", rec.Code)
	}
	if rec := w.post("/radio/stations/"+ownerStation+"/remove", w.ben, nil); rec.Code != http.StatusNotFound {
		t.Errorf("stranger remove: %d", rec.Code)
	}
	if rec := w.get("/api/v1/radio/stations", w.guest); rec.Code != http.StatusNotFound {
		t.Errorf("guest list: %d", rec.Code)
	}
	// The audit log has an entry for the add and no address in it.
	rows, _ := w.a.st.Reader().Query(`SELECT target || detail_json FROM audit_log WHERE action = 'radio.add'`)
	defer rows.Close()
	seen := 0
	for rows.Next() {
		var s string
		rows.Scan(&s)
		seen++
		if strings.Contains(s, "http") || strings.Contains(s, "radio.test") {
			t.Errorf("audit holds an address: %s", s)
		}
	}
	if seen != 3 { // ben, cy (the playlist) and the owner
		t.Errorf("audit entries = %d", seen)
	}
}

func TestRadioAddRefusals(t *testing.T) {
	w := newRadioWorld(t)
	w.fx.pub.Resolve["private.test"] = "127.0.0.1"
	for _, tc := range []struct {
		name, url string
		status    int
		text      string
	}{
		{"private dns", w.fx.pub.URL("private.test", "/live.mp3"), 422, "public web address"},
		{"literal loopback", "http://127.0.0.1:8095/live.mp3", 422, "public web address"},
		{"hls by extension", w.fx.pub.URL("radio.test", "/a.m3u8"), 422, "Tendshelf can't play this kind of stream yet."},
		{"hls by type", w.fx.pub.URL("radio.test", "/hls"), 422, "Tendshelf can't play this kind of stream yet."},
		{"web page", w.fx.pub.URL("radio.test", "/page"), 422, "not a radio stream"},
		{"not a url", "hello", 422, "web address"},
		{"missing", w.fx.pub.URL("radio.test", "/nothing"), 422, "does not exist"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := w.post("/radio/stations", w.ben, url.Values{"stream_url": {tc.url}})
			if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.text) {
				t.Errorf("%d %q", rec.Code, rec.Body)
			}
			// The refusal never repeats the address or anything the host said.
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
	// Both a slug and an address, or neither, is a plain refusal.
	for _, f := range []url.Values{{}, {"slug": {"cool-fm"}, "stream_url": {w.fx.liveURL()}}} {
		if rec := w.post("/radio/stations", w.ben, f); rec.Code != 422 {
			t.Errorf("%v: %d", f, rec.Code)
		}
	}
}

func TestRadioLiveStripsTitlesAndSetsHeaders(t *testing.T) {
	w := newRadioWorld(t)
	id := w.add(w.ben, w.fx.liveURL())
	_, audio := radioAudio()
	rec := w.get("/radio/live/"+id, w.ben, func(r *http.Request) { r.Header.Set("Range", "bytes=100-") })
	if rec.Code != http.StatusOK || !bytes.Equal(rec.Body.Bytes(), audio) {
		t.Fatalf("live: %d, %d bytes (want %d audio bytes, no title blocks)", rec.Code, rec.Body.Len(), len(audio))
	}
	h := rec.Header()
	if h.Get("Content-Type") != "audio/mpeg" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Content-Security-Policy") != "default-src 'none'; sandbox" || h.Get("Cache-Control") != "private, no-store" ||
		h.Get("Content-Range") != "" || h.Get("Accept-Ranges") != "none" {
		t.Errorf("headers %v", h)
	}
	for k, v := range h {
		joined := strings.Join(v, " ")
		if strings.HasPrefix(strings.ToLower(k), "icy-") || strings.Contains(joined, "Secret") || strings.Contains(joined, "tracker") || strings.Contains(k, "Cookie") {
			t.Errorf("upstream header leaked: %s: %s", k, joined)
		}
	}
	// No Range, no cookie and the title request reached the station.
	w.fx.mu.Lock()
	last := w.fx.reqs[len(w.fx.reqs)-1]
	w.fx.mu.Unlock()
	if last.Get("Range") != "" || last.Get("Cookie") != "" || last.Get("Icy-Metadata") != "1" {
		t.Errorf("upstream request headers %v", last)
	}
	// The latest title is forgotten when the last listener leaves, so the next poll shows nothing stale...
	rec = w.get("/hx/radio/now/"+id, w.ben)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `id="radio-now"`) || !strings.Contains(rec.Body.String(), "every 15s") {
		t.Errorf("now: %d %q", rec.Code, rec.Body)
	}
	// ...and while a listener is there, the cleaned, escaped title shows. Keep one open.
	w.fx.setHold(make(chan struct{}))
	defer close(w.fx.holdCh())
	srv := httptest.NewServer(w.a.h)
	defer srv.Close()
	req, _ := http.NewRequest("GET", srv.URL+"/radio/live/"+id, nil)
	req.AddCookie(w.ben)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	buf := make([]byte, 3*radioMetaint)
	if _, err := io.ReadFull(resp.Body, buf); err != nil {
		t.Fatal(err)
	}
	var now string
	for i := 0; i < 100; i++ {
		now = w.fx.svc.NowPlaying(id)
		if strings.Contains(now, "Second") {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if now != "Ann - Second <b>Song</b>" {
		t.Fatalf("title = %q", now)
	}
	rec = w.get("/hx/radio/now/"+id, w.ben)
	if !strings.Contains(rec.Body.String(), "Ann - Second &lt;b&gt;Song&lt;/b&gt;") || strings.Contains(rec.Body.String(), "<b>") {
		t.Errorf("now fragment: %q", rec.Body)
	}
	rec = w.get("/api/v1/radio/now/"+id, w.ben)
	var j struct{ Title string }
	json.Unmarshal(rec.Body.Bytes(), &j)
	if j.Title != "Ann - Second <b>Song</b>" {
		t.Errorf("api now: %d %q", rec.Code, rec.Body)
	}
}

func TestRadioLiveContentTypeAllowlist(t *testing.T) {
	w := newRadioWorld(t)
	id := w.add(w.ben, w.fx.liveURL())
	for _, tc := range []struct {
		ct     string
		status int
	}{
		{"audio/mpeg", 200}, {"audio/aacp", 200}, {"audio/ogg; codecs=opus", 200}, {"audio/flac", 200},
		{"text/html", 502}, {"image/svg+xml", 502}, {"application/javascript", 502}, {"", 502}, {"audio/x-mpegurl", 502},
	} {
		w.fx.mu.Lock()
		w.fx.liveCT = tc.ct
		w.fx.mu.Unlock()
		rec := w.get("/radio/live/"+id, w.ben)
		if rec.Code != tc.status {
			t.Errorf("%q: %d, want %d", tc.ct, rec.Code, tc.status)
		}
		if tc.status != 200 && (strings.Contains(rec.Body.String(), "radio.test") || strings.Contains(rec.Body.String(), "svg")) {
			t.Errorf("%q: error repeats upstream text: %q", tc.ct, rec.Body)
		}
	}
}

// liveGet starts a live request that ends with the returned cancel.
func (w *radioWorld) liveGet(ck *http.Cookie, path string) (*httptest.ResponseRecorder, context.CancelFunc, <-chan struct{}) {
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("GET", path, nil).WithContext(ctx)
	req.AddCookie(ck)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		w.a.h.ServeHTTP(rec, req)
	}()
	return rec, cancel, done
}

func (w *radioWorld) waitHits(n int64) {
	w.t.Helper()
	for i := 0; i < 250 && w.fx.hits.Load() < n; i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if w.fx.hits.Load() < n {
		w.t.Fatalf("station hits = %d, want %d", w.fx.hits.Load(), n)
	}
}

func TestRadioLiveSlotLimits(t *testing.T) {
	w := newRadioWorld(t)
	w.fx.setHold(make(chan struct{}))
	idBen := w.add(w.ben, w.fx.liveURL())
	idCy := w.add(w.cy, w.fx.liveURL())
	base := w.fx.hits.Load() // the adds probed the stream too
	_, cancel, done := w.liveGet(w.ben, "/radio/live/"+idBen)
	w.waitHits(base + 1)
	// One live station per person: a second request is 503 with plain words and Retry-After.
	rec := w.get("/radio/live/"+idBen, w.ben)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" || !strings.Contains(rec.Body.String(), "already listening") {
		t.Errorf("second live: %d %v %q", rec.Code, rec.Header(), rec.Body)
	}
	if rec = w.get("/api/v1/radio/live/"+idBen, w.ben); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("api second live: %d", rec.Code)
	}
	if w.fx.hits.Load() != base+1 {
		t.Errorf("a refused second live reached the station (%d)", w.fx.hits.Load())
	}
	// The 16 server slots are shared with podcast audio: fill the rest, and another person gets 503 too.
	var rels []func()
	for i := 0; i < podcast.MaxStreams-1; i++ {
		rel, ok := w.fx.pod.AcquireStream(fmt.Sprintf("filler%d", i/3))
		if !ok {
			rel, ok = w.fx.pod.AcquireGlobalStream()
		}
		if !ok {
			t.Fatalf("filler %d", i)
		}
		rels = append(rels, rel)
	}
	if rec = w.get("/radio/live/"+idCy, w.cy); rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "Too many streams") {
		t.Errorf("server full: %d %q", rec.Code, rec.Body)
	}
	for _, rel := range rels {
		rel()
	}
	// Ending the first live frees the person's place.
	cancel()
	<-done
	close(w.fx.holdCh())
	w.fx.setHold(nil)
	if rec = w.get("/radio/live/"+idBen, w.ben); rec.Code != http.StatusOK {
		t.Errorf("after the first ended: %d", rec.Code)
	}
}

func TestRadioLiveMaxSession(t *testing.T) {
	w := newRadioWorld(t)
	w.fx.svc.MaxSession = 300 * time.Millisecond
	w.fx.setHold(make(chan struct{}))
	defer close(w.fx.holdCh())
	id := w.add(w.ben, w.fx.liveURL())
	_, cancel, done := w.liveGet(w.ben, "/radio/live/"+id)
	defer cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the session did not end at its maximum")
	}
}

// TestRadioSavedStationPlaysWithOndaCastDown: the slug only helps adding; a saved station plays on its own.
func TestRadioSavedStationPlaysWithOndaCastDown(t *testing.T) {
	w := newRadioWorld(t)
	rec := w.post("/radio/stations", w.ben, url.Values{"slug": {"cool-fm"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("add by slug: %d %q", rec.Code, rec.Body)
	}
	var id, name, slug string
	if err := w.a.st.Reader().QueryRow(`SELECT id, name, ondacast_slug FROM radio_stations`).Scan(&id, &name, &slug); err != nil || name != "Cool FM" || slug != "cool-fm" {
		t.Fatalf("%v %q %q", err, name, slug)
	}
	w.fx.ondaDown.Store(true)
	_, audio := radioAudio()
	if rec = w.get("/radio/live/"+id, w.ben); rec.Code != 200 || !bytes.Equal(rec.Body.Bytes(), audio) {
		t.Errorf("live with OndaCast down: %d", rec.Code)
	}
	if rec = w.get("/api/v1/radio/stations", w.ben); rec.Code != 200 {
		t.Errorf("list with OndaCast down: %d", rec.Code)
	}
	// Adding by slug while it is down is a plain refusal (a slug looked up a moment ago is remembered for a short time);
	// an unknown slug is another one.
	if rec = w.post("/radio/stations", w.cy, url.Values{"slug": {"nope"}}); rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "isn't working") {
		t.Errorf("slug while down: %d %q", rec.Code, rec.Body)
	}
	w.fx.ondaDown.Store(false)
	if rec = w.post("/radio/stations", w.cy, url.Values{"slug": {"nope"}}); rec.Code != 422 {
		t.Errorf("unknown slug: %d", rec.Code)
	}
}

func TestRadioAPI(t *testing.T) {
	w := newRadioWorld(t)
	tok := w.bearer(w.benID)
	req := func(method, path, tok, body string) *httptest.ResponseRecorder {
		return deviceBearerReq(w.a, method, path, tok, func(r *http.Request) {
			if body != "" {
				r.Body = io.NopCloser(strings.NewReader(body))
				r.ContentLength = int64(len(body))
				r.Header.Set("Content-Type", "application/json")
			}
		})
	}
	rec := req("POST", "/api/v1/radio/stations", tok, fmt.Sprintf(`{"streamUrl":%q}`, w.fx.liveURL()))
	var created struct{ Station apiRadioStation }
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &created) != nil || created.Station.ID == "" {
		t.Fatalf("add: %d %q", rec.Code, rec.Body)
	}
	id := created.Station.ID
	if !created.Station.Favourite || created.Station.Codec != "mp3" || created.Station.LiveURL != "/api/v1/radio/live/"+id {
		t.Errorf("station %+v", created.Station)
	}
	if rec = req("POST", "/api/v1/radio/stations", tok, fmt.Sprintf(`{"streamUrl":%q}`, w.fx.liveURL())); rec.Code != http.StatusOK {
		t.Errorf("repeat add: %d", rec.Code)
	}
	if rec = req("POST", "/api/v1/radio/stations", tok, `{"streamUrl":"http://127.0.0.1:1/x"}`); rec.Code != 422 {
		t.Errorf("private add: %d %q", rec.Code, rec.Body)
	}
	if rec = req("POST", "/api/v1/radio/stations", tok, `{"streamUrl":"`+w.fx.pub.URL("radio.test", "/hls")+`"}`); rec.Code != 422 || !strings.Contains(rec.Body.String(), "can't play this kind of stream") {
		t.Errorf("hls add: %d %q", rec.Code, rec.Body)
	}
	rec = req("GET", "/api/v1/radio/stations", tok, "")
	var list struct{ Items []map[string]any }
	json.Unmarshal(rec.Body.Bytes(), &list)
	if rec.Code != 200 || len(list.Items) != 1 {
		t.Fatalf("list: %d %q", rec.Code, rec.Body)
	}
	if strings.Contains(rec.Body.String(), "radio.test") || strings.Contains(rec.Body.String(), "streamUrl") {
		t.Errorf("list shows the stream address: %q", rec.Body)
	}
	if rec = req("GET", "/api/v1/radio/live/"+id, tok, ""); rec.Code != 200 || rec.Header().Get("Content-Type") != "audio/mpeg" {
		t.Errorf("api live: %d %v", rec.Code, rec.Header())
	}
	rec = req("GET", "/api/v1/radio/recent", tok, "")
	list.Items = nil
	json.Unmarshal(rec.Body.Bytes(), &list)
	if rec.Code != 200 || len(list.Items) != 1 || list.Items[0]["lastPlayedAt"] == nil {
		t.Errorf("recent: %d %q", rec.Code, rec.Body)
	}
	other := w.bearer(w.cyID)
	if rec = req("DELETE", "/api/v1/radio/stations/"+id, other, ""); rec.Code != http.StatusNotFound {
		t.Errorf("stranger delete: %d", rec.Code)
	}
	if rec = req("GET", "/api/v1/radio/live/"+id, other, ""); rec.Code != http.StatusNotFound {
		t.Errorf("stranger live: %d", rec.Code)
	}
	if rec = req("DELETE", "/api/v1/radio/stations/"+id, tok, ""); rec.Code != http.StatusNoContent {
		t.Errorf("delete: %d", rec.Code)
	}
	if rec = req("GET", "/api/v1/radio/stations", tok, ""); !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("after delete: %q", rec.Body)
	}
	if rec = req("GET", "/api/v1/radio/stations", "", ""); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d", rec.Code)
	}
	if rec = req("POST", "/api/v1/radio/stations", tok, ""); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("no body: %d", rec.Code)
	}
}

func TestRadioAddRateLimitAndFavouriteCap(t *testing.T) {
	w := newRadioWorld(t)
	for i := 0; i < 30; i++ {
		if rec := w.post("/radio/stations", w.ben, url.Values{"stream_url": {"hello"}}); rec.Code != 422 {
			t.Fatalf("attempt %d: %d", i, rec.Code)
		}
	}
	rec := w.post("/radio/stations", w.ben, url.Values{"stream_url": {w.fx.liveURL()}})
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("31st attempt: %d %v", rec.Code, rec.Header())
	}
	// Another person has their own allowance.
	if rec = w.post("/radio/stations", w.cy, url.Values{"stream_url": {w.fx.liveURL()}}); rec.Code != http.StatusSeeOther {
		t.Errorf("other person: %d", rec.Code)
	}
}

func (w *radioWorld) bearer(uid string) string {
	w.t.Helper()
	var tok string
	err := w.a.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		tok, _, err = auth.NewDevices(w.a.st, nil).Issue(context.Background(), tx, uid, "Phone", auth.KindMobile)
		return err
	})
	if err != nil {
		w.t.Fatal(err)
	}
	return tok
}

// silentMP3Frame is one MPEG-1 Layer III frame (128 kbps, 44.1 kHz) of silence: 26 ms every browser decodes.
func silentMP3Frame() []byte {
	f := make([]byte, 417)
	copy(f, []byte{0xFF, 0xFB, 0x90, 0x64})
	return f
}

// serveSilence is an endless live station for the browser: silent MP3 frames with a title block between every four,
// slightly faster than real time. The title carries the connection's number, so a reconnect shows. Connections are counted
// (conns) and the open ones too (active), so a test can see a pause let go of the stream.
func (f *radioFix) serveSilence(w http.ResponseWriter, r *http.Request) {
	n := f.conns.Add(1)
	f.active.Add(1)
	defer f.active.Add(-1)
	const frames = 4
	w.Header().Set("Content-Type", "audio/mpeg")
	w.Header().Set("Icy-Metaint", fmt.Sprint(frames*417))
	w.WriteHeader(http.StatusOK)
	meta := []byte(fmt.Sprintf("StreamTitle='Ann - Song %d';", n))
	block := make([]byte, 1+((len(meta)+15)/16)*16)
	block[0] = byte(len(block) / 16)
	copy(block[1:], meta)
	frame := silentMP3Frame()
	for i := 0; ; i++ {
		for k := 0; k < frames; k++ {
			if _, err := w.Write(frame); err != nil {
				return
			}
		}
		// the title once, then empty blocks (a zero byte)
		blk := []byte{0}
		if i == 0 {
			blk = block
		}
		if _, err := w.Write(blk); err != nil {
			return
		}
		w.(http.Flusher).Flush()
		select {
		case <-r.Context().Done():
			return
		case <-time.After(80 * time.Millisecond):
		}
	}
}
