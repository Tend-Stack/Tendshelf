//go:build dev

package web

import (
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
)

// TestReaderDropCapOnlyOnProse: a dedication in short lines and an epigraph get no big first letter and no
// first-line indent; a chapter that opens with prose keeps its drop cap.
func TestReaderDropCapOnlyOnProse(t *testing.T) {
	b := chromium(t)
	chs := []ebooktest.EPUBChapter{
		{Title: "Title Page", EpubType: "titlepage", Paras: []string{"The Pager"}},
		{Title: "Dedication", EpubType: "chapter", Paras: []string{"The", "dedication", "of this book", "to my mother"}},
		{Title: "One", EpubType: "chapter", Paras: pgProse("a", 2)},
	}
	l := pgSeedEPUB(t, chs, nil)
	srv := pgServe(t, l)
	for _, c := range []struct {
		ch             string
		drop, indented bool
	}{{"ch=2", false, false}, {"ch=3", true, true}} {
		tab := pgOpen(t, b, srv, l, 390, 844, true, c.ch)
		var drops int
		tab.Eval(`document.querySelectorAll(".ts-reader__drop").length`, &drops)
		if (drops > 0) != c.drop {
			t.Errorf("%s: %d drop caps, want %v", c.ch, drops, c.drop)
		}
		var indent string
		tab.Eval(`getComputedStyle(document.querySelectorAll("#reader p[data-c]")[1]).textIndent`, &indent)
		if (indent != "0px") != c.indented {
			t.Errorf("%s: second paragraph indent %q, want indented %v", c.ch, indent, c.indented)
		}
		if p := rfixShot("dropcap-" + c.ch + ".png"); p != "" {
			tab.Screenshot(p)
		}
		tab.Close()
	}
}
