package upload

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

// Ledger states (upload_paths.state) and kinds.
const (
	pathPlanned = "planned"
	pathStaged  = "staged"
	pathPresent = "present"
	pathRemoved = "removed"
	pathKept    = "kept"

	kindDir  = "dir"
	kindFile = "file"
)

// errPathTaken means another live ledger row already names the path (the partial unique index).
var errPathTaken = errors.New("upload: path already in the ledger")

// pathRow is one upload_paths row.
type pathRow struct {
	ID, UploadID, FolderID, Path, Kind, State string
	Size, MtimeNs                             int64
	QuickHash, SHA256                         string
}

const pathCols = `id, upload_id, library_folder_id, path, kind, state, COALESCE(size,0), COALESCE(mtime_ns,0),
  COALESCE(quick_hash,''), COALESCE(sha256,'')`

func scanPath(r rowScanner) (pathRow, error) {
	var p pathRow
	err := r.Scan(&p.ID, &p.UploadID, &p.FolderID, &p.Path, &p.Kind, &p.State, &p.Size, &p.MtimeNs, &p.QuickHash, &p.SHA256)
	return p, err
}

// plan records a path the package is about to create. It must be committed before the path exists;
// the caller passes a path already checked with within. A path another live row names is
// errPathTaken and nothing is written.
func (s *Service) plan(ctx context.Context, g guard, uploadID, folderID, path, kind, state string) (string, error) {
	id := db.NewID("upp")
	now := s.nowMs()
	err := s.write(ctx, g, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO upload_paths(id, upload_id, library_folder_id, path, kind, state, created_at, updated_at)
  VALUES (?,?,?,?,?,?,?,?)`, id, uploadID, folderID, path, kind, state, now, now)
		return err
	})
	if db.IsUniqueViolation(err) {
		return "", errPathTaken
	}
	return id, err
}

// markPath changes a row's state, and records what the file looked like when it is not zero.
func (s *Service) markPath(ctx context.Context, g guard, id, state string) error {
	return s.write(ctx, g, func(tx *sql.Tx) error { return s.markPathTx(ctx, tx, id, state) })
}

func (s *Service) markPathTx(ctx context.Context, tx *sql.Tx, id, state string) error {
	_, err := tx.ExecContext(ctx, `UPDATE upload_paths SET state = ?, updated_at = ? WHERE id = ?`, state, s.nowMs(), id)
	return err
}

// describePath stores the size, mtime and hashes of a placed file.
func (s *Service) describePath(ctx context.Context, g guard, id string, size, mtimeNs int64, quick, sha string) error {
	return s.write(ctx, g, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE upload_paths SET size = ?, mtime_ns = ?, quick_hash = ?, sha256 = ?, updated_at = ? WHERE id = ?`,
			size, mtimeNs, nullIfEmpty(quick), nullIfEmpty(sha), s.nowMs(), id)
		return err
	})
}

// dropPath deletes a row for a path that was never created.
func (s *Service) dropPath(ctx context.Context, g guard, id string) error {
	return s.write(ctx, g, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM upload_paths WHERE id = ?`, id)
		return err
	})
}

func (s *Service) pathsOf(ctx context.Context, uploadID string) ([]pathRow, error) {
	rows, err := s.st.Reader().QueryContext(ctx, `SELECT `+pathCols+` FROM upload_paths WHERE upload_id = ? ORDER BY created_at, id`, uploadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []pathRow
	for rows.Next() {
		p, err := scanPath(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
