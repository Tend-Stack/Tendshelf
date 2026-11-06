//go:build dev

package web

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// a4Stub stands in for the object the Android app adds (WebViewCompat.addWebMessageListener): it records every
// string the page posts and lets a test push the app's state through onmessage. It is installed before any script.
const a4Stub = `(() => {
  window.__sent = [];
  window.__beacons = 0;
  navigator.sendBeacon = () => { window.__beacons++; return true; };
  window.tsNative = { postMessage: (m) => window.__sent.push(m), onmessage: null };
})();`

func a4Tab(t *testing.T, b *cdp.Browser, w int) *cdp.Tab {
	t.Helper()
	tab := b.NewTab(w, 900, w < 768, false)
	_ = tab.Call("Page.enable", nil, nil)
	if err := tab.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": a4Stub}, nil); err != nil {
		t.Fatal(err)
	}
	return tab
}

func a4Push(tab *cdp.Tab, json string) {
	tab.Eval(fmt.Sprintf(`window.tsNative.onmessage({data: %s})`, cdp.Quote(json)), nil)
}

func a4State(book string, ms int64, playing bool, speed string) string {
	return fmt.Sprintf(`{"t":"state","bookId":%q,"positionMs":%d,"durationMs":180000,"playing":%v,"buffering":false,"ended":false,"speed":%s,"chapterIdx":0}`,
		book, ms, playing, speed)
}

func a4Sent(tab *cdp.Tab) []string {
	var sent []string
	tab.Eval(`window.__sent`, &sent)
	return sent
}

func a4WaitSent(t *testing.T, tab *cdp.Tab, words string) {
	t.Helper()
	tab.WaitFor(fmt.Sprintf(`window.__sent.includes(%s)`, cdp.Quote(words)), 8*time.Second)
}

// TestNativeAudioBrowser drives the web player the way the Android app does: a fake tsNative takes the page's words
// and pushes state. The bar must follow each push, say exact words back, and never save, ask for the next book or
// beacon, because native owns the place (A4, invariant 4).
func TestNativeAudioBrowser(t *testing.T) {
	for _, w := range []int{390, 1440} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			b := chromium(t)
			e := playerApp(t)
			log := &requestLog{}
			srv := httptest.NewServer(log.wrap(e.spy))
			t.Cleanup(srv.Close)
			tab := a4Tab(t, b, w)
			tab.Navigate(srv.URL + "/?shell=android&next=/libraries")
			tab.WaitFor(`window.tsPlayer !== undefined && window.tsNativeAudio !== undefined`, 10*time.Second)
			a4WaitSent(t, tab, "hello 1")

			// The page has a theme colour to give the app.
			sent := a4Sent(tab)
			themed := false
			for _, s := range sent {
				themed = themed || strings.HasPrefix(s, "theme #")
			}
			if !themed {
				t.Errorf("no theme message in %v", sent)
			}

			// Play from a button: the page asks the app to start the book at its best place, and the audio element stays empty.
			e.playerInject(tab, e.book.ID, 0, 0, "")
			playerClickSel(t, tab, "#play-link")
			a4WaitSent(t, tab, "start "+e.book.ID+" -")
			var src any
			tab.Eval(`document.getElementById("audio").getAttribute("src")`, &src)
			if src != nil {
				t.Errorf("the audio element has a src: %v", src)
			}

			// Each push changes the bar.
			a4Push(tab, a4State(e.book.ID, 45000, true, "1.00"))
			tab.WaitFor(`window.tsPlayer.state().playing === true`, 5*time.Second)
			s := playerStateOf(tab)
			if s.BookID != e.book.ID || s.PositionMs < 45000 || s.PositionMs > 47000 {
				t.Errorf("state after a push: %+v", s)
			}
			var chapter int
			tab.Eval(`window.tsPlayer.state().chapterIdx`, &chapter)
			if chapter != 1 {
				t.Errorf("chapter %d at 45 s, want 1", chapter)
			}

			// Controls say exact words.
			playerClickSel(t, tab, "[data-ts-toggle]")
			a4WaitSent(t, tab, "pause")
			a4Push(tab, a4State(e.book.ID, 46000, false, "1.00"))
			tab.WaitFor(`window.tsPlayer.state().playing === false`, 5*time.Second)
			tab.Eval(`window.tsPlayer.seek(100000)`, nil)
			a4WaitSent(t, tab, "seek 100000")
			tab.Eval(`window.tsPlayer.setSpeed(1.5)`, nil)
			a4WaitSent(t, tab, "speed 1.50")
			tab.Eval(`window.tsPlayer.skip(1)`, nil)
			tab.WaitFor(`window.__sent.some((m) => m.startsWith("seek ") && m !== "seek 100000")`, 5*time.Second)

			// The app's speed is the page's speed.
			a4Push(tab, a4State(e.book.ID, 50000, true, "2.00"))
			tab.WaitFor(`window.tsPlayer.state().speed === 2`, 5*time.Second)

			// Android Back: nothing open means the page answers 0; an open full player closes and answers 1.
			a4Push(tab, `{"t":"back"}`)
			a4WaitSent(t, tab, "back 0")
			tab.Eval(`document.querySelector("[data-ts-open]").click()`, nil)
			tab.WaitFor(`document.querySelector("dialog.ts-player[open]") !== null`, 5*time.Second)
			a4Push(tab, `{"t":"back"}`)
			a4WaitSent(t, tab, "back 1")
			tab.WaitFor(`document.querySelector("dialog.ts-player[open]") === null`, 5*time.Second)

			// Native owns the place: no save, no next-book request, no beacon, even after a tick interval and a hide.
			time.Sleep(11 * time.Second)
			tab.Eval(`document.dispatchEvent(new Event("visibilitychange")); window.dispatchEvent(new Event("pagehide"))`, nil)
			time.Sleep(300 * time.Millisecond)
			if n := log.count("/progress/") + log.count("/hx/player/next"); n != 0 {
				t.Errorf("%d save or next-book requests: %v", n, log.paths)
			}
			var beacons int
			tab.Eval(`window.__beacons`, &beacons)
			if beacons != 0 {
				t.Errorf("%d beacons", beacons)
			}
			if log.count("/stream/") != 0 {
				t.Errorf("the page streamed the book itself: %v", log.paths)
			}
		})
	}
}

