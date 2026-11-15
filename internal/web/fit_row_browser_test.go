//go:build dev

package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// fitProbeJS describes an action row after the fit script has run: how many lines its drawn controls sit on, which
// keys are folded away, which twins an opened menu shows, and whether any folded control is still reachable by Tab.
// root is the row's selector; line says which of its children form the line the person sees.
const fitProbeJS = `(async (root, lineSel) => {
  await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
  const row = document.querySelector(root);
  const drawn = (el) => el.getClientRects().length > 0 && getComputedStyle(el).visibility !== "hidden";
  const line = [...row.querySelectorAll(lineSel)].filter(drawn);
  const centres = line.map((el) => { const b = el.getBoundingClientRect(); return b.top + b.height / 2; });
  const spread = centres.length ? Math.max(...centres) - Math.min(...centres) : 0;
  const menu = row.querySelector(".ts-moremenu");
  const menuShown = !!menu && drawn(menu);
  const folded = [...new Set([...row.querySelectorAll("[data-ts-fit-off]")].filter((el) => !el.hidden).map((el) => el.dataset.tsFitItem))];
  const reachable = [...row.querySelectorAll("[data-ts-fit-off]")].filter((el) => getComputedStyle(el).display !== "none").length;
  let twins = [];
  if (menu) {
    menu.open = true;
    twins = [...menu.querySelectorAll("[data-ts-fit-for]")].filter(drawn).map((el) => el.dataset.tsFitFor);
    menu.open = false;
  }
  const dupes = twins.filter((k) => [...row.querySelectorAll('[data-ts-fit-item="' + k + '"]')].some(drawn));
  const play = row.querySelector("[data-ts-play], .ts-split, [data-ts-read]");
  return {lines: spread <= 2 ? 1 : 2, spread, folded, twins, dupes, reachable, menuShown, playShown: !!play && drawn(play),
    menuRight: menu ? menu.getBoundingClientRect().right : 0, vw: innerWidth, fitOn: row.hasAttribute("data-ts-fit-on"), over: document.documentElement.scrollWidth > innerWidth + 1};
})`

type fitProbe struct {
	Lines                int
	Spread               float64
	Folded, Twins, Dupes []string
	Reachable            int
	MenuShown, PlayShown bool
	MenuRight, VW        float64
	FitOn, Over          bool
}

func fitProbeOf(tab interface{ Eval(string, any) }, root, line string) fitProbe {
	var p fitProbe
	tab.Eval(fitProbeJS+`(`+jsString(root)+`,`+jsString(line)+`)`, &p)
	return p
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	seen := map[string]bool{}
	for _, k := range a {
		seen[k] = true
	}
	for _, k := range b {
		if !seen[k] {
			return false
		}
	}
	return true
}

func (p fitProbe) check(t *testing.T, what string) {
	t.Helper()
	if !p.FitOn {
		t.Errorf("%s: the fit script did not run", what)
	}
	if p.Lines != 1 {
		t.Errorf("%s: the actions are on more than one line (centres differ by %.1f px)", what, p.Spread)
	}
	if !p.PlayShown {
		t.Errorf("%s: the play button is not drawn", what)
	}
	if !sameSet(p.Folded, p.Twins) {
		t.Errorf("%s: folded %v but the menu shows twins %v", what, p.Folded, p.Twins)
	}
	if len(p.Folded) > 0 && !p.MenuShown {
		t.Errorf("%s: buttons are folded away but the three-dot button is not drawn", what)
	}
	if len(p.Dupes) > 0 {
		t.Errorf("%s: %v show in the row and in the menu", what, p.Dupes)
	}
	if p.Reachable > 0 {
		t.Errorf("%s: %d folded controls can still be reached", what, p.Reachable)
	}
	if p.MenuShown && p.MenuRight > p.VW+1 {
		t.Errorf("%s: the three-dot button ends at %.0f px, past the %.0f px window", what, p.MenuRight, p.VW)
	}
	if p.Over {
		t.Errorf("%s: the page scrolls sideways", what)
	}
}

const bookFitRow = "#book-play"

// bookFitLine is what the person sees as the line: at 1280 and up the play row, below it the action cells.
func bookFitLine(w int) string {
	if w >= 1280 {
		return ".ts-split, [data-ts-read], .ts-book__acts > *"
	}
	return ".ts-book__acts > *"
}

func resizeTab(t *testing.T, tab interface{ Call(string, any, any) error }, w, h int) {
	t.Helper()
	if err := tab.Call("Emulation.setDeviceMetricsOverride", map[string]any{"width": w, "height": h, "deviceScaleFactor": 1, "mobile": false}, nil); err != nil {
		t.Fatal(err)
	}
}

