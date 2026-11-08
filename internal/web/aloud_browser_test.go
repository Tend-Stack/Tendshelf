//go:build dev

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// Read aloud in Chromium (decision 0032, R1). Needs TENDSHELF_TEST_BROWSER. The browser's speech engine is replaced by a
// fake that exists only here: it logs what would be spoken and lets the test end or fail an utterance.

// alVoices: two local English voices (one the default), a local German voice and an online English voice.
const alVoices = `[
 {"voiceURI":"local-en","name":"Local English","lang":"en-US","localService":true,"default":true},
 {"voiceURI":"alpha-en","name":"Alpha English","lang":"en-GB","localService":true,"default":false},
 {"voiceURI":"local-de","name":"Lokal Deutsch","lang":"de-DE","localService":true,"default":false},
 {"voiceURI":"remote-en","name":"Remote English","lang":"en-US","localService":false,"default":false}
]`

// fakeSpeech returns the script that installs the fake speechSynthesis before the page's own scripts run. With auto, each
// utterance starts and ends by itself after about 40 ms; without, the test ends it through window.__tts.end().
func fakeSpeech(voicesJSON string, auto bool) string {
	return fmt.Sprintf(`(() => {
  const t = { log: [], cancels: 0, voices: %s, ms: 40 };
  class FakeUtterance extends EventTarget {
    constructor(text) { super(); this.text = text; this.rate = 1; this.voice = null; }
  }
  window.SpeechSynthesisUtterance = FakeUtterance;
  let now = null;
  const finish = (u, name, code) => {
    if (!u || now !== u) return;
    if (name !== "start") now = null;
    const event = new Event(name);
    if (code) event.error = code;
    u.dispatchEvent(event);
  };
  const fake = new (class extends EventTarget {
    getVoices() { return t.voices; }
    speak(u) {
      t.log.push({ text: u.text, rate: u.rate, voice: u.voice ? u.voice.voiceURI : null });
      now = u;
      if (%t) {
        setTimeout(() => finish(u, "start"), 5);
        setTimeout(() => finish(u, "end"), t.ms);
      }
    }
    cancel() {
      t.cancels++;
      const u = now;
      finish(u, "error", "interrupted");
    }
  })();
  t.end = () => finish(now, "end");
  t.fail = (code) => finish(now, "error", code);
  t.setVoices = (list) => { t.voices = list; fake.dispatchEvent(new Event("voiceschanged")); };
  Object.defineProperty(window, "speechSynthesis", { configurable: true, value: fake });
  window.__tts = t;
})();`, voicesJSON, auto)
}

// alBook is a title page and three chapters of ten paragraphs, then a last chapter of one paragraph.
func alBook() []ebooktest.EPUBChapter {
	chs := []ebooktest.EPUBChapter{{Title: "Title Page", EpubType: "titlepage", Paras: []string{"Aloud"}}}
	for i := 1; i <= 3; i++ {
		chs = append(chs, ebooktest.EPUBChapter{Title: fmt.Sprintf("Aloud chapter %d", i), EpubType: "chapter", Paras: pgProse(fmt.Sprintf("c%d", i+1), 10)})
	}
	return append(chs, ebooktest.EPUBChapter{Title: "Aloud last", EpubType: "chapter", Paras: pgProse("c5", 1)})
}

type alVP = struct {
	name   string
	w, h   int
	mobile bool
}

// alOpen opens the reader with the fake speech installed. The flow is pages at 390 px and scroll at 1440 px.
func alOpen(t *testing.T, b *cdp.Browser, e *ofrEnv, vp alVP, script, query string) *cdp.Tab {
	t.Helper()
	pgFlow(t, e.l, map[bool]string{true: "pages", false: "scroll"}[vp.mobile])
	tab := b.NewTab(vp.w, vp.h, vp.mobile, true)
	_ = tab.Call("Page.enable", nil, nil)
	if err := tab.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": script}, nil); err != nil {
		t.Fatal(err)
	}
	tab.Navigate(e.read(query))
	tab.WaitFor(ofrMounted, 10*time.Second)
	return tab
}

// alPress presses the read-aloud control that is shown at this width.
func alPress(tab *cdp.Tab) {
	tab.Eval(`[...document.querySelectorAll("[data-aloud-src]")].find((b) => b.offsetParent !== null).click()`, nil)
	tab.WaitFor(`!document.getElementById("reader-aloud").hidden`, 5*time.Second)
}

