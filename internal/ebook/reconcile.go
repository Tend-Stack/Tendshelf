package ebook

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

// underAny reports whether path is one of prefixes or below one of them.
func underAny(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if path == p || strings.HasPrefix(path, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// op is one database change decided outside the write transaction.
type op func(ctx context.Context, tx *sql.Tx) error

// Reconcile brings the library's ebook rows in line with what a scan saw: new files become pending,
// changed files are read again, moved files keep their row, vanished files are marked missing, and
// unpaired files are paired by folder. Files are only hashed (read-only); errors are logged and the
// rest goes on.
func (s *Service) Reconcile(ctx context.Context, es scan.EbookScan) error {
	seen := map[string]bool{}
	for _, fo := range es.Folders {
		for _, f := range fo.Files {
			seen[f.Path] = true
		}
	}
	now := s.now().UnixMilli()
	var ops []op
	moved := map[string]bool{} // row ids already claimed by a moved file

	for _, fo := range es.Folders {
		rows, err := s.libraryRows(ctx, fo.FolderID)
		if err != nil {
			return err
		}
		byPath := map[string]File{}
		for _, r := range rows {
			byPath[r.Path] = r
		}
		for _, f := range fo.Files {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if r, ok := byPath[f.Path]; ok {
				if r.Size == f.Size && r.MtimeNS == f.MtimeNs {
					if r.MissingSince != 0 {
						id := r.ID
						ops = append(ops, func(ctx context.Context, tx *sql.Tx) error {
							_, err := tx.ExecContext(ctx, `UPDATE ebook_files SET missing_since=NULL, updated_at=? WHERE id=?`, now, id)
							return err
						})
					}
					continue
				}
				qh, size, err := media.QuickHash(f.Path)
				if err != nil {
					s.log.Warn("ebooks: could not hash a file", "err", err)
					continue
				}
				id, same := r.ID, qh == r.QuickHash && size == r.Size
				mtime := f.MtimeNs
				ops = append(ops, func(ctx context.Context, tx *sql.Tx) error {
					if same {
						_, err := tx.ExecContext(ctx, `UPDATE ebook_files SET mtime_ns=?, missing_since=NULL, updated_at=? WHERE id=?`, mtime, now, id)
						return err
					}
					_, err := tx.ExecContext(ctx, `UPDATE ebook_files SET quick_hash=?, size=?, mtime_ns=?, sha256=NULL, format=NULL, partial_md5=NULL,
state='pending', refused_reason=NULL, error_code=NULL, job_id=NULL, missing_since=NULL, updated_at=? WHERE id=?`,
						qh, size, mtime, now, id)
					return err
				})
				continue
			}
			qh, size, err := media.QuickHash(f.Path)
			if err != nil {
				s.log.Warn("ebooks: could not hash a file", "err", err)
				continue
			}
			ext := ExtOf(f.Name)
			if ext == "" {
				continue
			}
			// A file with the same bytes whose old row is missing or no longer seen was moved.
			old, found, err := s.movedFrom(ctx, es.LibraryID, qh, size, seen, moved)
			if err != nil {
				return err
			}
			p, fid, fpath, mt := f.Path, fo.FolderID, f.Path, f.MtimeNs
			if found {
				moved[old] = true
				ops = append(ops, func(ctx context.Context, tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, `UPDATE ebook_files SET path=?, folder_id=?, mtime_ns=?, missing_since=NULL,
original_name=?, updated_at=? WHERE id=?`, p, fid, mt, filepath.Base(fpath), now, old)
					return err
				})
				continue
			}
			id, name, sz := db.NewID("ebf"), filepath.Base(f.Path), size
			lib := es.LibraryID
			ops = append(ops, func(ctx context.Context, tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `INSERT INTO ebook_files(id, origin, library_id, folder_id, path, ext, size, mtime_ns,
quick_hash, state, original_name, created_at, updated_at) VALUES(?, 'library', ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?, ?)`,
					id, lib, fid, p, ext, sz, mt, qh, name, now, now)
				return err
			})
		}
		// Rows no longer seen in a walked folder.
		for _, r := range rows {
			if seen[r.Path] || moved[r.ID] || r.MissingSince != 0 || underAny(r.Path, es.Skip) {
				continue
			}
			if es.Scope != "" && !underAny(r.Path, []string{es.Scope}) {
				continue
			}
			id := r.ID
			ops = append(ops, func(ctx context.Context, tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `UPDATE ebook_files SET missing_since=?, updated_at=? WHERE id=? AND missing_since IS NULL`, now, now, id)
				return err
			})
		}
	}
	if len(ops) > 0 {
		err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
			for _, o := range ops {
				if err := o(ctx, tx); err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if err := s.syncTextBooks(ctx, es.LibraryID); err != nil {
		s.log.Warn("ebooks: could not update text books", "err", err)
	}
	if err := s.pairByFolder(ctx, es.LibraryID); err != nil {
		s.log.Warn("ebooks: pairing failed", "err", err)
	}
	return nil
}

func (s *Service) libraryRows(ctx context.Context, folderID string) ([]File, error) {
	rs, err := s.db.Reader().QueryContext(ctx, `SELECT `+fileCols+` FROM ebook_files WHERE folder_id = ? ORDER BY path`, folderID)
	if err != nil {
		return nil, err
	}
	defer rs.Close()
	var out []File
	for rs.Next() {
		f, err := scanFile(rs)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rs.Err()
}

// movedFrom finds the row of a library file with these bytes that is missing or was not seen in this
// scan, and is not yet claimed.
func (s *Service) movedFrom(ctx context.Context, libraryID, qh string, size int64, seen, claimed map[string]bool) (string, bool, error) {
	rs, err := s.db.Reader().QueryContext(ctx, `SELECT id, path, COALESCE(missing_since,0) FROM ebook_files
WHERE library_id=? AND origin='library' AND quick_hash=? AND size=? ORDER BY id`, libraryID, qh, size)
	if err != nil {
		return "", false, err
	}
	defer rs.Close()
	for rs.Next() {
		var id, p string
		var missing int64
		if err := rs.Scan(&id, &p, &missing); err != nil {
			return "", false, err
		}
		if claimed[id] {
			continue
		}
		if missing != 0 || !seen[p] {
			return id, true, nil
		}
	}
	return "", false, rs.Err()
}

// folderBooks maps each directory that holds a live audiobook to the audiobooks there. A single-file book
// sits in its directory. Text books are not listed: a folder rule pairs files with audio only.
func (s *Service) folderBooks(ctx context.Context, libraryID string) (map[string][]string, error) {
	br, err := s.db.Reader().QueryContext(ctx, `SELECT id, root_path FROM books WHERE library_id=? AND kind='audio' AND missing_since IS NULL ORDER BY root_path`, libraryID)
	if err != nil {
		return nil, err
	}
	defer br.Close()
	byDir := map[string][]string{} // directory holding the book -> book ids
	for br.Next() {
		var id, root string
		if err := br.Scan(&id, &root); err != nil {
			return nil, err
		}
		key := root
		if k := media.Classify(root); k == media.KindAudio || k == media.KindProtected {
			key = filepath.Dir(root) // a single-file book sits in its directory
		}
		byDir[key] = append(byDir[key], id)
	}
	return byDir, br.Err()
}

// folderBook is the one audiobook whose folder holds the ebook at path, or whose single audio file sits
// beside it; failing that, the one audiobook of the parent folder. A PDF uses the same-folder rule only:
// a PDF in a parent folder is as likely a manual or a scan as the book's text.
func folderBook(byDir map[string][]string, path, ext string) (string, bool) {
	dir := filepath.Dir(path)
	ids := byDir[dir]
	if len(ids) == 0 && ext != "pdf" {
		ids = byDir[filepath.Dir(dir)]
	}
	if len(ids) != 1 {
		return "", false
	}
	return ids[0], true
}

// pairByFolder pairs unpaired library ebooks with the one live audiobook whose folder holds them, or whose
// single audio file sits beside them; failing that, with the one audiobook of the parent folder. It never
// unpairs and never overrides a pairing made another way.
func (s *Service) pairByFolder(ctx context.Context, libraryID string) error {
	rs, err := s.db.Reader().QueryContext(ctx, `SELECT id, path, ext FROM ebook_files
WHERE library_id=? AND origin='library' AND book_id IS NULL AND hold IS NULL ORDER BY path`, libraryID)
	if err != nil {
		return err
	}
	type pend struct{ id, path, ext string }
	var todo []pend
	for rs.Next() {
		var p pend
		if err := rs.Scan(&p.id, &p.path, &p.ext); err != nil {
			rs.Close()
			return err
		}
		todo = append(todo, p)
	}
	rs.Close()
	if len(todo) == 0 {
		return nil
	}
	byDir, err := s.folderBooks(ctx, libraryID)
	if err != nil {
		return err
	}
	for _, p := range todo {
		id, ok := folderBook(byDir, p.path, p.ext)
		if !ok {
			continue
		}
		if _, err := s.db.Writer().ExecContext(ctx, `UPDATE ebook_files SET book_id=?, paired_by='folder', updated_at=?
WHERE id=? AND book_id IS NULL AND hold IS NULL
AND NOT EXISTS (SELECT 1 FROM ebook_not_same WHERE ebook_file_id=? AND book_id=?)`, id, s.now().UnixMilli(), p.id, p.id, id); err != nil {
			return err
		}
	}
	return nil
}

// enqueue queues the read of one file and records the job id on the row.
func (s *Service) enqueue(ctx context.Context, fileID, createdBy string) error {
	id, err := s.runner.Enqueue(ctx, KindIngest, IngestPayload{FileID: fileID}, jobs.EnqueueOptions{CreatedBy: createdBy})
	if err != nil {
		return err
	}
	_, err = s.db.Writer().ExecContext(ctx, `UPDATE ebook_files SET job_id=? WHERE id=?`, id, fileID)
	return err
}

// EnqueuePending queues a read for every pending file that has no waiting job. Files refused with the
// code not_yet (a parser that did not exist yet) are reset to pending first, so a newer build reads
// them.
func (s *Service) EnqueuePending(ctx context.Context) error {
	if err := s.EnqueueStale(ctx); err != nil {
		return err
	}
	if _, err := s.db.Writer().ExecContext(ctx, `UPDATE ebook_files SET state='pending', refused_reason=NULL, error_code=NULL,
updated_at=? WHERE state='refused' AND error_code='not_yet'`, s.now().UnixMilli()); err != nil {
		return err
	}
	active, err := s.runner.ActiveKeys(ctx, KindIngest, "file_id")
	if err != nil {
		return err
	}
	rs, err := s.db.Reader().QueryContext(ctx, `SELECT id FROM ebook_files WHERE state='pending' ORDER BY created_at, id`)
	if err != nil {
		return err
	}
	var ids []string
	for rs.Next() {
		var id string
		if err := rs.Scan(&id); err != nil {
			rs.Close()
			return err
		}
		if !active[id] {
			ids = append(ids, id)
		}
	}
	rs.Close()
	for _, id := range ids {
		if err := s.enqueue(ctx, id, ""); err != nil {
			return err
		}
	}
	return s.EnqueueDigests(ctx)
}

// EnqueueStale marks ready files for reading again when no edition exists for their present bytes and the
// present recipe (a new parser version). Their older edition keeps serving until the new one lands
// (Store.EditionFor). EnqueuePending then queues the job, at start-up and after every scan.
func (s *Service) EnqueueStale(ctx context.Context) error {
	_, err := s.db.Writer().ExecContext(ctx, `UPDATE ebook_files SET state='pending', job_id=NULL, updated_at=?
WHERE state='ready' AND sha256 IS NOT NULL AND NOT EXISTS (
  SELECT 1 FROM editions e WHERE e.ebook_file_id = ebook_files.id AND e.source_sha256 = ebook_files.sha256 AND e.recipe = ?)`,
		s.now().UnixMilli(), Recipe)
	return err
}
