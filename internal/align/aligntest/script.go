package aligntest

import (
	"math/rand/v2"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
)

const frameMs = 20

// Piece is one thing the narrator does: say a sentence (Words), or play MusicMS of music. SID names the
// sentence of the edition it reads; empty means speech that is not in the text (a publisher intro). Para
// groups sentences into paragraphs: a change of Para is followed by a longer pause.
type Piece struct {
	SID     string
	Para    int
	Words   []string
	MusicMS int64
}

// SWord is a spoken word with its times in milliseconds.
type SWord struct {
	Text           string
	StartMS, EndMS int64
}

// Item is a laid-out Piece.
type Item struct {
	Piece
	Spoken         []SWord
	StartMS, EndMS int64
}

// Script is the narration with every time known.
type Script struct {
	Items   []Item
	TotalMS int64
}

// Truth maps a sentence id to its true start and end in milliseconds.
type Truth map[string][2]int64

// Pieces returns one Piece per sentence of the document, in reading order, with the document's own words
// (lower case). Paragraph numbers count blocks across the book.
func Pieces(doc *ebook.Doc) []Piece {
	var out []Piece
	para := 0
	for _, ch := range doc.Chapters {
		for _, b := range ch.Blocks {
			para++
			spans := ebook.WordSpans(b)
			for k := range b.Sentences {
				p := Piece{SID: ebook.SentenceID(b.ID, k), Para: para}
				for _, w := range spans {
					if w.Sentence == k {
						p.Words = append(p.Words, strings.ToLower(w.Text))
					}
				}
				out = append(out, p)
			}
		}
	}
	return out
}

// NewScript lays pieces out in time: a word takes the longer of its slot at 1.5 to 4.5 words per second (a
// rate drawn per sentence) and two frames per letter plus one; sentences are separated by 200 to 800 ms,
// paragraphs by 1.2 s; the file starts and ends with half a second of silence.
func NewScript(pieces []Piece, seed int64) Script {
	rng := rand.New(rand.NewPCG(uint64(seed), 17))
	var s Script
	at := int64(500)
	for i, p := range pieces {
		it := Item{Piece: p, StartMS: at}
		if p.MusicMS > 0 {
			at += p.MusicMS
			it.EndMS = at
			s.Items = append(s.Items, it)
			continue
		}
		rate := 1.5 + 3*rng.Float64()
		slot := int64(1000/rate) / frameMs
		for _, w := range p.Words {
			n := int64(2*len(w) + 1)
			if slot > n {
				n = slot
			}
			it.Spoken = append(it.Spoken, SWord{Text: w, StartMS: at, EndMS: at + n*frameMs})
			at += n * frameMs
		}
		it.EndMS = at
		s.Items = append(s.Items, it)
		pause := int64(200+rng.IntN(601)) / frameMs * frameMs
		if i+1 < len(pieces) && pieces[i+1].Para != p.Para {
			pause = 1200
		}
		at += pause
	}
	s.TotalMS = at + 500
	return s
}

// Truth returns the true times of every sentence the script reads from the edition.
func (s Script) Truth() Truth {
	t := Truth{}
	for _, it := range s.Items {
		if it.SID != "" && len(it.Spoken) > 0 {
			t[it.SID] = [2]int64{it.Spoken[0].StartMS, it.Spoken[len(it.Spoken)-1].EndMS}
		}
	}
	return t
}
