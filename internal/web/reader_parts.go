package web

import (
	"github.com/Tend-Stack/tendshelf/internal/ebook"
)

// Chapter parts. A TXT or PDF can be one chapter of 2 MB, which no column layout lays out quickly, so a
// chapter is served in parts of about rsPartUnits UTF-16 units, cut at block edges. Server addresses drive
// next and previous, so the client does no arithmetic.

// rsPartUnits is the size a part grows to before the next block starts a new one.
const rsPartUnits = 40000

// rsPart is blocks [From,To) of a chapter. Before is the units of text before the part inside the chapter.
type rsPart struct {
	From, To      int
	Before, Units int
}

// rsUnits counts the UTF-16 units of s, the unit every offset in a document uses.
func rsUnits(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// rsParts cuts a chapter into parts. A block is never split, so a single block over the size is its own
// part. A chapter always has at least one part.
func rsParts(ch ebook.Chapter) []rsPart {
	var out []rsPart
	cur := rsPart{}
	total := 0
	for i, b := range ch.Blocks {
		n := rsUnits(b.Text)
		if cur.Units > 0 && cur.Units+n > rsPartUnits {
			cur.To = i
			out = append(out, cur)
			cur = rsPart{From: i, Before: total}
		}
		cur.Units += n
		total += n
	}
	cur.To = len(ch.Blocks)
	return append(out, cur)
}

// rsPartIndex is the 1-based part that holds block number bi (0-based) of a chapter.
func rsPartIndex(parts []rsPart, bi int) int {
	for i, p := range parts {
		if bi >= p.From && bi < p.To {
			return i + 1
		}
	}
	return len(parts)
}

// rsBlockPos reads a block id "c<chapter>.p<block>" into a 1-based chapter and 0-based block index. ok is
// false when the document has no such block.
func rsBlockPos(d *ebook.Doc, blockID string) (ch, bi int, ok bool) {
	c, p, _, valid := ebook.ParseSID(blockID + ".s0")
	if !valid || c < 1 || c > len(d.Chapters) || p < 1 || p > len(d.Chapters[c-1].Blocks) {
		return 0, 0, false
	}
	if d.Chapters[c-1].Blocks[p-1].ID != blockID {
		return 0, 0, false
	}
	return c, p - 1, true
}

// rsCharsBefore returns the units of text in all chapters before chapter idx (1-based), and the book total.
func rsCharsBefore(d *ebook.Doc, idx int) (before, total int) {
	for _, c := range d.Chapters {
		n := 0
		for _, b := range c.Blocks {
			n += rsUnits(b.Text)
		}
		if c.Idx < idx {
			before += n
		}
		total += n
	}
	return before, total
}
