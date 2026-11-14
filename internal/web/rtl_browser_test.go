//go:build dev

package web

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// Right-to-left text in the page reader (R4, P3). Needs TENDSHELF_TEST_BROWSER. An Arabic book: columns run
// leftward, scrollLeft is 0 at the start and goes negative, and every way of turning mirrors.

// rtlChapters is a title page and three chapters of Arabic paragraphs; extra paragraphs end the first chapter.
func rtlChapters(extra ...string) []ebooktest.EPUBChapter {
	paras := append(ebooktest.ArabicParas(200), extra...)
	chs := []ebooktest.EPUBChapter{{Title: "الغلاف", EpubType: "titlepage", Paras: []string{"كتاب"}}}
	for i := 1; i <= 3; i++ {
		chs = append(chs, ebooktest.EPUBChapter{Title: fmt.Sprintf("الفصل %d", i), EpubType: "chapter", Paras: paras})
	}
	return chs
}

func rtlSeed(t *testing.T, flow string, extra ...string) *rdrLab {
	t.Helper()
	l := pgSeedEPUB(t, rtlChapters(extra...), func(o *ebooktest.EPUBOptions) { o.Lang = "ar"; o.Title = "كتاب" })
	pgFlow(t, l, flow)
	return l
}

// rtlPage is the number of pages the part is scrolled by (negative scrollLeft counted as positive).
const rtlPage = `Math.round(-pgBox().scrollLeft / pgStep())`

func rtlLeft(tab *cdp.Tab) float64 {
	var v float64
	pgEval(tab, `pgBox().scrollLeft`, &v)
	return v
}

func rtlFirst(tab *cdp.Tab) string {
	var s string
	pgEval(tab, `pgShown()[0] || ""`, &s)
	return s
}

func TestRTLPagesTurn(t *testing.T) {
	b := chromium(t)
	l := rtlSeed(t, "pages")
	srv := pgServe(t, l)
	for _, vp := range pgViewports {
		t.Run(vp.name, func(t *testing.T) {
			tab := pgOpen(t, b, srv, l, vp.w, vp.h, vp.mobile, "ch=2")
			time.Sleep(300 * time.Millisecond)
			var dir string
			tab.Eval(`document.getElementById("reader").dir`, &dir)
			if dir != "rtl" {
				t.Fatalf("reader dir %q, want rtl", dir)
			}
			if v := rtlLeft(tab); v != 0 {
				t.Fatalf("scrollLeft at the start %v, want 0", v)
			}
			first := rtlFirst(tab)
			var step float64
			pgEval(tab, `pgStep()`, &step)

			// ArrowLeft goes forward: scrollLeft is -step, the sentence shown moves on.
			pgKey(t, tab, "ArrowLeft", "ArrowLeft", 37, "", 0)
			pgWaitFor(tab, rtlPage+` === 1`)
			if v := rtlLeft(tab); v < -step-1 || v > -step+1 {
				t.Errorf("scrollLeft after ArrowLeft %v, want about %v", v, -step)
			}
			second := rtlFirst(tab)
			if second == first || second == "" {
				t.Errorf("the page did not move on: %q then %q", first, second)
			}
			var onPage bool
			pgEval(tab, `pgOnPage(document.querySelector('.ts-s[data-sid="`+second+`"]'))`, &onPage)
			if !onPage {
				t.Error("the first sentence shown is not on the page")
			}
			// ArrowRight goes back.
			pgKey(t, tab, "ArrowRight", "ArrowRight", 39, "", 0)
			pgWaitFor(tab, rtlPage+` === 0`)
			if got := rtlFirst(tab); got != first {
				t.Errorf("back on %q, want %q", got, first)
			}

			if vp.mobile {
				// A tap at 10 % is back at the start (nothing to do), at 90 % it was back; at 10 % forward.
				pgTouch(t, tab, float64(vp.w)*0.1, 400, float64(vp.w)*0.1, 400)
				pgWaitFor(tab, rtlPage+` === 1`)
				pgTouch(t, tab, float64(vp.w)*0.9, 400, float64(vp.w)*0.9, 400)
				pgWaitFor(tab, rtlPage+` === 0`)
				// A swipe to the right goes forward, to the left back.
				pgTouch(t, tab, 100, 400, 220, 400)
				pgWaitFor(tab, rtlPage+` === 1`)
				pgTouch(t, tab, 220, 400, 100, 400)
				pgWaitFor(tab, rtlPage+` === 0`)
			} else {
				var disabled, enabled string
				start := pgOpen(t, b, srv, l, vp.w, vp.h, vp.mobile, "ch=1")
				time.Sleep(300 * time.Millisecond)
				start.Eval(`document.querySelector('.ts-edge[data-edge="-1"]').getAttribute("aria-disabled")`, &disabled)
				start.Eval(`document.querySelector('.ts-edge[data-edge="1"]').getAttribute("aria-disabled")`, &enabled)
				if disabled != "true" || enabled != "false" {
					t.Errorf("at the start the previous edge is aria-disabled=%q, the next %q", disabled, enabled)
				}
				var leftIsNext bool
				tab.Eval(`(() => { return document.querySelector(".ts-edge[data-edge=\"1\"]").getBoundingClientRect().left < document.querySelector(".ts-edge[data-edge=\"-1\"]").getBoundingClientRect().left; })()`, &leftIsNext)
				if !leftIsNext {
					t.Error("the left edge is not the next-page button")
				}
				tab.Eval(`document.querySelector('.ts-edge[data-edge="1"]').click()`, nil)
				pgWaitFor(tab, rtlPage+` === 1`)
				tab.Eval(`document.querySelector('.ts-edge[data-edge="-1"]').click()`, nil)
				pgWaitFor(tab, rtlPage+` === 0`)
			}

			// Turn on, save, and the place is further into the book; a reload and a different size find it again.
			before, _, _ := pgSaved(t, l)
			for i := 0; i < 3; i++ {
				pgKey(t, tab, "ArrowLeft", "ArrowLeft", 37, "", 0)
				time.Sleep(250 * time.Millisecond)
			}
			pgWaitFor(tab, rtlPage+` === 3`)
			sid := pgSave(t, tab, l, before)
			var onShown bool
			other := pgViewports[0]
			if vp.mobile {
				other = pgViewports[1]
			}
			again := pgOpen(t, b, srv, l, other.w, other.h, other.mobile, "")
			time.Sleep(600 * time.Millisecond)
			pgEval(again, `pgOnPage(document.querySelector('.ts-s[data-sid="`+sid+`"]'))`, &onShown)
			if !onShown {
				t.Errorf("resumed at the other size, the saved sentence %s is not on the page", sid)
			}
		})
	}
}

