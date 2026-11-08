//go:build dev

package web

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// Read aloud in the Android app's window (decision 0032, R2). Needs TENDSHELF_TEST_BROWSER. A stub tsNative takes the
// page's words and lets the test push the app's answers, as native_audio_browser_test.go does for the player.

const nsStub = `(() => {
  window.__sent = [];
  const listeners = [];
  window.tsNative = {
    postMessage: (m) => window.__sent.push(m),
    onmessage: null,
    addEventListener: (type, fn) => { if (type === "message") listeners.push(fn); },
  };
  window.__push = (json) => { listeners.forEach((fn) => fn({ data: json })); };
})();`

// nsOpen opens the reader as the app's window does: the shell link sets the cookie, then the book.
func nsOpen(t *testing.T, b *cdp.Browser, e *ofrEnv, vp alVP) *cdp.Tab {
	t.Helper()
	pgFlow(t, e.l, map[bool]string{true: "pages", false: "scroll"}[vp.mobile])
	tab := b.NewTab(vp.w, vp.h, vp.mobile, true)
	_ = tab.Call("Page.enable", nil, nil)
	if err := tab.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": nsStub}, nil); err != nil {
		t.Fatal(err)
	}
	next := "/books/" + e.l.book.ID + "/read?ch=2"
	tab.Navigate(e.URL + "/?shell=android&next=" + url.QueryEscape(next))
	tab.WaitFor(ofrMounted, 10*time.Second)
	return tab
}

func nsSent(tab *cdp.Tab) (sent []string) {
	tab.Eval(`window.__sent`, &sent)
	return sent
}

// nsSays is the "say" messages sent so far, split into n, rate, voice id and text.
func nsSays(tab *cdp.Tab) (says [][4]string) {
	for _, m := range nsSent(tab) {
		if parts := strings.SplitN(m, " ", 5); len(parts) == 5 && parts[0] == "say" {
			says = append(says, [4]string{parts[1], parts[2], parts[3], parts[4]})
		}
	}
	return says
}

func nsPush(tab *cdp.Tab, json string) {
	tab.Eval(fmt.Sprintf(`window.__push(%s)`, cdp.Quote(json)), nil)
}

func nsWaitSays(t *testing.T, tab *cdp.Tab, n int) {
	t.Helper()
	tab.WaitFor(fmt.Sprintf(`window.__sent.filter((m) => m.startsWith("say ")).length >= %d`, n), 8*time.Second)
}

const nsShown = `[...document.querySelectorAll("[data-aloud-src]")].some((b) => !b.hidden)`

func TestNativeSpeechBrowser(t *testing.T) {
	b := chromium(t)
	for _, vp := range pgViewports {
		t.Run(vp.name, func(t *testing.T) {
			e := ofrApp(t, alBook(), nil)
			tab := nsOpen(t, b, e, vp)

			// The page asks once, and the control stays hidden until the app lists a voice.
			tab.WaitFor(`window.__sent.includes("tts voices")`, 8*time.Second)
			time.Sleep(200 * time.Millisecond)
			var shown bool
			tab.Eval(nsShown, &shown)
			if shown {
				t.Fatal("the control is shown before the app listed a voice")
			}

			nsPush(tab, `{"t":"voices","list":[{"id":"v-de","lang":"de-DE","def":false},{"id":"v-en-a","lang":"en-US","def":true},{"id":"v-en-b","lang":"en-GB","def":false}]}`)
			tab.WaitFor(nsShown, 3*time.Second)
			alPress(tab)

			nsWaitSays(t, tab, 1)
			sentences := alSentences(tab)
			first := nsSays(tab)[0]
			if first[0] != "1" || first[1] != "1.00" || first[2] != "v-en-a" {
				t.Fatalf("first say %v: want n 1, rate 1.00 and the default voice of the book's language", first)
			}
			at := alIndex(sentences, first[3])
			if at < 0 {
				t.Fatalf("first say text %q is no sentence of the page", first[3])
			}

			nsPush(tab, `{"t":"tts","n":1,"ev":"start"}`)
			// An event of another utterance is ignored.
			nsPush(tab, `{"t":"tts","n":9,"ev":"end"}`)
			time.Sleep(150 * time.Millisecond)
			if got := len(nsSays(tab)); got != 1 {
				t.Fatalf("an event for another number moved on: %d says", got)
			}
			nsPush(tab, `{"t":"tts","n":1,"ev":"end"}`)
			nsWaitSays(t, tab, 2)
			second := nsSays(tab)[1]
			if second[0] != "2" || second[3] != sentences[at+1] {
				t.Errorf("second say %v: want n 2 and the next sentence %q", second, sentences[at+1])
			}
			var now string
			tab.WaitFor(`document.querySelector("#reader .ts-s.is-now") !== null`, 3*time.Second)
			tab.Eval(`document.querySelector("#reader .ts-s.is-now").textContent.replace(/\s+/g, " ").trim()`, &now)
			if now != sentences[at+1] {
				t.Errorf("marked sentence %q, want %q", now, sentences[at+1])
			}

			// The app stopped by itself (the background): Paused, Play offered.
			nsPush(tab, `{"t":"tts","n":2,"ev":"stopped"}`)
			tab.WaitFor(`document.querySelector('[data-aloud="play"]').getAttribute("aria-label") === "Play"`, 3*time.Second)

			// Every id sent was listed.
			listed := map[string]bool{"v-de": true, "v-en-a": true, "v-en-b": true}
			for _, s := range nsSays(tab) {
				if !listed[s[2]] {
					t.Errorf("say named an unlisted voice %q", s[2])
				}
			}
		})
	}
}

func TestNativeSpeechBrowserNoVoice(t *testing.T) {
	b := chromium(t)
	e := ofrApp(t, alBook(), nil)
	tab := nsOpen(t, b, e, pgViewports[len(pgViewports)-1])
	tab.WaitFor(`window.__sent.includes("tts voices")`, 8*time.Second)
	nsPush(tab, `{"t":"voices","list":[]}`)
	time.Sleep(300 * time.Millisecond)
	var shown bool
	tab.Eval(nsShown, &shown)
	if shown {
		t.Error("an empty voice list shows the control")
	}
	if n := len(nsSays(tab)); n != 0 {
		t.Errorf("%d says without a voice", n)
	}
}
