//go:build dev

package web

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/webdriver"
)

// The stored reader shell of a right-to-left book (R4, plan item 5). Kept offline, opened with no connection:
// the worker's shell carries dir="rtl", one forward turn (ArrowLeft) moves the page to a negative scrollLeft,
// and the place survives a reload offline.
// Run by: TENDSHELF_TEST_BROWSER=<chromium> go test -tags dev -run TestRTLOfflineShell ./internal/web/

func TestRTLOfflineShell(t *testing.T) {
	b := chromium(t)
	e := ofrApp(t, rtlChapters(), func(o *ebooktest.EPUBOptions) { o.Lang = "ar"; o.Title = "كتاب" })
	pgFlow(t, e.l, "pages")
	tab := b.NewTab(390, 844, true, true)
	ofrSeedCache(t, e, tab)
	ofrOffline(t, e, tab, true)

	tab.Navigate(e.read("ch=2"))
	tab.WaitFor(`document.querySelector("[data-offline-shell]") && `+ofrMounted, 15*time.Second)
	time.Sleep(300 * time.Millisecond)
	var dir string
	tab.Eval(`document.getElementById("reader").dir`, &dir)
	if dir != "rtl" {
		t.Fatalf("offline reader dir %q, want rtl", dir)
	}
	if v := rtlLeft(tab); v != 0 {
		t.Fatalf("scrollLeft at the start %v, want 0", v)
	}
	var step float64
	pgEval(tab, `pgStep()`, &step)
	first := rtlFirst(tab)

	pgKey(t, tab, "ArrowLeft", "ArrowLeft", 37, "", 0)
	pgWaitFor(tab, rtlPage+` === 1`)
	if v := rtlLeft(tab); v < -step-1 || v > -step+1 {
		t.Errorf("scrollLeft after ArrowLeft %v, want about %v", v, -step)
	}
	if second := rtlFirst(tab); second == first || second == "" {
		t.Errorf("the page did not move on: %q then %q", first, second)
	}
	for i := 0; i < 2; i++ {
		pgKey(t, tab, "ArrowLeft", "ArrowLeft", 37, "", 0)
		time.Sleep(250 * time.Millisecond)
	}
	pgWaitFor(tab, rtlPage+` === 3`)
	var at string
	tab.Eval(`document.getElementById("reader").dataset.at`, &at)

	// Leave the page the way a person does, then open it again offline: same direction, same place.
	tab.Eval(`window.dispatchEvent(new Event("pagehide"))`, nil)
	tab.WaitFor(`localStorage.getItem("ts.offline.reading")`, 5*time.Second)
	tab.Navigate(e.read(""))
	tab.WaitFor(`document.querySelector("[data-offline-shell]") && `+ofrMounted, 15*time.Second)
	time.Sleep(600 * time.Millisecond)
	tab.Eval(`document.getElementById("reader").dir`, &dir)
	if dir != "rtl" {
		t.Fatalf("reloaded reader dir %q, want rtl", dir)
	}
	var after string
	tab.Eval(`document.getElementById("reader").dataset.at`, &after)
	if at == "" || after != at {
		t.Errorf("after the reload the place is %q, want %q", after, at)
	}
	if v := rtlLeft(tab); v >= 0 {
		t.Errorf("after the reload scrollLeft %v, want a negative page offset", v)
	}
}

// TestVisualWebKitRTLPages opens the Arabic fixture in WebKitGTK in the pages flow: the reader is right to
// left, scrollLeft starts at 0 and ArrowLeft takes it negative. Same opt-in as the other WebKit tests
// (TENDSHELF_TEST_WEBKIT_DRIVER, run through `scripts/isolated-display.sh make visual-webkit`). The window
// cannot be narrower than 447 CSS px, so the phone width is 447, not 390. The key is a synthetic keydown
// (the driver wrapper has no key input); the reader does not check isTrusted.
func TestVisualWebKitRTLPages(t *testing.T) {
	driver := os.Getenv("TENDSHELF_TEST_WEBKIT_DRIVER")
	if driver == "" {
		t.Skip("TENDSHELF_TEST_WEBKIT_DRIVER not set")
	}
	browser := os.Getenv("TENDSHELF_TEST_WEBKIT_BROWSER")
	if browser == "" {
		browser = defaultMiniBrowser
	}
	l := rtlSeed(t, "pages")
	srv := pgServe(t, l)
	out := visualDir(t, "webkit")
	s := webdriver.Start(t, driver, browser)
	for _, w := range []int{447, 1440} {
		s.SetViewport(w, 900)
		t.Run(fmt.Sprintf("rtl-pages-%d", w), func(t *testing.T) {
			s.Navigate(srv.URL + l.readPath("ch=2"))
			var ready bool
			for i := 0; i < 40 && !ready; i++ {
				s.Eval(`document.querySelector("#reader.ts-reader--pages .ts-s") !== null`, &ready)
				time.Sleep(200 * time.Millisecond)
			}
			if !ready {
				t.Fatal("the reader did not mount")
			}
			time.Sleep(400 * time.Millisecond)
			var m struct {
				Dir   string  `json:"dir"`
				Left  float64 `json:"left"`
				After float64 `json:"after"`
			}
			s.Eval(`({dir: document.getElementById("reader").dir, left: document.querySelector("section[data-part]").scrollLeft})`, &m)
			s.Exec(`window.dispatchEvent(new KeyboardEvent("keydown", {key: "ArrowLeft", code: "ArrowLeft", bubbles: true}))`, nil)
			for i := 0; i < 20 && m.After >= 0; i++ {
				time.Sleep(200 * time.Millisecond)
				s.Eval(`({after: document.querySelector("section[data-part]").scrollLeft})`, &m)
			}
			if m.Dir != "rtl" || m.Left != 0 || m.After >= 0 {
				t.Errorf("dir %q, start scrollLeft %v, after ArrowLeft %v; want rtl, 0, negative", m.Dir, m.Left, m.After)
			}
			s.Screenshot(filepath.Join(out, fmt.Sprintf("rtl-pages-%d.png", w)))
		})
	}
}
