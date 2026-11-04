//go:build dev

package web

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The page reader in Chromium (RD2). Needs TENDSHELF_TEST_BROWSER. Every test builds a synthetic book: a title
// page and chapters of numbered sentences, so no two screens of text are alike.

var pgViewports = []struct {
	name   string
	w, h   int
	mobile bool
}{{"mobile-390", 390, 844, true}, {"desktop-1440", 1440, 900, false}}

// pgProse is n paragraphs of unique sentences ("Sentence 3.2 ...").
func pgProse(tag string, n int) []string {
	words := []string{"lantern", "harbour", "orchard", "winter", "copper", "river", "meadow", "candle"}
	out := make([]string, n)
	for i := range out {
		var sb strings.Builder
		for j := 0; j < 5; j++ {
			fmt.Fprintf(&sb, "Sentence %s.%d.%d walks past the %s and the %s on a long and quiet road. ", tag, i+1, j+1, words[(i+j)%len(words)], words[(i*3+j)%len(words)])
		}
		out[i] = strings.TrimSpace(sb.String())
	}
	return out
}

// pgSeed is a book of a title page and chapters of paras paragraphs each.
func pgSeed(t *testing.T, chapters, paras int) *rdrLab {
	t.Helper()
	chs := []ebooktest.EPUBChapter{{Title: "Title Page", EpubType: "titlepage", Paras: []string{"The Pager"}}}
	for i := 1; i <= chapters; i++ {
		chs = append(chs, ebooktest.EPUBChapter{Title: fmt.Sprintf("Chapter %d", i), EpubType: "chapter", Paras: pgProse(fmt.Sprint(i), paras)})
	}
	return pgSeedEPUB(t, chs, nil)
}

func pgSeedEPUB(t *testing.T, chs []ebooktest.EPUBChapter, opt func(*ebooktest.EPUBOptions)) *rdrLab {
	t.Helper()
	o := ebooktest.EPUBOptions{Version: 3, Title: "The Pager", Authors: []string{"A. Writer"}, Chapters: chs}
	if opt != nil {
		opt(&o)
	}
	l := &rdrLab{textLab: newTextLab(t)}
	l.run(t)
	f, err := l.svc.AddUpload(context.Background(), l.book, l.ownerID, "pager.epub", bytes.NewReader(ebooktest.EPUB(t, o)))
	if err != nil {
		t.Fatal(err)
	}
	l.file = l.waitState(t, f.ID, "ready")
	if err := l.a.runner.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	ed, ok, err := l.svc.Store().CurrentEdition(context.Background(), l.file.ID)
	if err != nil || !ok {
		t.Fatalf("edition: %v %v", ok, err)
	}
	l.ed = ed
	if l.doc, err = ebook.ReadDoc(l.a.dir+"/text", ed.DocSHA256); err != nil {
		t.Fatal(err)
	}
	return l
}

// pgServe serves the app as the owner.
func pgServe(t *testing.T, l *rdrLab) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(l.owner)
		l.a.h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// pgOpen opens the reader and waits until its first layout is done.
func pgOpen(t *testing.T, b *cdp.Browser, srv *httptest.Server, l *rdrLab, w, h int, mobile bool, query string) *cdp.Tab {
	t.Helper()
	tab := b.NewTab(w, h, mobile, true)
	tab.Navigate(srv.URL + l.readPath(query))
	tab.WaitFor(`window.htmx !== undefined && document.querySelector("#reader.ts-reader--pages, #reader.ts-reader--scroll") !== null`, 8*time.Second)
	return tab
}

// pgFlow sets the person's flow setting through the real route.
func pgFlow(t *testing.T, l *rdrLab, flow string) {
	t.Helper()
	if code, _, _ := rsetPost(t, l, l.owner, true, "flow", flow); code != http.StatusNoContent {
		t.Fatalf("flow %s: %d", flow, code)
	}
}

