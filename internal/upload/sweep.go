package upload

import (
	"context"
	"database/sql"
	"os"
	"time"
)

const (
	staleAfter  = 24 * time.Hour
	pruneAfter  = 30 * 24 * time.Hour
	sweepEvery  = time.Hour
	firstSweep  = time.Minute
	endedStates = `('placed','refused','cancelled','expired','failed')`
)

// Run sweeps once a minute after start and then hourly, until ctx ends.
func (s *Service) Run(ctx context.Context) {
	timer := time.NewTimer(firstSweep)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("upload: sweep failed", "err", err)
			}
			timer.Reset(sweepEvery)
		}
	}
}

// Sweep expires receiving uploads with no chunk for 24 hours, removes staging folders that have no
// upload row (older than 24 hours) or whose upload has ended, and deletes rows of ended uploads
// older than 30 days. The ledger (upload_paths) is never pruned.
func (s *Service) Sweep(ctx context.Context) error {
	now := s.cfg.Now()
	cut := now.Add(-staleAfter).UnixMilli()
	rows, err := s.st.Reader().QueryContext(ctx, `SELECT id FROM uploads WHERE state = 'receiving' AND updated_at < ?`, cut)
	if err != nil {
		return err
	}
	var stale []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		stale = append(stale, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range stale {
		l := s.lock(id)
		l.Lock()
		err := s.setState(ctx, nil, id, StateExpired, "", StateReceiving)
		l.Unlock()
		if err == nil {
			s.removeStaging(id)
			s.forget(id)
		}
	}

	// Staging folders.
	entries, err := os.ReadDir(s.incomingDir())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, e := range entries {
		id := e.Name()
		if !e.IsDir() || !idRe.MatchString(id) {
			continue
		}
		var state string
		qerr := s.st.Reader().QueryRowContext(ctx, `SELECT state FROM uploads WHERE id = ?`, id).Scan(&state)
		switch {
		case qerr == nil:
			switch state {
			case StatePlaced, StateRefused, StateCancelled, StateExpired:
				s.removeStaging(id)
			}
		default:
			if info, ierr := e.Info(); ierr == nil && now.Sub(info.ModTime()) > staleAfter {
				s.removeStaging(id)
			}
		}
	}

	// Old rows, with the staging of the failed ones.
	old := now.Add(-pruneAfter).UnixMilli()
	rows, err = s.st.Reader().QueryContext(ctx, `SELECT id FROM uploads WHERE state IN `+endedStates+` AND updated_at < ?`, old)
	if err != nil {
		return err
	}
	var gone []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		gone = append(gone, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, id := range gone {
		s.removeStaging(id)
		err := s.write(ctx, nil, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM uploads WHERE id = ? AND state IN `+endedStates, id)
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}
