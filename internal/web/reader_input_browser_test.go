//go:build dev

package web

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
)

// Tap zones, keys, swipe and the time left, in Chromium (RD2). Needs TENDSHELF_TEST_BROWSER.

func pgPageIs(tab interface {
	WaitFor(string, time.Duration)
}, n int) {
	tab.WaitFor(fmt.Sprintf(`(() => {`+pgJS+` return pgPage() === %d; })()`, n), 5*time.Second)
}

func TestReaderInputTaps(t *testing.T) {
	b := chromium(t)
	chs := []ebooktest.EPUBChapter{
		{Title: "Title Page", EpubType: "titlepage", Paras: []string{"Taps"}},
		{Title: "One", EpubType: "chapter", Raw: `<p>See <a href="ch3.xhtml#t">the other chapter</a> now. ` + pgProse("a", 1)[0] + `</p>` + rawParas(pgProse("b", 40))},
		{Title: "Two", EpubType: "chapter", Raw: `<p id="t">` + pgProse("t", 1)[0] + `</p>`},
	}
	l := pgSeedEPUB(t, chs, nil)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	for _, vp := range pgViewports[:1] { // taps are a touch matter: 390 px
		t.Run(vp.name, func(t *testing.T) {
			tab := pgOpen(t, b, srv, l, vp.w, vp.h, vp.mobile, "ch=2")
			w, mid := float64(vp.w), float64(vp.h)/2
			// Right quarter: next page. Left quarter: back. Middle: the chrome hides and shows.
			pgTouch(t, tab, w*0.92, mid, w*0.92, mid)
			pgPageIs(tab, 1)
			pgTouch(t, tab, w*0.08, mid, w*0.08, mid)
			pgPageIs(tab, 0)
			pgTouch(t, tab, w*0.5, mid, w*0.5, mid)
			tab.WaitFor(`document.querySelector(".ts-readerpage").dataset.chrome === "off"`, 3*time.Second)
			pgTouch(t, tab, w*0.5, mid, w*0.5, mid)
			tab.WaitFor(`document.querySelector(".ts-readerpage").dataset.chrome === "on"`, 3*time.Second)

			// A tap on a link follows the link and turns nothing.
			var rect struct{ X, Y float64 }
			tab.Eval(`(() => { const r = document.querySelector("a.ts-reader__link").getBoundingClientRect(); return {X: r.x + r.width / 2, Y: r.y + r.height / 2}; })()`, &rect)
			pgTouch(t, tab, rect.X, rect.Y, rect.X, rect.Y)
			tab.WaitFor(`location.search.includes("ch=3") || location.search.includes("at=c3")`, 5*time.Second)
		})
	}

	// Swapped sides: the right quarter goes back.
	rsetPost(t, l, l.owner, true, "tap_swap", "1")
	tab := pgOpen(t, b, srv, l, 390, 844, true, "ch=2")
	pgNext(t, tab)
	pgPageIs(tab, 1)
	pgTouch(t, tab, 390*0.92, 422, 390*0.92, 422)
	pgPageIs(tab, 0)
	pgTouch(t, tab, 390*0.08, 422, 390*0.08, 422)
	pgPageIs(tab, 1)
	// A selection keeps a tap from turning the page.
	tab.Eval(`getSelection().selectAllChildren(document.querySelector(".ts-s"))`, nil)
	pgTouch(t, tab, 390*0.92, 422, 390*0.92, 422)
	time.Sleep(300 * time.Millisecond)
	var page int
	pgEval(tab, `pgPage()`, &page)
	if page != 1 {
		t.Errorf("a tap with a selection turned the page to %d", page)
	}
}

// rawParas turns paragraphs into markup.
func rawParas(paras []string) string {
	var sb strings.Builder
	for _, p := range paras {
		sb.WriteString("<p>" + p + "</p>")
	}
	return sb.String()
}

