// Package aligntest builds synthetic documents, narration scripts and CTC emissions for the alignment
// tests. Nothing here is audio and nothing is a real book: words are seeded pseudo-words and the
// "emissions" are generated log-probabilities with known word times.
package aligntest

import (
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"unicode"
	"unicode/utf16"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
)

var (
	wordListOnce sync.Once
	wordList     []string
)

// vocabulary returns the 600 pseudo-words (letters only, 3 to 9 letters, all different).
func vocabulary() []string {
	wordListOnce.Do(func() {
		const consonants, vowels = "bdfgklmnprstvz", "aeiou"
		rng := rand.New(rand.NewPCG(7, 11))
		seen := map[string]bool{}
		for len(wordList) < 600 {
			var sb strings.Builder
			for s := 0; s < 1+rng.IntN(3); s++ {
				sb.WriteByte(consonants[rng.IntN(len(consonants))])
				sb.WriteByte(vowels[rng.IntN(len(vowels))])
			}
			if rng.IntN(2) == 0 {
				sb.WriteByte(consonants[rng.IntN(len(consonants))])
			}
			w := sb.String()
			if len(w) < 3 || seen[w] {
				continue
			}
			seen[w] = true
			wordList = append(wordList, w)
		}
	})
	return wordList
}

// Words returns n pseudo-words drawn with the seed.
func Words(seed int64, n int) []string {
	list := vocabulary()
	rng := rand.New(rand.NewPCG(uint64(seed), 99))
	out := make([]string, n)
	for i := range out {
		out[i] = list[rng.IntN(len(list))]
	}
	return out
}

// Sentence builds one sentence of n pseudo-words, capitalised and ending in a full stop.
func Sentence(seed int64, n int) string {
	w := Words(seed, n)
	w[0] = strings.ToUpper(w[0][:1]) + w[0][1:]
	return strings.Join(w, " ") + "."
}

// Paragraph builds a paragraph of the given number of sentences of about 8 to 14 words each.
func Paragraph(seed int64, sentences int) string {
	rng := rand.New(rand.NewPCG(uint64(seed), 5))
	parts := make([]string, sentences)
	for i := range parts {
		parts[i] = Sentence(seed*1000+int64(i), 8+rng.IntN(7))
	}
	return strings.Join(parts, " ")
}

// Chapters builds chapters of paragraphs holding about wordsPerChapter words each.
func Chapters(seed int64, chapters, wordsPerChapter int) [][]string {
	out := make([][]string, chapters)
	for c := range out {
		for w := 0; w < wordsPerChapter; {
			p := Paragraph(seed*100000+int64(c)*1000+int64(len(out[c])), 3+len(out[c])%3)
			out[c] = append(out[c], p)
			w += len(strings.Fields(p))
		}
	}
	return out
}

// Doc builds a document from chapters of paragraphs. A paragraph becomes one block "c<idx>.p<n>"; sentences
// end at . ! or ? followed by a space or the end; words are runs of letters, digits and apostrophes. Offsets
// are UTF-16 units.
func Doc(chapters [][]string) *ebook.Doc {
	d := &ebook.Doc{
		V: ebook.DocV, Recipe: ebook.Recipe, Format: "epub", Layout: "reflow",
		Meta: ebook.Meta{Title: "Synthetic", Language: "en"},
	}
	for ci, paras := range chapters {
		ch := ebook.Chapter{Idx: ci + 1, Title: fmt.Sprintf("Chapter %d", ci+1)}
		for pi, text := range paras {
			ch.Blocks = append(ch.Blocks, NewBlock(fmt.Sprintf("c%d.p%d", ci+1, pi+1), text))
		}
		d.Chapters = append(d.Chapters, ch)
	}
	return d
}

// NewBlock builds a paragraph block with sentence and word offsets.
func NewBlock(id, text string) ebook.Block {
	b := ebook.Block{ID: id, Kind: ebook.KindP, Text: text, Runs: []ebook.Run{}}
	rs := []rune(text)
	off := make([]int, len(rs)+1) // UTF-16 offset of each rune
	for i, r := range rs {
		off[i+1] = off[i] + len(utf16.Encode([]rune{r}))
	}
	isWord := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '\'' }
	start := 0
	for i := 0; i < len(rs); i++ {
		if isWord(rs[i]) {
			j := i
			for j < len(rs) && isWord(rs[j]) {
				j++
			}
			b.Words = append(b.Words, [2]int{off[i], off[j]})
			i = j - 1
		}
		if rs[i] == '.' || rs[i] == '!' || rs[i] == '?' {
			if i+1 == len(rs) || rs[i+1] == ' ' {
				b.Sentences = append(b.Sentences, [2]int{off[start], off[i+1]})
				start = i + 1
				for start < len(rs) && rs[start] == ' ' {
					start++
				}
			}
		}
	}
	if start < len(rs) {
		b.Sentences = append(b.Sentences, [2]int{off[start], off[len(rs)]})
	}
	if b.Words == nil {
		b.Words = [][2]int{}
	}
	if b.Sentences == nil {
		b.Sentences = [][2]int{}
	}
	return b
}

// WordTexts returns the words of a block in order.
func WordTexts(b ebook.Block) []string {
	var out []string
	for _, w := range ebook.WordSpans(b) {
		out = append(out, w.Text)
	}
	return out
}
