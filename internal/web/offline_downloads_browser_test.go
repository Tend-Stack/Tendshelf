//go:build dev

package web

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
	"github.com/Tend-Stack/tendshelf/internal/testutil/wav"
)

// The download manager (decision 0012, package B2) in a real browser: a download from the book page
// (T2), pause, reload, resume and cancel (T4), a full cache (T7) and a file that needs a compatible
// copy (T8).
// Run by: TENDSHELF_TEST_BROWSER=<chromium> go test -tags dev -run OfflineDownload ./internal/web/

// offlineCacheJS lists the person cache's keys and the bytes held in chunks.
const offlineCacheJS = `(async () => {
  const cache = await caches.open("ts-person-" + document.documentElement.dataset.tsPerson);
  const keys = [];
  let bytes = 0;
  for (const req of await cache.keys()) {
    const u = new URL(req.url);
    keys.push(u.pathname + u.search);
    if (u.search.includes("chunk=")) bytes += (await (await cache.match(req)).arrayBuffer()).byteLength;
  }
  return { keys, bytes };
})()`

type offlineCache struct {
	Keys  []string
	Bytes int
}

func (e *offlineEnv) cacheDump(t *testing.T, tab *cdp.Tab) offlineCache {
	t.Helper()
	var c offlineCache
	tab.Eval(offlineCacheJS, &c)
	return c
}

// downloadsPage opens the Downloads page and waits for the manager.
func (e *offlineEnv) downloadsPage(t *testing.T, tab *cdp.Tab) {
	t.Helper()
	tab.Navigate(e.URL + "/me/downloads")
	tab.WaitFor(`window.tsDownloads !== undefined`, 10*time.Second)
}

func (e *offlineEnv) startDownload(t *testing.T, tab *cdp.Tab) {
	t.Helper()
	tab.Eval(`window.tsDownloads.start(`+cdp.Quote(e.book.ID)+`)`, nil)
}

// wantReadyCache checks that every file is whole: its completion entry, exactly its chunks, the
// player fragment, and (when no file is a compatible copy) that the bytes add up to the sizes.
// compatFile is the index of the file stored as a compatible copy, or -1.
func (e *offlineEnv) wantReadyCache(t *testing.T, c offlineCache, compatFile int) {
	t.Helper()
	var total int64
	for i, f := range e.files {
		key := "/stream/" + f.ID
		sep := "?"
		if i == compatFile {
			key += "?compat=1"
			sep = "&"
		}
		chunks := 0
		for _, k := range c.Keys {
			if strings.HasPrefix(k, key+sep+"chunk=") {
				chunks++
			}
		}
		if !slices.Contains(c.Keys, key) || chunks < 1 {
			t.Errorf("file %d: completion entry %v, %d chunks", i, slices.Contains(c.Keys, key), chunks)
		}
		if i != compatFile {
			if want := int((f.Size + offlineChunk - 1) / offlineChunk); chunks != want {
				t.Errorf("file %d: %d chunks, want %d", i, chunks, want)
			}
			total += f.Size
		}
	}
	if !slices.Contains(c.Keys, "/hx/player/"+e.book.ID) {
		t.Errorf("the player fragment is not stored: %v", c.Keys)
	}
	if compatFile < 0 && int64(c.Bytes) != total {
		t.Errorf("stored %d bytes, want %d", c.Bytes, total)
	}
}

func tabHidden(t *testing.T, tab *cdp.Tab, sel string) bool {
	t.Helper()
	var hidden bool
	tab.Eval(`document.querySelector(`+cdp.Quote(sel)+`).hidden`, &hidden)
	return hidden
}

// T2: Download on the book page, the Downloads page lists the book as ready, nothing is half stored.
func TestOfflineDownloadFromBookPage(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)
	tab := e.offlineTab(t, b)
	tab.Navigate(e.URL + "/books/" + e.book.ID)
	tab.WaitFor(`window.tsDownloads !== undefined && !document.querySelector("[data-ts-download]").hidden`, 10*time.Second)
	tab.Eval(`document.querySelector("[data-ts-download]").click()`, nil)
	tab.WaitFor(`window.tsDownloads.list().some((r) => r.state === "ready") && !document.querySelector("[data-ts-download-status]").hidden`, 30*time.Second)
	var label string
	tab.Eval(`document.querySelector("[data-ts-download-label]").textContent`, &label)
	if label == "" || !tabHidden(t, tab, "[data-ts-download]") {
		t.Errorf("book page: label %q, download button hidden %v", label, tabHidden(t, tab, "[data-ts-download]"))
	}

	e.downloadsPage(t, tab)
	tab.WaitFor(`document.querySelector("[data-ts-ready-list] li")`, 10*time.Second)
	var row string
	tab.Eval(`document.querySelector("[data-ts-ready-list] li").textContent`, &row)
	if !strings.Contains(row, "Harbour Lamp") || !strings.Contains(row, "Ready offline") {
		t.Errorf("ready row = %q", row)
	}
	if !tabHidden(t, tab, "[data-ts-empty]") || tabHidden(t, tab, "[data-ts-ready]") {
		t.Error("the empty state shows beside a ready book")
	}
	e.wantReadyCache(t, e.cacheDump(t, tab), -1)
}

