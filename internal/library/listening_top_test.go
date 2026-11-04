package library_test

import (
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

const topMin = int64(60_000)

var topSeq int

// topSession inserts a session of listenedMs that started at start and lasted spanMs on the wall clock.
func topSession(t *testing.T, st *db.Store, user, book string, start time.Time, spanMs, listenedMs int64) {
	t.Helper()
	topSeq++
	if _, err := st.Writer().Exec(`INSERT INTO listening_sessions(id, user_id, book_id, device_id, started_at, ended_at, start_ms, end_ms, listened_ms, speed_pct)
VALUES (?, ?, ?, 'dev', ?, ?, 0, ?, ?, 100)`, fmt.Sprintf("tses_%d", topSeq), user, book, start.UnixMilli(), start.UnixMilli()+spanMs, listenedMs, listenedMs); err != nil {
		t.Fatal(err)
	}
}

func topNames(l []library.TopName) []string {
	var out []string
	for _, n := range l {
		out = append(out, fmt.Sprintf("%s:%d", n.Name, n.Ms/topMin))
	}
	return out
}

func topBooks(l []library.TopBook) []string {
	var out []string
	for _, b := range l {
		out = append(out, fmt.Sprintf("%s:%d", b.Book.Title, b.Ms/topMin))
	}
	return out
}

func TestTopListened(t *testing.T) {
	r, st, _, f := listenSeed(t)
	add := func(title string, authors, narrators []string) library.Book {
		return librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: title, DurationMs: 3_600_000, Authors: authors, Narrators: narrators})
	}
	big := add("Big", []string{"Ann"}, []string{"Nat"})
	mid := add("Mid", []string{"Ann", "Bob"}, []string{"Nat"})
	tieA := add("Alpha", []string{"Cy"}, nil)
	tieB := add("Beta", []string{"Di"}, nil)
	tiny := add("Tiny", []string{"Eve"}, nil)
	hidden := add("Hidden", []string{"Hal"}, []string{"Hen"})
	day := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	from, to := day.Add(-12*time.Hour).UnixMilli(), day.Add(12*time.Hour).UnixMilli() // Nov 3 00:00 to Nov 4 00:00
	span := func(m int64) int64 { return m * topMin }
	topSession(t, st, listenOwner, big.ID, day, span(60), span(60))
	topSession(t, st, listenOwner, mid.ID, day, span(30), span(30))
	topSession(t, st, listenOwner, tieB.ID, day, span(10), span(10))
	topSession(t, st, listenOwner, tieA.ID, day, span(10), span(10))
	topSession(t, st, listenOwner, tiny.ID, day, span(1)-1, span(1)-1)
	topSession(t, st, listenOwner, hidden.ID, day, span(500), span(500))
	topSession(t, st, listenMember, tieA.ID, day, span(900), span(900)) // another person's

	got, err := r.TopListened(ctx, allScope, listenOwner, from, to, 5, 3)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Hidden:500", "Big:60", "Mid:30", "Alpha:10", "Beta:10"}; !reflect.DeepEqual(topBooks(got.Books), want) {
		t.Fatalf("books %v, want %v (ties by title, under a minute dropped)", topBooks(got.Books), want)
	}
	if want := []string{"Hal:500", "Ann:90", "Bob:30"}; !reflect.DeepEqual(topNames(got.Authors), want) {
		t.Fatalf("authors %v, want %v (Ann gets Big and Mid in full; Mid credits Bob too)", topNames(got.Authors), want)
	}
	if want := []string{"Hen:500", "Nat:90"}; !reflect.DeepEqual(topNames(got.Narrators), want) {
		t.Fatalf("narrators %v, want %v", topNames(got.Narrators), want)
	}

	// A hidden book is absent from books, authors and narrators.
	got, err = r.TopListened(ctx, listenScope(hidden.ID), listenOwner, from, to, 5, 3)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Big:60", "Mid:30", "Alpha:10", "Beta:10"}; !reflect.DeepEqual(topBooks(got.Books), want) {
		t.Fatalf("scoped books %v, want %v", topBooks(got.Books), want)
	}
	if want := []string{"Ann:90", "Bob:30", "Cy:10"}; !reflect.DeepEqual(topNames(got.Authors), want) {
		t.Fatalf("scoped authors %v, want %v", topNames(got.Authors), want)
	}
	if want := []string{"Nat:90"}; !reflect.DeepEqual(topNames(got.Narrators), want) {
		t.Fatalf("scoped narrators %v, want %v", topNames(got.Narrators), want)
	}

	// Limits apply.
	if got, _ = r.TopListened(ctx, allScope, listenOwner, from, to, 2, 1); len(got.Books) != 2 || len(got.Authors) != 1 {
		t.Fatalf("limits: %d books, %d authors", len(got.Books), len(got.Authors))
	}
	// Nothing in another period.
	if got, _ = r.TopListened(ctx, allScope, listenOwner, to, to+86_400_000, 5, 3); len(got.Books)+len(got.Authors)+len(got.Narrators) != 0 {
		t.Fatalf("empty period: %+v", got)
	}
}

