package progress

import (
	"context"
	"database/sql"
	"time"
)

// StatsDayMinMs is the least listening that makes a day count towards a streak and a mark.
const StatsDayMinMs = 60_000

const (
	statsWindowDays = 64   // days read in one query; the streak pages back by this much
	statsMaxStreak  = 3660 // hard cap on how far the streak walk goes
)

// Day mark states.
const (
	DayListened = "listened" // at least StatsDayMinMs on that day
	DayToday    = "today"    // today, nothing yet
	DayMissed   = "missed"   // an earlier day without listening
	DayAhead    = "ahead"    // a day still to come
)

// DayMark is one day of the current week, Sunday first.
type DayMark struct {
	Weekday time.Weekday
	State   string
}

// Stats are one person's listening numbers. They count only that person's own sessions and name no book.
type Stats struct {
	WeekMs    int64 // listened since Sunday 00:00 local
	Streak    int   // consecutive local days with listening, ending today or yesterday
	Finished  int   // books finished in the local calendar year
	Year      int
	Days      [7]DayMark
	TodayDone bool
}

// Stats computes the person's numbers at now, cutting days at midnight in loc (nil means time.Local).
// A session is cut at local midnight (splitSession), so one that crosses it counts on both days. Every read is by user_id and a started_at or
// finished_at range.
func (s *Service) Stats(ctx context.Context, userID string, loc *time.Location, now time.Time) (Stats, error) {
	if loc == nil {
		loc = time.Local
	}
	y, m, d := now.In(loc).Date()
	today := time.Date(y, m, d, 0, 0, 0, 0, time.UTC) // a civil date, keyed in UTC so DST never moves it
	civil := func(off int) time.Time { return today.AddDate(0, 0, off) }
	startOf := func(c time.Time) time.Time { return time.Date(c.Year(), c.Month(), c.Day(), 0, 0, 0, 0, loc) }
	weekday := int(today.Weekday())

	sums := map[int64]int64{}
	loadedFrom := -statsWindowDays // civil offset of the oldest day read so far
	load := func(fromOff, toOff int) error {
		return s.eachPiece(ctx, userID, loc, startOf(civil(fromOff)), startOf(civil(toOff)), func(at time.Time, ms int64, _ int) {
			yy, mm, dd := at.Date()
			sums[time.Date(yy, mm, dd, 0, 0, 0, 0, time.UTC).Unix()] += ms
		})
	}
	if err := load(loadedFrom, 1); err != nil {
		return Stats{}, err
	}

	st := Stats{Year: y}
	for i := 0; i < 7; i++ {
		off := i - weekday
		c := civil(off)
		ms := sums[c.Unix()]
		st.WeekMs += ms
		mark := DayMark{Weekday: time.Weekday(i)}
		switch {
		case off > 0:
			mark.State = DayAhead
		case ms >= StatsDayMinMs:
			mark.State = DayListened
		case off == 0:
			mark.State = DayToday
		default:
			mark.State = DayMissed
		}
		st.Days[i] = mark
	}
	st.TodayDone = sums[today.Unix()] >= StatsDayMinMs

	// Streak: from today, or yesterday while today is still empty, back to the first empty day.
	var oldest int64 = -1 // MIN(started_at), read once when the walk reaches the window start
	start := 0
	if !st.TodayDone {
		start = -1
	}
	for off := start; off > -statsMaxStreak-1 && st.Streak < statsMaxStreak; off-- {
		if off < loadedFrom {
			if oldest < 0 {
				var min sql.NullInt64
				if err := s.st.Reader().QueryRowContext(ctx,
					`SELECT MIN(started_at) FROM listening_sessions WHERE user_id = ?`, userID).Scan(&min); err != nil {
					return Stats{}, err
				}
				if !min.Valid {
					break
				}
				oldest = min.Int64
			}
			if oldest >= startOf(civil(loadedFrom)).UnixMilli() {
				break // nothing older exists
			}
			if err := load(loadedFrom-statsWindowDays, loadedFrom); err != nil {
				return Stats{}, err
			}
			loadedFrom -= statsWindowDays
		}
		if sums[civil(off).Unix()] < StatsDayMinMs {
			break
		}
		st.Streak++
	}

	// Finished books in the local calendar year.
	from := time.Date(y, time.January, 1, 0, 0, 0, 0, loc).UnixMilli()
	to := time.Date(y+1, time.January, 1, 0, 0, 0, 0, loc).UnixMilli()
	if err := s.st.Reader().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM progress WHERE user_id = ? AND finished_at >= ? AND finished_at < ?`,
		userID, from, to).Scan(&st.Finished); err != nil {
		return Stats{}, err
	}
	return st, nil
}
