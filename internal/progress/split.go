package progress

import (
	"context"
	"time"
)

// SplitLookbackMs is how far back a session can reach: a session counts at most its last 24 hours, so
// one with a bad clock cannot spread its listening over years. Readers look back this far for a
// session that began before a period but ended inside it.
const SplitLookbackMs = 86_400_000

// splitSession spreads listenedMs evenly over the wall-clock span startMs..endMs and calls fn once per
// local hour with the start of that piece (in loc) and its share. Cuts use time.Date with hour+1, so a
// repeated DST hour is one piece in that hour and a missing hour gets nothing; half-hour zones cut at
// local :00. Shares are whole milliseconds and the last piece takes the remainder, so they sum to
// listenedMs. The library's top-listened SQL (internal/library/listening_top.go) clips with the same rule.
func splitSession(startMs, endMs, listenedMs int64, loc *time.Location, fn func(at time.Time, ms int64)) {
	if listenedMs <= 0 {
		return
	}
	if endMs-startMs > SplitLookbackMs {
		startMs = endMs - SplitLookbackMs
	}
	if endMs <= startMs {
		fn(time.UnixMilli(startMs).In(loc), listenedMs)
		return
	}
	span := endMs - startMs
	var sum int64
	for c := startMs; c < endMs; {
		lt := time.UnixMilli(c).In(loc)
		next := time.Date(lt.Year(), lt.Month(), lt.Day(), lt.Hour()+1, 0, 0, 0, loc)
		if !next.After(lt) {
			// Go resolved a missing local hour backwards (spring forward): cut at the hour after it.
			next = time.Date(lt.Year(), lt.Month(), lt.Day(), lt.Hour()+2, 0, 0, 0, loc)
		}
		if !next.After(lt) {
			next = lt.Add(time.Hour)
		}
		e := min(next.UnixMilli(), endMs)
		share := listenedMs * (e - c) / span
		if e == endMs {
			share = listenedMs - sum
		}
		sum += share
		fn(lt, share)
		c = e
	}
}

// eachPiece reads the person's sessions that can reach [from, to) and calls fn for every hourly piece
// that starts inside it. Bounded by user_id and a started_at range on listening_sessions_user.
func (s *Service) eachPiece(ctx context.Context, userID string, loc *time.Location, from, to time.Time,
	fn func(at time.Time, ms int64, speedPct int)) error {
	rows, err := s.st.Reader().QueryContext(ctx, eachPieceSQL, userID,
		from.UnixMilli()-SplitLookbackMs, to.UnixMilli(), from.UnixMilli(), from.UnixMilli())
	if err != nil {
		return err
	}
	defer rows.Close()
	f, t := from.UnixMilli(), to.UnixMilli()
	for rows.Next() {
		var started, ended, ms int64
		var pct int
		if err := rows.Scan(&started, &ended, &ms, &pct); err != nil {
			return err
		}
		splitSession(started, ended, ms, loc, func(at time.Time, share int64) {
			if u := at.UnixMilli(); u >= f && u < t {
				fn(at, share, pct)
			}
		})
	}
	return rows.Err()
}

const eachPieceSQL = `SELECT started_at, ended_at, listened_ms, speed_pct FROM listening_sessions
WHERE user_id = ? AND started_at >= ? AND started_at < ? AND (ended_at > ? OR started_at >= ?)`
