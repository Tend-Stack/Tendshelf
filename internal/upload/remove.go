package upload

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/scan"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// RemoveResult lists what "Remove this book" did: the files it deleted and the ones it kept
// because they changed since the upload placed them.
type RemoveResult struct {
	Deleted []string `json:"deleted"`
	Kept    []string `json:"kept"`
}

// Removable reports whether every live audio file and every live library ebook file of the book is a
// file this package placed (a present ledger row), and the book has at least one. Only then is
// "Remove this book" offered.
func (s *Service) Removable(ctx context.Context, bookID string) bool {
	var live, owned int
	err := s.st.Reader().QueryRowContext(ctx, `SELECT COUNT(*), COALESCE(SUM(EXISTS (SELECT 1 FROM upload_paths p
  WHERE p.path = a.path AND p.kind = 'file' AND p.state = 'present')), 0) FROM (
    SELECT path FROM audio_files WHERE book_id = ?1 AND missing_since IS NULL
    UNION ALL
    SELECT path FROM ebook_files WHERE book_id = ?1 AND path IS NOT NULL AND missing_since IS NULL) a`, bookID).Scan(&live, &owned)
	return err == nil && live > 0 && live == owned
}

// RemovableBy is Removable and, in addition, every live audio and library ebook file of the book lies inside a
// book root that one of userID's uploads published (upload_books): a contributor removes only what they placed.
func (s *Service) RemovableBy(ctx context.Context, bookID, userID string) bool {
	if !s.Removable(ctx, bookID) {
		return false
	}
	theirs, err := s.allFilesAddedBy(ctx, bookID, userID)
	return err == nil && theirs
}

// allFilesAddedBy reports whether no live audio or library ebook file of the book lies outside userID's
// upload_books roots.
func (s *Service) allFilesAddedBy(ctx context.Context, bookID, userID string) (bool, error) {
	var outside int
	err := s.st.Reader().QueryRowContext(ctx, `SELECT COUNT(*) FROM (
    SELECT path AS root_path FROM audio_files WHERE book_id = ? AND missing_since IS NULL
    UNION ALL
    SELECT path AS root_path FROM ebook_files WHERE book_id = ? AND path IS NOT NULL AND missing_since IS NULL) a
  WHERE NOT `+share.AddedBySQL("a"), bookID, bookID, userID).Scan(&outside)
	return err == nil && outside == 0, err
}