// Page helpers, as JavaScript: the part's box, the sentences on the page, the page index.
const pgJS = `
const pgBox = () => document.querySelector("section[data-part]");
const pgStep = () => { const s = getComputedStyle(document.getElementById("reader")); return parseFloat(s.getPropertyValue("--ts-cols")) * (parseFloat(s.getPropertyValue("--ts-col")) + parseFloat(s.getPropertyValue("--ts-gap"))); };
const pgPage = () => Math.round(pgBox().scrollLeft / pgStep());
const pgOnPage = (el) => { const b = pgBox().getBoundingClientRect(); return [...el.getClientRects()].some(r => r.right > b.left + 1 && r.left < b.right - 1); };
const pgShown = () => [...document.querySelectorAll(".ts-s")].filter(pgOnPage).map(e => e.dataset.sid);
const pgInView = (el) => { const b = document.getElementById("reader").getBoundingClientRect(); return [...el.getClientRects()].some(r => r.bottom > b.top && r.top < b.bottom); };
`

func pgEval(tab *cdp.Tab, expr string, out any) {
	tab.Eval(`(() => {`+pgJS+` return (`+expr+`); })()`, out)
}

func pgWaitFor(tab *cdp.Tab, cond string) {
	tab.WaitFor(`(() => {`+pgJS+` return (`+cond+`); })()`, 5*time.Second)
}

func pgKey(t *testing.T, tab *cdp.Tab, key, code string, vk int, text string, mods int) {
	t.Helper()
	for _, typ := range []string{"keyDown", "keyUp"} {
		p := map[string]any{"type": typ, "key": key, "code": code, "windowsVirtualKeyCode": vk, "modifiers": mods}
		if typ == "keyDown" && text != "" {
			p["text"] = text
		}
		if err := tab.Call("Input.dispatchKeyEvent", p, nil); err != nil {
			t.Fatal(err)
		}
	}
}

func pgNext(t *testing.T, tab *cdp.Tab) { pgKey(t, tab, "ArrowRight", "ArrowRight", 39, "", 0) }
func pgPrev(t *testing.T, tab *cdp.Tab) { pgKey(t, tab, "ArrowLeft", "ArrowLeft", 37, "", 0) }

// pgTouch sends one touch gesture: down at (x0, y0), up at (x1, y1).
func pgTouch(t *testing.T, tab *cdp.Tab, x0, y0, x1, y1 float64) {
	t.Helper()
	if err := tab.Call("Emulation.setTouchEmulationEnabled", map[string]any{"enabled": true, "maxTouchPoints": 1}, nil); err != nil {
		t.Fatal(err)
	}
	send := func(typ string, x, y float64) {
		points := []map[string]any{{"x": x, "y": y}}
		if typ == "touchEnd" {
			points = []map[string]any{}
		}
		if err := tab.Call("Input.dispatchTouchEvent", map[string]any{"type": typ, "touchPoints": points}, nil); err != nil {
			t.Fatal(err)
		}
	}
	send("touchStart", x0, y0)
	send("touchMove", (x0+x1)/2, (y0+y1)/2)
	send("touchMove", x1, y1)
	send("touchEnd", x1, y1)
}

// pgSaved reads back the reading place the server holds for the owner.
func pgSaved(t *testing.T, l *rdrLab) (sid string, off int, ok bool) {
	t.Helper()
	p, found, err := l.a.srv.progress.Reading(context.Background(), l.ownerID, l.book.ID)
	if err != nil {
		t.Fatal(err)
	}
	return p.SID, p.Off, found
}

