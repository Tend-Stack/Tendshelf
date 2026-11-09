package web

import (
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
)

func TestRdrDropCapAt(t *testing.T) {
	prose := strings.Repeat("It was a quiet road and the lantern burned low. ", 4)
	para := func(text string, links ...ebook.Link) ebook.Block {
		return ebook.Block{Kind: ebook.KindP, Text: text, Links: links}
	}
	cases := []struct {
		name  string
		front bool
		b     ebook.Block
		want  bool
	}{
		{"prose", false, para(prose), true},
		{"prose in front matter", true, para(prose), false},
		{"dedication line", false, para("The dedication"), false},
		{"epigraph line", false, para("Oh, the torment, the torment of the long night"), false},
		{"just under the length", false, para(strings.Repeat("a", rdrDropCapMin-1)), false},
		{"just at the length", false, para(strings.Repeat("a", rdrDropCapMin)), true},
		{"list item", false, ebook.Block{Kind: ebook.KindLI, Text: prose}, false},
		{"quote", false, ebook.Block{Kind: ebook.KindQuote, Text: prose}, false},
		{"paragraph of links", false, para(prose, ebook.Link{Start: 0, End: len(prose), To: "b-2"}), false},
		{"prose with one link", false, para(prose, ebook.Link{Start: 3, End: 10, To: "b-2"}), true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ch := ebook.Chapter{Front: c.front, Blocks: []ebook.Block{c.b}}
			if got := rdrDropCapAt(ch, 0); got != c.want {
				t.Errorf("rdrDropCapAt = %v, want %v", got, c.want)
			}
		})
	}
}

func TestRdrVerseRuns(t *testing.T) {
	short := ebook.Block{Kind: ebook.KindP, Text: "A short line"}
	long := ebook.Block{Kind: ebook.KindP, Text: strings.Repeat("A long line of prose. ", 5)}
	quote := ebook.Block{Kind: ebook.KindQuote, Text: "x"}
	cases := []struct {
		name string
		in   []ebook.Block
		want []bool
	}{
		{"three short lines", []ebook.Block{short, short, short}, []bool{true, true, true}},
		{"two short lines are not a run", []ebook.Block{short, short, long}, []bool{false, false, false}},
		{"run after prose", []ebook.Block{long, short, short, short, long}, []bool{false, true, true, true, false}},
		{"a quote breaks the run", []ebook.Block{short, short, quote, short}, []bool{false, false, false, false}},
		{"empty", nil, []bool{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rdrVerseRuns(c.in)
			if len(got) != len(c.want) {
				t.Fatalf("len %d, want %d", len(got), len(c.want))
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Errorf("block %d: %v, want %v", i, got[i], c.want[i])
				}
			}
		})
	}
}