type alEntry struct {
	Text  string
	Rate  float64
	Voice string
}

func alLog(tab *cdp.Tab) (list []alEntry) {
	tab.Eval(`window.__tts.log`, &list)
	return list
}

func alWaitLog(t *testing.T, tab *cdp.Tab, n int) {
	t.Helper()
	tab.WaitFor(fmt.Sprintf(`window.__tts.log.length >= %d`, n), 8*time.Second)
}

func alCancels(tab *cdp.Tab) (n int) {
	tab.Eval(`window.__tts.cancels`, &n)
	return n
}

// alSentences is the text of every sentence in the page, as the reader speaks it.
func alSentences(tab *cdp.Tab) (list []string) {
	tab.Eval(`[...document.querySelectorAll("#reader .ts-s")].map((s) => s.textContent.replace(/\s+/g, " ").trim())`, &list)
	return list
}

func alIndex(list []string, text string) int {
	for i, s := range list {
		if s == text {
			return i
		}
	}
	return -1
}

func alLabel(tab *cdp.Tab) (s string) {
	tab.Eval(`document.querySelector('[data-aloud="play"]').getAttribute("aria-label")`, &s)
	return s
}

func alStatus(tab *cdp.Tab) (s string) {
	tab.Eval(`document.querySelector(".ts-aloud__status").textContent`, &s)
	return s
}

func alKey(t *testing.T, tab *cdp.Tab, key string) {
	t.Helper()
	pgKey(t, tab, key, "Key"+strings.ToUpper(key), int(strings.ToUpper(key)[0]), key, 0)
}

func TestAloudBrowserVoices(t *testing.T) {
	b := chromium(t)
	for _, vp := range pgViewports {
		t.Run(vp.name, func(t *testing.T) {
			e := ofrApp(t, alBook(), nil)
			tab := alOpen(t, b, e, vp, fakeSpeech(`[]`, false), "ch=2")
			hidden := `[...document.querySelectorAll("[data-aloud-src]")].every((b) => b.hidden)`
			tab.WaitFor(hidden, 3*time.Second)

			// An online voice alone never shows the control.
			tab.Eval(`window.__tts.setVoices([{voiceURI: "remote-en", name: "Remote English", lang: "en-US", localService: false, default: true}])`, nil)
			time.Sleep(300 * time.Millisecond)
			tab.WaitFor(hidden, 3*time.Second)

			// A local voice shows it, and the online voice is never offered.
			tab.Eval(`window.__tts.setVoices(`+alVoices+`)`, nil)
			tab.WaitFor(`[...document.querySelectorAll("[data-aloud-src]")].some((b) => !b.hidden)`, 3*time.Second)
			alPress(tab)
			var options []string
			tab.Eval(`[...document.querySelectorAll("#aloud-voice option")].map((o) => o.value)`, &options)
			if strings.Join(options, ",") != ",alpha-en,local-en,local-de" {
				t.Errorf("voice options %v: want the default, the book's language by name, then the others", options)
			}
			alWaitLog(t, tab, 1)
			if l := alLog(tab); l[0].Voice == "" || l[0].Voice == "remote-en" {
				t.Errorf("first utterance voice %q, want a local voice", l[0].Voice)
			}
			var group string
			tab.Eval(`document.querySelector("#aloud-voice optgroup").label`, &group)
			if group != "Other languages" {
				t.Errorf("group %q", group)
			}
		})
	}
}

