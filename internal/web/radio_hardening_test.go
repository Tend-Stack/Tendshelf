package web

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/radio"
)

// endlessReader never ends, so a relay of it only stops when a write fails.
type endlessReader struct{}

func (endlessReader) Read(p []byte) (int, error) { return len(p), nil }

// stalledClient connects, sends a request, reads the response head and then reads nothing more.
func stalledClient(t *testing.T, srv *httptest.Server, req string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if tc, ok := conn.(*net.TCPConn); ok {
		tc.SetReadBuffer(4096)
	}
	if _, err := fmt.Fprint(conn, req); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReaderSize(conn, 256)
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if line == "\r\n" {
			return conn
		}
	}
}

// TestRelayCopyStalledClientEnds covers the helper both relays share (radio live and podcast episode audio):
// a client that stops reading ends the copy within the write deadline instead of holding it forever.
func TestRelayCopyStalledClientEnds(t *testing.T) {
	old := relayWriteDeadline
	relayWriteDeadline = 300 * time.Millisecond
	defer func() { relayWriteDeadline = old }()
	done := make(chan error, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
		done <- relayCopy(w, endlessReader{}, false)
	}))
	defer srv.Close()
	stalledClient(t, srv, "GET / HTTP/1.1\r\nHost: x\r\n\r\n")
	select {
	case err := <-done:
		if err == nil {
			t.Error("relay ended without an error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("a client that stopped reading held the relay")
	}
}

// TestRadioLiveStalledClientFreesSlot: the same through the real route; the person's live place comes back.
func TestRadioLiveStalledClientFreesSlot(t *testing.T) {
	old := relayWriteDeadline
	relayWriteDeadline = 300 * time.Millisecond
	defer func() { relayWriteDeadline = old }()
	w := newRadioWorld(t)
	id := w.add(w.ben, w.fx.pub.URL("radio.test", "/endless.mp3"))
	srv := httptest.NewServer(w.a.h)
	defer srv.Close()
	stalledClient(t, srv, "GET /radio/live/"+id+" HTTP/1.1\r\nHost: x\r\nCookie: "+w.ben.String()+"\r\n\r\n")
	deadline := time.Now().Add(10 * time.Second)
	for {
		rel, err := w.fx.svc.Acquire(w.benID, id)
		if err == nil {
			rel()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the stalled client still holds the live place: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestRadioLiveHeadIsNotAPlay: HEAD asks about the stream and must not enter the person's recent plays.
func TestRadioLiveHeadIsNotAPlay(t *testing.T) {
	w := newRadioWorld(t)
	id := w.add(w.ben, w.fx.liveURL())
	plays := func() int {
		var n int
		if err := w.a.st.Reader().QueryRow(`SELECT count(*) FROM radio_plays WHERE user_id = ?`, w.benID).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := plays()
	if rec := w.a.do(http.MethodHead, "/radio/live/"+id, nil, nil, w.ben); rec.Code != http.StatusOK {
		t.Fatalf("head: %d", rec.Code)
	}
	if plays() != before {
		t.Error("a HEAD recorded a play")
	}
	w.get("/radio/live/"+id, w.ben)
	if plays() != before+1 {
		t.Error("a GET did not record a play")
	}
}

// TestRadioLiveEndsWhenRadioTurnedOff: a running relay stops within a few seconds of the owner's switch.
func TestRadioLiveEndsWhenRadioTurnedOff(t *testing.T) {
	old := radioEnabledEvery
	radioEnabledEvery = 50 * time.Millisecond
	defer func() { radioEnabledEvery = old }()
	w := newRadioWorld(t)
	w.fx.setHold(make(chan struct{}))
	defer close(w.fx.holdCh())
	id := w.add(w.ben, w.fx.liveURL())
	base := w.fx.hits.Load()
	_, cancel, done := w.liveGet(w.ben, "/radio/live/"+id)
	defer cancel()
	w.waitHits(base + 1)
	w.fx.enable(t, w.a, false)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the relay kept running after radio was turned off")
	}
}

// TestRadioAddParallelIsBounded: the 30 an hour budget holds under parallel requests; only 30 reach a station.
func TestRadioAddParallelIsBounded(t *testing.T) {
	w := newRadioWorld(t)
	tok := w.a.tokenFor(t, w.ben)
	base := w.fx.hits.Load()
	var wg sync.WaitGroup
	var mu sync.Mutex
	codes := map[int]int{}
	for i := 0; i < 60; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rec := w.a.do(http.MethodPost, "/radio/stations", url.Values{"_csrf": {tok}, "stream_url": {w.fx.liveURL()}}, nil, w.ben)
			mu.Lock()
			codes[rec.Code]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if codes[http.StatusTooManyRequests] != 30 {
		t.Errorf("answers = %v, want exactly 30 refused", codes)
	}
	if got := w.fx.hits.Load() - base; got > 30 {
		t.Errorf("%d probes for a budget of 30", got)
	}
}

// TestRadioAddFullListDoesNotProbe: at 100 favourites nothing is dialled.
func TestRadioAddFullListDoesNotProbe(t *testing.T) {
	w := newRadioWorld(t)
	for i := 0; i < radio.MaxFavourites; i++ {
		if _, _, err := w.fx.svc.Save(t.Context(), w.benID, radio.Input{StreamURL: fmt.Sprintf("http://full.test/s%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	base := w.fx.hits.Load()
	rec := w.post("/radio/stations", w.ben, url.Values{"stream_url": {w.fx.liveURL()}})
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), strings.Fields(radioErrTooMany)[0]) {
		t.Errorf("full list: %d %q", rec.Code, rec.Body)
	}
	if w.fx.hits.Load() != base {
		t.Error("a full list still probed the station")
	}
}

// TestRadioAddKeptStationOnFullList: adding a station already on a full list answers with it, with no probe.
func TestRadioAddKeptStationOnFullList(t *testing.T) {
	w := newRadioWorld(t)
	for i := 0; i < radio.MaxFavourites; i++ {
		if _, _, err := w.fx.svc.Save(t.Context(), w.benID, radio.Input{StreamURL: fmt.Sprintf("http://full.test/s%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	base := w.fx.hits.Load()
	rec := w.post("/radio/stations", w.ben, url.Values{"stream_url": {"http://full.test/s7"}})
	if rec.Code >= 400 {
		t.Errorf("a kept station on a full list: %d %q", rec.Code, rec.Body)
	}
	if w.fx.hits.Load() != base {
		t.Error("adding a kept station probed it again")
	}
}
