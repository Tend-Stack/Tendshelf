package progress_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

var rstSeq int

// rstSession inserts one reading session that ended at `end` after `ms` of active reading and `chars` read.
func rstSession(t testing.TB, st *db.Store, user, book string, end time.Time, ms, chars int64) {
	t.Helper()
	rstSeq++
	_, err := st.Writer().Exec(`INSERT INTO reading_sessions(id, user_id, book_id, device_id, started_at, ended_at, chars_read, active_ms)
VALUES (?, ?, ?, 'dev', ?, ?, ?, ?)`, fmt.Sprintf("rds_t%d", rstSeq), user, book, end.UnixMilli()-ms, end.UnixMilli(), chars, ms)
	if err != nil {
		t.Fatal(err)
	}
}

func rstFinished(t testing.TB, st *db.Store, user, book string, at time.Time) {
	t.Helper()
	_, err := st.Writer().Exec(`INSERT INTO reading_positions(user_id, book_id, chapter_idx, char_pos, percent_bp, finished_at, client_updated_at, server_updated_at, device_id)
VALUES (?, ?, 1, 0, 10000, ?, ?, ?, 'dev')`, user, book, at.UnixMilli(), at.UnixMilli(), at.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
}

func TestReadStatsService(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no zoneinfo")
	}
	tokyo, err := time.LoadLocation("Asia/Tokyo")
	if err != nil {
		t.Skip("no zoneinfo")
	}
	min := int64(60_000)
	// 2026-11-04 10:00 UTC: Wednesday morning in New York, Wednesday evening in Tokyo.
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC)
	type ses struct {
		end   time.Time
		ms    int64
		chars int64
		other bool // belongs to the other person
	}
	utc := func(m time.Month, d, h, mi int) time.Time { return time.Date(2026, m, d, h, mi, 0, 0, time.UTC) }
	cases := []struct {
		name                  string
		loc                   *time.Location
		sessions              []ses
		ever                  bool
		total                 int64
		daysRead, streak, cpm int
		day                   int // index in the month, with want ms
		dayMs                 int64
	}{
		{name: "empty", loc: ny, day: 3},
		{name: "another person's sessions are ignored", loc: ny, day: 3,
			sessions: []ses{{end: utc(11, 4, 9, 0), ms: 10 * min, chars: 1000, other: true}}},
		{name: "02:30 UTC is the evening before in New York, and the next morning in Tokyo", loc: ny, ever: true, total: 5 * min, daysRead: 1, streak: 1, cpm: 20, day: 2, dayMs: 5 * min,
			sessions: []ses{{end: utc(11, 4, 2, 30), ms: 5 * min, chars: 100}}},
		{name: "the same session in Tokyo", loc: tokyo, ever: true, total: 5 * min, daysRead: 1, streak: 1, cpm: 20, day: 3, dayMs: 5 * min,
			sessions: []ses{{end: utc(11, 4, 2, 30), ms: 5 * min, chars: 100}}},
		{name: "speed: 6000 characters in 10 minutes is 600 a minute", loc: time.UTC, ever: true, total: 10 * min, daysRead: 1, streak: 1, cpm: 600, day: 3, dayMs: 10 * min,
			sessions: []ses{{end: utc(11, 4, 8, 0), ms: 4 * min, chars: 2400}, {end: utc(11, 4, 9, 0), ms: 6 * min, chars: 3600}}},
		{name: "streak with a gap: today and yesterday, then a hole", loc: time.UTC, ever: true, total: 9 * min, daysRead: 3, streak: 2, day: 3, dayMs: 3 * min,
			sessions: []ses{{end: utc(11, 4, 8, 0), ms: 3 * min}, {end: utc(11, 3, 8, 0), ms: 3 * min}, {end: utc(11, 1, 8, 0), ms: 3 * min}}},
		{name: "a streak through yesterday while today is empty", loc: time.UTC, ever: true, total: 6 * min, daysRead: 2, streak: 2, day: 3,
			sessions: []ses{{end: utc(11, 3, 8, 0), ms: 3 * min}, {end: utc(11, 2, 8, 0), ms: 3 * min}}},
		{name: "a 59 second day does not count as read", loc: time.UTC, ever: true, total: 59_000, streak: 0, day: 3, dayMs: 59_000,
			sessions: []ses{{end: utc(11, 4, 8, 0), ms: 59_000, chars: 10}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, st, u, books, _ := progEnv(t)
			for _, s := range c.sessions {
				user := u[0]
				if s.other {
					user = u[1]
				}
				rstSession(t, st, user, books[0].ID, s.end, s.ms, s.chars)
			}
			y, m, d := now.In(c.loc).Date()
			p, err := progress.PeriodOf(progress.RangeMonth, time.Date(y, m, d, 0, 0, 0, 0, time.UTC), c.loc)
			if err != nil {
				t.Fatal(err)
			}
			r, err := svc.ReadingStats(context.Background(), u[0], c.loc, p, now)
			if err != nil {
				t.Fatal(err)
			}
			if r.Ever != c.ever || r.TotalMs != c.total || r.DaysRead != c.daysRead || r.Streak != c.streak || r.Cpm != c.cpm {
				t.Errorf("got ever=%v total=%d days=%d streak=%d cpm=%d; want %v %d %d %d %d",
					r.Ever, r.TotalMs, r.DaysRead, r.Streak, r.Cpm, c.ever, c.total, c.daysRead, c.streak, c.cpm)
			}
			if len(r.Days) != p.Days {
				t.Fatalf("%d days, want %d", len(r.Days), p.Days)
			}
			if got := r.Days[c.day]; got != c.dayMs {
				t.Errorf("day %d = %d ms, want %d", c.day+1, got, c.dayMs)
			}
		})
	}
}

func TestReadStatsFinishes(t *testing.T) {
	svc, st, u, books, _ := progEnv(t)
	ctx := context.Background()
	at := func(d int) time.Time { return time.Date(2026, 11, d, 12, 0, 0, 0, time.UTC) }
	rstFinished(t, st, u[0], books[0].ID, at(2))
	rstFinished(t, st, u[0], books[1].ID, at(3))
	rstFinished(t, st, u[1], books[2].ID, at(3)) // another person
	from, to := at(1).UnixMilli(), at(30).UnixMilli()
	got, err := svc.ReadingFinishes(ctx, u[0], from, to)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].BookID != books[1].ID || got[1].BookID != books[0].ID {
		t.Errorf("finishes = %+v, want newest first and only this person's", got)
	}
	if n, err := svc.CountReadingFinished(ctx, u[0], from, to); err != nil || n != 2 {
		t.Errorf("count = %d, %v", n, err)
	}
}