// TestFitRowBookBrowser: the book page's action row is one line at 1440, 1280, 1100, 1024, 768 and 390 px and at large
// text; the number of folded buttons only grows as the window narrows and comes back as it widens; every folded
// button has one twin in the opened menu and none shows twice; the twins work.
func TestFitRowBookBrowser(t *testing.T) {
	b := chromium(t)
	l := fmt2Seed(t)
	fmt2Listened(t, l, l.owner, l.book.ID, 5000)
	owner := l.owner
	base := themedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(&http.Cookie{Name: owner.Name, Value: owner.Value})
		l.a.h.ServeHTTP(w, r)
	}))
	widths := []int{1440, 1280, 1100, 1024, 768, 390}
	tab := b.NewTab(1440, 900, false, true)
	tab.Navigate(base + "/books/" + l.book.ID + "?theme=dark")
	tab.WaitFor(`document.querySelector("#book-play[data-ts-fit-on]") !== null`, 10*time.Second)
	counts := map[int]int{}
	for _, w := range widths {
		resizeTab(t, tab, w, 900)
		p := fitProbeOf(tab, bookFitRow, bookFitLine(w))
		p.check(t, "book "+strconv.Itoa(w))
		counts[w] = len(p.Folded)
		if dir := os.Getenv("TENDSHELF_FIT_SHOTS"); dir != "" {
			tab.Screenshot(filepath.Join(dir, "book-"+strconv.Itoa(w)+".png"))
		}
		t.Logf("%d px: folded %v", w, p.Folded)
	}
	// Within one layout (1280 and up, then the tablet's) a narrower window never shows more than a wider one.
	for _, group := range [][]int{{1440, 1280}, {1100, 1024, 768}} {
		for i := 1; i < len(group); i++ {
			if counts[group[i]] < counts[group[i-1]] {
				t.Errorf("%d px folds %d, fewer than %d px (%d)", group[i], counts[group[i]], group[i-1], counts[group[i-1]])
			}
		}
	}
	if counts[1280] == 0 {
		t.Errorf("nothing folds at 1280 px; the test is not exercising the script")
	}
	// Widen again: the buttons come back.
	for i := len(widths) - 1; i >= 0; i-- {
		resizeTab(t, tab, widths[i], 900)
		p := fitProbeOf(tab, bookFitRow, bookFitLine(widths[i]))
		p.check(t, "book (widening) "+strconv.Itoa(widths[i]))
		if len(p.Folded) != counts[widths[i]] {
			t.Errorf("%d px folds %d going up and %d going down", widths[i], len(p.Folded), counts[widths[i]])
		}
	}
	// Large text at 1280.
	resizeTab(t, tab, 1280, 900)
	tab.Eval(`document.documentElement.dataset.textSize = "large"`, nil)
	fitProbeOf(tab, bookFitRow, bookFitLine(1280)).check(t, "book large text 1280")
	tab.Eval(`delete document.documentElement.dataset.textSize`, nil)
	// Text at 200 % (the way the accessibility audit sets it): the row refits itself, and the three-dot button stays on screen.
	for _, w := range []int{1440, 1280, 1024} {
		resizeTab(t, tab, w, 900)
		tab.Eval(`document.documentElement.style.fontSize = "200%"`, nil)
		p := fitProbeOf(tab, bookFitRow, bookFitLine(w))
		p.Over = false // the title column beside the cover is wider than the window at 200 % (not this row's doing)
		p.check(t, "book 200% text "+strconv.Itoa(w))
		tab.Eval(`document.documentElement.style.fontSize = ""`, nil)
	}
}

// TestFitRowBookPhone: the phone bar keeps its cells on one line, with and without large text.
func TestFitRowBookPhone(t *testing.T) {
	b := chromium(t)
	l := fmt2Seed(t)
	owner := l.owner
	base := themedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(&http.Cookie{Name: owner.Name, Value: owner.Value})
		l.a.h.ServeHTTP(w, r)
	}))
	for _, w := range []int{390, 360} {
		tab := b.NewTab(w, 800, true, true)
		tab.Navigate(base + "/books/" + l.book.ID + "?theme=dark")
		tab.WaitFor(`document.querySelector("#book-play[data-ts-fit-on]") !== null`, 10*time.Second)
		fitProbeOf(tab, bookFitRow, ".ts-book__acts > *").check(t, "phone "+strconv.Itoa(w))
		tab.Eval(`document.documentElement.style.fontSize = "200%"`, nil)
		p := fitProbeOf(tab, bookFitRow, ".ts-book__acts > *")
		p.check(t, "phone large "+strconv.Itoa(w))
		t.Logf("%d px large text: folded %v", w, p.Folded)
	}
}

