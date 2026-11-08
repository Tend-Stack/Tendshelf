//go:build dev

package web

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// downloadAllAppStub stands in for the Android app's side of the bridge: it answers "dl ask", "dl room", "dl add" and
// "dl remove" the way Bridge.kt does (an add is answered at once with the state it has, then queued, then ready),
// and keeps every word it receives in window.__sent. __held marks books the app already has, __room is what it can
// still give, __readyAfter how long a queued book takes (ms).
const downloadAllAppStub = `(() => {
  window.__sent = [];
  window.__held = {};
  window.__room = 1e12;
  window.__readyAfter = 150;
  const listeners = [];
  const say = (id, state) => window.__push(JSON.stringify({ t: "dl", bookId: id, state, pct: state === "ready" ? 100 : 0 }));
  window.tsNative = {
    onmessage: null,
    addEventListener: (type, fn) => { if (type === "message") listeners.push(fn); },
    postMessage: (m) => {
      window.__sent.push(m);
      const [word, verb, id] = m.split(" ");
      if (word !== "dl") return;
      if (verb === "ask") say(id, window.__held[id] || "none");
      if (verb === "room") setTimeout(() => window.__push(JSON.stringify({ t: "room", bytes: window.__room })), 10);
      if (verb === "remove") { delete window.__held[id]; say(id, "none"); }
      if (verb === "add") {
        say(id, window.__held[id] || "none");
        setTimeout(() => { window.__held[id] = "queued"; say(id, "queued"); }, 60);
        setTimeout(() => { if (window.__held[id] === "queued") { window.__held[id] = "ready"; say(id, "ready"); } }, 60 + window.__readyAfter);
      }
    },
  };
  window.__push = (json) => { listeners.forEach((fn) => fn({ data: json })); };
})();`

func downloadAllAppSeries(t *testing.T, e *playerEnv) (string, []library.Book) {
	t.Helper()
	var books []library.Book
	for i := 1; i <= 4; i++ {
		books = append(books, librarytest.AddBook(t, e.a.lib, e.folder, librarytest.BookSpec{
			Title: fmt.Sprintf("App Saga %d", i), Authors: []string{"Imogen Hale"}, DurationMs: 60_000, Files: 1,
			Series: "App Saga", Sequence: fmt.Sprint(i),
		}))
	}
	streamExec(t, e.a, `UPDATE audio_files SET size = 5000000 WHERE book_id IN (SELECT id FROM books WHERE title LIKE 'App Saga %')`)
	series, err := e.a.lib.BookSeries(context.Background(), books[0].ID)
	if err != nil || len(series) != 1 {
		t.Fatal(series, err)
	}
	return "/series/" + series[0].SeriesID, books
}

// TestDownloadAllNativeBrowser: in the app's window Download all asks how each book stands, skips the ones the app
// holds, confirms, then sends "dl add <id>" for each in the page's order; it asks the app for room first, and Stop sends
// "dl remove" for what is still queued.
func TestDownloadAllNativeBrowser(t *testing.T) {
	for _, w := range []int{390, 1440} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			b := chromium(t)
			e := playerApp(t)
			path, books := downloadAllAppSeries(t, e)
			tab := b.NewTab(w, 900, w < 768, false)
			_ = tab.Call("Page.enable", nil, nil)
			if err := tab.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": downloadAllAppStub}, nil); err != nil {
				t.Fatal(err)
			}
			tab.Navigate(e.URL + "/?shell=android&next=" + path)
			tab.WaitFor(`document.querySelector("[data-ts-download-all]") && !document.querySelector("[data-ts-download-all]").hidden`, 15*time.Second)
			// The app already holds the second book.
			tab.Eval(`window.__held[`+cdp.Quote(books[1].ID)+`] = "ready"`, nil)

			// Too big first: the app says how much room it has, and nothing is added.
			tab.Eval(`window.__room = 1000`, nil)
			dlallOpen(t, tab)
			if got := dlallText(t, tab, "title"); got != "Not enough room" {
				t.Errorf("title %q, want Not enough room", got)
			}
			var added int
			tab.Eval(`window.__sent.filter((m) => m.startsWith("dl add ")).length`, &added)
			if added != 0 {
				t.Errorf("%d books added although they do not fit", added)
			}
			tab.Eval(`document.querySelector("[data-ts-dlall-cancel]").click()`, nil)

			tab.Eval(`window.__room = 1e12`, nil)
			dlallOpen(t, tab)
			if got := dlallText(t, tab, "title"); got != "Download 3 books?" {
				t.Errorf("confirm title %q", got)
			}
			tab.Eval(`document.querySelector("[data-ts-dlall-go]").click()`, nil)
			tab.WaitFor(`document.querySelector("[data-ts-dlall-status]").textContent === "3 books are ready offline."`, 20*time.Second)
			var sent string
			tab.Eval(`window.__sent.filter((m) => m.startsWith("dl add ")).join("|")`, &sent)
			want := strings.Join([]string{"dl add " + books[0].ID, "dl add " + books[2].ID, "dl add " + books[3].ID}, "|")
			if sent != want {
				t.Errorf("words sent %q, want %q", sent, want)
			}

			// A new batch of three books the app holds none of, slow enough to stop.
			tab.Eval(`window.__held = {}; window.__readyAfter = 3000;`, nil)
			dlallOpen(t, tab)
			tab.Eval(`document.querySelector("[data-ts-dlall-go]").click()`, nil)
			tab.WaitFor(`document.querySelector("[data-ts-dlall-status]").textContent.startsWith("Downloading ")`, 10*time.Second)
			tab.WaitFor(`window.__held[`+cdp.Quote(books[1].ID)+`] === "queued"`, 10*time.Second)
			tab.Eval(`document.querySelector("[data-ts-dlall-stop]").click()`, nil)
			tab.WaitFor(`document.querySelector("[data-ts-dlall-status]").textContent.startsWith("Stopped.")`, 5*time.Second)
			var removed int
			tab.Eval(`window.__sent.filter((m) => m.startsWith("dl remove ")).length`, &removed)
			if removed < 1 {
				t.Error("Stop sent no dl remove")
			}
			var over bool
			tab.Eval(`document.documentElement.scrollWidth > innerWidth`, &over)
			if over {
				t.Error("horizontal overflow")
			}
		})
	}
}
