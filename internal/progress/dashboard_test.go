package progress_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// dashSession inserts a session with an explicit end and speed.
func dashSession(t testing.TB, st *db.Store, user, book string, start, end time.Time, listenedMs int64, speed int) {
	t.Helper()
	statsSeq++
	_, err := st.Writer().Exec(`INSERT INTO listening_sessions(id, user_id, book_id, device_id, started_at, ended_at, start_ms, end_ms, listened_ms, speed_pct)
VALUES (?, ?, ?, 'dev', ?, ?, 0, ?, ?, ?)`, fmt.Sprintf("dses_%d", statsSeq), user, book, start.UnixMilli(), end.UnixMilli(), listenedMs, listenedMs, speed)
	if err != nil {
		t.Fatal(err)
	}
}

func TestPeriodOf(t *testing.T) {
	utc := time.UTC
	d := func(y int, m time.Month, day int) time.Time { return time.Date(y, m, day, 15, 0, 0, 0, utc) }
	cases := []struct {
		name, rng string
		day       time.Time
		first     string
		days      int
	}{
		{"Sunday starts its own week", progress.RangeWeek, d(2026, 11, 1), "2026-11-01", 7},
		{"Wednesday's week", progress.RangeWeek, d(2026, 11, 4), "2026-11-01", 7},
		{"Saturday's week", progress.RangeWeek, d(2026, 11, 7), "2026-11-01", 7},
		{"week across a year", progress.RangeWeek, d(2027, 1, 1), "2026-12-27", 7},
		{"Feb 2028 has 29 days", progress.RangeMonth, d(2028, 2, 10), "2028-02-01", 29},
		{"Feb 2026 has 28 days", progress.RangeMonth, d(2026, 2, 28), "2026-02-01", 28},
		{"2028 has 366 days", progress.RangeYear, d(2028, 6, 1), "2028-01-01", 366},
		{"2026 has 365 days", progress.RangeYear, d(2026, 12, 31), "2026-01-01", 365},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := progress.PeriodOf(c.rng, c.day, utc)
			if err != nil {
				t.Fatal(err)
			}
			if p.First.Format("2006-01-02") != c.first || p.Days != c.days || !p.To.Equal(p.From.AddDate(0, 0, c.days)) {
				t.Fatalf("got first=%s days=%d from=%v to=%v", p.First.Format("2006-01-02"), p.Days, p.From, p.To)
			}
		})
	}
	if _, err := progress.PeriodOf("decade", d(2026, 1, 1), utc); !errors.Is(err, progress.ErrRange) {
		t.Fatalf("unknown range: %v", err)
	}
	if ny, err := time.LoadLocation("America/New_York"); err == nil {
		p, _ := progress.PeriodOf(progress.RangeWeek, d(2026, 11, 4), ny) // the week with the 25-hour Sunday
		if got := p.To.Sub(p.From); got != 7*24*time.Hour+time.Hour {
			t.Fatalf("DST week length %v", got)
		}
		if p.From.Hour() != 0 || p.To.Hour() != 0 || p.From.Location() != ny {
			t.Fatalf("bounds are not local midnights: %v %v", p.From, p.To)
		}
	}
}

func TestPeriodShift(t *testing.T) {
	utc := time.UTC
	for _, c := range []struct {
		rng   string
		from  time.Time
		n     int
		first string
		days  int
	}{
		{progress.RangeMonth, time.Date(2026, 1, 15, 0, 0, 0, 0, utc), -1, "2025-12-01", 31},
		{progress.RangeMonth, time.Date(2026, 12, 31, 0, 0, 0, 0, utc), 2, "2027-02-01", 28},
		{progress.RangeWeek, time.Date(2026, 12, 30, 0, 0, 0, 0, utc), 1, "2027-01-03", 7},
		{progress.RangeWeek, time.Date(2027, 1, 2, 0, 0, 0, 0, utc), -1, "2026-12-20", 7},
		{progress.RangeYear, time.Date(2027, 3, 1, 0, 0, 0, 0, utc), -1, "2026-01-01", 365},
		{progress.RangeYear, time.Date(2027, 3, 1, 0, 0, 0, 0, utc), 1, "2028-01-01", 366},
	} {
		p, _ := progress.PeriodOf(c.rng, c.from, utc)
		q := p.Shift(c.n, utc)
		if q.First.Format("2006-01-02") != c.first || q.Days != c.days || q.Range != c.rng {
			t.Errorf("%s shift %d: got %s %d days, want %s %d", c.rng, c.n, q.First.Format("2006-01-02"), q.Days, c.first, c.days)
		}
	}
}

