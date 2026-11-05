package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// ctSeries puts l.book at place 1 of the series "Saga" and adds the books with the given titles after it, in order.
func ctSeries(t *testing.T, l *rdrLab, titles ...string) []library.Book {
	t.Helper()
	var out []library.Book
	for i, ti := range titles {
		out = append(out, librarytest.AddBook(t, l.a.lib, library.Folder{ID: l.book.FolderID, LibraryID: l.book.LibraryID, Path: l.a.dir}, librarytest.BookSpec{
			Title: ti, Authors: []string{"Imogen Textauthor"}, DurationMs: sweepHourMs, AddedAt: int64(10 + i), Files: 1}))
	}
	err := l.a.lib.Update(context.Background(), func(tx *library.Tx) error {
		sid, err := tx.EnsureSeries("Saga", "")
		if err != nil {
			return err
		}
		if err := tx.SetBookSeries(l.book.ID, []library.BookSeriesLink{{SeriesID: sid, Sequence: "1"}}); err != nil {
			return err
		}
		for i, b := range out {
			if err := tx.SetBookSeries(b.ID, []library.BookSeriesLink{{SeriesID: sid, Sequence: string(rune('2' + i))}}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// ctEnd is the last chapter of the book as the fragment the reader appends.
func ctEnd(l *rdrLab, ck *http.Cookie) string {
	return l.get(ck, "/hx/books/"+l.book.ID+"/read/chapters/4?edition="+l.ed.ID, true).Body.String()
}

func TestSeriesNextEnd(t *testing.T) {
	l := rdrSeed(t)
	if strings.Contains(ctEnd(l, l.owner), "Next in") {
		t.Error("a book with no series offers a next one")
	}
	next := ctSeries(t, l, "Second Lantern", "Third Lantern")
	frag := ctEnd(l, l.owner)
	for _, w := range []string{"Next in Saga: Second Lantern", `href="/books/` + next[0].ID + `"`} {
		if !strings.Contains(frag, w) {
			t.Errorf("end block misses %q", w)
		}
	}
	if strings.Contains(frag, "Third Lantern") {
		t.Error("the link skips a book")
	}
	if strings.Contains(l.get(l.owner, "/hx/books/"+l.book.ID+"/read/chapters/2?edition="+l.ed.ID, true).Body.String(), "Next in") {
		t.Error("the link is on a chapter that is not the last")
	}
	if page := l.get(l.owner, l.readPath("ch=4"), false).Body.String(); !strings.Contains(page, "Next in Saga: Second Lantern") {
		t.Error("the reader page misses the next-in-series link")
	}
	// A hidden next book is skipped and its title never shows: a member holding only the first book sees no link.
	member := l.a.as(t, users.RoleMember)
	id := sweepUserID(t, l.a, member)
	if _, err := l.a.st.Writer().Exec(`INSERT INTO grants(id, user_id, scope, book_id, can_read, created_at, updated_at) VALUES ('gr_ct', ?, 'book', ?, 1, 1, 1)`, id, l.book.ID); err != nil {
		t.Fatal(err)
	}
	got := ctEnd(l, member)
	if !strings.Contains(got, "ts-reader__end") {
		t.Fatalf("the member cannot read the book: %.200s", got)
	}
	if strings.Contains(got, "Lantern</a>") || strings.Contains(got, "Second Lantern") || strings.Contains(got, "Next in") {
		t.Errorf("a book the person cannot see leaks: %.300s", got)
	}
}

func TestSeriesNextLast(t *testing.T) {
	l := rdrSeed(t)
	ctSeries(t, l)
	err := l.a.lib.Update(context.Background(), func(tx *library.Tx) error {
		links, err := tx.BookSeries(l.book.ID)
		if err != nil {
			return err
		}
		links[0].Sequence = "3"
		return tx.SetBookSeries(l.book.ID, links)
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ctEnd(l, l.owner), "Next in") {
		t.Error("the last book of a series offers a next one")
	}
}

func TestSeriesNextPage(t *testing.T) {
	l := rdrSeed(t)
	next := ctSeries(t, l, "Second Lantern")
	_, _, err := l.a.srv.progress.SaveReading(context.Background(), progress.ReadingSave{UserID: l.ownerID, BookID: next[0].ID,
		ChapterIdx: 2, CharPos: 100, PercentBP: 4200, ClientUpdatedAt: 5000, DeviceID: "device-test-0001"})
	if err != nil {
		t.Fatal(err)
	}
	links, err := l.a.lib.BookSeries(context.Background(), l.book.ID)
	if err != nil || len(links) == 0 {
		t.Fatalf("series: %v", err)
	}
	body := strings.ReplaceAll(l.get(l.owner, "/series/"+links[0].SeriesID, false).Body.String(), "&middot;", "·")
	for _, w := range []string{"Book 1 · Not started", "Book 2 · 42%"} {
		if !strings.Contains(body, w) {
			t.Errorf("series page misses %q in %.0s", w, body)
		}
	}
}

// ctPost posts a form to path as ck, with a valid token unless token is false, and returns the status.
func ctPost(t *testing.T, l *rdrLab, ck *http.Cookie, path string, token bool) int {
	t.Helper()
	form := url.Values{}
	if token {
		form.Set("_csrf", l.a.tokenFor(t, ck))
	}
	return l.a.do(http.MethodPost, path, form, nil, ck).Code
}