func TestAloudBrowserStart(t *testing.T) {
	b := chromium(t)
	for _, vp := range pgViewports {
		t.Run(vp.name, func(t *testing.T) {
			e := ofrApp(t, alBook(), nil)
			tab := alOpen(t, b, e, vp, fakeSpeech(alVoices, false), "ch=2")
			tab.WaitFor(`[...document.querySelectorAll("[data-aloud-src]")].some((b) => !b.hidden)`, 3*time.Second)
			// Keys do nothing while the strip is closed.
			alKey(t, tab, "k")
			time.Sleep(200 * time.Millisecond)
			if n := len(alLog(tab)); n != 0 {
				t.Fatalf("k spoke %d sentences with the strip closed", n)
			}
			alPress(tab)
			alWaitLog(t, tab, 1)
			l := alLog(tab)
			var at string
			tab.Eval(`(() => { const s = document.querySelector('#reader .ts-s[data-sid="' + CSS.escape(document.getElementById("reader").dataset.at) + '"]') || document.querySelector("#reader .ts-s"); return s ? s.textContent.replace(/\s+/g, " ").trim() : ""; })()`, &at)
			if l[0].Text != at || at == "" {
				t.Errorf("first utterance %q, want the sentence at the place %q", l[0].Text, at)
			}
			var marked []string
			tab.Eval(`[...document.querySelectorAll("#reader .ts-s.is-now")].map((s) => s.textContent.replace(/\s+/g, " ").trim())`, &marked)
			if len(marked) != 1 || marked[0] != l[0].Text {
				t.Errorf("marked %v, want the spoken sentence", marked)
			}
			if l[0].Voice != "local-en" {
				t.Errorf("voice %q, want the default voice of the book's language", l[0].Voice)
			}
			// The mark is more than colour, and under reduced motion it does not fade.
			var style struct{ Underline, Transition string }
			tab.Eval(`(() => { const s = getComputedStyle(document.querySelector("#reader .ts-s.is-now")); return {Underline: s.textDecorationLine, Transition: s.transitionDuration}; })()`, &style)
			if style.Underline != "underline" || style.Transition != "0s" {
				t.Errorf("mark style %+v, want an underline and no fade", style)
			}
			var focus string
			tab.Eval(`document.activeElement.dataset.aloud || ""`, &focus)
			if focus != "play" {
				t.Errorf("focus on %q, want the Pause button", focus)
			}
		})
	}
}

func TestAloudBrowserContinues(t *testing.T) {
	b := chromium(t)
	for _, vp := range pgViewports {
		t.Run(vp.name, func(t *testing.T) {
			e := ofrApp(t, alBook(), nil)
			tab := alOpen(t, b, e, vp, fakeSpeech(alVoices, true), "ch=2")
			tab.WaitFor(`[...document.querySelectorAll("[data-aloud-src]")].some((b) => !b.hidden)`, 3*time.Second)
			alPress(tab)
			// Past the end of chapter 2 and well into chapter 3.
			tab.WaitFor(`window.__tts.log.some((e) => e.text.includes("Sentence c3.9."))`, 20*time.Second)
			tab.Eval(`document.querySelector('[data-aloud="play"]').click()`, nil)
			var section string
			tab.Eval(`[...document.querySelectorAll("#reader section[data-part]")].map((s) => s.dataset.ch).join()`, &section)
			if !strings.Contains(","+section+",", ",3,") {
				t.Errorf("the parts in the page are chapters %s, want chapter 3 among them", section)
			}
			if vp.mobile {
				var count int
				tab.Eval(`document.querySelectorAll("#reader section[data-part]").length`, &count)
				if count != 1 {
					t.Errorf("pages hold %d parts, want 1", count)
				}
				var turned bool
				tab.Eval(`document.querySelector("#reader section[data-part]").scrollLeft > 0`, &turned)
				if !turned {
					t.Error("the page did not turn while reading chapter 3")
				}
			}
			time.Sleep(3500 * time.Millisecond)
			var sid string
			tab.Eval(`fetch(document.getElementById("reader").dataset.save).then((r) => r.json()).then((j) => j.reading.sid)`, &sid)
			if !strings.HasPrefix(sid, "c3.") {
				t.Errorf("saved place %q, want a sentence of chapter 3", sid)
			}
		})
	}
}

