package web

import (
	"context"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// lsSeries puts the text book at place 2 of a series, beside a first book nobody has opened.
func lsSeries(t *testing.T, l *fmt2Lab) {
	t.Helper()
	ctx := context.Background()
	id, err := l.a.lib.CreateTextBook(ctx, library.TextBookIn{LibraryID: l.book.LibraryID, FolderID: l.book.FolderID,
		RootPath: l.a.dir + "/prologue.epub", Title: "Prologue Pages"})
	if err != nil {
		t.Fatal(err)
	}
	err = l.a.lib.Update(ctx, func(tx *library.Tx) error {
		sid, err := tx.EnsureSeries("Saga", "")
		if err != nil {
			return err
		}
		if err := tx.SetBookSeries(id, []library.BookSeriesLink{{SeriesID: sid, Sequence: "1"}}); err != nil {
			return err
		}
		return tx.SetBookSeries(l.tbook.ID, []library.BookSeriesLink{{SeriesID: sid, Sequence: "2"}})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// lsRow is Home from the "Your series in progress" heading on, or "" when it is not there.
func lsRow(body string) string {
	i := strings.Index(body, "Your series in progress")
	if i < 0 {
		return ""
	}
	return body[i:]
}

func TestSeriesFormatsHome(t *testing.T) {
	l := fmt2Seed(t)
	lsSeries(t, l)
	if lsRow(l.get(l.owner, "/", false).Body.String()) != "" {
		t.Fatal("a series nobody touched is listed")
	}
	fmt2Reading(t, l, l.owner, l.tbook.ID, 4200, 5000)
	fmt2Listened(t, l, l.owner, l.book.ID, 9000) // the hero is the audiobook, not the series' book
	row := lsRow(l.get(l.owner, "/", false).Body.String())
	for _, w := range []string{"Book 2 of 2", "Plain Prose", "42%", `href="/books/` + l.tbook.ID + `/read"`, `aria-label="Continue series Saga"`, "ts-iconbtn"} {
		if !strings.Contains(row, w) {
			t.Errorf("series row: missing %q in %.600s", w, row)
		}
	}
	if strings.Contains(row, `data-ts-play="`+l.tbook.ID) {
		t.Error("the row has a play control as well as a Read one")
	}
	// The hero's own book carries no control in its series row: make the text book the newest place.
	fmt2Reading(t, l, l.owner, l.tbook.ID, 4300, 20_000)
	body := l.get(l.owner, "/", false).Body.String()
	if !strings.Contains(body, "Continue reading") {
		t.Fatal("the text book is not the hero")
	}
	if strings.Contains(lsRow(body), `/read"`) {
		t.Error("the hero's book has a second resume control in its series row")
	}
	// Someone with no right to read gets no Read control; another person sees none of this reading.
	fmt2Reading(t, l, l.noRead, l.tbook.ID, 1500, 5000)
	if got := lsRow(l.get(l.noRead, "/", false).Body.String()); strings.Contains(got, "/read") {
		t.Errorf("no right to read, yet Read: %.300s", got)
	}
	if got := lsRow(l.get(l.admin, "/", false).Body.String()); got != "" {
		t.Errorf("the admin sees the owner's series: %.200s", got)
	}
}

func TestSeriesFormatsTile(t *testing.T) {
	books := []library.BookCard{{ID: "a", DurationMs: 100_000}, {ID: "b", DurationMs: 100_000}, {ID: "c", DurationMs: 100_000}}
	listen := func(pos, at int64, fin bool) progress.Progress {
		p := progress.Progress{PositionMs: pos, DurationMs: 100_000, ClientUpdatedAt: at}
		if fin {
			p.FinishedAt = at
		}
		return p
	}
	read := func(bp int, at int64, fin bool) progress.ReadingPosition {
		p := progress.ReadingPosition{PercentBP: bp, ClientUpdatedAt: at}
		if fin {
			p.FinishedAt = at
		}
		return p
	}
	type reads = map[string]progress.ReadingPosition
	type listens = map[string]progress.Progress
	for _, c := range []struct {
		name string
		i    int
		prog listens
		read reads
		want string
	}{
		{"read only", 1, nil, reads{"b": read(4200, 1, false)}, "42%"},
		{"read clamps to 99", 1, nil, reads{"b": read(9990, 1, false)}, "99%"},
		{"read clamps up to 1", 1, nil, reads{"b": read(20, 1, false)}, "1%"},
		{"finished by reading", 1, nil, reads{"b": read(10000, 1, true)}, "Finished"},
		{"finished by listening, reading open", 1, listens{"b": listen(100, 1, true)}, reads{"b": read(4200, 2, false)}, "Finished"},
		{"reading newest wins", 1, listens{"b": listen(70_000, 1, false)}, reads{"b": read(2500, 2, false)}, "25%"},
		{"listening newest wins", 1, listens{"b": listen(70_000, 3, false)}, reads{"b": read(2500, 2, false)}, "70%"},
		{"reading place at zero", 2, nil, reads{"c": read(0, 1, false)}, "Not started"},
		{"up next", 1, nil, reads{"a": read(4200, 1, false)}, "Next in series"},
	} {
		if got := seriesTileState(c.i, 0, books, c.prog, c.read); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestSeriesFormatsCardLine(t *testing.T) {
	l := fmt2Seed(t)
	fmt2Reading(t, l, l.owner, l.tbook.ID, 4200, 5000)
	fmt2Listened(t, l, l.owner, l.book.ID, 3000) // 600000 of 3600000 ms: 17 %
	page := l.get(l.owner, "/libraries", false).Body.String()
	for _, w := range []string{"42 % through", "17 % through", `data-ts-value="42"`, `data-ts-value="17"`} {
		if !strings.Contains(page, w) {
			t.Errorf("library grid: missing %q", w)
		}
	}
	if frag := l.get(l.owner, "/hx/libraries/items", true).Body.String(); !strings.Contains(frag, "42 % through") {
		t.Error("the items fragment draws no line")
	}
	// Nobody else's progress shows.
	if other := l.get(l.admin, "/libraries", false).Body.String(); strings.Contains(other, "% through") {
		t.Error("another person's progress shows on their cards")
	}
	// A finished book has no line.
	if _, err := l.a.srv.progress.SetReadingFinished(context.Background(), fmt2Uid(t, l, l.owner), l.tbook.ID, true); err != nil {
		t.Fatal(err)
	}
	page = l.get(l.owner, "/libraries", false).Body.String()
	if strings.Contains(page, "42 % through") || !strings.Contains(page, "17 % through") {
		t.Error("a finished book keeps its line, or an unfinished one lost it")
	}
}

func TestSeriesFormatsCardState(t *testing.T) {
	d := int64(3_600_000)
	for _, c := range []struct {
		name string
		p    *progress.Progress
		rp   *progress.ReadingPosition
		want bookStand
	}{
		{"nothing", nil, nil, bookStand{}},
		{"listening", &progress.Progress{PositionMs: d / 2, DurationMs: d}, nil, bookStand{Started: true, Percent: 50}},
		{"audio without a length", &progress.Progress{PositionMs: 5}, nil, bookStand{Started: true, Percent: -1}},
		{"reading", nil, &progress.ReadingPosition{PercentBP: 1234}, bookStand{Started: true, Percent: 12}},
		{"finished in either", &progress.Progress{PositionMs: 5, DurationMs: d, ClientUpdatedAt: 9}, &progress.ReadingPosition{PercentBP: 1, FinishedAt: 1, ClientUpdatedAt: 1}, bookStand{Finished: true}},
		{"newer reading", &progress.Progress{PositionMs: d / 2, DurationMs: d, ClientUpdatedAt: 1}, &progress.ReadingPosition{PercentBP: 2000, ClientUpdatedAt: 2}, bookStand{Started: true, Percent: 20}},
	} {
		if got := standIn(c.p, c.rp, 0); got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}
}
