package web

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
)

// skSeed is a four-chapter book of plain ASCII text, so a character count is a byte count.
func skSeed(t *testing.T) *rsLab {
	t.Helper()
	chs := []ebooktest.EPUBChapter{{Title: "Title Page", EpubType: "titlepage", Paras: []string{"Skim Book"}}}
	for i := 1; i <= 4; i++ {
		chs = append(chs, ebooktest.EPUBChapter{Title: fmt.Sprintf("Chapter %d", i), EpubType: "chapter",
			Paras: []string{fmt.Sprintf("Opening words of chapter %d with a lantern of number %d.", i, i*111), "A second paragraph follows here."}})
	}
	return rsSeed(t, ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "Skim Book", Authors: []string{"A. Writer"}, Chapters: chs}))
}

// skBlockAt names the block that holds character want of the book, counted straight over the document.
func skBlockAt(d *ebook.Doc, want int) ebook.Block {
	for _, c := range d.Chapters {
		for _, b := range c.Blocks {
			if want < len(b.Text) {
				return b
			}
			want -= len(b.Text)
		}
	}
	return ebook.Block{}
}

func TestSkimStart(t *testing.T) {
	l := skSeed(t)
	total := 0
	for _, c := range l.doc.Chapters {
		for _, b := range c.Blocks {
			total += len(b.Text)
		}
	}
	at := func(q string) string {
		t.Helper()
		rec := l.get(l.owner, l.page(q), false)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %q: %d", q, rec.Code)
		}
		return rsAttr(rsReaderText(rec.Body.String()), "data-at")
	}
	for _, bp := range []int{0, 1, 2500, 5000, 7777, 9999, 10000} {
		b := skBlockAt(l.doc, min(bp*total/10000, total-1))
		if got := at(fmt.Sprintf("bp=%d", bp)); got != ebook.SentenceID(b.ID, 0) {
			t.Errorf("bp=%d: opens at %q, want the start of block %s", bp, got, b.ID)
		}
	}
	// bp wins over ch: the chapter in the address is only the bar's guess.
	b := skBlockAt(l.doc, 5000*total/10000)
	if got := at("ch=2&bp=5000"); got != ebook.SentenceID(b.ID, 0) {
		t.Errorf("ch=2&bp=5000 opens at %q", got)
	}
	// Junk is ignored: the page opens as if bp were absent (here chapter 3 by ch, with no sentence).
	for _, junk := range []string{"x", "-1", "10001", "1.5", ""} {
		body := l.get(l.owner, l.page("ch=3&bp="+junk), false).Body.String()
		if !strings.Contains(body, `id="reader-ch-3"`) || rsAttr(rsReaderText(body), "data-at") != "" {
			t.Errorf("bp=%q was not ignored", junk)
		}
	}
	// An explicit part keeps its chapter: bp is ignored beside it.
	if body := l.get(l.owner, l.page("ch=3&part=1&bp=9000"), false).Body.String(); !strings.Contains(body, `id="reader-ch-3"`) || rsAttr(rsReaderText(body), "data-at") != "" {
		t.Error("part= should keep ch=3 and ignore bp")
	}
	// at wins over bp.
	sid := ebook.SentenceID(l.doc.Chapters[1].Blocks[0].ID, 0)
	if got := at("at=" + sid + "&bp=9000"); got != sid {
		t.Errorf("at must win over bp, got %q", got)
	}
	// The bar, its tip and the chapter list are on the page; the script shows them.
	body := l.get(l.owner, l.page("ch=2"), false).Body.String()
	for _, want := range []string{`id="reader-skim"`, `id="reader-skim-tip"`, `data-skim=`, `class="ts-skim"`} {
		if !strings.Contains(body, want) {
			t.Errorf("page misses %q", want)
		}
	}
	// Without the right to read the route is still 404.
	if rec := l.get(l.noRead, l.page("bp=5000"), false); rec.Code != http.StatusNotFound {
		t.Errorf("without can_read: %d", rec.Code)
	}
}
