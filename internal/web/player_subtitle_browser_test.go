//go:build dev

package web

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// sweepSetChapters replaces the fixture book's chapters: one entry per title, equal lengths over the
// book's 180 s. An empty title is a chapter without a name.
func sweepSetChapters(t *testing.T, e *playerEnv, titles []string) {
	t.Helper()
	step := int64(180_000 / len(titles))
	var chs []library.Chapter
	for i, title := range titles {
		chs = append(chs, library.Chapter{StartMs: int64(i) * step, EndMs: int64(i+1) * step, Title: title, Source: library.ChapterFile})
	}
	if err := e.a.lib.Update(context.Background(), func(tx *library.Tx) error { return tx.ReplaceChapters(e.book.ID, chs) }); err != nil {
		t.Fatal(err)
	}
}

// sweepSubtitle reads the line under the book's title and the serif-italic chapter part of it.
func sweepSubtitle(tab *cdp.Tab) (line, italic string) {
	var got struct{ Line, Italic string }
	tab.Eval(`(() => { const p = document.querySelector("#player-full .ts-player__author");
	  return {Line: p.textContent.replace(/\s+/g, " ").trim(), Italic: getComputedStyle(p.querySelector("[data-ts-full-chapter-title]")).fontStyle}; })()`, &got)
	return got.Line, got.Italic
}

// TestFullPlayerBrowserSubtitle: the board's "<chapter title> · <author>" under the title, following
// the chapter; the author alone for an untitled chapter and for a one-chapter book.
func TestFullPlayerBrowserSubtitle(t *testing.T) {
	e := playerApp(t)
	sweepSetChapters(t, e, []string{"Chapter 1", "Chapter 2", "Chapter 3", "", "Chapter 5", "Chapter 6"})
	tab := fullpOpen(t, e, 390, 844, false)
	fullpOpenPlayer(t, tab)

	// 100 s in: the fourth chapter (90 s to 120 s) has no name.
	if line, _ := sweepSubtitle(tab); line != "Imogen Hale" {
		t.Errorf("untitled chapter: %q, want the author alone", line)
	}
	tab.Eval(`window.tsPlayer.seek(10000)`, nil)
	tab.WaitFor(`document.querySelector("#player-full .ts-player__author").textContent.includes("Chapter 1")`, 2*time.Second)
	if line, italic := sweepSubtitle(tab); line != "Chapter 1 · Imogen Hale" || italic != "italic" {
		t.Errorf("chapter 1: %q (%s), want %q in italic", line, italic, "Chapter 1 · Imogen Hale")
	}
	tab.Eval(`window.tsPlayer.seek(70000)`, nil)
	tab.WaitFor(`document.querySelector("#player-full .ts-player__author").textContent.includes("Chapter 3")`, 2*time.Second)
	if line, _ := sweepSubtitle(tab); line != "Chapter 3 · Imogen Hale" {
		t.Errorf("after seeking to chapter 3: %q", line)
	}

	// One chapter: nothing to name.
	sweepSetChapters(t, e, []string{"The whole book"})
	tab = fullpOpen(t, e, 390, 844, false)
	fullpOpenPlayer(t, tab)
	if line, _ := sweepSubtitle(tab); line != "Imogen Hale" {
		t.Errorf("one chapter: %q, want the author alone", line)
	}
}

// TestFullPlayerBrowserLeftInBookHours: the line left in book keeps Home's forms across the hour.
func TestFullPlayerBrowserLeftInBookHours(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 844, false)
	fullpOpenPlayer(t, tab)
	for minutes, want := range map[int]string{0: "0 m left in book", 1: "1 m left in book", 59: "59 m left in book", 60: "1 h left in book",
		61: "1 h 1 m left in book", 65: "1 h 5 m left in book", 120: "2 h left in book", 1052: "17 h 32 m left in book"} {
		var got string
		tab.Eval(`window.tsPlayerFull.debug.leftInBook(`+strconv.Itoa(minutes)+`)`, &got)
		if got != want {
			t.Errorf("%d min: %q, want %q", minutes, got, want)
		}
	}
}
