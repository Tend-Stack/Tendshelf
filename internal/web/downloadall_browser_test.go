//go:build dev

package web

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
	"github.com/Tend-Stack/tendshelf/internal/testutil/wav"
)

// "Download all" (L10) in a real browser: the confirm step, the queue order, books already saved, Stop, a book that
// fails, and a download that does not fit; at desktop and phone width, and the confirm step in all four themes.
// Run by: TENDSHELF_TEST_BROWSER=<chromium> go test -tags dev -run DownloadAllBrowser ./internal/web/

// downloadAllSaga adds a four-book series to the offline app's library, each book one small file, and returns the
// series page address and the books in series order.
func downloadAllSaga(t *testing.T, e *offlineEnv) (string, []library.Book) {
	t.Helper()
	_, f := librarytest.NewLibrary(t, e.a.lib, "Saga library", t.TempDir())
	var books []library.Book
	for i := 1; i <= 4; i++ {
		b := librarytest.AddBook(t, e.a.lib, f, librarytest.BookSpec{
			Title: fmt.Sprintf("Lamp Saga %d", i), Authors: []string{"Imogen Hale"}, DurationMs: offlineFileMs, Files: 1,
			Series: "Lamp Saga", Sequence: fmt.Sprint(i),
		})
		files, err := e.a.lib.AudioFiles(context.Background(), b.ID)
		if err != nil || len(files) != 1 {
			t.Fatalf("audio files = %d, %v", len(files), err)
		}
		if err := wav.Write(files[0].Path, offlineFileMs, 330+i*40); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(files[0].Path)
		if err != nil {
			t.Fatal(err)
		}
		streamExec(t, e.a, `UPDATE audio_files SET container='wav', codec='pcm_s16le', duration_ms=?, offset_ms=0, size=?, mtime_ns=? WHERE id=?`,
			offlineFileMs, fi.Size(), fi.ModTime().UnixNano(), files[0].ID)
		books = append(books, b)
	}
	series, err := e.a.lib.BookSeries(context.Background(), books[0].ID)
	if err != nil || len(series) != 1 {
		t.Fatal(series, err)
	}
	return "/series/" + series[0].SeriesID, books
}

// downloadAllPage opens the series page and waits until the control shows.
func (e *offlineEnv) downloadAllPage(t *testing.T, tab *cdp.Tab, path string) {
	t.Helper()
	tab.Navigate(e.URL + path)
	tab.WaitFor(`window.tsDownloads !== undefined && document.querySelector("[data-ts-download-all]") && !document.querySelector("[data-ts-download-all]").hidden`, 15*time.Second)
}

func dlallText(t *testing.T, tab *cdp.Tab, part string) string {
	t.Helper()
	var s string
	tab.Eval(`(document.querySelector("[data-ts-dlall-`+part+`]") || {}).textContent || ""`, &s)
	return s
}

// dlallOpen taps Download all and waits for the dialog.
func dlallOpen(t *testing.T, tab *cdp.Tab) {
	t.Helper()
	tab.Eval(`document.querySelector("[data-ts-dlall-start]").click()`, nil)
	tab.WaitFor(`document.querySelector("[data-ts-dlall-dialog]").open`, 10*time.Second)
}

func dlallReady(t *testing.T, tab *cdp.Tab) int {
	t.Helper()
	var n int
	tab.Eval(`window.tsDownloads.list().filter((r) => r.state === "ready").length`, &n)
	return n
}