func TestRTLSlideOffset(t *testing.T) {
	b := chromium(t)
	l := rtlSeed(t, "pages")
	if code, _, _ := rsetPost(t, l, l.owner, true, "turn", "slide"); code != 204 {
		t.Fatalf("turn slide: %d", code)
	}
	srv := pgServe(t, l)
	tab := b.NewTab(390, 844, true, false)
	tab.Navigate(srv.URL + l.readPath("ch=2"))
	tab.WaitFor(`window.htmx !== undefined && document.querySelector("#reader.ts-reader--pages") !== null`, 8*time.Second)
	time.Sleep(400 * time.Millisecond)
	tab.Eval(`window.__kf = []; const was = Element.prototype.animate; Element.prototype.animate = function (k, o) { window.__kf.push(JSON.stringify(k)); return was.call(this, k, o); };`, nil)
	pgKey(t, tab, "ArrowLeft", "ArrowLeft", 37, "", 0)
	pgWaitFor(tab, rtlPage+` === 1`)
	var kf string
	tab.Eval(`window.__kf.join("|")`, &kf)
	if !strings.Contains(kf, "translateX(-24px)") {
		t.Errorf("a forward slide in a right-to-left book used %s, want translateX(-24px)", kf)
	}
}

func TestRTLFlipTurn(t *testing.T) {
	b := chromium(t)
	l := rtlSeed(t, "pages")
	srv := pgServe(t, l)
	out := os.Getenv("TENDSHELF_FLIP_OUT")
	if out != "" {
		_ = os.MkdirAll(out, 0o755)
	}
	for _, vp := range pgViewports {
		t.Run(vp.name, func(t *testing.T) {
			tab := flipOpen(t, b, srv, l, vp.w, vp.h, vp.mobile, "ch=2")
			var shown []string
			pgEval(tab, `pgShown()`, &shown)
			var step float64
			pgEval(tab, `pgStep()`, &step)
			pgKey(t, tab, "ArrowLeft", "ArrowLeft", 37, "", 0)
			flipWaitStage(tab)
			flipPause(tab, 0.5)
			time.Sleep(120 * time.Millisecond)
			var rtl bool
			tab.Eval(`document.querySelector(".ts-flip").classList.contains("ts-flip--rtl")`, &rtl)
			if !rtl {
				t.Error("the stage has no ts-flip--rtl")
			}
			var leaf []string
			tab.Eval(`[...document.querySelectorAll(".ts-flip__leaf .ts-flip__side:not(.ts-flip__side--back) .ts-s")].map(e => e.dataset.sid)`, &leaf)
			if len(leaf) == 0 {
				t.Fatal("the leaf's front shows no sentence")
			}
			old := map[string]bool{}
			for _, s := range shown {
				old[s] = true
			}
			for _, s := range leaf {
				if !old[s] {
					t.Errorf("the leaf's face shows %s, which was not on the old page", s)
				}
			}
			// The leaf is mirrored: its box sits at the left of the page, its text reads right to left.
			var wrapDir string
			tab.Eval(`document.querySelector(".ts-flip__face .ts-reader").dir`, &wrapDir)
			if wrapDir != "rtl" {
				t.Errorf("face dir %q, want rtl", wrapDir)
			}
			if out != "" {
				flipShot(t, tab, filepath.Join(out, "rtl-flip-"+vp.name+"-forward-50.png"))
			}
			tab.Eval(`document.getAnimations().forEach(a => a.finish())`, nil)
			flipWaitGone(tab)
			if v := rtlLeft(tab); v < -step-1 || v > -step+1 {
				t.Errorf("scrollLeft after the flight %v, want about %v", v, -step)
			}
			// And back.
			time.Sleep(300 * time.Millisecond)
			pgKey(t, tab, "ArrowRight", "ArrowRight", 39, "", 0)
			flipWaitStage(tab)
			flipPause(tab, 0.5)
			if out != "" {
				time.Sleep(120 * time.Millisecond)
				flipShot(t, tab, filepath.Join(out, "rtl-flip-"+vp.name+"-back-50.png"))
			}
			tab.Eval(`document.getAnimations().forEach(a => a.finish())`, nil)
			flipWaitGone(tab)
			if v := rtlLeft(tab); v != 0 {
				t.Errorf("scrollLeft after the flight back %v, want 0", v)
			}
		})
	}
}

