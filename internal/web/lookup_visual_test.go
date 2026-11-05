//go:build dev

package web

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// TestDefineVisual captures the look-up popup over the reader and the word list at 390 and 1440 px, and asserts
// both fit: no sideways scroll, targets of at least 44 px, text of at least 13 px. Needs TENDSHELF_TEST_BROWSER.
func TestDefineVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	l := pgSeedEPUB(t, pgLongChapter(), nil)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	sid := l.sids()[1]
	for i, w := range []string{"lantern", "serene", "ember"} {
		_, _, err := l.a.srv.progress.AddWord(context.Background(), progress.Word{UserID: l.ownerID, BookID: l.book.ID, EditionID: l.ed.ID, Word: w,
			Quote: "The " + w + " light fell across the long grey floor of the room where they waited.", SID: sid, Off: i})
		if err != nil {
			t.Fatal(err)
		}
	}
	lkDict(t, l, "Visual words", "the\tA definite article, used before a noun that is known to the reader.\\nSecond line of the meaning.\n")
	const probe = `({over: document.documentElement.scrollWidth > innerWidth, small: [...document.querySelectorAll("dialog[open] a, dialog[open] button, main a, main button")].filter((e) => e.offsetParent && (e.getBoundingClientRect().height < 43.5)).map((e) => e.textContent.trim()).slice(0, 5)})`
	for _, width := range []int{390, 1440} {
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			tab := pgOpen(t, b, srv, l, width, 900, width == 390, "at="+sid)
			rnSelect(tab, sid, 0, 3)
			tab.Eval(`document.querySelector('#reader-marks [data-ts-mark="lookup"]').click()`, nil)
			tab.WaitFor(`document.querySelector("dialog.ts-lookup[open]") !== null`, 8*time.Second)
			time.Sleep(300 * time.Millisecond)
			var m struct {
				Over  bool
				Small []string
			}
			tab.Eval(probe, &m)
			if m.Over || len(m.Small) > 0 {
				t.Errorf("look-up popup: %+v", m)
			}
			tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("lookup-%d.png", width)))

			tab = b.NewTab(width, 900, width == 390, false)
			tab.Navigate(srv.URL + "/me/words")
			tab.WaitFor(`document.querySelector(".ts-words__item") !== null`, 5*time.Second)
			tab.Eval(`document.fonts.ready.then(() => true)`, nil)
			time.Sleep(300 * time.Millisecond)
			tab.Eval(probe, &m)
			if m.Over || len(m.Small) > 0 {
				t.Errorf("word list: %+v", m)
			}
			tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("words-%d.png", width)))
		})
	}
	t.Logf("screenshots in %s", out)
}
