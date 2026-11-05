//go:build dev

package web

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
	"github.com/Tend-Stack/tendshelf/internal/testutil/wav"
)

// The download of a book's text in a real browser (G6b, decision 0019): one Download stores the text, or the
// text and the audio; the shell comes last; removal leaves nothing; the daily check keeps it honest.
// Run by: TENDSHELF_TEST_BROWSER=<chromium> go test -tags dev -run OffDownload ./internal/web/
// Helpers start with "ofd".

// ofdTextOnly turns the lab's book into a book with text and no audio.
func ofdTextOnly(t *testing.T, e *ofrEnv) {
	t.Helper()
	w := e.l.a.st.Writer()
	if _, err := w.Exec(`DELETE FROM audio_files WHERE book_id = ?`, e.l.book.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Exec(`UPDATE books SET kind = 'text' WHERE id = ?`, e.l.book.ID); err != nil {
		t.Fatal(err)
	}
}

// ofdWithAudio gives the lab's book a real, small audio file next to its text.
func ofdWithAudio(t *testing.T, e *ofrEnv) {
	t.Helper()
	files, err := e.l.a.lib.AudioFiles(context.Background(), e.l.book.ID)
	if err != nil || len(files) != 1 {
		t.Fatalf("audio files = %d, %v", len(files), err)
	}
	af := files[0]
	if err := os.MkdirAll(filepath.Dir(af.Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := wav.Write(af.Path, 13_000, 440); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(af.Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.l.a.st.Writer().Exec(`UPDATE audio_files SET container='wav', codec='pcm_s16le', duration_ms=13000, offset_ms=0, size=?, mtime_ns=? WHERE id=?`,
		fi.Size(), fi.ModTime().UnixNano(), af.ID); err != nil {
		t.Fatal(err)
	}
}

// ofdRec is a book's entry in the download index.
type ofdRec struct {
	State, Error string
	Bytes, Done  float64
	FinishedAt   float64
	Files        []struct{ Size, Chunks float64 }
	Text         *struct {
		EditionID, Revision string
		Parts, Images       []string
		Bytes, Have         float64
	}
	Found bool
}

func ofdRecord(tab *cdp.Tab, id string) ofdRec {
	var r ofdRec
	tab.Eval(`(() => { const r = (JSON.parse(localStorage.getItem("ts.offline") || "{}").books || {})[`+cdp.Quote(id)+`]; return r ? Object.assign({Found: true}, r) : {}; })()`, &r)
	return r
}

func ofdWaitState(t *testing.T, tab *cdp.Tab, id, state string) {
	t.Helper()
	tab.WaitFor(`((JSON.parse(localStorage.getItem("ts.offline") || "{}").books || {})[`+cdp.Quote(id)+`] || {}).state === `+cdp.Quote(state), 30*time.Second)
}

// ofdKeys lists the person's cache by path and query.
func ofdKeys(tab *cdp.Tab) []string {
	var keys []string
	tab.Eval(`(async () => {
  const cache = await caches.open("ts-person-" + document.documentElement.dataset.tsPerson);
  return (await cache.keys()).map((r) => { const u = new URL(r.url); return u.pathname + u.search; });
})()`, &keys)
	return keys
}

// ofdOpen opens the book page and waits for the worker and the download manager.
func ofdOpen(t *testing.T, e *ofrEnv, tab *cdp.Tab) {
	t.Helper()
	tab.Navigate(e.URL + "/books/" + e.l.book.ID)
	tab.WaitFor(`window.tsDownloads !== undefined && navigator.serviceWorker.controller`, 20*time.Second)
}

// ofdDownload starts the download of the lab's book and waits until it is ready.
func ofdDownload(t *testing.T, e *ofrEnv, tab *cdp.Tab) ofdRec {
	t.Helper()
	ofdOpen(t, e, tab)
	tab.Eval(`window.tsDownloads.start(`+cdp.Quote(e.l.book.ID)+`)`, nil)
	ofdWaitState(t, tab, e.l.book.ID, "ready")
	return ofdRecord(tab, e.l.book.ID)
}

// ofdWantText checks that every address of the text is stored, the shells included.
func ofdWantText(t *testing.T, tab *cdp.Tab, id string, rec ofdRec) {
	t.Helper()
	if rec.Text == nil {
		t.Fatal("the record has no text")
	}
	keys := ofdKeys(tab)
	want := append(append([]string{}, rec.Text.Parts...), rec.Text.Images...)
	want = append(want, "/books/"+id+"/read", "/books/"+id+"/read?sheet=contents")
	for _, k := range want {
		if !slices.Contains(keys, k) {
			t.Errorf("the cache lacks %s", k)
		}
	}
	if int(rec.Text.Have) != len(rec.Text.Parts)+len(rec.Text.Images) {
		t.Errorf("text.have = %v of %d", rec.Text.Have, len(rec.Text.Parts)+len(rec.Text.Images))
	}
}

// ofdHeld waits until the offline shell the page re-seeds lists the book's Read button.
func ofdHeld(t *testing.T, e *ofrEnv, tab *cdp.Tab) {
	t.Helper()
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(250 * time.Millisecond) {
		var shell string
		tab.Eval(`(async () => { const c = await caches.open("ts-person-" + document.documentElement.dataset.tsPerson); const r = await c.match("/offline"); return r ? await r.text() : ""; })()`, &shell)
		if strings.Contains(shell, "/books/"+e.l.book.ID+"/read") {
			return
		}
	}
	t.Fatal("the stored offline shell never listed the book")
}

var ofdViewports = ofrViewports

// A text book: Download, ready, then in airplane mode it opens from /offline and from Downloads.
func TestOffDownloadText(t *testing.T) {
	b := chromium(t)
	for _, vp := range ofdViewports {
		t.Run(vp.name, func(t *testing.T) {
			chs, opt := ofrBook()
			e := ofrApp(t, chs, opt)
			pgFlow(t, e.l, vp.flow)
			ofdTextOnly(t, e)
			id := e.l.book.ID
			tab := b.NewTab(vp.w, vp.h, vp.mobile, true)
			rec := ofdDownload(t, e, tab)
			if len(rec.Files) != 0 || rec.Text == nil || rec.Bytes <= 0 {
				t.Fatalf("record = %+v", rec)
			}
			ofdWantText(t, tab, id, rec)
			for _, k := range ofdKeys(tab) {
				if strings.HasPrefix(k, "/stream/") {
					t.Errorf("a text book stored %s", k)
				}
			}
			ofdHeld(t, e, tab)

			// From the offline shell.
			ofrOffline(t, e, tab, true)
			tab.Navigate(e.URL + "/libraries")
			tab.WaitFor(`document.querySelector("[data-ts-offline-shell]") && document.querySelector('a[data-ts-read][href="/books/`+id+`/read"]')`, 15*time.Second)
			tab.Eval(`document.querySelector('a[data-ts-read][href="/books/`+id+`/read"]').click()`, nil)
			tab.WaitFor(`document.querySelector("[data-offline-shell]") && `+ofrMounted, 15*time.Second)

			// From the Downloads page.
			ofrOffline(t, e, tab, false)
			tab.Navigate(e.URL + "/me/downloads")
			tab.WaitFor(`document.querySelector('[data-ts-ready] [data-id="`+id+`"] a[href="/books/`+id+`/read"]')`, 15*time.Second)
			ofrOffline(t, e, tab, true)
			tab.Eval(`document.querySelector('[data-ts-ready] [data-id="`+id+`"] a[href="/books/`+id+`/read"]').click()`, nil)
			tab.WaitFor(`document.querySelector("[data-offline-shell]") && `+ofrMounted, 15*time.Second)
		})
	}
}

// One Download stores the text and the audio; both rows work offline; the audio is stored as before.
func TestOffDownloadBoth(t *testing.T) {
	b := chromium(t)
	chs, opt := ofrBook()
	e := ofrApp(t, chs, opt)
	pgFlow(t, e.l, "pages")
	ofdWithAudio(t, e)
	id := e.l.book.ID
	tab := b.NewTab(390, 844, true, true)
	rec := ofdDownload(t, e, tab)
	if len(rec.Files) != 1 || rec.Text == nil {
		t.Fatalf("record = %+v", rec)
	}
	ofdWantText(t, tab, id, rec)
	if want := rec.Files[0].Size + rec.Text.Bytes; rec.Bytes != want || rec.Done != want {
		t.Errorf("bytes %v done %v, want %v (audio plus text)", rec.Bytes, rec.Done, want)
	}
	var chunk float64
	tab.Eval(`(async () => {
  const cache = await caches.open("ts-person-" + document.documentElement.dataset.tsPerson);
  let n = 0;
  for (const r of await cache.keys()) if (new URL(r.url).search.includes("chunk=")) n += (await (await cache.match(r)).arrayBuffer()).byteLength;
  return n;
})()`, &chunk)
	if chunk != rec.Files[0].Size {
		t.Errorf("audio chunks hold %v bytes, the file is %v", chunk, rec.Files[0].Size)
	}
	if !slices.Contains(ofdKeys(tab), "/hx/player/"+id) {
		t.Error("the player fragment is not stored")
	}
	ofdHeld(t, e, tab)
	ofrOffline(t, e, tab, true)
	tab.Navigate(e.URL + "/libraries")
	tab.WaitFor(`document.querySelector("[data-ts-offline-shell]") && document.querySelector('a[data-ts-read][href="/books/`+id+`/read"]') && document.querySelector('[data-ts-play="`+id+`"]')`, 15*time.Second)
	tab.Eval(`document.querySelector('a[data-ts-read][href="/books/`+id+`/read"]').click()`, nil)
	tab.WaitFor(`document.querySelector("[data-offline-shell]") && `+ofrMounted, 15*time.Second)
}

// The shell is absent until the end; a pause and a resume finish the job; a full cache fails with the
// existing sentence and leaves nothing behind.
func TestOffDownloadPartial(t *testing.T) {
	b := chromium(t)
	chs, opt := ofrBook()
	e := ofrApp(t, chs, opt)
	pgFlow(t, e.l, "pages")
	ofdTextOnly(t, e)
	id := e.l.book.ID
	tab := b.NewTab(390, 844, true, true)
	ofdOpen(t, e, tab)
	e.slow.Store(400)
	tab.Eval(`window.tsDownloads.start(`+cdp.Quote(id)+`)`, nil)
	tab.WaitFor(`(((JSON.parse(localStorage.getItem("ts.offline") || "{}").books || {})[`+cdp.Quote(id)+`] || {}).text || {}).have >= 1`, 20*time.Second)
	tab.Eval(`window.tsDownloads.pause(`+cdp.Quote(id)+`)`, nil)
	ofdWaitState(t, tab, id, "paused")
	for _, k := range ofdKeys(tab) {
		if strings.HasPrefix(k, "/books/"+id+"/read") {
			t.Errorf("%s is stored before the end: a half-stored book would open", k)
		}
	}
	e.slow.Store(0)
	tab.Eval(`window.tsDownloads.resume(`+cdp.Quote(id)+`)`, nil)
	ofdWaitState(t, tab, id, "ready")
	ofdWantText(t, tab, id, ofdRecord(tab, id))

	// A full cache: the first put fails, the download is failed with "space", and nothing stays.
	tab.Eval(`window.tsDownloads.remove(`+cdp.Quote(id)+`)`, nil)
	tab.WaitFor(`!(JSON.parse(localStorage.getItem("ts.offline") || "{}").books || {})[`+cdp.Quote(id)+`]`, 10*time.Second)
	tab.Eval(`window.tsDownloads.debug.failNextPut = "x"; window.tsDownloads.start(`+cdp.Quote(id)+`)`, nil)
	ofdWaitState(t, tab, id, "failed")
	if r := ofdRecord(tab, id); r.Error != "space" || r.Text == nil || r.Text.Have != 0 {
		t.Errorf("failed record = %+v", r)
	}
	tab.WaitFor(`[...document.querySelectorAll(".ts-toast")].some((t) => t.textContent.includes("There isn't room on this phone"))`, 10*time.Second)
	for _, k := range ofdKeys(tab) {
		if strings.Contains(k, id) {
			t.Errorf("%s is left in the cache after a failed download", k)
		}
	}
}

// Removing a download leaves nothing of the book in the cache, the index or the reading queue.
func TestOffDownloadRemove(t *testing.T) {
	b := chromium(t)
	chs, opt := ofrBook()
	e := ofrApp(t, chs, opt)
	pgFlow(t, e.l, "pages")
	ofdTextOnly(t, e)
	id := e.l.book.ID
	tab := b.NewTab(390, 844, true, true)
	ofdDownload(t, e, tab)
	tab.Eval(`localStorage.setItem("ts.offline.reading", JSON.stringify({`+cdp.Quote(id)+`: { form: "x", chars: 0, active: 0, sent: true }}))`, nil)
	tab.Eval(`window.tsDownloads.remove(`+cdp.Quote(id)+`)`, nil)
	tab.WaitFor(`!(JSON.parse(localStorage.getItem("ts.offline") || "{}").books || {})[`+cdp.Quote(id)+`] && !JSON.parse(localStorage.getItem("ts.offline.reading") || "{}")[`+cdp.Quote(id)+`]`, 10*time.Second)
	// The purge ends after the record is gone; wait for the cache to settle.
	deadline := time.Now().Add(10 * time.Second)
	var left []string
	for time.Now().Before(deadline) {
		left = left[:0]
		for _, k := range ofdKeys(tab) {
			if strings.Contains(k, id) {
				left = append(left, k)
			}
		}
		if len(left) == 0 {
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Errorf("left in the cache: %v", left)
}

// The daily check: a new highlight stores the text again, another edition's addresses are replaced, a network
// failure keeps the text and a 404 removes it.
func TestOffDownloadRefresh(t *testing.T) {
	b := chromium(t)
	chs, opt := ofrBook()
	e := ofrApp(t, chs, opt)
	pgFlow(t, e.l, "pages")
	ofdTextOnly(t, e)
	id := e.l.book.ID
	tab := b.NewTab(390, 844, true, true)
	rec := ofdDownload(t, e, tab)
	first := rec.Text.Revision
	check := func() { tab.Eval(`window.tsDownloads.check(true)`, nil) }

	// A highlight made online: a new revision, and the stored part shows it.
	sid := ebook.SentenceID(e.l.doc.Chapters[0].Blocks[0].ID, 0)
	if _, _, err := e.l.a.srv.progress.AddHighlight(context.Background(), progress.Highlight{UserID: e.l.ownerID, BookID: id, EditionID: e.l.ed.ID, Chapter: 1,
		StartSID: sid, EndSID: sid, StartOff: 0, EndOff: 3, Quote: "The"}); err != nil {
		t.Fatal(err)
	}
	check()
	tab.WaitFor(`(((JSON.parse(localStorage.getItem("ts.offline") || "{}").books || {})[`+cdp.Quote(id)+`] || {}).text || {}).revision !== `+cdp.Quote(first), 20*time.Second)
	rec = ofdRecord(tab, id)
	ofdWantText(t, tab, id, rec)
	var stored string
	tab.Eval(`(async () => { const c = await caches.open("ts-person-" + document.documentElement.dataset.tsPerson); return await (await c.match(`+cdp.Quote(rec.Text.Parts[0])+`)).text(); })()`, &stored)
	if want := e.l.get(e.l.owner, rec.Text.Parts[0], true).Body.String(); stored != want {
		t.Error("the stored first part is not the server's part with the highlight")
	}

	// Another edition: a stale address and a stale revision. The refresh drops the address.
	stale := "/hx/books/" + id + "/read/chapters/9?edition=old"
	tab.Eval(`(async () => {
  const c = await caches.open("ts-person-" + document.documentElement.dataset.tsPerson);
  await c.put(`+cdp.Quote(stale)+`, new Response("old"));
  const index = JSON.parse(localStorage.getItem("ts.offline"));
  const text = index.books[`+cdp.Quote(id)+`].text;
  text.revision = "old";
  text.parts.push(`+cdp.Quote(stale)+`);
  localStorage.setItem("ts.offline", JSON.stringify(index));
})()`, nil)
	check()
	tab.WaitFor(`(((JSON.parse(localStorage.getItem("ts.offline") || "{}").books || {})[`+cdp.Quote(id)+`] || {}).text || {}).revision === `+cdp.Quote(rec.Text.Revision), 20*time.Second)
	if slices.Contains(ofdKeys(tab), stale) {
		t.Error("an address of the old edition is still stored")
	}
	ofdWantText(t, tab, id, ofdRecord(tab, id))

	// The server cannot be reached: nothing changes.
	e.down.Store(true)
	check()
	time.Sleep(1500 * time.Millisecond)
	e.down.Store(false)
	if r := ofdRecord(tab, id); !r.Found || r.Text == nil {
		t.Error("a network failure removed the text")
	}
	ofdWantText(t, tab, id, ofdRecord(tab, id))

	// The text is gone: the manifest answers 404 and the text-only book is removed.
	if _, err := e.l.a.st.Writer().Exec(`DELETE FROM ebook_files WHERE book_id = ?`, id); err != nil {
		t.Fatal(err)
	}
	check()
	tab.WaitFor(`!(JSON.parse(localStorage.getItem("ts.offline") || "{}").books || {})[`+cdp.Quote(id)+`]`, 20*time.Second)
}

// A text-only book takes its finished date from the manifest, so "Remove finished books" works for it.
func TestOffDownloadFinished(t *testing.T) {
	b := chromium(t)
	chs, opt := ofrBook()
	e := ofrApp(t, chs, opt)
	pgFlow(t, e.l, "pages")
	ofdTextOnly(t, e)
	id := e.l.book.ID
	tab := b.NewTab(390, 844, true, true)
	ofdDownload(t, e, tab)
	finished := time.Now().Add(-10 * 24 * time.Hour).UnixMilli()
	if _, err := e.l.a.st.Writer().Exec(`INSERT INTO reading_positions(user_id, book_id, edition_id, chapter_idx, char_pos, percent_bp, finished_at, client_updated_at, server_updated_at, device_id)
		VALUES (?, ?, ?, 1, 0, 10000, ?, ?, ?, 'ofddevice001')`, e.l.ownerID, id, e.l.ed.ID, finished, finished, finished); err != nil {
		t.Fatal(err)
	}
	tab.Eval(`window.tsDownloads.check(true)`, nil)
	tab.WaitFor(`((JSON.parse(localStorage.getItem("ts.offline") || "{}").books || {})[`+cdp.Quote(id)+`] || {}).finishedAt > 0`, 20*time.Second)
	// The setting on: the next check removes it.
	tab.Eval(`localStorage.setItem("ts.offline.prefs", JSON.stringify({ removeFinished: true })); window.tsDownloads.check(true)`, nil)
	tab.WaitFor(`!(JSON.parse(localStorage.getItem("ts.offline") || "{}").books || {})[`+cdp.Quote(id)+`]`, 20*time.Second)
}
