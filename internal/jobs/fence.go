package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

const (
	// args: job id, attempts
	sqlFenceCheck = `SELECT 1 FROM jobs` + fence

	// args: payload field, kind
	sqlActiveKeys = `SELECT json_extract(payload_json, '$.' || ?) FROM jobs
WHERE kind=? AND state IN ('queued','running','held')`
)

// Fence checks, inside tx, that this run still holds its lease: the job row is
// running with the attempts value this run was claimed with. A handler that
// writes other tables calls it first in the same write transaction, so a run
// that lost its lease (expired, cancelled, superseded) writes nothing. It
// returns ErrLeaseLost when the row no longer matches.
func (t *Task) Fence(ctx context.Context, tx *sql.Tx) error {
	var one int
	err := tx.QueryRowContext(ctx, sqlFenceCheck, t.ID, t.Attempt).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrLeaseLost
	}
	return err
}

// ActiveKeys returns the distinct values of a payload field (a top-level key
// matching ^[a-z_]+$) across the queued, running and held jobs of one kind. A
// producer uses it to avoid enqueueing a job for work that is already waiting.
func (r *Runner) ActiveKeys(ctx context.Context, kind, field string) (map[string]bool, error) {
	if !codePattern.MatchString(field) {
		return nil, fmt.Errorf("jobs: bad payload field %q", field)
	}
	rs, err := r.st.Reader().QueryContext(ctx, sqlActiveKeys, field, kind)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	out := map[string]bool{}
	for rs.Next() {
		var v sql.NullString
		if err := rs.Scan(&v); err != nil {
			return nil, err
		}
		if v.Valid {
			out[v.String] = true
		}
	}
	return out, rs.Err()
}
