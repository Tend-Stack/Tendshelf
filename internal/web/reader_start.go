package web

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// rsPlace is where the reader page opens: a chapter part and, when the place is a sentence, its id and the
// offset in its block.
type rsPlace struct {
	Ch, Part int
	At       string
	Off      int
}

// rsStartPlace decides where the page opens. ok is false when the address names something the document does not
// have. The rungs, in order: an address that names a place (`at`, or `ch` and `part`); the person's saved place
// on this edition; the saved place found again by its quote; its percent; the first chapter that is not front
// matter. Listening progress is never read (invariant 7).
func (s *server) rsStartPlace(ctx context.Context, u users.User, rc readerCtx, q url.Values) (rsPlace, bool, error) {
	n := len(rc.doc.Chapters)
	var pl rsPlace
	if q.Has("ch") {
		c, err := strconv.Atoi(q.Get("ch"))
		if err != nil || c < 1 || c > n {
			return pl, false, nil
		}
		pl.Ch = c
	}
	partGiven := 0
	if q.Has("part") {
		p, err := strconv.Atoi(q.Get("part"))
		if err != nil || p < 1 {
			return pl, false, nil
		}
		partGiven = p
	}
	if at := q.Get("at"); at != "" {
		c, ok := rdrSIDExists(rc.doc, at)
		if !ok {
			return pl, false, nil
		}
		if pl.Ch == 0 {
			pl.Ch = c
		}
		if c == pl.Ch {
			pl.At = at
			pl.Part = rsPartAt(rc.doc, at)
			pl.Off = rsClampOff(rc.doc, at, atoiOr(q.Get("off"), 0))
			return pl, true, nil
		}
	}
	// The skim bar (G4): bp is a place in the book in hundredths of a percent. It wins over ch, which only
	// names the chapter the bar guessed. A bad value is ignored, as is any bp beside an explicit part.
	if bp := q.Get("bp"); bp != "" && partGiven == 0 {
		if n, err := strconv.Atoi(bp); err == nil && n >= 0 && n <= 10000 {
			if at, ok := rsPlaceAtChars(rc.doc, n); ok {
				return at, true, nil
			}
		}
	}
	if pl.Ch != 0 {
		pl.Part = max(partGiven, 1)
		if pl.Part > len(rsParts(rc.doc.Chapters[pl.Ch-1])) {
			return pl, false, nil
		}
		return pl, true, nil
	}
	if partGiven != 0 {
		return pl, false, nil // a part of no chapter
	}
	if at, off, ok, err := s.rsSavedPlace(ctx, u, rc); err != nil {
		return pl, false, err
	} else if ok {
		c, _ := rdrSIDExists(rc.doc, at)
		return rsPlace{Ch: c, Part: rsPartAt(rc.doc, at), At: at, Off: rsClampOff(rc.doc, at, off)}, true, nil
	}
	for i, c := range rc.doc.Chapters {
		if !c.Front {
			return rsPlace{Ch: i + 1, Part: 1}, true, nil
		}
	}
	return rsPlace{Ch: 1, Part: 1}, true, nil
}

// rsPlaceAtChars is the start of the block that holds the place bp (0 to 10000) of the way through the text of
// d: its chapter, part and first sentence, offset 0. ok is false for a book with no text.
func rsPlaceAtChars(d *ebook.Doc, bp int) (rsPlace, bool) {
	_, total := rsCharsBefore(d, 0)
	if total <= 0 {
		return rsPlace{}, false
	}
	want := min(bp*total/10000, total-1) // 10000 is the end: the last block, not past it
	for _, c := range d.Chapters {
		for bi, b := range c.Blocks {
			n := rsUnits(b.Text)
			if want < n {
				return rsPlace{Ch: c.Idx, Part: rsPartIndex(rsParts(c), bi), At: ebook.SentenceID(b.ID, 0)}, true
			}
			want -= n
		}
	}
	return rsPlace{}, false
}

func atoiOr(s string, def int) int {
	if n, err := strconv.Atoi(s); err == nil && n >= 0 {
		return n
	}
	return def
}

// rsPartAt finds the part of its chapter that holds the block of sentence id sid.
func rsPartAt(d *ebook.Doc, sid string) int {
	c, bi, ok := rsBlockPos(d, rdrBlockOf(sid))
	if !ok {
		return 1
	}
	return rsPartIndex(rsParts(d.Chapters[c-1]), bi)
}

// rsClampOff keeps an offset inside its block.
func rsClampOff(d *ebook.Doc, sid string, off int) int {
	c, bi, ok := rsBlockPos(d, rdrBlockOf(sid))
	if !ok {
		return 0
	}
	return min(max(off, 0), rsUnits(d.Chapters[c-1].Blocks[bi].Text))
}

// rsSavedPlace finds the sentence to open at from the person's reading position: the place itself when it was
// saved on this edition, else the same text found again by its quote, else the percent.
func (s *server) rsSavedPlace(ctx context.Context, u users.User, rc readerCtx) (sid string, off int, ok bool, err error) {
	pos, found, err := s.progress.Reading(ctx, u.ID, rc.book.ID)
	if err != nil || !found {
		return "", 0, false, err
	}
	if pos.EditionID == rc.ed.ID && pos.SID != "" {
		if _, ok := rdrSIDExists(rc.doc, pos.SID); ok {
			return pos.SID, pos.Off, true, nil
		}
	}
	if quote, _, _ := strings.Cut(pos.Quote, "\n"); quote != "" {
		if a, ok := ebook.Reanchor(rc.doc, quote, "", "", ebook.Anchor{SID: pos.SID, Off: pos.Off}); ok {
			return a.SID, a.Off, true, nil
		}
	}
	if pos.PercentBP > 0 {
		_, total := rsCharsBefore(rc.doc, 0)
		if a, ok := rc.doc.AnchorAtChar(total * pos.PercentBP / 10000); ok {
			return a.SID, a.Off, true, nil
		}
	}
	return "", 0, false, nil
}
