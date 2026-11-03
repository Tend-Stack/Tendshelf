//go:build dev

package web

import (
	"context"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/wav"
)

// Up next (Q3): autoplay when a book ends, the finished mark that survives starting another book,
// and the three-tab arrow keys.

// shortBook adds a one-file book of a few seconds and returns it with its file, as the fixture's own
// book is (WAV, so the browser plays it without a conversion).
func (e *playerEnv) shortBook(t *testing.T, title string) (library.Book, library.AudioFile) {
	t.Helper()
	book := librarytest.AddBook(t, e.a.lib, e.folder, librarytest.BookSpec{Title: title, Authors: []string{"Imogen Hale"}, DurationMs: 3000, Files: 1})
	files, err := e.a.lib.AudioFiles(context.Background(), book.ID)
	if err != nil || len(files) != 1 {
		t.Fatalf("audio files of %s = %d, %v", title, len(files), err)
	}
	if err := wav.Write(files[0].Path, 3000, 660); err != nil {
		t.Fatal(err)
	}
	streamExec(t, e.a, `UPDATE audio_files SET container='wav', codec='pcm_s16le', duration_ms=3000, offset_ms=0 WHERE id=?`, files[0].ID)
	files, _ = e.a.lib.AudioFiles(context.Background(), book.ID)
	return book, files[0]
}

// finishedAt is the owner's finished mark for the book (0 when none).
func (e *playerEnv) finishedAt(t *testing.T, bookID string) int64 {
	t.Helper()
	var at *int64
	err := e.a.st.Reader().QueryRow(`SELECT finished_at FROM progress WHERE user_id = ? AND book_id = ?`, e.ownerID, bookID).Scan(&at)
	if err != nil || at == nil {
		return 0
	}
	return *at
}

func (e *playerEnv) queueCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := e.a.st.Reader().QueryRow(`SELECT count(*) FROM queue_items WHERE user_id = ?`, e.ownerID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestQueueAutoplayBrowser(t *testing.T) {
	e := playerApp(t)
	b, bf := e.shortBook(t, "Second Lamp")
	c, _ := e.shortBook(t, "Third Lamp")
	d, _ := e.shortBook(t, "Fourth Lamp")
	tab := playerOpen(t, chromium(t), e.URL, 390, 800)
	tab.Eval(`window.__tsMarker = "same page"`, nil)

	waitFinished := func(id string) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for e.finishedAt(t, id) == 0 && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if e.finishedAt(t, id) == 0 {
			t.Fatalf("book %s was not marked finished", id)
		}
	}

	// Empty queue: A ends near its end and the bar stays on A, paused, as before.
	e.playerInject(tab, e.book.ID, 178_000, 1, "")
	playerClickSel(t, tab, "#play-link")
	tab.WaitFor(`window.tsPlayer.state().playing`, 8*time.Second)
	waitFinished(e.book.ID)
	time.Sleep(1500 * time.Millisecond)
	if s := playerStateOf(tab); s.BookID != e.book.ID || s.Playing {
		t.Fatalf("empty queue: state %+v, want paused on the first book", s)
	}
	finishedA := e.finishedAt(t, e.book.ID)

	// Starting another book after one ended must not clear the ended book's mark (the fix in save()).
	// C and D wait in Up next, so B's end hands over to them.
	streamExec(t, e.a, `INSERT INTO queue_items(user_id, book_id, position, added_at) VALUES (?, ?, 1, 1), (?, ?, 2, 2)`, e.ownerID, c.ID, e.ownerID, d.ID)
	playerDropLink(t, tab)
	eb := *e
	eb.files = []library.AudioFile{bf}
	eb.playerInject(tab, b.ID, 0, 0, "")
	playerClickSel(t, tab, "#play-link")
	tab.WaitFor(`window.tsPlayer.state().bookId === `+quoteJS(b.ID), 8*time.Second)
	if got := e.finishedAt(t, e.book.ID); got != finishedA {
		t.Errorf("first book finished_at = %d after starting another, was %d", got, finishedA)
	}

	// B ends: C starts by itself, then D, then it stops. No page load in between.
	tab.WaitFor(`window.tsPlayer.state().bookId === `+quoteJS(c.ID)+` && window.tsPlayer.state().playing`, 12*time.Second)
	tab.WaitFor(`window.tsPlayer.state().bookId === `+quoteJS(d.ID)+` && window.tsPlayer.state().playing`, 12*time.Second)
	waitFinished(b.ID)
	waitFinished(c.ID)
	waitFinished(d.ID)
	time.Sleep(1500 * time.Millisecond)
	if s := playerStateOf(tab); s.BookID != d.ID || s.Playing {
		t.Errorf("end of the queue: state %+v, want paused on the last book", s)
	}
	for _, id := range []string{e.book.ID, b.ID, c.ID, d.ID} {
		if e.finishedAt(t, id) == 0 {
			t.Errorf("book %s lost its finished mark", id)
		}
	}
	if n := e.queueCount(t); n != 0 {
		t.Errorf("Up next has %d rows, want 0", n)
	}
	var marker string
	tab.Eval(`window.__tsMarker`, &marker)
	if marker != "same page" {
		t.Error("the page reloaded")
	}
}

// quoteJS quotes an id for a script expression (ids are plain letters and digits).
func quoteJS(s string) string { return `"` + s + `"` }

func TestNowPanelThreeTabKeys(t *testing.T) {
	e := nowPanelEnv(t)
	tab := nowPanelOpen(t, e, 1440, 900)
	// About, Bookmarks and Up next: the arrow keys wrap around all three.
	var tabs int
	tab.Eval(`(() => { const list = document.querySelector(".ts-np__tablist"); list.firstElementChild.focus(); return list.children.length; })()`, &tabs)
	if tabs != 3 {
		t.Fatalf("%d tabs, want 3", tabs)
	}
	active := func() int {
		var i int
		tab.Eval(`[...document.querySelector(".ts-np__tablist").children].indexOf(document.activeElement)`, &i)
		return i
	}
	for i, step := range []struct {
		key  string
		want int
	}{{"ArrowRight", 1}, {"ArrowRight", 2}, {"ArrowRight", 0}, {"ArrowLeft", 2}, {"ArrowLeft", 1}, {"ArrowLeft", 0}} {
		playerKey(t, tab, step.key)
		time.Sleep(150 * time.Millisecond)
		if got := active(); got != step.want {
			t.Fatalf("step %d: %s focused tab %d, want %d", i, step.key, got, step.want)
		}
	}
}
