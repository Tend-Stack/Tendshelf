//go:build dev

package web

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// Footnotes, marks, bookmarks and search hits in Chromium (RD3). Needs TENDSHELF_TEST_BROWSER.

func TestReaderNoteBrowser(t *testing.T) {
	b := chromium(t)
	chs := []ebooktest.EPUBChapter{
		{Title: "Title Page", EpubType: "titlepage", Paras: []string{"Notes"}},
		{Title: "One", EpubType: "chapter", Raw: `<p>Start<a epub:type="noteref" href="ch3.xhtml#n1">1</a> of the chapter. ` + pgProse("a", 1)[0] + `</p>` +
			`<p>See <a href="ch3.xhtml#t">the other chapter</a> now.</p>` + rawParas(pgProse("b", 10))},
		{Title: "Two", EpubType: "chapter", Raw: `<p id="t">` + pgProse("t", 1)[0] + `</p><aside epub:type="footnote" id="n1"><p>Footnote words.</p></aside>`},
	}
	l := pgSeedEPUB(t, chs, nil)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	tab := pgOpen(t, b, srv, l, 390, 844, true, "ch=2")
	open := `document.querySelector("dialog.ts-readernote--note[open]")`
	// Click the footnote link: the popup opens, focus moves into it, and it says where to go and how to close.
	tab.Eval(`document.querySelector("a[data-note-href]").focus(); document.querySelector("a[data-note-href]").click()`, nil)
	tab.WaitFor(open+` !== null`, 5*time.Second)
	var got struct {
		Text, Link string
		Inside     bool
	}
	tab.Eval(`({Text: `+open+`.textContent, Link: `+open+`.querySelector("a").textContent, Inside: !!document.activeElement.closest("dialog")})`, &got)
	if !strings.Contains(got.Text, "Footnote words.") || got.Link != "Go to the note" || !got.Inside {
		t.Errorf("popup: %+v", got)
	}
	var location string
	tab.Eval(`location.pathname + location.search`, &location)
	if strings.Contains(location, "ch=3") {
		t.Error("opening a footnote moved the page")
	}
	// Escape closes it and focus returns to the link.
	pgKey(t, tab, "Escape", "Escape", 27, "", 0)
	tab.WaitFor(open+` === null`, 5*time.Second)
	var back bool
	tab.Eval(`document.activeElement.hasAttribute("data-note-href")`, &back)
	if !back {
		t.Error("focus did not return to the footnote link")
	}
	// The backdrop closes it too.
	tab.Eval(`document.querySelector("a[data-note-href]").click()`, nil)
	tab.WaitFor(open+` !== null`, 5*time.Second)
	tab.Eval(`document.querySelector("dialog.ts-readernote--note").click()`, nil)
	tab.WaitFor(open+` === null`, 5*time.Second)

	// A link to another place jumps, and the next page offers the way back to the sentence that held it.
	var from string
	tab.Eval(`document.querySelector("a.ts-reader__link:not([data-note-href])").closest(".ts-s").dataset.sid`, &from)
	tab.Eval(`document.querySelector("a.ts-reader__link:not([data-note-href])").click()`, nil)
	tab.WaitFor(`document.querySelector(".ts-reader__back") !== null`, 8*time.Second)
	var chip struct{ Text, Href string }
	tab.Eval(`({Text: document.querySelector(".ts-reader__back").textContent, Href: document.querySelector(".ts-reader__back").getAttribute("href")})`, &chip)
	if chip.Text != "Back to where I was" || !strings.Contains(chip.Href, "at="+from) {
		t.Errorf("back chip: %+v, want a link to %s", chip, from)
	}
	tab.Eval(`document.querySelector(".ts-reader__back").click()`, nil)
	tab.WaitFor(`location.search.includes("at=`+from+`")`, 8*time.Second)
}