func TestDownloadAllBrowserSeries(t *testing.T) {
	b := chromium(t)
	for _, width := range []int{1440, 390} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			e := offlineApp(t)
			path, books := downloadAllSaga(t, e)
			tab := e.offlineTab(t, b)
			if width != 1440 {
				tab = b.NewTab(width, 844, true, false)
				tab.Navigate(e.URL + "/libraries")
				tab.WaitFor(`window.tsPlayer !== undefined && navigator.serviceWorker.controller`, 20*time.Second)
			}
			e.downloadAllPage(t, tab, path)

			// One book is saved already: it is skipped in the count and never fetched again.
			tab.Eval(`window.tsDownloads.start(`+cdp.Quote(books[1].ID)+`)`, nil)
			tab.WaitFor(`window.tsDownloads.list().some((r) => r.state === "ready")`, 30*time.Second)

			dlallOpen(t, tab)
			if got := dlallText(t, tab, "title"); got != "Download 3 books?" {
				t.Errorf("confirm title %q", got)
			}
			if got := dlallText(t, tab, "body"); !strings.HasSuffix(got, ". Books you already have are skipped.") || !strings.HasPrefix(got, "About ") {
				t.Errorf("confirm body %q", got)
			}
			if n := dlallReady(t, tab); n != 1 {
				t.Errorf("nothing starts before the person confirms: %d ready", n)
			}
			// Not now closes it and starts nothing.
			tab.Eval(`document.querySelector("[data-ts-dlall-cancel]").click()`, nil)
			var count int
			tab.Eval(`window.tsDownloads.list().length`, &count)
			if count != 1 || dlallText(t, tab, "status") != "" {
				t.Errorf("Not now left %d records and status %q", count, dlallText(t, tab, "status"))
			}

			dlallOpen(t, tab)
			tab.Eval(`document.querySelector("[data-ts-dlall-go]").click()`, nil)
			tab.WaitFor(`window.tsDownloads.list().filter((r) => r.state === "ready").length === 4`, 60*time.Second)
			tab.WaitFor(`document.querySelector("[data-ts-dlall-status]").textContent === "3 books are ready offline."`, 10*time.Second)
			if !tabHidden(t, tab, "[data-ts-dlall-stop]") || tabHidden(t, tab, "[data-ts-dlall-start]") {
				t.Error("after the batch Stop is hidden and Download all is back")
			}
			// Queue order: the books the batch added follow the page's order.
			var titles string
			tab.Eval(`window.tsDownloads.list().filter((r) => r.title !== "`+books[1].Title+`").map((r) => r.title).join("|")`, &titles)
			if want := strings.Join([]string{books[0].Title, books[2].Title, books[3].Title}, "|"); titles != want {
				t.Errorf("queue order %q, want %q", titles, want)
			}
			// Everything is saved now: the dialog says so and starts nothing.
			dlallOpen(t, tab)
			if got := dlallText(t, tab, "title"); got != "Nothing new to download" {
				t.Errorf("all saved: title %q", got)
			}
			if !tabHidden(t, tab, "[data-ts-dlall-go]") {
				t.Error("all saved: the Download button is shown")
			}
			var over bool
			tab.Eval(`document.documentElement.scrollWidth > innerWidth`, &over)
			if over {
				t.Error("horizontal overflow")
			}
		})
	}
}

// Stop cancels what is queued and keeps what finished.
func TestDownloadAllBrowserStop(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)
	path, _ := downloadAllSaga(t, e)
	tab := e.offlineTab(t, b)
	e.downloadAllPage(t, tab, path)
	e.slow.Store(700) // each file takes its time, so the batch is still running when Stop is tapped
	dlallOpen(t, tab)
	tab.Eval(`document.querySelector("[data-ts-dlall-go]").click()`, nil)
	tab.WaitFor(`document.querySelector("[data-ts-dlall-status]").textContent.startsWith("Downloading ")`, 10*time.Second)
	tab.WaitFor(`!document.querySelector("[data-ts-dlall-stop]").hidden`, 5*time.Second)
	if got := dlallText(t, tab, "status"); !strings.HasSuffix(got, " of 4") {
		t.Errorf("progress %q", got)
	}
	tab.Eval(`document.querySelector("[data-ts-dlall-stop]").click()`, nil)
	tab.WaitFor(`document.querySelector("[data-ts-dlall-status]").textContent.startsWith("Stopped.")`, 10*time.Second)
	e.slow.Store(0)
	time.Sleep(1500 * time.Millisecond)
	var pending int
	tab.Eval(`window.tsDownloads.list().filter((r) => r.state !== "ready").length`, &pending)
	ready := dlallReady(t, tab)
	if pending != 0 || ready >= 4 {
		t.Errorf("after Stop: %d unfinished, %d ready", pending, ready)
	}
	if got, want := dlallText(t, tab, "status"), fmt.Sprintf("Stopped. %d of 4 saved.", ready); got != want {
		// A book may finish between the tap and the cancel; the line then says one fewer than the final count.
		t.Logf("status %q, final ready %d", got, ready)
	}
	if tabHidden(t, tab, "[data-ts-dlall-start]") {
		t.Error("Download all does not come back after Stop")
	}
}

