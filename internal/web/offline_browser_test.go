//go:build dev

package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
	"github.com/Tend-Stack/tendshelf/internal/testutil/wav"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// Offline listening (decision 0012, package B1) in a real browser: the worker controls the page and
// holds every asset (T1); with the server down the shell and a stored book play from chunks (T3);
// a place and a bookmark made offline arrive once, after reconnecting (T5); signing out, or another
// person signing in, leaves nothing behind (T6).
// Run by: TENDSHELF_TEST_BROWSER=<chromium> go test -tags dev -run Offline ./internal/web/
// The download manager is package B2, so these tests store the chunks themselves (offlineSeedJS).

const (
	offlineChunk  = 64 << 10 // the app's chunk size in these tests
	offlineFileMs = 13_000   // each of the two files: 208 KB, three and a bit chunks
)

// offlineEnv is a server with one book of two short WAV files and a switch that makes it unreachable.
type offlineEnv struct {
	a      *testApp
	book   library.Book
	files  []library.AudioFile
	URL    string
	down   atomic.Bool
	slow   atomic.Int64                // milliseconds each download=1 request waits, so a test can act mid-download
	cookie atomic.Pointer[http.Cookie] // added to requests that carry no session of their own
}

func offlineApp(t *testing.T) *offlineEnv {
	t.Helper()
	return offlineAppWith(t, nil)
}

// offlineAppWith is offlineApp with more server options.
func offlineAppWith(t *testing.T, more func(*Options)) *offlineEnv {
	t.Helper()
	a := newTestApp(t, func(o *Options) {
		o.DownloadChunk = offlineChunk
		if more != nil {
			more(o)
		}
	})
	owner := a.owner(t)
	_, f := librarytest.NewLibrary(t, a.lib, "Offline library", t.TempDir())
	book := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{
		Title: "Harbour Lamp", Authors: []string{"Imogen Hale"}, DurationMs: 2 * offlineFileMs, Files: 2,
	})
	bookVisualCover(t, a, book, 0)
	files, err := a.lib.AudioFiles(context.Background(), book.ID)
	if err != nil || len(files) != 2 {
		t.Fatalf("audio files = %d, %v", len(files), err)
	}
	for i, af := range files {
		if err := wav.Write(af.Path, offlineFileMs, 440+i*100); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(af.Path)
		if err != nil {
			t.Fatal(err)
		}
		streamExec(t, a, `UPDATE audio_files SET container='wav', codec='pcm_s16le', duration_ms=?, offset_ms=?, size=?, mtime_ns=? WHERE id=?`,
			offlineFileMs, int64(i)*offlineFileMs, fi.Size(), fi.ModTime().UnixNano(), af.ID)
	}
	files, _ = a.lib.AudioFiles(context.Background(), book.ID)
	e := &offlineEnv{a: a, book: book, files: files}
	e.cookie.Store(owner)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if e.down.Load() {
			// The server is gone: the connection closes without an answer.
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				_ = conn.Close()
			}
			return
		}
		if ms := e.slow.Load(); ms > 0 && r.URL.Query().Has("download") {
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
		if ck := e.cookie.Load(); ck != nil {
			if _, err := r.Cookie(ck.Name); err != nil {
				r.AddCookie(ck)
			}
		}
		a.h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	e.URL = srv.URL
	return e
}

// offlineTab opens the Library and waits until the worker controls the page and has stored the assets.
func (e *offlineEnv) offlineTab(t *testing.T, b *cdp.Browser) *cdp.Tab {
	t.Helper()
	tab := b.NewTab(1440, 900, false, false)
	if err := tab.Call("Network.enable", nil, nil); err != nil {
		t.Fatal(err)
	}
	tab.Navigate(e.URL + "/libraries")
	tab.WaitFor(`window.tsPlayer !== undefined && navigator.serviceWorker.controller`, 20*time.Second)
	return tab
}

