package progress_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

var statsSeq int

// statsSession inserts one finished session that started at `at` and listened for `ms`.
func statsSession(t testing.TB, st *db.Store, user, book string, at time.Time, ms int64) {
	t.Helper()
	statsSeq++
	_, err := st.Writer().Exec(`INSERT INTO listening_sessions(id, user_id, book_id, device_id, started_at, ended_at, start_ms, end_ms, listened_ms, speed_pct)
VALUES (?, ?, ?, 'dev', ?, ?, 0, ?, ?, 100)`, fmt.Sprintf("ses_%d", statsSeq), user, book, at.UnixMilli(), at.UnixMilli()+ms, ms, ms)
	if err != nil {
		t.Fatal(err)
	}
}

func statsFinished(t testing.TB, st *db.Store, user, book string, at time.Time) {
	t.Helper()
	_, err := st.Writer().Exec(`INSERT INTO progress(user_id, book_id, position_ms, duration_ms, finished_at, client_updated_at, server_updated_at, device_id)
VALUES (?, ?, 3600000, 3600000, ?, ?, ?, 'dev')`, user, book, at.UnixMilli(), at.UnixMilli(), at.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
}

func marks(s progress.Stats) string {
	out := ""
	for _, d := range s.Days {
		out += string(d.State[0])
	}
	return out // l listened, t today, m missed, a ahead
}

func TestStatsQueries(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no zoneinfo")
	}
	// Wednesday 2026-11-04 10:00 in New York; the week began Sunday 2026-11-01, the day DST ended (a 25-hour day).
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, ny)
	min := int64(60_000)
	type ses struct {
		at time.Time
		ms int64
	}
	day := func(m time.Month, d, h, mi int) time.Time { return time.Date(2026, m, d, h, mi, 0, 0, ny) }
	cases := []struct {
		name         string
		loc          *time.Location
		now          time.Time
		sessions     []ses
		week         int64
		streak       int
		todayDone    bool
		marks        string
		finishedAt   []time.Time
		wantFinished int
	}{
		{name: "empty", loc: ny, now: now, marks: "mmmtaaa"},
		{name: "week sum; Saturday is not in the week", loc: ny, now: now, marks: "lmmlaaa", week: 30*min + 59_000, streak: 1, todayDone: true,
			sessions: []ses{{day(10, 31, 12, 0), 9 * min}, {day(11, 1, 9, 0), 10 * min}, {day(11, 2, 9, 0), 59_000}, {day(11, 4, 8, 0), 20 * min}}},
		{name: "23:30 local counts that day", loc: ny, now: now, marks: "mmltaaa", week: 5 * min, streak: 1,
			sessions: []ses{{day(11, 3, 23, 30), 5 * min}}},
		{name: "23:50 to 00:10 local marks both days", loc: ny, now: now, marks: "mlltaaa", week: 20 * min, streak: 2,
			sessions: []ses{{day(11, 2, 23, 50), 20 * min}}},
		{name: "DST week: Saturday 23:30 EDT is still Saturday, the 25-hour Sunday starts 00:00 EDT", loc: ny, now: now, marks: "llltaaa", week: 8 * min, streak: 4,
			sessions: []ses{{day(10, 31, 23, 30), 2 * min}, {day(11, 1, 0, 30), 2 * min}, {day(11, 1, 23, 30), 2 * min}, {day(11, 2, 12, 0), 2 * min}, {day(11, 3, 12, 0), 2 * min}}},
		{name: "streak through yesterday when today is empty", loc: ny, now: now, marks: "mlltaaa", week: 4 * min, streak: 2,
			sessions: []ses{{day(11, 2, 12, 0), 2 * min}, {day(11, 3, 12, 0), 2 * min}}},
		{name: "a 59 second day breaks the streak", loc: ny, now: now, marks: "lmltaaa", week: 4*min + 59_000, streak: 1,
			sessions: []ses{{day(11, 1, 12, 0), 2 * min}, {day(11, 2, 12, 0), 59_000}, {day(11, 3, 12, 0), 2 * min}}},
		{name: "UTC cuts a Tuesday evening New York session on Wednesday", loc: time.UTC, now: now, marks: "mmmlaaa", week: 5 * min, streak: 1, todayDone: true,
			sessions: []ses{{day(11, 3, 21, 30), 5 * min}}},
		{name: "finished: the year starts at local midnight", loc: ny, now: now, marks: "mmmtaaa", wantFinished: 2,
			finishedAt: []time.Time{day(1, 1, 0, 5), day(10, 1, 12, 0), time.Date(2025, 12, 31, 23, 30, 0, 0, ny)}},
		{name: "finished: the year ends at local midnight", loc: ny, now: now, marks: "mmmtaaa", wantFinished: 1,
			finishedAt: []time.Time{time.Date(2026, 12, 31, 23, 30, 0, 0, ny), time.Date(2027, 1, 1, 0, 30, 0, 0, ny)}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, st, u, books, _ := progEnv(t)
			// Another person's rows must never count.
			statsSession(t, st, u[1], books[0].ID, now, 500*min)
			statsFinished(t, st, u[1], books[0].ID, now)
			for _, s := range c.sessions {
				statsSession(t, st, u[0], books[0].ID, s.at, s.ms)
			}
			for i, f := range c.finishedAt {
				statsFinished(t, st, u[0], books[i%len(books)].ID, f)
			}
			got, err := svc.Stats(ctx, u[0], c.loc, c.now)
			if err != nil {
				t.Fatal(err)
			}
			if got.WeekMs != c.week || got.Streak != c.streak || got.TodayDone != c.todayDone || marks(got) != c.marks || got.Finished != c.wantFinished || got.Year != 2026 {
				t.Fatalf("got week=%d streak=%d today=%v marks=%s finished=%d year=%d; want week=%d streak=%d today=%v marks=%s finished=%d",
					got.WeekMs, got.Streak, got.TodayDone, marks(got), got.Finished, got.Year, c.week, c.streak, c.todayDone, c.marks, c.wantFinished)
			}
		})
	}
}

// TestStatsStreakPaging: a streak longer than the 64-day window pages back, stops at the first gap
// and at the oldest session.
func TestStatsStreakPaging(t *testing.T) {
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name        string
		days, gapAt int // consecutive days ending today; a missing day at that many days back (0: none)
		want        int
	}{
		{"150 days to the oldest session", 150, 0, 150},
		{"exactly the window", 64, 0, 64},
		{"gap after the first page", 150, 100, 100},
		{"gap on the window edge", 150, 64, 64},
		{"gap on the first day of page two", 150, 65, 65},
	} {
		t.Run(c.name, func(t *testing.T) {
			svc, st, u, books, _ := progEnv(t)
			for i := 0; i < c.days; i++ {
				if c.gapAt > 0 && i == c.gapAt {
					continue
				}
				statsSession(t, st, u[0], books[0].ID, now.AddDate(0, 0, -i).Add(-2*time.Hour), 2*60_000)
			}
			got, err := svc.Stats(ctx, u[0], time.UTC, now)
			if err != nil {
				t.Fatal(err)
			}
			if got.Streak != c.want || !got.TodayDone {
				t.Fatalf("streak %d (today %v), want %d", got.Streak, got.TodayDone, c.want)
			}
		})
	}
}
