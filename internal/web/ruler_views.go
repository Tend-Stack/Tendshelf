package web

import (
	"fmt"
	"math"
	"strings"

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

// whereBookLeft is bookLeft as the where-line says it. Under an hour the line already says "min" for the
// chapter, so the book's minutes say "min" too ("2 min left in chapter · 2 min left in book"); from an hour
// up the copy deck's "6 h 40 m" stays.
func whereBookLeft(bookLeft string) string {
	if strings.Contains(bookLeft, " h") || !strings.HasSuffix(bookLeft, " m") {
		return bookLeft
	}
	return strings.TrimSuffix(bookLeft, " m") + " min"
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
	whereLeft := whereBookLeft(bookLeft)
	v.Label, v.Where = homeBookProgress, fmt.Sprintf(homeWhereBook, whereLeft)
	v.WhereShort = fmt.Sprintf(bookWhereShortBook, bookLeft)
	v.Chapters = len(chs)
	if cur := currentChapter(chs, pos); cur >= 0 {
		v.Current = cur + 1
		left := int64(math.Ceil(float64(max(chs[cur].EndMs-pos, 0)) / speed / 60_000))
		names := chapterNamesOf(chs)
		v.Label = fmt.Sprintf(homeRulerChapter, names.InlineOf(cur)) // "Book progress, chapter 13 of 51"
		v.Where = fmt.Sprintf(homeWhere, names.Name(cur), max(left, 1), whereLeft)
		v.WhereShort = fmt.Sprintf(bookWhereShort, names.Name(cur), fmt.Sprintf("%d min", max(left, 1)))
	}
	return v
}
