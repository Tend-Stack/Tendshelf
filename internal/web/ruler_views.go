package web

import (
	"fmt"
	"math"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

// listenRuler is a book's progress as the ruler and its where-lines show it: Home's hero and the
// book page's play region both build it here.
type listenRuler struct {
	Label, Now string
	Ticks      []string
	// Chapters and Current (1-based, 0 before the first chapter) let the ruler pick a chapter.
	Chapters, Current int
	// Where is the long line ("Chapter 12 · 18 min left in chapter · 6 h 40 m left in book");
	// WhereShort is the tablet's short one ("Chapter 12 · 18 min left", or "6 h 40 m left").
	Where, WhereShort string
}

// bookLeftText is the time left in a book of durMs from posMs at the person's speed ("6 h 40 m", "0 m" at the end).
func bookLeftText(posMs, durMs int64, speed float64) string {
	if speed <= 0 {
		speed = 1
	}
	left := durationText(int64(float64(max(durMs-min(posMs, durMs), 0)) / speed))
	if left == "" {
		left = "0 m"
	}
	return left
}

// listenRulerFor builds the ruler for a position in a book of durMs, at the person's speed. Chapter
// words appear only when the book has chapters.
func listenRulerFor(chs []library.Chapter, posMs, durMs int64, speed float64) listenRuler {
	dur, pos := durMs, min(posMs, durMs)
	v := listenRuler{Now: percentText(pos, dur)}
	for _, c := range chs {
		if c.StartMs > 0 && dur > 0 && c.StartMs < dur {
			v.Ticks = append(v.Ticks, percentText(c.StartMs, dur))
		}
	}
	if speed <= 0 {
		speed = 1
	}
	bookLeft := bookLeftText(pos, dur, speed)
	v.Label, v.Where = homeBookProgress, fmt.Sprintf(homeWhereBook, bookLeft)
	v.WhereShort = fmt.Sprintf(bookWhereShortBook, bookLeft)
	v.Chapters = len(chs)
	if cur := currentChapter(chs, pos); cur >= 0 {
		v.Current = cur + 1
		left := int64(math.Ceil(float64(max(chs[cur].EndMs-pos, 0)) / speed / 60_000))
		v.Label = fmt.Sprintf(homeRulerChapter, cur+1, len(chs))
		v.Where = fmt.Sprintf(homeWhere, cur+1, max(left, 1), bookLeft)
		v.WhereShort = fmt.Sprintf(bookWhereShort, cur+1, fmt.Sprintf("%d min", max(left, 1)))
	}
	return v
}
