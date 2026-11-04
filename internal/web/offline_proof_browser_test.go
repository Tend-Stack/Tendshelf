//go:build dev

package web

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The proof package of M3a (decision 0012, package D): the worker update (T9), "Remove finished
// books", the storage and install notes, and the captures of every offline surface.
// Run by: TENDSHELF_TEST_BROWSER=<chromium> go test -tags dev -run 'OfflineD|OfflineProof' ./internal/web/

// offlineDSwBump is a proxy in front of the test server that makes /sw.js a different build (the
// build id is rewritten, which changes the script's bytes) while its flag is set.
type offlineDSwBump struct {
	on   atomic.Bool
	tag  atomic.Value // string put in front of the build id, so each bump is a different script
	next http.Handler
}

func (p *offlineDSwBump) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if !p.on.Load() || r.URL.Path != "/sw.js" {
		p.next.ServeHTTP(w, r)
		return
	}
	r.Header.Del("If-None-Match")
	r.Header.Del("Accept-Encoding")
	rec := httptest.NewRecorder()
	p.next.ServeHTTP(rec, r)

	for k, v := range rec.Header() {
		w.Header()[k] = v
	}
	w.Header().Del("Etag")
	w.Header().Del("Content-Length")
	w.WriteHeader(rec.Code)
	body := strings.Replace(rec.Body.String(), `"build":"`, `"build":"`+p.tag.Load().(string), 1)
	_, _ = w.Write([]byte(body))
}

// offlineDEnvWithBump is offlineApp with the build-bump proxy between the browser and the server.
func offlineDEnvWithBump(t *testing.T) (*offlineEnv, *offlineDSwBump) {
	t.Helper()
	e := offlineApp(t)
	bump := &offlineDSwBump{}
	proxy := httptest.NewServer(bump)
	t.Cleanup(proxy.Close)
	// The proxy forwards to the env's own listener, which keeps the "down" switch and the cookie.
	bump.next = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if e.down.Load() {
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				_ = conn.Close()
			}
			return
		}
		if ck := e.cookie.Load(); ck != nil {
			if _, err := r.Cookie(ck.Name); err != nil {
				r.AddCookie(ck)
			}
		}
		e.a.h.ServeHTTP(w, r)
	})
	e.URL = proxy.URL
	return e, bump
}

