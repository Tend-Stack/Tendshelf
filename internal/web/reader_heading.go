package web

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
	"github.com/Tend-Stack/tendshelf/internal/textmatch"
)

// rdrOwnNumber finds the number a chapter title gives itself ("Chapter 12: The Road", "Capítulo 3").
var rdrOwnNumber = regexp.MustCompile(`^(?:chapter|capitulo|chapitre|kapitel|capitolo)\s+(\d{1,4})(?:\D|$)`)

// rdrUnnumbered are the opening words of a title that is a part of the story but not a numbered chapter.
var rdrUnnumbered = map[string]bool{
	"prologue": true, "prolog": true, "prologo": true, "epilogue": true, "epilog": true, "epilogo": true,
	"preface": true, "prefacio": true, "foreword": true, "introduction": true, "introduccion": true,
	"afterword": true, "interlude": true, "interludio": true, "coda": true,
}

// rdrTitleKey is a title reduced to its letters and digits, lower case and without accents, so two
// spellings of one heading compare equal ("Chapter 1: The Glass Box" and "Chapter 1The Glass Box").
func rdrTitleKey(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteString(textmatch.Fold(r))
		}
	}
	return b.String()
}

// rdrIsUnnumbered says whether a title opens with a word such as Prologue or Epilogue.
func rdrIsUnnumbered(title string) bool {
	first := strings.FieldsFunc(strings.ToLower(title), func(r rune) bool { return !unicode.IsLetter(r) })
	return len(first) > 0 && rdrUnnumbered[rdrTitleKey(first[0])]
}

// rdrOverline is the small label above a chapter's title. Opening pages say so; a prologue, an epilogue
// and the like carry no label and are not counted; a title that gives its own number keeps that number;
// any other chapter is counted among the numbered ones before it.
func rdrOverline(chs []ebook.Chapter, idx int) string {
	ch := chs[idx-1]
	if ch.Front {
		return readerFrontOnly
	}
	if rdrIsUnnumbered(ch.Title) {
		return ""
	}
	if m := rdrOwnNumber.FindStringSubmatch(textmatch.Norm(ch.Title)); m != nil {
		if n, err := strconv.Atoi(m[1]); err == nil && n > 0 {
			return fmt.Sprintf(readerChapter, n)
		}
	}
	n := 0
	for _, c := range chs[:idx] {
		if !c.Front && !rdrIsUnnumbered(c.Title) {
			n++
		}
	}
	return fmt.Sprintf(readerChapter, n)
}

// rdrRepeatedHeading returns the index of the heading that opens a chapter's text and only repeats the
// chapter's title, which the page already shows above the text; -1 when there is none. Pictures and
// rules before it are passed over; any other block first means the heading is part of the text.
func rdrRepeatedHeading(ch ebook.Chapter) int {
	want := rdrTitleKey(ch.Title)
	if want == "" {
		return -1
	}
	for i, b := range ch.Blocks {
		switch b.Kind {
		case ebook.KindImg, ebook.KindHR:
			continue
		case ebook.KindH:
			if rdrTitleKey(b.Text) == want {
				return i
			}
		}
		return -1
	}
	return -1
}
