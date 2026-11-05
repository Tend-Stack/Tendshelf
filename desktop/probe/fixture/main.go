// Command fixture writes the synthetic EPUB the exit run reads in the desktop window (M6 D7).
// The text is made up here; no real book is used. Usage: fixture OUTFILE.epub
package main

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook/ebooktest"
)

// prose makes n paragraphs of invented sentences, enough to fill several pages.
func prose(tag string, n int) string {
	words := []string{"lantern", "harbour", "orchard", "winter", "copper", "river", "meadow", "candle"}
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteString("<p>")
		for j := 0; j < 5; j++ {
			fmt.Fprintf(&sb, "Sentence %s.%d.%d walks past the %s and the %s on a long and quiet road. ", tag, i+1, j+1, words[(i+j)%len(words)], words[(i*3+j)%len(words)])
		}
		sb.WriteString("</p>")
	}
	return sb.String()
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: fixture OUTFILE.epub")
		os.Exit(2)
	}
	chapters := []ebooktest.EPUBChapter{
		{Title: "Title Page", EpubType: "titlepage", Paras: []string{"The Pager"}},
		{Title: "One", EpubType: "chapter", Raw: `<p>Start<a epub:type="noteref" href="ch3.xhtml#n1">1</a> of the chapter. The hidden zebra crossing is here.</p>` +
			`<p>See <a href="ch3.xhtml#t">the other chapter</a> now.</p>` + prose("a", 40)},
		{Title: "Two", EpubType: "chapter", Raw: `<p id="t">The second chapter begins.</p>` + prose("b", 30) +
			`<aside epub:type="footnote" id="n1"><p>Footnote words.</p></aside>`},
	}
	// A zero testing.T is enough for the helper: it only calls Helper and Fatal.
	var t testing.T
	data := ebooktest.EPUB(&t, ebooktest.EPUBOptions{Version: 3, Title: "The Pager", Authors: []string{"A. Writer"}, Chapters: chapters})
	if err := os.WriteFile(os.Args[1], data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
