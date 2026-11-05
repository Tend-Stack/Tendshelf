//go:build dev

package web

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// TestOffDownloadCapture records the changed surfaces at 390, 768, 1024 and 1440: the book page with its
// Download control, the Downloads page with a text row, and the offline shell with a Read button, beside
// Play (a book with both) and alone (a book with text only). Output: .local/tests/offdownload/<run>/.
func TestOffDownloadCapture(t *testing.T) {
	b := chromium(t)
	out := os.Getenv("TENDSHELF_VISUAL_OUT")
	if out == "" {
		out = filepath.Join("..", "..", ".local", "tests", "offdownload", time.Now().UTC().Format("20060102T150405Z"))
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	chs, opt := ofrBook()
	e := ofrApp(t, chs, opt)
	pgFlow(t, e.l, "pages")
	ofdWithAudio(t, e)
	id := e.l.book.ID
	widths := []struct {
		name   string
		w, h   int
		mobile bool
	}{{"390", 390, 844, true}, {"768", 768, 1024, false}, {"1024", 1024, 768, false}, {"1440", 1440, 900, false}}
	for _, vp := range widths {
		tab := b.NewTab(vp.w, vp.h, vp.mobile, true)
		ofdDownload(t, e, tab)
		ofdHeld(t, e, tab)
		shot := func(name string) {
			time.Sleep(400 * time.Millisecond)
			tab.ScreenshotFull(filepath.Join(out, name+"-"+vp.name+".png"))
		}
		tab.Navigate(e.URL + "/books/" + id)
		tab.WaitFor(`document.querySelector("[data-ts-download-status]") && !document.querySelector("[data-ts-download-status]").hidden`, 10*time.Second)
		shot("book-ready")
		tab.Navigate(e.URL + "/me/downloads")
		tab.WaitFor(`document.querySelector('[data-ts-ready] [data-id="`+id+`"]')`, 10*time.Second)
		shot("downloads")
		tab.Navigate(e.URL + "/offline?books=" + id + "&text=" + id)
		tab.WaitFor(`document.querySelector("[data-ts-offline-shell]")`, 10*time.Second)
		shot("offline-both")
		tab.Eval(`window.tsDownloads && window.tsDownloads.remove(`+cdp.Quote(id)+`)`, nil)
		tab.Close()
	}
	ofdTextOnly(t, e)
	for _, vp := range widths {
		tab := b.NewTab(vp.w, vp.h, vp.mobile, true)
		tab.Navigate(e.URL + "/offline?books=" + id + "&text=" + id)
		tab.WaitFor(`document.querySelector("[data-ts-offline-shell]")`, 10*time.Second)
		time.Sleep(400 * time.Millisecond)
		tab.ScreenshotFull(filepath.Join(out, "offline-text-"+vp.name+".png"))
		tab.Close()
	}
	t.Logf("captures: %s", out)
}
