package ebook

import (
	"sort"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/textmatch"
)

// PairMeta is what an ebook says about itself.
type PairMeta struct {
	Title      string
	Authors    []string
	ISBN, ASIN string
}

// BookKey is what pairing compares a book by.
type BookKey struct {
	ID, Title  string
	Authors    []string
	ISBN, ASIN string
}

// Reason says why a book is a candidate for an ebook.
type Reason string

// The reasons, strongest first.
const (
	ReasonISBN    Reason = "isbn"
	ReasonASIN    Reason = "asin"
	ReasonTitle   Reason = "title"
	ReasonAuthor  Reason = "author"
	ReasonSimilar Reason = "similar"
	// ReasonFolder is the same-folder rule: the ebook sits beside the audiobook's files.
	ReasonFolder Reason = "folder"
)

// Candidate is one book an ebook might belong to.
type Candidate struct {
	BookID string
	Reason Reason
	Score  float64
}

// similarTitleRatio is how alike two title keys must be to suggest a book on the title alone.
const similarTitleRatio = 0.75

func surnameSet(names []string) map[string]bool {
	out := map[string]bool{}
	for _, n := range names {
		if s := textmatch.Surname(n); s != "" {
			out[s] = true
		}
	}
	return out
}

func sameSet(a, b map[string]bool) bool {
	if len(a) == 0 || len(a) != len(b) {
		return false
	}
	for k := range a {
		if !b[k] {
			return false
		}
	}
	return true
}

func intersects(a, b map[string]bool) bool {
	for k := range a {
		if b[k] {
			return true
		}
	}
	return false
}

func cleanASIN(s string) string { return strings.ToUpper(strings.TrimSpace(s)) }

// Match rates the books an ebook might belong to, best first. A book in notSame is skipped. limit <= 0 keeps all.
func Match(m PairMeta, books []BookKey, notSame map[string]bool, limit int) []Candidate {
	isbn := textmatch.ISBN13(m.ISBN)
	asin := cleanASIN(m.ASIN)
	tk := textmatch.TitleKey(m.Title)
	mine := surnameSet(m.Authors)
	titles := map[string]string{}
	var out []Candidate
	for _, b := range books {
		if notSame[b.ID] {
			continue
		}
		var best Candidate
		set := func(r Reason, score float64) {
			if score > best.Score {
				best = Candidate{BookID: b.ID, Reason: r, Score: score}
			}
		}
		if isbn != "" && isbn == textmatch.ISBN13(b.ISBN) {
			set(ReasonISBN, 1)
		}
		if asin != "" && asin == cleanASIN(b.ASIN) {
			set(ReasonASIN, 1)
		}
		if tk != "" {
			bk := textmatch.TitleKey(b.Title)
			titles[b.ID] = b.Title
			theirs := surnameSet(b.Authors)
			if bk == tk && sameSet(mine, theirs) {
				set(ReasonTitle, 0.9)
			}
			r := 0.0
			if bk != "" {
				r = textmatch.Ratio(tk, bk)
			}
			if intersects(mine, theirs) {
				set(ReasonAuthor, 0.5+0.4*r)
			}
			if r >= similarTitleRatio {
				set(ReasonSimilar, 0.6*r)
			}
		}
		if best.Score > 0 {
			titles[b.ID] = b.Title
			out = append(out, best)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		if titles[out[i].BookID] != titles[out[j].BookID] {
			return titles[out[i].BookID] < titles[out[j].BookID]
		}
		return out[i].BookID < out[j].BookID
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

// Decide says whether the candidates single out one book to pair automatically, and how it is recorded
// ("isbn" also covers an ASIN match; the table has no 'asin' value). Two or more candidates at the deciding level
// are suggestions only.
func Decide(c []Candidate) (bookID, pairedBy string, ok bool) {
	var ids []string
	titles := 0
	title := ""
	for _, x := range c {
		switch x.Reason {
		case ReasonISBN, ReasonASIN:
			ids = append(ids, x.BookID)
		case ReasonTitle:
			titles++
			title = x.BookID
		}
	}
	switch {
	case len(ids) == 1:
		return ids[0], "isbn", true
	case len(ids) == 0 && titles == 1:
		return title, "title", true
	}
	return "", "", false
}

// Check is one line of the evidence shown beside a suggestion.
type Check struct {
	Kind         string // same_title, different_title, same_isbn, chapters_same, chapters_count, chapters_differ
	OK           bool
	Author, ISBN string
	N, M         int
}

// Checks lists what agrees and what differs between an ebook and a book. bookChapterTitles may be empty
// (no chapter lines then).
func Checks(m PairMeta, b BookKey, ebookCh []ChapterRow, bookChapterTitles []string) []Check {
	var out []Check
	tk, bk := textmatch.TitleKey(m.Title), textmatch.TitleKey(b.Title)
	if tk != "" && tk == bk {
		c := Check{Kind: "same_title", OK: true}
		theirs := surnameSet(b.Authors)
		mine := surnameSet(m.Authors)
		for _, a := range b.Authors {
			if mine[textmatch.Surname(a)] && theirs[textmatch.Surname(a)] {
				c.Author = a
				break
			}
		}
		out = append(out, c)
	} else {
		out = append(out, Check{Kind: "different_title"})
	}
	if i := textmatch.ISBN13(m.ISBN); i != "" && i == textmatch.ISBN13(b.ISBN) {
		out = append(out, Check{Kind: "same_isbn", OK: true, ISBN: i})
	}
	if len(bookChapterTitles) > 0 {
		var names []string
		for _, c := range ebookCh {
			if !c.Front {
				names = append(names, textmatch.Norm(c.Title))
			}
		}
		n, mm := len(names), len(bookChapterTitles)
		same := n == mm
		for i := 0; same && i < n; i++ {
			if names[i] != textmatch.Norm(bookChapterTitles[i]) {
				same = false
			}
		}
		switch {
		case same:
			out = append(out, Check{Kind: "chapters_same", OK: true, N: n, M: mm})
		case n == mm:
			out = append(out, Check{Kind: "chapters_count", OK: true, N: n, M: mm})
		default:
			out = append(out, Check{Kind: "chapters_differ", N: n, M: mm})
		}
	}
	return out
}
