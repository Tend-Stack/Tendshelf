package web

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/textmatch"
)

// An audiobook chapter is named by its own number, not by its place among the tracks: a book that opens
// with a Prologue track has "Chapter 13: Mud" at track 14, and the player must say 13. One function decides,
// here, and the player's script only reads what it decided (data-num and data-total).

// chapterLeadFmt is the words of a numbered chapter on their own ("Chapter 13").
const chapterLeadFmt = "Chapter %d"

// chapterNames is the numbering of one audiobook's chapters.
type chapterNames struct {
	titles []string
	// Nums holds each chapter's number; 0 is a chapter without one (a Prologue, Opening Credits, an Epilogue).
	Nums []int
	// Total is how many chapters carry a number. In a book where none does, every chapter is counted by its place.
	Total int
}

// newChapterNames numbers the chapters given by their titles in order. When at least one title carries its
// own number ("Chapter 12: The Road", "Capítulo 3"), the others have none. When none does, each chapter is
// counted by its place, as before.
func newChapterNames(titles []string) chapterNames {
	n := chapterNames{titles: titles, Nums: make([]int, len(titles))}
	for i, t := range titles {
		n.Nums[i] = chapterOwnNumber(t)
		if n.Nums[i] > 0 {
			n.Total++
		}
	}
	if n.Total == 0 {
		for i := range n.Nums {
			n.Nums[i] = i + 1
		}
		n.Total = len(titles)
	}
	return n
}

// chapterNamesOf numbers a book's stored chapters.
func chapterNamesOf(chs []library.Chapter) chapterNames {
	titles := make([]string, len(chs))
	for i, c := range chs {
		titles[i] = c.Title
	}
	return newChapterNames(titles)
}

// chapterOwnNumber is the number a title gives itself, 0 when it gives none.
func chapterOwnNumber(title string) int {
	if m := rdrOwnNumber.FindStringSubmatch(textmatch.Norm(title)); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			return n
		}
	}
	return 0
}

// chapterShortName is a title up to its first ":", " - " or " – ", trimmed: "Prologue: 9:17 P.M." is "Prologue".
func chapterShortName(title string) string {
	cut := len(title)
	for _, sep := range []string{":", " - ", " – "} {
		if i := strings.Index(title, sep); i >= 0 && i < cut {
			cut = i
		}
	}
	return strings.TrimSpace(title[:cut])
}

// numbered says whether chapter i (0-based) has a number to show.
func (n chapterNames) numbered(i int) bool {
	return i >= 0 && i < len(n.Nums) && n.Nums[i] > 0
}

// Name is how chapter i (0-based) is called on its own: "Chapter 13" when numbered, else its short name
// ("Prologue"), and with no title either, its place ("Chapter 3").
func (n chapterNames) Name(i int) string {
	if n.numbered(i) {
		return fmt.Sprintf(chapterLeadFmt, n.Nums[i])
	}
	if i >= 0 && i < len(n.titles) {
		if s := chapterShortName(n.titles[i]); s != "" {
			return s
		}
	}
	return fmt.Sprintf(chapterLeadFmt, i+1)
}

// Of is the header form: "Chapter 13 of 51" for a numbered chapter, the short name alone for the others.
func (n chapterNames) Of(i int) string {
	if n.numbered(i) {
		return fmt.Sprintf(nowPanelChapterOf, n.Nums[i], n.Total)
	}
	return n.Name(i)
}

// Inline is Name for the middle of a sentence: "chapter 13" but "Prologue".
func (n chapterNames) Inline(i int) string {
	return n.lowerNumbered(i, n.Name(i))
}

// InlineOf is Of for the middle of a sentence: "chapter 13 of 51" but "Prologue".
func (n chapterNames) InlineOf(i int) string {
	return n.lowerNumbered(i, n.Of(i))
}

// lowerNumbered lowers the first letter of a numbered chapter's words; a name keeps its capital.
func (n chapterNames) lowerNumbered(i int, s string) string {
	if !n.numbered(i) {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(r)) + s[size:]
}
