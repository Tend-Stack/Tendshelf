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

// The line that says where someone is reading names the chapter as the reader's heading does. A book whose
// file opens with a cover, a title page, a copyright page and contents keeps "Chapter 12" in its sixteenth
// section, and the line must not say 16.
func TestReadPlaceName(t *testing.T) {
	rows := []ebook.ChapterRow{
		{Title: "Cover", Front: true},
		{Title: "Title Page", Front: true},
		{Title: "Copyright", Front: true},
		{Title: "Contents", Front: true},
		{Title: "Prologue: 9:17 P.M."},
		{Title: "Chapter 1: The Glass Box"},
		{Title: "The Road"},
		{Title: "Chapter 12: Eye of the Storm"},
		{Title: ""},
		{Title: "Epilogue"},
	}
	cases := []struct {
		idx   int
		name  string
		where string
	}{
		{1, "Opening pages", "Opening pages · 18 %"},
		{5, "Prologue: 9:17 P.M.", "Prologue: 9:17 P.M. · 18 %"},
		{6, "Chapter 1", "Chapter 1 · 18 %"},
		{7, "Chapter 2", "Chapter 2 · 18 %"},
		{8, "Chapter 12", "Chapter 12 · 18 %"},
		{9, "Chapter 4", "Chapter 4 · 18 %"},
		{10, "Epilogue", "Epilogue · 18 %"},
		{0, "", "18 %"},
		{11, "", "18 %"},
	}
	for _, c := range cases {
		name := readPlaceName(rows, c.idx)
		if name != c.name {
			t.Errorf("chapter %d: name %q, want %q", c.idx, name, c.name)
		}
		if where := readWhereText(name, 1850); where != c.where {
			t.Errorf("chapter %d: line %q, want %q", c.idx, where, c.where)
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