func TestReaderInputKeys(t *testing.T) {
	b := chromium(t)
	l := pgSeed(t, 2, 40)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	tab := pgOpen(t, b, srv, l, 390, 844, true, "ch=2")
	// The player (and the rest of the page) listens on document: it must not see the reader's keys.
	tab.Eval(`window.__keys = 0; document.addEventListener("keydown", () => window.__keys++)`, nil)
	pgNext(t, tab)
	pgPageIs(tab, 1)
	pgKey(t, tab, "PageDown", "PageDown", 34, "", 0)
	pgPageIs(tab, 2)
	pgKey(t, tab, " ", "Space", 32, " ", 0)
	pgPageIs(tab, 3)
	pgKey(t, tab, " ", "Space", 32, " ", 8) // Shift+Space goes back
	pgPageIs(tab, 2)
	pgKey(t, tab, "PageUp", "PageUp", 33, "", 0)
	pgPageIs(tab, 1)
	pgPrev(t, tab)
	pgPageIs(tab, 0)
	var seen int
	tab.Eval(`window.__keys`, &seen)
	if seen != 0 {
		t.Errorf("document saw %d reader keys; the player would have acted on them", seen)
	}
	// ] goes to the next chapter, [ back.
	pgKey(t, tab, "]", "BracketRight", 221, "]", 0)
	tab.WaitFor(`location.search.includes("ch=3")`, 5*time.Second)

	// Focus in a field leaves the keys alone: the field gets them, the page stays.
	tab = pgOpen(t, b, srv, l, 390, 844, true, "ch=2")
	tab.Eval(`window.__keys = 0; document.addEventListener("keydown", () => window.__keys++);
		const input = document.body.appendChild(document.createElement("input")); input.id = "pg-field"; input.focus()`, nil)
	pgNext(t, tab)
	pgKey(t, tab, " ", "Space", 32, " ", 0)
	time.Sleep(300 * time.Millisecond)
	var page int
	pgEval(tab, `pgPage()`, &page)
	tab.Eval(`window.__keys`, &seen)
	if page != 0 || seen != 2 {
		t.Errorf("with a field focused: page %d, document saw %d keys (want 0 and 2)", page, seen)
	}
}

func TestReaderInputSwipe(t *testing.T) {
	b := chromium(t)
	l := pgSeed(t, 2, 40)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	tab := pgOpen(t, b, srv, l, 390, 844, true, "ch=2")
	pgTouch(t, tab, 330, 400, 90, 410) // left: next
	pgPageIs(tab, 1)
	pgTouch(t, tab, 90, 400, 330, 395) // right: back
	pgPageIs(tab, 0)
	pgTouch(t, tab, 200, 300, 160, 340) // too short: neither a swipe nor a tap
	time.Sleep(300 * time.Millisecond)
	var page int
	pgEval(tab, `pgPage()`, &page)
	if page != 0 {
		t.Errorf("a short drag turned the page to %d", page)
	}
	pgTouch(t, tab, 200, 200, 210, 500) // mostly vertical: not a swipe
	time.Sleep(300 * time.Millisecond)
	pgEval(tab, `pgPage()`, &page)
	if page != 0 {
		t.Errorf("a vertical drag turned the page to %d", page)
	}
}

func TestReaderInputTimeLeft(t *testing.T) {
	b := chromium(t)
	l := pgSeed(t, 2, 40)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	minutes := regexp.MustCompile(`(\d+) min left in this chapter`)
	chapterMinutes := func(text string) int {
		m := minutes.FindStringSubmatch(text)
		if m == nil {
			t.Fatalf("no chapter time in %q", text)
		}
		n, _ := strconv.Atoi(m[1])
		return n
	}
	read := func() (string, string) {
		tab := pgOpen(t, b, srv, l, 390, 844, true, "ch=2")
		tab.WaitFor(`document.getElementById("reader-left").textContent !== ""`, 5*time.Second)
		var left, page string
		tab.Eval(`document.getElementById("reader-left").textContent`, &left)
		tab.Eval(`document.getElementById("reader-page").textContent`, &page)
		return left, page
	}
	left, page := read()
	if !strings.HasPrefix(left, "About ") || !strings.Contains(left, "left in the book") {
		t.Errorf("default speed: %q", left)
	}
	if !regexp.MustCompile(`^p\. \d+ of \d+$`).MatchString(page) {
		t.Errorf("page line %q", page)
	}
	slow := chapterMinutes(left)
	if slow%5 != 0 || slow == 0 {
		t.Errorf("the time %d min is not in steps of five", slow)
	}
	// Three sessions of a minute at twice the default speed teach the real one.
	for i := 0; i < 3; i++ {
		if err := l.a.srv.progress.AddReadingTime(context.Background(), l.ownerID, l.book.ID, "timeleftdevice01", 2000, 60_000, time.Now().Add(time.Duration(i-5)*2*time.Hour).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	left, _ = read()
	if strings.HasPrefix(left, "About ") {
		t.Errorf("a learned speed still says About: %q", left)
	}
	if fast := chapterMinutes(strings.TrimSpace(left)); fast >= slow {
		t.Errorf("twice the speed should shorten %d min, got %d", slow, fast)
	}
}
