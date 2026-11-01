package scan

import (
	"reflect"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
)

func pc(start, end int64, title string) media.ProbeChapter {
	return media.ProbeChapter{StartMs: start, EndMs: end, Title: title}
}

func ch(idx int, start, end int64, title, source string) library.Chapter {
	return library.Chapter{Idx: idx, StartMs: start, EndMs: end, Title: title, Source: source}
}

func TestBookChapters(t *testing.T) {
	threeUp := []media.ProbeChapter{pc(0, 2000, "One"), pc(2000, 4000, "Two"), pc(4000, 6000, "Three")}
	cases := []struct {
		name  string
		title string
		files []timed
		want  []library.Chapter
	}{
		{"no files", "B", nil, nil},
		{"single file with chapters", "B",
			[]timed{{DurationMs: 6000, Name: "b.m4b", Chapters: threeUp}},
			[]library.Chapter{ch(0, 0, 2000, "One", "file"), ch(1, 2000, 4000, "Two", "file"), ch(2, 4000, 6000, "Three", "file")}},
		{"single file without chapters", "B",
			[]timed{{DurationMs: 6000, Name: "b.m4b"}}, []library.Chapter{}},
		{"single file chapters clamped", "B",
			[]timed{{DurationMs: 5000, Name: "b.m4b", Chapters: []media.ProbeChapter{pc(0, 3000, "A"), pc(3000, 9000, "B"), pc(7000, 9000, "Gone")}}},
			[]library.Chapter{ch(0, 0, 3000, "A", "file"), ch(1, 3000, 5000, "B", "file")}},
		{"whole-book list repeated in every part", "B",
			[]timed{
				{OffsetMs: 0, DurationMs: 3000, Name: "p1.m4b", Chapters: []media.ProbeChapter{pc(0, 3000, "A"), pc(3000, 6000, "B")}},
				{OffsetMs: 3000, DurationMs: 3000, Name: "p2.m4b", Chapters: []media.ProbeChapter{pc(0, 3000, "A"), pc(3000, 6000, "B")}},
			},
			[]library.Chapter{ch(0, 0, 3000, "A", "file"), ch(1, 3000, 6000, "B", "file")}},
		{"same list that fits the first part is per-file", "B",
			[]timed{
				{OffsetMs: 0, DurationMs: 3000, Name: "p1.m4b", Chapters: []media.ProbeChapter{pc(0, 3000, "Ch")}},
				{OffsetMs: 3000, DurationMs: 3000, Name: "p2.m4b", Chapters: []media.ProbeChapter{pc(0, 3000, "Ch")}},
			},
			[]library.Chapter{ch(0, 0, 3000, "Ch", "file"), ch(1, 3000, 6000, "Ch", "file")}},
		{"per-file lists shifted, slivers dropped", "B",
			[]timed{
				{OffsetMs: 0, DurationMs: 4000, Name: "p1.m4b", Chapters: []media.ProbeChapter{pc(0, 2000, "A"), pc(2000, 4000, "B")}},
				{OffsetMs: 4000, DurationMs: 3000, Name: "p2.m4b", Chapters: []media.ProbeChapter{pc(0, 50, "Sliver"), pc(50, 3000, "C"), pc(3000, 3050, "Tail")}},
			},
			[]library.Chapter{ch(0, 0, 2000, "A", "file"), ch(1, 2000, 4000, "B", "file"), ch(2, 4050, 7000, "C", "file")}},
		{"one per file with unique tag titles", "Book",
			[]timed{
				{OffsetMs: 0, DurationMs: 1000, Name: "01.mp3", TagTitle: "Opening"},
				{OffsetMs: 1000, DurationMs: 2000, Name: "02.mp3", TagTitle: "Middle"},
			},
			[]library.Chapter{ch(0, 0, 1000, "Opening", "files"), ch(1, 1000, 3000, "Middle", "files")}},
		{"duplicate tag title falls back to file names", "Book",
			[]timed{
				{OffsetMs: 0, DurationMs: 1000, Name: "01 Intro.mp3", TagTitle: "Same"},
				{OffsetMs: 1000, DurationMs: 2000, Name: "02 End.mp3", TagTitle: "Same"},
			},
			[]library.Chapter{ch(0, 0, 1000, "01 Intro", "files"), ch(1, 1000, 3000, "02 End", "files")}},
		{"tag title equal to the book title falls back", "Book",
			[]timed{
				{OffsetMs: 0, DurationMs: 1000, Name: "a.mp3", TagTitle: "Book"},
				{OffsetMs: 1000, DurationMs: 1000, Name: "b.mp3", TagTitle: "Other"},
			},
			[]library.Chapter{ch(0, 0, 1000, "a", "files"), ch(1, 1000, 2000, "b", "files")}},
		{"a missing tag title falls back", "Book",
			[]timed{
				{OffsetMs: 0, DurationMs: 1000, Name: "a.mp3", TagTitle: "One"},
				{OffsetMs: 1000, DurationMs: 1000, Name: "b.mp3"},
			},
			[]library.Chapter{ch(0, 0, 1000, "a", "files"), ch(1, 1000, 2000, "b", "files")}},
		{"some parts without chapters: one per file", "Book",
			[]timed{
				{OffsetMs: 0, DurationMs: 1000, Name: "a.m4b", Chapters: []media.ProbeChapter{pc(0, 1000, "x")}},
				{OffsetMs: 1000, DurationMs: 1000, Name: "b.m4b"},
			},
			[]library.Chapter{ch(0, 0, 1000, "a", "files"), ch(1, 1000, 2000, "b", "files")}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := bookChapters(c.title, c.files)
			if !reflect.DeepEqual(got, c.want) && !(len(got) == 0 && len(c.want) == 0) {
				t.Fatalf("chapters =\n%+v\nwant\n%+v", got, c.want)
			}
		})
	}
}
