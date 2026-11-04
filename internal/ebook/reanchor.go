package ebook

import "strings"

// Anchor is a place in a document: the sentence that holds it and the UTF-16 offset inside the block
// text (the same pair highlights, bookmarks and reading positions store).
type Anchor struct {
	SID string
	Off int
}

// Reanchor finds where quote sits in d, for a mark made on an older reading of the same file. In
// order: the exact text at near; else the only match in near's chapter; else the only match in the
// book. When several places match, the context (prefix before, suffix after) picks one, and if it still
// does not narrow it to a single place the mark is not moved (false). Pure: no I/O, no clock.
func Reanchor(d *Doc, quote, prefix, suffix string, near Anchor) (Anchor, bool) {
	if d == nil || quote == "" || strings.Contains(quote, "\n") {
		return Anchor{}, false
	}
	nch, np, _, ok := ParseSID(near.SID)
	if ok && nch >= 1 && nch <= len(d.Chapters) {
		ch := d.Chapters[nch-1]
		if np >= 1 && np <= len(ch.Blocks) && quoteAt(ch.Blocks[np-1].Text, quote, near.Off) {
			return anchorIn(ch.Blocks[np-1], near.Off), true
		}
		if a, found := uniqueIn(d.Chapters[nch-1:nch], quote, prefix, suffix); found {
			return a, true
		}
	}
	return uniqueIn(d.Chapters, quote, prefix, suffix)
}

// quoteAt reports whether text holds quote starting at UTF-16 offset off.
func quoteAt(text, quote string, off int) bool {
	if off < 0 {
		return false
	}
	// Walk to the byte index of off; a mid-pair offset never matches.
	units := 0
	for i, r := range text {
		if units == off {
			return strings.HasPrefix(text[i:], quote)
		}
		if units > off {
			return false
		}
		if r >= 0x10000 {
			units += 2
		} else {
			units++
		}
	}
	return false
}

// uniqueIn finds the single place quote occurs in chs, using the context to choose between several.
func uniqueIn(chs []Chapter, quote, prefix, suffix string) (Anchor, bool) {
	type hit struct {
		b      *Block
		off    int
		ctxHit bool
	}
	var hits []hit
	for ci := range chs {
		for bi := range chs[ci].Blocks {
			b := &chs[ci].Blocks[bi]
			for from := 0; from <= len(b.Text); {
				i := strings.Index(b.Text[from:], quote)
				if i < 0 {
					break
				}
				at := from + i
				hits = append(hits, hit{b: b, off: u16Len(b.Text[:at]),
					ctxHit: contextFits(b.Text, at, len(quote), prefix, suffix)})
				from = at + 1
			}
		}
	}
	switch {
	case len(hits) == 1:
		return anchorIn(*hits[0].b, hits[0].off), true
	case len(hits) > 1 && (prefix != "" || suffix != ""):
		var pick *hit
		for i := range hits {
			if hits[i].ctxHit {
				if pick != nil {
					return Anchor{}, false
				}
				pick = &hits[i]
			}
		}
		if pick != nil {
			return anchorIn(*pick.b, pick.off), true
		}
	}
	return Anchor{}, false
}

// contextFits is true when the text before the match ends with prefix and the text after starts with
// suffix (an empty side always fits).
func contextFits(text string, at, n int, prefix, suffix string) bool {
	return strings.HasSuffix(text[:at], prefix) && strings.HasPrefix(text[at+n:], suffix)
}

// anchorIn names the sentence of b that holds offset off; an offset past every sentence belongs to the
// last one, and a block with none (an image) uses sentence 0.
func anchorIn(b Block, off int) Anchor {
	k := 0
	for i, s := range b.Sentences {
		if s[0] <= off {
			k = i
		}
	}
	return Anchor{SID: SentenceID(b.ID, k), Off: off}
}
