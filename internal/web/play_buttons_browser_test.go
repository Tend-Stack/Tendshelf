//go:build dev

package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// P1c-6 browser tests (make player): the real play buttons of the book page and Home, in Chromium,
// on the P1c-4 fixture (one book of two 90 s WAV files and six 30 s chapters).

// playBtnOpenPage opens a page and waits for the player module.
func playBtnOpenPage(t *testing.T, b *cdp.Browser, url string, w, h int) *cdp.Tab {
	t.Helper()
	tab := b.NewTab(w, h, w < 768, false)
	tab.Navigate(url)
	tab.WaitFor(`window.tsPlayer !== undefined`, 10*time.Second)
	return tab
}

func playBtnText(tab *cdp.Tab, sel string) string {
	var s string
	tab.Eval(`(document.querySelector(`+cdp.Quote(sel)+`) || {textContent: ""}).textContent.trim()`, &s)
	return s
}

// TestPlayButtonBrowserFirstSound clicks the real play button of the book page (no injected link):
// five cold runs, median under 1000 ms from the click to the first sound.
func TestPlayButtonBrowserFirstSound(t *testing.T) {
	e := playerApp(t)
	var runs []float64
	for i := 0; i < 5; i++ {
		b := chromium(t) // a new browser per run: empty storage, empty cache
		tab := playBtnOpenPage(t, b, e.URL+"/books/"+e.book.ID, 390, 800)
		if got := playBtnText(tab, "#book-play [data-ts-play]"); got != "Start listening" {
			t.Fatalf("run %d: the book page button says %q", i, got)
		}
		playerClickSel(t, tab, "#book-play [data-ts-play]")
		tab.WaitFor(`performance.getEntriesByName("ts-first-sound").length > 0`, 10*time.Second)
		var ms float64
		tab.Eval(`performance.measure("fs", "ts-play-click", "ts-first-sound").duration`, &ms)
		runs = append(runs, ms)
		if s := playerStateOf(tab); !s.Playing || s.BookID != e.book.ID {
			t.Errorf("run %d: state %+v", i, s)
		}
	}
	med := playerMedian(runs)
	t.Logf("first sound from the book page button: runs %.0f ms, median %.0f ms", runs, med)
	out, _ := json.Marshal(map[string]any{"runsMs": runs, "medianMs": med})
	if err := os.WriteFile(filepath.Join(playerOut(t), "first-sound-book-page.json"), out, 0o644); err != nil {
		t.Fatal(err)
	}
	if med >= 1000 {
		t.Errorf("median first sound %.0f ms, want under 1000", med)
	}
}

// playBtnSlowPlayer delays the player script until gate is closed, so a click can arrive before it ran.
type playBtnSlowPlayer struct {
	next http.Handler
	gate chan struct{}
}

func (s playBtnSlowPlayer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.URL.Path, "/js/player") {
		<-s.gate
	}
	s.next.ServeHTTP(w, r)
}

// TestPlayButtonBrowserEarlyClick: player.js loads as an async module, so a click can arrive first.
// The link's own href then opens the file at the place (htmx must not boost it).
func TestPlayButtonBrowserEarlyClick(t *testing.T) {
	e := playerApp(t)
	e.store(t, 95_000, "sweepfirstdev", "First device", 0, false)
	gate := make(chan struct{})
	srv := httptest.NewServer(playBtnSlowPlayer{next: e.spy, gate: gate})
	t.Cleanup(srv.Close)
	t.Cleanup(func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	})
	tab := chromium(t).NewTab(390, 800, true, false)
	if err := tab.Call("Page.navigate", map[string]any{"url": srv.URL + "/books/" + e.book.ID}, nil); err != nil { // Navigate would wait for the module
		t.Fatal(err)
	}
	tab.WaitFor(`document.querySelector("#book-play [data-ts-play]") !== null`, 10*time.Second)
	var hasPlayer bool
	tab.Eval(`window.tsPlayer !== undefined`, &hasPlayer)
	if hasPlayer {
		t.Fatal("the player loaded before the click: the test proves nothing")
	}
	playerClickSel(t, tab, "#book-play [data-ts-play]")
	tab.WaitFor(`location.pathname === "/stream/`+e.files[1].ID+`"`, 10*time.Second)
	var hash string
	tab.Eval(`location.hash`, &hash)
	if hash != "#t=5.000" {
		t.Errorf("the plain link opened %q, want #t=5.000 (95 s is 5 s into file 2)", hash)
	}
}

