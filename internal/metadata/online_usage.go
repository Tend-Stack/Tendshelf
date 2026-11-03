package metadata

import (
	"context"
	"database/sql"
	"slices"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

const dayMillis = int64(24 * time.Hour / time.Millisecond)

// usageDay is the UTC day number of a time: Unix milliseconds divided by a day.
func usageDay(t time.Time) int64 { return t.UnixMilli() / dayMillis }

// usagePrune is how many days of request counts are kept.
const usagePruneDays = 7

// AddOnlineUsage adds the requests really sent (by provider, as online.Result.SentBy reports them)
// to the day's count. A provider the table does not know is ignored. It lives in the database so
// a restart cannot reset the daily cap.
func AddOnlineUsage(ctx context.Context, st *db.Store, day int64, sent map[string]int) error {
	var providers []string
	for p, n := range sent {
		if n > 0 && slices.Contains(online.Providers, p) {
			providers = append(providers, p)
		}
	}
	if len(providers) == 0 {
		return nil
	}
	return st.WriteTx(ctx, func(tx *sql.Tx) error {
		for _, p := range providers {
			if _, err := tx.ExecContext(ctx, `INSERT INTO online_usage(day, provider, requests) VALUES (?,?,?)
  ON CONFLICT(day, provider) DO UPDATE SET requests = requests + excluded.requests`, day, p, sent[p]); err != nil {
				return err
			}
		}
		return nil
	})
}

// OnlineUsage returns how many requests were sent to the provider on the UTC day.
func OnlineUsage(ctx context.Context, q db.RowQueryer, day int64, provider string) (int, error) {
	var n int
	err := q.QueryRowContext(ctx, `SELECT requests FROM online_usage WHERE day = ? AND provider = ?`, day, provider).Scan(&n)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return n, err
}

// PruneOnlineUsage deletes the counts of days before beforeDay.
func PruneOnlineUsage(ctx context.Context, st *db.Store, beforeDay int64) error {
	_, err := st.Writer().ExecContext(ctx, `DELETE FROM online_usage WHERE day < ?`, beforeDay)
	return err
}
