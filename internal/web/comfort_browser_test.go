//go:build dev

package web

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The comfort settings in Chromium (G5). Needs TENDSHELF_TEST_BROWSER.

func cfBook() []ebooktest.EPUBChapter {
	return []ebooktest.EPUBChapter{{Title: "Comfort chapter", EpubType: "chapter", Paras: pgProse("cf", 12)}}
}

// cfSet saves comfort settings through the real route, as the sheet would.
func cfSet(t *testing.T, e *ofrEnv, kv ...string) {
	t.Helper()
	if code, _, _ := rsetPost(t, e.l, e.l.owner, true, kv...); code != 204 {
		t.Fatalf("settings %v: %d", kv, code)
	}
}

// cfOpen opens the reader at a viewport and waits for the text.
func cfOpen(t *testing.T, b *cdp.Browser, e *ofrEnv, w, h int, mobile bool) *cdp.Tab {
	t.Helper()
	return cfOpenWith(t, b, e, w, h, mobile, "")
}

// cfOpenWith runs a script before the page's own (a fake wake lock, say) and then opens the reader.
func cfOpenWith(t *testing.T, b *cdp.Browser, e *ofrEnv, w, h int, mobile bool, before string) *cdp.Tab {
	t.Helper()
	tab := b.NewTab(w, h, mobile, true)
	if before != "" {
		_ = tab.Call("Page.enable", map[string]any{}, nil)
		if err := tab.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": before}, nil); err != nil {
			t.Fatal(err)
		}
	}
	tab.Navigate(e.read("ch=1"))
	tab.WaitFor(ofrMounted, 10*time.Second)
	return tab
}

// cfModule is the address of reader-comfort.js, read from the settings marker.
const cfModule = `document.querySelector("[data-comfort-src]").dataset.comfortSrc`

func TestComfortBrowserWarm(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	for _, vp := range pgViewports {
		t.Run(vp.name, func(t *testing.T) {
			e := ofrApp(t, cfBook(), nil)
			tab := cfOpen(t, b, e, vp.w, vp.h, vp.mobile)

			// warmAt is a pure function: always is on, evening is 20:00 to 05:59, off never.
			var got []bool
			tab.Eval(`import(`+cfModule+`).then((m) => [["off", 12], ["off", 22], ["always", 12], ["always", 3], ["evening", 19], ["evening", 20],
				["evening", 23], ["evening", 0], ["evening", 5], ["evening", 6], ["evening", 12], ["junk", 22]].map(([s, h]) => m.warmAt(s, h)))`, &got)
			want := []bool{false, false, true, true, false, true, true, true, true, false, false, false}
			for i := range want {
				if got[i] != want[i] {
					t.Errorf("warmAt case %d = %v, want %v", i, got[i], want[i])
				}
			}

			// Always: the page carries the tint (in the three themes), the contrast theme does not.
			cfSet(t, e, "warm", "always")
			tab.Navigate(e.read("ch=1"))
			tab.WaitFor(ofrMounted+` && document.querySelector(".ts-readerpage").dataset.warm === "on"`, 10*time.Second)
			for _, theme := range []string{"dark", "light", "sepia", "contrast"} {
				var content string
				tab.Eval(`(() => { document.documentElement.dataset.theme = `+cdp.Quote(theme)+`; return getComputedStyle(document.querySelector(".ts-readerpage"), "::after").content; })()`, &content)
				if theme == "contrast" && content != "none" {
					t.Errorf("contrast theme has a tint: content %q", content)
				}
				if theme != "contrast" && content != `""` {
					t.Errorf("%s has no tint: content %q", theme, content)
				}
				if theme != "contrast" {
					tab.Screenshot(filepath.Join(out, fmt.Sprintf("comfort-warm-%s-%d.png", theme, vp.w)))
				}
			}

			// Evening follows the device's clock.
			cfSet(t, e, "warm", "evening")
			tab.Navigate(e.read("ch=1"))
			tab.WaitFor(ofrMounted, 10*time.Second)
			var evening, hourSaysSo bool
			tab.Eval(`(() => { const h = new Date().getHours(); return h >= 20 || h < 6; })()`, &hourSaysSo)
			tab.Eval(`document.querySelector(".ts-readerpage").dataset.warm === "on"`, &evening)
			if evening != hourSaysSo {
				t.Errorf("evening tint is %v but the hour says %v", evening, hourSaysSo)
			}
		})
	}
}