// pgSave makes the reader send its place now (the page-hide beacon) and waits for the server to hold a place
// different from before.
func pgSave(t *testing.T, tab *cdp.Tab, l *rdrLab, before string) string {
	t.Helper()
	tab.Eval(`window.dispatchEvent(new Event("pagehide"))`, nil)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if sid, _, ok := pgSaved(t, l); ok && sid != before {
			return sid
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the place was not saved")
	return ""
}

func TestPaginatorTurn(t *testing.T) {
	b := chromium(t)
	l := pgSeed(t, 2, 40)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	for _, vp := range pgViewports {
		t.Run(vp.name, func(t *testing.T) {
			tab := pgOpen(t, b, srv, l, vp.w, vp.h, vp.mobile, "ch=2")
			var cols string
			tab.Eval(`getComputedStyle(document.getElementById("reader")).getPropertyValue("--ts-cols")`, &cols)
			if want := map[bool]string{true: "1", false: "2"}[vp.mobile]; cols != want {
				t.Errorf("%s columns at %d px, want %s", cols, vp.w, want)
			}
			var count int
			pgEval(tab, `Math.ceil(pgBox().scrollWidth / pgStep())`, &count)
			if count < 3 {
				t.Fatalf("only %d pages", count)
			}
			// No line of text lies across a page edge, so nothing is clipped between pages.
			var clipped []string
			pgEval(tab, `[...document.querySelectorAll(".ts-s")].filter(el => [...el.getClientRects()].some(r => {
				const b = pgBox().getBoundingClientRect(), step = pgStep();
				const a = Math.floor((r.left - b.left + pgBox().scrollLeft + 2) / step), z = Math.floor((r.right - b.left + pgBox().scrollLeft - 2) / step);
				return a !== z; })).map(e => e.dataset.sid)`, &clipped)
			if len(clipped) > 0 {
				t.Errorf("lines cut by a page edge: %v", clipped)
			}
			var seen []string
			for i := 0; i < 5; i++ {
				var shown []string
				pgEval(tab, `pgShown()`, &shown)
				if len(shown) == 0 {
					t.Fatalf("page %d shows nothing", i)
				}
				if i > 0 && strings.Join(shown, ",") == seen[len(seen)-1] {
					t.Errorf("turn %d left the same text on screen", i)
				}
				seen = append(seen, strings.Join(shown, ","))
				pgNext(t, tab)
				pgWaitFor(tab, fmt.Sprintf(`pgPage() === %d`, i+1))
			}
			// Back again: the first page returns.
			for i := 5; i > 0; i-- {
				pgPrev(t, tab)
				pgWaitFor(tab, fmt.Sprintf(`pgPage() === %d`, i-1))
			}
			var shown []string
			pgEval(tab, `pgShown()`, &shown)
			if strings.Join(shown, ",") != seen[0] {
				t.Error("going back did not return to the first page")
			}
		})
	}
}

func TestPaginatorResumeAcrossSizes(t *testing.T) {
	b := chromium(t)
	l := pgSeed(t, 2, 40)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	small := pgOpen(t, b, srv, l, 390, 844, true, "ch=2")
	for i := 0; i < 4; i++ {
		pgNext(t, small)
		pgWaitFor(small, fmt.Sprintf(`pgPage() === %d`, i+1))
	}
	sid := pgSave(t, small, l, "")
	sentenceOnPage := func(tab *cdp.Tab) bool {
		var on bool
		pgEval(tab, fmt.Sprintf(`pgOnPage(document.querySelector('.ts-s[data-sid="%s"]'))`, sid), &on)
		return on
	}
	if !sentenceOnPage(small) {
		t.Fatalf("the saved sentence %s is not on the page it was saved from", sid)
	}
	wide := pgOpen(t, b, srv, l, 1440, 900, false, "")
	if !sentenceOnPage(wide) {
		t.Errorf("at 1440 the saved sentence %s is not on screen", sid)
	}
	// Other text sizes, with a reload each, and then live from the sheet.
	for _, size := range []string{"18", "22"} {
		rsetPost(t, l, l.owner, true, "size", size)
		tab := pgOpen(t, b, srv, l, 390, 844, true, "")
		if !sentenceOnPage(tab) {
			t.Errorf("at %s px the saved sentence %s is not on screen", size, sid)
		}
	}
	rsetPost(t, l, l.owner, true, "size", "20")
	tab := pgOpen(t, b, srv, l, 390, 844, true, "")
	tab.Eval(`document.querySelector(".ts-aa").click()`, nil)
	tab.WaitFor(`document.querySelector("#sheet form[data-ts-reader-settings]") !== null`, 5*time.Second)
	tab.Eval(`document.getElementById("rs-size-26") ? document.getElementById("rs-size-26").click() : document.getElementById("rs-size-22").click()`, nil)
	tab.WaitFor(`document.getElementById("reader").className.match(/size-(22|26)/) !== null`, 5*time.Second)
	time.Sleep(300 * time.Millisecond)
	if !sentenceOnPage(tab) {
		t.Errorf("after a live size change the saved sentence %s is not on screen", sid)
	}
}

func TestPaginatorSwitchFlow(t *testing.T) {
	b := chromium(t)
	l := pgSeed(t, 2, 40)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	tab := pgOpen(t, b, srv, l, 390, 844, true, "ch=2")
	for i := 0; i < 3; i++ {
		pgNext(t, tab)
		pgWaitFor(tab, fmt.Sprintf(`pgPage() === %d`, i+1))
	}
	var first string
	pgEval(tab, `pgShown()[0]`, &first)
	sel := fmt.Sprintf(`document.querySelector('.ts-s[data-sid="%s"]')`, first)
	tab.Eval(`document.querySelector(".ts-aa").click()`, nil)
	tab.WaitFor(`document.getElementById("rs-flow-scroll") !== null`, 5*time.Second)
	tab.Eval(`document.getElementById("rs-flow-scroll").click()`, nil)
	tab.WaitFor(`document.getElementById("reader").classList.contains("ts-reader--scroll")`, 5*time.Second)
	time.Sleep(300 * time.Millisecond)
	var ok bool
	pgEval(tab, `pgInView(`+sel+`)`, &ok)
	if !ok {
		t.Error("switching to scroll lost the place")
	}
	tab.Eval(`document.getElementById("rs-flow-pages").click()`, nil)
	tab.WaitFor(`document.getElementById("reader").classList.contains("ts-reader--pages")`, 5*time.Second)
	time.Sleep(300 * time.Millisecond)
	pgEval(tab, `pgOnPage(`+sel+`)`, &ok)
	if !ok {
		t.Error("switching back to pages lost the place")
	}
}

// pgLongChapter is one chapter of about 100,000 characters: three parts.
func pgLongChapter() []ebooktest.EPUBChapter {
	return []ebooktest.EPUBChapter{
		{Title: "Title Page", EpubType: "titlepage", Paras: []string{"The Pager"}},
		{Title: "Chapter 1", EpubType: "chapter", Paras: pgProse("1", 320)},
		{Title: "Chapter 2", EpubType: "chapter", Paras: pgProse("2", 12)},
	}
}

func TestPaginatorChapterEdge(t *testing.T) {
	b := chromium(t)
	l := pgSeedEPUB(t, pgLongChapter(), nil)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	ch := l.doc.Chapters[1]
	parts := rsParts(ch)
	if len(parts) < 2 {
		t.Fatalf("the chapter has %d parts", len(parts))
	}
	lastBlock := ch.Blocks[parts[0].To-1]
	lastSID := ebook.SentenceID(lastBlock.ID, len(lastBlock.Sentences)-1)
	firstBlock := ch.Blocks[parts[1].From]
	firstSID := ebook.SentenceID(firstBlock.ID, 0)
	tab := pgOpen(t, b, srv, l, 390, 844, true, "at="+lastSID)
	var part string
	tab.Eval(`document.querySelector("section[data-part]").dataset.part`, &part)
	if part != "1" {
		t.Fatalf("opened part %s", part)
	}
	var onLast bool
	pgEval(tab, fmt.Sprintf(`pgOnPage(document.querySelector('.ts-s[data-sid="%s"]'))`, lastSID), &onLast)
	if !onLast {
		t.Fatal("the last sentence of part 1 is not on screen")
	}
	// Forward until the next part is shown (the last sentence is on the last page or one before it).
	for i := 0; i < 8 && part != "2"; i++ {
		pgNext(t, tab)
		time.Sleep(200 * time.Millisecond)
		tab.Eval(`document.querySelector("section[data-part]").dataset.part`, &part)
	}
	if part != "2" {
		t.Fatal("turning forward never reached part 2")
	}
	var onFirst bool
	pgEval(tab, fmt.Sprintf(`pgPage() === 0 && pgOnPage(document.querySelector('.ts-s[data-sid="%s"]'))`, firstSID), &onFirst)
	if !onFirst {
		t.Error("part 2 does not open on its first page with its first sentence")
	}
	// Back across the edge: the last page of part 1 holds its last sentence.
	pgPrev(t, tab)
	tab.WaitFor(`document.querySelector("section[data-part]").dataset.part === "1"`, 5*time.Second)
	pgEval(tab, fmt.Sprintf(`pgOnPage(document.querySelector('.ts-s[data-sid="%s"]'))`, lastSID), &onLast)
	if !onLast {
		t.Error("going back did not land on the last page of part 1")
	}
}

func TestPaginatorLongChapter(t *testing.T) {
	b := chromium(t)
	l := pgSeedEPUB(t, pgLongChapter(), nil)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	for _, vp := range pgViewports {
		t.Run(vp.name, func(t *testing.T) {
			tab := pgOpen(t, b, srv, l, vp.w, vp.h, vp.mobile, "ch=2")
			var ms float64
			tab.Eval(`Math.max(...performance.getEntriesByName("ts-reader-lay").map(e => e.duration))`, &ms)
			t.Logf("layout of one chapter part (about %d characters): %.1f ms", pgPartUnits(l, 0), ms)
			if ms > 500 {
				t.Errorf("laying out a part took %.0f ms", ms)
			}
			var text int
			tab.Eval(`document.querySelector("section[data-part]").textContent.length`, &text)
			if text > 60000 {
				t.Errorf("the part holds %d characters of text", text)
			}
		})
	}
}

// pgPartUnits is the size of the first part of the long chapter.
func pgPartUnits(l *rdrLab, part int) int {
	ch := l.doc.Chapters[1]
	n := 0
	p := rsParts(ch)[part]
	for _, bl := range ch.Blocks[p.From:p.To] {
		n += rsUnits(bl.Text)
	}
	return n
}

func TestPaginatorImage(t *testing.T) {
	b := chromium(t)
	chs := []ebooktest.EPUBChapter{
		{Title: "Title Page", EpubType: "titlepage", Paras: []string{"Pictures"}},
		{Title: "One", EpubType: "chapter", Raw: "<p>" + pgProse("p", 1)[0] + `</p><img src="pic.png" alt="Lamp"/>` + "<p>" + pgProse("q", 1)[0] + "</p>"},
	}
	l := pgSeedEPUB(t, chs, func(o *ebooktest.EPUBOptions) {
		o.ManifestXML = `<item id="p" href="pic.png" media-type="image/png"/>`
		o.Extra = []ebooktest.Entry{{Name: "OEBPS/pic.png", Data: rsPNG(300, 3000)}} // a very tall picture
	})
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	for _, vp := range pgViewports {
		t.Run(vp.name, func(t *testing.T) {
			tab := pgOpen(t, b, srv, l, vp.w, vp.h, vp.mobile, "ch=2")
			tab.WaitFor(`document.querySelector(".ts-reader__image") !== null`, 5*time.Second)
			var fits bool
			pgEval(tab, `(() => { const i = document.querySelector(".ts-reader__image").getBoundingClientRect(), b = pgBox().getBoundingClientRect();
				return i.height <= b.height + 1 && i.width <= b.width + 1; })()`, &fits)
			if !fits {
				t.Error("the picture is taller or wider than a page")
			}
		})
	}
}

func TestPaginatorSaveBeacon(t *testing.T) {
	b := chromium(t)
	l := pgSeed(t, 2, 40)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	tab := pgOpen(t, b, srv, l, 390, 844, true, "ch=2")
	if _, _, ok := pgSaved(t, l); ok {
		t.Fatal("opening the book must not write a place")
	}
	for i := 0; i < 2; i++ {
		pgNext(t, tab)
		pgWaitFor(tab, fmt.Sprintf(`pgPage() === %d`, i+1))
	}
	// The debounce has not run; hiding the page sends the place by beacon.
	tab.Eval(`Object.defineProperty(document, "hidden", {value: true, configurable: true}); document.dispatchEvent(new Event("visibilitychange"))`, nil)
	sid := pgSave(t, tab, l, "")
	p, _, err := l.a.srv.progress.Reading(context.Background(), l.ownerID, l.book.ID)
	if err != nil || p.Quote == "" || p.CharPos <= 0 || p.ChapterIdx != 2 {
		t.Errorf("saved %+v (%v)", p, err)
	}
	if _, ok := pgChapterOf(sid); !ok {
		t.Errorf("sentence id %q", sid)
	}
	// The listening place is untouched.
	if pr, err := l.a.srv.progress.Get(context.Background(), l.ownerID, l.book.ID); err == nil && pr.ClientUpdatedAt != 0 {
		t.Error("reading wrote a listening place")
	}
}

func pgChapterOf(sid string) (int, bool) {
	c, _, _, ok := ebook.ParseSID(sid)
	return c, ok
}

func TestPaginatorOtherDevice(t *testing.T) {
	b := chromium(t)
	l := pgSeed(t, 2, 40)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	tab := pgOpen(t, b, srv, l, 390, 844, true, "ch=2")
	time.Sleep(300 * time.Millisecond) // the first look at the server's place
	// Another device reads further.
	sid, _ := l.sidIn(30)
	form := fmt.Sprintf("edition_id=%s&chapter_idx=2&sid=%s&off=0&quote=x&char_pos=5000&client_updated_at=%d&device_id=otherdevice&_csrf=%s",
		l.ed.ID, sid, time.Now().Add(time.Hour).UnixMilli(), l.a.tokenFor(t, l.owner))
	req, _ := http.NewRequest(http.MethodPost, srv.URL+"/reading/"+l.book.ID, strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	res, err := http.DefaultClient.Do(req)
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("other device save: %v %v", res, err)
	}
	tab.Eval(`Object.defineProperty(document, "hidden", {value: false, configurable: true}); document.dispatchEvent(new Event("visibilitychange"))`, nil)
	tab.WaitFor(`[...document.querySelectorAll("#toasts .ts-toast")].some(t => t.textContent.includes("You read further on"))`, 5*time.Second)
	var buttons []string
	tab.Eval(`[...document.querySelectorAll("#toasts .ts-toast button")].map(b => b.textContent)`, &buttons)
	if strings.Join(buttons, "|") != "Go there|Stay here" {
		t.Errorf("toast buttons %v", buttons)
	}
	// It never jumps by itself.
	var page string
	tab.Eval(`location.search`, &page)
	if strings.Contains(page, "at=") {
		t.Errorf("the page moved by itself: %s", page)
	}
	tab.Eval(`[...document.querySelectorAll("#toasts .ts-toast button")].find(b => b.textContent === "Stay here").click()`, nil)
	tab.WaitFor(`document.querySelectorAll("#toasts .ts-toast").length === 0`, 3*time.Second)
}

// With the flow left on automatic, a phone gets pages and a desktop gets scroll; the same book, the same place.
func TestPaginatorAutoFlow(t *testing.T) {
	b := chromium(t)
	l := pgSeed(t, 2, 40)
	srv := pgServe(t, l)
	for _, vp := range pgViewports {
		t.Run(vp.name, func(t *testing.T) {
			tab := pgOpen(t, b, srv, l, vp.w, vp.h, vp.mobile, "ch=2")
			var pages bool
			tab.Eval(`document.getElementById("reader").classList.contains("ts-reader--pages")`, &pages)
			if pages != vp.mobile {
				t.Errorf("pages = %v at %d px", pages, vp.w)
			}
			// In scroll, the key moves a screen and the reader still reports a place.
			if !pages {
				pgNext(t, tab)
				tab.WaitFor(`document.getElementById("reader").scrollTop > 300`, 5*time.Second)
			}
		})
	}
}
