package ebook

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// Errors of the pairing writes. None of them touches a file on disk: pairing changes rows only.
var (
	ErrFileNotFound  = errors.New("ebook: file not found")
	ErrBookNotFound  = errors.New("ebook: book not found")
	ErrNotReady      = errors.New("ebook: file is not ready")
	ErrAlreadyOnBook = errors.New("ebook: the book already has this upload")
)

func (s *Service) audit(ctx context.Context, tx *sql.Tx, userID, action, fileID string, detail any) error {
	return db.Audit(ctx, tx, db.AuditEntry{At: s.now().UnixMilli(), UserID: userID, Action: action, Target: "ebook:" + fileID, Detail: detail})
}

func bookLive(ctx context.Context, tx *sql.Tx, bookID string) (bool, error) {
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM books WHERE id = ? AND missing_since IS NULL`, bookID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// Pair puts a ready file on a book by a person's choice. It moves the file off any other book.
func (s *Service) Pair(ctx context.Context, fileID, bookID, userID string) error {
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var state, origin, sha, from string
		err := tx.QueryRowContext(ctx, `SELECT state, origin, COALESCE(sha256,''), COALESCE(book_id,'') FROM ebook_files WHERE id = ?`, fileID).
			Scan(&state, &origin, &sha, &from)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrFileNotFound
		}
		if err != nil {
			return err
		}
		if state != "ready" {
			return ErrNotReady
		}
		if ok, err := bookLive(ctx, tx, bookID); err != nil {
			return err
		} else if !ok {
			return ErrBookNotFound
		}
		if from == bookID {
			return nil
		}
		if origin == "upload" && sha != "" {
			var one int
			err := tx.QueryRowContext(ctx, `SELECT 1 FROM ebook_files WHERE book_id = ? AND origin = 'upload' AND sha256 = ? AND id <> ? LIMIT 1`,
				bookID, sha, fileID).Scan(&one)
			if err == nil {
				return ErrAlreadyOnBook
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		now := s.now().UnixMilli()
		if _, err := tx.ExecContext(ctx, `UPDATE ebook_files SET book_id=?, paired_by='person', hold=NULL, book_default=0, updated_at=? WHERE id=?`,
			bookID, now, fileID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM ebook_not_same WHERE ebook_file_id=? AND book_id=?`, fileID, bookID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM text_choices WHERE ebook_file_id=?`, fileID); err != nil {
			return err
		}
		return s.audit(ctx, tx, userID, "ebook.pair", fileID, map[string]string{"book": bookID, "from": from})
	})
}

// Unpair takes a file off the book it is on, remembers that a person did it, and says it is not that book.
func (s *Service) Unpair(ctx context.Context, fileID, bookID, userID string) error {
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		now := s.now().UnixMilli()
		res, err := tx.ExecContext(ctx, `UPDATE ebook_files SET book_id=NULL, paired_by=NULL, hold='person', book_default=0, updated_at=?
WHERE id=? AND book_id=?`, now, fileID, bookID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrFileNotFound
		}
		if err := insertNotSame(ctx, tx, fileID, bookID, userID, now); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM text_choices WHERE ebook_file_id=?`, fileID); err != nil {
			return err
		}
		return s.audit(ctx, tx, userID, "ebook.unpair", fileID, map[string]string{"book": bookID})
	})
}

func insertNotSame(ctx context.Context, tx *sql.Tx, fileID, bookID, userID string, now int64) error {
	var uid any
	if userID != "" {
		uid = userID
	}
	_, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO ebook_not_same(ebook_file_id, book_id, user_id, created_at) VALUES (?,?,?,?)`,
		fileID, bookID, uid, now)
	return err
}

// NotSame records that an unpaired file is not this book, so no pairing offers it again.
func (s *Service) NotSame(ctx context.Context, fileID, bookID, userID string) error {
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var one int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM ebook_files WHERE id=? AND book_id IS NULL`, fileID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrFileNotFound
		}
		if err != nil {
			return err
		}
		if ok, err := bookLive(ctx, tx, bookID); err != nil {
			return err
		} else if !ok {
			return ErrBookNotFound
		}
		if err := insertNotSame(ctx, tx, fileID, bookID, userID, s.now().UnixMilli()); err != nil {
			return err
		}
		return s.audit(ctx, tx, userID, "ebook.not_same", fileID, map[string]string{"book": bookID})
	})
}

