//go:build dev

package web

import (
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// rpfFailing makes every fetch of a chapter part fail while window.__fails is above zero (a dropped network).
const rpfFailing = `window.__fails = 1000; const realFetch = window.fetch.bind(window);
window.fetch = (u, o) => (String(u).includes("/read/chapters/") && window.__fails-- > 0 ? Promise.reject(new TypeError("down")) : realFetch(u, o));`

// TestReaderPartFailureToast: a part that cannot be fetched gives one toast however often the person turns on,
// Try again recovers once the network is back, and leaving the reader leaves no toast behind.
func TestReaderPartFailureToast(t *testing.T) {
	b := chromium(t)
	l := pgSeedEPUB(t, pgLongChapter(), nil)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	ch := l.doc.Chapters[1]
	parts := rsParts(ch)
	lastBlock := ch.Blocks[parts[0].To-1]
	lastSID := ebook.SentenceID(lastBlock.ID, len(lastBlock.Sentences)-1)
	toasts := `document.querySelectorAll("#toasts .ts-toast").length`

	// open shows the end of part 1 with every part fetch failing, and turns on until the toast shows.
	open := func(t *testing.T) *cdp.Tab {
		tab := b.NewTab(390, 844, true, true)
		_ = tab.Call("Page.enable", map[string]any{}, nil)
		if err := tab.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": rpfFailing}, nil); err != nil {
			t.Fatal(err)
		}
		tab.Navigate(srv.URL + l.readPath("at="+lastSID))
		tab.WaitFor(`window.htmx !== undefined && document.querySelector("#reader.ts-reader--pages") !== null`, 8*time.Second)
		for i := 0; i < 8; i++ { // to the last page, then past it
			pgNext(t, tab)
			time.Sleep(150 * time.Millisecond)
		}
		tab.WaitFor(toasts+` === 1`, 10*time.Second)
		return tab
	}

	t.Run("one-toast-and-try-again", func(t *testing.T) {
		tab := open(t)
		for i := 0; i < 2; i++ {
			pgNext(t, tab) // another failed turn: the quiet retry, then the same toast
			time.Sleep(3500 * time.Millisecond)
			var n int
			tab.Eval(toasts, &n)
			if n != 1 {
				t.Fatalf("%d toasts after another failed turn", n)
			}
		}
		tab.Eval(`window.__fails = 0`, nil)
		tab.Eval(`document.querySelector("#toasts .ts-toast button").click()`, nil)
		tab.WaitFor(`document.querySelector("section[data-part]").dataset.part === "2" && `+toasts+` === 0`, 8*time.Second)
	})

	t.Run("leaving-clears-the-toast", func(t *testing.T) {
		tab := open(t)
		tab.Eval(`document.querySelector(".ts-readerbar a").click()`, nil)
		tab.WaitFor(`location.pathname === "/books/`+l.book.ID+`" && document.querySelector("#reader") === null`, 8*time.Second)
		time.Sleep(300 * time.Millisecond)
		var n int
		tab.Eval(toasts, &n)
		if n != 0 {
			t.Errorf("%d toasts followed the person to the book page", n)
		}
	})
}
