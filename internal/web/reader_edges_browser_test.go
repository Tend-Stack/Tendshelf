//go:build dev

package web

import (
	"context"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The page edges, the ribbon, the hint and the page line (decision 0036, Package C). Needs TENDSHELF_TEST_BROWSER.

// prOpen opens the reader at a size, as a touch screen or as a mouse, and waits until the ribbons have been placed.
func prOpen(t *testing.T, b *cdp.Browser, srv *httptest.Server, l *rdrLab, w, h int, touch bool, query string) *cdp.Tab {
	t.Helper()
	tab := b.NewTab(w, h, touch, true)
	if touch {
		prTouchDevice(t, tab)
	}
	tab.Navigate(srv.URL + l.readPath(query))
	tab.WaitFor(`window.htmx !== undefined && document.fonts.status === "loaded" && document.querySelector("#reader.ts-reader--pages") !== null && document.querySelector(".ts-ribbon:not([hidden])") !== null`, 10*time.Second)
	return tab
}

// prPress presses the ribbon of a page the way the device does: a touch tap or a mouse click on its hit area.
func prPress(t *testing.T, tab *cdp.Tab, touch bool, page int) {
	t.Helper()
	x, y := prCentre(tab, fmt.Sprintf(`.ts-ribbon[data-page="%d"]`, page))
	if touch {
		pgTouch(t, tab, x, y, x, y)
		return
	}
	prClick(t, tab, x, y)
}

// prBookmarks counts the person's text bookmarks and audio bookmarks on the book.
func prBookmarks(t *testing.T, l *rdrLab) (text, audio int) {
	t.Helper()
	ctx := context.Background()
	tb, err := l.a.srv.progress.TextBookmarks(ctx, l.ownerID, l.book.ID)
	if err != nil {
		t.Fatal(err)
	}
	ab, err := l.a.srv.progress.Bookmarks(ctx, l.ownerID, l.book.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	return len(tb), len(ab)
}

// prLabel is the accessible name of the first shown ribbon.
func prLabel(tab *cdp.Tab) string {
	var s string
	tab.Eval(`document.querySelector(".ts-ribbon:not([hidden])").getAttribute("aria-label")`, &s)
	return s
}

// TestPageRibbon: a press marks the page, the flag lands in the text and the ribbon is pressed; a second press clears it.
// The right page of a spread takes that page's own sentence. The marks sheet lists it; the audio Bookmarks list does not.
func TestPageRibbon(t *testing.T) {
	b := chromium(t)
	l := rpBook(t, 2, 60)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	for _, vp := range []struct {
		name  string
		w, h  int
		touch bool
	}{{"390", 390, 844, true}, {"1440", 1440, 900, false}} {
		t.Run(vp.name, func(t *testing.T) {
			tab := prOpen(t, b, srv, l, vp.w, vp.h, vp.touch, "ch=3")
			if got := prLabel(tab); got != "Bookmark this page" {
				t.Errorf("unmarked label %q", got)
			}
			prPress(t, tab, vp.touch, 0)
			tab.WaitFor(`document.querySelector(".ts-ribbon[data-page='0'][aria-pressed='true']") !== null`, 5*time.Second)
			var flags int
			var sid, label string
			tab.Eval(`document.querySelectorAll(".ts-reader__bm").length`, &flags)
			tab.Eval(`document.querySelector(".ts-ribbon[data-page='0']").dataset.bm`, &sid)
			tab.Eval(`document.querySelector(".ts-ribbon[data-page='0']").getAttribute("aria-label")`, &label)
			if flags != 1 || sid == "" || label != "Remove bookmark" {
				t.Errorf("after the press: %d flags, bm %q, label %q", flags, sid, label)
			}
			if text, audio := prBookmarks(t, l); text != 1 || audio != 0 {
				t.Errorf("stored: %d text and %d audio bookmarks, want 1 and 0", text, audio)
			}
			var quote string
			tab.Eval(fmt.Sprintf(`document.querySelector('.ts-s[data-sid="%s"]').textContent.trim().slice(0, 12)`, sid), &quote)
			if body := l.get(l.owner, l.readPath("sheet=marks"), false).Body.String(); !strings.Contains(body, quote) {
				t.Errorf("the marks sheet does not list the bookmark (%q)", quote)
			}
			prPress(t, tab, vp.touch, 0)
			tab.WaitFor(`document.querySelector(".ts-ribbon[aria-pressed='true']") === null`, 5*time.Second)
			tab.Eval(`document.querySelectorAll(".ts-reader__bm").length`, &flags)
			if text, _ := prBookmarks(t, l); flags != 0 || text != 0 {
				t.Errorf("after the second press: %d flags, %d stored", flags, text)
			}
			if got := prLabel(tab); got != "Bookmark this page" {
				t.Errorf("label after clearing %q", got)
			}
		})
	}

	// A spread has two ribbons; the right one takes the first sentence of the right page.
	tab := prOpen(t, b, srv, l, 2000, 1100, false, "ch=4")
	if got := prRibbons(tab); len(got) != 2 {
		t.Fatalf("a spread shows %d ribbons, want 2", len(got))
	}
	prPress(t, tab, false, 1)
	tab.WaitFor(`document.querySelector(".ts-ribbon[data-page='1'][aria-pressed='true']") !== null`, 5*time.Second)
	if got := prRibbons(tab); got[0] || !got[1] {
		t.Errorf("pressed states %v, want [false true]", got)
	}
	var onRight bool
	tab.Eval(`(() => {
		const box = document.querySelector("section[data-part]");
		const s = getComputedStyle(document.getElementById("reader"));
		const step = parseFloat(s.getPropertyValue("--ts-col")) + parseFloat(s.getPropertyValue("--ts-gap"));
		const left = box.getBoundingClientRect().left + step - 1;
		const el = document.querySelector('.ts-s[data-sid="' + document.querySelector(".ts-ribbon[data-page='1']").dataset.bm + '"]');
		const rects = [...el.getClientRects()];
		const last = rects[rects.length - 1];
		return last.left >= left && last.left < left + step;
	})()`, &onRight)
	if !onRight {
		t.Error("the right ribbon marked a sentence that does not end on the right page")
	}
}

// TestRibbonSurvivesResize: a bookmark's page shows its ribbon pressed at another size, and after a resize in place.
func TestRibbonSurvivesResize(t *testing.T) {
	b := chromium(t)
	l := rpBook(t, 2, 60)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	sid := "c3.p20.s1"
	rec := l.hx(t, http.MethodPost, "/books/"+l.book.ID+"/bookmarks", url.Values{"edition_id": {l.ed.ID}, "sid": {sid}}, l.owner)
	if rec.Code != http.StatusOK {
		t.Fatalf("bookmark: %d", rec.Code)
	}
	for _, vp := range []struct {
		w, h  int
		touch bool
	}{{390, 844, true}, {1440, 900, false}} {
		tab := prOpen(t, b, srv, l, vp.w, vp.h, vp.touch, "ch=3&at="+sid)
		tab.WaitFor(`document.querySelector(".ts-ribbon[aria-pressed='true']") !== null`, 5*time.Second)
	}
	// The same window made narrower: the place stays, so the page with the flag is still the page shown.
	tab := prOpen(t, b, srv, l, 1440, 900, false, "ch=3&at="+sid)
	tab.WaitFor(`document.querySelector(".ts-ribbon[aria-pressed='true']") !== null`, 5*time.Second)
	if err := tab.Call("Emulation.setDeviceMetricsOverride", map[string]any{"width": 700, "height": 900, "deviceScaleFactor": 1, "mobile": false}, nil); err != nil {
		t.Fatal(err)
	}
	tab.WaitFor(`!document.getElementById("reader").classList.contains("ts-reader--spread") && document.querySelector(".ts-ribbon[aria-pressed='true']") !== null`, 5*time.Second)
}

// TestEdgeButtons: the edges are buttons with names, at least 44 by 44, reachable by Tab and Enter; a click turns, a drag in the
// text selects and does not turn; below 1024 px, and on a touch screen, they take no pointer and so never swallow a tap.
func TestEdgeButtons(t *testing.T) {
	b := chromium(t)
	l := rpBook(t, 2, 60)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	tab := prOpen(t, b, srv, l, 1440, 900, false, "ch=3")
	var names []string
	tab.Eval(`[...document.querySelectorAll(".ts-edge")].map(e => e.getAttribute("aria-label"))`, &names)
	if len(names) != 2 || names[0] != "Previous page" || names[1] != "Next page" {
		t.Fatalf("edge names %v", names)
	}
	var sizes [][2]float64
	tab.Eval(`[...document.querySelectorAll(".ts-edge")].map(e => { const r = e.getBoundingClientRect(); return [r.width, r.height]; })`, &sizes)
	for i, s := range sizes {
		if s[0] < 44 || s[1] < 44 {
			t.Errorf("edge %d is %.0f x %.0f px", i, s[0], s[1])
		}
	}
	// At the very start of the book the previous edge rests; mid-book it does not.
	var rest, mid string
	tab.Eval(`document.querySelector(".ts-edge--prev").getAttribute("aria-disabled")`, &mid)
	first := prOpen(t, b, srv, l, 1440, 900, false, "ch=1")
	first.Eval(`document.querySelector(".ts-edge--prev").getAttribute("aria-disabled")`, &rest)
	if rest != "true" || mid != "false" {
		t.Errorf("previous edge at rest: start %q (want true), mid-book %q (want false)", rest, mid)
	}
	// The mouse turns forward and back.
	x, y := prCentre(tab, ".ts-edge--next")
	prClick(t, tab, x, y)
	pgPageIs(tab, 1)
	x, y = prCentre(tab, ".ts-edge--prev")
	prClick(t, tab, x, y)
	pgPageIs(tab, 0)
	// A drag in the text selects it and turns nothing.
	var r struct{ X, Y, W float64 }
	tab.Eval(`(() => { const r = document.querySelector("section[data-part] p").getBoundingClientRect(); return {X: r.x, Y: r.y + 8, W: r.width}; })()`, &r)
	prMouse(t, tab, "mouseMoved", r.X+4, r.Y)
	prMouse(t, tab, "mousePressed", r.X+4, r.Y)
	prDragTo(t, tab, r.X+r.W/2, r.Y)
	prDragTo(t, tab, r.X+r.W-4, r.Y)
	prMouse(t, tab, "mouseReleased", r.X+r.W-4, r.Y)
	time.Sleep(200 * time.Millisecond)
	var selected int
	var page int
	tab.Eval(`getSelection().toString().length`, &selected)
	pgEval(tab, `pgPage()`, &page)
	if selected == 0 || page != 0 {
		t.Errorf("a drag in the text: %d characters selected, page %d", selected, page)
	}
	// The keyboard: Tab reaches the next edge and Enter turns.
	tab.Eval(`getSelection().removeAllRanges(); document.activeElement.blur()`, nil)
	for i := 0; i < 30; i++ {
		var on bool
		tab.Eval(`document.activeElement.classList.contains("ts-edge--next")`, &on)
		if on {
			break
		}
		pgKey(t, tab, "Tab", "Tab", 9, "", 0)
	}
	var onNext bool
	tab.Eval(`document.activeElement.classList.contains("ts-edge--next")`, &onNext)
	if !onNext {
		t.Fatal("Tab never reached the next-page edge")
	}
	pgKey(t, tab, "Enter", "Enter", 13, "\r", 0)
	pgPageIs(tab, 1)

	// A phone and any touch screen: no pointer lands on an edge, so a tap at the side is the tap zone's.
	for _, vp := range []struct{ w, h int }{{390, 844}, {1280, 800}} {
		tab := prOpen(t, b, srv, l, vp.w, vp.h, true, "ch=3")
		var hit bool
		tab.Eval(fmt.Sprintf(`!!document.elementFromPoint(%d, %d).closest(".ts-edge")`, vp.w-10, vp.h/2), &hit)
		if hit {
			t.Errorf("%d px touch screen: an edge takes the tap at the side", vp.w)
		}
		var names int
		tab.Eval(`document.querySelectorAll(".ts-edge").length`, &names)
		if names != 2 {
			t.Errorf("%d px: %d edges in the tree, want 2 (a screen reader still needs them)", vp.w, names)
		}
	}
}

// TestTapSwapZones: swapped sides turn the other way for a finger, and the edge follows its own label for a mouse or keyboard.
func TestTapSwapZones(t *testing.T) {
	b := chromium(t)
	l := rpBook(t, 2, 60)
	pgFlow(t, l, "pages")
	rsetPost(t, l, l.owner, true, "tap_swap", "1")
	srv := pgServe(t, l)
	tab := prOpen(t, b, srv, l, 1440, 900, false, "ch=3")
	// A mouse click on Next page goes forward, swapped or not.
	x, y := prCentre(tab, ".ts-edge--next")
	prClick(t, tab, x, y)
	pgPageIs(tab, 1)
	// A finger's click on the same edge honours the swap: the right side goes back.
	tab.Eval(`document.querySelector(".ts-edge--next").dispatchEvent(new PointerEvent("click", {pointerType: "touch", bubbles: true, cancelable: true}))`, nil)
	pgPageIs(tab, 0)
	tab.Eval(`document.querySelector(".ts-edge--prev").dispatchEvent(new PointerEvent("click", {pointerType: "touch", bubbles: true, cancelable: true}))`, nil)
	pgPageIs(tab, 1)
	// The same on a phone: the right quarter goes back and the left goes forward.
	ph := prOpen(t, b, srv, l, 390, 844, true, "ch=3")
	pgKey(t, ph, "ArrowRight", "ArrowRight", 39, "", 0)
	pgPageIs(ph, 1)
	pgTouch(t, ph, 390*0.92, 422, 390*0.92, 422)
	pgPageIs(ph, 0)
	pgTouch(t, ph, 390*0.08, 422, 390*0.08, 422)
	pgPageIs(ph, 1)
}

// TestReaderHint: a touch screen is told once where to tap and how the page turns, mirrored with swapped sides; it goes at the
// first turn or tap and never returns. A mouse gets no sentence, and is not shown the pulse under reduced motion.
func TestReaderHint(t *testing.T) {
	l := rpBook(t, 2, 60)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	b := chromium(t)
	tab := prOpen(t, b, srv, l, 390, 844, true, "ch=3")
	tab.WaitFor(`document.querySelector(".ts-pagehint") !== null`, 5*time.Second)
	var text, keep string
	var chevrons []string
	tab.Eval(`document.querySelector(".ts-pagehint__text").textContent`, &text)
	tab.Eval(`[...document.querySelectorAll(".ts-pagehint__chev")].map(c => c.className)`, &chevrons)
	tab.Eval(`localStorage.getItem("ts.reader.hinted")`, &keep)
	if text != "Tap the sides or swipe to turn the page. Tap the middle for the menu." || keep != "1" {
		t.Errorf("hint %q, key %q", text, keep)
	}
	if len(chevrons) != 2 || !strings.Contains(chevrons[0], "chev--l") || !strings.Contains(chevrons[0], "zone--l") || !strings.Contains(chevrons[1], "chev--r") {
		t.Errorf("chevrons %v", chevrons)
	}
	var role string
	var taking string
	tab.Eval(`document.querySelector(".ts-pagehint").getAttribute("role")`, &role)
	tab.Eval(`getComputedStyle(document.querySelector(".ts-pagehint")).pointerEvents`, &taking)
	if role != "status" || taking != "none" {
		t.Errorf("hint role %q, pointer-events %q", role, taking)
	}
	pgTouch(t, tab, 390*0.92, 422, 390*0.92, 422)
	tab.WaitFor(`document.querySelector(".ts-pagehint") === null`, 3*time.Second)
	// Once: the same device does not see it again.
	tab.Navigate(srv.URL + l.readPath("ch=3"))
	tab.WaitFor(`document.querySelector("#reader.ts-reader--pages") !== null`, 8*time.Second)
	time.Sleep(600 * time.Millisecond)
	var again bool
	tab.Eval(`document.querySelector(".ts-pagehint") !== null`, &again)
	if again {
		t.Error("the hint came back")
	}

	// Swapped sides: the left zone now goes forward, so its chevron points right.
	rsetPost(t, l, l.owner, true, "tap_swap", "1")
	b2 := chromium(t)
	tab = prOpen(t, b2, srv, l, 390, 844, true, "ch=3")
	tab.WaitFor(`document.querySelector(".ts-pagehint") !== null`, 5*time.Second)
	tab.Eval(`[...document.querySelectorAll(".ts-pagehint__chev")].map(c => c.className)`, &chevrons)
	if len(chevrons) != 2 || !strings.Contains(chevrons[0], "zone--l") || !strings.Contains(chevrons[0], "chev--r") || !strings.Contains(chevrons[1], "chev--l") {
		t.Errorf("mirrored chevrons %v", chevrons)
	}
	// A turn by key takes it away too.
	pgKey(t, tab, "ArrowRight", "ArrowRight", 39, "", 0)
	tab.WaitFor(`document.querySelector(".ts-pagehint") === null`, 3*time.Second)

	// A mouse: no sentence; the key is set; reduced motion means no pulse.
	b3 := chromium(t)
	tab = prOpen(t, b3, srv, l, 1440, 900, false, "ch=3")
	time.Sleep(300 * time.Millisecond)
	var shown bool
	var animating int
	tab.Eval(`document.querySelector(".ts-pagehint") !== null`, &shown)
	tab.Eval(`localStorage.getItem("ts.reader.hinted")`, &keep)
	tab.Eval(`document.getAnimations().filter(a => a.effect.target && a.effect.target.classList.contains("ts-edge")).length`, &animating)
	if shown || keep != "1" || animating != 0 {
		t.Errorf("mouse: hint shown %v, key %q, %d edge animations under reduced motion", shown, keep, animating)
	}
}

// TestPageLiveRegion: a turn is spoken as the page line, a moment after the last turn, and not on arrival.
func TestPageLiveRegion(t *testing.T) {
	b := chromium(t)
	l := rpBook(t, 2, 60)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	tab := prOpen(t, b, srv, l, 1440, 900, false, "ch=3")
	var live, role string
	tab.Eval(`document.getElementById("reader-page-live").textContent`, &live)
	tab.Eval(`document.getElementById("reader-page-live").getAttribute("role")`, &role)
	if live != "" || role != "status" {
		t.Errorf("on arrival: %q, role %q", live, role)
	}
	pgNext(t, tab)
	pgNext(t, tab)
	tab.WaitFor(`document.getElementById("reader-page-live").textContent !== ""`, 3*time.Second)
	var line, spoken string
	tab.Eval(`document.getElementById("reader-page").textContent`, &line)
	tab.Eval(`document.getElementById("reader-page-live").textContent`, &spoken)
	if spoken != line {
		t.Errorf("spoken %q, page line %q", spoken, line)
	}
}

// TestEdgeContrast: the chevron and the ribbon are 3:1 against the page at rest, the pressed ribbon too, and the foot's
// words 4.5:1, in all four themes.
func TestEdgeContrast(t *testing.T) {
	b := chromium(t)
	l := rpBook(t, 2, 60)
	pgFlow(t, l, "pages")
	base := brServe(t, l, l.owner)
	for _, theme := range []string{"dark", "light", "sepia", "contrast"} {
		t.Run(theme, func(t *testing.T) {
			tab := b.NewTab(1440, 900, false, true)
			tab.Navigate(base + l.readPath("ch=3&theme="+theme))
			tab.WaitFor(`document.querySelector("#reader.ts-reader--pages") !== null && document.fonts.status === "loaded" && document.querySelector(".ts-ribbon:not([hidden])") !== null`, 10*time.Second)
			var got map[string]float64
			tab.Eval(`(() => {
				const rgb = (s) => s.match(/[\d.]+/g).slice(0, 3).map(Number).map((v) => (s.startsWith("color(") ? v * 255 : v));
				const lum = (c) => { const f = c.map((v) => { v /= 255; return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4; }); return 0.2126 * f[0] + 0.7152 * f[1] + 0.0722 * f[2]; };
				const ratio = (a, b) => { const x = lum(a), y = lum(b); return (Math.max(x, y) + 0.05) / (Math.min(x, y) + 0.05); };
				const mix = (a, b, o) => a.map((v, i) => v * o + b[i] * (1 - o));
				const ground = rgb(getComputedStyle(document.querySelector(".ts-reader__col")).backgroundColor);
				const edge = document.querySelector(".ts-edge--next");
				const ribbon = document.querySelector(".ts-ribbon");
				const ec = getComputedStyle(edge);
				const rc = getComputedStyle(ribbon);
				const foot = rgb(getComputedStyle(document.getElementById("reader-foot")).color);
				return {
					chevron: ratio(mix(rgb(ec.color), ground, Number(ec.opacity)), ground),
					ribbon: ratio(mix(rgb(rc.color), ground, Number(rc.opacity)), ground),
					foot: ratio(foot, ground),
					accent: ratio(rgb(getComputedStyle(document.documentElement).getPropertyValue("--accent").trim() ? (() => { const d = document.createElement("i"); d.style.color = "var(--accent)"; document.body.append(d); const c = getComputedStyle(d).color; d.remove(); return c; })() : "rgb(0,0,0)"), ground),
				};
			})()`, &got)
			if got["chevron"] < 3 || got["ribbon"] < 3 || got["accent"] < 3 || got["foot"] < 4.5 || math.IsNaN(got["foot"]) {
				t.Errorf("contrast at rest: chevron %.2f, ribbon %.2f, pressed %.2f (want 3), foot %.2f (want 4.5)", got["chevron"], got["ribbon"], got["accent"], got["foot"])
			}
		})
	}
}
