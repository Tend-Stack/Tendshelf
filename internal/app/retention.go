package app

import (
	"context"
	"log/slog"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

const (
	retentionFirst = 5 * time.Minute
	retentionEvery = 6 * time.Hour
)

// runRetention keeps the database and the cover cache from growing without end: finished job rows
// older than 30 days, audit entries older than a year, and online covers past seven days or over
// the size cap. The first pass is five minutes after start, then every six hours, until ctx ends.
func runRetention(ctx context.Context, st *db.Store, covers *online.Client, log *slog.Logger) {
	timer := time.NewTimer(retentionFirst)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			sweepRetention(ctx, st, covers, log, time.Now())
			timer.Reset(retentionEvery)
		}
	}
}

// sweepRetention is one pass of runRetention. A failure is logged and never stops the others.
func sweepRetention(ctx context.Context, st *db.Store, covers *online.Client, log *slog.Logger, now time.Time) {
	got, err := db.Sweep(ctx, st, now)
	if err != nil && ctx.Err() == nil {
		log.Warn("retention: sweep failed", "err", err)
	} else if got.Jobs > 0 || got.Audit > 0 {
		log.Info("retention: removed old rows", "jobs", got.Jobs, "audit", got.Audit)
	}
	if covers == nil {
		return
	}
	if n, err := covers.PruneCovers(online.CoverCacheLimit); err != nil {
		log.Warn("retention: cover cache prune failed", "err", err)
	} else if n > 0 {
		log.Info("retention: removed cached covers", "files", n)
	}
}
