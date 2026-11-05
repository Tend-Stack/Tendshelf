package db

import (
	"context"
	"time"
)

const (
	// JobsKeep is how long a finished job row stays: long enough to read why something failed.
	JobsKeep = 30 * 24 * time.Hour
	// AuditKeep is how long the audit trail stays.
	AuditKeep = 365 * 24 * time.Hour
)

// Retention reports what one Sweep removed.
type Retention struct {
	Jobs, Audit int64
}

// Sweep deletes finished job rows older than JobsKeep (plus any leftover align.* row that is not
// running: that feature is gone) and audit entries older than AuditKeep.
func Sweep(ctx context.Context, st *Store, now time.Time) (Retention, error) {
	var out Retention
	res, err := st.Writer().ExecContext(ctx, `DELETE FROM jobs WHERE updated_at < ?
		AND (state IN ('done','failed','cancelled') OR (kind LIKE 'align.%' AND state <> 'running'))`,
		now.Add(-JobsKeep).UnixMilli())
	if err != nil {
		return out, err
	}
	out.Jobs, _ = res.RowsAffected()
	res, err = st.Writer().ExecContext(ctx, `DELETE FROM audit_log WHERE at < ?`, now.Add(-AuditKeep).UnixMilli())
	if err != nil {
		return out, err
	}
	out.Audit, _ = res.RowsAffected()
	return out, nil
}
