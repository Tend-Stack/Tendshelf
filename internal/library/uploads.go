package library

import (
	"context"
	"database/sql"
	"fmt"
)

// SetUploadFolder chooses the folder of a library that receives uploads (decision 0015); an empty
// folderID turns uploads off. The folder must belong to the library: the check is part of the
// UPDATE itself, so no other order of events can point a library at someone else's folder.
func (r *Repo) SetUploadFolder(ctx context.Context, libraryID, folderID string) error {
	return r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var res sql.Result
		var err error
		if folderID == "" {
			res, err = tx.ExecContext(ctx, `UPDATE libraries SET upload_folder_id = NULL WHERE id = ?`, libraryID)
		} else {
			res, err = tx.ExecContext(ctx, `UPDATE libraries SET upload_folder_id = ?
  WHERE id = ? AND EXISTS (SELECT 1 FROM library_folders WHERE id = ? AND library_id = ?)`,
				folderID, libraryID, folderID, libraryID)
		}
		if err != nil {
			return mapErr(err, "library")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: library folder", ErrNotFound)
		}
		return nil
	})
}

// BookRootsIn lists the root paths of every book in a folder, missing ones included, so a new book
// is never placed inside or on top of an existing one.
func (r *Repo) BookRootsIn(ctx context.Context, folderID string) ([]string, error) {
	rows, err := r.st.Reader().QueryContext(ctx, `SELECT root_path FROM books WHERE folder_id = ? ORDER BY root_path`, folderID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteBookAndFiles deletes a book's audio file rows and then the book (cascading to its chapters,
// metadata, progress, bookmarks and queue rows). It is the database half of "Remove this book"
// (decision 0015, point 11).
func (t *Tx) DeleteBookAndFiles(id string) error {
	if _, err := t.exec(`DELETE FROM audio_files WHERE book_id = ?`, id); err != nil {
		return err
	}
	_, err := t.DeleteEmptyBook(id)
	return err
}
