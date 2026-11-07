package upload

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
)

// Placement says where an upload of one person is built (decision 0029).
type Placement int

// The placements. The zero value is PlaceRefused, so a callback that forgets to answer places nothing; a
// nil Config.MayPlace means PlaceTop.
const (
	PlaceRefused   Placement = iota // the person may no longer add books here: nothing is placed
	PlaceTop                        // the top of the upload folder (owner and admin)
	PlaceOwnFolder                  // the person's own folder inside it (a contributor)
)

// personPrefix starts the name of a contributor's folder.
const personPrefix = "Added by "

// personUploadID is the upload_id of the ledger row of a person's folder. It matches no upload, so the sweep
// and "Remove this book" never touch the row, and the folder is never deleted by Tendshelf.
func personUploadID(userID string) string { return "contrib-" + userID }

func isDirNoLink(fi fs.FileInfo) bool { return fi.IsDir() && fi.Mode()&fs.ModeSymlink == 0 }

// personFolder returns the path of the person's own folder inside the upload folder lr, creating it when
// the person has none yet. The folder is chosen once per person and upload folder and stored in
// contributor_folders; later changes of the display name do not rename it. A new name is "Added by "
// plus SafeName(name), then " (2)" to " (99)" until nothing of that name exists: an existing directory
// is never adopted. The ledger row is written before the directory is made, like every path here.
func (s *Service) personFolder(ctx context.Context, g guard, lr *lroot, folderID, userID, name string) (string, error) {
	for range 3 { // a lost race re-reads the row the winner wrote
		var stored string
		err := s.st.Reader().QueryRowContext(ctx, `SELECT path FROM contributor_folders WHERE folder_id = ? AND user_id = ?`,
			folderID, userID).Scan(&stored)
		switch {
		case err == nil:
			return s.useStoredFolder(lr, stored)
		case !errors.Is(err, sql.ErrNoRows):
			return "", err
		}
		p, raced, err := s.newPersonFolder(ctx, g, lr, folderID, userID, name)
		if err != nil || !raced {
			return p, err
		}
	}
	return "", personLayoutError()
}

func personLayoutError() error {
	return &jobs.Error{Code: "layout", Text: "Tendshelf could not find a free place for this person's folder in the library folder.", Permanent: true}
}

// useStoredFolder checks the stored folder: a real directory is used; one that is gone is made again with
// a plain Mkdir; anything else in its place (a file, a link) is an error and nothing is adopted.
func (s *Service) useStoredFolder(lr *lroot, p string) (string, error) {
	if p == lr.path || !within(lr.path, p) {
		return "", fmt.Errorf("upload: %q is outside the upload folder", p)
	}
	fi, err := lr.Lstat(p)
	switch {
	case err == nil && isDirNoLink(fi):
		return p, nil
	case err == nil:
		return "", &jobs.Error{Code: "layout", Text: "Something else is now where this person's folder was. Move it away, then press Try again.", Permanent: true}
	case !errors.Is(err, fs.ErrNotExist):
		return "", err
	}
	if err := lr.Mkdir(p, 0o755); err != nil {
		return "", err
	}
	return p, nil
}

// newPersonFolder picks a free name, records it and makes the directory. raced is true when another
// placement stored a folder for the same person first.
func (s *Service) newPersonFolder(ctx context.Context, g guard, lr *lroot, folderID, userID, name string) (p string, raced bool, err error) {
	base := cutName(personPrefix + SafeName(name, "a contributor"))
	for n := 1; n <= 99; n++ {
		leaf := base
		if n > 1 {
			leaf = suffixed(base, n)
		}
		cand := filepath.Join(lr.path, leaf)
		if !within(lr.path, cand) || cand == lr.path {
			return "", false, personLayoutError()
		}
		if _, lerr := lr.Lstat(cand); lerr == nil {
			continue
		} else if !errors.Is(lerr, fs.ErrNotExist) {
			return "", false, lerr
		}
		if !usableAncestor(lr, cand, nil) {
			continue
		}
		rowID := db.NewID("upp")
		now := s.nowMs()
		werr := s.write(ctx, g, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, `INSERT INTO upload_paths(id, upload_id, library_folder_id, path, kind, state, created_at, updated_at)
  VALUES (?,?,?,?,?,?,?,?)`, rowID, personUploadID(userID), folderID, cand, kindDir, pathPlanned, now, now); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO contributor_folders(folder_id, user_id, path, created_at) VALUES (?,?,?,?)`,
				folderID, userID, cand, now)
			return err
		})
		if db.IsUniqueViolation(werr) {
			var one int
			if qerr := s.st.Reader().QueryRowContext(ctx, `SELECT 1 FROM contributor_folders WHERE folder_id = ? AND user_id = ?`,
				folderID, userID).Scan(&one); qerr == nil {
				return "", true, nil
			}
			continue // the name is claimed by someone else's row
		}
		if werr != nil {
			return "", false, werr
		}
		if merr := lr.Mkdir(cand, 0o755); merr != nil {
			if derr := s.dropPersonFolder(ctx, g, rowID, folderID, userID); derr != nil {
				return "", false, derr
			}
			if errors.Is(merr, fs.ErrExist) {
				continue // made by someone between the check and the call; never adopted
			}
			return "", false, merr
		}
		return cand, false, s.markPath(ctx, g, rowID, pathPresent)
	}
	return "", false, personLayoutError()
}

// dropPersonFolder removes the two rows of a folder that was never created.
func (s *Service) dropPersonFolder(ctx context.Context, g guard, rowID, folderID, userID string) error {
	return s.write(ctx, g, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM upload_paths WHERE id = ?`, rowID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM contributor_folders WHERE folder_id = ? AND user_id = ?`, folderID, userID)
		return err
	})
}