func TestComfortBrowserHide(t *testing.T) {
	b := chromium(t)
	for _, vp := range pgViewports {
		t.Run(vp.name, func(t *testing.T) {
			e := ofrApp(t, cfBook(), nil)
			cfSet(t, e, "auto_hide", "1")
			tab := cfOpen(t, b, e, vp.w, vp.h, vp.mobile)
			chrome := `document.querySelector(".ts-readerpage").dataset.chrome`

			// Focus in the bar keeps it, even after the delay.
			tab.Eval(`document.querySelector(".ts-readerbar a").focus()`, nil)
			time.Sleep(4500 * time.Millisecond)
			var state string
			tab.Eval(`String(`+chrome+`)`, &state)
			if state == "off" {
				t.Error("the bar hid while focus was in it")
			}

			// An open sheet keeps it.
			tab.Eval(`document.querySelector("a.ts-aa").click()`, nil)
			tab.WaitFor(`document.querySelector("#sheet [role=dialog]") !== null`, 5*time.Second)
			tab.Eval(`document.activeElement.blur()`, nil)
			time.Sleep(4500 * time.Millisecond)
			tab.Eval(`String(`+chrome+`)`, &state)
			if state == "off" {
				t.Error("the bar hid under an open sheet")
			}
			tab.Eval(`document.querySelector("#sheet [data-ts-close]").click()`, nil)
			tab.WaitFor(`document.querySelector("#sheet [role=dialog]") === null`, 5*time.Second)

			// With nothing in the way, it hides about 3 s after the last input. Closing the sheet gives focus back to
			// the Aa link, which is in the bar and so keeps it; the person has to move on first.
			time.Sleep(500 * time.Millisecond)
			tab.Eval(`document.activeElement.blur()`, nil)
			tab.WaitFor(chrome+` === "off"`, 8*time.Second)

			// Focus brings it back (the rule in pages-reader.css).
			var opacity string
			tab.Eval(`getComputedStyle(document.querySelector(".ts-readerbar")).opacity`, &opacity)
			if opacity != "0" {
				t.Errorf("the hidden bar has opacity %s", opacity)
			}
			tab.Eval(`document.querySelector(".ts-readerbar a").focus()`, nil)
			tab.WaitFor(`getComputedStyle(document.querySelector(".ts-readerbar")).opacity === "1"`, 3*time.Second)
		})
	}
}

// cfFakeLock stands in for the Screen Wake Lock API and counts what the page asks of it.
const cfFakeLock = `window.__locks = { taken: 0, released: 0 };
Object.defineProperty(navigator, "wakeLock", { configurable: true, value: { request: async () => {
  window.__locks.taken++;
  const lock = new EventTarget();
  lock.release = async () => { window.__locks.released++; lock.dispatchEvent(new Event("release")); };
  return lock;
} } });`

func TestComfortBrowserWake(t *testing.T) {
	b := chromium(t)
	for _, vp := range pgViewports {
		t.Run(vp.name, func(t *testing.T) {
			e := ofrApp(t, cfBook(), nil)
			cfSet(t, e, "keep_awake", "1")

			// The lock is taken once, and let go when the person leaves the page through a boosted link.
			tab := cfOpenWith(t, b, e, vp.w, vp.h, vp.mobile, cfFakeLock)
			tab.WaitFor(`window.__locks.taken === 1`, 5*time.Second)
			tab.Eval(`document.querySelector(".ts-readerbar a").click()`, nil)
			tab.WaitFor(`document.getElementById("reader") === null`, 10*time.Second)
			tab.WaitFor(`window.__locks.released === 1`, 5*time.Second)

			// Turning it off in the sheet lets go too: set it on, open, and switch off.
			tab2 := cfOpenWith(t, b, e, vp.w, vp.h, vp.mobile, cfFakeLock)
			tab2.WaitFor(`window.__locks.taken === 1`, 5*time.Second)
			tab2.Eval(`document.querySelector("a.ts-aa").click()`, nil)
			tab2.WaitFor(`document.getElementById("rs-keep_awake") !== null`, 5*time.Second)
			tab2.Eval(`(() => { document.querySelector(".ts-readset__more").open = true; document.getElementById("rs-keep_awake").click(); })()`, nil)
			tab2.WaitFor(`window.__locks.released === 1`, 5*time.Second)

			// No API at all: nothing breaks, and the other settings still work.
			cfSet(t, e, "warm", "always")
			tab3 := cfOpenWith(t, b, e, vp.w, vp.h, vp.mobile, `Object.defineProperty(navigator, "wakeLock", { configurable: true, value: undefined });`)
			tab3.WaitFor(`document.querySelector(".ts-readerpage").dataset.warm === "on"`, 5*time.Second)
		})
	}
}

func TestComfortNotLoaded(t *testing.T) {
	b := chromium(t)
	e := ofrApp(t, cfBook(), nil)
	tab := cfOpen(t, b, e, 390, 844, true)
	loaded := `performance.getEntriesByType("resource").some((x) => x.name.includes("reader-comfort"))`
	var yes bool
	tab.Eval(loaded, &yes)
	if yes {
		t.Fatal("reader-comfort.js was requested with every comfort setting off")
	}

	// Turning one on in the sheet fetches it, and it acts at once.
	tab.Eval(`document.querySelector("a.ts-aa").click()`, nil)
	tab.WaitFor(`document.getElementById("rs-warm-always") !== null`, 5*time.Second)
	tab.Eval(`(() => { document.querySelector(".ts-readset__more").open = true; document.querySelector("label[for=rs-warm-always]").click(); })()`, nil)
	tab.WaitFor(loaded+` && document.querySelector(".ts-readerpage").dataset.warm === "on"`, 5*time.Second)
}
