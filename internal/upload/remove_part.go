package upload

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"slices"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// Removing one part of a combined book (decision 0015, amendment "Remove one part"): a book that has audio and
// attached ebook files can lose its text or its audio on its own. The rules are the whole-book ones applied to
// that part alone: only a file this package placed (a present ledger row) that is unchanged is deleted, through
// the same removeFile, and a library file is never touched.

// PartFile is one attached ebook file and whether it can be removed on its own.
type PartFile struct {
	ID string
	// Path is the library path of a file found in or placed into a library; empty for a copy uploaded on the
	// Text page, which lives in Tendshelf's own data folder (Uploaded is true).
	Path      string
	Uploaded  bool
	Removable bool
}

// Parts says what "Remove this book" can do for a book. Combined is true when the book has live audio and
// at least one attached ebook file; only then are parts offered.
type Parts struct {
	Combined bool
	Whole    bool
	Audio    bool
	// HasAudio is true when the book has live audio, owned or not.
	HasAudio bool
	// Text lists every attached ebook file, also for a text-only book.
	Text []PartFile
}

// EditionRemovable reports whether one ebook file can be removed on its own: it is removable under the ledger
// rule and the book keeps something (audio, or another ebook file). The only file of a text book is the
// whole-book removal's.
func (p Parts) EditionRemovable(fileID string) (PartFile, bool) {
	for _, f := range p.Text {
		if f.ID == fileID {
			return f, f.Removable && (p.HasAudio || len(p.Text) > 1)
		}
	}
	return PartFile{}, false
}

// AnyText reports whether at least one ebook file can be removed on its own.
func (p Parts) AnyText() bool {
	for _, f := range p.Text {
		if f.Removable {
			return true
		}
	}
	return false
}

// Any reports whether some removal is on offer.
func (p Parts) Any() bool { return p.Whole || (p.Combined && (p.Audio || p.AnyText())) }

// pathOwned is true when path is a present ledger file and, for placedBy, lies in one of that person's roots.
func (s *Service) pathOwned(ctx context.Context, path, placedBy string) bool {
	var one int
	err := s.st.Reader().QueryRowContext(ctx, `SELECT 1 FROM upload_paths WHERE path = ? AND kind = 'file' AND state = 'present' LIMIT 1`, path).Scan(&one)
	if err != nil {
		return false
	}
	if placedBy == "" {
		return true
	}
	err = s.st.Reader().QueryRowContext(ctx, `SELECT 1 FROM (SELECT ? AS root_path) a WHERE `+share.AddedBySQL("a"), path, placedBy).Scan(&one)
	return err == nil
}

// PartsOf reads what can be removed; placedBy is the contributor's id, empty for owner and admin.
func (s *Service) PartsOf(ctx context.Context, bookID, placedBy string) (Parts, error) {
	var p Parts
	if placedBy != "" {
		p.Whole = s.RemovableBy(ctx, bookID, placedBy)
	} else {
		p.Whole = s.Removable(ctx, bookID)
	}
	rows, err := s.st.Reader().QueryContext(ctx, `SELECT path FROM audio_files WHERE book_id = ? AND missing_since IS NULL ORDER BY idx`, bookID)
	if err != nil {
		return p, err
	}
	var audio []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			rows.Close()
			return p, err
		}
		audio = append(audio, path)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return p, err
	}
	files, err := s.st.Reader().QueryContext(ctx, `SELECT id, COALESCE(path,''), origin = 'upload', COALESCE(uploaded_by,'') FROM ebook_files
WHERE book_id = ? AND missing_since IS NULL ORDER BY created_at, id`, bookID)
	if err != nil {
		return p, err
	}
	defer files.Close()
	for files.Next() {
		var f PartFile
		var by string
		if err := files.Scan(&f.ID, &f.Path, &f.Uploaded, &by); err != nil {
			return p, err
		}
		if f.Uploaded {
			// Tendshelf's own stored copy: a contributor removes only what they uploaded.
			f.Removable = placedBy == "" || by == placedBy
		} else {
			f.Removable = s.pathOwned(ctx, f.Path, placedBy)
		}
		p.Text = append(p.Text, f)
	}
	if err := files.Err(); err != nil {
		return p, err
	}
	p.Combined = len(audio) > 0 && len(p.Text) > 0
	p.Audio = len(audio) > 0
	for _, a := range audio {
		if !s.pathOwned(ctx, a, placedBy) {
			p.Audio = false
			break
		}
	}
	p.HasAudio = len(audio) > 0
	if !p.Combined {
		p.Audio = false
	}
	// A book with audio and text is removed whole when its audio is Tendshelf's: its removable ebook files go with it
	// and a library ebook that is not Tendshelf's stays on disk, unpaired.
	if p.Combined && p.Audio {
		p.Whole = true
	}
	// A text-only book is Tendshelf's to remove when every one of its files is: stored Text-page uploads and
	// ledger files alike.
	if !p.HasAudio && len(p.Text) > 0 && !slices.ContainsFunc(p.Text, func(f PartFile) bool { return !f.Removable }) {
		p.Whole = true
	}
	return p, nil
}