func TestRTLFlipDrag(t *testing.T) {
	b := chromium(t)
	l := rtlSeed(t, "pages")
	srv := pgServe(t, l)
	tab := flipOpen(t, b, srv, l, 390, 844, true, "ch=2")
	// A drag toward the right edge (a swipe to the right) carries the leaf forward.
	pgTouch(t, tab, 100, 400, 330, 400)
	pgWaitFor(tab, rtlPage+` === 1`)
	flipWaitGone(tab)
	if v := rtlLeft(tab); v >= 0 {
		t.Errorf("scrollLeft after a dragged turn %v, want negative", v)
	}
}

func TestRTLSpreadRibbons(t *testing.T) {
	b := chromium(t)
	l := rtlSeed(t, "pages")
	srv := pgServe(t, l)
	tab := pgOpen(t, b, srv, l, 1440, 900, false, "ch=2")
	time.Sleep(300 * time.Millisecond)
	var ok bool
	tab.Eval(`(() => { const r = n => document.querySelector('.ts-ribbon[data-page="' + n + '"]').getBoundingClientRect().left; return r(0) > r(1); })()`, &ok)
	if !ok {
		t.Error("ribbon 0 is not right of ribbon 1")
	}
}

func TestRTLSkimMirrored(t *testing.T) {
	b := chromium(t)
	e := ofrApp(t, rtlChapters(), func(o *ebooktest.EPUBOptions) { o.Lang = "ar" })
	pgFlow(t, e.l, "pages")
	tab := skOpen(t, b, e, 390, 844, true, "ch=2")
	var dir, ruler string
	tab.Eval(`getComputedStyle(document.getElementById("reader-skim")).direction`, &dir)
	tab.Eval(`document.querySelector(".ts-readdock__ruler").getAttribute("dir") || ""`, &ruler)
	if dir != "rtl" || ruler != "rtl" {
		t.Errorf("skim direction %q, ruler dir %q, want rtl both", dir, ruler)
	}
	// Dragging from the right end to 40 % of the bar goes forward: later chapters.
	skDrag(t, tab, true, 0.98, 0.4)
	skJumped(t, tab)
	var chap int
	tab.Eval(`Number(document.querySelector("#reader section[data-ch]").dataset.ch)`, &chap)
	if chap < 3 {
		t.Errorf("a drag to the left opened chapter %d, want 3 or later", chap)
	}
}