func TestListening(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no zoneinfo")
	}
	const min = int64(60_000)
	at := func(m time.Month, d, h, mi int) time.Time { return time.Date(2026, m, d, h, mi, 0, 0, ny) }
	now := at(11, 4, 10, 0)
	type ses struct {
		start, end time.Time
		ms         int64
		speed      int
	}
	plain := func(start time.Time, ms int64) ses {
		return ses{start, start.Add(time.Duration(ms) * time.Millisecond), ms, 100}
	}
	cases := []struct {
		name         string
		rng          string
		day          time.Time
		sessions     []ses
		total        int64
		daysListened int
		daysSoFar    int
		speed        int
		hours        map[int]int64
		weekdays     map[int]int64
		dayMs        map[int]int64 // index in period
	}{
		{name: "empty month", rng: progress.RangeMonth, day: now, daysSoFar: 4},
		{name: "month totals; October falls outside", rng: progress.RangeMonth, day: now, daysSoFar: 4, speed: 100,
			sessions: []ses{plain(at(11, 2, 9, 0), 30*min), plain(at(11, 2, 9, 30), 90*min), plain(at(11, 3, 21, 0), 30*min), plain(at(10, 31, 21, 0), 60*min)},
			total:    150 * min, daysListened: 2, hours: map[int]int64{9: 60 * min, 10: 60 * min, 21: 30 * min}, // 9:30-11:00 of the 90 min lands 9:30-10, 10-11
			dayMs: map[int]int64{1: 120 * min, 2: 30 * min}},
		{name: "Oct 31 23:30 to Nov 1 00:30 gives half to each month (Nov)", rng: progress.RangeMonth, day: now, daysSoFar: 4, speed: 100,
			sessions: []ses{plain(at(10, 31, 23, 30), 60*min)},
			total:    30 * min, daysListened: 1, hours: map[int]int64{0: 30 * min}, dayMs: map[int]int64{0: 30 * min}},
		{name: "Oct 31 23:30 to Nov 1 00:30 gives half to each month (Oct)", rng: progress.RangeMonth, day: at(10, 15, 0, 0), daysSoFar: 31, speed: 100,
			sessions: []ses{plain(at(10, 31, 23, 30), 60*min)},
			total:    30 * min, daysListened: 1, hours: map[int]int64{23: 30 * min}, dayMs: map[int]int64{30: 30 * min}},
		{name: "week of 4 Nov: Sunday to Saturday", rng: progress.RangeWeek, day: now, daysSoFar: 4, speed: 100,
			sessions: []ses{plain(at(11, 1, 8, 0), 5*min), plain(at(11, 7, 23, 0), 5*min), plain(at(11, 8, 0, 0), 5*min)},
			total:    10 * min, daysListened: 2, hours: map[int]int64{8: 5 * min, 23: 5 * min}, weekdays: map[int]int64{0: 5 * min, 6: 5 * min}},
		{name: "past month: all days so far", rng: progress.RangeMonth, day: at(9, 10, 0, 0), daysSoFar: 30},
		{name: "future month: no days so far", rng: progress.RangeMonth, day: at(12, 10, 0, 0), daysSoFar: 0},
		{name: "speed is weighted by time: 60 m at 100 and 20 m at 200 is 125", rng: progress.RangeMonth, day: now, daysSoFar: 4, speed: 125,
			sessions: []ses{{at(11, 2, 9, 0), at(11, 2, 10, 0), 60 * min, 100}, {at(11, 3, 9, 0), at(11, 3, 9, 20), 20 * min, 200}},
			total:    80 * min, daysListened: 2, hours: map[int]int64{9: 80 * min}, dayMs: map[int]int64{1: 60 * min, 2: 20 * min}},
		{name: "spring forward day in New York", rng: progress.RangeWeek, day: at(3, 8, 12, 0), daysSoFar: 7, speed: 100,
			sessions: []ses{{at(3, 8, 1, 30), at(3, 8, 3, 30), 60 * min, 100}},
			total:    60 * min, daysListened: 1, hours: map[int]int64{1: 30 * min, 3: 30 * min}, weekdays: map[int]int64{0: 60 * min}},
		{name: "fall back day in New York", rng: progress.RangeWeek, day: at(11, 1, 12, 0), daysSoFar: 4, speed: 100,
			sessions: []ses{{at(11, 1, 0, 30), time.Date(2026, 11, 1, 1, 30, 0, 0, ny).Add(time.Hour), 120 * min, 100}}, // 00:30 EDT to 01:30 EST (2 h)
			total:    120 * min, daysListened: 1, hours: map[int]int64{0: 30 * min, 1: 90 * min}, weekdays: map[int]int64{0: 120 * min}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			svc, st, u, books, _ := progEnv(t)
			// Another person's rows must never count.
			dashSession(t, st, u[1], books[0].ID, now.Add(-time.Hour), now, 60*min, 100)
			for _, s := range c.sessions {
				dashSession(t, st, u[0], books[0].ID, s.start, s.end, s.ms, s.speed)
			}
			p, err := progress.PeriodOf(c.rng, c.day, ny)
			if err != nil {
				t.Fatal(err)
			}
			got, err := svc.Listening(ctx, u[0], ny, p, now)
			if err != nil {
				t.Fatal(err)
			}
			if got.TotalMs != c.total || got.DaysListened != c.daysListened || got.DaysSoFar != c.daysSoFar || got.SpeedPct != c.speed {
				t.Fatalf("total=%d listened=%d sofar=%d speed=%d; want %d %d %d %d", got.TotalMs, got.DaysListened, got.DaysSoFar, got.SpeedPct,
					c.total, c.daysListened, c.daysSoFar, c.speed)
			}
			if len(got.Days) != p.Days {
				t.Fatalf("days %d, want %d", len(got.Days), p.Days)
			}
			for h := 0; h < 24; h++ {
				if got.Hours[h] != c.hours[h] {
					t.Errorf("hour %d = %d, want %d", h, got.Hours[h], c.hours[h])
				}
			}
			for w := 0; w < 7; w++ {
				if c.weekdays != nil && got.Weekdays[w] != c.weekdays[w] {
					t.Errorf("weekday %d = %d, want %d", w, got.Weekdays[w], c.weekdays[w])
				}
			}
			if c.dayMs != nil {
				for i, ms := range got.Days {
					if ms != c.dayMs[i] {
						t.Errorf("day %d = %d, want %d", i, ms, c.dayMs[i])
					}
				}
			}
			if got.Ever != (len(c.sessions) > 0) {
				t.Errorf("Ever = %v with %d own sessions", got.Ever, len(c.sessions))
			}
		})
	}
}

