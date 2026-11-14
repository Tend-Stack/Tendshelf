//go:build dev

package web

import (
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// prcOpenRead clicks the player's read control: the bar's book icon on a wide screen, the full player's
// item (after opening the full player) on a phone.
func prcOpenRead(t *testing.T, tab *cdp.Tab, mobile bool) {
	t.Helper()
	if mobile {
		playerClickSel(t, tab, "[data-ts-open]")
		tab.WaitFor(fullpDialog+`.open`, 3*time.Second)
		time.Sleep(450 * time.Millisecond) // the view transition
		playerClickSel(t, tab, "[data-ts-full-read]")
		return
	}
	playerClickSel(t, tab, "[data-ts-bar-read]")
}

// TestPlayerReadChoiceBrowser: the player's read control. Where the listening chapter has a match the
// "Read from" sheet opens (the full player closes first) and its first row goes to the mapped chapter, the sheet
// goes and the audio plays on. Where nothing matches the control goes straight to the reader (HX-Location), and
// the audio plays on too.
func TestPlayerReadChoiceBrowser(t *testing.T) {
	b := chromium(t)
	for _, vp := range pgViewports {
		t.Run(vp.name, func(t *testing.T) {
			l := pgSeed(t, 6, 6)
			rtocAudio(t, l)
			srv := pgServe(t, l)
			tab := b.NewTab(vp.w, vp.h, vp.mobile, true)
			tab.Navigate(srv.URL + "/books/" + l.book.ID)
			tab.WaitFor(`window.htmx !== undefined && window.tsPlayer !== undefined`, 10*time.Second)
			load := func(ms int) {
				// A real click on the book's play button (sound needs a user gesture), then a seek to where the test wants to be.
				tab.WaitFor(`document.querySelector("[data-ts-play]") !== null`, 10*time.Second)
				playerClickSel(t, tab, "[data-ts-play]")
				tab.WaitFor(`window.tsPlayer.state().bookId === `+jsString(l.book.ID)+` && window.tsPlayer.state().playing`, 15*time.Second)
				tab.Eval(`window.tsPlayer.seek(`+itoa(int64(ms))+`)`, nil)
				tab.WaitFor(`window.tsPlayer.state().positionMs >= `+itoa(int64(ms)), 10*time.Second)
				tab.WaitFor(`!document.querySelector("[data-ts-bar-read]").hidden`, 5*time.Second)
			}
			read := func() { prcOpenRead(t, tab, vp.mobile) }

			// Nothing maps ("Opening" has no ebook chapter): straight to the reader, the audio keeps playing.
			load(10_000)
			read()
			tab.WaitFor(`location.pathname === "/books/`+l.book.ID+`/read" && document.getElementById("reader") !== null`, 10*time.Second)
			var path string
			tab.Eval(`location.pathname + location.search`, &path)
			if path != "/books/"+l.book.ID+"/read" {
				t.Errorf("the straight way went to %q", path)
			}
			var sheet bool
			tab.Eval(`document.querySelector("#sheet .ts-sheet") !== null`, &sheet)
			if sheet {
				t.Error("a sheet opened with one option")
			}
			var before, after float64
			tab.Eval(`window.tsPlayer.state().positionMs`, &before)
			time.Sleep(1500 * time.Millisecond)
			tab.Eval(`window.tsPlayer.state().positionMs`, &after)
			var playing bool
			tab.Eval(`window.tsPlayer.state().playing`, &playing)
			if !playing || after <= before {
				t.Errorf("the audio stopped when going to read: playing=%v, %v -> %v", playing, before, after)
			}

			// "Chapter 2" matches ebook chapter 3: the sheet opens with that first.
			tab.Navigate(srv.URL + "/books/" + l.book.ID)
			tab.WaitFor(`window.htmx !== undefined && window.tsPlayer !== undefined`, 10*time.Second)
			load(45_000)
			read()
			tab.WaitFor(`document.querySelector("#sheet #format-choice-title") !== null`, 8*time.Second)
			var title string
			var rows []string
			tab.Eval(`document.querySelector("#format-choice-title").textContent`, &title)
			tab.Eval(`[...document.querySelectorAll("#sheet .ts-choice a")].map(a => a.getAttribute("href"))`, &rows)
			if title != "Read from" || len(rows) != 2 || !strings.Contains(rows[0], "?ch=3&") || !strings.Contains(rows[1], "?ch=2&") {
				t.Fatalf("sheet %q rows %v", title, rows)
			}
			var full bool
			tab.Eval(`document.getElementById("player-full").open`, &full)
			if full {
				t.Error("the full player stays open under the sheet")
			}
			playerClickSel(t, tab, "#sheet .ts-choice a")
			tab.WaitFor(`document.querySelector("section[data-ch='3']") !== null && document.querySelector("#sheet .ts-sheet") === null`, 10*time.Second)
			tab.Eval(`window.tsPlayer.state().positionMs`, &before)
			time.Sleep(1500 * time.Millisecond)
			tab.Eval(`window.tsPlayer.state().positionMs`, &after)
			tab.Eval(`window.tsPlayer.state().playing`, &playing)
			if !playing || after <= before {
				t.Errorf("the audio stopped when picking a row: playing=%v, %v -> %v", playing, before, after)
			}
		})
	}
}
