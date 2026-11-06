package progress

import (
	"context"
	"database/sql"
	"errors"
)

// KOReader progress (S6). One row per person and document keeps the last place a KOReader sent, in its
// own words, so KOReader gets its exact pointer back. The row is separate from reading_positions, which
// holds only places Tendshelf can use.

// KosyncMaxRows is how many documents one person keeps; the least recently updated are dropped.
const KosyncMaxRows = 10_000

// KosyncRow is one KOReader place. Percentage is 0..1 and UpdatedAt is Unix milliseconds.
type KosyncRow struct {
	Document   string
	Progress   string
	Percentage float64
	Device     string
	DeviceID   string
	UpdatedAt  int64
}

// KosyncPut stores a place for the person, replacing an older row for the same document, then drops the
// least recently updated rows over the cap. A put older than the stored row still replaces it: KOReader
// sends its place when the person leaves a page, and the person knows best which device they used last.
func (s *Service) KosyncPut(ctx context.Context, userID string, r KosyncRow) error {
	return s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO kosync_progress(user_id, document, progress, percentage, device, device_id, updated_at)
VALUES (?,?,?,?,?,?,?) ON CONFLICT(user_id, document) DO UPDATE SET progress = excluded.progress, percentage = excluded.percentage,
  device = excluded.device, device_id = excluded.device_id, updated_at = excluded.updated_at`,
			userID, r.Document, r.Progress, r.Percentage, r.Device, r.DeviceID, r.UpdatedAt); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM kosync_progress WHERE user_id = ? AND document IN (
  SELECT document FROM kosync_progress WHERE user_id = ? ORDER BY updated_at DESC, document LIMIT -1 OFFSET ?)`,
			userID, userID, KosyncMaxRows)
		return err
	})
}

// KosyncGet returns the person's stored place for a document; ok is false when there is none. Only the
// person's own row is ever read.
func (s *Service) KosyncGet(ctx context.Context, userID, document string) (KosyncRow, bool, error) {
	var r KosyncRow
	err := s.st.Reader().QueryRowContext(ctx, `SELECT document, progress, percentage, device, device_id, updated_at
FROM kosync_progress WHERE user_id = ? AND document = ?`, userID, document).
		Scan(&r.Document, &r.Progress, &r.Percentage, &r.Device, &r.DeviceID, &r.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return KosyncRow{}, false, nil
	}
	return r, err == nil, err
}
