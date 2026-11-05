//go:build dev

package web

import (
	"slices"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The worker answers /stream/ only for a file whose download is stored; every other stream goes to
// the server directly (Firefox hung the site when the worker proxied long Range requests for books
// that were not downloaded). Run by:
// TENDSHELF_TEST_BROWSER=<chromium> go test -tags dev -run OfflineStream ./internal/web/

// offlineStreamProbeJS fetches a stream file range and reports whether the worker's fetch handler
// answered it (the response is marked by the worker: it never sets Content-Length by itself, while
// a stored answer is built with Cache-Control: no-store; the server's answer is not).
const offlineStreamProbeJS = `window.__probe = async (path) => {
  const res = await fetch(path, { headers: { Range: "bytes=0-99" }, cache: "no-store" });
  await res.arrayBuffer();
  const entry = performance.getEntriesByName(new URL(path, location.href).href).pop();
  return { status: res.status, worker: res.headers.get("Cache-Control") === "no-store", workerStart: entry ? entry.workerStart : -1 };
}`

type streamProbe struct {
	Status      int
	Worker      bool
	WorkerStart float64
}

func (e *offlineEnv) probe(t *testing.T, tab *cdp.Tab, path string) streamProbe {
	t.Helper()
	tab.Eval(offlineStreamProbeJS, nil)
	var p streamProbe
	tab.Eval(`window.__probe(`+cdp.Quote(path)+`)`, &p)
	return p
}

// streams is what the worker believes is stored, after it has looked.
func (e *offlineEnv) streams(t *testing.T, tab *cdp.Tab) []string {
	t.Helper()
	var s []string
	tab.Eval(`window.__streams().then((all) => all && all.filter((k) => k.startsWith("/stream/")))`, &s)
	return s
}

// restartWorker stops the worker the way the browser does when idle; the next event starts it again
// with an empty memory.
func restartWorker(t *testing.T, tab *cdp.Tab) {
	t.Helper()
	if err := tab.Call("ServiceWorker.enable", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := tab.Call("ServiceWorker.stopAllWorkers", nil, nil); err != nil {
		t.Fatal(err)
	}
}

// A book that is not stored: the worker does not answer its stream, the server does.
func TestOfflineStreamNotStoredBypassesWorker(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)
	tab := e.offlineTab(t, b)
	tab.Eval(offlineSeedJS, nil)
	tab.WaitFor(`window.__streams !== undefined`, 5*time.Second)
	if s := e.streams(t, tab); s == nil || len(s) != 0 {
		t.Fatalf("stored streams = %v, want known and empty", s)
	}
	p := e.probe(t, tab, "/stream/"+e.files[0].ID)
	if p.Status != 206 || p.Worker {
		t.Errorf("not stored: %+v, want the server's answer, not the worker's", p)
	}
}

// A stored book plays from chunks, online and offline.
func TestOfflineStreamStoredFromChunks(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)
	tab := e.offlineTab(t, b)
	e.seed(t, tab)
	want := []string{"/stream/" + e.files[0].ID, "/stream/" + e.files[1].ID}
	got := e.streams(t, tab)
	slices.Sort(got)
	slices.Sort(want)
	if !slices.Equal(got, want) {
		t.Fatalf("stored streams = %v, want %v", got, want)
	}
	var all []string
	tab.Eval(`window.__streams()`, &all)
	if !slices.Contains(all, "/hx/player/"+e.book.ID) {
		t.Errorf("the worker does not list the player fragment: %v", all)
	}
	for _, down := range []bool{false, true} {
		e.setDown(t, tab, down)
		p := e.probe(t, tab, "/stream/"+e.files[1].ID)
		if p.Status != 206 || !p.Worker {
			t.Errorf("down=%v: %+v, want the worker's answer from chunks", down, p)
		}
	}
}

// A worker that has just started while offline still knows a stored book, and plays it.
func TestOfflineStreamRestartedWorkerOffline(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)
	tab := e.offlineTab(t, b)
	e.seed(t, tab)
	e.setDown(t, tab, true)
	restartWorker(t, tab)
	// The first request may arrive before the worker has looked: it is still answered from chunks.
	p := e.probe(t, tab, "/stream/"+e.files[0].ID)
	if p.Status != 206 || !p.Worker {
		t.Errorf("restarted worker, offline: %+v, want the worker's answer from chunks", p)
	}
	if s := e.streams(t, tab); len(s) != 2 {
		t.Errorf("after the restart the worker lists %v, want both files", s)
	}
}

// Sign-out clears the list: the stored book is no longer answered.
func TestOfflineStreamSignOutClears(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)
	tab := e.offlineTab(t, b)
	e.seed(t, tab)
	tab.Navigate(e.URL + "/libraries?signed_out=1")
	tab.WaitFor(`window.tsPlayer !== undefined && navigator.serviceWorker.controller`, 20*time.Second)
	tab.Eval(offlineSeedJS, nil)
	tab.WaitFor(`window.__streams !== undefined`, 5*time.Second)
	tab.WaitFor(`(async () => { const s = await window.__streams(); return s && s.length === 0; })()`, 10*time.Second)
	if p := e.probe(t, tab, "/stream/"+e.files[0].ID); p.Worker {
		t.Errorf("after sign-out: %+v, want the server's answer", p)
	}
}

// A removed download stops being answered by the worker.
func TestOfflineStreamRemovedDownload(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)
	tab := e.offlineTab(t, b)
	e.downloadsPage(t, tab)
	tab.Eval(offlineSeedJS, nil)
	e.startDownload(t, tab)
	tab.WaitFor(`window.tsDownloads.list()[0]?.state === "ready"`, 40*time.Second)
	tab.WaitFor(`(async () => (await window.__streams()).filter((k) => k.startsWith("/stream/")).length === 2)()`, 10*time.Second)
	if p := e.probe(t, tab, "/stream/"+e.files[0].ID); !p.Worker {
		t.Fatalf("downloaded: %+v, want the worker's answer", p)
	}
	tab.Eval(`window.tsDownloads.remove(`+cdp.Quote(e.book.ID)+`)`, nil)
	tab.WaitFor(`(async () => (await window.__streams()).length === 0)()`, 15*time.Second)
	if p := e.probe(t, tab, "/stream/"+e.files[0].ID); p.Worker || p.Status != 206 {
		t.Errorf("removed: %+v, want the server's answer", p)
	}
}