// Staying is how many attached ebook files a whole-book removal leaves on disk because Tendshelf did not place them.
func (p Parts) Staying() int {
	n := 0
	for _, f := range p.Text {
		if !f.Removable {
			n++
		}
	}
	return n
}

// RemoveWhole removes a whole book. A book with audio and ebook files first loses every ebook file that is
// removable on its own, by the same rules and code as one edition (dropUpload removes a copy uploaded on the Text
// page, ebook.Service.RemoveUpload; the others go through RemoveEbookFile); an ebook file that is not removable,
// or that changed since it was placed, stays on disk and is only unpaired. Then the audio and the book go the
// way Remove always did. Any other book is Remove.
func (s *Service) RemoveWhole(ctx context.Context, userID, bookID, placedBy string, dropUpload func(ctx context.Context, fileID string) error) (RemoveResult, error) {
	parts, err := s.PartsOf(ctx, bookID, placedBy)
	if err != nil {
		return RemoveResult{}, err
	}
	if !parts.HasAudio && len(parts.Text) > 0 {
		return s.removeTextBook(ctx, userID, bookID, placedBy, parts, dropUpload)
	}
	if !parts.Combined {
		return s.Remove(ctx, userID, bookID, placedBy)
	}
	if !parts.Audio {
		return RemoveResult{Deleted: []string{}, Kept: []string{}}, fail(409, "not_removable", "Tendshelf did not add this book, so it will not remove it.")
	}
	total := RemoveResult{Deleted: []string{}, Kept: []string{}}
	for _, f := range parts.Text {
		if !f.Removable {
			continue
		}
		if f.Uploaded {
			if dropUpload != nil {
				if err := dropUpload(ctx, f.ID); err != nil {
					return total, err
				}
			}
			continue
		}
		res, err := s.RemoveEbookFile(ctx, userID, bookID, f.ID, placedBy)
		if err != nil {
			return total, err
		}
		total.Deleted = append(total.Deleted, res.Deleted...)
		total.Kept = append(total.Kept, res.Kept...)
	}
	// What is left of the text only loses its place on the book (rows only, as when a book is deleted).
	err = s.write(ctx, nil, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE ebook_files SET book_id = NULL, paired_by = NULL, book_default = 0 WHERE book_id = ?`, bookID)
		return err
	})
	if err != nil {
		return total, err
	}
	res, err := s.Remove(ctx, userID, bookID, placedBy)
	total.Deleted = append(total.Deleted, res.Deleted...)
	total.Kept = append(total.Kept, res.Kept...)
	return total, err
}

// RemoveEbookFile removes one attached library ebook file of a book that also has audio (a copy uploaded on the
// Text page is ebook.Service.RemoveUpload's): the file when it is a ledger
// file and unchanged, then its row (editions and the marks made in it go by cascade). The audio, the progress
// and the bookmarks stay. A file that changed since the upload stays, is named in Kept, and its row stays.
func (s *Service) RemoveEbookFile(ctx context.Context, userID, bookID, fileID, placedBy string) (RemoveResult, error) {
	res := RemoveResult{Deleted: []string{}, Kept: []string{}}
	book, err := s.repo.Book(ctx, bookID)
	if errors.Is(err, library.ErrNotFound) {
		return res, errNotFound
	} else if err != nil {
		return res, err
	}
	var path string
	err = s.st.Reader().QueryRowContext(ctx, `SELECT COALESCE(path,'') FROM ebook_files WHERE id = ? AND book_id = ?`, fileID, bookID).Scan(&path)
	if errors.Is(err, sql.ErrNoRows) {
		return res, errNotFound
	} else if err != nil {
		return res, err
	}
	var audio int
	if err := s.st.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM audio_files WHERE book_id = ? AND missing_since IS NULL`, bookID).Scan(&audio); err != nil {
		return res, err
	}
	var files int
	if err := s.st.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM ebook_files WHERE book_id = ?`, bookID).Scan(&files); err != nil {
		return res, err
	}
	if audio == 0 && files < 2 {
		return res, fail(409, "not_removable", "This is the only text of the book, so remove the whole book instead.")
	}
	if path == "" || !s.pathOwned(ctx, path, "") {
		return res, fail(409, "not_removable", "Tendshelf did not add this text book, so it will not remove it.")
	}
	if placedBy != "" && !s.pathOwned(ctx, path, placedBy) {
		return res, fail(409, "not_theirs", "Someone else added this text book, so only an admin can remove it.")
	}
	folder, err := s.repo.Folder(ctx, book.FolderID)
	if err != nil {
		return res, fail(409, "not_removable", "The folder this book is in is no longer part of the library.")
	}
	lr, err := openLroot(folder.Path)
	if err != nil {
		return res, err
	}
	defer lr.Close()
	uploads, err := s.uploadsOfPaths(ctx, map[string]bool{path: true})
	if err != nil {
		return res, err
	}
	gone := false
	for _, uid := range uploads {
		rows, err := s.pathsOf(ctx, uid)
		if err != nil {
			return res, err
		}
		for _, r := range rows {
			if r.Kind == kindFile && r.State == pathPresent && filepath.Clean(r.Path) == filepath.Clean(path) && within(folder.Path, r.Path) {
				gone = s.removeFile(ctx, lr, r, &res)
			}
		}
	}
	if !gone {
		if _, err := lr.Lstat(path); err != nil {
			gone = true // already gone from the disk: only the row is left
		}
	}
	if gone {
		err = s.write(ctx, nil, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `DELETE FROM ebook_files WHERE id = ? AND book_id = ?`, fileID, bookID); err != nil {
				return err
			}
			return s.audit(ctx, tx, userID, "upload.remove_text", "book:"+bookID, map[string]any{"deleted": len(res.Deleted), "title": book.Title})
		})
	}
	return res, err
}

// RemoveAudio removes the audio of a combined book and leaves its text. split runs first and must take the
// book's ebook files off it (ebook.Service.SplitText), so each becomes a text book with its reading place,
// highlights and bookmarks; the ledger files of those ebooks are then left alone. The audiobook goes the way a
// whole-book removal goes: its listening progress and bookmarks go with it.
func (s *Service) RemoveAudio(ctx context.Context, userID, bookID, placedBy string, split func(context.Context) error) (RemoveResult, error) {
	parts, err := s.PartsOf(ctx, bookID, placedBy)
	if err != nil {
		return RemoveResult{}, err
	}
	if !parts.Combined || !parts.Audio {
		return RemoveResult{Deleted: []string{}, Kept: []string{}}, fail(409, "not_removable", "Tendshelf did not add the audio of this book, so it will not remove it.")
	}
	keep := map[string]bool{}
	for _, f := range parts.Text {
		if f.Path != "" {
			keep[f.Path] = true
		}
	}
	if err := split(ctx); err != nil {
		return RemoveResult{}, err
	}
	return s.remove(ctx, userID, bookID, placedBy, keep)
}

// removeTextBook removes a book that has no audio: every stored Text-page upload goes through dropUpload (the same
// ebook.Service.RemoveUpload one edition uses), and ledger ebook files through Remove, which also deletes the book.
// A book of stored uploads only is deleted here, with what cascades from it (progress, bookmarks, shelves,
// metadata). Nothing is removed unless every file of the book is Tendshelf's.
func (s *Service) removeTextBook(ctx context.Context, userID, bookID, placedBy string, parts Parts, dropUpload func(ctx context.Context, fileID string) error) (RemoveResult, error) {
	res := RemoveResult{Deleted: []string{}, Kept: []string{}}
	if !parts.Whole {
		return res, fail(409, "not_removable", "Tendshelf did not add this book, so it will not remove it.")
	}
	book, err := s.repo.Book(ctx, bookID)
	if errors.Is(err, library.ErrNotFound) {
		return res, errNotFound
	} else if err != nil {
		return res, err
	}
	ledger := 0
	for _, f := range parts.Text {
		if f.Uploaded {
			if dropUpload == nil {
				return res, fail(409, "not_removable", "This book cannot be removed here.")
			}
			if err := dropUpload(ctx, f.ID); err != nil {
				return res, err
			}
		} else {
			ledger++
		}
	}
	if ledger > 0 {
		r, err := s.Remove(ctx, userID, bookID, placedBy)
		return r, err
	}
	if err := s.repo.Update(ctx, func(tx *library.Tx) error { return tx.DeleteBookAndFiles(bookID) }); err != nil {
		return res, err
	}
	err = s.write(ctx, nil, func(tx *sql.Tx) error {
		return s.audit(ctx, tx, userID, "upload.remove", "book:"+bookID, map[string]any{"deleted": 0, "kept": 0, "title": book.Title})
	})
	return res, err
}