// TestNativeAudioReloadAndStart: a reloaded page learns what the app plays (hello, then state, then the book loads by
// itself), and a bookmark's Play sends its place with the start.
func TestNativeAudioReloadAndStart(t *testing.T) {
	b := chromium(t)
	e := playerApp(t)
	tab := a4Tab(t, b, 390)
	tab.Navigate(e.URL + "/?shell=android&next=/libraries")
	tab.WaitFor(`window.tsNativeAudio !== undefined`, 10*time.Second)
	a4WaitSent(t, tab, "hello 1")
	// The app is playing a book the page never loaded: nativebook loads it.
	a4Push(tab, a4State(e.book.ID, 20000, true, "1.50"))
	tab.WaitFor(`window.tsPlayer.state().bookId === `+cdp.Quote(e.book.ID), 8*time.Second)
	tab.WaitFor(`window.tsPlayer.state().playing === true`, 5*time.Second)
	// The speed pushed before the book was loaded is the page's speed once it is.
	tab.WaitFor(`window.tsPlayer.state().speed === 1.5`, 5*time.Second)
	var hidden bool
	tab.Eval(`document.getElementById("player").hidden`, &hidden)
	if hidden {
		t.Error("the bar is hidden for a book the app plays")
	}

	// A bookmark's Play, with a different book held by the app: start carries the place.
	a4Push(tab, a4State("", 0, false, "1.00"))
	tab.Eval(`(() => { const a = document.createElement("a"); a.id = "mark"; a.href = "#"; a.textContent = "Mark";
	  a.dataset.tsPlayAt = "45000"; a.dataset.tsBook = `+cdp.Quote(e.book.ID)+`;
	  a.style.cssText = "position:fixed;left:8px;top:8px;width:120px;height:60px;z-index:50;background:white;color:black";
	  document.getElementById("main").append(a); })()`, nil)
	playerClickSel(t, tab, "#mark")
	a4WaitSent(t, tab, "start "+e.book.ID+" 45000")
}

// TestNativeAudioIgnoredInBrowserAndLink: without the Android shell, and on a share-link page even inside the app,
// a tsNative object is never used and the page plays through its own audio element.
func TestNativeAudioIgnoredInBrowserAndLink(t *testing.T) {
	b := chromium(t)
	e := playerApp(t)
	plain := a4Tab(t, b, 390)
	plain.Navigate(e.URL + "/libraries")
	plain.WaitFor(`window.tsPlayer !== undefined`, 10*time.Second)
	var built bool
	plain.Eval(`window.tsNativeAudio !== undefined`, &built)
	if built || len(a4Sent(plain)) != 0 {
		t.Errorf("a browser page used tsNative: built %v, sent %v", built, a4Sent(plain))
	}

	le := linkNewEnv(t)
	_, token := le.make(le.book.ID, 7, "")
	public := httptest.NewServer(le.a.h)
	t.Cleanup(public.Close)
	link := a4Tab(t, b, 390)
	if err := link.Call("Network.setCookie", map[string]any{"name": shellCookie, "value": "android", "url": public.URL}, nil); err != nil {
		t.Fatal(err)
	}
	link.Navigate(public.URL + "/s/" + token)
	link.WaitFor(`window.tsPlayer !== undefined`, 10*time.Second)
	var shell string
	link.Eval(`document.documentElement.dataset.shell || ""`, &shell)
	link.Eval(`window.tsNativeAudio !== undefined`, &built)
	if built || len(a4Sent(link)) != 0 {
		t.Errorf("a share-link page used tsNative (shell %q): built %v, sent %v", shell, built, a4Sent(link))
	}
}
