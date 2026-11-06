//go:build dev

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// Offline reading in a real browser (G6a, decision 0019). A held book opens from the stored shell with no
// connection and with a hung one; a place is queued before it is sent and arrives once; nothing the worker
// must not answer is answered.
// Run by: TENDSHELF_TEST_BROWSER=<chromium> go test -tags dev -run OffReadBrowser ./internal/web/
// The download is package G6b, so ofrSeedCache stores what a finished download holds, from page script.

// ofrEnv is a text book behind a server with the same switches as offlineEnv: down (refused) and stall (hangs).
type ofrEnv struct {
	l      *rdrLab
	URL    string
	down   atomic.Bool
	stall  atomic.Bool
	ended  chan struct{}
	posts  atomic.Int64 // POST /reading/{id} requests that reached the server
	slow   atomic.Int64 // milliseconds each chapter part waits, so a download test can act mid-way (G6b)
	cookie atomic.Pointer[http.Cookie]
}

func ofrApp(t *testing.T, chs []ebooktest.EPUBChapter, opt func(*ebooktest.EPUBOptions)) *ofrEnv {
	t.Helper()
	l := pgSeedEPUB(t, chs, opt)
	e := &ofrEnv{l: l, ended: make(chan struct{})}
	e.cookie.Store(l.owner)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if e.down.Load() {
			if conn, _, err := w.(http.Hijacker).Hijack(); err == nil {
				_ = conn.Close()
			}
			return
		}
		if e.stall.Load() && offlineStalls(r.URL.Path) {
			select {
			case <-e.ended:
			case <-r.Context().Done():
			}
			return
		}
		if ms := e.slow.Load(); ms > 0 && strings.Contains(r.URL.Path, "/read/chapters/") {
			time.Sleep(time.Duration(ms) * time.Millisecond)
		}
		if r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/reading/") {
			e.posts.Add(1)
		}
		if ck := e.cookie.Load(); ck != nil {
			if _, err := r.Cookie(ck.Name); err != nil {
				r.AddCookie(ck)
			}
		}
		l.a.h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(e.ended) })
	e.URL = srv.URL
	return e
}

func (e *ofrEnv) read(q string) string {
	p := e.URL + "/books/" + e.l.book.ID + "/read"
	if q != "" {
		p += "?" + q
	}
	return p
}

// ofrSeedJS stores what a finished download holds: every chapter part and picture under the manifest's
// addresses, the two shells, and the entry in the download index.
const ofrSeedJS = `window.__ofrSeed = async (id) => {
  const cache = await caches.open("ts-person-" + document.documentElement.dataset.tsPerson);
  const m = await (await fetch("/offline/books/" + id + "/manifest")).json();
  for (const u of [...m.parts, ...m.images]) await cache.put(u, await fetch(u));
  for (const q of ["", "?sheet=contents"]) await cache.put("/books/" + id + "/read" + q, await fetch("/offline/books/" + id + "/read" + q));
  const index = JSON.parse(localStorage.getItem("ts.offline") || '{"books":{}}');
  index.books = index.books || {};
  index.books[id] = { text: true };
  localStorage.setItem("ts.offline", JSON.stringify(index));
  return m.parts.length + m.images.length;
}`

// ofrSeedCache opens the app online, waits for the worker and stores the book. It returns the tab.
func ofrSeedCache(t *testing.T, e *ofrEnv, tab *cdp.Tab) {
	t.Helper()
	tab.Navigate(e.URL + "/libraries")
	tab.WaitFor(`window.tsPlayer !== undefined && navigator.serviceWorker.controller`, 20*time.Second)
	tab.Eval(ofrSeedJS, nil)
	var n int
	tab.Eval(`window.__ofrSeed(`+cdp.Quote(e.l.book.ID)+`)`, &n)
	if n == 0 {
		t.Fatal("nothing stored")
	}
}

// ofrOffline makes the tab offline (CDP) and the server refuse, or brings both back.
func ofrOffline(t *testing.T, e *ofrEnv, tab *cdp.Tab, down bool) {
	t.Helper()
	e.down.Store(down)
	if err := tab.Call("Network.emulateNetworkConditions", map[string]any{"offline": down, "latency": 0, "downloadThroughput": -1, "uploadThroughput": -1}, nil); err != nil {
		t.Fatal(err)
	}
}

