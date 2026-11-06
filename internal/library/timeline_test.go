package library_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

func tlIDs(rows []library.TimelineRow) []string {
	var out []string
	for _, x := range rows {
		out = append(out, x.ID)
	}
	return out
}

// tlSession inserts a session with a fixed id that ended at end (Unix ms) after one minute.
func tlSession(t *testing.T, r *library.Repo, id, user, book string, end, endMs int64) {
	t.Helper()
	st := r.Store()
	if _, err := st.Writer().Exec(`INSERT INTO listening_sessions(id, user_id, book_id, device_id, started_at, ended_at, start_ms, end_ms, listened_ms, speed_pct)
VALUES (?, ?, ?, 'dev', ?, ?, 0, ?, 60000, 100)`, id, user, book, end-60_000, end, endMs); err != nil {
		t.Fatal(err)
	}
}

func TestTimelineKeysetAndTies(t *testing.T) {
	r, _, _, f := listenSeed(t)
	b := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "A", DurationMs: 3_600_000})
	base := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC).UnixMilli()
	tlSession(t, r, "ts_old", listenOwner, b.ID, base-1000, 10)
	tlSession(t, r, "ts_b", listenOwner, b.ID, base, 10) // same end time as ts_a: ties by id
	tlSession(t, r, "ts_a", listenOwner, b.ID, base, 10)
	tlSession(t, r, "ts_new", listenOwner, b.ID, base+1000, 10)

	var all []string
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		rows, next, err := r.Timeline(ctx, allScope, listenOwner, "", cursor, 3)
		if err != nil {
			t.Fatal(err)
		}
		if next != "" && len(rows) != 3 {
			t.Fatalf("a full page was %d rows", len(rows))
		}
		all = append(all, tlIDs(rows)...)
		if next == "" {
			break
		}
		cursor = next
	}
	if want := []string{"ts_new", "ts_a", "ts_b", "ts_old"}; !reflect.DeepEqual(all, want) {
		t.Fatalf("got %v, want %v", all, want)
	}
	rows, _, err := r.Timeline(ctx, allScope, listenOwner, "", "garbage", 2)
	if err != nil || len(rows) != 2 || rows[0].ID != "ts_new" {
		t.Fatalf("a bad cursor is the first page: %v %v", tlIDs(rows), err)
	}
}

func TestTimelineScopeAndRevoked(t *testing.T) {
	r, _, _, f := listenSeed(t)
	shown := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Shown", DurationMs: 3_600_000})
	gone := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Gone", DurationMs: 3_600_000})
	base := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC).UnixMilli()
	tlSession(t, r, "ts_1", listenOwner, shown.ID, base, 10)
	tlSession(t, r, "ts_2", listenOwner, gone.ID, base+1, 10)
	tlSession(t, r, "ts_3", listenMember, shown.ID, base+2, 10) // another person's

	rows, _, err := r.Timeline(ctx, allScope, listenOwner, "", "", 50)
	if err != nil || !reflect.DeepEqual(tlIDs(rows), []string{"ts_2", "ts_1"}) {
		t.Fatalf("own rows only: %v %v", tlIDs(rows), err)
	}
	rows, _, _ = r.Timeline(ctx, listenScope(gone.ID), listenOwner, "", "", 50)
	if !reflect.DeepEqual(tlIDs(rows), []string{"ts_1"}) {
		t.Fatalf("revoked book still listed: %v", tlIDs(rows))
	}
	rows, _, _ = r.Timeline(ctx, listenScope(gone.ID), listenOwner, gone.ID, "", 50)
	if len(rows) != 0 {
		t.Fatalf("a book the person cannot see lists nothing: %v", tlIDs(rows))
	}
	rows, _, _ = r.Timeline(ctx, allScope, listenOwner, shown.ID, "", 50)
	if !reflect.DeepEqual(tlIDs(rows), []string{"ts_1"}) {
		t.Fatalf("book filter: %v", tlIDs(rows))
	}
}

func TestTimelineFindsTheChapterAtTheEnd(t *testing.T) {
	r, _, _, f := listenSeed(t)
	b := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Chapters", DurationMs: 3_000_000})
	for i, c := range []struct {
		s, e int64
		t    string
	}{{0, 1000, "One"}, {1000, 2000, "Two"}, {2000, 3000, "Three"}} {
		if _, err := r.Store().Writer().Exec(`INSERT INTO chapters(book_id, idx, start_ms, end_ms, title, source) VALUES (?, ?, ?, ?, ?, 'user')`, b.ID, i, c.s, c.e, c.t); err != nil {
			t.Fatal(err)
		}
	}
	base := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC).UnixMilli()
	for i, end := range []int64{0, 1500, 2000} {
		tlSession(t, r, fmt.Sprintf("ts_%d", i), listenOwner, b.ID, base-int64(i), end)
	}
	rows, _, err := r.Timeline(ctx, allScope, listenOwner, "", "", 50)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, x := range rows {
		got = append(got, fmt.Sprintf("%d:%s", x.ChapterIdx, x.Chapter))
	}
	if want := []string{"0:One", "1:Two", "2:Three"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}
}
