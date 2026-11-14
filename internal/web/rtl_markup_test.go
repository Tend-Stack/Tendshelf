package web

import (
	"net/http"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
)

// rtlEdgeLTR are the two edge buttons of a left-to-right book, byte for byte as they were before right-to-left
// support.
const rtlEdgeLTR = `<button type="button" class="ts-edge ts-edge--prev" data-edge="-1" aria-label="Previous page"></button> ` +
	`<button type="button" class="ts-edge ts-edge--next" data-edge="1" aria-label="Next page"></button>`

// rtlTagOf is the opening tag of the element with id in s, "" when there is none.
func rtlTagOf(s, id string) string {
	i := strings.Index(s, ` id="`+id+`"`)
	if i < 0 {
		return ""
	}
	start := strings.LastIndex(s[:i], "<")
	end := strings.Index(s[i:], ">")
	if start < 0 || end < 0 {
		return ""
	}
	return s[start : i+end+1]
}

func rtlGet(t *testing.T, l *rsLab, path string, htmx bool) string {
	t.Helper()
	rec := l.get(l.owner, path, htmx)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET %s: %d", path, rec.Code)
	}
	return rec.Body.String()
}

func TestRTLReaderMarkup(t *testing.T) {
	paras := append(ebooktest.ArabicParas(8), "A short English paragraph with plenty of Latin letters in it.")
	l := rsSeed(t, ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "Arabic", Authors: []string{"A. Writer"}, Lang: "ar",
		Chapters: []ebooktest.EPUBChapter{{Title: "One", EpubType: "chapter", Paras: paras}}}))
	if l.doc.Meta.Dir != "rtl" {
		t.Fatalf("fixture is not right to left: Meta.Dir %q", l.doc.Meta.Dir)
	}
	pageSet := map[string]string{
		"page":          rtlGet(t, l, l.page(""), false),
		"offline shell": rtlGet(t, l, "/offline/books/"+l.book.ID+"/read", false),
	}
	for name, s := range pageSet {
		root := rtlTagOf(s, "reader")
		if !strings.Contains(root, ` lang="ar"`) || !strings.Contains(root, ` dir="rtl"`) {
			t.Errorf("%s: #reader lacks lang=ar and dir=rtl: %s", name, root)
		}
		if strings.Index(root, ` lang="ar"`) > strings.Index(root, ` dir="rtl"`) {
			t.Errorf("%s: dir should follow lang", name)
		}
		next := `<button type="button" class="ts-edge ts-edge--next" data-edge="-1" aria-label="Previous page"></button>`
		prev := `<button type="button" class="ts-edge ts-edge--prev" data-edge="1" aria-label="Next page"></button>`
		if !strings.Contains(s, next+" "+prev) {
			t.Errorf("%s: edge buttons not in right-to-left order", name)
		}
		if strings.Contains(s, rtlEdgeLTR) {
			t.Errorf("%s: left-to-right edge markup present", name)
		}
		if !strings.Contains(s, `class="ts-readdock__ruler" dir="rtl"`) {
			t.Errorf("%s: ruler lacks dir=rtl", name)
		}
	}
	frag := rtlGet(t, l, "/hx/books/"+l.book.ID+"/read/chapters/1", true)
	if !strings.Contains(frag, "Latin letters") {
		t.Fatal("fragment lacks the English paragraph")
	}
	var ltr, withDir int
	for _, p := range strings.Split(frag, "<p ")[1:] {
		tag := p[:strings.Index(p, ">")]
		if strings.Contains(p, "Latin letters") {
			if !strings.Contains(tag, ` dir="ltr"`) {
				t.Errorf("English paragraph lacks dir=ltr: %s", tag)
			}
			ltr++
		} else if strings.Contains(tag, " dir=") {
			withDir++
		}
	}
	if ltr != 1 || withDir != 0 {
		t.Errorf("ltr paragraphs %d (want 1), Arabic paragraphs with a dir %d (want 0)", ltr, withDir)
	}
}

func TestRTLLTRMarkupUnchanged(t *testing.T) {
	l := rsSeed(t, ebooktest.EPUB(t, ebooktest.EPUBOptions{Version: 3, Title: "English", Authors: []string{"A. Writer"}, Lang: "en",
		Chapters: []ebooktest.EPUBChapter{{Title: "One", EpubType: "chapter", Paras: []string{
			"The first paragraph is plain English and it runs on for a little while.", "A second one follows it."}}}}))
	if l.doc.Meta.Dir != "" {
		t.Fatalf("English book has Meta.Dir %q", l.doc.Meta.Dir)
	}
	for name, s := range map[string]string{
		"page":          rtlGet(t, l, l.page(""), false),
		"offline shell": rtlGet(t, l, "/offline/books/"+l.book.ID+"/read", false),
		"fragment":      rtlGet(t, l, "/hx/books/"+l.book.ID+"/read/chapters/1", true),
	} {
		if strings.Contains(s, " dir=") {
			t.Errorf("%s of a left-to-right book carries a dir attribute", name)
		}
		if name != "fragment" && !strings.Contains(s, rtlEdgeLTR) {
			t.Errorf("%s: edge markup changed", name)
		}
	}
}