const ofrMounted = `document.querySelector("#reader.ts-reader--pages .ts-s, #reader.ts-reader--scroll .ts-s") !== null`

// ofrBook is a title page, a chapter of two parts, and a chapter with a picture.
func ofrBook() ([]ebooktest.EPUBChapter, func(*ebooktest.EPUBOptions)) {
	chs := []ebooktest.EPUBChapter{
		{Title: "Title Page", EpubType: "titlepage", Paras: []string{"Offline"}},
		{Title: "Long", EpubType: "chapter", Paras: pgProse("a", 120)},
		{Title: "Pictures", EpubType: "chapter", Raw: `<p>` + pgProse("p", 1)[0] + `</p><img src="pic.png" alt="Lamp"/><p>` + pgProse("q", 1)[0] + `</p>`},
	}
	return chs, func(o *ebooktest.EPUBOptions) {
		o.ManifestXML = `<item id="p" href="pic.png" media-type="image/png"/>`
		o.Extra = []ebooktest.Entry{{Name: "OEBPS/pic.png", Data: rsPNG(40, 30)}}
	}
}

// ofrRecord is one entry of ts.offline.reading.
type ofrRecord struct {
	Form          string
	Chars, Active float64
	Sent, Found   bool
}

// ofrQueue reads the book's entry of ts.offline.reading.
func ofrQueue(tab *cdp.Tab, id string) (rec ofrRecord, found bool) {
	tab.Eval(`(() => { const r = JSON.parse(localStorage.getItem("ts.offline.reading") || "{}")[`+cdp.Quote(id)+`]; return r ? Object.assign({found: true}, r) : {}; })()`, &rec)
	return rec, rec.Found
}

var ofrViewports = []struct {
	name, flow string
	w, h       int
	mobile     bool
}{{"mobile-390-pages", "pages", 390, 844, true}, {"desktop-1440-scroll", "scroll", 1440, 900, false}}

func TestOffReadBrowserOpen(t *testing.T) {
	b := chromium(t)
	for _, vp := range ofrViewports {
		t.Run(vp.name, func(t *testing.T) {
			chs, opt := ofrBook()
			e := ofrApp(t, chs, opt)
			pgFlow(t, e.l, vp.flow)
			tab := b.NewTab(vp.w, vp.h, vp.mobile, true)
			ofrSeedCache(t, e, tab)
			ofrOffline(t, e, tab, true)

			// Open at the last block of part 1 of chapter 2, so the next turn crosses a part edge.
			parts := rsParts(e.l.doc.Chapters[1])
			if len(parts) < 2 {
				t.Fatalf("chapter 2 has %d parts", len(parts))
			}
			last := e.l.doc.Chapters[1].Blocks[parts[0].To-1]
			tab.Navigate(e.read("at=" + last.ID + ".s0"))
			tab.WaitFor(`document.querySelector("[data-offline-shell]") && `+ofrMounted, 15*time.Second)
			tab.WaitFor(`document.querySelector("section[data-part='1']")`, 5*time.Second)
			if vp.flow == "pages" {
				for i := 0; i < 12; i++ {
					var two bool
					tab.Eval(`document.querySelector("section[data-part='2']") !== null`, &two)
					if two {
						break
					}
					pgNext(t, tab)
					time.Sleep(150 * time.Millisecond)
				}
			} else {
				tab.Eval(`(() => { const r = document.getElementById("reader"); r.scrollTop = r.scrollHeight; })()`, nil)
			}
			tab.WaitFor(`document.querySelector("section[data-part='2']")`, 10*time.Second)

			// The picture of chapter 3 shows, from the stored copy.
			tab.Navigate(e.read("ch=3"))
			tab.WaitFor(`document.querySelector("[data-offline-shell]") && `+ofrMounted+` && document.querySelector("img.ts-reader__image")`, 15*time.Second)
			tab.WaitFor(`document.querySelector("img.ts-reader__image").complete && document.querySelector("img.ts-reader__image").naturalWidth === 40`, 10*time.Second)

			// The contents open from the stored contents shell, a chapter link opens, and closing keeps the place.
			tab.Eval(`document.querySelector('.ts-readerbar a[href*="sheet=contents"]').click()`, nil)
			tab.WaitFor(`document.getElementById("reader-sheet-title")`, 10*time.Second)
			tab.Eval(`document.querySelector('.ts-reader__toc a[href*="ch=2"]').click()`, nil)
			tab.WaitFor(`location.search.includes("ch=2") && document.querySelector("section[data-ch='2']") && `+ofrMounted, 15*time.Second)
			// Read on, then open the contents and close them: the book opens where the person was, not at the
			// chapter's start and not at the shell's own place.
			for i := 0; i < 3; i++ {
				if vp.flow == "pages" {
					pgNext(t, tab)
				} else {
					tab.Eval(`document.getElementById("reader").scrollTop += 700`, nil)
				}
				time.Sleep(250 * time.Millisecond)
			}
			tab.Eval(`document.querySelector('.ts-readerbar a[href*="sheet=contents"]').click()`, nil)
			tab.WaitFor(`document.getElementById("reader-sheet-title")`, 10*time.Second)
			tab.Eval(`window.__closing = 1; document.querySelector(".ts-sheet__head a[aria-label]").click()`, nil)
			tab.WaitFor(`window.__closing === undefined && location.search === "" && !document.getElementById("reader-sheet-title") && `+ofrMounted, 15*time.Second)
			rec, ok := ofrQueue(tab, e.l.book.ID)
			var at string
			tab.Eval(`document.getElementById("reader").dataset.at`, &at)
			sid := strings.TrimPrefix(strings.SplitN(strings.SplitAfter(rec.Form, "sid=")[1], "&", 2)[0], "")
			if !ok || at == "" || at != sid || at == "c2.p1.s0" {
				t.Errorf("closing the contents reopened at %q, the queued place is %q", at, sid)
			}
		})
	}
}