func TestAloudBrowserControls(t *testing.T) {
	b := chromium(t)
	for _, vp := range pgViewports {
		t.Run(vp.name, func(t *testing.T) {
			e := ofrApp(t, alBook(), nil)
			tab := alOpen(t, b, e, vp, fakeSpeech(alVoices, false), "ch=2")
			tab.WaitFor(`[...document.querySelectorAll("[data-aloud-src]")].some((b) => !b.hidden)`, 3*time.Second)
			alPress(tab)
			alWaitLog(t, tab, 1)
			all := alSentences(tab)
			i0 := alIndex(all, alLog(tab)[0].Text)
			if i0 < 0 || i0+3 >= len(all) {
				t.Fatalf("first sentence index %d of %d", i0, len(all))
			}
			expect := func(n, want int) {
				t.Helper()
				alWaitLog(t, tab, n)
				if got := alLog(tab)[n-1].Text; got != all[want] {
					t.Errorf("utterance %d is %q, want sentence %d %q", n, got, want, all[want])
				}
			}
			click := func(name string) {
				tab.Eval(`document.querySelector('[data-aloud="`+name+`"]').click()`, nil)
			}
			click("fwd")
			expect(2, i0+1)
			click("fwd")
			expect(3, i0+2)
			click("back")
			expect(4, i0+1)

			// Pause cancels and says Play; Play speaks the same sentence again.
			before := alCancels(tab)
			click("play")
			if alCancels(tab) <= before || alLabel(tab) != "Play" {
				t.Errorf("after Pause: cancels %d (was %d), label %q", alCancels(tab), before, alLabel(tab))
			}
			click("play")
			expect(5, i0+1)
			if alLabel(tab) != "Pause" {
				t.Errorf("label %q after Play", alLabel(tab))
			}

			// j goes back, k forward, with the strip open.
			alKey(t, tab, "k")
			expect(6, i0+2)
			alKey(t, tab, "j")
			expect(7, i0+1)

			// Stop clears the mark and speaks no more; the keys do nothing once it is closed.
			click("stop")
			var marked int
			tab.Eval(`document.querySelectorAll("#reader .ts-s.is-now").length`, &marked)
			var hidden bool
			tab.Eval(`document.getElementById("reader-aloud").hidden`, &hidden)
			if marked != 0 || !hidden {
				t.Errorf("after Stop: %d marked, strip hidden %v", marked, hidden)
			}
			alKey(t, tab, "k")
			time.Sleep(250 * time.Millisecond)
			if n := len(alLog(tab)); n != 7 {
				t.Errorf("%d utterances after the strip closed, want 7", n)
			}
		})
	}
}

func TestAloudBrowserPlayer(t *testing.T) {
	b := chromium(t)
	e := ofrApp(t, alBook(), nil)
	vp := pgViewports[1]
	tab := alOpen(t, b, e, vp, fakeSpeech(alVoices, false), "ch=2")
	tab.WaitFor(`[...document.querySelectorAll("[data-aloud-src]")].some((b) => !b.hidden)`, 3*time.Second)
	tab.Eval(`(() => { window.__paused = 0; window.__playing = true; window.tsPlayer = { state: () => ({ playing: window.__playing }), pause: () => { window.__paused++; window.__playing = false; } }; })()`, nil)
	alPress(tab)
	alWaitLog(t, tab, 1)
	var paused int
	tab.Eval(`window.__paused`, &paused)
	if paused != 1 {
		t.Errorf("the audiobook was paused %d times, want 1", paused)
	}
	// The audiobook starting stops the voice.
	before := alCancels(tab)
	tab.Eval(`document.dispatchEvent(new CustomEvent("ts:player:state", { detail: { playing: true } }))`, nil)
	if alCancels(tab) <= before || alLabel(tab) != "Play" {
		t.Errorf("audiobook start: cancels %d (was %d), label %q", alCancels(tab), before, alLabel(tab))
	}
}

func TestAloudBrowserSettings(t *testing.T) {
	b := chromium(t)
	e := ofrApp(t, alBook(), nil)
	vp := pgViewports[1]
	tab := alOpen(t, b, e, vp, fakeSpeech(alVoices, false), "ch=2")
	tab.WaitFor(`[...document.querySelectorAll("[data-aloud-src]")].some((b) => !b.hidden)`, 3*time.Second)
	alPress(tab)
	alWaitLog(t, tab, 1)
	tab.Eval(`(() => { const r = document.getElementById("aloud-rate"); r.value = "1.5"; r.dispatchEvent(new Event("change", { bubbles: true })); })()`, nil)
	tab.Eval(`(() => { const v = document.getElementById("aloud-voice"); v.value = "alpha-en"; v.dispatchEvent(new Event("change", { bubbles: true })); })()`, nil)
	alWaitLog(t, tab, 3)
	l := alLog(tab)
	if last := l[len(l)-1]; last.Rate != 1.5 || last.Voice != "alpha-en" {
		t.Errorf("last utterance %+v, want rate 1.5 and the Alpha voice", last)
	}
	var stored struct{ Voice, Rate string }
	tab.Eval(`({Voice: localStorage.getItem("ts.aloud.voice"), Rate: localStorage.getItem("ts.aloud.rate")})`, &stored)
	if stored.Voice != "alpha-en" || stored.Rate != "1.5" {
		t.Errorf("stored %+v", stored)
	}
	// After a reload the selects show the choice, and nothing was added to the reader's own settings.
	tab.Navigate(e.read("ch=2"))
	tab.WaitFor(ofrMounted+` && [...document.querySelectorAll("[data-aloud-src]")].some((b) => !b.hidden)`, 10*time.Second)
	alPress(tab)
	var shown struct{ Voice, Rate string }
	tab.Eval(`({Voice: document.getElementById("aloud-voice").value, Rate: document.getElementById("aloud-rate").value})`, &shown)
	if shown.Voice != "alpha-en" || shown.Rate != "1.5" {
		t.Errorf("after reload the selects show %+v", shown)
	}
	alWaitLog(t, tab, 1)
	if first := alLog(tab)[0]; first.Rate != 1.5 || first.Voice != "alpha-en" {
		t.Errorf("after reload the first utterance %+v", first)
	}
}