// rnSelect selects chars a to z of a sentence, as a person's drag would.
func rnSelect(tab *cdp.Tab, sid string, a, z int) {
	tab.Eval(fmt.Sprintf(`(() => { const s = document.querySelector('.ts-s[data-sid="%s"]'); const w = document.createTreeWalker(s, NodeFilter.SHOW_TEXT); const nodes = []; for (let n = w.nextNode(); n; n = w.nextNode()) nodes.push(n);
 const spot = (k) => { for (const n of nodes) { if (k <= n.length) return [n, k]; k -= n.length; } };
 const r = new Range(); r.setStart(...spot(%d)); r.setEnd(...spot(%d));
 getSelection().removeAllRanges(); getSelection().addRange(r); return true; })()`, sid, a, z), nil)
	tab.WaitFor(`!document.getElementById("reader-marks").hidden`, 5*time.Second)
}

func TestReaderMarksBrowser(t *testing.T) {
	b := chromium(t)
	l := pgSeedEPUB(t, pgLongChapter(), nil)
	ch := l.doc.Chapters[1]
	parts := rsParts(ch)
	if len(parts) < 2 {
		t.Fatalf("%d parts", len(parts))
	}
	// A sentence in the second part, one that does not start its block: offsets must add the sentence start.
	var blk ebook.Block
	for _, bl := range ch.Blocks[parts[1].From:parts[1].To] {
		if len(bl.Sentences) > 2 {
			blk = bl
			break
		}
	}
	if blk.ID == "" {
		t.Fatal("no block with three sentences in part 2")
	}
	units := []rune(blk.Text)
	for i, c := range []struct{ flow, colour string }{{"pages", "green"}, {"scroll", "pink"}} {
		t.Run(c.flow, func(t *testing.T) {
			pgFlow(t, l, c.flow)
			srv := pgServe(t, l)
			sid := ebook.SentenceID(blk.ID, 1)
			shown := "pgOnPage"
			if c.flow == "scroll" {
				shown = "pgInView"
			}
			tab := pgOpen(t, b, srv, l, 390, 844, true, "at="+sid)
			isShown := func() (ok bool) {
				pgEval(tab, fmt.Sprintf(`%s(document.querySelector('.ts-s[data-sid="%s"]'))`, shown, sid), &ok)
				return ok
			}
			if !isShown() {
				t.Fatal("the sentence is not on screen")
			}
			var page int
			pgEval(tab, `pgPage()`, &page)
			// Select inside the sentence and pick the colour.
			rnSelect(tab, sid, 2, 9)
			tab.Eval(fmt.Sprintf(`document.querySelector('#reader-marks [data-ts-mark="highlight"][data-colour="%s"]').click()`, c.colour), nil)
			tab.WaitFor(fmt.Sprintf(`document.querySelector('.ts-hl[data-colour="%s"]') !== null`, c.colour), 8*time.Second)
			hls, err := l.a.srv.progress.Highlights(context.Background(), l.ownerID, l.book.ID)
			if err != nil || len(hls) != 2*i+1 {
				t.Fatalf("highlights: %v %v", hls, err)
			}
			h := rnWithColour(hls, c.colour)
			start := blk.Sentences[1][0] + 2
			if h.StartSID != sid || h.StartOff != start || h.EndOff != start+7 || h.Colour != c.colour || h.Chapter != 2 {
				t.Errorf("saved %+v, want offsets %d to %d in %s", h, start, start+7, sid)
			}
			if want := string(units[start : start+7]); h.Quote != want {
				t.Errorf("quote %q, want %q", h.Quote, want)
			}
			// The swap drew the block again and the person is still on the same page.
			var now int
			pgEval(tab, `pgPage()`, &now)
			if c.flow == "pages" && now != page {
				t.Errorf("page %d became %d after the highlight", page, now)
			}
			if !isShown() {
				t.Error("the highlight swap moved the sentence off screen")
			}
			// A note: the dialog is the page's own, Save sends it, Cancel does not.
			rnSelect(tab, sid, 12, 18)
			tab.Eval(`document.querySelector('#reader-marks [data-ts-mark="note"]').click()`, nil)
			tab.WaitFor(`document.getElementById("reader-note-dialog").open`, 5*time.Second)
			tab.Eval(`document.querySelector('#reader-note-dialog button[value="cancel"]').click()`, nil)
			tab.WaitFor(`!document.getElementById("reader-note-dialog").open`, 5*time.Second)
			rnSelect(tab, sid, 12, 18)
			tab.Eval(`document.querySelector('#reader-marks [data-ts-mark="note"]').click()`, nil)
			tab.WaitFor(`document.getElementById("reader-note-dialog").open`, 5*time.Second)
			tab.Eval(`document.querySelector('#reader-note-dialog textarea').value = "a thought"; document.querySelector('#reader-note-dialog button[value="save"]').click()`, nil)
			tab.WaitFor(fmt.Sprintf(`document.querySelectorAll(".ts-notedot").length === %d`, i+1), 8*time.Second)
			if hls, _ = l.a.srv.progress.Highlights(context.Background(), l.ownerID, l.book.ID); len(hls) != 2*i+2 || !rnHasNote(hls, "a thought") {
				t.Errorf("after the note: %+v", hls)
			}
			// A bookmark keeps the first sentence on screen.
			tab.Eval(`document.querySelector('.ts-readerbar [data-ts-mark="bookmark"]').click()`, nil)
			var saved string
			for end := time.Now().Add(6 * time.Second); time.Now().Before(end) && saved == ""; time.Sleep(150 * time.Millisecond) {
				if bms, _ := l.a.srv.progress.TextBookmarks(context.Background(), l.ownerID, l.book.ID); len(bms) > i {
					saved = bms[0].SID
				}
			}
			var visible bool
			pgEval(tab, fmt.Sprintf(`%s(document.querySelector('.ts-s[data-sid="%s"]'))`, shown, saved), &visible)
			if saved == "" || !visible {
				t.Errorf("bookmark %q (on screen %v)", saved, visible)
			}
		})
	}
}