// A reading place is queued before it is sent, read offline arrives once, and ts.pending is never touched.
func TestOffReadBrowserPlace(t *testing.T) {
	b := chromium(t)
	chs, opt := ofrBook()
	e := ofrApp(t, chs, opt)
	pgFlow(t, e.l, "pages")
	tab := b.NewTab(390, 844, true, true)
	ofrSeedCache(t, e, tab)
	ofrOffline(t, e, tab, true)
	tab.Navigate(e.read("ch=2"))
	tab.WaitFor(`document.querySelector("[data-offline-shell]") && `+ofrMounted, 15*time.Second)
	for i := 0; i < 3; i++ {
		pgNext(t, tab)
		time.Sleep(200 * time.Millisecond)
	}
	tab.Eval(`window.dispatchEvent(new Event("pagehide"))`, nil)
	id := e.l.book.ID
	tab.WaitFor(`localStorage.getItem("ts.offline.reading")`, 5*time.Second)
	rec, ok := ofrQueue(tab, id)
	if !ok || rec.Sent || !strings.Contains(rec.Form, "sid=") || strings.Contains(rec.Form, "chars_read") || strings.Contains(rec.Form, "_csrf") {
		t.Fatalf("queue = %+v", rec)
	}
	if rec.Chars <= 0 {
		t.Errorf("the characters read offline are not in the record: %+v", rec)
	}

	// Close the tab, reopen offline: the shell opens at the place.
	tab.Close()
	tab = b.NewTab(390, 844, true, true)
	tab.Navigate(e.read(""))
	tab.WaitFor(`document.querySelector("[data-offline-shell]") && `+ofrMounted, 15*time.Second)
	var at string
	tab.Eval(`document.getElementById("reader").dataset.at`, &at)
	if at == "" || !strings.Contains(rec.Form, "sid="+at+"&") {
		t.Errorf("reopened at %q, queue %q", at, rec.Form)
	}

	// Back online: one place on the server, the characters once, ts.pending untouched.
	ofrOffline(t, e, tab, false)
	tab.Navigate(e.URL + "/libraries")
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, _, found := pgSaved(t, e.l); found || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if sid, _, found := pgSaved(t, e.l); !found || sid == "" {
		t.Fatal("the place never reached the server")
	}
	var chars int64
	read := func() {
		_ = e.l.a.st.Reader().QueryRow(`SELECT coalesce(sum(chars_read),0) FROM reading_sessions WHERE book_id = ?`, id).Scan(&chars)
	}
	read()
	if chars != int64(rec.Chars) {
		t.Errorf("server counted %d characters, the record held %v", chars, rec.Chars)
	}
	tab.Navigate(e.URL + "/libraries") // a second load sends nothing more
	time.Sleep(time.Second)
	before := chars
	read()
	if chars != before {
		t.Errorf("characters counted twice: %d then %d", before, chars)
	}
	var pending bool
	tab.Eval(`localStorage.getItem("ts.pending") === null`, &pending)
	if !pending {
		t.Error("ts.pending was written for text")
	}
	if _, held := ofrQueue(tab, id); !held {
		t.Error("a held book keeps its sent record as its last place")
	}
}

