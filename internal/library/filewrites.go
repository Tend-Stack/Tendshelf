package library

import (
	"context"
	"errors"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

// ErrStateMoved means a ledger row is no longer in the state the caller expected: someone else
// (a second run, or recovery) moved it first.
var ErrStateMoved = errors.New("library: file write is in another state")

// States of a file_writes row (migration 0011). The open ones are every state but the last three.
const (
	WriteCopying    = "copying"
	WriteVerifying  = "verifying"
	WriteSwapping   = "swapping"
	WriteSwapped    = "swapped"
	WriteConfirming = "confirming"
	WriteConfirmed  = "confirmed"
	WriteUndoing    = "undoing"
	WriteUndone     = "undone"
	WriteFailed     = "failed"
)

// FileWrite is one row of the write-details ledger: one audio file and one write. The paths are
// the original and its two hidden siblings. The ledger row is committed before each file-system
// step, so after a crash the state names the step that may be half done.
type FileWrite struct {
	ID, JobID, BookID, AudioFileID string
	State                          string
	OriginalPath, BackupPath       string
	TempPath                       string
	BeforeSHA256, AfterSHA256      string
	AudioHash, ErrorCode           string
	CreatedBy                      string
	CreatedAt, UpdatedAt           int64
}

// NewFileWriteID returns a new ledger row id ("fw_" plus a ULID).
func NewFileWriteID() string { return db.NewID("fw") }

const (
	fileWriteColNames = `id, job_id, book_id, audio_file_id, state, original_path, backup_path, temp_path,
  before_sha256, after_sha256, audio_hash, error_code, created_by, created_at, updated_at`
	// fileWriteCols reads the nullable columns as empty strings.
	fileWriteCols = `id, job_id, book_id, audio_file_id, state, original_path, backup_path, temp_path,
  before_sha256, COALESCE(after_sha256, ''), audio_hash, COALESCE(error_code, ''), created_by, created_at, updated_at`
)

func scanFileWrite(s scanner) (FileWrite, error) {
	var w FileWrite
	err := s.Scan(&w.ID, &w.JobID, &w.BookID, &w.AudioFileID, &w.State, &w.OriginalPath, &w.BackupPath, &w.TempPath,
		&w.BeforeSHA256, &w.AfterSHA256, &w.AudioHash, &w.ErrorCode, &w.CreatedBy, &w.CreatedAt, &w.UpdatedAt)
	return w, err
}

// InsertFileWrite adds a row. A second open row for the same original path is ErrConflict.
func (t *Tx) InsertFileWrite(w *FileWrite) error {
	if w.ID == "" {
		w.ID = NewFileWriteID()
	}
	if w.CreatedAt == 0 {
		w.CreatedAt = t.r.nowMs()
	}
	w.UpdatedAt = w.CreatedAt
	_, err := t.exec(`INSERT INTO file_writes(`+fileWriteColNames+`) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		w.ID, w.JobID, w.BookID, w.AudioFileID, w.State, w.OriginalPath, w.BackupPath, w.TempPath,
		w.BeforeSHA256, nullStr(w.AfterSHA256), w.AudioHash, nullStr(w.ErrorCode), w.CreatedBy, w.CreatedAt, w.UpdatedAt)
	return mapErr(err, "file write")
}

// SetFileWriteState moves a row from one state to another. A non-nil after records after_sha256;
// a non-empty code records error_code. A row that is not in state from gives ErrStateMoved.
func (t *Tx) SetFileWriteState(id, from, to string, after *string, code string) error {
	var a any
	if after != nil {
		a = *after
	}
	res, err := t.exec(`UPDATE file_writes SET state = ?, after_sha256 = COALESCE(?, after_sha256),
  error_code = COALESCE(?, error_code), updated_at = ? WHERE id = ? AND state = ?`,
		to, a, nullStr(code), t.r.nowMs(), id, from)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrStateMoved
	}
	return nil
}

// OpenFileWrites returns every row that is not confirmed, undone or failed, oldest first: the rows
// recovery looks at and the backups the person has not yet decided about.
func (r *Repo) OpenFileWrites(ctx context.Context) ([]FileWrite, error) {
	return many(ctx, r.st.Reader(), scanFileWrite, `SELECT `+fileWriteCols+` FROM file_writes
  WHERE state NOT IN ('confirmed','undone','failed') ORDER BY created_at, id`)
}

// BookFileWrites returns a book's ledger rows in the given states (every state when none is
// named), oldest first.
func (r *Repo) BookFileWrites(ctx context.Context, bookID string, states ...string) ([]FileWrite, error) {
	q := `SELECT ` + fileWriteCols + ` FROM file_writes WHERE book_id = ?`
	args := []any{bookID}
	if len(states) > 0 {
		q += ` AND state IN (?` + strings.Repeat(`,?`, len(states)-1) + `)`
		for _, s := range states {
			args = append(args, s)
		}
	}
	return many(ctx, r.st.Reader(), scanFileWrite, q+` ORDER BY created_at, id`, args...)
}

// FileWrite returns one ledger row, or ErrNotFound.
func (r *Repo) FileWrite(ctx context.Context, id string) (FileWrite, error) {
	return one(r.st.Reader().QueryRowContext(ctx, `SELECT `+fileWriteCols+` FROM file_writes WHERE id = ?`, id),
		scanFileWrite, "file write")
}