func TestReaderSearchBrowser(t *testing.T) {
	b := chromium(t)
	chs := []ebooktest.EPUBChapter{
		{Title: "Title Page", EpubType: "titlepage", Paras: []string{"Find"}},
		{Title: "One", EpubType: "chapter", Raw: rawParas(pgProse("a", 12)) + `<p>The hidden zebra crossing is here. ` + pgProse("z", 1)[0] + `</p>` + rawParas(pgProse("b", 12))},
	}
	l := pgSeedEPUB(t, chs, nil)
	pgFlow(t, l, "pages")
	srv := pgServe(t, l)
	tab := pgOpen(t, b, srv, l, 390, 844, true, "ch=2")
	// The search link in the bar opens the sheet; the form sends the question through htmx and lists the hit.
	tab.Eval(`document.querySelector('.ts-readerbar a[href*="sheet=search"]').click()`, nil)
	tab.WaitFor(`document.getElementById("reader-q") !== null`, 5*time.Second)
	tab.Eval(`document.getElementById("reader-q").value = "ZEBRA"; document.getElementById("reader-q").form.requestSubmit()`, nil)
	tab.WaitFor(`document.querySelector("#reader-hits mark") !== null`, 8*time.Second)
	// Following the hit lands on its page with the words marked in the highlight registry.
	tab.Eval(`document.querySelector("#reader-hits a").click()`, nil)
	tab.WaitFor(`location.search.includes("hit=5") && document.querySelector("#reader.ts-reader--pages") !== null`, 8*time.Second)
	pgWaitFor(tab, `CSS.highlights.has("ts-hit") && [...CSS.highlights.get("ts-hit")].map(r => r.toString()).join("") === "zebra" && [...CSS.highlights.get("ts-hit")].every(r => [...document.querySelectorAll(".ts-s")].filter(pgOnPage).some(s => s.contains(r.startContainer)))`)
}
