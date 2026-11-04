package web

import (
	"strings"
	"unicode/utf16"

	"github.com/Tend-Stack/tendshelf/internal/align"
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

// rdrTimes gives the audio range of a highlight from a chapter's timing: the start of the first timed word at or
// after the start point, the end of the last timed word before the end point, using the sentence times when a
// word is not timed. Only timed sentences count; with none, both are nil.
func rdrTimes(t *align.Timing, startSID string, startOff int, endSID string, endOff int) (start, end *int64) {
	first, last := -1, -1
	for i, sid := range t.SID {
		if ebook.CompareSID(sid, startSID) < 0 || ebook.CompareSID(sid, endSID) > 0 || t.Gap[i] != nil || t.SS[i] < 0 || t.SE[i] < 0 {
			continue
		}
		if first < 0 {
			first = i
		}
		last = i
	}
	if first < 0 {
		return nil, nil
	}
	s, e := t.SS[first], t.SE[last]
	for w := range t.WS {
		if t.WM[w] != align.WordTimed || t.W0[w] < 0 || t.W1[w] < 0 {
			continue
		}
		if t.WS[w] == first && (rdrBlockOf(t.SID[first]) != rdrBlockOf(startSID) || t.WO1[w] > startOff) {
			s = min(s, t.W0[w])
			break
		}
	}
	for w := len(t.WS) - 1; w >= 0; w-- {
		if t.WM[w] != align.WordTimed || t.W0[w] < 0 || t.W1[w] < 0 {
			continue
		}
		if t.WS[w] == last && (rdrBlockOf(t.SID[last]) != rdrBlockOf(endSID) || t.WO0[w] < endOff) {
			e = max(e, t.W1[w])
			break
		}
	}
	if e < s {
		e = s
	}
	return &s, &e
}
