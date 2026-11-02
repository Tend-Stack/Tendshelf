//go:build dev

package web

import (
	"context"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// Saving or deleting a bookmark note from the sheet must not interrupt the sound (make player). The
// sheet's forms post through htmx; a plain form post would reload the page and take the audio with it.

// notekeepOpenSheet makes a bookmark from the full player and follows the toast's "Add a note"
// link, which opens the note sheet over the page. It returns once the sheet is on screen.
func notekeepOpenSheet(t *testing.T, tab *cdp.Tab) {
	t.Helper()
	fullpOpenPlayer(t, tab)
	playerClickSel(t, tab, "#player-full .ts-player__mark")
	tab.WaitFor(`document.querySelector("#player-full [data-ts-full-toasts] .ts-toast a") !== null`, 3*time.Second)
	playerClickSel(t, tab, "#player-full [data-ts-full-toasts] .ts-toast a")
	tab.WaitFor(`!`+fullpDialog+`.open`, 3*time.Second)
	tab.WaitFor(`document.querySelector("#sheet .ts-bookmark-sheet") !== null`, 5*time.Second)
}

// notekeepAfter checks what a finished sheet action leaves behind: the sound still playing and moving,
// the sheet gone, the toast showing.
func notekeepAfter(t *testing.T, tab *cdp.Tab, what, toast string, before int64) {
	t.Helper()
	tab.WaitFor(`document.querySelector("#sheet .ts-bookmark-sheet") === null`, 5*time.Second)
	tab.WaitFor(`[...document.querySelectorAll("#toasts .ts-toast")].some((t) => t.textContent.includes(`+cdp.Quote(toast)+`))`, 5*time.Second)
	time.Sleep(1200 * time.Millisecond)
	var playing bool
	tab.Eval(`window.tsPlayer.state().playing`, &playing)
	if !playing {
		t.Errorf("%s stopped the sound", what)
	}
	if after := fullpPos(tab); after <= before {
		t.Errorf("%s: position %d ms did not move on from %d ms", what, after, before)
	}
}

func TestBookmarkNoteKeepsPlaying(t *testing.T) {
	e := playerApp(t)
	ctx := context.Background()
	stored := func() []progress.Bookmark {
		list, err := e.a.srv.progress.Bookmarks(ctx, e.ownerID, e.book.ID, false)
		if err != nil {
			t.Fatal(err)
		}
		return list
	}
	tab := fullpOpen(t, e, 390, 844, false)
	bmkeyPlay(t, tab, "[data-ts-toggle]")

	// Save a note.
	notekeepOpenSheet(t, tab)
	before := fullpPos(tab)
	tab.Eval(`(() => { const n = document.getElementById("bms-note"); n.value = "Check the lamp"; n.dispatchEvent(new Event("input", {bubbles: true})); })()`, nil)
	playerClickSel(t, tab, `#sheet form[action$="/note"] button[type="submit"]`)
	notekeepAfter(t, tab, "saving a note", "Note saved.", before)
	// The server closed the sheet: focus does not fall to the page body (the toast that opened the
	// sheet is gone, so it goes to the page content).
	var focus string
	tab.Eval(`document.activeElement ? document.activeElement.tagName + "#" + document.activeElement.id : "none"`, &focus)
	if focus != "MAIN#main" {
		t.Errorf("focus after the save is on %s, want the page content", focus)
	}
	list := stored()
	if len(list) != 1 || list[0].Note != "Check the lamp" {
		t.Fatalf("stored bookmarks after the save: %+v", list)
	}

	// Delete the bookmark.
	notekeepOpenSheet(t, tab)
	before = fullpPos(tab)
	playerClickSel(t, tab, `#sheet form[action$="/delete"] button[type="submit"]`)
	notekeepAfter(t, tab, "deleting a bookmark", "Bookmark deleted.", before)
	if list := stored(); len(list) != 1 || list[0].Note != "Check the lamp" {
		t.Errorf("stored bookmarks after the delete: %+v", list)
	}
}
