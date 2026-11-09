//go:build dev

package web

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
	"github.com/Tend-Stack/tendshelf/internal/testutil/webdriver"
)

// Pages by default, the bounded page, page numbers and the bar (decision 0036). Needs TENDSHELF_TEST_BROWSER.

// rpBook is a title page, a dedication of very short lines, and chapters of prose.
func rpBook(t *testing.T, chapters, paras int) *rdrLab {
	t.Helper()
	chs := []ebooktest.EPUBChapter{
		{Title: "Title Page", EpubType: "titlepage", Paras: []string{"The Pager"}},
		{Title: "Dedication", EpubType: "dedication", Raw: `<p>For my mother,</p><p>who read aloud.</p>`},
	}
	for i := 1; i <= chapters; i++ {
		chs = append(chs, ebooktest.EPUBChapter{Title: fmt.Sprintf("Chapter %d", i), EpubType: "chapter", Paras: pgProse(fmt.Sprint(i), paras)})
	}
	return pgSeedEPUB(t, chs, nil)
}

// rpVar reads one CSS variable of the reader root as a number of pixels (or a plain number).
func rpVar(tab *cdp.Tab, name string) float64 {
	var v float64
	tab.Eval(fmt.Sprintf(`parseFloat(getComputedStyle(document.getElementById("reader")).getPropertyValue(%q))`, name), &v)
	return v
}

func rpPageLine(tab *cdp.Tab) string {
	var s string
	tab.Eval(`document.getElementById("reader-page").textContent`, &s)
	return s
}

// TestPagesAtEveryWidth: pages are the default from a phone to a wide window; a stored scroll stays scroll.
func TestPagesAtEveryWidth(t *testing.T) {
	b := chromium(t)
	l := rpBook(t, 2, 40)
	srv := pgServe(t, l)
	for _, w := range []int{390, 768, 1024, 1440, 2000} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			tab := pgOpen(t, b, srv, l, w, 900, w == 390, "ch=3")
			var pages bool
			tab.Eval(`document.getElementById("reader").classList.contains("ts-reader--pages")`, &pages)
			if !pages {
				t.Errorf("no pages at %d px", w)
			}
		})
	}
	pgFlow(t, l, "scroll")
	tab := pgOpen(t, b, srv, l, 1440, 900, false, "ch=3")
	var scroll bool
	tab.Eval(`document.getElementById("reader").classList.contains("ts-reader--scroll")`, &scroll)
	if !scroll {
		t.Error("a stored scroll was turned into pages")
	}
	pgNext(t, tab)
	tab.WaitFor(`document.getElementById("reader").scrollTop > 300`, 5*time.Second)
}

// TestPagesSpreadBounds: at 2000 x 1100 two columns, each under 34em, the sheet centred, the height a whole number of lines.
func TestPagesSpreadBounds(t *testing.T) {
	b := chromium(t)
	l := rpBook(t, 2, 40)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	tab := pgOpen(t, b, srv, l, 2000, 1100, false, "ch=3")
	var m struct {
		Cols, Col, Em, Line, PageH, Left, Right, Width float64
	}
	tab.Eval(`(() => {
		const root = document.getElementById("reader"), css = getComputedStyle(root), text = root.querySelector(".ts-reader__text"), tcss = getComputedStyle(text);
		const frame = root.querySelector(".ts-reader__col").getBoundingClientRect(), rect = root.getBoundingClientRect();
		return { cols: parseFloat(css.getPropertyValue("--ts-cols")), col: parseFloat(css.getPropertyValue("--ts-col")), em: parseFloat(tcss.fontSize),
			line: parseFloat(tcss.lineHeight), pageH: parseFloat(css.getPropertyValue("--ts-page-h")), left: frame.left - rect.left, right: rect.right - frame.right, width: frame.width };
	})()`, &m)
	if m.Cols != 2 {
		t.Fatalf("%v columns at 2000 px", m.Cols)
	}
	if m.Col >= 34*m.Em || m.Col < 24*m.Em {
		t.Errorf("a column is %.0f px = %.1f em, want 24 to 34", m.Col, m.Col/m.Em)
	}
	if d := m.Left - m.Right; d > 1 || d < -1 {
		t.Errorf("the sheet is off centre: %.1f px left, %.1f px right", m.Left, m.Right)
	}
	if lines := m.PageH / m.Line; math.Abs(lines-math.Round(lines)) > 0.02 {
		t.Errorf("the page is %.2f lines tall", lines)
	}
	if m.PageH > 1.6*m.Width {
		t.Errorf("the page is %.0f tall for a sheet %.0f wide", m.PageH, m.Width)
	}
}

