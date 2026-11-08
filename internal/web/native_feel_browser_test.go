//go:build dev

package web

import (
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// Android feel in the app's window (decision 0033, F1). Needs TENDSHELF_TEST_BROWSER. The stub tsNative of
// native_speech_browser_test.go takes the page's words and lets the test push the app's answers.

var nfViewports = []alVP{{"mobile-390", 390, 844, true}, {"desktop-1440", 1440, 900, false}}

// nfOpen opens the reader as the app's window does, always in pages, with extra script run before the page's own.
func nfOpen(t *testing.T, b *cdp.Browser, e *ofrEnv, vp alVP, before string) *cdp.Tab {
	t.Helper()
	pgFlow(t, e.l, "pages")
	tab := b.NewTab(vp.w, vp.h, vp.mobile, true)
	_ = tab.Call("Page.enable", nil, nil)
	if err := tab.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": nsStub + before}, nil); err != nil {
		t.Fatal(err)
	}
	next := "/books/" + e.l.book.ID + "/read?ch=2"
	tab.Navigate(e.URL + "/?shell=android&next=" + url.QueryEscape(next))
	tab.WaitFor(ofrMounted, 10*time.Second)
	return tab
}

func nfPage(tab *cdp.Tab) (page int) {
	pgEval(tab, `pgPage()`, &page)
	return page
}

func TestNativeFeelBrowserPageKeys(t *testing.T) {
	b := chromium(t)
	for _, vp := range nfViewports {
		t.Run(vp.name, func(t *testing.T) {
			e := ofrApp(t, alBook(), nil)
			cfSet(t, e, "volume_keys", "1")
			tab := nfOpen(t, b, e, vp, "")
			tab.WaitFor(`window.__sent.includes("pagekeys 1")`, 8*time.Second)

			nsPush(tab, `{"t":"pagekey","dir":"next"}`)
			pgWaitFor(tab, `pgPage() === 1`)
			nsPush(tab, `{"t":"pagekey","dir":"prev"}`)
			pgWaitFor(tab, `pgPage() === 0`)
			nsPush(tab, `{"t":"pagekey","dir":"next"}`)
			pgWaitFor(tab, `pgPage() === 1`)
			// Unknown directions and broken JSON do nothing.
			nsPush(tab, `{"t":"pagekey","dir":"sideways"}`)
			nsPush(tab, `not json`)
			time.Sleep(300 * time.Millisecond)
			if got := nfPage(tab); got != 1 {
				t.Errorf("page %d after answers that must be ignored, want 1", got)
			}

			// A boosted link away from the reader gives the keys back.
			tab.Eval(`document.querySelector(".ts-readerbar a").click()`, nil)
			tab.WaitFor(`document.getElementById("reader") === null`, 10*time.Second)
			tab.WaitFor(`window.__sent.includes("pagekeys 0")`, 8*time.Second)

			// Off: nothing is sent and a pushed answer does nothing.
			cfSet(t, e, "volume_keys", "0")
			off := nfOpen(t, b, e, vp, "")
			time.Sleep(500 * time.Millisecond)
			if sent := nsSent(off); slices.Contains(sent, "pagekeys 1") {
				t.Errorf("sent %v with the setting off", sent)
			}
			nsPush(off, `{"t":"pagekey","dir":"next"}`)
			time.Sleep(300 * time.Millisecond)
			if got := nfPage(off); got != 0 {
				t.Errorf("page %d, a pushed key moved a reader with the setting off", got)
			}
		})
	}
}

func TestNativeFeelBrowserWake(t *testing.T) {
	b := chromium(t)
	for _, vp := range nfViewports {
		t.Run(vp.name, func(t *testing.T) {
			e := ofrApp(t, alBook(), nil)
			cfSet(t, e, "keep_awake", "1")
			// The page's own lock is installed and must stay unused: the app's stand-in wins.
			tab := nfOpen(t, b, e, vp, cfFakeLock)
			tab.WaitFor(`window.__sent.includes("wake 1")`, 20*time.Second)
			var taken int
			tab.Eval(`window.__locks.taken`, &taken)
			if taken != 0 {
				t.Errorf("navigator.wakeLock taken %d times", taken)
			}
			tab.Eval(`document.querySelector(".ts-readerbar a").click()`, nil)
			tab.WaitFor(`document.getElementById("reader") === null`, 10*time.Second)
			tab.WaitFor(`window.__sent.includes("wake 0")`, 20*time.Second)
		})
	}
}

func TestNativeFeelBrowserQuiet(t *testing.T) {
	b := chromium(t)
	for _, vp := range nfViewports {
		t.Run(vp.name, func(t *testing.T) {
			e := ofrApp(t, alBook(), nil)
			tab := b.NewTab(vp.w, vp.h, vp.mobile, true)
			_ = tab.Call("Page.enable", nil, nil)
			if err := tab.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": nsStub}, nil); err != nil {
				t.Fatal(err)
			}
			tab.Navigate(e.URL + "/?shell=android&next=" + url.QueryEscape("/me/settings/playback"))
			tab.WaitFor(`document.getElementById("st-quiet") !== null`, 10*time.Second)
			tab.Eval(`document.getElementById("st-quiet").click()`, nil)
			tab.WaitFor(`window.__sent.includes("quiet 1")`, 8*time.Second)
			tab.Eval(`document.getElementById("st-quiet").click()`, nil)
			tab.WaitFor(`window.__sent.includes("quiet 0")`, 8*time.Second)
		})
	}
}
