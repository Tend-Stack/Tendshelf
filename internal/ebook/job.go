package ebook

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
)

// KindIngest is the job kind that reads one ebook file into a document.
const KindIngest = "ebook.ingest"

// IngestPayload is the payload of an ebook.ingest job.
type IngestPayload struct {
	FileID string `json:"file_id"`
}

// IngestResult is the stored result of an ebook.ingest job.
type IngestResult struct {
	State    string `json:"state"`
	Reason   string `json:"reason,omitempty"`
	Chapters int    `json:"chapters,omitempty"`
	Words    int    `json:"words,omitempty"`
}

// Register adds the ingest job kind and removes upload temp files a crash left behind.
func (s *Service) Register(r *jobs.Runner) error {
	s.removeIncoming()
	if err := r.Register(jobs.Kind{Name: KindIngest, Concurrency: 1, MaxAttempts: 2, Handler: s.handleIngest}); err != nil {
		return err
	}
	return r.Register(jobs.Kind{Name: KindDigests, Concurrency: 1, MaxAttempts: 2, Handler: s.handleDigests})
}

// errDiscard rolls back a result whose file changed while it was being read.
var errDiscard = errors.New("ebook: result discarded")

func (s *Service) handleIngest(ctx context.Context, t *jobs.Task) (any, error) {
	var p IngestPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.FileID == "" {
		return nil, &jobs.Error{Code: "bad_payload", Text: "This job was queued with settings Tendshelf cannot read.", Permanent: true, Err: err}
	}
	f, ok, err := s.store.File(ctx, p.FileID)
	if err != nil {
		return nil, err
	}
	if !ok || f.State != "pending" {
		return IngestResult{State: "skipped"}, nil
	}
	path := f.Path
	if f.Origin == "upload" {
		path = s.storedPath(f.SHA256, f.Ext)
	}
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		return s.finishState(ctx, t, f, "failed", "", "missing")
	}
	doc, err := Read(ctx, path, f.Ext, s.limits)
	if err != nil {
		if cerr := context.Cause(ctx); cerr != nil {
			return nil, cerr
		}
		var ref *Refusal
		if errors.As(err, &ref) {
			res, werr := s.finishState(ctx, t, f, "refused", ref.Reason, ref.Code)
			if werr == nil && f.Origin == "upload" {
				s.dropBytes(ctx, f.SHA256, f.Ext, f.ID)
			}
			s.log.Info("ebooks: file refused", "reason", ref.Reason, "code", ref.Code)
			return res, werr
		}
		return s.failRead(ctx, t, f, err)
	}
	js, err := Encode(doc)
	if err != nil {
		return s.failRead(ctx, t, f, err)
	}
	sha, err := WriteDoc(s.textDir(), js)
	if err != nil {
		return s.failRead(ctx, t, f, err)
	}
	olds, err := s.olderEditions(ctx, f.ID, doc.SourceSHA256, doc.Recipe)
	if err != nil {
		return s.failRead(ctx, t, f, err)
	}
	// The name KOReader gives this file. A read failure leaves it empty; the digests job tries again.
	var digest any
	if d, err := PartialMD5(path); err != nil {
		s.log.Warn("ebooks: could not make a document digest", "err", err)
	} else {
		digest = d
	}
	words, chars := 0, 0
	for _, ch := range doc.Chapters {
		for _, b := range ch.Blocks {
			words += len(b.Words)
			chars += u16Len(b.Text)
		}
	}
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := t.Fence(ctx, tx); err != nil {
			return err
		}
		now := s.now().UnixMilli()
		res, err := tx.ExecContext(ctx, `UPDATE ebook_files SET state='ready', sha256=?, format=?, refused_reason=NULL,
error_code=NULL, partial_md5=?, updated_at=? WHERE id=? AND quick_hash=? AND state='pending'`, doc.SourceSHA256, doc.Format, digest, now, f.ID, f.QuickHash)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return errDiscard
		}
		authors, _ := json.Marshal(nonNil(doc.Meta.Authors))
		eid := db.NewID("edn")
		res, err = tx.ExecContext(ctx, `INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format,
layout, title, subtitle, authors_json, language, isbn, asin, chapter_count, word_count, char_count, page_count, created_at, meta_json)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT DO NOTHING`,
			eid, f.ID, doc.SourceSHA256, doc.Recipe, sha, len(js), doc.Format, doc.Layout, doc.Meta.Title, doc.Meta.Subtitle,
			string(authors), doc.Meta.Language, doc.Meta.Identifiers.ISBN, doc.Meta.Identifiers.ASIN,
			len(doc.Chapters), words, chars, doc.PageCount, now, metaJSON(doc.Meta))
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return nil // the same bytes were read by the same recipe before; its chapters are there
		}
		for _, ch := range doc.Chapters {
			cw, cc, first, firstID := 0, 0, "", ""
			for _, b := range ch.Blocks {
				cw += len(b.Words)
				cc += u16Len(b.Text)
				if first == "" && (b.Kind == KindP || b.Kind == KindQuote || b.Kind == KindLI) && strings.TrimSpace(b.Text) != "" {
					first, firstID = firstLine(b.Text), b.ID
				}
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO edition_chapters(edition_id, idx, title, front, first_line, first_block_id,
block_count, word_count, char_count, src) VALUES(?,?,?,?,?,?,?,?,?,?)`,
				eid, ch.Idx, ch.Title, boolInt(ch.Front), first, firstID, len(ch.Blocks), cw, cc, ch.Src); err != nil {
				return err
			}
		}
		return s.rehome(ctx, tx, olds, doc, eid, now)
	})
	if err == nil {
		s.dropDocs(ctx, olds)
	}
	switch {
	case errors.Is(err, errDiscard):
		return IngestResult{State: "discarded"}, nil
	case err != nil:
		if errors.Is(err, jobs.ErrLeaseLost) {
			return nil, err
		}
		return s.failRead(ctx, t, f, err)
	}
	if f.BookID == "" {
		if err := s.pairFile(ctx, f.ID); err != nil {
			s.log.Warn("ebooks: pairing failed", "err", err)
		}
		// Still no book (no audiobook or other edition matched, or a person held the file): it becomes a book.
		if err := s.ensureFile(ctx, f.ID); err != nil {
			s.log.Warn("ebooks: could not make a book", "err", err)
		}
	}
	return IngestResult{State: "ready", Chapters: len(doc.Chapters), Words: words}, nil
}

// finishState writes a refusal or failure, only if the file is still the one this job read.
func (s *Service) finishState(ctx context.Context, t *jobs.Task, f File, state, reason, code string) (any, error) {
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if err := t.Fence(ctx, tx); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE ebook_files SET state=?, refused_reason=NULLIF(?, ''), error_code=?, updated_at=?
WHERE id=? AND quick_hash=? AND state='pending'`, state, reason, code, s.now().UnixMilli(), f.ID, f.QuickHash)
		return err
	})
	if err != nil {
		return nil, err
	}
	return IngestResult{State: state, Reason: reason}, nil
}