// TestPagesNumbersStep: forward the number goes up by one a page (two on a spread), back returns the same numbers, a part
// boundary adds one, and no page number is ever part of the saved place.
func TestPagesNumbersStep(t *testing.T) {
	b := chromium(t)
	l := pgSeedEPUB(t, pgLongChapter(), nil)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	for _, vp := range []struct {
		w, h   int
		mobile bool
		step   int
	}{{390, 844, true, 1}, {2000, 1100, false, 2}} {
		t.Run(fmt.Sprint(vp.w), func(t *testing.T) {
			tab := pgOpen(t, b, srv, l, vp.w, vp.h, vp.mobile, "ch=2")
			first := func() int {
				var n int
				tab.Eval(`parseInt(document.getElementById("reader-page").textContent.match(/\d+/)[0], 10)`, &n)
				return n
			}
			var seen []int
			seen = append(seen, first())
			for i := 0; i < 10; i++ {
				pgNext(t, tab)
				want := seen[len(seen)-1] + vp.step
				tab.WaitFor(fmt.Sprintf(`parseInt(document.getElementById("reader-page").textContent.match(/\d+/)[0], 10) === %d`, want), 5*time.Second)
				seen = append(seen, want)
			}
			for i := len(seen) - 2; i >= 0; i-- {
				pgPrev(t, tab)
				tab.WaitFor(fmt.Sprintf(`parseInt(document.getElementById("reader-page").textContent.match(/\d+/)[0], 10) === %d`, seen[i]), 5*time.Second)
			}
			// Across a part: walk to the end of the first part and one turn past it.
			var left int
			for i := 0; i < 400; i++ {
				var atEdge bool
				tab.Eval(`document.querySelector("section[data-part]").dataset.part === "1" && Math.round(document.querySelector("section[data-part]").scrollLeft) >= document.querySelector("section[data-part]").scrollWidth - document.querySelector("section[data-part]").clientWidth - 2`, &atEdge)
				if atEdge {
					break
				}
				pgNext(t, tab)
				time.Sleep(30 * time.Millisecond)
			}
			left = first()
			pgNext(t, tab)
			tab.WaitFor(`document.querySelector("section[data-part]").dataset.part === "2"`, 5*time.Second)
			if got := first(); got != left+vp.step && got != left+1 {
				t.Errorf("across a part the number went %d to %d", left, got)
			}
			// The saved place has no page number in it.
			var body string
			tab.Eval(`document.getElementById("reader").dataset.hereSid`, &body)
			if body == "" || strings.Contains(strings.ToLower(body), "page") {
				t.Errorf("here = %q", body)
			}
		})
	}
}

// TestPagesNumbersAbout: opening mid-book says about, and a reload says the same.
func TestPagesNumbersAbout(t *testing.T) {
	b := chromium(t)
	l := pgSeedEPUB(t, pgLongChapter(), nil)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	tab := pgOpen(t, b, srv, l, 390, 844, true, "ch=2&part=2")
	line := rpPageLine(tab)
	if !strings.Contains(line, "about") {
		t.Fatalf("the page line is %q", line)
	}
	tab.Navigate(srv.URL + l.readPath("ch=2&part=2"))
	tab.WaitFor(`document.querySelector("#reader.ts-reader--pages") !== null && document.getElementById("reader-page").textContent !== ""`, 8*time.Second)
	if again := rpPageLine(tab); again != line {
		t.Errorf("a reload changed %q to %q", line, again)
	}
}

// TestChromeNoRelay: hiding and showing the bar never lays the pages out again, on a phone and a wide window.
func TestChromeNoRelay(t *testing.T) {
	b := chromium(t)
	l := rpBook(t, 2, 40)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	for _, vp := range pgViewports {
		t.Run(vp.name, func(t *testing.T) {
			tab := pgOpen(t, b, srv, l, vp.w, vp.h, vp.mobile, "ch=3")
			pgNext(t, tab)
			pgWaitFor(tab, `pgPage() === 1`)
			h, left := rpVar(tab, "--ts-page-h"), 0.0
			tab.Eval(`document.querySelector("section[data-part]").scrollLeft`, &left)
			for _, state := range []string{"on", "off", "on"} {
				tab.Eval(fmt.Sprintf(`document.querySelector(".ts-readerpage").dataset.chrome = %q`, state), nil)
				time.Sleep(250 * time.Millisecond)
				var now float64
				tab.Eval(`document.querySelector("section[data-part]").scrollLeft`, &now)
				if rpVar(tab, "--ts-page-h") != h || now != left {
					t.Errorf("chrome %s: page height %v scrollLeft %v, was %v %v", state, rpVar(tab, "--ts-page-h"), now, h, left)
				}
			}
		})
	}
}

