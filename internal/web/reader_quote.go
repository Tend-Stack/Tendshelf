package web

import (
	"strings"
	"unicode/utf16"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
)

// rdrQuote is the text of a highlight with up to 32 characters of context each side.
type rdrQuote struct{ Quote, Prefix, Suffix string }

// rdrQuoteOf reads the canonical text between two points of one chapter. Both sentences must exist, start must
// not come after end, and the offsets must fall inside their blocks.
func rdrQuoteOf(ch ebook.Chapter, startSID string, startOff int, endSID string, endOff int) (rdrQuote, bool) {
	si, ei := -1, -1
	for i, b := range ch.Blocks {
		if b.ID == rdrBlockOf(startSID) {
			si = i
		}
		if b.ID == rdrBlockOf(endSID) {
			ei = i
		}
	}
	if si < 0 || ei < si {
		return rdrQuote{}, false
	}
	_, _, sk, _ := ebook.ParseSID(startSID)
	_, _, ek, _ := ebook.ParseSID(endSID)
	sb, eb := ch.Blocks[si], ch.Blocks[ei]
	if sk >= len(sb.Sentences) || ek >= len(eb.Sentences) {
		return rdrQuote{}, false
	}
	su, eu := utf16.Encode([]rune(sb.Text)), utf16.Encode([]rune(eb.Text))
	if startOff < 0 || startOff > len(su) || endOff < 0 || endOff > len(eu) {
		return rdrQuote{}, false
	}
	var parts []string
	for i := si; i <= ei; i++ {
		u := utf16.Encode([]rune(ch.Blocks[i].Text))
		a, b := 0, len(u)
		if i == si {
			a = startOff
		}
		if i == ei {
			b = endOff
		}
		if a > b {
			return rdrQuote{}, false
		}
		parts = append(parts, string(utf16.Decode(u[a:b])))
	}
	pre, suf := []rune(string(utf16.Decode(su[:startOff]))), []rune(string(utf16.Decode(eu[endOff:])))
	return rdrQuote{
		Quote:  strings.Join(parts, "\n"),
		Prefix: string(pre[max(0, len(pre)-32):]),
		Suffix: string(suf[:min(len(suf), 32)]),
	}, true
}
