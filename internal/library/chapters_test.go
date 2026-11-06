package library_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

func TestChapterEditBuild(t *testing.T) {
	many := func(n int) []library.ChapterDraft {
		out := make([]library.ChapterDraft, n)
		for i := range out {
			out[i] = library.ChapterDraft{StartMs: int64(i) * 8, Title: fmt.Sprint("c", i)}
		}
		return out
	}
	d := func(start int64, title string) library.ChapterDraft {
		return library.ChapterDraft{StartMs: start, Title: title}
	}
	cases := []struct {
		name string
		in   []library.ChapterDraft
		want []library.Chapter
		code string
		row  int
	}{
		{"ends derived", []library.ChapterDraft{d(0, "A"), d(1000, " B "), d(4000, "C")}, []library.Chapter{
			{Idx: 0, StartMs: 0, EndMs: 1000, Title: "A", Source: "user"},
			{Idx: 1, StartMs: 1000, EndMs: 4000, Title: "B", Source: "user"},
			{Idx: 2, StartMs: 4000, EndMs: 9000, Title: "C", Source: "user"}}, "", 0},
		{"one row", []library.ChapterDraft{d(0, "Only")}, []library.Chapter{{StartMs: 0, EndMs: 9000, Title: "Only", Source: "user"}}, "", 0},
		{"control characters dropped", []library.ChapterDraft{d(0, "A\x00\tB\n")}, []library.Chapter{{StartMs: 0, EndMs: 9000, Title: "AB", Source: "user"}}, "", 0},
		{"empty", nil, nil, "empty", -1},
		{"first not zero", []library.ChapterDraft{d(5, "A")}, nil, "first_not_zero", 0},
		{"equal start", []library.ChapterDraft{d(0, "A"), d(0, "B")}, nil, "out_of_order", 1},
		{"going back", []library.ChapterDraft{d(0, "A"), d(5000, "B"), d(4000, "C")}, nil, "out_of_order", 2},
		{"at the end", []library.ChapterDraft{d(0, "A"), d(9000, "B")}, nil, "past_end", 1},
		{"past the end", []library.ChapterDraft{d(0, "A"), d(9001, "B")}, nil, "past_end", 1},
		{"no title", []library.ChapterDraft{d(0, "A"), d(10, "  ")}, nil, "no_title", 1},
		{"only control characters", []library.ChapterDraft{d(0, "\x01")}, nil, "no_title", 0},
		{"title 500 ok", []library.ChapterDraft{d(0, strings.Repeat("é", 500))}, []library.Chapter{{StartMs: 0, EndMs: 9000, Title: strings.Repeat("é", 500), Source: "user"}}, "", 0},
		{"title 501", []library.ChapterDraft{d(0, strings.Repeat("é", 501))}, nil, "title_long", 0},
		{"1000 rows", many(1000), nil, "", 0},
		{"1001 rows", many(1001), nil, "too_many", -1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := library.BuildChapters(c.in, 9000, "user")
			if c.code != "" {
				if err == nil || err.Code != c.code || err.Row != c.row {
					t.Fatalf("err = %v, want %s row %d", err, c.code, c.row)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.want != nil && !reflect.DeepEqual(got, c.want) {
				t.Errorf("got %+v, want %+v", got, c.want)
			}
			if len(c.in) == 1000 && (len(got) != 1000 || got[999].EndMs != 9000) {
				t.Errorf("1000 rows: %d, last end %d", len(got), got[999].EndMs)
			}
		})
	}
}

func TestChapterEditHelpers(t *testing.T) {
	file := []library.Chapter{{Source: "file"}, {Source: "sidecar"}}
	if library.ChaptersOverridden(file) || library.ChaptersOverridden(nil) {
		t.Error("file chapters are not an override")
	}
	for _, s := range []string{"user", "online"} {
		if !library.ChaptersOverridden(append(file, library.Chapter{Source: s})) {
			t.Errorf("%s not seen as an override", s)
		}
	}
	a := []library.Chapter{{Idx: 0, StartMs: 0, EndMs: 5, Title: "A", Source: "user"}}
	b := []library.Chapter{{Idx: 0, StartMs: 0, EndMs: 5, Title: "B", Source: "user"}}
	if r := library.ChaptersRev(a); len(r) != 16 || r != library.ChaptersRev(a) || r == library.ChaptersRev(b) {
		t.Errorf("rev %q", r)
	}
	if !library.ChaptersPastDuration([]library.Chapter{{EndMs: 10_001}}, 9000) || library.ChaptersPastDuration([]library.Chapter{{EndMs: 10_000}}, 9000) {
		t.Error("past-duration limit is one second")
	}
}