// TestPagesResumeUnderBar: on a phone the saved place opens with the bar hidden and its text on screen.
func TestPagesResumeUnderBar(t *testing.T) {
	b := chromium(t)
	l := rpBook(t, 2, 40)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	var sid string
	pgOpen(t, b, srv, l, 390, 844, true, "ch=3").Eval(`document.querySelectorAll(".ts-s")[120].dataset.sid`, &sid)
	tab := pgOpen(t, b, srv, l, 390, 844, true, "ch=3&at="+sid)
	var chrome string
	tab.Eval(`document.querySelector(".ts-readerpage").dataset.chrome || ""`, &chrome)
	if chrome != "off" {
		t.Errorf("chrome = %q on a phone, want off", chrome)
	}
	var ok bool
	pgEval(tab, fmt.Sprintf(`(() => { const r = document.querySelector('.ts-s[data-sid=%q]').getClientRects()[0]; const bar = document.querySelector(".ts-readerbar").getBoundingClientRect();
		return pgInView(document.querySelector('.ts-s[data-sid=%q]')) && r.top > 0 && r.bottom <= innerHeight; })()`, sid, sid), &ok)
	if !ok {
		t.Error("the place is not on screen")
	}
}

// TestShortPartCentred: a part of a few short lines sits in a normal page frame, a little below the top edge.
func TestShortPartCentred(t *testing.T) {
	b := chromium(t)
	l := rpBook(t, 1, 10)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	for _, w := range []int{390, 2000} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			tab := pgOpen(t, b, srv, l, w, 1100, w == 390, "ch=2")
			var m struct {
				Short       bool
				Top, Height float64
				FrameW      float64
			}
			tab.Eval(`(() => { const s = document.querySelector("section[data-part]"), p = s.querySelector("p"); return { short: s.classList.contains("ts-reader__short"),
				top: p.getBoundingClientRect().top - s.getBoundingClientRect().top, height: s.getBoundingClientRect().height,
				frameW: document.querySelector(".ts-reader__col").getBoundingClientRect().width }; })()`, &m)
			if !m.Short || m.Top < 0.15*m.Height {
				t.Errorf("short = %v, first line %.0f px down a page of %.0f", m.Short, m.Top, m.Height)
			}
			if w == 2000 && m.FrameW > 1500 {
				t.Errorf("the sheet is %.0f px wide", m.FrameW)
			}
		})
	}
}

// TestSheetScrimCoversWindow: a sheet's scrim runs to the window's right edge at 1440 and 2000, with no strip of page beside it.
func TestSheetScrimCoversWindow(t *testing.T) {
	b := chromium(t)
	l := rpBook(t, 2, 20)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	for _, w := range []int{1440, 2000} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			tab := pgOpen(t, b, srv, l, w, 900, false, "ch=3&sheet=contents")
			tab.WaitFor(`document.querySelector("#sheet .ts-scrim") !== null`, 8*time.Second)
			time.Sleep(450 * time.Millisecond)
			var m struct{ Right, Page, Inner float64 }
			tab.Eval(`({ right: document.querySelector("#sheet .ts-scrim").getBoundingClientRect().right, page: document.querySelector(".ts-readerpage").getBoundingClientRect().right, inner: innerWidth })`, &m)
			if m.Right < m.Inner-0.5 || m.Page < m.Inner-0.5 {
				t.Errorf("the scrim ends at %.0f and the reader at %.0f of %.0f px", m.Right, m.Page, m.Inner)
			}
		})
	}
}

