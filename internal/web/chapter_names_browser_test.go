//go:build dev

package web

import (
	"context"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

// chapterNamesEnv is the player fixture with its chapters replaced: four of them over the 180 s book, in
// the given order (30 s, 60 s, 60 s, 30 s), so 100 s is in the third.
func chapterNamesEnv(t *testing.T, titles [4]string) *playerEnv {
	t.Helper()
	e := playerAppWith(t, func(*librarytest.BookSpec) {})
	bounds := []int64{0, 30_000, 90_000, 150_000, 180_000}
	var chs []library.Chapter
	for i, title := range titles {
		chs = append(chs, library.Chapter{StartMs: bounds[i], EndMs: bounds[i+1], Title: title, Source: library.ChapterFile})
	}
	if err := e.a.lib.Update(context.Background(), func(tx *library.Tx) error { return tx.ReplaceChapters(e.book.ID, chs) }); err != nil {
		t.Fatal(err)
	}
	return e
}

// TestChapterNumbersBrowser: a book that opens with a Prologue says "Chapter 2 of 2" at its third track,
// and the Prologue itself by name; the bar, the header, the chapter list and the chapter bar agree.
func TestChapterNumbersBrowser(t *testing.T) {
	for _, vp := range []struct {
		name string
		w, h int
	}{{"mobile-390", 390, 844}, {"desktop-1440", 1440, 900}} {
		t.Run("prologue "+vp.name, func(t *testing.T) {
			e := chapterNamesEnv(t, [4]string{"Prologue: 9:17 P.M.", "Chapter 1: Mud", "Chapter 2: Rain", "Epilogue"})
			tab := fullpOpen(t, e, vp.w, vp.h, true)
			fullpOpenPlayer(t, tab)
			text := func(js string) string {
				var s string
				tab.Eval(js, &s)
				return s
			}
			header := func() string {
				return text(`document.querySelector("#player-full [data-ts-full-chapter]").textContent.toLowerCase()`)
			}
			// 100 s: the third track, "Chapter 2: Rain"
			if got := header(); got != "chapter 2 of 2" {
				t.Errorf("third track header = %q, want %q", got, "chapter 2 of 2")
			}
			if got := text(`document.querySelector("[data-ts-meta]").textContent`); got != "Chapter 2 · 1 min left" {
				t.Errorf("bar meta = %q", got)
			}
			// the Prologue: its name alone, no "of"
			tab.Eval(`window.tsPlayer.seek(10000)`, nil)
			tab.WaitFor(`document.querySelector("#player-full [data-ts-full-chapter]").textContent.toLowerCase() === "prologue"`, 3*time.Second)
			if got := header(); got != "prologue" {
				t.Errorf("prologue header = %q", got)
			}
			if got := text(`document.querySelector("[data-ts-meta]").textContent`); got != "Prologue · 1 min left" {
				t.Errorf("prologue bar meta = %q", got)
			}
			// the chapter list's number column: empty, 1, 2, empty
			// the sheet's list is built when it opens: from the full player's bottom row at phone width, from the bar's icon on a desktop
			if vp.w < 768 {
				playerClickSel(t, tab, "[data-ts-open-chapters]")
			} else {
				tab.Eval(`document.querySelector("[data-ts-bar-chapters]").click()`, nil)
			}
			tab.WaitFor(`!document.querySelector("#player-full [data-ts-chapters-sheet]").hidden`, 2*time.Second)
			var column []string
			tab.Eval(`[...document.querySelectorAll("#player-full [data-ts-chapter-list] button")].map((b) => b.children[0].textContent)`, &column)
			if len(column) != 4 || column[0] != "" || column[1] != "1" || column[2] != "2" || column[3] != "" {
				t.Errorf("number column = %q, want [\"\" 1 2 \"\"]", column)
			}
			// the chapter bar's description of the third track agrees with the header
			var described string
			tab.Eval(`(() => { const book = window.tsPlayer.info(); return window.tsPlayer.chapterWords(book, 2).header + "|" + window.tsPlayer.chapterWords(book, 3).header; })()`, &described)
			if described != "Chapter 2 of 2|Epilogue" {
				t.Errorf("chapterWords = %q", described)
			}
		})
	}
	t.Run("no titles keeps places", func(t *testing.T) {
		e := chapterNamesEnv(t, [4]string{"", "", "", ""})
		tab := fullpOpen(t, e, 390, 844, true)
		fullpOpenPlayer(t, tab)
		var got string
		tab.Eval(`document.querySelector("#player-full [data-ts-full-chapter]").textContent.toLowerCase()`, &got)
		if got != "chapter 3 of 4" {
			t.Errorf("header = %q, want %q", got, "chapter 3 of 4")
		}
		var meta string
		tab.Eval(`document.querySelector("[data-ts-meta]").textContent`, &meta)
		if meta != "Chapter 3 · 1 min left" {
			t.Errorf("bar meta = %q", meta)
		}
	})
}
