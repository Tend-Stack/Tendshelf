package library_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// lsRead stores a reading place. at is a number of seconds that orders saves, on the same clock as listenSave.
func lsRead(t *testing.T, svc *progress.Service, user, book string, bp int, at int64, finished bool) {
	t.Helper()
	base := time.Now().UnixMilli() - 1_000_000
	if _, _, err := svc.SaveReading(ctx, progress.ReadingSave{UserID: user, BookID: book, ChapterIdx: 1, DeviceID: "device-test-1",
		ClientUpdatedAt: base + at*1000, PercentBP: bp, Finished: finished}); err != nil {
		t.Fatal(err)
	}
}

func TestSeriesFormatsRepo(t *testing.T) {
	type save struct {
		book   int // 1-based
		text   bool
		bp     int
		at     int64
		finish bool
	}
	cases := []struct {
		name     string
		saves    []save
		hide     int
		other    bool // the saves belong to the member, the owner is asked
		wantNext int  // 0 = series absent
		started  bool
		readBP   int
		readLast bool
		done     int
	}{
		{"started by reading only", []save{{2, true, 4200, 1, false}}, 0, false, 2, true, 4200, true, 0},
		{"finished by reading moves on", []save{{2, true, 10000, 1, true}}, 0, false, 3, false, 0, false, 1},
		{"listening only is unchanged", []save{{2, false, 5000, 1, false}}, 0, false, 2, true, 0, false, 0},
		{"both, reading newest wins", []save{{2, false, 5000, 1, false}, {2, true, 3000, 2, false}}, 0, false, 2, true, 3000, true, 0},
		{"both, listening newest wins", []save{{2, true, 3000, 1, false}, {2, false, 5000, 2, false}}, 0, false, 2, true, 3000, false, 0},
		{"listened, then finished reading", []save{{2, false, 5000, 1, false}, {2, true, 10000, 2, true}}, 0, false, 3, false, 0, false, 1},
		{"hidden book's reading not shown", []save{{2, true, 4200, 1, false}}, 2, false, 0, false, 0, false, 0},
		{"another person's reading ignored", []save{{2, true, 4200, 1, false}}, 0, true, 0, false, 0, false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, _, svc, f := listenSeed(t)
			var books []library.Book
			for i := 1; i <= 4; i++ {
				books = append(books, librarytest.AddBook(t, r, f, librarytest.BookSpec{
					Title: fmt.Sprintf("S%d", i), Series: "Saga", Sequence: fmt.Sprint(i), DurationMs: 3_600_000}))
			}
			user := listenOwner
			if c.other {
				user = listenMember
			}
			for _, s := range c.saves {
				if s.text {
					lsRead(t, svc, user, books[s.book-1].ID, s.bp, s.at, s.finish)
				} else {
					listenSave(t, svc, user, books[s.book-1].ID, int64(s.bp), s.at, s.finish)
				}
			}
			sc := allScope
			if c.hide > 0 {
				sc = listenScope(books[c.hide-1].ID)
			}
			got, err := r.SeriesInProgress(ctx, sc, listenOwner, 10)
			if err != nil {
				t.Fatal(err)
			}
			if c.wantNext == 0 {
				if len(got) != 0 {
					t.Fatalf("got %+v", got)
				}
				return
			}
			if len(got) != 1 {
				t.Fatalf("got %d series", len(got))
			}
			g := got[0]
			if g.Next.ID != books[c.wantNext-1].ID || g.Started != c.started || g.ReadBP != c.readBP || g.ReadLast != c.readLast || g.Done != c.done {
				t.Fatalf("got next %s started %v readBP %d readLast %v done %d, want next %d started %v %d %v %d",
					g.Next.Title, g.Started, g.ReadBP, g.ReadLast, g.Done, c.wantNext, c.started, c.readBP, c.readLast, c.done)
			}
		})
	}
}
