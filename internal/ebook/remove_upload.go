package ebook

import (
	"context"
	"database/sql"
)

// RemoveUpload deletes an ebook that was uploaded on a book's Text page ("Remove the text book only"): its row
// (the editions and the reading marks made in it go by cascade) and its stored copy in the data folder, unless
// another upload row still uses the same bytes. The stored copy is Tendshelf's own, never a library file. A
// library ebook is not Tendshelf's to delete and is ErrNotFound here. The book it was on is left alone: the
// caller removes the file from a book that also has audio.
func (s *Service) RemoveUpload(ctx context.Context, userID, bookID, fileID string) error {
	f, ok, err := s.store.File(ctx, fileID)
	if err != nil {
		return err
	}
	if !ok || f.Origin != "upload" || f.BookID != bookID {
		return ErrNotFound
	}
	err = s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM ebook_files WHERE id = ? AND book_id = ? AND origin = 'upload'`, fileID, bookID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return ErrNotFound
		}
		return s.audit(ctx, tx, userID, "ebook.remove_upload", fileID, map[string]string{"book": bookID})
	})
	if err != nil {
		return err
	}
	s.dropBytes(ctx, f.SHA256, f.Ext, fileID)
	return nil
}
