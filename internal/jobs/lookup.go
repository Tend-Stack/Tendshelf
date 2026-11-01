package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const (
	// args: payload field, value
	sqlLookupCols = `SELECT id, kind, state, payload_json, result_json, progress_json, error_code, error_text,
attempts, max_attempts, lease_until, not_before, created_by, created_at, updated_at FROM jobs
WHERE kind=? AND json_extract(payload_json, '$.' || ?) = ?`

	sqlActiveFor = sqlLookupCols + ` AND state IN ('queued','running','held') ORDER BY created_at DESC, id DESC LIMIT 1`
	sqlLatestFor = sqlLookupCols + ` ORDER BY created_at DESC, id DESC LIMIT 1`
)

// ActiveFor returns the newest queued, running or held job of a kind whose payload field (a
// top-level key matching ^[a-z_]+$) equals value. The bool is false when there is none.
func (r *Runner) ActiveFor(ctx context.Context, kind, field, value string) (Job, bool, error) {
	return r.lookup(ctx, sqlActiveFor, kind, field, value)
}

// LatestFor is ActiveFor over jobs in any state: the newest job of the kind for that value.
func (r *Runner) LatestFor(ctx context.Context, kind, field, value string) (Job, bool, error) {
	return r.lookup(ctx, sqlLatestFor, kind, field, value)
}

func (r *Runner) lookup(ctx context.Context, query, kind, field, value string) (Job, bool, error) {
	if !codePattern.MatchString(field) {
		return Job{}, false, fmt.Errorf("jobs: bad payload field %q", field)
	}
	j, err := scanJob(r.st.Reader().QueryRowContext(ctx, query, kind, field, value))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, false, nil
	}
	if err != nil {
		return Job{}, false, err
	}
	return j, true, nil
}