func TestListeningEverAndFinished(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no zoneinfo")
	}
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, ny)
	svc, st, u, books, _ := progEnv(t)
	p, _ := progress.PeriodOf(progress.RangeMonth, now, ny)
	got, err := svc.Listening(ctx, u[0], ny, p, now)
	if err != nil || got.Ever || got.TotalMs != 0 || got.SpeedPct != 0 || got.Finished != 0 {
		t.Fatalf("empty: %+v err=%v", got, err)
	}
	statsSession(t, st, u[1], books[0].ID, now, 60_000) // another person's session does not make this person "ever"
	if got, _ = svc.Listening(ctx, u[0], ny, p, now); got.Ever {
		t.Fatal("Ever true from another person's session")
	}
	statsFinished(t, st, u[0], books[0].ID, time.Date(2026, 10, 31, 23, 30, 0, 0, ny)) // October
	statsFinished(t, st, u[0], books[1].ID, time.Date(2026, 11, 1, 0, 0, 0, 0, ny))    // first instant of November
	statsFinished(t, st, u[0], books[2].ID, time.Date(2026, 11, 30, 23, 59, 0, 0, ny)) // last minute of November
	statsFinished(t, st, u[1], books[0].ID, time.Date(2026, 11, 2, 12, 0, 0, 0, ny))   // someone else's
	statsSession(t, st, u[0], books[0].ID, now.Add(-time.Hour), 60_000)
	got, err = svc.Listening(ctx, u[0], ny, p, now)
	if err != nil || got.Finished != 2 || !got.Ever {
		t.Fatalf("finished=%d ever=%v err=%v", got.Finished, got.Ever, err)
	}
	dec, _ := progress.PeriodOf(progress.RangeMonth, time.Date(2026, 12, 1, 0, 0, 0, 0, ny), ny)
	if got, _ = svc.Listening(ctx, u[0], ny, dec, now); got.Finished != 0 {
		t.Fatalf("December finished %d", got.Finished)
	}
}