// setDown makes the server unreachable (and the tab offline) or brings both back.
func (e *offlineEnv) setDown(t *testing.T, tab *cdp.Tab, down bool) {
	t.Helper()
	e.down.Store(down)
	err := tab.Call("Network.emulateNetworkConditions", map[string]any{
		"offline": down, "latency": 0, "downloadThroughput": -1, "uploadThroughput": -1,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
}

// offlineSeedJS stores what a finished download holds, as the manager will (plan: Data shapes): the
// book's fragment, its cover, every file in chunks with its completion entry, and the shell.
const offlineSeedJS = `window.__seed = async (bookId) => {
  const cache = await caches.open("ts-person-" + document.documentElement.dataset.tsPerson);
  const chunk = Number(document.documentElement.dataset.tsChunk);
  const html = await (await fetch("/hx/player/" + bookId)).text();
  await cache.put("/hx/player/" + bookId, new Response(html, { headers: { "Content-Type": "text/html" } }));
  const data = new DOMParser().parseFromString(html, "text/html");
  const cover = data.getElementById("player-data").dataset.coverUrl;
  if (cover) await cache.put(cover, await fetch(cover));
  const ids = [...data.querySelectorAll("[data-ts-files] > li")].map((li) => li.dataset.id);
  for (const id of ids) {
    const key = "/stream/" + id;
    let n = 0, size = 0, type = "", etag = "";
    while (n === 0 || n * chunk < size) {
      const res = await fetch(key + "?download=1", { headers: { Range: "bytes=" + n * chunk + "-" + ((n + 1) * chunk - 1) }, cache: "no-store" });
      if (res.status !== 206) throw new Error("chunk " + n + " of " + id + ": " + res.status);
      size = Number(res.headers.get("Content-Range").split("/")[1]);
      type = res.headers.get("Content-Type");
      etag = res.headers.get("ETag") || "";
      await cache.put(key + "?chunk=" + n, new Response(await res.arrayBuffer(), { headers: { "Content-Type": type } }));
      n++;
    }
    await cache.put(key, new Response(null, { headers: { "Content-Type": type, "X-Ts-Size": String(size), "X-Ts-Chunk": String(chunk), "X-Ts-Chunks": String(n), ETag: etag } }));
  }
  await cache.put("/offline", await fetch("/offline?books=" + bookId));
  return ids.length;
}`

func (e *offlineEnv) seed(t *testing.T, tab *cdp.Tab) {
	t.Helper()
	tab.Eval(offlineSeedJS, nil)
	var n int
	tab.Eval(`window.__seed(`+cdp.Quote(e.book.ID)+`)`, &n)
	if n != 2 {
		t.Fatalf("seeded %d files, want 2", n)
	}
}

// showShell takes the server down, follows a boosted link and waits for the offline shell and banner.
func (e *offlineEnv) showShell(t *testing.T, tab *cdp.Tab) {
	t.Helper()
	e.setDown(t, tab, true)
	tab.Eval(`document.querySelector('a[data-nav="/"]').click()`, nil)
	tab.WaitFor(`document.querySelector("#main [data-ts-offline-shell]")`, 10*time.Second)
	tab.WaitFor(`!document.getElementById("ts-offline").hidden`, 5*time.Second)
	tab.WaitFor(`!document.activeViewTransition`, 5*time.Second) // a click during the transition lands on <html>
}

// T1: the worker controls the page and ts-static-<build> holds every asset the server listed.
func TestOfflineWorkerControlsPage(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)
	tab := e.offlineTab(t, b)
	var got struct {
		Build  string
		Want   []string
		Have   []string
		Caches []string
	}
	tab.WaitFor(`(async () => { const s = await (await fetch("/sw.js")).text(); const tb = JSON.parse(/^self\.TS_BUILD = (\{.*\});/.exec(s)[1]); const c = await caches.open("ts-static-" + tb.build); return (await c.keys()).length === tb.assets.length })()`, 20*time.Second)
	tab.Eval(`(async () => {
	  const s = await (await fetch("/sw.js")).text();
	  const tb = JSON.parse(/^self\.TS_BUILD = (\{.*\});/.exec(s)[1]);
	  const c = await caches.open("ts-static-" + tb.build);
	  return { Build: tb.build, Want: tb.assets, Have: (await c.keys()).map((r) => new URL(r.url).pathname), Caches: await caches.keys() };
	})()`, &got)
	if len(got.Want) == 0 {
		t.Fatal("the worker lists no assets")
	}
	slices.Sort(got.Want)
	slices.Sort(got.Have)
	if !slices.Equal(got.Want, got.Have) {
		t.Errorf("cached assets differ from the list: want %d, have %d", len(got.Want), len(got.Have))
	}
	if !slices.Contains(got.Caches, "ts-static-"+got.Build) {
		t.Errorf("caches %v lack ts-static-%s", got.Caches, got.Build)
	}
}

// T3: with the server down, a boosted click shows the shell and the banner, the stored book plays
// from its chunks, and a seek into the middle of the second file keeps playing.
func TestOfflineShellPlays(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)
	tab := e.offlineTab(t, b)
	e.seed(t, tab)
	e.showShell(t, tab)

	playerClickSel(t, tab, "#main [data-ts-play]")
	tab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().positionMs > 1000`, 15*time.Second)

	middle := offlineFileMs + offlineFileMs/2
	tab.Eval(`window.tsPlayer.seek(`+strconv.Itoa(middle)+`)`, nil)
	tab.WaitFor(`window.tsPlayer.state().positionMs > `+strconv.Itoa(middle+1000), 15*time.Second)
	var s struct {
		Playing bool
		Idx     int
		Err     any
		Src     string
	}
	tab.Eval(`({Playing: window.tsPlayer.state().playing, Idx: window.tsPlayer.state().fileIdx, Err: document.getElementById("audio").error, Src: document.getElementById("audio").currentSrc})`, &s)
	if !s.Playing || s.Idx != 1 || s.Err != nil || !strings.Contains(s.Src, e.files[1].ID) {
		t.Errorf("after the seek: %+v, want playing file 2 without an error", s)
	}
}

// T5: a place and a bookmark made offline reach the server once the connection is back, and a
// second reconnect adds nothing.
func TestOfflineSyncsAfterReconnect(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)
	tab := e.offlineTab(t, b)
	e.seed(t, tab)
	e.showShell(t, tab)

	playerClickSel(t, tab, "#main [data-ts-play]")
	tab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().positionMs > 5000`, 20*time.Second)
	tab.Eval(`window.tsPlayer.pause()`, nil)
	tab.Eval(`document.querySelector("[data-ts-bar-bookmark]").click()`, nil)
	tab.WaitFor(`JSON.parse(localStorage.getItem("ts.offline.marks") || "[]").length === 1`, 5*time.Second)
	tab.WaitFor(`document.getElementById("toasts").textContent.includes("saves when you")`, 5*time.Second)
	tab.WaitFor(`Object.keys(JSON.parse(localStorage.getItem("ts.pending") || "{}")).length === 1`, 5*time.Second)
	var at int64
	tab.Eval(`window.tsPlayer.state().positionMs`, &at)

	e.setDown(t, tab, false)
	tab.WaitFor(`!localStorage.getItem("ts.offline.marks") || localStorage.getItem("ts.offline.marks") === "[]"`, 15*time.Second)
	tab.WaitFor(`!localStorage.getItem("ts.pending") || localStorage.getItem("ts.pending") === "{}"`, 15*time.Second)
	tab.WaitFor(`document.getElementById("toasts").textContent.includes("Back online")`, 5*time.Second)

	// The notice leaves by itself (it carries a lifetime) and a tap dismisses it at once (operator,
	// 2026-11-03: the "ready offline" notice could not be dismissed).
	var life string
	tab.Eval(`[...document.querySelectorAll("#toasts .ts-toast")].find((t) => t.textContent.includes("Back online")).dataset.tsMs || ""`, &life)
	if life == "" {
		t.Error(`the "Back online" notice has no lifetime (data-ts-ms)`)
	}
	tab.Eval(`[...document.querySelectorAll("#toasts .ts-toast")].find((t) => t.textContent.includes("Back online")).click()`, nil)
	tab.WaitFor(`!document.getElementById("toasts").textContent.includes("Back online")`, 3*time.Second)

	var saved int64
	row := e.a.st.Writer().QueryRowContext(context.Background(), `SELECT position_ms FROM progress WHERE book_id=?`, e.book.ID)
	if err := row.Scan(&saved); err != nil {
		t.Fatal(err)
	}
	if d := saved - at; d < -1500 || d > 1500 {
		t.Errorf("server position %d, client %d", saved, at)
	}
	if n := e.a.count(t, `SELECT COUNT(*) FROM bookmarks WHERE book_id=? AND deleted_at IS NULL`, e.book.ID); n != 1 {
		t.Errorf("%d bookmarks, want 1", n)
	}

	e.setDown(t, tab, true)
	e.setDown(t, tab, false)
	time.Sleep(1500 * time.Millisecond)
	if n := e.a.count(t, `SELECT COUNT(*) FROM bookmarks WHERE book_id=? AND deleted_at IS NULL`, e.book.ID); n != 1 {
		t.Errorf("%d bookmarks after a second reconnect, want 1", n)
	}
}