func TestRTLScrollFlow(t *testing.T) {
	b := chromium(t)
	l := rtlSeed(t, "scroll", "This paragraph is plainly English and carries many Latin letters so its own direction is left to right.")
	srv := pgServe(t, l)
	tab := pgOpen(t, b, srv, l, 390, 844, true, "ch=2")
	time.Sleep(300 * time.Millisecond)
	var arabicRight, englishLeft bool
	tab.Eval(`(() => { const c = document.getElementById("reader").getBoundingClientRect();
		const line = (p) => { const w = document.createTreeWalker(p, NodeFilter.SHOW_TEXT); const n = w.nextNode(); const r = document.createRange(); r.setStart(n, 0); r.setEnd(n, 1); return r.getBoundingClientRect(); };
		const ps = [...document.querySelectorAll("#reader p")];
		const ar = ps.find(p => /[؀-ۿ]/.test(p.textContent)); const en = ps.find(p => p.textContent.includes("plainly English"));
		window.__ar = Math.abs(line(ar).right - (c.right)) ; window.__en = Math.abs(line(en).left - c.left); return true; })()`, nil)
	var ar, en float64
	tab.Eval(`window.__ar`, &ar)
	tab.Eval(`window.__en`, &en)
	arabicRight, englishLeft = ar < 60, en < 60
	if !arabicRight {
		t.Errorf("the Arabic block starts %v px from the right edge", ar)
	}
	if !englishLeft {
		t.Errorf("the English block starts %v px from the left edge", en)
	}
	var y0, y1 float64
	tab.Eval(`document.getElementById("reader").scrollTop`, &y0)
	pgKey(t, tab, "ArrowLeft", "ArrowLeft", 37, "", 0)
	time.Sleep(600 * time.Millisecond)
	tab.Eval(`document.getElementById("reader").scrollTop`, &y1)
	if y1 <= y0 {
		t.Errorf("ArrowLeft in the scroll flow moved the page from %v to %v, want further down", y0, y1)
	}
}

func TestRTLHebrewMixed(t *testing.T) {
	b := chromium(t)
	hebrew := strings.Join(ebooktest.HebrewParas(1), " ")
	raw := "<p>" + hebrew + " Hello World " + hebrew + "</p>"
	chs := []ebooktest.EPUBChapter{{Title: "Title", EpubType: "titlepage", Paras: []string{"x"}}, {Title: "One", EpubType: "chapter", Raw: raw}}
	l := pgSeedEPUB(t, chs, func(o *ebooktest.EPUBOptions) { o.Lang = "he" })
	pgFlow(t, l, "scroll")
	srv := pgServe(t, l)
	tab := pgOpen(t, b, srv, l, 390, 844, true, "ch=2")
	time.Sleep(300 * time.Millisecond)
	var hello, world, right float64
	tab.Eval(`(() => { const find = (word) => { const w = document.createTreeWalker(document.getElementById("reader"), NodeFilter.SHOW_TEXT); let n;
		while ((n = w.nextNode())) { const i = n.data.indexOf(word); if (i >= 0) { const r = document.createRange(); r.setStart(n, i); r.setEnd(n, i + word.length); return r.getBoundingClientRect(); } } return null; };
		window.__h = find("Hello").left; window.__w = find("World").left; return true; })()`, nil)
	tab.Eval(`window.__h`, &hello)
	tab.Eval(`window.__w`, &world)
	if !(hello < world) {
		t.Errorf("Hello at x=%v, World at x=%v: the English phrase does not read left to right", hello, world)
	}
	tab.Eval(`Math.abs(document.querySelector("#reader p").getBoundingClientRect().right - document.getElementById("reader").getBoundingClientRect().right)`, &right)
	var align string
	tab.Eval(`getComputedStyle(document.querySelector("#reader p")).direction`, &align)
	if align != "rtl" {
		t.Errorf("the Hebrew paragraph direction %q, want rtl", align)
	}
}