func TestAloudBrowserLeaving(t *testing.T) {
	b := chromium(t)
	e := ofrApp(t, alBook(), nil)
	vp := pgViewports[1]
	tab := alOpen(t, b, e, vp, fakeSpeech(alVoices, true), "ch=2")
	tab.WaitFor(`[...document.querySelectorAll("[data-aloud-src]")].some((b) => !b.hidden)`, 3*time.Second)
	alPress(tab)
	alWaitLog(t, tab, 3)
	before := alCancels(tab)
	tab.Eval(`document.querySelector(".ts-readerbar a.ts-iconbtn").click()`, nil)
	tab.WaitFor(`document.getElementById("reader") === null`, 8*time.Second)
	if alCancels(tab) <= before {
		t.Error("leaving the reader cancelled nothing")
	}
	n := len(alLog(tab))
	time.Sleep(500 * time.Millisecond)
	if after := len(alLog(tab)); after != n {
		t.Errorf("the voice went on after leaving: %d to %d utterances", n, after)
	}
}

func TestAloudBrowserMessages(t *testing.T) {
	b := chromium(t)
	e := ofrApp(t, alBook(), nil)
	vp := pgViewports[1]
	tab := alOpen(t, b, e, vp, fakeSpeech(alVoices, false), "ch=2")
	tab.WaitFor(`[...document.querySelectorAll("[data-aloud-src]")].some((b) => !b.hidden)`, 3*time.Second)
	alPress(tab)
	alWaitLog(t, tab, 1)
	tab.Eval(`window.__tts.fail("not-allowed")`, nil)
	tab.WaitFor(`document.querySelector(".ts-aloud__status").textContent !== ""`, 3*time.Second)
	if s := alStatus(tab); s != "Press Play to start reading aloud." {
		t.Errorf("blocked: %q", s)
	}
	if alLabel(tab) != "Play" {
		t.Errorf("label %q after a blocked start", alLabel(tab))
	}
	tab.Eval(`document.querySelector('[data-aloud="play"]').click()`, nil)
	alWaitLog(t, tab, 2)
	tab.Eval(`window.__tts.fail("synthesis-failed")`, nil)
	tab.WaitFor(`document.querySelector(".ts-aloud__status").textContent !== ""`, 3*time.Second)
	if s := alStatus(tab); s != "The voice on this device stopped. Press Play to try again." {
		t.Errorf("failed: %q", s)
	}

}

// The end of the book: the last chapter is one paragraph.
func TestAloudBrowserEnd(t *testing.T) {
	b := chromium(t)
	for _, vp := range pgViewports {
		t.Run(vp.name, func(t *testing.T) {
			e := ofrApp(t, alBook(), nil)
			tab := alOpen(t, b, e, vp, fakeSpeech(alVoices, true), "ch=5")
			tab.WaitFor(`[...document.querySelectorAll("[data-aloud-src]")].some((b) => !b.hidden)`, 3*time.Second)
			alPress(tab)
			tab.WaitFor(`document.querySelector(".ts-aloud__status").textContent !== ""`, 10*time.Second)
			if s := alStatus(tab); s != "You have reached the end of the book." {
				t.Errorf("status %q", s)
			}
			if alLabel(tab) != "Play" {
				t.Errorf("label %q at the end", alLabel(tab))
			}
		})
	}
}