// T4: pause after one chunk, reload, resume: the bytes add up; cancel leaves no key of the book.
func TestOfflineDownloadPauseResumeCancel(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)
	tab := e.offlineTab(t, b)
	e.downloadsPage(t, tab)

	e.slow.Store(400)
	e.startDownload(t, tab)
	tab.WaitFor(`window.tsDownloads.list().length === 1 && window.tsDownloads.list()[0].done > 0`, 20*time.Second)
	tab.Eval(`window.tsDownloads.pause(`+cdp.Quote(e.book.ID)+`)`, nil)
	var r struct {
		State       string
		Done, Bytes int64
	}
	tab.WaitFor(`window.tsDownloads.list()[0].state === "paused"`, 5*time.Second)
	tab.Eval(`window.tsDownloads.list()[0]`, &r)
	if r.Done <= 0 || r.Done >= r.Bytes {
		t.Fatalf("paused with %d of %d bytes, want part of the book", r.Done, r.Bytes)
	}

	// A reload keeps the pause; Resume finishes the book.
	e.slow.Store(0)
	e.downloadsPage(t, tab)
	time.Sleep(time.Second)
	tab.Eval(`window.tsDownloads.list()[0]`, &r)
	if r.State != "paused" {
		t.Fatalf("after the reload the state is %q, want paused", r.State)
	}
	tab.Eval(`document.querySelector('[data-ts-active-list] [data-ts-act="resume"]').click()`, nil)
	tab.WaitFor(`window.tsDownloads.list()[0].state === "ready"`, 30*time.Second)
	e.wantReadyCache(t, e.cacheDump(t, tab), -1)

	// Cancel in the middle of a second download leaves nothing of the book.
	tab.Eval(`window.tsDownloads.remove(`+cdp.Quote(e.book.ID)+`)`, nil)
	tab.WaitFor(`window.tsDownloads.list().length === 0`, 5*time.Second)
	e.slow.Store(400)
	e.startDownload(t, tab)
	tab.WaitFor(`window.tsDownloads.list().length === 1 && window.tsDownloads.list()[0].done > 0`, 20*time.Second)
	tab.Eval(`window.tsDownloads.cancel(`+cdp.Quote(e.book.ID)+`)`, nil)
	tab.WaitFor(`window.tsDownloads.list().length === 0`, 5*time.Second)
	time.Sleep(time.Second) // a late chunk would show here
	ids := []string{e.book.ID, e.files[0].ID, e.files[1].ID}
	for _, k := range e.cacheDump(t, tab).Keys {
		for _, id := range ids {
			if strings.Contains(k, id) {
				t.Errorf("cancel left %s", k)
			}
		}
	}
}

// T7: a full cache stops the download, says so in plain words and leaves no chunks.
func TestOfflineDownloadQuota(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)
	tab := e.offlineTab(t, b)
	e.downloadsPage(t, tab)

	e.slow.Store(300)
	e.startDownload(t, tab)
	tab.WaitFor(`window.tsDownloads.list().length === 1 && window.tsDownloads.list()[0].done > 0`, 20*time.Second)
	tab.Eval(`window.tsDownloads.debug.failNextPut = "quota"`, nil)
	tab.WaitFor(`window.tsDownloads.list()[0].state === "failed"`, 10*time.Second)
	tab.WaitFor(`document.querySelector("[data-ts-active-list] [data-ts-meta]")?.textContent.includes("room on this phone")`, 5*time.Second)
	var row string
	tab.Eval(`document.querySelector("[data-ts-active-list] [data-ts-meta]").textContent`, &row)
	if !strings.Contains(row, "Harbour Lamp") {
		t.Errorf("failed row = %q", row)
	}
	for _, k := range e.cacheDump(t, tab).Keys {
		if strings.Contains(k, "chunk=") || strings.Contains(k, e.files[0].ID) {
			t.Errorf("a failed download left %s", k)
		}
	}
}

// T8: a file this browser cannot play as it is takes the compatible copy: stored under ?compat=1,
// and the 503 while the copy is made is waited out.
func TestOfflineDownloadCompat(t *testing.T) {
	b := chromium(t)
	enq := &streamEnq{}
	e := offlineAppWith(t, func(o *Options) {
		o.Compat = mediajobs.NewCompat(o.Library, media.Tools{}, mediajobs.CompatDir(o.DataDir), 1<<30, enq, nil)
	})
	streamExec(t, e.a, `UPDATE audio_files SET container='ogg', codec='opus' WHERE id=?`, e.files[0].ID)
	// The copy appears a moment after the first request for it.
	go func() {
		for i := 0; i < 400 && enq.count() == 0; i++ {
			time.Sleep(50 * time.Millisecond)
		}
		dir := mediajobs.CompatDir(e.a.dir)
		_ = os.MkdirAll(dir, 0o755)
		_ = os.WriteFile(filepath.Join(dir, e.files[0].QuickHash+"-r"+strconv.Itoa(media.CompatRecipe)+".m4a"), wav.Bytes(5000, 440), 0o644)
	}()
	tab := e.offlineTab(t, b)
	e.downloadsPage(t, tab)
	tab.Eval(`HTMLMediaElement.prototype.canPlayType = () => ""`, nil)
	e.startDownload(t, tab)
	tab.WaitFor(`window.tsDownloads.list()[0]?.state === "ready"`, 40*time.Second)
	if enq.count() != 1 {
		t.Errorf("%d conversions were asked for, want 1", enq.count())
	}
	c := e.cacheDump(t, tab)
	key := "/stream/" + e.files[0].ID
	if !slices.Contains(c.Keys, key+"?compat=1") || !slices.Contains(c.Keys, key+"?compat=1&chunk=0") || slices.Contains(c.Keys, key) {
		t.Errorf("compat file keys: %v", c.Keys)
	}
	e.wantReadyCache(t, c, 0)
}
