//go:build dev

package web

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
	"github.com/Tend-Stack/tendshelf/internal/testutil/wav"
)

// The bug (operator, 2026-11-03): writing details into a book stopped the book that was playing.
// These tests play book A, work on book B, and after every step assert that no page load happened
// and A still plays and has moved on.

// addPlayingBook adds a book of two WAV files that the browser can play to the app, in a library
// of its own, and returns the pieces playerInject needs.
func addPlayingBook(t *testing.T, a *testApp) *playerEnv {
	t.Helper()
	_, f := librarytest.NewLibrary(t, a.lib, "Playing library", t.TempDir())
	book := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{
		Title: "Harbour Lamp", Authors: []string{"Imogen Hale"}, DurationMs: 180_000, Files: 2,
	})
	files, err := a.lib.AudioFiles(context.Background(), book.ID)
	if err != nil || len(files) != 2 {
		t.Fatalf("audio files = %d, %v", len(files), err)
	}
	for i, af := range files {
		if err := wav.Write(af.Path, 90_000, 440+i*100); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(af.Path)
		if err != nil {
			t.Fatal(err)
		}
		streamExec(t, a, `UPDATE audio_files SET container='wav', codec='pcm_s16le', duration_ms=90000, offset_ms=?, size=?, mtime_ns=? WHERE id=?`,
			int64(i)*90_000, fi.Size(), fi.ModTime().UnixNano(), af.ID)
	}
	files, _ = a.lib.AudioFiles(context.Background(), book.ID)
	return &playerEnv{a: a, book: book, files: files}
}

// keepsPlayingTab opens the app, plays book A, goes to book B's Details tab by a boosted link and
// returns the tab with a check that asserts, after each step, that no page load happened and A
// still plays and has moved on.
func keepsPlayingTab(t *testing.T, b *cdp.Browser, a *testApp, bookB string, w, h int) (*cdp.Tab, func(step string)) {
	t.Helper()
	e := addPlayingBook(t, a)
	base := themedServer(t, a.signedIn(t))
	tab := playerOpen(t, b, base, w, h)
	e.playerInject(tab, e.book.ID, 0, 0, "")
	playerClickSel(t, tab, "#play-link")
	tab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().positionMs > 300`, 10*time.Second)
	tab.Eval(`window.__tsMarker = 1`, nil)
	var last int64
	check := func(step string) {
		t.Helper()
		var marker int
		tab.Eval(`window.__tsMarker || 0`, &marker)
		if marker != 1 {
			t.Errorf("%s: the page was reloaded", step)
		}
		s := playerStateOf(tab)
		if !s.Playing || s.BookID != e.book.ID {
			t.Errorf("%s: state %+v, want book A playing", step, s)
		}
		deadline := time.Now().Add(4 * time.Second)
		for s.PositionMs <= last && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
			s = playerStateOf(tab)
		}
		if s.PositionMs <= last {
			t.Errorf("%s: position %d did not advance past %d", step, s.PositionMs, last)
		}
		last = s.PositionMs
	}
	check("playing")
	// htmx.process makes the injected link a boosted one, as a link in the page would be
	tab.Eval(`(() => { const a = document.createElement("a"); a.id = "go-b"; a.href = "/books/`+bookB+`/details"; a.textContent = "B";
	  a.style.cssText = "position:fixed;left:8px;top:80px;width:120px;height:60px;z-index:50;background:white;color:black";
	  document.getElementById("main").append(a); htmx.process(a); })()`, nil)
	playerClickSel(t, tab, "#go-b")
	tab.WaitFor(`location.pathname.endsWith("/details") && document.querySelector(".ts-book__write, .ts-dpanel__cover") !== null`, 8*time.Second)
	check("details tab")
	return tab, check
}

var keepsPlayingViewports = []struct {
	name string
	w, h int
}{{"desktop-1440", 1440, 900}, {"mobile-390", 390, 844}}

// TestWriteKeepsPlayingBrowser writes details into book B and chooses Undo, then Looks good, while
// book A plays. Needs TENDSHELF_TEST_BROWSER.
func TestWriteKeepsPlayingBrowser(t *testing.T) {
	b := chromium(t)
	for _, vp := range keepsPlayingViewports {
		t.Run(vp.name, func(t *testing.T) {
			l := newWriteLab(t, 1, false)
			l.start(t)
			tab, check := keepsPlayingTab(t, b, l.a, l.book.ID, vp.w, vp.h)
			round := func(name, choice string) {
				tab.WaitFor(`document.querySelector(".ts-book__write") !== null`, 5*time.Second)
				playerClickSel(t, tab, ".ts-book__write")
				tab.WaitFor(`document.querySelector("#sheet form.ts-write__form") !== null`, 5*time.Second)
				check(name + ": sheet open")
				playerClickSel(t, tab, `#sheet form.ts-write__form button[type="submit"]`)
				tab.WaitFor(`document.querySelector("#sheet #write-body") !== null`, 8*time.Second)
				check(name + ": write started")
				tab.WaitFor(`document.querySelector("#sheet .ts-write__pending") !== null`, 20*time.Second)
				check(name + ": waiting for you")
				tab.Eval(`[...document.querySelectorAll("#sheet .ts-write__actions button")].find(b => b.textContent.trim() === "`+choice+`").setAttribute("id", "choice")`, nil)
				playerClickSel(t, tab, "#choice")
				tab.WaitFor(`document.querySelector(".ts-write__pending") === null && document.querySelector("#sheet .ts-write") === null`, 8*time.Second)
				check(name + ": " + choice)
			}
			round("first", "Undo")
			round("second", "Looks good")
		})
	}
}

