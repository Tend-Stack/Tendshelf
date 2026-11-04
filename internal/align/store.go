package align

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

// RecipeAlign names the alignment pipeline; a change to any stage bumps it so alignments are made again.
const RecipeAlign = "tendshelf-align-v1"

// Alignment states.
const (
	StateQueued    = "queued"
	StateEmitting  = "emitting"
	StateAligning  = "aligning"
	StateDone      = "done"
	StatePartial   = "partial"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
	StateHeld      = "held"
)

// Chapter states.
const (
	ChapterPending   = "pending"
	ChapterAligned   = "aligned"
	ChapterUnmatched = "unmatched"
	ChapterFailed    = "failed"
)

// activeGuard selects the rows a running job may write to. Every job write repeats this guard so a Cancel
// or Discard that landed first is never overwritten.
const activeGuard = `id = ? AND job_id = ? AND state IN ('queued','emitting','aligning','held')`

// Alignment is one row of alignments. Empty strings stand for NULL, zero times for NULL.
type Alignment struct {
	ID, EditionID, BookID string
	Source                string // "model" or "overlay"
	Recipe                string
	RuntimeID             string
	ModelSHA256           string
	AudioKey, DocSHA256   string
	State                 string
	Labels                []string
	JobID                 string
	Coverage              float64
	SentencesTotal        int
	SentencesTimed        int
	ErrorCode             string
	CreatedAt, UpdatedAt  int64
	FinishedAt            int64
	SupersededBy          string
}

// ChapterRow is one row of alignment_chapters. A nil StartMS means the chapter has no times.
type ChapterRow struct {
	Idx            int
	State          string
	ChapterKey     string
	StartMS, EndMS *int64
	SentencesTotal int
	SentencesTimed int
	MeanConfidence *float64
	UpdatedAt      int64
}

// Finish is what a job writes when it ends: the state (done, partial or failed) and the counts.
type Finish struct {
	State          string
	Labels         []string
	Coverage       float64
	SentencesTotal int
	SentencesTimed int
	ErrorCode      string
}

// Store reads and writes alignments, runtime components excluded.
type Store struct {
	db  *db.Store
	now func() time.Time
}

// NewStore returns a Store over st.
func NewStore(st *db.Store) *Store { return &Store{db: st, now: time.Now} }

const alignmentCols = `id, edition_id, book_id, source, recipe, runtime_id, model_sha256, audio_key, doc_sha256, state,
labels_json, job_id, coverage, sentences_total, sentences_timed, error_code, created_at, updated_at, finished_at, superseded_by`

type scanner interface{ Scan(...any) error }

func scanAlignment(r scanner) (Alignment, error) {
	var a Alignment
	var runtime, model, job, code, super sql.NullString
	var finished sql.NullInt64
	var labels string
	err := r.Scan(&a.ID, &a.EditionID, &a.BookID, &a.Source, &a.Recipe, &runtime, &model, &a.AudioKey, &a.DocSHA256,
		&a.State, &labels, &job, &a.Coverage, &a.SentencesTotal, &a.SentencesTimed, &code, &a.CreatedAt, &a.UpdatedAt,
		&finished, &super)
	if err != nil {
		return a, err
	}
	a.RuntimeID, a.ModelSHA256, a.JobID, a.ErrorCode, a.SupersededBy = runtime.String, model.String, job.String, code.String, super.String
	a.FinishedAt = finished.Int64
	if err := json.Unmarshal([]byte(labels), &a.Labels); err != nil {
		return a, err
	}
	return a, nil
}

