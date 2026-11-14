package ebook

import (
	"context"
	"database/sql"
)

// EnsureDefault gives a book that has ready ebook files but no book edition the newest ready one, after an
// edition was removed. A person's own choice that pointed at the removed file went with it (text_choices cascades
// on the file), so that person reads the book's edition again. Nothing changes when the book still has its edition
// or has no ready file.
func (s *Service) EnsureDefault(ctx context.Context, bookID string) error {
	return s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var has int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM ebook_files WHERE book_id = ? AND state = 'ready' AND book_default = 1`, bookID).Scan(&has); err != nil || has > 0 {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE ebook_files SET book_default = 1, updated_at = ? WHERE id = (
SELECT id FROM ebook_files WHERE book_id = ? AND state = 'ready' ORDER BY created_at DESC, id DESC LIMIT 1)`, s.now().UnixMilli(), bookID)
		return err
	})
}
