package ebook

import (
	"context"
	"encoding/json"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
)

// KindDigests is the job kind that gives ready ebook files the digest KOReader names them by (S6).
// New files get it in the ingest job; this job catches up on files read before the column existed and on
// files whose digest could not be computed. It only reads files.
const KindDigests = "ebook.digests"

// digestBatch is how many files one run of the job handles.
const digestBatch = 200

// DigestsPayload is the payload of an ebook.digests job. After is the last file id the previous run
// looked at, so a file that cannot be read never makes the job run forever.
type DigestsPayload struct {
	After string `json:"after"`
}

func (s *Service) handleDigests(ctx context.Context, t *jobs.Task) (any, error) {
	var p DigestsPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil {
		return nil, &jobs.Error{Code: "bad_payload", Text: "This job was queued with settings Tendshelf cannot read.", Permanent: true, Err: err}
	}
	rs, err := s.db.Reader().QueryContext(ctx, `SELECT id, quick_hash FROM ebook_files
WHERE state = 'ready' AND partial_md5 IS NULL AND missing_since IS NULL AND id > ? ORDER BY id LIMIT ?`, p.After, digestBatch)
	if err != nil {
		return nil, err
	}
	type todo struct{ id, quick string }
	var batch []todo
	for rs.Next() {
		var w todo
		if err := rs.Scan(&w.id, &w.quick); err != nil {
			rs.Close()
			return nil, err
		}
		batch = append(batch, w)
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return nil, err
	}
	done := 0
	for _, w := range batch {
		if cerr := context.Cause(ctx); cerr != nil {
			return nil, cerr
		}
		f, ok, err := s.store.File(ctx, w.id)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		sum, err := PartialMD5(s.FilePath(f))
		if err != nil {
			s.log.Warn("ebooks: could not make a document digest", "err", err)
			continue
		}
		// Only if the file is still the one that was read.
		res, err := s.db.Writer().ExecContext(ctx, `UPDATE ebook_files SET partial_md5 = ? WHERE id = ? AND quick_hash = ? AND partial_md5 IS NULL`, sum, w.id, w.quick)
		if err != nil {
			return nil, err
		}
		if n, _ := res.RowsAffected(); n == 1 {
			done++
		}
	}
	if len(batch) == digestBatch {
		if _, err := s.runner.Enqueue(ctx, KindDigests, DigestsPayload{After: batch[len(batch)-1].id}, jobs.EnqueueOptions{}); err != nil {
			return nil, err
		}
	}
	return map[string]int{"digests": done}, nil
}

// EnqueueDigests queues the catch-up job when a ready file lacks its digest and none is waiting.
func (s *Service) EnqueueDigests(ctx context.Context) error {
	var n int
	if err := s.db.Reader().QueryRowContext(ctx, `SELECT count(*) FROM (SELECT 1 FROM ebook_files
WHERE state = 'ready' AND partial_md5 IS NULL AND missing_since IS NULL LIMIT 1)`).Scan(&n); err != nil || n == 0 {
		return err
	}
	active, err := s.runner.ActiveKeys(ctx, KindDigests, "after")
	if err != nil || len(active) > 0 {
		return err
	}
	_, err = s.runner.Enqueue(ctx, KindDigests, DigestsPayload{}, jobs.EnqueueOptions{})
	return err
}

// FilesByDigest lists the ready files that have this partial MD5 and a book, oldest first. The caller
// decides which of them the person may read.
func (s *Store) FilesByDigest(ctx context.Context, digest string) ([]File, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT `+fileCols+` FROM ebook_files
WHERE partial_md5 = ? AND state = 'ready' AND book_id IS NOT NULL ORDER BY created_at, id`, digest)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