// The operator's phone: requests hang, they are not refused. The reader still opens, and the place whose
// beacon was lost still lands.
func TestOffReadBrowserHang(t *testing.T) {
	b := chromium(t)
	chs, opt := ofrBook()
	e := ofrApp(t, chs, opt)
	pgFlow(t, e.l, "pages")
	tab := b.NewTab(390, 844, true, true)
	ofrSeedCache(t, e, tab)

	e.stall.Store(true)
	start := time.Now()
	tab.Navigate(e.read("ch=2"))
	tab.WaitFor(`document.querySelector("[data-offline-shell]") && `+ofrMounted, 20*time.Second)
	if d := time.Since(start); d > 15*time.Second {
		t.Errorf("the reader took %s to open on a hung network", d)
	}
	for i := 0; i < 2; i++ {
		pgNext(t, tab)
		time.Sleep(200 * time.Millisecond)
	}
	tab.Eval(`window.dispatchEvent(new Event("pagehide"))`, nil) // the beacon is sent and hangs
	rec, ok := ofrQueue(tab, e.l.book.ID)
	if !ok || rec.Sent || !strings.Contains(rec.Form, "sid=") {
		t.Fatalf("the place was not queued before the send: %+v", rec)
	}
	tab.Close()

	e.stall.Store(false)
	tab = b.NewTab(390, 844, true, true)
	tab.Navigate(e.URL + "/libraries")
	deadline := time.Now().Add(20 * time.Second)
	for {
		if _, _, found := pgSaved(t, e.l); found || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if _, _, found := pgSaved(t, e.l); !found {
		t.Fatal("the place whose beacon hung never landed")
	}
}

// A record of 25 minutes is sent in three posts, within the server's cap of 10 minutes each.
func TestOffReadBrowserSlices(t *testing.T) {
	b := chromium(t)
	chs, opt := ofrBook()
	e := ofrApp(t, chs, opt)
	l := e.l
	sid := l.doc.Chapters[1].Blocks[0].ID + ".s0"
	form := fmt.Sprintf("edition_id=%s&chapter_idx=2&sid=%s&off=0&quote=Sentence&char_pos=100&client_updated_at=%d&device_id=ofrslices001",
		l.ed.ID, sid, time.Now().Add(-time.Hour).UnixMilli())
	tab := b.NewTab(1440, 900, false, true)
	// The first load only lets the page learn who is signed in (a change of person clears the queue). The
	// record is then written at the start of the second page, before its scripts run. Writing it into a
	// loaded page raced that page's own start-up flush, which could send a slice too (4 posts, not 3).
	tab.Navigate(e.URL + "/libraries")
	tab.WaitFor(`window.tsPlayer !== undefined`, 20*time.Second)
	record := `{` + cdp.Quote(l.book.ID) + `: {form: ` + cdp.Quote(form) + `, chars: 150000, active: 1500000, sent: false}}`
	_ = tab.Call("Page.enable", map[string]any{}, nil)
	if err := tab.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{
		"source": `localStorage.setItem("ts.offline.reading", JSON.stringify(` + record + `))`,
	}, nil); err != nil {
		t.Fatal(err)
	}
	e.posts.Store(0)
	tab.Navigate(e.URL + "/libraries")
	tab.WaitFor(`JSON.parse(localStorage.getItem("ts.offline.reading") || "{}")[`+cdp.Quote(l.book.ID)+`] === undefined`, 15*time.Second)
	if n := e.posts.Load(); n != 3 {
		t.Errorf("%d posts, want 3", n)
	}
	var chars, active int64
	_ = l.a.st.Reader().QueryRow(`SELECT coalesce(sum(chars_read),0), coalesce(sum(active_ms),0) FROM reading_sessions WHERE book_id = ?`, l.book.ID).Scan(&chars, &active)
	if chars != 150000 || active != 1500000 {
		t.Errorf("server counted %d characters and %d ms", chars, active)
	}
}

