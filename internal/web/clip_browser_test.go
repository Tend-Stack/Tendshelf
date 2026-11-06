//go:build dev

package web

import (
	"context"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// clip.js stops a clip at its end and leaves the player alone otherwise (make player; real Chromium).

const (
	clipStartMs = 20_000
	clipEndMs   = 25_000
)

// clipOpenTab saves a clip on the fixture book, loads the book page, and reaches the Bookmarks tab by the
// boosted tab link, so the script comes in with a swap as it does for a person.
func clipOpenTab(t *testing.T, e *playerEnv) *cdp.Tab {
	t.Helper()
	ctx := context.Background()
	bm, err := e.a.srv.progress.AddBookmark(ctx, e.ownerID, e.book.ID, clipStartMs, "a line")
	if err != nil {
		t.Fatal(err)
	}
	end, kind := int64(clipEndMs), "quote"
	if _, err := e.a.srv.progress.UpdateBookmark(ctx, e.ownerID, bm.ID, progress.BookmarkPatch{EndMs: &end, Kind: &kind}); err != nil {
		t.Fatal(err)
	}
	tab := fullpOpen(t, e, 390, 844, false)
	tab.Navigate(e.URL + "/books/" + e.book.ID)
	tab.WaitFor(`window.tsPlayer && window.tsPlayer.state().bookId !== ""`, 10*time.Second)
	playerClickSel(t, tab, `a[href="/books/`+e.book.ID+`/bookmarks"]`)
	tab.WaitFor(`document.querySelector("[data-ts-clip-end]") !== null && !document.querySelector("[data-ts-clip-end]").hidden`, 5*time.Second)
	return tab
}

func TestClipBrowserStopsAtEnd(t *testing.T) {
	e := playerApp(t)
	tab := clipOpenTab(t, e)
	playerClickSel(t, tab, "[data-ts-clip-end]")
	tab.WaitFor(`window.tsPlayer.state().playing`, 5*time.Second)
	if p := fullpPos(tab); p < clipStartMs-1000 || p > clipEndMs {
		t.Fatalf("the clip started at %d ms", p)
	}
	tab.WaitFor(`!window.tsPlayer.state().playing`, 10*time.Second)
	fullpNear(t, "the clip stopped at", fullpPos(tab), clipEndMs, 500)
	// It stays stopped: the stop is not a loop.
	time.Sleep(800 * time.Millisecond)
	var playing bool
	tab.Eval(`window.tsPlayer.state().playing`, &playing)
	if playing {
		t.Error("the sound started again after the clip")
	}
}

func TestClipBrowserSeekDisarms(t *testing.T) {
	e := playerApp(t)
	tab := clipOpenTab(t, e)
	playerClickSel(t, tab, "[data-ts-clip-end]")
	tab.WaitFor(`window.tsPlayer.state().playing`, 5*time.Second)
	time.Sleep(700 * time.Millisecond) // let the script see the clip playing
	tab.Eval(`window.tsPlayer.seek(23000)`, nil)
	time.Sleep(500 * time.Millisecond)
	// A seek past the end: the sound keeps playing (the clip is disarmed, not stopped).
	tab.Eval(`window.tsPlayer.seek(60000)`, nil)
	time.Sleep(2500 * time.Millisecond)
	var s struct {
		Playing bool
		Pos     int64
	}
	tab.Eval(`({Playing: window.tsPlayer.state().playing, Pos: window.tsPlayer.state().positionMs})`, &s)
	if !s.Playing || s.Pos < 60000 {
		t.Errorf("after seeking past the end: playing %v at %d ms", s.Playing, s.Pos)
	}
}
