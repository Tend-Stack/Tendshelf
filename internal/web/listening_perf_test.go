package web

import (
	"context"
	"fmt"
	"math/rand"
	"net/http"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// sweepPerfSessions gives one person 30 synthetic listening sessions a day (60 s to 21 min, random
// book from books, speeds 100 to 145 %) over the days before now, and a finished book on about one
// day in twenty. rng makes it repeatable. days counts back from now: the first day is now minus
// `days` days. The rows go in one transaction.
func sweepPerfSessions(t testing.TB, st *db.Store, userID string, books []string, rng *rand.Rand, now time.Time, days int) {
	t.Helper()
	tx, err := st.Writer().Begin()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for day := now.AddDate(0, 0, -days); day.Before(now); day = day.AddDate(0, 0, 1) {
		for k := 0; k < 30; k++ {
			start := day.Add(time.Duration(rng.Intn(86_400)) * time.Second)
			span := int64(60_000 + rng.Intn(1_200_000))
			n++
			if _, err := tx.Exec(`INSERT INTO listening_sessions(id, user_id, book_id, device_id, started_at, ended_at, start_ms, end_ms, listened_ms, speed_pct)
VALUES (?, ?, ?, 'dev', ?, ?, 0, ?, ?, ?)`, fmt.Sprintf("%s_%d", userID, n), userID, books[rng.Intn(len(books))], start.UnixMilli(), start.UnixMilli()+span, span, span, 100+5*rng.Intn(10)); err != nil {
				t.Fatal(err)
			}
		}
		if rng.Intn(20) == 0 {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO progress(user_id, book_id, position_ms, duration_ms, finished_at, client_updated_at, server_updated_at, device_id)
VALUES (?, ?, 1, 1, ?, ?, ?, 'dev')`, userID, books[rng.Intn(len(books))], day.UnixMilli(), day.UnixMilli(), day.UnixMilli()); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

// TestPerfListeningYear times every read behind the year view of the listening dashboard (the year's
// numbers, the year totals, top books/authors/narrators, finished books and Profile's stats) on 4 people
// with 3 years of synthetic sessions each (30 a day, about 33,000 per person, 200 books, fixed seed).
// After one warm-up the median of five runs must be 150 ms or less, without -race. `make perf` runs it.
// The page itself (package B) adds only rendering on top of these reads.
func TestPerfListeningYear(t *testing.T) {
	sweepPerfOn(t)
	ctx := context.Background()
	st, err := db.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	repo := library.New(st)
	_, f := librarytest.NewLibrary(t, repo, "Main", "/audio")
	var books []string
	for i := 0; i < 200; i++ {
		b := librarytest.AddBook(t, repo, f, librarytest.BookSpec{Title: fmt.Sprintf("Book %03d", i), DurationMs: 36_000_000,
			Authors: []string{fmt.Sprintf("Author %d", i%40)}, Narrators: []string{fmt.Sprintf("Narrator %d", i%25)}})
		books = append(books, b.ID)
	}
	users := []string{"us_p0", "us_p1", "us_p2", "us_p3"}
	rng := rand.New(rand.NewSource(7))
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	for _, u := range users {
		if _, err := st.Writer().Exec(`INSERT INTO users(id, username, role, created_at) VALUES (?, ?, 'member', 1)`, u, u); err != nil {
			t.Fatal(err)
		}
		sweepPerfSessions(t, st, u, books, rng, now, 1096)
	}
	svc := progress.New(st, nil)
	sc := library.Scope{Books: share.Filter{SQL: "1=1"}, Libraries: share.Filter{SQL: "1=1"}}
	loc := time.UTC
	p, _ := progress.PeriodOf(progress.RangeYear, now, loc)
	run := func() time.Duration {
		begin := time.Now()
		u := users[0]
		if _, err := svc.Listening(ctx, u, loc, p, now); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.YearTotals(ctx, u, loc, now); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.TopListened(ctx, sc, u, p.From.UnixMilli(), p.To.UnixMilli(), 5, 3); err != nil {
			t.Fatal(err)
		}
		if _, err := repo.FinishedBetween(ctx, sc, u, p.From.UnixMilli(), p.To.UnixMilli(), 24); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Stats(ctx, u, loc, now); err != nil {
			t.Fatal(err)
		}
		return time.Since(begin)
	}
	run() // warm-up
	var ms []float64
	for i := 0; i < 5; i++ {
		ms = append(ms, float64(run().Microseconds())/1000)
	}
	sort.Float64s(ms)
	t.Logf("listening year view reads on 4 x ~33,000 sessions (cpu %s): runs %v ms, median %.1f ms (budget 150 ms)", sweepCPUModel(), ms, ms[2])
	if ms[2] > 150 {
		t.Fatalf("median %.1f ms is over the 150 ms budget", ms[2])
	}
}

// TestPerfListeningPage times the whole GET /me/listening?range=year (reads, rendering and the page
// shell) for a person with 3 years of synthetic sessions (30 a day, about 33,000), with three other
// people's rows of the same size in the table. After one warm-up the median of five must be 150 ms or
// less, without -race.
func TestPerfListeningPage(t *testing.T) {
	sweepPerfOn(t)
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	a := newTestApp(t, func(o *Options) { o.Now = func() time.Time { return now } })
	owner := a.owner(t)
	ownerID := sweepUserID(t, a, owner)
	statsZone(t, a, ownerID, "UTC")
	_, f := librarytest.NewLibrary(t, a.lib, "Main", adminAudioDir(t, false))
	var books []string
	for i := 0; i < 200; i++ {
		b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: fmt.Sprintf("Book %03d", i), DurationMs: 36_000_000,
			Authors: []string{fmt.Sprintf("Author %d", i%40)}, Narrators: []string{fmt.Sprintf("Narrator %d", i%25)}})
		books = append(books, b.ID)
	}
	rng := rand.New(rand.NewSource(7))
	tx, err := a.st.Writer().Begin()
	if err != nil {
		t.Fatal(err)
	}
	others := []string{"us_p1", "us_p2", "us_p3"}
	for _, u := range others {
		if _, err := tx.Exec(`INSERT INTO users(id, username, role, created_at) VALUES (?, ?, 'member', 1)`, u, u); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	for _, u := range append([]string{ownerID}, others...) {
		for day := now.AddDate(-3, 0, 0); day.Before(now); day = day.AddDate(0, 0, 1) {
			for k := 0; k < 30; k++ {
				start := day.Add(time.Duration(rng.Intn(86_400)) * time.Second)
				span := int64(60_000 + rng.Intn(1_200_000))
				n++
				if _, err := tx.Exec(`INSERT INTO listening_sessions(id, user_id, book_id, device_id, started_at, ended_at, start_ms, end_ms, listened_ms, speed_pct)
VALUES (?, ?, ?, 'dev', ?, ?, 0, ?, ?, ?)`, fmt.Sprintf("pg_%d", n), u, books[rng.Intn(len(books))], start.UnixMilli(), start.UnixMilli()+span, span, span, 100+5*rng.Intn(10)); err != nil {
					t.Fatal(err)
				}
			}
			if rng.Intn(20) == 0 {
				if _, err := tx.Exec(`INSERT OR IGNORE INTO progress(user_id, book_id, position_ms, duration_ms, finished_at, client_updated_at, server_updated_at, device_id)
VALUES (?, ?, 1, 1, ?, ?, ?, 'dev')`, u, books[rng.Intn(len(books))], day.UnixMilli(), day.UnixMilli(), day.UnixMilli()); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	run := func() time.Duration {
		begin := time.Now()
		rec := a.do(http.MethodGet, "/me/listening?range=year", nil, nil, owner)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Year by year") {
			t.Fatalf("page = %d", rec.Code)
		}
		return time.Since(begin)
	}
	run() // warm-up
	var ms []float64
	for i := 0; i < 5; i++ {
		ms = append(ms, float64(run().Microseconds())/1000)
	}
	sort.Float64s(ms)
	t.Logf("GET /me/listening?range=year on 4 x ~33,000 sessions (cpu %s): runs %v ms, median %.1f ms (budget 150 ms)", sweepCPUModel(), ms, ms[2])
	if ms[2] > 150 {
		t.Fatalf("median %.1f ms is over the 150 ms budget", ms[2])
	}
}
