package upload

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"syscall"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

// RemoveResult lists what "Remove this book" did: the files it deleted and the ones it kept
// because they changed since the upload placed them.
type RemoveResult struct {
	Deleted []string `json:"deleted"`
	Kept    []string `json:"kept"`
}

// Removable reports whether every live audio file of the book is a file this package placed (a
// present ledger row), and the book has at least one. Only then is "Remove this book" offered.
func (s *Service) Removable(ctx context.Context, bookID string) bool {
	var live, owned int
	err := s.st.Reader().QueryRowContext(ctx, `SELECT COUNT(*),
  COALESCE(SUM(EXISTS (SELECT 1 FROM upload_paths p WHERE p.path = a.path AND p.kind = 'file' AND p.state = 'present')), 0)
  FROM audio_files a WHERE a.book_id = ? AND a.missing_since IS NULL`, bookID).Scan(&live, &owned)
	return err == nil && live > 0 && live == owned
}

// Remove deletes the files this package placed for a book, and then the book. This is the only
// delete path (decision 0015, point 11). A file is deleted only when its ledger row is present and
// its size, mtime and quick hash still match what was recorded; any other file stays and is named.
// Folders the ledger created are removed deepest first, only when empty. The book's rows (and, by
// cascade, everyone's progress, bookmarks and queue entries for it) go only when no audio file of
// it is left on disk; otherwise a scoped scan brings the library in line.
func (s *Service) Remove(ctx context.Context, userID, bookID string) (RemoveResult, error) {
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
	folder, err := s.repo.Folder(ctx, book.FolderID)
	if err != nil {
		return res, fail(409, "not_removable", "The folder this book is in is no longer part of the library.")
	}
	audio, err := s.repo.AudioFiles(ctx, bookID)
	if err != nil {
		return res, err
	}
	audioPaths := map[string]bool{}
	for _, a := range audio {
		audioPaths[a.Path] = true
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
				if s.removeFile(ctx, r, &res) {
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
		err := os.Remove(d.Path) // rmdir: fails when anything is inside
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
		if _, err := os.Lstat(a.Path); err == nil {
			left = true
			break
		}
	}
	detail := map[string]any{"deleted": len(res.Deleted), "kept": len(res.Kept), "title": book.Title}
	if !left {
		err = s.repo.Update(ctx, func(tx *library.Tx) error { return tx.DeleteBookAndFiles(bookID) })
		if err != nil {
			return res, err
		}
		err = s.write(ctx, nil, func(tx *sql.Tx) error { return s.audit(ctx, tx, userID, "upload.remove", "book:"+bookID, detail) })
		return res, err
	}
	sub := book.RootPath
	if fi, err := os.Lstat(sub); err != nil || !fi.IsDir() {
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
func (s *Service) removeFile(ctx context.Context, r pathRow, res *RemoveResult) bool {
	fi, err := os.Lstat(r.Path)
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
		quick, _, qerr := media.QuickHash(r.Path)
		unchanged = qerr == nil && quick == r.QuickHash
	}
	if !unchanged {
		res.Kept = append(res.Kept, r.Path)
		_ = s.markPath(ctx, nil, r.ID, pathKept)
		return false
	}
	if err := os.Remove(r.Path); err != nil {
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
