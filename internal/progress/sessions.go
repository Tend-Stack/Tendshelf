package progress

import (
	"context"
	"database/sql"
	"errors"
	"math"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

// Session rules. A session is one continuous stretch of listening to one book on one device.
const (
	// SessionGapMs is the longest pause between two saves that still belongs to the same session.
	SessionGapMs = 60_000
	// SessionJumpMs is the largest position change, beyond the audio played, that is not a seek
	// (covers skip back or forward 30 s and the 5-s auto-rewind).
	SessionJumpMs = 35_000
	// SessionMaxListenMs caps the playing time one save may report.
	SessionMaxListenMs = 600_000
	// SessionMinNewMs is the least playing time that opens a new session.
	SessionMinNewMs = 1_000
)

// recordSession extends or opens the listening session a save reports. t is the clamped client time and
// u.PositionMs the position as stored. It runs in the save's transaction, so a failed save leaves no session.
func recordSession(ctx context.Context, tx *sql.Tx, u Update, t int64) error {
	if u.DeviceID == ServerDevice || u.ListenedMs == 0 || u.Speed == 0 {
		return nil
	}
	l := min(u.ListenedMs, SessionMaxListenMs)
	v := u.Speed
	pct := roundPct(v)
	x := u.PositionMs

	var id string
	var endedAt, endMs, speedPct int64
	found := true
	err := tx.QueryRowContext(ctx, `SELECT id, ended_at, end_ms, speed_pct FROM listening_sessions
WHERE user_id = ? AND book_id = ? AND device_id = ? ORDER BY ended_at DESC LIMIT 1`,
		u.UserID, u.BookID, u.DeviceID).Scan(&id, &endedAt, &endMs, &speedPct)
	if errors.Is(err, sql.ErrNoRows) {
		found = false
	} else if err != nil {
		return err
	}
	if found {
		if t <= endedAt {
			return nil // a retry or an out-of-order save is never counted twice
		}
		idle := (t - endedAt) - l
		if idle <= SessionGapMs && speedPct == pct && math.Abs(float64(x)-(float64(endMs)+float64(l)*v)) <= SessionJumpMs {
			_, err := tx.ExecContext(ctx, `UPDATE listening_sessions SET ended_at = ?, end_ms = ?, listened_ms = listened_ms + ? WHERE id = ?`,
				t, x, min(l, t-endedAt), id)
			return err
		}
	}
	if l < SessionMinNewMs {
		return nil
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO listening_sessions(id, user_id, book_id, device_id, started_at, ended_at, start_ms, end_ms, listened_ms, speed_pct)
VALUES (?,?,?,?,?,?,?,?,?,?)`,
		db.NewID("ls"), u.UserID, u.BookID, u.DeviceID, t-l, t, max(0, x-int64(math.Round(float64(l)*v))), x, l, pct)
	return err
}
