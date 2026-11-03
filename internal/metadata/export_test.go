package metadata

import (
	"context"
	"time"
)

// SetBulkClock replaces the service's clock, the bulk job's sleep and its jitter, so tests run a
// whole bulk lookup without waiting.
func (s *Service) SetBulkClock(now func() time.Time, sleep func(context.Context, time.Duration) error, jitter func() float64) {
	s.now, s.sleep, s.jitter = now, sleep, jitter
}
