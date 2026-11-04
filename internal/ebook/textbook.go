package ebook

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/library"
)

// Text books (decision 0018). An ebook file with no audiobook is a book of kind 'text'. This file makes
// them, keeps them alive and tells the rest of Tendshelf (details, cover) about them. Rows only: no
// library file is ever written, renamed or deleted here.

// OnBookCreated adds a function told about the ids of text books just made, in the order registered. It is
// how the details job and the cover job hear about a book the scanner never saw. Call it before the service runs.
func (s *Service) OnBookCreated(f func(ctx context.Context, bookIDs []string)) {
	s.created = append(s.created, f)
}

func (s *Service) notifyCreated(ctx context.Context, ids ...string) {
	if len(ids) == 0 {
		return
	}
	for _, f := range s.created {
		f(ctx, ids)
	}
}

// textRoot is where a text book's root_path points for this file, and the folder the row needs. A library
// file is its own root. An upload has no path in the library, so the root is a unique absolute path under
// the ebooks folder (nothing lives there) and the folder is the library's oldest. ok is false when the
// library has no folder at all.
func (s *Service) textRoot(ctx context.Context, tx *sql.Tx, f File) (root, folderID string, ok bool, err error) {
	if f.Origin == "library" {
		return f.Path, f.FolderID, f.Path != "" && f.FolderID != "", nil
	}
	err = tx.QueryRowContext(ctx, `SELECT id FROM library_folders WHERE library_id = ? ORDER BY created_at, id LIMIT 1`, f.LibraryID).Scan(&folderID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", "", false, nil
	}
	if err != nil {
		return "", "", false, err
	}
	return filepath.Join(s.ebooksDir(), f.ID), folderID, true, nil
}

// fileStem is a file's name without its ebook extension.
func fileStem(f File) string {
	name := f.OriginalName
	if name == "" {
		name = filepath.Base(f.Path)
	}
	if e := ExtOf(name); e != "" {
		name = strings.TrimSuffix(name, name[len(name)-len(e)-1:])
	}
	return strings.TrimSpace(name)
}

// ensureBook gives a ready file with no book a book of its own. It repeats the conditions inside the
// transaction, so a pairing made since the read wins. It returns the new book's id, or "" when nothing was made.
func (s *Service) ensureBook(ctx context.Context, f File, ed Edition) (string, error) {
	title := strings.TrimSpace(ed.Title)
	if title == "" {
		title = fileStem(f)
	}
	if title == "" {
		title = "Untitled"
	}
	var made string
	err := s.db.WriteTx(ctx, func(tx *sql.Tx) error {
		var one int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM ebook_files WHERE id = ? AND book_id IS NULL AND state = 'ready' AND missing_since IS NULL`, f.ID).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		root, folder, ok, err := s.textRoot(ctx, tx, f)
		if err != nil || !ok {
			if err == nil {
				s.log.Warn("ebooks: no library folder to hold a book for an ebook", "library", f.LibraryID)
			}
			return err
		}
		id, err := s.freeBookID(ctx, tx, f.MergedFrom)
		if err != nil {
			return err
		}
		if _, err := s.lib.TxOn(ctx, tx).CreateTextBook(library.TextBookIn{LibraryID: f.LibraryID, FolderID: folder, RootPath: root, Title: title, ID: id}); err != nil {
			return err
		}
		if id == f.MergedFrom {
			if _, err := tx.ExecContext(ctx, `DELETE FROM merged_books WHERE from_id = ?`, id); err != nil {
				return err
			}
		}
		_, err = tx.ExecContext(ctx, `UPDATE ebook_files SET book_id=?, paired_by=NULL, book_default=1, updated_at=? WHERE id=?`,
			id, s.now().UnixMilli(), f.ID)
		made = id
		return err
	})
	if err != nil {
		return "", err
	}
	s.notifyCreated(ctx, made)
	return made, nil
}

// freeBookID returns want when no book has that id, else a new id.
func (s *Service) freeBookID(ctx context.Context, tx *sql.Tx, want string) (string, error) {
	if want == "" {
		return db.NewID("bk"), nil
	}
	var one int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM books WHERE id = ?`, want).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return want, nil
	}
	if err != nil {
		return "", err
	}
	return db.NewID("bk"), nil
}

// ensureFile is ensureBook for a file id; a file that is not ready, has a book or has no edition is left.
func (s *Service) ensureFile(ctx context.Context, fileID string) error {
	f, ok, err := s.store.File(ctx, fileID)
	if err != nil || !ok || f.BookID != "" || f.State != "ready" || f.MissingSince != 0 {
		return err
	}
	ed, ok, err := s.store.CurrentEdition(ctx, f.ID)
	if err != nil || !ok {
		return err
	}
	_, err = s.ensureBook(ctx, f, ed)
	return err
}

// ensureBooks is the catch-up: every ready file with no book, hold rows included, gets one. This is how
// the inbox rows of earlier versions become books.
func (s *Service) ensureBooks(ctx context.Context) error {
	afterAt, afterID := int64(-1), ""
	for {
		rs, err := s.db.Reader().QueryContext(ctx, `SELECT id FROM ebook_files
WHERE book_id IS NULL AND state = 'ready' AND missing_since IS NULL AND (created_at, id) > (?, ?) ORDER BY created_at, id LIMIT ?`,
			afterAt, afterID, pairBatch)
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
			ids = append(ids, id)
		}
		rs.Close()
		if err := rs.Err(); err != nil {
			return err
		}
		for _, id := range ids {
			if err := s.ensureFile(ctx, id); err != nil {
				return err
			}
		}
		if len(ids) < pairBatch {
			return nil
		}
		last, ok, err := s.store.File(ctx, ids[len(ids)-1])
		if err != nil || !ok {
			return err
		}
		afterAt, afterID = last.CreatedAt, last.ID
	}
}

// syncTextBooks marks a text book missing when none of its files is live and clears the mark when one is.
// The scanner's audio sweep never touches a text book, so this is its only life cycle.
func (s *Service) syncTextBooks(ctx context.Context, libraryID string) error {
	now := s.now().UnixMilli()
	const live = `EXISTS (SELECT 1 FROM ebook_files f WHERE f.book_id = books.id AND f.missing_since IS NULL AND f.state IN ('ready','pending'))`
	if _, err := s.db.Writer().ExecContext(ctx, `UPDATE books SET missing_since = ? WHERE library_id = ? AND kind = 'text'
AND missing_since IS NULL AND NOT `+live, now, libraryID); err != nil {
		return err
	}
	_, err := s.db.Writer().ExecContext(ctx, `UPDATE books SET missing_since = NULL WHERE library_id = ? AND kind = 'text'
AND missing_since IS NOT NULL AND `+live, libraryID)
	return err
}

// SoleStemDir says whether a library ebook's folder belongs to that one book, as in Calibre's "Title (id)/":
// the folder holds ebook files of exactly one name stem (an EPUB and a MOBI of one title count as one).
// Only then may the folder's sidecar files and picture be lent to the book. An upload root has no
// extension and never qualifies.
func SoleStemDir(root string) (string, bool) {
	if ExtOf(root) == "" {
		return "", false
	}
	dir := filepath.Dir(root)
	ents, err := os.ReadDir(dir)
	if err != nil {
		return "", false
	}
	stems := map[string]bool{}
	for _, e := range ents {
		if !e.Type().IsRegular() {
			continue
		}
		if x := ExtOf(e.Name()); x != "" {
			stems[strings.ToLower(strings.TrimSuffix(e.Name(), e.Name()[len(e.Name())-len(x)-1:]))] = true
		}
	}
	return dir, len(stems) == 1
}
