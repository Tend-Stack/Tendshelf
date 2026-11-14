//go:build dev

package web

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/wav"
)

// rtocSaved waits until the person's saved reading place is in chapter ch (the POST /reading answered).
func rtocSaved(t *testing.T, l *rdrLab, ch int) {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if rp, ok, err := l.a.srv.progress.Reading(context.Background(), l.ownerID, l.book.ID); err == nil && ok && rp.ChapterIdx == ch {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("the reading place never reached chapter %d", ch)
}

// TestReaderContentsPickBrowser: picking a chapter in the contents goes to it, the sheet closes and stays
// on that chapter whether it is closed with the X, the scrim or Escape, and the place is saved so a reload
// opens there (the bug: closing the sheet jumped back to the chapter it was opened from).
func TestReaderContentsPickBrowser(t *testing.T) {
	b := chromium(t)
	l := pgSeed(t, 6, 6)
	srv := pgServe(t, l)
	for _, vp := range pgViewports {
		t.Run(vp.name, func(t *testing.T) {
			tab := pgOpen(t, b, srv, l, vp.w, vp.h, vp.mobile, "ch=2")
			const open = `document.querySelector('a[href*="sheet=contents"]').click()`
			const sheetGone = `!document.getElementById("reader-sheet-title")`
			const shows3 = `location.search.includes("ch=3") && document.querySelector("section[data-ch='3']") !== null`
			tab.Eval(open, nil)
			tab.WaitFor(`document.getElementById("reader-sheet-title")`, 8*time.Second)
			tab.Eval(`document.querySelector('.ts-reader__toc a[href*="ch=3"]').click()`, nil)
			tab.WaitFor(shows3+" && "+sheetGone, 8*time.Second)
			rtocSaved(t, l, 3)
			for _, how := range []string{
				`document.querySelector(".ts-sheet__head a[aria-label]").click()`,
				`document.querySelector("#sheet .ts-scrim").click()`,
				`document.dispatchEvent(new KeyboardEvent("keydown", {key: "Escape"}))`,
			} {
				tab.Eval(open, nil)
				tab.WaitFor(`document.getElementById("reader-sheet-title")`, 8*time.Second)
				tab.Eval(how, nil)
				tab.WaitFor(sheetGone, 8*time.Second)
				time.Sleep(500 * time.Millisecond) // a late navigation to the old address would show here
				var ok bool
				tab.Eval(shows3, &ok)
				if !ok {
					t.Fatalf("after %s the page is not on chapter 3", how)
				}
			}
			tab.Navigate(srv.URL + l.readPath(""))
			tab.WaitFor(`document.querySelector("section[data-ch='3']") !== null`, 8*time.Second)
		})
	}
}

// rtocAudio gives the lab's audiobook a real 90 s file and chapters named like the ebook's chapters
// (document chapter 3 is "Chapter 2").
func rtocAudio(t *testing.T, l *rdrLab) {
	t.Helper()
	files, err := l.a.lib.AudioFiles(context.Background(), l.book.ID)
	if err != nil || len(files) != 1 {
		t.Fatalf("audio files = %d, %v", len(files), err)
	}
	if err := os.MkdirAll(filepath.Dir(files[0].Path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := wav.Write(files[0].Path, 90_000, 440); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(files[0].Path)
	if err != nil {
		t.Fatal(err)
	}
	streamExec(t, l.a, `UPDATE audio_files SET container='wav', codec='pcm_s16le', duration_ms=90000, size=?, mtime_ns=? WHERE id=?`,
		fi.Size(), fi.ModTime().UnixNano(), files[0].ID)
	for i, c := range []struct {
		start int64
		title string
	}{{0, "Opening"}, {20_000, "Chapter 1"}, {40_000, "Chapter 2"}, {60_000, "Chapter 3"}} {
		streamExec(t, l.a, `INSERT INTO chapters(book_id, idx, start_ms, end_ms, title, source) VALUES (?, ?, ?, ?, ?, 'file')`,
			l.book.ID, i, c.start, c.start+20_000, c.title)
	}
}

// TestReaderSpeakerChoiceBrowser: the speaker asks where to listen from; the first row plays at the start of
// the audio chapter that matches the chapter being read, and tapping the speaker while it plays pauses with
// no sheet.
func TestReaderSpeakerChoiceBrowser(t *testing.T) {
	b := chromium(t)
	for _, vp := range pgViewports {
		t.Run(vp.name, func(t *testing.T) {
			// A lab of its own: the first run's listening place would otherwise be the second's first row.
			l := pgSeed(t, 6, 6)
			rtocAudio(t, l)
			srv := pgServe(t, l)
			tab := pgOpen(t, b, srv, l, vp.w, vp.h, vp.mobile, "ch=3")
			const speaker = `document.querySelector("[data-ts-choose]")`
			tab.WaitFor(speaker+" !== null && window.tsPlayer !== undefined", 8*time.Second)
			tab.Eval(`document.querySelector("[data-chrome]")?.setAttribute("data-chrome", "on")`, nil) // a phone opens with the bar hidden
			time.Sleep(400 * time.Millisecond)
			playerClickSel(t, tab, "[data-ts-choose]") // a real click: sound needs a user gesture
			tab.WaitFor(`document.querySelector("#sheet #format-choice-title") !== null`, 8*time.Second)
			var first string
			tab.Eval(`document.querySelector("#sheet .ts-choice [data-ts-play-at]").dataset.tsPlayAt`, &first)
			if first != "40000" {
				t.Fatalf("the first row plays at %q, want the start of Chapter 2 (40000)", first)
			}
			playerClickSel(t, tab, "#sheet .ts-choice [data-ts-play-at]")
			tab.WaitFor(`window.tsPlayer.state().bookId === `+jsString(l.book.ID)+` && window.tsPlayer.state().playing && window.tsPlayer.state().positionMs >= 40000`, 10*time.Second)
			tab.WaitFor(`document.querySelector("#sheet .ts-sheet") === null`, 8*time.Second)
			playerClickSel(t, tab, "[data-ts-choose]")
			tab.WaitFor(`!window.tsPlayer.state().playing`, 8*time.Second)
			time.Sleep(400 * time.Millisecond)
			var sheet bool
			tab.Eval(`document.querySelector("#sheet .ts-sheet") !== null`, &sheet)
			if sheet {
				t.Error("tapping the speaker while it plays opened the sheet")
			}
		})
	}
}