// TestPlayButtonBrowserMarkFinished: "Mark as finished" answers 204 with HX-Trigger, which reaches the
// body on htmx 4 and refreshes #book-play without a page load; "Listen again" on a finished book that
// is still loaded plays from the start.
func TestPlayButtonBrowserMarkFinished(t *testing.T) {
	e := playerApp(t)
	e.store(t, 100_000, "sweepfirstdev", "First device", time.Now().UnixMilli()-5000, false)
	tab := playBtnOpenPage(t, chromium(t), e.URL+"/books/"+e.book.ID, 390, 800)
	if got := playBtnText(tab, "#book-play [data-ts-play]"); got != "Resume" {
		t.Fatalf("the button says %q, want Resume", got)
	}
	// Load the book and play, so it is the loaded one when the mark arrives.
	playerClickSel(t, tab, "#book-play [data-ts-play]")
	tab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().positionMs > 100500`, 10*time.Second)
	tab.Eval(`window.tsPlayer.pause(); window.__marker = "same page"`, nil)
	playerClickSel(t, tab, "#book-play form button")
	tab.WaitFor(`document.querySelector("#book-play [data-ts-play]").textContent.trim() === "Listen again"`, 8*time.Second)
	var same string
	tab.Eval(`window.__marker || ""`, &same)
	if same != "same page" {
		t.Fatal("the region refreshed with a page load, not through HX-Trigger")
	}
	if got := playBtnText(tab, "#book-play form button"); got != "Mark as not started" {
		t.Errorf("the mark button says %q", got)
	}
	var fromStart string
	tab.Eval(`document.querySelector("#book-play [data-ts-play]").dataset.tsFromStart || ""`, &fromStart)
	if fromStart != "1" {
		t.Error("Listen again is not marked from-start")
	}
	// Listen again: the book is still loaded, so the click seeks to 0 and plays.
	playerClickSel(t, tab, "#book-play [data-ts-play]")
	tab.WaitFor(`window.tsPlayer.state().playing`, 8*time.Second)
	if s := playerStateOf(tab); s.PositionMs > 8000 || s.FileIdx != 0 {
		t.Errorf("Listen again played from %+v, want the start of the book", s)
	}
}

// TestHomeHeroBrowserOneResume: with the hero's Resume on screen the mini-player is hidden; scrolled
// past, it returns; a pause saves and refreshes the hero.
func TestHomeHeroBrowserOneResume(t *testing.T) {
	e := playerApp(t)
	e.store(t, 10_000, "sweepfirstdev", "First device", time.Now().UnixMilli()-5000, false)
	tab := playBtnOpenPage(t, chromium(t), e.URL+"/", 390, 700)
	tab.WaitFor(`window.tsPlayer.state().bookId !== ""`, 8*time.Second)
	if got := playBtnText(tab, "#home-hero [data-ts-play]"); got != "Resume" {
		t.Fatalf("the hero button says %q", got)
	}
	if n := strings.TrimSpace(playBtnText(tab, "#home-hero .ts-hero__where")); !strings.HasPrefix(n, "Chapter 1 · ") {
		t.Errorf("where-you-are line %q", n)
	}
	tab.WaitFor(`document.getElementById("player").classList.contains("is-suppressed") && document.getElementById("player").inert`, 5*time.Second)
	// Scrolled past the hero: the bar is back.
	tab.Eval(`(() => { const d = document.createElement("div"); d.style.height = "3000px"; document.getElementById("main").append(d); window.scrollTo(0, 2500); })()`, nil)
	tab.WaitFor(`!document.getElementById("player").classList.contains("is-suppressed") && !document.getElementById("player").inert`, 5*time.Second)
	tab.Eval(`window.scrollTo(0, 0)`, nil)
	tab.WaitFor(`document.getElementById("player").classList.contains("is-suppressed")`, 5*time.Second)
	// Play from the hero, pause: the save refreshes the hero's Resume to the new place.
	playerClickSel(t, tab, "#home-hero [data-ts-play]")
	tab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().positionMs > 14000`, 10*time.Second)
	tab.Eval(`window.tsPlayer.pause(); window.__marker = "same page"`, nil)
	tab.WaitFor(`Number(document.querySelector("#home-hero [data-ts-play]").dataset.tsStartMs) >= 13000`, 8*time.Second)
	var same string
	tab.Eval(`window.__marker || ""`, &same)
	if same != "same page" {
		t.Error("the hero refreshed with a page load")
	}
	if n := playBtnText(tab, "#home-hero [data-ts-play]"); n != "Resume" {
		t.Errorf("the refreshed hero button says %q", n)
	}
	var n int
	tab.Eval(`document.querySelectorAll("[data-ts-play='`+e.book.ID+`']").length`, &n)
	if n != 1 {
		t.Errorf("Home has %d resume controls for the book", n)
	}
}

// TestPlayChaptersBrowser: a chapter link starts the book at that chapter, loaded or not.
func TestPlayChaptersBrowser(t *testing.T) {
	e := playerApp(t)
	tab := playBtnOpenPage(t, chromium(t), e.URL+"/books/"+e.book.ID+"/chapters", 390, 800)
	near := func(want int64) {
		t.Helper()
		lo, hi := want-1000, want+4000 // a little playing time passes before the poll
		tab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().positionMs >= `+strconv.FormatInt(lo, 10)+
			` && window.tsPlayer.state().positionMs <= `+strconv.FormatInt(hi, 10), 10*time.Second)
	}
	// click scrolls the row to the middle of the viewport first: a real click needs it on screen.
	click := func(ms int64) {
		sel := `a.ts-chapter[data-ts-play-at="` + strconv.FormatInt(ms, 10) + `"]`
		tab.Eval(`document.querySelector(`+cdp.Quote(sel)+`).scrollIntoView({block: "center"})`, nil)
		time.Sleep(300 * time.Millisecond) // the bar may slide in or out
		playerClickSel(t, tab, sel)
	}
	// Not loaded: chapter 4 starts at 90 s, the first sample of file 2.
	click(90_000)
	near(90_000)
	if s := playerStateOf(tab); s.BookID != e.book.ID {
		t.Errorf("state %+v", s)
	}
	// Loaded: chapter 2 starts at 30 s.
	click(30_000)
	near(30_000)
}
