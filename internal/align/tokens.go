package align

import (
	"errors"
	"strings"
	"unicode"
	"unicode/utf16"

	"golang.org/x/text/unicode/norm"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
)

// Vocab is the model's symbol table. Blank is the CTC blank, Delim the word delimiter ("|").
type Vocab struct {
	Symbols []string
	Blank   int
	Delim   int
	Upper   bool // the model has only upper-case letters
	letters map[rune]int
}

// NewVocab checks the symbols and builds the letter table. Single-rune symbols other than the delimiter are
// letters; the table is in upper-case mode when none of them is lower case.
func NewVocab(symbols []string, blank int, delim string) (Vocab, error) {
	v := Vocab{Symbols: symbols, Blank: blank, Delim: -1, letters: map[rune]int{}}
	if blank < 0 || blank >= len(symbols) {
		return Vocab{}, errors.New("align: blank outside the vocabulary")
	}
	for i, s := range symbols {
		if s == delim {
			v.Delim = i
		}
	}
	if v.Delim < 0 {
		return Vocab{}, errors.New("align: delimiter not in the vocabulary")
	}
	lower := false
	for i, s := range symbols {
		rs := []rune(s)
		if len(rs) != 1 || i == v.Delim || i == blank {
			continue
		}
		v.letters[rs[0]] = i
		if unicode.IsLower(rs[0]) {
			lower = true
		}
	}
	v.Upper = !lower
	if len(v.letters) == 0 {
		return Vocab{}, errors.New("align: vocabulary has no letters")
	}
	return v, nil
}

// Size is the number of symbols.
func (v Vocab) Size() int { return len(v.Symbols) }

// IsLetter reports whether id is a letter symbol.
func (v Vocab) IsLetter(id int) bool {
	return id >= 0 && id < len(v.Symbols) && id != v.Blank && id != v.Delim && len([]rune(v.Symbols[id])) == 1
}

// Letter returns the lower-case text of a letter symbol.
func (v Vocab) Letter(id int) string { return strings.ToLower(v.Symbols[id]) }

func (v Vocab) lookup(r rune) (int, bool) {
	if v.Upper {
		r = unicode.ToUpper(r)
	} else {
		r = unicode.ToLower(r)
	}
	id, ok := v.letters[r]
	return id, ok
}

// Encode returns the tokens of a normalised word and the word as the model spells it (lower case). A letter
// the model lacks is tried without its accent; if that fails too, ok is false and the word is a wildcard.
func (v Vocab) Encode(word string) (tokens []int, spelt string, ok bool) {
	if word == "" {
		return nil, "", false
	}
	var sb strings.Builder
	for _, r := range word {
		if id, found := v.lookup(r); found {
			tokens = append(tokens, id)
			sb.WriteString(v.Letter(id))
			continue
		}
		plain := strings.Map(func(x rune) rune {
			if unicode.Is(unicode.Mn, x) {
				return -1
			}
			return x
		}, norm.NFD.String(string(r)))
		if plain == "" {
			return nil, "", false
		}
		for _, p := range plain {
			id, found := v.lookup(p)
			if !found {
				return nil, "", false
			}
			tokens = append(tokens, id)
			sb.WriteString(v.Letter(id))
		}
	}
	return tokens, sb.String(), true
}

// TUnit is one spoken word of the edition. Chapter is the position in the document, Block the position in
// the chapter, Sentence the sentence index in the block, Word the source word index in the block (several
// units can share one). Off0 and Off1 are the source word's UTF-16 offsets in the block text.
type TUnit struct {
	Chapter, Block, Sentence, Word int
	Off0, Off1                     int
	Norm                           string
	Tokens                         []int
	Wildcard, Front                bool
}

// TSentence is one sentence. First and End are its unit range (End exclusive; equal when it has no words).
type TSentence struct {
	SID                  string
	Chapter, Block, Sent int
	First, End           int
	Front                bool
}

// TChapter is one chapter. Idx is the document's 1-based chapter number.
type TChapter struct {
	Idx                int
	Front              bool
	FirstUnit, EndUnit int
	FirstSent, EndSent int
}

// Text is the edition as the aligner sees it.
type Text struct {
	Units     []TUnit
	Sentences []TSentence
	Chapters  []TChapter
}

// BuildText normalises doc into units for language lang. A unit the normaliser marks as a wildcard, or with
// a letter the model lacks, becomes a wildcard.
func BuildText(doc *ebook.Doc, lang string, v Vocab) Text {
	var t Text
	for ci, ch := range doc.Chapters {
		tc := TChapter{Idx: ch.Idx, Front: ch.Front, FirstUnit: len(t.Units), FirstSent: len(t.Sentences)}
		for bi, b := range ch.Blocks {
			sentAt := len(t.Sentences)
			for k := range b.Sentences {
				t.Sentences = append(t.Sentences, TSentence{
					SID: ebook.SentenceID(b.ID, k), Chapter: ci, Block: bi, Sent: k,
					First: len(t.Units), End: len(t.Units), Front: ch.Front,
				})
			}
			if len(b.Sentences) == 0 {
				continue
			}
			valid := validWordIndexes(b)
			spans := ebook.WordSpans(b)
			if len(valid) != len(spans) {
				continue
			}
			lastSent := 0
			for _, u := range Normalize(lang, spans) {
				sp := spans[u.Src]
				if sp.Sentence >= 0 {
					lastSent = sp.Sentence
				}
				w := b.Words[valid[u.Src]]
				tu := TUnit{
					Chapter: ci, Block: bi, Sentence: lastSent, Word: u.Src,
					Off0: w[0], Off1: w[1], Front: ch.Front,
				}
				if toks, spelt, ok := v.Encode(u.Norm); ok && !u.Wildcard {
					tu.Tokens, tu.Norm = toks, spelt
				} else {
					tu.Wildcard = true
				}
				at := len(t.Units)
				t.Units = append(t.Units, tu)
				s := &t.Sentences[sentAt+lastSent]
				if s.First == s.End {
					s.First = at
				}
				s.End = at + 1
			}
		}
		tc.EndUnit, tc.EndSent = len(t.Units), len(t.Sentences)
		t.Chapters = append(t.Chapters, tc)
	}
	return t
}

// validWordIndexes lists the indexes of b.Words that ebook.WordSpans keeps.
func validWordIndexes(b ebook.Block) []int {
	n := len(utf16.Encode([]rune(b.Text)))
	var out []int
	for i, w := range b.Words {
		if w[0] < 0 || w[1] > n || w[0] > w[1] {
			continue
		}
		out = append(out, i)
	}
	return out
}
