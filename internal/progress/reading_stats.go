package progress

import (
	"context"
	"time"
)

const (
	readingStreakDays  = 400 // how far back the reading streak walks
	readingFinishLimit = 12  // books ReadingFinishes returns at most
	readingCpmFloorMs  = 60_000
)

// Reading is one person's reading in one period (G2). It counts only their own reading_sessions and names no book.
type Reading struct {
	Period    Period
	Days      []int64 // active ms per local day of the period, in order, by the day a session ended
	TotalMs   int64
	Chars     int64
	DaysRead  int // days with at least StatsDayMinMs
	DaysSoFar int // days of the period up to and including today
	Cpm       int // characters per minute over the period; 0 under a minute of reading
	Finished  int // books finished in the period (reading_positions.finished_at)
	Streak    int // consecutive local days read, ending today or yesterday
	Ever      bool
}

// civilDay is the local date of ms in loc as UTC midnight, the way Period.First is written.
func civilDay(ms int64, loc *time.Location) time.Time {
	y, m, d := time.UnixMilli(ms).In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// ReadingStats computes the person's reading for p at now. Every read is bound by user_id.
func (s *Service) ReadingStats(ctx context.Context, userID string, loc *time.Location, p Period, now time.Time) (Reading, error) {
	if loc == nil {
		loc = time.Local
	}
	r := Reading{Period: p, Days: make([]int64, p.Days), DaysSoFar: p.daysSoFar(now, loc)}
	rows, err := s.st.Reader().QueryContext(ctx,
		`SELECT ended_at, active_ms, chars_read FROM reading_sessions WHERE user_id = ? AND ended_at >= ? AND ended_at < ?`,
		userID, p.From.UnixMilli(), p.To.UnixMilli())
	if err != nil {
		return Reading{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var ended, active, chars int64
		if err := rows.Scan(&ended, &active, &chars); err != nil {
			return Reading{}, err
		}
		day := int(civilDay(ended, loc).Sub(p.First).Hours() / 24)
		if day < 0 || day >= p.Days {
			continue
		}
		r.Days[day] += active
		r.TotalMs += active
		r.Chars += chars
	}
	if err := rows.Err(); err != nil {
		return Reading{}, err
	}
	for _, ms := range r.Days {
		if ms >= StatsDayMinMs {
			r.DaysRead++
		}
	}
	if r.TotalMs >= readingCpmFloorMs {
		r.Cpm = int(r.Chars * 60_000 / r.TotalMs)
	}
	if r.Finished, err = s.CountReadingFinished(ctx, userID, p.From.UnixMilli(), p.To.UnixMilli()); err != nil {
		return Reading{}, err
	}
	if err := s.st.Reader().QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM reading_sessions WHERE user_id = ?)`, userID).Scan(&r.Ever); err != nil {
		return Reading{}, err
	}
	if r.Ever {
		if r.Streak, err = s.readingStreak(ctx, userID, loc, now); err != nil {
			return Reading{}, err
		}
	}
	return r, nil
}

// HasReading reports whether the person has any reading session at all.
func (s *Service) HasReading(ctx context.Context, userID string) (ever bool, err error) {
	err = s.st.Reader().QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM reading_sessions WHERE user_id = ?)`, userID).Scan(&ever)
	return ever, err
}

// CountReadingFinished is how many books the person finished in [from, to) (Unix ms).
func (s *Service) CountReadingFinished(ctx context.Context, userID string, from, to int64) (n int, err error) {
	err = s.st.Reader().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM reading_positions WHERE user_id = ? AND finished_at >= ? AND finished_at < ?`, userID, from, to).Scan(&n)
	return n, err
}

// readingStreak counts consecutive local days with at least StatsDayMinMs read, ending today, or yesterday
// while today is still empty (the rule of Stats), looking back at most readingStreakDays days.
func (s *Service) readingStreak(ctx context.Context, userID string, loc *time.Location, now time.Time) (int, error) {
	today := civilDay(now.UnixMilli(), loc)
	first := today.AddDate(0, 0, -readingStreakDays)
	from := time.Date(first.Year(), first.Month(), first.Day(), 0, 0, 0, 0, loc)
	rows, err := s.st.Reader().QueryContext(ctx,
		`SELECT ended_at, active_ms FROM reading_sessions WHERE user_id = ? AND ended_at >= ? AND ended_at <= ?`,
		userID, from.UnixMilli(), now.UnixMilli())
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	sums := map[int64]int64{}
	for rows.Next() {
		var ended, active int64
		if err := rows.Scan(&ended, &active); err != nil {
			return 0, err
		}
		sums[civilDay(ended, loc).Unix()] += active
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	day := today
	if sums[day.Unix()] < StatsDayMinMs {
		day = day.AddDate(0, 0, -1)
	}
	n := 0
	for n < readingStreakDays && sums[day.Unix()] >= StatsDayMinMs {
		n++
		day = day.AddDate(0, 0, -1)
	}
	return n, nil
}

// ReadingFinish is a book the person finished and when. The caller shows it only through a scoped read.
type ReadingFinish struct {
	BookID string
	At     int64
}

// ReadingFinishes lists the person's finished books in [from, to) (Unix ms), newest first, at most 12.
func (s *Service) ReadingFinishes(ctx context.Context, userID string, from, to int64) ([]ReadingFinish, error) {
	rows, err := s.st.Reader().QueryContext(ctx,
		`SELECT book_id, finished_at FROM reading_positions WHERE user_id = ? AND finished_at >= ? AND finished_at < ?
ORDER BY finished_at DESC, book_id LIMIT ?`, userID, from, to, readingFinishLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ReadingFinish
	for rows.Next() {
		var f ReadingFinish
		if err := rows.Scan(&f.BookID, &f.At); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