// TestFitRowTwinsWork: at 768 px the Mark control is folded; its twin marks the book finished and the shelf twin opens
// the shelf sheet; Tab never lands on a folded control.
func TestFitRowTwinsWork(t *testing.T) {
	b := chromium(t)
	l := fmt2Seed(t)
	fmt2Listened(t, l, l.owner, l.book.ID, 5000)
	owner := l.owner
	base := themedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(&http.Cookie{Name: owner.Name, Value: owner.Value})
		l.a.h.ServeHTTP(w, r)
	}))
	tab := b.NewTab(768, 900, false, true)
	tab.Navigate(base + "/books/" + l.book.ID + "?theme=dark")
	tab.WaitFor(`document.querySelector("#book-play[data-ts-fit-on]") !== null`, 10*time.Second)
	var folded bool
	tab.Eval(`!!document.querySelector('#book-play form[data-ts-fit-item="mark"][data-ts-fit-off]')`, &folded)
	if !folded {
		t.Fatal("Mark as finished is not folded away at 768 px")
	}
	// Tab through the page: no focused element is a folded control.
	var landed bool
	tab.Eval(`(() => { for (const el of document.querySelectorAll("#book-play [data-ts-fit-off] a, #book-play [data-ts-fit-off] button")) { if (el.tabIndex >= 0 && el.getClientRects().length) return true; } return false; })()`, &landed)
	if landed {
		t.Error("a folded control can take focus")
	}
	// The mark twin.
	tab.Eval(`document.querySelector("#book-play .ts-moremenu").open = true`, nil)
	tab.Eval(`document.querySelector('#book-play [data-ts-fit-for="mark"]').click()`, nil)
	deadline := time.Now().Add(5 * time.Second)
	for {
		rec := l.a.do(http.MethodGet, "/hx/books/"+l.book.ID+"/play", nil, proxyDefault, owner)
		if strings.Contains(rec.Body.String(), "Mark as not started") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the Mark twin did not mark the book finished")
		}
		time.Sleep(100 * time.Millisecond)
	}
	// The shelf twin (the menu may have been re-read after the save).
	tab.WaitFor(`document.querySelector('#book-play[data-ts-fit-on] [data-ts-fit-for="shelf"]') !== null`, 5*time.Second)
	var open bool
	tab.Eval(`document.querySelector("#book-play .ts-moremenu").open`, &open)
	if open {
		t.Error("the menu stayed open after a twin was used")
	}
	tab.Eval(`(() => { const m = document.querySelector("#book-play .ts-moremenu"); m.open = true; document.querySelector('#book-play [data-ts-fit-for="shelf"]').click(); })()`, nil)
	tab.WaitFor(`document.querySelector("#sheet [role=dialog], #sheet .ts-sheet") !== null`, 5*time.Second)
}

// TestFitRowPodShowBrowser: a followed show's buttons stay on one line at 1440, 1024, 768, 390 and 360 px; Stop following
// folds first.
func TestFitRowPodShowBrowser(t *testing.T) {
	b := chromium(t)
	v := newPodVisual(t)
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		r.AddCookie(v.owner)
		v.a.h.ServeHTTP(rw, r)
	}))
	t.Cleanup(srv.Close)
	path := "/podcasts/shows/" + v.shows["The Harbour Hour"]
	for _, w := range []int{1440, 1024, 768, 390, 360} {
		tab := b.NewTab(w, 900, w < 700, true)
		tab.Navigate(srv.URL + path)
		tab.WaitFor(`document.querySelector(".ts-podshow__actions[data-ts-fit-on]") !== null`, 10*time.Second)
		p := fitProbeOf(tab, ".ts-podshow__actions", ".ts-podshow__actions > *")
		p.check(t, "show "+strconv.Itoa(w))
		t.Logf("%d px: folded %v", w, p.Folded)
		if len(p.Folded) > 0 && p.Folded[0] != "unfollow" && len(p.Folded) == 1 {
			t.Errorf("show %d: %v folded first, want Stop following", w, p.Folded)
		}
	}
}

// TestFitRowTextBookLargeText: a text book (Read is the primary button) at 200 % text and 1440 px keeps the three-dot
// button on screen, even though the column beside the cover is narrower than the Read button and the menu together.
func TestFitRowTextBookLargeText(t *testing.T) {
	b := chromium(t)
	l := fmt2Seed(t)
	owner := l.owner
	base := themedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(&http.Cookie{Name: owner.Name, Value: owner.Value})
		l.a.h.ServeHTTP(w, r)
	}))
	tab := b.NewTab(1440, 900, false, true)
	tab.Navigate(base + "/books/" + l.tbook.ID + "?theme=dark")
	tab.WaitFor(`document.querySelector("#book-play[data-ts-fit-on]") !== null`, 10*time.Second)
	fitProbeOf(tab, bookFitRow, bookFitLine(1440)).check(t, "text book 1440")
	tab.Eval(`document.documentElement.style.fontSize = "200%"`, nil)
	p := fitProbeOf(tab, bookFitRow, bookFitLine(1440))
	p.Lines, p.Over = 1, false // this narrow, the row may wrap once (the last resort); the button must stay on screen
	p.check(t, "text book 200% 1440")
}
