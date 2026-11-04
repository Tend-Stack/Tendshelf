package upload

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
)

// recoverPlacement brings an upload back to a known state after a crash or a retry (step a). It
// reads the ledger and the file system, through the library root, and never deletes a path it
// cannot prove it created:
//
//   - a recorded hidden build folder still there means the rename did not happen: it is removed
//     (its exact name and place are checked) and the upload starts over from the staged bytes;
//   - a recorded build folder gone and the recorded book folder there means the rename happened but
//     the database did not hear: the files are checked against the ledger and the upload is
//     committed (published is true). With no staged rows at all there is nothing to commit; the
//     recorded paths are cleared and the upload starts over, so a retry never loops on it;
//   - planned rows whose path does not exist are deleted; a planned path that exists is kept (it
//     may not be ours to remove) and never touched again.
func (s *Service) recoverPlacement(ctx context.Context, g guard, uploadID string, lr *lroot) (published bool, err error) {
	up, err := s.loadUpload(ctx, uploadID)
	if err != nil {
		return false, err
	}
	if up.TempPath != "" {
		_, terr := lr.Lstat(up.TempPath)
		switch {
		case terr == nil:
			s.removeBuild(ctx, g, uploadID, lr, up.TempPath)
			if _, still := lr.Lstat(up.TempPath); still == nil {
				return false, &jobs.Error{Code: "recover", Text: "Tendshelf could not clean up an unfinished upload. Check the server log.", Permanent: true}
			}
		case !errors.Is(terr, fs.ErrNotExist):
			return false, terr
		}
		if terr != nil && up.BookPath != "" && within(lr.path, up.BookPath) {
			if fi, err := lr.Lstat(up.BookPath); err == nil && fi.IsDir() && fi.Mode()&fs.ModeSymlink == 0 {
				n, ok, err := s.sizesMatch(ctx, lr, uploadID, up.BookPath)
				if err != nil {
					return false, err
				}
				if n > 0 && !ok {
					return false, s.jobFail(ctx, g, uploadID, "recover", "The book folder does not match what was uploaded. Nothing was changed.", true, nil)
				}
				if n > 0 {
					if err := s.commitPublish(ctx, g, uploadID, up.CreatedBy, up.BookPath, up.TempPath); err != nil {
						return false, err
					}
					return true, nil
				}
			}
		}
		if err := s.write(ctx, g, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE uploads SET temp_path = NULL, book_path = NULL WHERE id = ?`, uploadID)
			return err
		}); err != nil {
			return false, err
		}
	}
	rows, err := s.pathsOf(ctx, uploadID)
	if err != nil {
		return false, err
	}
	for _, r := range rows {
		if r.State != pathPlanned && r.State != pathStaged {
			continue
		}
		if _, err := lr.Lstat(r.Path); errors.Is(err, fs.ErrNotExist) {
			if err := s.dropPath(ctx, g, r.ID); err != nil {
				return false, err
			}
		} else if err == nil {
			if err := s.markPath(ctx, g, r.ID, pathKept); err != nil {
				return false, err
			}
		} else {
			return false, err
		}
	}
	return false, nil
}

// sizesMatch counts the staged file rows of the book and reports whether every one exists at its
// path, as a regular file, with its recorded size.
func (s *Service) sizesMatch(ctx context.Context, lr *lroot, uploadID, book string) (n int, ok bool, err error) {
	rows, err := s.pathsOf(ctx, uploadID)
	if err != nil {
		return 0, false, err
	}
	ok = true
	for _, r := range rows {
		if r.Kind != kindFile || r.State != pathStaged || !within(book, r.Path) {
			continue
		}
		n++
		fi, err := lr.Lstat(r.Path)
		if err != nil || !fi.Mode().IsRegular() || fi.Size() != r.Size {
			ok = false
		}
	}
	return n, ok, nil
}