func TestAloudBrowserLayout(t *testing.T) {
	b := chromium(t)
	for _, vp := range pgViewports {
		t.Run(vp.name, func(t *testing.T) {
			e := ofrApp(t, alBook(), nil)
			tab := alOpen(t, b, e, vp, fakeSpeech(alVoices, false), "ch=2")
			tab.WaitFor(`[...document.querySelectorAll("[data-aloud-src]")].some((b) => !b.hidden)`, 3*time.Second)
			var shown string
			tab.Eval(`[...document.querySelectorAll("[data-aloud-src]")].filter((b) => b.offsetParent !== null).map((b) => b.className.match(/ts-aloud-open--(\w+)/)[1]).join()`, &shown)
			if want := map[bool]string{true: "dock", false: "bar"}[vp.mobile]; shown != want {
				t.Errorf("shown opener %q, want %q", shown, want)
			}
			alPress(tab)
			tab.Eval(`document.querySelector(".ts-aloud__more").open = true`, nil)
			var m struct {
				Small                  []string
				BarOver, RowOver, Page bool
			}
			tab.Eval(`(() => {
				const small = [...document.querySelectorAll("#reader-aloud button, #reader-aloud summary, #reader-aloud select, .ts-aloud-open")]
					.filter((e) => e.offsetParent !== null).filter((e) => { const r = e.getBoundingClientRect(); return r.width < 43.5 || r.height < 43.5; })
					.map((e) => e.dataset.aloud + " " + e.getBoundingClientRect().width + "x" + e.getBoundingClientRect().height);
				const bar = document.querySelector(".ts-readerbar");
				const row = document.querySelector(".ts-readdock__row");
				return {Small: small, BarOver: bar.scrollWidth > bar.clientWidth, RowOver: row.scrollWidth > row.clientWidth,
					Page: document.documentElement.scrollWidth > innerWidth};
			})()`, &m)
			if len(m.Small) > 0 || m.BarOver || m.RowOver || m.Page {
				b, _ := json.Marshal(m)
				t.Errorf("layout %s", b)
			}
		})
	}
}

func TestAloudBrowserOffline(t *testing.T) {
	b := chromium(t)
	e := ofrApp(t, alBook(), nil)
	pgFlow(t, e.l, "pages")
	tab := b.NewTab(390, 844, true, true)
	_ = tab.Call("Page.enable", nil, nil)
	if err := tab.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": fakeSpeech(alVoices, false)}, nil); err != nil {
		t.Fatal(err)
	}
	ofrSeedCache(t, e, tab)
	ofrOffline(t, e, tab, true)
	tab.Navigate(e.read("ch=2"))
	tab.WaitFor(`document.querySelector("[data-offline-shell]") && `+ofrMounted+` && [...document.querySelectorAll("[data-aloud-src]")].some((b) => !b.hidden)`, 15*time.Second)
	alPress(tab)
	alWaitLog(t, tab, 1)
	var marked int
	tab.Eval(`document.querySelectorAll("#reader .ts-s.is-now").length`, &marked)
	if marked != 1 {
		t.Errorf("offline: %d sentences marked", marked)
	}
}

// TestAloudVisual captures the reader with the read-aloud strip open and a sentence marked, at 390, 768, 1024 and 1440 px,
// in dark, light and sepia. There is no board for it: compare with BRAND "Reader" and the dock. The voice names are fixed.
func TestAloudVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	l := pgSeedEPUB(t, alBook(), nil)
	base := themedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(l.owner)
		l.a.h.ServeHTTP(w, r)
	}))
	sizes := []struct{ w, h int }{{390, 844}, {768, 1024}, {1024, 768}, {1440, 900}}
	for _, sz := range sizes {
		for _, theme := range []string{"dark", "light", "sepia"} {
			t.Run(fmt.Sprintf("%d-%s", sz.w, theme), func(t *testing.T) {
				tab := b.NewTab(sz.w, sz.h, sz.w < 768, true)
				_ = tab.Call("Page.enable", nil, nil)
				if err := tab.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": fakeSpeech(alVoices, false)}, nil); err != nil {
					t.Fatal(err)
				}
				tab.Navigate(base + l.readPath("ch=2&theme="+theme))
				tab.WaitFor(ofrMounted+` && [...document.querySelectorAll("[data-aloud-src]")].some((b) => !b.hidden)`, 10*time.Second)
				alPress(tab)
				alWaitLog(t, tab, 1)
				tab.Eval(`document.querySelector('[data-aloud="fwd"]').click()`, nil)
				alWaitLog(t, tab, 2)
				time.Sleep(300 * time.Millisecond)
				tab.Screenshot(filepath.Join(out, fmt.Sprintf("aloud-%s-%d.png", theme, sz.w)))
			})
		}
	}
}
