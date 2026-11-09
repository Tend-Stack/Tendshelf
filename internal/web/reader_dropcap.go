package web

import (
	"unicode/utf16"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
)

const (
	// rdrDropCapMin is the shortest paragraph (in characters) that gets a big first letter: a dedication, an
	// epigraph or a line of a list is shorter, a paragraph of prose is not.
	rdrDropCapMin = 140
	// rdrVerseMax and rdrVerseRun say what a run of short lines is: this many paragraphs in a row, each at most
	// this many characters. Such a run is verse or a dedication, and its lines take no first-line indent.
	rdrVerseMax = 50
	rdrVerseRun = 3
)

// rdrProse says whether a block is a real paragraph of prose, the only thing that opens with a drop cap: a
// paragraph (not a list item, quote, note or heading) that is long enough and is not mostly links (a table of
// contents written as a paragraph of links).
func rdrProse(b ebook.Block) bool {
	if b.Kind != ebook.KindP || utf8.RuneCountInString(b.Text) < rdrDropCapMin {
		return false
	}
	linked := 0
	for _, l := range b.Links {
		linked += l.End - l.Start
	}
	return linked*2 < len(utf16.Encode([]rune(b.Text)))
}

// rdrDropCapAt says whether the chapter's first paragraph (the one at index i of its blocks) opens with a drop
// cap: never in front matter ("Opening pages"), and only for prose.
func rdrDropCapAt(ch ebook.Chapter, i int) bool {
	return !ch.Front && rdrProse(ch.Blocks[i])
}

// rdrVerseRuns marks the paragraphs that sit in a run of short lines (verse, a dedication), so the page can drop the
// first-line indent that would otherwise push every line after the first to the right.
func rdrVerseRuns(blocks []ebook.Block) []bool {
	out := make([]bool, len(blocks))
	for i := 0; i < len(blocks); {
		j := i
		for j < len(blocks) && blocks[j].Kind == ebook.KindP && utf8.RuneCountInString(blocks[j].Text) <= rdrVerseMax {
			j++
		}
		if j-i >= rdrVerseRun {
			for k := i; k < j; k++ {
				out[k] = true
			}
		}
		i = max(j, i+1)
	}
	return out
}
