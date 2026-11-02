package web

import (
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

func TestSeriesTileState(t *testing.T) {
	books := []library.BookCard{{ID: "a"}, {ID: "b"}, {ID: "c"}, {ID: "d"}, {ID: "e"}}
	prog := map[string]progress.Progress{
		"a": {FinishedAt: 5, PositionMs: 100, DurationMs: 100},
		"c": {PositionMs: 40_000, DurationMs: 100_000},
		"d": {PositionMs: 10, DurationMs: 0},
		"e": {PositionMs: 1, DurationMs: 1_000_000},
	}
	for _, c := range []struct {
		name  string
		i, at int
		prog  map[string]progress.Progress
		want  string
	}{
		{"this book beats everything", 0, 0, prog, "This book"},
		{"finished", 0, 1, prog, "Finished"},
		{"percent", 2, 1, prog, "40%"},
		{"percent beats up next", 2, 1, prog, "40%"},
		{"unknown length", 3, 1, prog, "Started"},
		{"percent clamps up to 1", 4, 1, prog, "1%"},
		{"up next", 1, 0, prog, "Up next"},
		{"not started", 1, 3, prog, "Not started"},
		{"up next needs a current book", 1, -1, prog, "Not started"},
		{"no progress at all", 2, 0, nil, "Not started"},
		{"clamps down to 99", 1, 0, map[string]progress.Progress{"b": {PositionMs: 99_900, DurationMs: 100_000}}, "99%"},
	} {
		if got := seriesTileState(c.i, c.at, books, c.prog); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestBookSeriesPlace(t *testing.T) {
	for _, c := range []struct {
		name string
		seq  string
		seqs map[string]string
		want string
	}{
		{"whole run", "2", map[string]string{"a": "1", "b": "2", "c": "3"}, "Book 2 of 3"},
		{"gap", "1", map[string]string{"a": "1", "c": "3"}, "Book 1"},
		{"fraction", "1.5", map[string]string{"a": "1", "b": "1.5", "c": "2"}, "Book 1.5"},
		{"duplicate places", "1", map[string]string{"a": "1", "b": "1"}, "Book 1"},
		{"no place", "", map[string]string{"a": "1"}, ""},
		{"nothing visible", "2", nil, "Book 2"},
		{"padded", " 2 ", map[string]string{"a": "1", "b": "2"}, "Book 2 of 2"},
	} {
		if got := bookSeriesPlace(c.seq, c.seqs); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestBookOverline(t *testing.T) {
	now := time.Date(2026, time.November, 2, 12, 0, 0, 0, time.UTC)
	march := time.Date(2026, time.March, 9, 8, 0, 0, 0, time.UTC).UnixMilli()
	lastYear := time.Date(2025, time.January, 30, 23, 0, 0, 0, time.UTC).UnixMilli()
	series := library.BookDetail{Series: []library.SeriesRef{{ID: "s", Name: "Harbour Lights", Sequence: "2"}}}
	for _, c := range []struct {
		name  string
		d     library.BookDetail
		place string
		p     *progress.Progress
		want  string
	}{
		{"this year", series, "Book 2 of 3", &progress.Progress{FinishedAt: march}, "Harbour Lights · Book 2 of 3 · finished in March"},
		{"other year", series, "Book 2", &progress.Progress{FinishedAt: lastYear}, "Harbour Lights · Book 2 · finished in January 2025"},
		{"unfinished", series, "Book 2 of 3", &progress.Progress{PositionMs: 5}, "Harbour Lights · Book 2 of 3"},
		{"no progress", series, "", nil, "Harbour Lights"},
		{"no series, finished", library.BookDetail{}, "", &progress.Progress{FinishedAt: march}, "Finished in March"},
		{"no series, nothing", library.BookDetail{}, "", nil, ""},
	} {
		if got := bookOverline(c.d, c.place, c.p, now); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestListenRuler(t *testing.T) {
	chs := []library.Chapter{
		{StartMs: 0, EndMs: 60_000}, {StartMs: 60_000, EndMs: 120_000}, {StartMs: 120_000, EndMs: 180_000},
	}
	v := listenRulerFor(chs, 90_000, 180_000, 1)
	if v.Label != "Book progress, chapter 2 of 3" || v.Now != "50.0" || len(v.Ticks) != 2 || v.Ticks[0] != "33.3" || v.Ticks[1] != "66.7" {
		t.Errorf("ruler: %+v", v)
	}
	if !strings.HasPrefix(v.Where, "Chapter 2 · 1 min left in chapter · ") || !strings.HasSuffix(v.Where, " left in book") {
		t.Errorf("where: %q", v.Where)
	}
	if v.WhereShort != "Chapter 2 · 1 min left" {
		t.Errorf("where short: %q", v.WhereShort)
	}
	none := listenRulerFor(nil, 90_000, 3_600_000, 0)
	if none.Label != "Book progress" || len(none.Ticks) != 0 || none.Where != "59 m left in book" || none.WhereShort != "59 m left" {
		t.Errorf("no chapters: %+v", none)
	}
	// A faster speed shortens the time left.
	fast := listenRulerFor(nil, 0, 7_200_000, 2)
	if fast.WhereShort != "1 h left" {
		t.Errorf("speed 2: %q", fast.WhereShort)
	}
}