// T6: signing out leaves no downloads or unsent saves in the browser, and another person signing in
// finds an empty Downloads page.
func TestOfflineSignOutWipes(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)
	e.cookie.Store(nil) // this test signs in through the form
	tab := b.NewTab(1440, 900, false, false)
	tab.Navigate(e.URL + "/login")
	tab.Eval(`(() => { document.getElementById("username").value = "ana"; document.getElementById("password").value = `+cdp.Quote(testPassword)+`; document.querySelector("form.ts-form").requestSubmit() })()`, nil)
	tab.WaitFor(`document.documentElement.dataset.tsPerson && navigator.serviceWorker.controller`, 20*time.Second)
	e.seed(t, tab)
	tab.Eval(`(() => { localStorage.setItem("ts.offline", JSON.stringify({v: 1, books: {}})); localStorage.setItem("ts.offline.marks", "[]") })()`, nil)

	// sign out the way the Profile page does, then come back to the sign-in page as the redirect does
	tab.Eval(`fetch("/logout", { method: "POST", headers: { "X-CSRF-Token": JSON.parse(document.body.getAttribute("hx-headers:inherited"))["X-CSRF-Token"] } })`, nil)
	// storage left over from the old session (the header may not reach a fetch) is the page script's to remove
	tab.Eval(`(async () => { await (await caches.open("ts-person-leftover")).put("/offline", new Response("x")); localStorage.setItem("ts.offline", "{}"); localStorage.setItem("ts.pending", "{}") })()`, nil)
	tab.Navigate(e.URL + "/login?signed_out=1")
	tab.WaitFor(`(async () => !(await caches.keys()).some((k) => k.startsWith("ts-person-")) && localStorage.getItem("ts.offline") === null && localStorage.getItem("ts.pending") === null)()`, 10*time.Second)

	// another person signs in on the same browser
	member := e.a.as(t, users.RoleMember)
	e.cookie.Store(member)
	tab.Navigate(e.URL + "/me/downloads")
	tab.WaitFor(`document.documentElement.dataset.tsPerson`, 10*time.Second)
	tab.Eval(`(async () => { await (await caches.open("ts-person-someone-else")).put("/offline", new Response("x")); localStorage.setItem("ts.offline.person", "someone-else"); localStorage.setItem("ts.offline", "{}"); localStorage.setItem("ts.pending", "{}") })()`, nil)
	tab.Navigate(e.URL + "/me/downloads")
	tab.WaitFor(`(async () => !(await caches.keys()).includes("ts-person-someone-else") && localStorage.getItem("ts.offline") === null && localStorage.getItem("ts.pending") === null && localStorage.getItem("ts.offline.person") === document.documentElement.dataset.tsPerson)()`, 10*time.Second)
	var empty bool
	tab.Eval(`Boolean(document.querySelector("[data-ts-empty]")) && !document.querySelector("[data-ts-empty]").hidden`, &empty)
	if !empty {
		t.Error("the new person's Downloads page is not the empty state")
	}
}
