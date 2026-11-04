package upload

import (
	"context"
	"time"
)

// RecentWindow is how long the Add books page lists a person's finished uploads.
const RecentWindow = 24 * time.Hour

// List returns the person's uploads of the last RecentWindow, newest first: the queue of the Add
// books page. A cancelled or expired upload is not listed. Each carries its files with the server's
// offsets, so a reload can offer "Choose files again".
func (s *Service) List(ctx context.Context, userID string) ([]View, error) {
	since := s.cfg.Now().Add(-RecentWindow).UnixMilli()
	rows, err := s.st.Reader().QueryContext(ctx, `SELECT `+uploadCols+` FROM uploads
  WHERE created_by = ? AND updated_at >= ? AND state NOT IN (?, ?) ORDER BY created_at DESC, id DESC`,
		userID, since, StateCancelled, StateExpired)
	if err != nil {
		return nil, err
	}
	var ups []uploadRow
	for rows.Next() {
		u, err := scanUpload(rows)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		ups = append(ups, u)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()
	out := make([]View, 0, len(ups))
	for _, u := range ups {
		files, err := s.loadFiles(ctx, u.ID)
		if err != nil {
			return nil, err
		}
		out = append(out, s.view(u, files))
	}
	return out, nil
}