// ebookPaths lists the library paths of the book's ebook files, missing ones included.
func (s *Service) ebookPaths(ctx context.Context, bookID string) ([]string, error) {
	rows, err := s.st.Reader().QueryContext(ctx, `SELECT path FROM ebook_files WHERE book_id = ? AND path IS NOT NULL ORDER BY path`, bookID)
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

// Remove deletes the files this package placed for a book, and then the book. This is the only
// delete path (decision 0015, point 11). A file is deleted only when its ledger row is present and
// its size, mtime and quick hash still match what was recorded; any other file stays and is named.
// Folders the ledger created are removed deepest first, only when empty. The book's rows (and, by
// cascade, everyone's progress, bookmarks and queue entries for it) go only when no audio file of
// it is left on disk; otherwise a scoped scan brings the library in line.
//
// placedBy, when set, limits the removal to one person's books (decision 0029): every live audio and
// library ebook file must lie inside a root that person's uploads published, otherwise 409 not_theirs and
// nothing is touched. A contributor always removes with placedBy set to themselves; owner and admin pass "".
func (s *Service) Remove(ctx context.Context, userID, bookID, placedBy string) (RemoveResult, error) {
	res := RemoveResult{Deleted: []string{}, Kept: []string{}}
	book, err := s.repo.Book(ctx, bookID)
	if errors.Is(err, library.ErrNotFound) {
		return res, errNotFound
	} else if err != nil {
		return res, err
	}
	if !s.Removable(ctx, bookID) {
		return res, fail(409, "not_removable", "Tendshelf did not add this book, so it will not remove it.")
	}
	if placedBy != "" {
		if theirs, err := s.allFilesAddedBy(ctx, bookID, placedBy); err != nil {
			return res, err
		} else if !theirs {
			return res, fail(409, "not_theirs", "Someone else added part of this book, so only an admin can remove it.")
		}
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
	audio, err := s.repo.AudioFiles(ctx, bookID)
	if err != nil {
		return res, err
	}
	audioPaths := map[string]bool{}
	for _, a := range audio {
		audioPaths[a.Path] = true
	}
	ebooks, err := s.ebookPaths(ctx, bookID)
	if err != nil {
		return res, err
	}
	for _, p := range ebooks {
		audioPaths[p] = true
	}
	uploads, err := s.uploadsOfPaths(ctx, audioPaths)
	if err != nil {
		return res, err
	}

	var dirs []pathRow
	for _, uid := range uploads {
		rows, err := s.pathsOf(ctx, uid)
		if err != nil {
			return res, err
		}
		for _, r := range rows {
			if r.State != pathPresent || !within(folder.Path, r.Path) || filepath.Clean(r.Path) == filepath.Clean(folder.Path) {
				continue
			}
			switch r.Kind {
			case kindFile:
				if !within(book.RootPath, r.Path) {
					continue
				}
				if s.removeFile(ctx, lr, r, &res) {
					continue
				}
			case kindDir:
				if within(book.RootPath, r.Path) || within(r.Path, book.RootPath) {
					dirs = append(dirs, r)
				}
			}
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i].Path) > len(dirs[j].Path) })
	for _, d := range dirs {
		err := lr.Remove(d.Path) // rmdir: fails when anything is inside
		switch {
		case err == nil || errors.Is(err, fs.ErrNotExist):
			_ = s.markPath(ctx, nil, d.ID, pathRemoved)
		case errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, fs.ErrExist):
			_ = s.markPath(ctx, nil, d.ID, pathKept)
		default:
			s.log.Warn("upload: cannot remove folder", "path", d.Path, "err", err)
		}
	}

	left := false
	for _, a := range audio {
		if _, err := lr.Lstat(a.Path); err == nil {
			left = true
			break
		}
	}
	// An ebook row whose file is gone is deleted here, before the book row, so the ebook service cannot
	// make a new book from it. One that is still on disk (changed since the upload) stays and keeps the book.
	var gone []string
	for _, p := range ebooks {
		if _, err := lr.Lstat(p); err == nil {
			left = true
		} else {
			gone = append(gone, p)
		}
	}
	if len(gone) > 0 {
		err := s.write(ctx, nil, func(tx *sql.Tx) error {
			for _, p := range gone {
				if _, err := tx.ExecContext(ctx, `DELETE FROM ebook_files WHERE book_id = ? AND path = ?`, bookID, p); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return res, err
		}
	}
	detail := map[string]any{"deleted": len(res.Deleted), "kept": len(res.Kept), "title": book.Title}
	if !left {
		err = s.repo.Update(ctx, func(tx *library.Tx) error { return tx.DeleteBookAndFiles(bookID) })
		if err != nil {
			return res, err
		}
		// The record of who added it goes with the book: its root and anything inside it.
		err = s.write(ctx, nil, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `DELETE FROM upload_books WHERE root_path = ?1 OR (root_path > ?1 || '/' AND root_path < ?1 || '0')`, book.RootPath)
			return err
		})
		if err != nil {
			return res, err
		}
		err = s.write(ctx, nil, func(tx *sql.Tx) error { return s.audit(ctx, tx, userID, "upload.remove", "book:"+bookID, detail) })
		return res, err
	}
	sub := book.RootPath
	if fi, err := lr.Lstat(sub); err != nil || !fi.IsDir() {
		sub = filepath.Dir(sub)
	}
	if rel, err := filepath.Rel(folder.Path, sub); err == nil && within(folder.Path, sub) && rel != "." {
		if _, err := s.enq.Enqueue(ctx, scan.KindScan, scan.ScanPayload{LibraryID: book.LibraryID, FolderID: folder.ID, Subdir: filepath.ToSlash(rel)},
			jobs.EnqueueOptions{CreatedBy: userID}); err != nil {
			s.log.Warn("upload: cannot queue the scan after a remove", "err", err)
		}
	}
	err = s.write(ctx, nil, func(tx *sql.Tx) error { return s.audit(ctx, tx, userID, "upload.remove", "book:"+bookID, detail) })
	return res, err
}

// removeFile deletes one ledger file when it is unchanged. It reports whether the file is gone.
func (s *Service) removeFile(ctx context.Context, lr *lroot, r pathRow, res *RemoveResult) bool {
	fi, err := lr.Lstat(r.Path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		_ = s.markPath(ctx, nil, r.ID, pathRemoved)
		return true
	case err != nil:
		res.Kept = append(res.Kept, r.Path)
		return false
	}
	unchanged := fi.Mode().IsRegular() && fi.Size() == r.Size && fi.ModTime().UnixNano() == r.MtimeNs
	if unchanged {
		unchanged = false
		if f, oerr := lr.Open(r.Path); oerr == nil {
			quick, _, qerr := media.QuickHashFile(f)
			f.Close()
			unchanged = qerr == nil && quick == r.QuickHash
		}
	}
	if !unchanged {
		res.Kept = append(res.Kept, r.Path)
		_ = s.markPath(ctx, nil, r.ID, pathKept)
		return false
	}
	if err := lr.Remove(r.Path); err != nil {
		res.Kept = append(res.Kept, r.Path)
		return false
	}
	_ = s.markPath(ctx, nil, r.ID, pathRemoved)
	res.Deleted = append(res.Deleted, r.Path)
	return true
}

// uploadsOfPaths finds the uploads whose present file rows name these paths.
func (s *Service) uploadsOfPaths(ctx context.Context, paths map[string]bool) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for p := range paths {
		rows, err := s.st.Reader().QueryContext(ctx, `SELECT DISTINCT upload_id FROM upload_paths WHERE path = ? AND kind = 'file' AND state = 'present'`, p)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return nil, err
			}
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
		if err := rows.Close(); err != nil {
			return nil, err
		}
	}
	sort.Strings(out)
	return out, nil
}
