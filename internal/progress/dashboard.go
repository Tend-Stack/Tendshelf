package progress

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Ranges of the listening dashboard.
const (
	RangeWeek  = "week"
	RangeMonth = "month"
	RangeYear  = "year"
)

// ErrRange is returned for a range that is not week, month or year.
var ErrRange = errors.New("progress: unknown range")

const yearTotalsMax = 10 // YearTotals looks back at most this many years, the current one included

// Period is a week (Sunday first), a calendar month or a calendar year.
type Period struct {
	Range    string
	First    time.Time // the first civil date, as UTC midnight so DST never moves it
	Days     int
	From, To time.Time // local midnights: [From, To) is the period in the person's zone
}

// PeriodOf is the period of the given range that contains day. Only day's year, month and day are used,
// as written, so the caller passes the date in the person's zone.
func PeriodOf(rng string, day time.Time, loc *time.Location) (Period, error) {
	y, m, d := day.Date()
	civil := time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	var first, end time.Time
	switch rng {
	case RangeWeek:
		first = civil.AddDate(0, 0, -int(civil.Weekday()))
		end = first.AddDate(0, 0, 7)
	case RangeMonth:
		first = time.Date(y, m, 1, 0, 0, 0, 0, time.UTC)
		end = first.AddDate(0, 1, 0)
	case RangeYear:
		first = time.Date(y, time.January, 1, 0, 0, 0, 0, time.UTC)
		end = first.AddDate(1, 0, 0)
	default:
		return Period{}, ErrRange
	}
	local := func(c time.Time) time.Time { return time.Date(c.Year(), c.Month(), c.Day(), 0, 0, 0, 0, loc) }
	return Period{Range: rng, First: first, Days: int(end.Sub(first).Hours() / 24), From: local(first), To: local(end)}, nil
}

// Shift is the period n periods later (earlier when n is negative).
func (p Period) Shift(n int, loc *time.Location) Period {
	var first time.Time
	switch p.Range {
	case RangeWeek:
		first = p.First.AddDate(0, 0, 7*n)
	case RangeMonth:
		first = p.First.AddDate(0, n, 0)
	default:
		first = p.First.AddDate(n, 0, 0)
	}
	out, err := PeriodOf(p.Range, first, loc)
	if err != nil {
		return p
	}
	return out
}

// Listening is one person's listening in one period. It counts all their own sessions and names no book.
type Listening struct {
	Period       Period
	Days         []int64   // ms per day of the period, in order
	Hours        [24]int64 // ms per local hour of the day
	Weekdays     [7]int64  // ms per weekday, Sunday first
	TotalMs      int64
	DaysListened int  // days with at least StatsDayMinMs
	DaysSoFar    int  // days of the period up to and including today (all if past, 0 if future)
	SpeedPct     int  // listening-time-weighted speed, rounded to 5; 0 without listening
	Finished     int  // books finished in the period
	Ever         bool // the person has any session at all
}

// Listening computes the person's numbers for p at now.
func (s *Service) Listening(ctx context.Context, userID string, loc *time.Location, p Period, now time.Time) (Listening, error) {
	if loc == nil {
		loc = time.Local
	}
	l := Listening{Period: p, Days: make([]int64, p.Days)}
	var weighted int64
	err := s.eachPiece(ctx, userID, loc, p.From, p.To, func(at time.Time, ms int64, pct int) {
		y, m, d := at.Date()
		day := int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Sub(p.First).Hours() / 24)
		if day < 0 || day >= p.Days {
			return
		}
		l.Days[day] += ms
		l.Hours[at.Hour()] += ms
		l.Weekdays[at.Weekday()] += ms
		l.TotalMs += ms
		weighted += ms * int64(pct)
	})
	if err != nil {
		return Listening{}, err
	}
	for _, ms := range l.Days {
		if ms >= StatsDayMinMs {
			l.DaysListened++
		}
	}
	l.DaysSoFar = p.daysSoFar(now, loc)
	if l.TotalMs > 0 {
		l.SpeedPct = int((weighted/l.TotalMs+2)/5) * 5
	}
	r := s.st.Reader()
	if err := r.QueryRowContext(ctx, `SELECT COUNT(*) FROM progress WHERE user_id = ? AND finished_at >= ? AND finished_at < ?`,
		userID, p.From.UnixMilli(), p.To.UnixMilli()).Scan(&l.Finished); err != nil {
		return Listening{}, err
	}
	if err := r.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM listening_sessions WHERE user_id = ?)`, userID).Scan(&l.Ever); err != nil {
		return Listening{}, err
	}
	return l, nil
}

// daysSoFar is how many days of the period are up to and including today in loc: all if it is past, 0 if future.
func (p Period) daysSoFar(now time.Time, loc *time.Location) int {
	y, m, d := now.In(loc).Date()
	switch off := int(time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Sub(p.First).Hours()/24) + 1; {
	case off <= 0:
		return 0
	case off >= p.Days:
		return p.Days
	default:
		return off
	}
}

// YearTotal is one calendar year of listening.
type YearTotal struct {
	Year     int
	Ms       int64
	Finished int
}

// YearTotals lists the person's years from their first session (at most the last 10) to now's year,
// oldest first. It is nil when the person has no session.
func (s *Service) YearTotals(ctx context.Context, userID string, loc *time.Location, now time.Time) ([]YearTotal, error) {
	if loc == nil {
		loc = time.Local
	}
	var first sql.NullInt64
	if err := s.st.Reader().QueryRowContext(ctx, `SELECT MIN(started_at) FROM listening_sessions WHERE user_id = ?`, userID).Scan(&first); err != nil {
		return nil, err
	}
	if !first.Valid {
		return nil, nil
	}
	last := now.In(loc).Year()
	start := max(time.UnixMilli(first.Int64).In(loc).Year(), last-yearTotalsMax+1)
	if start > last {
		return nil, nil
	}
	out := make([]YearTotal, last-start+1)
	for i := range out {
		out[i].Year = start + i
	}
	from := time.Date(start, time.January, 1, 0, 0, 0, 0, loc)
	to := time.Date(last+1, time.January, 1, 0, 0, 0, 0, loc)
	err := s.eachPiece(ctx, userID, loc, from, to, func(at time.Time, ms int64, _ int) {
		if i := at.Year() - start; i >= 0 && i < len(out) {
			out[i].Ms += ms
		}
	})
	if err != nil {
		return nil, err
	}
	rows, err := s.st.Reader().QueryContext(ctx, `SELECT finished_at FROM progress WHERE user_id = ? AND finished_at >= ?`, userID, from.UnixMilli())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var at int64
		if err := rows.Scan(&at); err != nil {
			return nil, err
		}
		if i := time.UnixMilli(at).In(loc).Year() - start; i >= 0 && i < len(out) {
			out[i].Finished++
		}
	}
	return out, rows.Err()
}