// TestPagesCapture writes the evidence pictures when TENDSHELF_PAGES_OUT names a folder.
func TestPagesCapture(t *testing.T) {
	out := os.Getenv("TENDSHELF_PAGES_OUT")
	if out == "" {
		t.Skip("TENDSHELF_PAGES_OUT not set")
	}
	b := chromium(t)
	l := rpBook(t, 3, 60)
	pgFlow(t, l, "pages")
	srv := brServe(t, l, l.owner) // the theme comes from ?theme=
	shot := func(name string, w, h int, theme, query string, prepare func(tab *cdp.Tab)) {
		t.Run(name, func(t *testing.T) {
			tab := b.NewTab(w, h, w == 390, true)
			tab.Navigate(srv + l.readPath(query+"&theme="+theme))
			tab.WaitFor(`document.querySelector("#reader.ts-reader--pages") !== null && document.fonts.status === "loaded"`, 10*time.Second)
			time.Sleep(300 * time.Millisecond)
			if prepare != nil {
				prepare(tab)
				time.Sleep(300 * time.Millisecond)
			}
			tab.Screenshot(filepath.Join(out, name+".png"))
		})
	}
	turn := func(n int) func(tab *cdp.Tab) {
		return func(tab *cdp.Tab) {
			for i := 0; i < n; i++ {
				pgNext(t, tab)
				time.Sleep(150 * time.Millisecond)
			}
		}
	}
	for _, vp := range []struct{ w, h int }{{390, 844}, {768, 1024}, {1024, 768}, {1440, 900}, {2000, 1100}} {
		shot(fmt.Sprintf("chapter-%d-dark", vp.w), vp.w, vp.h, "dark", "ch=3", turn(2))
	}
	for _, th := range []string{"light", "sepia", "contrast"} {
		shot("chapter-1440-"+th, 1440, 900, th, "ch=3", turn(2))
	}
	shot("part-opening-2000", 2000, 1100, "dark", "ch=3", nil)
	shot("contents-1440", 1440, 900, "dark", "ch=3&sheet=contents", nil)
	shot("short-part-2000", 2000, 1100, "dark", "ch=2", nil)
	shot("short-part-390", 390, 844, "dark", "ch=2", nil)
	shot("phone-bar-hidden", 390, 844, "dark", "ch=3", turn(1))
	shot("phone-bar-shown", 390, 844, "dark", "ch=3", func(tab *cdp.Tab) {
		tab.Eval(`document.querySelector(".ts-readerpage").dataset.chrome = "on"`, nil)
	})
}

// TestPagesFirefox: the page's size and numbers in Firefox. Opt-in: TENDSHELF_TEST_FIREFOX=1 (geckodriver on PATH or at /snap/bin).
func TestPagesFirefox(t *testing.T) {
	if os.Getenv("TENDSHELF_TEST_FIREFOX") == "" {
		t.Skip("set TENDSHELF_TEST_FIREFOX to 1 to run this")
	}
	driver, err := exec.LookPath("geckodriver")
	if err != nil {
		driver = "/snap/bin/geckodriver"
	}
	l := rpBook(t, 2, 40)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	s := webdriver.StartFirefox(t, driver, "", map[string]any{})
	for _, vp := range []struct{ w, h, cols int }{{2000, 1100, 2}, {500, 800, 1}} { // Firefox will not make a window narrower than 500 px
		s.SetViewport(vp.w, vp.h)
		s.Navigate(srv.URL + l.readPath("ch=3"))
		time.Sleep(1500 * time.Millisecond)
		var m struct {
			Cols, Col, Em, Left, Right, PageH, Line float64
			Words                                   string
		}
		s.Eval(`(() => { const root = document.getElementById("reader"), css = getComputedStyle(root), tcss = getComputedStyle(root.querySelector(".ts-reader__text")), f = root.querySelector(".ts-reader__col").getBoundingClientRect(), r = root.getBoundingClientRect();
			return { cols: parseFloat(css.getPropertyValue("--ts-cols")), col: parseFloat(css.getPropertyValue("--ts-col")), em: parseFloat(tcss.fontSize), left: f.left - r.left, right: r.right - f.right, pageH: parseFloat(css.getPropertyValue("--ts-page-h")), line: parseFloat(tcss.lineHeight), words: document.getElementById("reader-page").textContent }; })()`, &m)
		t.Logf("%d px: %+v", vp.w, m)
		if int(m.Cols) != vp.cols {
			t.Errorf("%d px: %v columns", vp.w, m.Cols)
		}
		if vp.w >= 1024 {
			if d := m.Left - m.Right; d > 1 || d < -1 {
				t.Errorf("off centre by %.1f px", d)
			}
			if m.Col > 34*m.Em {
				t.Errorf("column %.0f px is %.1f em", m.Col, m.Col/m.Em)
			}
		}
		if !strings.Contains(m.Words, "about") {
			t.Errorf("page line %q", m.Words)
		}
		var after string
		s.Eval(`(window.dispatchEvent(new KeyboardEvent("keydown", {key: "ArrowRight", bubbles: true})), true)`, nil)
		time.Sleep(600 * time.Millisecond)
		s.Eval(`document.getElementById("reader-page").textContent`, &after)
		if after == m.Words {
			t.Errorf("%d px: a turn left %q", vp.w, m.Words)
		}
		t.Logf("turn: %q -> %q", m.Words, after)
		if dir := os.Getenv("TENDSHELF_PAGES_OUT"); dir != "" {
			s.Screenshot(filepath.Join(dir, fmt.Sprintf("firefox-%d.png", vp.w)))
		}
	}
}