// TestCoverKeepsPlayingBrowser does the same for Change cover: add a picture, wait for the job,
// then choose the other cover.
func TestCoverKeepsPlayingBrowser(t *testing.T) {
	b := chromium(t)
	for _, vp := range keepsPlayingViewports {
		t.Run(vp.name, func(t *testing.T) {
			l := newCoverLab(t)
			l.start(t)
			tab, check := keepsPlayingTab(t, b, l.a, l.book.ID, vp.w, vp.h)
			playerClickSel(t, tab, ".ts-dpanel__cover")
			tab.WaitFor(`document.querySelector("#sheet form.ts-cover-pick__upload") !== null`, 5*time.Second)
			check("cover sheet open")
			png := base64.StdEncoding.EncodeToString(coverPNG(t))
			tab.Eval(fmt.Sprintf(`(() => {
				const bytes = Uint8Array.from(atob(%q), c => c.charCodeAt(0));
				const dt = new DataTransfer();
				dt.items.add(new File([bytes], "new-cover.png", {type: "image/png"}));
				document.querySelector('#sheet input[type="file"]').files = dt.files;
			})()`, png), nil)
			playerClickSel(t, tab, `#sheet form.ts-cover-pick__upload button[type="submit"]`)
			tab.WaitFor(`document.querySelector("#sheet #cover-body") !== null`, 8*time.Second)
			check("upload started")
			tab.WaitFor(`document.querySelector("#sheet #cover-body").textContent.includes("The new cover is in place.")`, 15*time.Second)
			check("upload finished")
			// choose the other card (the folder cover) and press the button
			tab.Eval(`document.querySelector('#sheet input[name="cover_id"]:not(:checked)').closest("label").id = "other-card"`, nil)
			playerClickSel(t, tab, "#other-card")
			playerClickSel(t, tab, `#sheet form.ts-cover-pick__form button[type="submit"]`)
			tab.WaitFor(`document.querySelector("#sheet .ts-cover-pick") === null`, 8*time.Second)
			check("cover chosen")
			if got := l.bookCover(t, l.book.ID); got != l.cover {
				t.Errorf("the chosen cover was not made current")
			}
		})
	}
}
