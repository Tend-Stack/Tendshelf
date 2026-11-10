package rss

import (
	"strings"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
)

// PlainText is the words of cleaned notes with the tags gone and white space collapsed, cut at max runes with an
// ellipsis. The show page draws it as the short, clamped preview above the full notes.
func PlainText(cleaned string, max int) string {
	z := xhtml.NewTokenizer(strings.NewReader(cleaned))
	var b strings.Builder
	for {
		switch z.Next() {
		case xhtml.ErrorToken:
			out := strings.Join(strings.Fields(b.String()), " ")
			if utf8.RuneCountInString(out) > max {
				r := []rune(out)
				out = strings.TrimSpace(string(r[:max])) + "…"
			}
			return out
		case xhtml.TextToken:
			b.Write(z.Text())
		case xhtml.StartTagToken, xhtml.EndTagToken, xhtml.SelfClosingTagToken:
			b.WriteByte(' ') // a paragraph or list item ends a word
		}
	}
}