// nullStr maps "" to NULL.
func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Insert writes a. Empty ID, Source, Recipe, State and times take their defaults (new id, "model",
// RecipeAlign, queued, now). A second active alignment for the edition fails (alignments_active).
func (s *Store) Insert(ctx context.Context, a Alignment) error {
	now := s.now().UnixMilli()
	if a.ID == "" {
		a.ID = db.NewID("al")
	}
	if a.Source == "" {
		a.Source = "model"
	}
	if a.Recipe == "" {
		a.Recipe = RecipeAlign
	}
	if a.State == "" {
		a.State = StateQueued
	}
	if a.CreatedAt == 0 {
		a.CreatedAt = now
	}
	if a.UpdatedAt == 0 {
		a.UpdatedAt = now
	}
	if a.Labels == nil {
		a.Labels = []string{}
	}
	labels, err := json.Marshal(a.Labels)
	if err != nil {
		return err
	}
	var finished any
	if a.FinishedAt != 0 {
		finished = a.FinishedAt
	}
	_, err = s.db.Writer().ExecContext(ctx, `INSERT INTO alignments(`+alignmentCols+`)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		a.ID, a.EditionID, a.BookID, a.Source, a.Recipe, nullStr(a.RuntimeID), nullStr(a.ModelSHA256), a.AudioKey, a.DocSHA256,
		a.State, string(labels), nullStr(a.JobID), a.Coverage, a.SentencesTotal, a.SentencesTimed, nullStr(a.ErrorCode),
		a.CreatedAt, a.UpdatedAt, finished, nullStr(a.SupersededBy))
	return err
}

func (s *Store) one(ctx context.Context, where string, args ...any) (Alignment, bool, error) {
	a, err := scanAlignment(s.db.Reader().QueryRowContext(ctx, `SELECT `+alignmentCols+` FROM alignments WHERE `+where, args...))
	if errors.Is(err, sql.ErrNoRows) {
		return Alignment{}, false, nil
	}
	return a, err == nil, err
}

// Get returns the alignment with this id.
func (s *Store) Get(ctx context.Context, id string) (Alignment, bool, error) {
	return s.one(ctx, `id = ?`, id)
}

// ByJob returns the newest alignment that a job wrote for.
func (s *Store) ByJob(ctx context.Context, jobID string) (Alignment, bool, error) {
	return s.one(ctx, `job_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`, jobID)
}

// Active returns the queued, running or held alignment of an edition, if any.
func (s *Store) Active(ctx context.Context, editionID string) (Alignment, bool, error) {
	return s.one(ctx, `edition_id = ? AND state IN ('queued','emitting','aligning','held')`, editionID)
}

// Latest returns the newest finished (done or partial) alignment of an edition that nothing replaced.
func (s *Store) Latest(ctx context.Context, editionID string) (Alignment, bool, error) {
	return s.one(ctx, `edition_id = ? AND state IN ('done','partial') AND superseded_by IS NULL
ORDER BY created_at DESC, id DESC LIMIT 1`, editionID)
}

// SetState moves an active alignment of this job to state, with an optional error code. It reports false
// when the row is no longer active (cancelled, discarded, finished) or belongs to another job.
func (s *Store) SetState(ctx context.Context, id, jobID, state, code string) (bool, error) {
	now := s.now().UnixMilli()
	var finished any
	switch state {
	case StateDone, StatePartial, StateFailed, StateCancelled:
		finished = now
	}
	res, err := s.db.Writer().ExecContext(ctx, `UPDATE alignments SET state = ?, error_code = ?, updated_at = ?,
finished_at = COALESCE(?, finished_at) WHERE `+activeGuard, state, nullStr(code), now, finished, id, jobID)
	return affected(res, err)
}

// Finish ends an active alignment of this job with its counts. A done or partial result also marks the
// edition's older done or partial alignments as superseded by it, in the same transaction.
func (s *Store) Finish(ctx context.Context, id, jobID string, f Finish) (bool, error) {
	now := s.now().UnixMilli()
	labels := f.Labels
	if labels == nil {
		labels = []string{}
	}
	lj, err := json.Marshal(labels)
	if err != nil {
		return false, err
	}
	tx, err := s.db.Writer().BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE alignments SET state = ?, labels_json = ?, coverage = ?, sentences_total = ?,
sentences_timed = ?, error_code = ?, updated_at = ?, finished_at = ? WHERE `+activeGuard,
		f.State, string(lj), f.Coverage, f.SentencesTotal, f.SentencesTimed, nullStr(f.ErrorCode), now, now, id, jobID)
	ok, err := affected(res, err)
	if err != nil || !ok {
		return false, err
	}
	if f.State == StateDone || f.State == StatePartial {
		if _, err := tx.ExecContext(ctx, `UPDATE alignments SET superseded_by = ? WHERE edition_id =
(SELECT edition_id FROM alignments WHERE id = ?) AND id <> ? AND state IN ('done','partial') AND superseded_by IS NULL`,
			id, id, id); err != nil {
			return false, err
		}
	}
	return true, tx.Commit()
}

// Adopt is what a job does when it starts and finds its alignment held: a person resumed it, so it takes
// the current audio key, model and runtime, clears the error and goes back to queued. It reports false
// unless the row is held and belongs to this job.
func (s *Store) Adopt(ctx context.Context, id, jobID, audioKey, modelSHA, runtimeID string) (bool, error) {
	res, err := s.db.Writer().ExecContext(ctx, `UPDATE alignments SET audio_key = ?, model_sha256 = ?, runtime_id = ?,
state = 'queued', error_code = NULL, updated_at = ? WHERE id = ? AND job_id = ? AND state = 'held'`,
		audioKey, nullStr(modelSHA), nullStr(runtimeID), s.now().UnixMilli(), id, jobID)
	return affected(res, err)
}

func affected(res sql.Result, err error) (bool, error) {
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Chapters lists the chapter rows of an alignment in text order.
func (s *Store) Chapters(ctx context.Context, id string) ([]ChapterRow, error) {
	rs, err := s.db.Reader().QueryContext(ctx, `SELECT chapter_idx, state, chapter_key, start_ms, end_ms, sentences_total,
sentences_timed, mean_confidence, updated_at FROM alignment_chapters WHERE alignment_id = ? ORDER BY chapter_idx`, id)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []ChapterRow
	for rs.Next() {
		c, err := scanChapter(rs)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rs.Err()
}

func scanChapter(r scanner) (ChapterRow, error) {
	var c ChapterRow
	var key sql.NullString
	var start, end sql.NullInt64
	var conf sql.NullFloat64
	if err := r.Scan(&c.Idx, &c.State, &key, &start, &end, &c.SentencesTotal, &c.SentencesTimed, &conf, &c.UpdatedAt); err != nil {
		return c, err
	}
	c.ChapterKey = key.String
	if start.Valid {
		v := start.Int64
		c.StartMS = &v
	}
	if end.Valid {
		v := end.Int64
		c.EndMS = &v
	}
	if conf.Valid {
		v := conf.Float64
		c.MeanConfidence = &v
	}
	return c, nil
}

// PutChapter writes (or replaces) one chapter row of an active alignment of this job. It reports false
// when the alignment is no longer active, so a cancelled job leaves nothing behind.
func (s *Store) PutChapter(ctx context.Context, id, jobID string, c ChapterRow) (bool, error) {
	if c.UpdatedAt == 0 {
		c.UpdatedAt = s.now().UnixMilli()
	}
	res, err := s.db.Writer().ExecContext(ctx, `INSERT INTO alignment_chapters(alignment_id, chapter_idx, state, chapter_key,
start_ms, end_ms, sentences_total, sentences_timed, mean_confidence, updated_at)
SELECT id, ?, ?, ?, ?, ?, ?, ?, ?, ? FROM alignments WHERE `+activeGuard+`
ON CONFLICT(alignment_id, chapter_idx) DO UPDATE SET state = excluded.state, chapter_key = excluded.chapter_key,
start_ms = excluded.start_ms, end_ms = excluded.end_ms, sentences_total = excluded.sentences_total,
sentences_timed = excluded.sentences_timed, mean_confidence = excluded.mean_confidence, updated_at = excluded.updated_at`,
		c.Idx, c.State, nullStr(c.ChapterKey), c.StartMS, c.EndMS, c.SentencesTotal, c.SentencesTimed, c.MeanConfidence,
		c.UpdatedAt, id, jobID)
	return affected(res, err)
}

// ChapterByKey finds an aligned chapter with this chapter key in any alignment: the resume point for a
// timing file that already exists.
func (s *Store) ChapterByKey(ctx context.Context, key string) (ChapterRow, bool, error) {
	c, err := scanChapter(s.db.Reader().QueryRowContext(ctx, `SELECT chapter_idx, state, chapter_key, start_ms, end_ms,
sentences_total, sentences_timed, mean_confidence, updated_at FROM alignment_chapters
WHERE chapter_key = ? AND state = 'aligned' ORDER BY updated_at DESC LIMIT 1`, key))
	if errors.Is(err, sql.ErrNoRows) {
		return ChapterRow{}, false, nil
	}
	return c, err == nil, err
}