func TestYearTotals(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skip("no zoneinfo")
	}
	now := time.Date(2026, 11, 4, 10, 0, 0, 0, ny)
	const min = int64(60_000)
	t.Run("empty", func(t *testing.T) {
		svc, st, u, books, _ := progEnv(t)
		statsSession(t, st, u[1], books[0].ID, now, min)
		got, err := svc.YearTotals(ctx, u[0], ny, now)
		if err != nil || got != nil {
			t.Fatalf("got %v err=%v", got, err)
		}
	})
	t.Run("three years, finished per year, year edge in local time", func(t *testing.T) {
		svc, st, u, books, _ := progEnv(t)
		statsSession(t, st, u[0], books[0].ID, time.Date(2024, 3, 5, 12, 0, 0, 0, ny), 10*min)
		statsSession(t, st, u[0], books[0].ID, time.Date(2025, 12, 31, 23, 50, 0, 0, ny), 20*min) // 10 + 10 across New Year
		statsSession(t, st, u[0], books[0].ID, time.Date(2026, 6, 1, 12, 0, 0, 0, ny), 5*min)
		statsSession(t, st, u[1], books[0].ID, time.Date(2026, 6, 1, 12, 0, 0, 0, ny), 500*min)
		statsFinished(t, st, u[0], books[0].ID, time.Date(2024, 4, 1, 12, 0, 0, 0, ny))
		statsFinished(t, st, u[0], books[1].ID, time.Date(2026, 1, 1, 0, 5, 0, 0, ny))
		statsFinished(t, st, u[1], books[1].ID, time.Date(2026, 1, 2, 0, 5, 0, 0, ny))
		got, err := svc.YearTotals(ctx, u[0], ny, now)
		if err != nil {
			t.Fatal(err)
		}
		want := []progress.YearTotal{{2024, 10 * min, 1}, {2025, 10 * min, 0}, {2026, 15 * min, 1}}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
	})
	t.Run("12 years are capped to 10", func(t *testing.T) {
		svc, st, u, books, _ := progEnv(t)
		for y := 2015; y <= 2026; y++ {
			statsSession(t, st, u[0], books[0].ID, time.Date(y, 6, 1, 12, 0, 0, 0, ny), min)
		}
		got, err := svc.YearTotals(ctx, u[0], ny, now)
		if err != nil || len(got) != 10 || got[0].Year != 2017 || got[9].Year != 2026 || got[0].Ms != min {
			t.Fatalf("got %v err=%v", got, err)
		}
	})
}
