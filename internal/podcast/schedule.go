package podcast

import (
	"context"
	"time"
)

// Scheduler timings: the first look a minute after start, then every five minutes.
const (
	scheduleFirst = time.Minute
	scheduleEvery = 5 * time.Minute
	dueBatch      = 50
	pruneEvery    = 24 * time.Hour
)

// Run ticks until ctx ends. A tick does nothing while podcasts are off, and refreshes followed shows only
// when the owner picked an interval (Settings.Refresh is not "off").
func (s *Service) Run(ctx context.Context) {
	wait := scheduleFirst
	var lastPrune time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = scheduleEvery
		lastPrune = s.Tick(ctx, lastPrune)
	}
}

// Tick is one scheduler pass; it returns the time of the last prune, updated when one was queued.
func (s *Service) Tick(ctx context.Context, lastPrune time.Time) time.Time {
	set := s.Settings(ctx)
	if !set.Enabled {
		return lastPrune
	}
	if set.Interval() > 0 {
		ids, err := s.DueShows(ctx, dueBatch)
		if err != nil {
			s.Log.Warn("podcasts: due shows", "err", err)
		}
		for _, id := range ids {
			if _, err := s.QueueRefresh(ctx, id, ""); err != nil {
				s.Log.Warn("podcasts: could not queue a refresh", "err", err)
				break
			}
		}
	}
	// Server auto-download of new episodes (podcasts.server_auto) arrives with P5.
	if s.now().Sub(lastPrune) >= pruneEvery {
		if _, err := s.enqueueOnce(ctx, KindPrune, "kind", "prune", map[string]string{"kind": "prune"}, ""); err != nil {
			s.Log.Warn("podcasts: could not queue a prune", "err", err)
		} else {
			lastPrune = s.now()
		}
	}
	return lastPrune
}