// failRead records an unexpected error as failed/read_error and ends the job without retrying.
func (s *Service) failRead(ctx context.Context, t *jobs.Task, f File, cause error) (any, error) {
	if _, err := s.finishState(ctx, t, f, "failed", "", "read_error"); err != nil {
		return nil, err
	}
	return nil, &jobs.Error{Code: "read_error", Text: "Tendshelf could not read this file. Try again later.", Permanent: true, Err: cause}
}

// metaJSON is what the file says beyond the edition's columns (editions.meta_json). A struct, so the
// field order and the bytes are always the same.
func metaJSON(m Meta) string {
	js, err := json.Marshal(struct {
		Description string `json:"description,omitempty"`
		Publisher   string `json:"publisher,omitempty"`
		Year        int    `json:"year,omitempty"`
		Series      string `json:"series,omitempty"`
		SeriesIndex string `json:"series_index,omitempty"`
	}{m.Description, m.Publisher, m.Year, m.Series, m.SeriesIndex})
	if err != nil {
		return "{}"
	}
	return string(js)
}

func nonNil(a []string) []string {
	if a == nil {
		return []string{}
	}
	return a
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// firstLine cuts text at a word boundary to at most 160 runes and adds an ellipsis when it cut.
func firstLine(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	rs := []rune(text)
	if len(rs) <= 160 {
		return text
	}
	cut := 160
	for i := 160; i > 0; i-- {
		if rs[i] == ' ' {
			cut = i
			break
		}
	}
	return strings.TrimRight(string(rs[:cut]), " ,;:") + "…"
}