func TestTopListenedClipsAtThePeriodEdges(t *testing.T) {
	r, st, _, f := listenSeed(t)
	book := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Edge", DurationMs: 3_600_000, Authors: []string{"Ann"}})
	other := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Capped", DurationMs: 3_600_000})
	mid := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC) // the period starts here
	from, to := mid.UnixMilli(), mid.AddDate(0, 1, 0).UnixMilli()
	topSession(t, st, listenOwner, book.ID, mid.Add(-20*time.Minute), 60*topMin, 60*topMin)                  // 40 of 60 minutes inside
	topSession(t, st, listenOwner, book.ID, mid.AddDate(0, 1, 0).Add(-30*time.Minute), 60*topMin, 60*topMin) // 30 of 60 inside
	topSession(t, st, listenOwner, book.ID, mid.Add(-3*time.Hour), 60*topMin, 60*topMin)                     // entirely before
	// A 30 h span counts only its last 24 h: 10 h of listening spread over 30 h, ending 6 h into the period,
	// leaves 6 of its last 24 hours inside, so 10 h * 6/24 = 150 minutes.
	topSession(t, st, listenOwner, other.ID, mid.Add(6*time.Hour-30*time.Hour), 30*60*topMin, 10*60*topMin)
	got, err := r.TopListened(ctx, allScope, listenOwner, from, to, 5, 3)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"Capped:150", "Edge:70"}; !reflect.DeepEqual(topBooks(got.Books), want) {
		t.Fatalf("books %v, want %v", topBooks(got.Books), want)
	}
}

// The per-book time here and the dashboard total there use the same rule.
func TestTopListenedMatchesListening(t *testing.T) {
	r, st, svc, f := listenSeed(t)
	book := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "One", DurationMs: 3_600_000, Authors: []string{"Ann"}})
	p, err := progress.PeriodOf(progress.RangeMonth, time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC), time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	rows := 0
	for _, s := range []struct {
		start      time.Time
		span, ms   int64
		wantInside bool
	}{
		{time.Date(2026, 10, 31, 23, 10, 0, 0, time.UTC), 7_000_001, 6_999_999, true},
		{time.Date(2026, 11, 10, 8, 20, 0, 0, time.UTC), 5_000_003, 4_000_001, true},
		{time.Date(2026, 11, 30, 23, 0, 0, 0, time.UTC), 9_000_007, 8_000_011, true},
		{time.Date(2026, 12, 2, 8, 0, 0, 0, time.UTC), 1_000_000, 1_000_000, false},
	} {
		topSession(t, st, listenOwner, book.ID, s.start, s.span, s.ms)
		rows++
	}
	l, err := svc.Listening(ctx, listenOwner, time.UTC, p, time.Date(2026, 11, 20, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	top, err := r.TopListened(ctx, allScope, listenOwner, p.From.UnixMilli(), p.To.UnixMilli(), 5, 3)
	if err != nil || len(top.Books) != 1 {
		t.Fatalf("top %+v err=%v", top, err)
	}
	if d := top.Books[0].Ms - l.TotalMs; d < -int64(rows) || d > int64(rows) {
		t.Fatalf("SQL clip %d vs Listening %d differ by %d ms (allowed %d)", top.Books[0].Ms, l.TotalMs, d, rows)
	}
}

func TestFinishedBetween(t *testing.T) {
	r, st, _, f := listenSeed(t)
	mk := func(title string) library.Book {
		return librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: title, DurationMs: 3_600_000})
	}
	a, b, c, d := mk("A"), mk("B"), mk("C"), mk("D")
	save := func(user, book string, at time.Time) {
		t.Helper()
		if _, err := st.Writer().Exec(`INSERT INTO progress(user_id, book_id, position_ms, duration_ms, finished_at, client_updated_at, server_updated_at, device_id)
VALUES (?, ?, 3600000, 3600000, ?, ?, ?, 'dev')`, user, book, at.UnixMilli(), at.UnixMilli(), at.UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	day := func(dd, h int) time.Time { return time.Date(2026, 11, dd, h, 0, 0, 0, time.UTC) }
	save(listenOwner, a.ID, day(1, 0))                                    // first instant: inside
	save(listenOwner, b.ID, day(15, 9))                                   // inside
	save(listenOwner, c.ID, day(30, 23))                                  // inside, newest
	save(listenOwner, d.ID, time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC)) // end bound is exclusive
	save(listenMember, b.ID, day(16, 9))                                  // another person's
	from, to := day(1, 0).UnixMilli(), time.Date(2026, 12, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	titles := func(l []library.FinishedBook) []string {
		var out []string
		for _, x := range l {
			out = append(out, x.Book.Title)
		}
		return out
	}
	got, err := r.FinishedBetween(ctx, allScope, listenOwner, from, to, 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"C", "B", "A"}; !reflect.DeepEqual(titles(got), want) || got[0].At != day(30, 23).UnixMilli() {
		t.Fatalf("got %v, want %v newest first", titles(got), want)
	}
	if got, _ = r.FinishedBetween(ctx, listenScope(b.ID), listenOwner, from, to, 10); !reflect.DeepEqual(titles(got), []string{"C", "A"}) {
		t.Fatalf("scoped: %v", titles(got))
	}
	if got, _ = r.FinishedBetween(ctx, allScope, listenOwner, from, to, 1); !reflect.DeepEqual(titles(got), []string{"C"}) {
		t.Fatalf("limit: %v", titles(got))
	}
	if got, _ = r.FinishedBetween(ctx, allScope, listenOwner, to+1000, to+2000, 10); len(got) != 0 {
		t.Fatalf("empty period: %v", titles(got))
	}
}
