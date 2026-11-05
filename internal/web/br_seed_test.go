//go:build dev

package web

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// M5 RD4 helpers (prefix br): one synthetic book that shows every reader state, shared by the accessibility
// audit, the captures and the layout-time test. The words are invented here; nothing comes from a real book.

// brChapters is a title page, a chapter with a footnote link and a link to another chapter, the chapter that
// holds the footnote and the target, and a third chapter. Each chapter is long enough to fill a few pages.
func brChapters() []ebooktest.EPUBChapter {
	return []ebooktest.EPUBChapter{
		{Title: "Title Page", EpubType: "titlepage", Paras: []string{"The Lantern Keeper"}},
		{Title: "The Harbour Road", EpubType: "chapter", Raw: `<p>The road began at the harbour wall<a epub:type="noteref" href="ch3.xhtml#n1">1</a> and ran past the lantern house. ` +
			pgProse("a", 1)[0] + `</p><p>See <a href="ch3.xhtml#t">the orchard chapter</a> for the rest. ` + pgProse("l", 1)[0] + `</p>` + rawParas(pgProse("b", 14))},
		{Title: "The Orchard", EpubType: "chapter", Raw: `<p id="t">` + pgProse("t", 1)[0] + `</p>` + rawParas(pgProse("c", 10)) +
			`<aside epub:type="footnote" id="n1"><p>An invented footnote about the harbour wall.</p></aside>`},
		{Title: "The Last Light", EpubType: "chapter", Paras: pgProse("d", 12)},
	}
}

// brSeed builds the reader lab: the book above, for the owner four highlights (one in each colour, one of them
// with a note) and a bookmark, and the admin set to scroll so both flows can be opened with one lab.
func brSeed(t *testing.T) *rdrLab {
	t.Helper()
	l := pgSeedEPUB(t, brChapters(), func(o *ebooktest.EPUBOptions) { o.Title = "The Lantern Keeper" })
	pgFlow(t, l, "pages")
	if code, _, _ := rsetPost(t, l, l.admin, true, "flow", "scroll"); code != http.StatusNoContent {
		t.Fatalf("admin flow: %d", code)
	}
	// Four different paragraphs, past the first two (they hold the footnote and the chapter link).
	var sids []string
	for _, b := range l.doc.Chapters[rdrCh-1].Blocks {
		if len(b.Sentences) > 0 {
			sids = append(sids, ebook.SentenceID(b.ID, 0))
		}
	}
	if len(sids) < 8 {
		t.Fatalf("%d paragraphs in the chapter", len(sids))
	}
	sids = sids[3:]
	post := "/hx/books/" + l.book.ID + "/highlights"
	for i, colour := range []string{"yellow", "green", "blue", "pink"} {
		form := url.Values{"edition": {l.ed.ID}, "start_sid": {sids[i]}, "start_off": {"0"}, "end_sid": {sids[i]}, "end_off": {"20"}, "colour": {colour}, "client_id": {"brhighlight" + colour}}
		if i == 3 {
			form.Set("note", "A thought worth keeping.")
		}
		if rec := l.hx(t, http.MethodPost, post, form, l.owner); rec.Code != http.StatusOK {
			t.Fatalf("highlight %s: %d %s", colour, rec.Code, rec.Body.String())
		}
	}
	bm := url.Values{"edition_id": {l.ed.ID}, "sid": {sids[5]}}
	if rec := l.hx(t, http.MethodPost, "/books/"+l.book.ID+"/bookmarks", bm, l.owner); rec.Code != http.StatusOK {
		t.Fatalf("bookmark: %d", rec.Code)
	}
	return l
}

// brFirstSID is the first sentence of the book's second chapter (the one with the footnote link).
func brFirstSID(l *rdrLab) string {
	for _, b := range l.doc.Chapters[1].Blocks {
		if len(b.Sentences) > 0 {
			return ebook.SentenceID(b.ID, 0)
		}
	}
	return ""
}

// brPath is the reader address for a query.
func brPath(l *rdrLab, q ...string) string {
	return l.readPath(strings.Join(q, "&"))
}

// brSheetOpen waits for the sheet a page was opened with and for its slide to end.
func brSheetOpen(t *testing.T, tab *cdp.Tab, e *a11yEnv) {
	tab.WaitFor(`document.querySelector("#sheet .ts-sheet") !== null`, 8*time.Second)
	time.Sleep(450 * time.Millisecond)
}
