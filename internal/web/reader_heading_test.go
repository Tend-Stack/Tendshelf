package web

import (
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/ebook"
)

func TestRdrOverline(t *testing.T) {
	chs := []ebook.Chapter{
		{Title: "Cover", Front: true},
		{Title: "Prologue: 9:17 P.M."},
		{Title: "Chapter 1: The Glass Box"},
		{Title: "The Road"},
		{Title: "Capítulo 7"},
		{Title: "Epílogo"},
		{Title: "Chapter One"},
	}
	want := []string{"Opening pages", "", "Chapter 1", "Chapter 2", "Chapter 7", "", "Chapter 4"}
	for i, w := range want {
		if got := rdrOverline(chs, i+1); got != w {
			t.Errorf("%q: overline %q, want %q", chs[i].Title, got, w)
		}
	}
}

func TestRdrRepeatedHeading(t *testing.T) {
	h := func(s string) ebook.Block { return ebook.Block{Kind: ebook.KindH, Text: s} }
	p := ebook.Block{Kind: ebook.KindP, Text: "It was a quiet road."}
	img := ebook.Block{Kind: ebook.KindImg, Img: "a.jpg"}
	cases := []struct {
		name   string
		title  string
		blocks []ebook.Block
		want   int
	}{
		{"same words run together", "Chapter 1: The Glass Box", []ebook.Block{h("Chapter 1The Glass Box"), p}, 0},
		{"same heading", "Prologue: 9:17 P.M.", []ebook.Block{h("Prologue9:17 P.M."), img, p}, 0},
		{"after a picture", "The Road", []ebook.Block{img, h("THE ROAD"), p}, 1},
		{"another heading", "Chapter 1", []ebook.Block{h("The Glass Box"), p}, -1},
		{"after a paragraph", "The Road", []ebook.Block{p, h("The Road")}, -1},
		{"no title", "", []ebook.Block{h(""), p}, -1},
		{"no heading", "The Road", []ebook.Block{p}, -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := rdrRepeatedHeading(ebook.Chapter{Title: c.title, Blocks: c.blocks}); got != c.want {
				t.Errorf("got %d, want %d", got, c.want)
			}
		})
	}
}
