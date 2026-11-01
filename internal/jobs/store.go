package jobs

// SQL for the jobs table. Every write a running job makes carries the fence
// (the row is still running and attempts is the token this run was claimed
// with), so a handler that lost its lease can never overwrite a newer run.
const (
	fence = ` WHERE id=? AND state='running' AND attempts=?`

	sqlInsert = `INSERT INTO jobs(id, kind, state, payload_json, max_attempts, not_before, created_by, created_at, updated_at)
VALUES (?, ?, 'queued', ?, ?, ?, ?, ?, ?)`

	sqlGet = `SELECT id, kind, state, payload_json, result_json, progress_json, error_code, error_text,
attempts, max_attempts, lease_until, not_before, created_by, created_at, updated_at FROM jobs WHERE id=?`

	// args: lease_until, now, kind, now
	sqlClaim = `UPDATE jobs SET state='running', attempts=attempts+1, lease_until=?, updated_at=?
WHERE id=(SELECT id FROM jobs WHERE state='queued' AND kind=? AND not_before<=? ORDER BY not_before, created_at, id LIMIT 1)
RETURNING id, payload_json, attempts, max_attempts`

	// args: new lease_until, now, id, attempts, now
	sqlRenew = `UPDATE jobs SET lease_until=?, updated_at=?` + fence + ` AND lease_until>=?`

	// args: progress, now, id, attempts
	sqlProgress = `UPDATE jobs SET progress_json=?, updated_at=?` + fence

	// args: result, progress, now, id, attempts
	sqlDone = `UPDATE jobs SET state='done', result_json=?, progress_json=COALESCE(?, progress_json),
error_code=NULL, error_text=NULL, lease_until=NULL, updated_at=?` + fence

	// args: not_before, code, text, progress, now, id, attempts
	sqlRetry = `UPDATE jobs SET state='queued', not_before=?, error_code=?, error_text=?,
progress_json=COALESCE(?, progress_json), lease_until=NULL, updated_at=?` + fence

	// args: code, text, progress, now, id, attempts
	sqlFail = `UPDATE jobs SET state='failed', error_code=?, error_text=?,
progress_json=COALESCE(?, progress_json), lease_until=NULL, updated_at=?` + fence

	// Stop: the handler was interrupted by shutdown. args: not_before, progress, now, id, attempts
	sqlRequeueShutdown = `UPDATE jobs SET state='queued', attempts=attempts-1, not_before=?, error_code=NULL, error_text=NULL,
progress_json=COALESCE(?, progress_json), lease_until=NULL, updated_at=?` + fence

	// args: code, text, progress, now, id, attempts
	sqlHold = `UPDATE jobs SET state='held', error_code=?, error_text=?,
progress_json=COALESCE(?, progress_json), lease_until=NULL, updated_at=?` + fence

	// args: now
	sqlExpired = `SELECT id, kind, attempts, max_attempts FROM jobs WHERE state='running' AND lease_until<?`

	// args: not_before, now, id, attempts
	sqlRecoverQueue = `UPDATE jobs SET state='queued', not_before=?, lease_until=NULL, updated_at=?` + fence

	sqlStateOf = `SELECT state FROM jobs WHERE id=?`

	// args: now, id
	sqlCancel = `UPDATE jobs SET state='cancelled', lease_until=NULL, updated_at=? WHERE id=? AND state IN ('queued','held','running')`

	// args: now, now, id
	sqlResume = `UPDATE jobs SET state='queued', not_before=?, error_code=NULL, error_text=NULL, updated_at=? WHERE id=? AND state='held'`
)

const (
	textInternal    = "Something went wrong while running this job."
	textExhausted   = "The server stopped while this job was running too many times, so it was not tried again."
	textInterrupted = "The server stopped while this job was running. It waits for a decision before running again."

	codeInternal    = "internal"
	codeInterrupted = "interrupted"
)
