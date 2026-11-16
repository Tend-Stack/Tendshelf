//go:build dev

package web

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestRemoveAnyBrowser opens the confirm page of a hand-added book in Chromium at a phone and a desktop width: the
// list is readable without sideways scrolling, every target is at least 44 px, the box has to be ticked before the
// post goes out, and after the post the status fragment reaches "held" (decision 0045).
func TestRemoveAnyBrowser(t *testing.T) {
	b := chromium(t)
	l := newRemoveLab(t)
	l.startRunner(t)
	names := make([]string, 0, 12)
	for i := 1; i <= 12; i++ {
		names = append(names, "Chapter "+strings.Repeat("long name ", 6)+string(rune('a'+i))+".mp3")
	}
	l.handBook(t, "Phone Book", names...)
	l.handBook(t, "Desktop Book", names...)
	l.scanAll(t)
	srv := httptest.NewServer(l.a.signedIn(t))
	t.Cleanup(srv.Close)

	for _, c := range []struct {
		name, dir string
		w, h      int
		mobile    bool
	}{{"mobile-390", "Phone Book", 390, 844, true}, {"desktop-1440", "Desktop Book", 1440, 900, false}} {
		t.Run(c.name, func(t *testing.T) {
			id := l.bookAt(t, c.dir)
			tab := b.NewTab(c.w, c.h, c.mobile, false)
			tab.Navigate(srv.URL + "/books/" + id + "/remove")
			tab.WaitFor(`document.querySelector(".ts-remove-any__form") !== null`, 10*time.Second)

			var wide bool
			tab.Eval(`(() => { const m = document.querySelector("#main") || document.documentElement; return m.scrollWidth > m.clientWidth + 1 || document.documentElement.scrollWidth > innerWidth + 1 })()`, &wide)
			if wide {
				t.Error("the page scrolls sideways")
			}
			var small string
			tab.Eval(`[...document.querySelectorAll(".ts-remove-any button, .ts-remove-any a.ts-btn, .ts-remove-any__check")].filter(e => e.getBoundingClientRect().height < 43.5).map(e => e.textContent.trim()).join("|")`, &small)
			if small != "" {
				t.Errorf("targets under 44 px: %q", small)
			}
			var list string
			tab.Eval(`document.querySelector(".ts-remove-any__files").textContent`, &list)
			if !strings.Contains(list, "Chapter") || !strings.Contains(list, ".mp3") {
				t.Errorf("the list is %q", list)
			}

			// Without the tick the browser holds the post back: the page stays and nothing is removed.
			tab.Eval(`document.querySelector("button[name=mode][value=files]").click()`, nil)
			time.Sleep(400 * time.Millisecond)
			var path string
			var invalid bool
			tab.Eval(`location.pathname`, &path)
			tab.Eval(`document.querySelector("input[name=understand]").validity.valueMissing`, &invalid)
			if !strings.HasSuffix(path, "/remove") || !invalid {
				t.Errorf("the post went out without the tick: %s (missing %v)", path, invalid)
			}
			if n := l.a.count(t, `SELECT count(*) FROM removals WHERE book_id = ?`, id); n != 0 {
				t.Errorf("a removal was started: %d", n)
			}

			tab.Eval(`document.querySelector("input[name=understand]").click()`, nil)
			tab.Eval(`document.querySelector("button[name=mode][value=files]").click()`, nil)
			tab.WaitFor(`document.querySelector("[data-state=held]") !== null`, 30*time.Second)
			var text string
			tab.Eval(`document.querySelector(".ts-remove-any__status").textContent.replace(/\s+/g, " ")`, &text)
			if !strings.Contains(text, "Removed. The files are kept until") || !strings.Contains(text, "Put back") {
				t.Errorf("status = %q", text)
			}
			if left, _ := filepath.Glob(filepath.Join(l.dir, c.dir, "*.mp3")); len(left) != 0 {
				t.Errorf("files are still in the book's folder: %v", left)
			}
		})
	}
}