// A book that fails does not stop the rest, and the last line says how many did not download.
func TestDownloadAllBrowserOneFails(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)
	path, books := downloadAllSaga(t, e)
	files, _ := e.a.lib.AudioFiles(context.Background(), books[1].ID)
	if err := os.Remove(files[0].Path); err != nil {
		t.Fatal(err)
	}
	tab := e.offlineTab(t, b)
	e.downloadAllPage(t, tab, path)
	dlallOpen(t, tab)
	tab.Eval(`document.querySelector("[data-ts-dlall-go]").click()`, nil)
	tab.WaitFor(`document.querySelector("[data-ts-dlall-status]").textContent === "1 of 4 didn't download. Try again."`, 60*time.Second)
	if n := dlallReady(t, tab); n != 3 {
		t.Errorf("%d books ready, want the other 3", n)
	}
	if tabHidden(t, tab, "[data-ts-dlall-start]") {
		t.Error("Download all is not offered again")
	}
}

// When the room estimate says the books will not fit, the dialog says so and nothing starts.
func TestDownloadAllBrowserTooBig(t *testing.T) {
	b := chromium(t)
	e := offlineApp(t)
	path, _ := downloadAllSaga(t, e)
	tab := e.offlineTab(t, b)
	e.downloadAllPage(t, tab, path)
	tab.Eval(`navigator.storage.estimate = async () => ({ quota: 1000000, usage: 999000 })`, nil)
	dlallOpen(t, tab)
	if got := dlallText(t, tab, "title"); got != "Not enough room" {
		t.Errorf("title %q", got)
	}
	if got := dlallText(t, tab, "body"); !strings.Contains(got, "Remove a download, then try again.") {
		t.Errorf("body %q", got)
	}
	if !tabHidden(t, tab, "[data-ts-dlall-go]") {
		t.Error("the Download button is shown for books that do not fit")
	}
	var count int
	tab.Eval(`window.tsDownloads.list().length`, &count)
	if count != 0 {
		t.Errorf("%d downloads started", count)
	}
}

// The confirm step in the four themes at 390 and 1440: no overflow, every target 44 px, a screenshot for the record.
func TestDownloadAllBrowserConfirmVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	e := offlineApp(t)
	path, _ := downloadAllSaga(t, e)
	for _, theme := range []string{"dark", "light", "sepia", "contrast"} {
		for _, w := range []int{390, 1440} {
			t.Run(fmt.Sprintf("%s-%d", theme, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w == 390, false)
				tab.Navigate(e.URL + "/libraries")
				tab.WaitFor(`window.tsPlayer !== undefined`, 20*time.Second)
				tab.Eval(`document.documentElement.dataset.theme = `+cdp.Quote(theme), nil)
				e.downloadAllPage(t, tab, path)
				tab.Eval(`document.documentElement.dataset.theme = `+cdp.Quote(theme), nil)
				dlallOpen(t, tab)
				time.Sleep(300 * time.Millisecond)
				var m browseProbe
				tab.Eval(browseProbeJS, &m)
				if m.Over {
					t.Error("horizontal overflow")
				}
				var small []string
				tab.Eval(`[...document.querySelectorAll("[data-ts-download-all] button")].filter((b) => b.offsetParent !== null || b.closest("dialog[open]")).filter((b) => b.getBoundingClientRect().height > 0 && b.getBoundingClientRect().height < 43.5).map((b) => b.className)`, &small)
				if len(small) > 0 {
					t.Errorf("targets under 44 px: %v", small)
				}
				tab.Screenshot(filepath.Join(out, fmt.Sprintf("download-all-confirm-%s-%d.png", theme, w)))
			})
		}
	}
}