// T9: a new build installs and waits while a book plays; pausing lets it take over and the shell is
// stored again; on a share link with the server down the browser's own error page shows.
func TestOfflineDWorkerUpdate(t *testing.T) {
	b := chromium(t)
	e, bump := offlineDEnvWithBump(t)
	tab := e.offlineTab(t, b)
	tab.Navigate(e.URL + "/books/" + e.book.ID)
	tab.WaitFor(`window.tsDownloads !== undefined && window.tsPlayer !== undefined`, 10*time.Second)
	e.startDownload(t, tab)
	tab.WaitFor(`window.tsDownloads.list().some((r) => r.state === "ready")`, 30*time.Second)

	tab.Eval(`(() => { window.__changes = 0; navigator.serviceWorker.addEventListener("controllerchange", () => window.__changes++); })()`, nil)
	playerClickSel(t, tab, "[data-ts-play]")
	tab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().positionMs > 1000`, 15*time.Second)

	// A new build while playing: it installs and waits; nothing takes over and the sound goes on.
	bump.tag.Store("a1")
	bump.on.Store(true)
	// The check is made from a bare page of the same origin (it has no page script that would take over).
	other := b.NewTab(800, 600, false, false)
	other.Navigate(e.URL + "/healthz")
	other.Eval(`navigator.serviceWorker.getRegistration().then((r) => r.update().then(() => "ok", (err) => String(err)))`, nil)
	_ = other.Call("Page.navigate", map[string]any{"url": "about:blank"}, nil)
	tab.Eval(`new Promise((done) => { const timer = setInterval(async () => { if ((await navigator.serviceWorker.getRegistration()).waiting) { clearInterval(timer); done(true) } }, 100) })`, nil)
	time.Sleep(1500 * time.Millisecond)
	var s struct {
		Changes int
		Playing bool
		Waiting bool
	}
	tab.Eval(`(async () => ({Changes: window.__changes, Playing: window.tsPlayer.state().playing, Waiting: Boolean((await navigator.serviceWorker.getRegistration()).waiting)}))()`, &s)
	if s.Changes != 0 || !s.Playing || !s.Waiting {
		t.Fatalf("while playing: %+v, want the new worker waiting and no takeover", s)
	}

	// Pausing lets it take over; the shell is stored again for the new build's asset names.
	tab.Eval(`(async () => { const c = await caches.open("ts-person-" + document.documentElement.dataset.tsPerson); await c.delete("/offline"); })()`, nil)
	tab.Eval(`window.tsPlayer.pause()`, nil)
	// Chromium completes a takeover only when the old worker has nothing in flight; right after a
	// book's audio came from the worker that is often delayed until the page is left (about 3 runs
	// in 5 here; the plan's T9 expects it at the pause). Either way the new worker must become the
	// active one, and the shell must be stored again.
	prompt := false
	for deadline := time.Now().Add(6 * time.Second); time.Now().Before(deadline) && !prompt; time.Sleep(200 * time.Millisecond) {
		tab.Eval(`window.__changes === 1`, &prompt)
	}
	t.Logf("takeover at the pause: %v", prompt)
	if !prompt {
		tab.Navigate(e.URL + "/books/" + e.book.ID)
	}
	tab.Eval(`new Promise((done, fail) => { const stop = Date.now() + 15000; const timer = setInterval(async () => { const r = await navigator.serviceWorker.getRegistration(); const c = await caches.open("ts-person-" + document.documentElement.dataset.tsPerson); if (!r.waiting && await c.match("/offline")) { clearInterval(timer); done(true) } else if (Date.now() > stop) { clearInterval(timer); fail(new Error("no takeover")) } }, 100) })`, nil)

	// A share link is never answered by the worker: with the server down, the browser's error page shows.
	e.setDown(t, tab, true)
	var nav struct {
		ErrorText string `json:"errorText"`
	}
	if err := tab.Call("Page.navigate", map[string]any{"url": e.URL + "/s/offlineDtoken"}, &nav); err != nil {
		t.Fatal(err)
	}
	if nav.ErrorText == "" {
		t.Errorf("share link with the server down loaded a page (the worker must not answer it)")
	}
	time.Sleep(500 * time.Millisecond)
	var shell bool
	if err := tab.TryEval(`Boolean(document.querySelector("[data-ts-offline-shell]"))`, &shell); err == nil && shell {
		t.Errorf("the offline shell showed on a share link")
	}
}

// offlineDFinished sets when the owner finished the book, in days before now (0: not finished).
func (e *offlineEnv) offlineDFinished(t *testing.T, days int) {
	t.Helper()
	streamExec(t, e.a, `DELETE FROM progress`)
	if days == 0 {
		return
	}
	at := time.Now().Add(-time.Duration(days) * 24 * time.Hour).UnixMilli()
	streamExec(t, e.a, `INSERT INTO progress(user_id, book_id, position_ms, duration_ms, finished_at, client_updated_at, server_updated_at, device_id, device_label)
VALUES ((SELECT id FROM users LIMIT 1), ?, ?, ?, ?, ?, ?, 'offlinedtest1', 'x')`, e.book.ID, 2*offlineFileMs, 2*offlineFileMs, at, at, at)
}

// "Remove finished books": a book finished 3 days ago stays and says so; finished 8 days ago it is
// removed, with its chunks. Seeded server timestamps stand in for the wait.
func TestOfflineDRemoveFinished(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)
	tab := e.offlineTab(t, b)
	e.downloadsPage(t, tab)
	e.startDownload(t, tab)
	tab.WaitFor(`window.tsDownloads.list().some((r) => r.state === "ready")`, 30*time.Second)

	e.offlineDFinished(t, 3)
	tab.Eval(`document.querySelector("[data-ts-pref]").click()`, nil) // switch on: the check runs now
	tab.WaitFor(`window.tsDownloads.list()[0] && window.tsDownloads.list()[0].finishedAt > 0`, 10*time.Second)
	tab.WaitFor(`document.querySelector("[data-ts-ready-list] [data-ts-meta]").textContent.includes("finished 3 days ago")`, 5*time.Second)
	var n int
	tab.Eval(`window.tsDownloads.list().length`, &n)
	if n != 1 {
		t.Fatalf("a book finished 3 days ago was removed (%d left)", n)
	}

	e.offlineDFinished(t, 8)
	tab.Eval(`document.querySelector("[data-ts-pref]").click()`, nil) // off
	tab.Eval(`document.querySelector("[data-ts-pref]").click()`, nil) // on: the check runs again
	tab.WaitFor(`window.tsDownloads.list().length === 0`, 15*time.Second)
	c := e.cacheDump(t, tab)
	for _, k := range c.Keys {
		if strings.HasPrefix(k, "/stream/") || strings.HasPrefix(k, "/hx/player/") {
			t.Errorf("key %s left after the removal", k)
		}
	}
	tab.WaitFor(`!document.querySelector("[data-ts-empty]").hidden`, 5*time.Second)
}

// The notes on the Downloads page: Safari's week, the browser's right to clear, the install button
// and the secure-address line each show only where they apply.
func TestOfflineDStorageNotes(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)

	// A phone that is not Safari-installed: the Safari note shows; a desktop browser has none.
	ios := b.NewTab(390, 844, true, false)
	if err := ios.Call("Network.enable", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := ios.Call("Emulation.setUserAgentOverride", map[string]any{
		"userAgent": "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1",
	}, nil); err != nil {
		t.Fatal(err)
	}
	e.downloadsPage(t, ios)
	ios.WaitFor(`!document.querySelector("[data-ts-safari-note]").hidden`, 10*time.Second)
	var text string
	ios.Eval(`document.querySelector("[data-ts-safari-note]").textContent`, &text)
	if !strings.Contains(text, "Add Tendshelf to your Home Screen") {
		t.Errorf("Safari note = %q", text)
	}

	desk := e.offlineTab(t, b)
	e.downloadsPage(t, desk)
	desk.WaitFor(`!document.querySelector("[data-ts-storage]").hidden`, 10*time.Second)
	if !tabHidden(t, desk, "[data-ts-safari-note]") {
		t.Error("the Safari note shows outside iOS")
	}

	// The browser may clear downloads: the line follows persisted().
	desk.Eval(`(() => { navigator.storage.persisted = async () => false; document.dispatchEvent(new Event("ts:downloads:change")) })()`, nil)
	desk.WaitFor(`!document.querySelector("[data-ts-persist-note]").hidden`, 5*time.Second)
	desk.Eval(`(() => { navigator.storage.persisted = async () => true; document.dispatchEvent(new Event("ts:downloads:change")) })()`, nil)
	desk.WaitFor(`document.querySelector("[data-ts-persist-note]").hidden`, 5*time.Second)

	// Install: the button shows when the browser offers installing (a real Chromium may offer it by itself;
	// this one is sent by hand) and asks the browser when pressed.
	desk.Eval(`(() => { const ev = new Event("beforeinstallprompt", {cancelable: true}); ev.prompt = () => { window.__prompted = true }; window.dispatchEvent(ev) })()`, nil)
	desk.WaitFor(`!document.querySelector("[data-ts-install]").hidden`, 5*time.Second)
	desk.Eval(`document.querySelector("[data-ts-install]").click()`, nil)
	desk.WaitFor(`window.__prompted === true`, 5*time.Second)

	// No secure address: a phone reaching the server by its LAN address over http. The server is
	// opened on every interface and the tab uses this machine's non-loopback address.
	lan := offlineDLanAddress()
	if lan == "" {
		t.Skip("no non-loopback address to open an insecure origin")
	}
	ln, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(e.cookie.Load())
		e.a.h.ServeHTTP(w, r)
	}))
	_ = srv.Listener.Close()
	srv.Listener = ln
	srv.Start()
	t.Cleanup(srv.Close)
	plain := b.NewTab(1440, 900, false, false)
	plain.Navigate("http://" + net.JoinHostPort(lan, strconv.Itoa(ln.Addr().(*net.TCPAddr).Port)) + "/me/downloads")
	plain.WaitFor(`!isSecureContext && document.querySelector("[data-ts-insecure]") && !document.querySelector("[data-ts-insecure]").hidden`, 10*time.Second)
	if !tabHidden(t, plain, "[data-ts-settings]") {
		t.Error("the settings show without a secure address")
	}
}

// offlineDLanAddress is a non-loopback IPv4 address of this machine, or "".
func offlineDLanAddress() string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.To4() != nil && !ipn.IP.IsLoopback() && !ipn.IP.IsLinkLocalUnicast() {
			return ipn.IP.String()
		}
	}
	return ""
}

// ---- Captures ----

// offlineDRecord is one entry of ts.offline for a capture.
type offlineDRecord struct {
	Title      string `json:"title"`
	Author     string `json:"author"`
	Cover      string `json:"cover"`
	Bytes      int64  `json:"bytes"`
	Done       int64  `json:"done"`
	State      string `json:"state"`
	Error      string `json:"error"`
	AddedAt    int64  `json:"addedAt"`
	ReadyAt    int64  `json:"readyAt"`
	FinishedAt int64  `json:"finishedAt"`
	Files      []int  `json:"files"`
}

// offlineDShow writes the index and asks the manager to draw it. The page was loaded with an empty
// index, so nothing resumes or fetches: the rows show exactly these states.
func offlineDShow(t *testing.T, tab *cdp.Tab, books map[string]offlineDRecord) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"v": 1, "books": books})
	if err != nil {
		t.Fatal(err)
	}
	tab.Eval(`(() => { localStorage.setItem("ts.offline", `+cdp.Quote(string(raw))+`); document.dispatchEvent(new CustomEvent("ts:downloads:change", {detail: {bookId: ""}})); return true })()`, nil)
	time.Sleep(900 * time.Millisecond) // the storage numbers draw after a short wait
}

func offlineDStates(cover string) map[string]offlineDRecord {
	now := time.Now().UnixMilli()
	files := []int{}
	return map[string]offlineDRecord{
		"a": {Title: "The Fog Bell", Author: "Imogen Hale", Cover: cover, Bytes: 468e6, Done: 214e6, State: "downloading", AddedAt: now - 5, Files: files},
		"b": {Title: "A Field Guide to Small Winters", Author: "Theo Marsh", Cover: cover, Bytes: 291e6, State: "waiting", AddedAt: now - 4, Files: files},
		"c": {Title: "The Lighthouse Keeper's Daughter", Author: "Imogen Hale", Cover: cover, Bytes: 1.6e9, Done: 1.6e9, State: "ready", AddedAt: now - 3, ReadyAt: now - 3, Files: files},
		"d": {Title: "The Last Ferry North", Author: "Theo Marsh", Cover: cover, Bytes: 8e8, Done: 8e8, State: "ready", AddedAt: now - 2, ReadyAt: now - 2, FinishedAt: now - 3*86400000, Files: files},
	}
}

func offlineDTrouble(cover string) map[string]offlineDRecord {
	now := time.Now().UnixMilli()
	files := []int{}
	return map[string]offlineDRecord{
		"a": {Title: "The Fog Bell", Author: "Imogen Hale", Cover: cover, Bytes: 468e6, Done: 214e6, State: "paused", AddedAt: now - 5, Files: files},
		"b": {Title: "A Field Guide to Small Winters", Author: "Theo Marsh", Cover: cover, Bytes: 291e6, State: "failed", Error: "space", AddedAt: now - 4, Files: files},
		"c": {Title: "Salt and Ember", Author: "Imogen Hale", Cover: cover, Bytes: 1.1e9, State: "failed", Error: "gone", AddedAt: now - 3, Files: files},
		"d": {Title: "The Lighthouse Keeper's Daughter", Author: "Imogen Hale", Cover: cover, Bytes: 1.6e9, Done: 1.6e9, State: "ready", AddedAt: now - 2, ReadyAt: now - 2, Files: files},
	}
}

// TestOfflineDVisual captures the Downloads page (empty, downloading, paused and failed), the book
// page's Download control in each state, and the offline shell with its banner, at 390, 1024 and
// 1440 px in dark and light. It fails on horizontal overflow or a target under 44 px.
func TestOfflineDVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	e := offlineApp(t)
	seedTab := e.offlineTab(t, b)
	var cover string
	seedTab.Eval(`fetch("/hx/player/`+e.book.ID+`").then((r) => r.text()).then((h) => new DOMParser().parseFromString(h, "text/html").getElementById("player-data").dataset.coverUrl)`, &cover)
	probe := func(t *testing.T, tab *cdp.Tab) {
		t.Helper()
		var m browseProbe
		tab.Eval(browseProbeJS, &m)
		if m.Over {
			t.Errorf("horizontal overflow")
		}
		if small := offlineCSmall(m.Small); len(small) > 0 {
			t.Errorf("targets under 44 px: %v", small)
		}
	}
	for _, sh := range []visualShot{{"dark", 390}, {"dark", 1024}, {"dark", 1440}, {"light", 390}, {"light", 1024}, {"light", 1440}} {
		theme, w := sh.Theme, sh.Width
		newTab := func() *cdp.Tab {
			tab := b.NewTab(w, 900, w == 390, false)
			if err := tab.Call("Network.enable", nil, nil); err != nil {
				t.Fatal(err)
			}
			if err := tab.Call("Network.setCookie", map[string]any{"name": themeCookie, "value": theme, "url": e.URL, "httpOnly": true}, nil); err != nil {
				t.Fatal(err)
			}
			return tab
		}
		name := func(kind string) string {
			return filepath.Join(out, fmt.Sprintf("offline-%s-%s-%d.png", kind, theme, w))
		}

		t.Run(fmt.Sprintf("downloads-%s-%d", theme, w), func(t *testing.T) {
			tab := newTab()
			tab.Navigate(e.URL + "/me/downloads")
			tab.WaitFor(`window.tsDownloads !== undefined`, 10*time.Second)
			time.Sleep(900 * time.Millisecond)
			probe(t, tab)
			tab.ScreenshotFull(name("downloads-empty"))
			offlineDShow(t, tab, offlineDStates(cover))
			tab.WaitFor(`document.querySelectorAll("[data-ts-active-list] li").length === 2 && document.querySelectorAll("[data-ts-ready-list] li").length === 2`, 5*time.Second)
			probe(t, tab)
			tab.ScreenshotFull(name("downloads-active"))
			offlineDShow(t, tab, offlineDTrouble(cover))
			tab.WaitFor(`document.querySelectorAll("[data-ts-active-list] li").length === 3`, 5*time.Second)
			probe(t, tab)
			tab.ScreenshotFull(name("downloads-trouble"))
		})

		t.Run(fmt.Sprintf("book-%s-%d", theme, w), func(t *testing.T) {
			tab := newTab()
			tab.Navigate(e.URL + "/books/" + e.book.ID)
			tab.WaitFor(`window.tsDownloads !== undefined && !document.querySelector("[data-ts-download]").hidden`, 10*time.Second)
			probe(t, tab)
			tab.ScreenshotFull(name("book-download"))
			for _, st := range []struct{ name, state string }{{"book-downloading", "downloading"}, {"book-waiting", "waiting"}, {"book-ready", "ready"}, {"book-failed", "failed"}} {
				rec := offlineDRecord{Title: "Harbour Lamp", Cover: cover, Bytes: 468e6, Done: 214e6, State: st.state, AddedAt: time.Now().UnixMilli(), Files: []int{}}
				if st.state == "ready" {
					rec.Done = rec.Bytes
				}
				if st.state == "failed" {
					rec.Error = "failed"
				}
				offlineDShow(t, tab, map[string]offlineDRecord{e.book.ID: rec})
				probe(t, tab)
				tab.ScreenshotFull(name(st.name))
			}
		})

		t.Run(fmt.Sprintf("shell-%s-%d", theme, w), func(t *testing.T) {
			tab := newTab()
			tab.Navigate(e.URL + "/libraries")
			tab.WaitFor(`window.tsPlayer !== undefined && navigator.serviceWorker.controller`, 20*time.Second)
			e.seed(t, tab)
			e.showShell(t, tab)
			time.Sleep(400 * time.Millisecond)
			var g struct{ BannerTop, BannerBottom, HeadTop, ScrollY, Shell float64 }
			tab.Eval(`(() => { const b = document.getElementById("ts-offline").getBoundingClientRect(); const h = document.querySelector("[data-ts-offline-shell] h1").getBoundingClientRect(); return {BannerTop: b.top, BannerBottom: b.bottom, HeadTop: h.top, ScrollY: scrollY, Shell: document.querySelector(".ts-shell").getBoundingClientRect().top} })()`, &g)
			t.Logf("banner %.0f-%.0f, heading top %.0f, shell top %.0f, scrollY %.0f", g.BannerTop, g.BannerBottom, g.HeadTop, g.Shell, g.ScrollY)
			if g.BannerBottom > g.HeadTop {
				t.Errorf("the banner (bottom %.0f) covers the heading (top %.0f)", g.BannerBottom, g.HeadTop)
			}
			probe(t, tab)
			tab.ScreenshotFull(name("shell"))
			e.setDown(t, tab, false)
		})
	}
	t.Logf("screenshots in %s", out)
}