// Keep marks an unpaired file as kept on its own: automatic pairing leaves it alone.
func (s *Service) Keep(ctx context.Context, fileID, userID string) error {
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE ebook_files SET hold='kept', updated_at=? WHERE id=? AND book_id IS NULL`, s.now().UnixMilli(), fileID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrFileNotFound
		}
		return s.audit(ctx, tx, userID, "ebook.keep", fileID, nil)
	})
}

// SetBookDefault makes a ready file on the book the book's edition.
func (s *Service) SetBookDefault(ctx context.Context, bookID, fileID, userID string) error {
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE ebook_files SET book_default=0 WHERE book_id=? AND book_default=1`, bookID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `UPDATE ebook_files SET book_default=1, updated_at=? WHERE id=? AND book_id=? AND state='ready'`,
			s.now().UnixMilli(), fileID, bookID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrFileNotFound
		}
		return s.audit(ctx, tx, userID, "ebook.default", fileID, map[string]string{"book": bookID})
	})
}

// Choose records the edition a person reads for a book. Choosing the book's default clears the choice.
func (s *Service) Choose(ctx context.Context, userID, bookID, fileID string) error {
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var def int
		err := tx.QueryRowContext(ctx, `SELECT book_default FROM ebook_files WHERE id=? AND book_id=? AND state='ready'`, fileID, bookID).Scan(&def)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrFileNotFound
		}
		if err != nil {
			return err
		}
		if def == 1 {
			_, err = tx.ExecContext(ctx, `DELETE FROM text_choices WHERE user_id=? AND book_id=?`, userID, bookID)
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO text_choices(user_id, book_id, ebook_file_id, updated_at) VALUES (?,?,?,?)
ON CONFLICT(user_id, book_id) DO UPDATE SET ebook_file_id=excluded.ebook_file_id, updated_at=excluded.updated_at`,
			userID, bookID, fileID, s.now().UnixMilli())
		return err
	})
}

// PairPending pairs every unpaired, ready file that automatic pairing may touch and that has exactly one candidate.
func (s *Service) PairPending(ctx context.Context) error {
	keys, err := s.store.bookKeys(ctx, share.Filter{})
	if err != nil || len(keys) == 0 {
		return err
	}
	afterAt, afterID := int64(-1), ""
	for {
		rs, err := s.db.Reader().QueryContext(ctx, `SELECT `+fileCols+` FROM ebook_files
WHERE book_id IS NULL AND hold IS NULL AND state = 'ready' AND (created_at, id) > (?, ?) ORDER BY created_at, id LIMIT ?`,
			afterAt, afterID, pairBatch)
		if err != nil {
			return err
		}
		var files []File
		for rs.Next() {
			f, err := scanFile(rs)
			if err != nil {
				rs.Close()
				return err
			}
			files = append(files, f)
		}
		rs.Close()
		if err := rs.Err(); err != nil {
			return err
		}
		if len(files) == 0 {
			return nil
		}
		if err := s.pairFiles(ctx, files, keys); err != nil {
			return err
		}
		last := files[len(files)-1]
		afterAt, afterID = last.CreatedAt, last.ID
		if len(files) < pairBatch {
			return nil
		}
	}
}

// pairFile is PairPending for one file.
func (s *Service) pairFile(ctx context.Context, fileID string) error {
	f, ok, err := s.store.File(ctx, fileID)
	if err != nil || !ok || f.BookID != "" || f.Hold != "" || f.State != "ready" {
		return err
	}
	keys, err := s.store.bookKeys(ctx, share.Filter{})
	if err != nil || len(keys) == 0 {
		return err
	}
	return s.pairFiles(ctx, []File{f}, keys)
}

func (s *Service) pairFiles(ctx context.Context, files []File, keys []BookKey) error {
	ids := make([]string, len(files))
	for i, f := range files {
		ids[i] = f.ID
	}
	ns, err := s.store.notSameFor(ctx, ids)
	if err != nil {
		return err
	}
	for _, f := range files {
		ed, ok, err := s.store.CurrentEdition(ctx, f.ID)
		if err != nil {
			return err
		}
		if !ok {
			continue
		}
		bookID, by, ok := Decide(Match(ed.pairMeta(), keys, ns[f.ID], 0))
		if !ok {
			continue
		}
		// The WHERE repeats every condition, so a person's change since the read wins (invariant 3).
		if _, err := s.db.Writer().ExecContext(ctx, `UPDATE ebook_files SET book_id=?, paired_by=?, book_default=0, updated_at=?
WHERE id=? AND book_id IS NULL AND hold IS NULL
AND NOT EXISTS (SELECT 1 FROM ebook_not_same WHERE ebook_file_id=? AND book_id=?)
AND EXISTS (SELECT 1 FROM books WHERE id=? AND missing_since IS NULL)`,
			bookID, by, s.now().UnixMilli(), f.ID, f.ID, bookID, bookID); err != nil {
			return err
		}
	}
	return nil
}