// Sign-out and a change of person leave no pages and no queue behind.
func TestOffReadBrowserWipe(t *testing.T) {
	b := chromium(t)
	chs, opt := ofrBook()
	e := ofrApp(t, chs, opt)
	tab := b.NewTab(1440, 900, false, true)
	ofrSeedCache(t, e, tab)
	stored := `(async () => { const c = await caches.open("ts-person-" + document.documentElement.dataset.tsPerson); localStorage.setItem("ts.offline.reading", '{"x":{"form":"a","chars":1,"active":1,"sent":true}}'); return (await c.keys()).length })()`
	var n int
	tab.Eval(stored, &n)
	if n == 0 {
		t.Fatal("nothing stored")
	}
	gone := `(async () => !(await caches.keys()).some((k) => k.startsWith("ts-person-")) && localStorage.getItem("ts.offline.reading") === null)()`
	tab.Eval(`fetch("/logout", { method: "POST", headers: { "X-CSRF-Token": JSON.parse(document.body.getAttribute("hx-headers:inherited"))["X-CSRF-Token"] } })`, nil)
	e.cookie.Store(nil)
	tab.Navigate(e.URL + "/login?signed_out=1")
	tab.WaitFor(gone, 10*time.Second)

	// Another person signs in on the same browser, with the last person's pages and queue still there.
	tab.Eval(`(async () => { await (await caches.open("ts-person-someone-else")).put("/books/x/read", new Response("x")); localStorage.setItem("ts.offline.person", "someone-else"); localStorage.setItem("ts.offline.reading", '{"x":{"form":"a","chars":1,"active":1,"sent":true}}') })()`, nil)
	e.cookie.Store(e.l.a.as(t, users.RoleMember))
	tab.Navigate(e.URL + "/me/downloads")
	tab.WaitFor(`document.documentElement.dataset.tsPerson`, 10*time.Second)
	tab.WaitFor(gone, 10*time.Second)
}

// With a book stored, what the worker must not answer is not answered: each of these reaches for the network,
// which is down, so each must fail.
func TestOffReadBrowserPass(t *testing.T) {
	b := chromium(t)
	chs, opt := ofrBook()
	e := ofrApp(t, chs, opt)
	tab := b.NewTab(1440, 900, false, true)
	ofrSeedCache(t, e, tab)
	ofrOffline(t, e, tab, true)
	id := e.l.book.ID
	for _, path := range []string{"/s/x", "/api/v1/me", "/login", "/reading/" + id, "/books/" + id + "/read?sheet=marks",
		"/books/" + id + "/read?download=1", "/offline/books/" + id + "/manifest"} {
		var answered string
		tab.Eval(`fetch(`+cdp.Quote(path)+`).then((r) => "answered " + r.status, () => "failed")`, &answered)
		if answered != "failed" {
			t.Errorf("%s: %s", path, answered)
		}
	}
	var post string
	tab.Eval(`fetch("/reading/`+id+`", { method: "POST", body: "a=b" }).then((r) => "answered " + r.status, () => "failed")`, &post)
	if post != "failed" {
		t.Errorf("POST /reading: %s", post)
	}
}

// A request from an open reader that gets no answer says so.
func TestOffReadBrowserNeedsNet(t *testing.T) {
	b := chromium(t)
	chs, opt := ofrBook()
	e := ofrApp(t, chs, opt)
	pgFlow(t, e.l, "scroll")
	tab := b.NewTab(1440, 900, false, true)
	ofrSeedCache(t, e, tab)
	tab.Navigate(e.read("ch=2"))
	tab.WaitFor(`window.htmx !== undefined && `+ofrMounted+` && document.querySelector("[data-ts-module*='reader-offline']")`, 15*time.Second)
	time.Sleep(500 * time.Millisecond)
	ofrOffline(t, e, tab, true)
	tab.Eval(`htmx.ajax("POST", document.getElementById("reader-bm-form").getAttribute("hx-post"), { source: document.getElementById("reader-bm-form"), swap: "none" })`, nil)
	tab.WaitFor(`[...document.querySelectorAll(".ts-toast")].some((t) => t.textContent.includes("That needs a connection."))`, 10*time.Second)
}
