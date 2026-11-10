//go:build dev

package web

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// TestEpisodeDownloadForOffline: Download on an episode stores its audio, player fragment and chapters in the person's
// cache through the download manager; with the network gone the worker answers a Range request for that episode from the
// cache while every other podcast address goes to the (dead) network; removing the download leaves nothing, and sign-out
// clears the person's caches.
// Run by: TENDSHELF_TEST_BROWSER=<chromium> go test -tags dev -run EpisodeDownloadForOffline ./internal/web/
func TestEpisodeDownloadForOffline(t *testing.T) {
	b := chromium(t)
	w := podWavWorld(t)
	// The server can be cut off: a request to it while down is dropped, like a phone with no signal.
	var down atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if down.Load() {
			if conn, _, err := rw.(http.Hijacker).Hijack(); err == nil {
				conn.Close()
			}
			return
		}
		r.AddCookie(w.ben)
		w.a.h.ServeHTTP(rw, r)
	}))
	t.Cleanup(srv.Close)
	base := srv.URL
	tab := b.NewTab(1280, 800, false, false)
	ep, other := w.eps[0], w.eps[1]

	tab.Navigate(base + "/podcasts/episodes/" + ep)
	tab.WaitFor(`window.tsDownloads !== undefined && navigator.serviceWorker.controller`, 20*time.Second)
	// The page's own control is drawn by the download manager once it knows the state.
	tab.WaitFor(`document.querySelector('[data-ts-download="`+ep+`"]') && !document.querySelector('[data-ts-download="`+ep+`"]').hidden`, 10*time.Second)
	tab.Eval(`document.querySelector('[data-ts-download="`+ep+`"]').click()`, nil)
	ofdWaitState(t, tab, ep, "ready")

	rec := ofdRecord(tab, ep)
	if !rec.Found || len(rec.Files) != 1 || int(rec.Files[0].Size) != len(w.fx.audio) {
		t.Fatalf("record: %+v (audio is %d bytes)", rec, len(w.fx.audio))
	}
	keys := ofdKeys(tab)
	for _, want := range []string{"/podcasts/audio/" + ep, "/hx/player/episode/" + ep, "/podcasts/chapters/" + ep} {
		if !slices.Contains(keys, want) {
			t.Errorf("the cache lacks %s (has %v)", want, keys)
		}
	}
	// The status link replaces the button once the download is ready.
	tab.WaitFor(`document.querySelector('[data-ts-download="`+ep+`"]').hidden && !document.querySelector('[data-ts-download-status]').hidden`, 10*time.Second)

	// The worker looks again when told the cache changed; give it a moment before the network goes.
	time.Sleep(500 * time.Millisecond)
	down.Store(true)
	if err := tab.Call("Network.emulateNetworkConditions", map[string]any{"offline": true, "latency": 0, "downloadThroughput": -1, "uploadThroughput": -1}, nil); err != nil {
		t.Fatal(err)
	}
	var rng struct {
		Status       int    `json:"status"`
		ContentRange string `json:"contentRange"`
		Bytes        int    `json:"bytes"`
	}
	tab.Eval(`fetch("/podcasts/audio/`+ep+`", {headers: {Range: "bytes=100-199"}}).then(async (r) => ({status: r.status, contentRange: r.headers.get("Content-Range"), bytes: (await r.arrayBuffer()).byteLength}))`, &rng)
	if rng.Status != 206 || rng.Bytes != 100 || !strings.HasPrefix(rng.ContentRange, "bytes 100-199/") {
		t.Errorf("offline range request: %+v", rng)
	}
	var fragment bool
	tab.Eval(`fetch("/hx/player/episode/`+ep+`").then((r) => r.ok)`, &fragment)
	if !fragment {
		t.Error("the player fragment was not answered offline")
	}
	// Anything else under /podcasts/ is not the worker's: it goes to the network, which is down.
	for _, path := range []string{"/podcasts/audio/" + other, "/podcasts/shows/" + w.show, "/podcasts/episodes/" + ep, "/hx/player/episode/" + other, "/radio/live/rs_x"} {
		var failed bool
		tab.Eval(`fetch(`+cdp.Quote(path)+`).then(() => false, () => true)`, &failed)
		if !failed {
			t.Errorf("%s was answered offline", path)
		}
	}
	// The downloaded episode plays offline in the real player.
	playerClickSel(t, tab, `[data-ts-play="`+ep+`"]`)
	tab.WaitFor(`window.tsPlayer.state().bookId === `+cdp.Quote(ep)+` && window.tsPlayer.state().playing && window.tsPlayer.state().positionMs > 500`, 20*time.Second)
	tab.Eval(`window.tsPlayer.pause()`, nil)
	down.Store(false)
	if err := tab.Call("Network.emulateNetworkConditions", map[string]any{"offline": false, "latency": 0, "downloadThroughput": -1, "uploadThroughput": -1}, nil); err != nil {
		t.Fatal(err)
	}

	// Remove: nothing of the episode stays in the cache and the worker stops answering for it.
	tab.Eval(`window.tsDownloads.remove(`+cdp.Quote(ep)+`)`, nil)
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(200 * time.Millisecond) {
		if !slices.ContainsFunc(ofdKeys(tab), func(k string) bool {
			return strings.Contains(k, "/podcasts/") || strings.Contains(k, "/hx/player/episode/")
		}) {
			break
		}
	}
	if keys = ofdKeys(tab); slices.ContainsFunc(keys, func(k string) bool { return strings.Contains(k, ep) }) {
		t.Errorf("the cache keeps %v after Remove", keys)
	}

	// Sign-out (the page opened with ?signed_out=1) clears the person's caches and download records, episodes too.
	tab.Eval(`window.tsDownloads.start(`+cdp.Quote(ep)+`)`, nil)
	ofdWaitState(t, tab, ep, "ready")
	tab.Navigate(base + "/podcasts?signed_out=1")
	tab.WaitFor(`document.readyState === "complete"`, 10*time.Second)
	time.Sleep(time.Second)
	if keys = ofdKeys(tab); slices.ContainsFunc(keys, func(k string) bool { return strings.Contains(k, ep) }) {
		t.Errorf("the cache keeps %v after sign-out", keys)
	}
	if rec := ofdRecord(tab, ep); rec.Found {
		t.Errorf("the download record survived sign-out: %+v", rec)
	}
}
