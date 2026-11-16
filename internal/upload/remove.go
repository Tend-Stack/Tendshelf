package upload

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"sort"
	"syscall"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/scan"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// RemoveResult lists what "Remove this book" did: the files it moved into the holding folder and the ones it kept
// because they changed since the upload placed them.
type RemoveResult struct {
	// Deleted lists the files that moved into the holding folder (the name is older than the holding folder).
	Deleted []string `json:"deleted"`
	Kept    []string `json:"kept"`
	// RemovalID is the removal that holds them, "" when nothing moved.
	RemovalID string `json:"removal_id"`
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

// Remove moves the files this package placed for a book into the holding folder and then removes the book
// (decision 0015, point 11, as changed by the addendum of 2026-11-15): the same holding folder, Put back and 30-day
// wait as a hand-added book (decision 0045), so every removal can be undone. A file moves only when its ledger row
// is present and its size, mtime and quick hash still match what was recorded; any other file stays and is named.
// Folders the ledger created are removed deepest first, only when empty. The book's rows (and, by cascade,
// everyone's progress, bookmarks and queue entries for it) go only when no audio file of it is left on disk;
// otherwise a scoped scan brings the library in line.
//
// placedBy, when set, limits the removal to one person's books (decision 0029): every live audio and
// library ebook file must lie inside a root that person's uploads published, otherwise 409 not_theirs and
// nothing is touched. A contributor always removes with placedBy set to themselves; owner and admin pass "".
func (s *Service) Remove(ctx context.Context, userID, bookID, placedBy string) (RemoveResult, error) {
	return s.removeHeld(ctx, userID, bookID, placedBy, scopeBook, nil, nil)
}

// holdScope says which part of a book an upload removal takes, and so what its commit does with the book.
type holdScope string

const (
	scopeBook  holdScope = "book"  // Remove: the book's files, then the book
	scopeWhole holdScope = "whole" // a combined book: audio and its removable ebook files; the rest of the text is unpaired
	scopeAudio holdScope = "audio" // the audio only; the text books stay
	scopeText  holdScope = "text"  // one ebook file; the book stays
)

func (sc holdScope) action() string {
	if sc == scopeText {
		return "upload.remove_text"
	}
	return "upload.remove"
}

// dropsOwners: who-added-it goes with the book when no file is left. The audio-only removal keeps it, because the
// text books that stay in that folder are still the person's to edit and remove.
func (sc holdScope) dropsOwners() bool { return sc == scopeBook || sc == scopeWhole }

// uploadPlan is what planUpload found: the removal to record, what stays, and the ledger ids to mark.
type uploadPlan struct {
	plan      RemovalPlan
	kept      []string
	keptIDs   []string // changed files: their ledger rows become 'kept'
	goneIDs   []string // ledger files already gone from the disk: 'removed', nothing to move
	gonePaths []string
	dirs      []holdDir
}

// holdDir is a folder Add books created, removed afterwards when empty.
type holdDir struct {
	path string
	idx  int // removal_paths.idx, -1 when no removal was recorded
	ids  []string
}

// planUpload reads the book's ledger rows and decides, file by file, what moves: an ordinary file of the recorded
// size, time and quick hash, inside the library folder (never through a link). Only reads; nothing changes.
// only, when set, names the one file of a text removal; extra lists ebook files outside the book's root that go
// with a whole book; keep lists files that stay.
func (s *Service) planUpload(ctx context.Context, book library.Book, folder library.Folder, lr *lroot, only string, keep, extra map[string]bool) (uploadPlan, error) {
	up := uploadPlan{}
	candidates := map[string]bool{}
	if only != "" {
		candidates[only] = true
	} else {
		audio, err := s.repo.AudioFiles(ctx, book.ID)
		if err != nil {
			return up, err
		}
		for _, a := range audio {
			candidates[a.Path] = true
		}
		ebooks, err := s.ebookPaths(ctx, book.ID)
		if err != nil {
			return up, err
		}
		for _, p := range ebooks {
			candidates[p] = true
		}
		for p := range extra {
			candidates[p] = true
		}
	}
	uploads, err := s.uploadsOfPaths(ctx, candidates)
	if err != nil {
		return up, err
	}
	root := filepath.Clean(book.RootPath)
	if only != "" {
		root = filepath.Clean(only)
	}
	var items []planItem
	var files []PlanFile
	seen := map[string]bool{}
	dirIDs := map[string][]string{}
	for _, uid := range uploads {
		rows, err := s.pathsOf(ctx, uid)
		if err != nil {
			return up, err
		}
		for _, r := range rows {
			if r.State != pathPresent || !within(folder.Path, r.Path) || filepath.Clean(r.Path) == filepath.Clean(folder.Path) {
				continue
			}
			switch r.Kind {
			case kindFile:
				var selected bool
				if only != "" {
					selected = filepath.Clean(r.Path) == filepath.Clean(only)
				} else {
					selected = (within(book.RootPath, r.Path) && !keep[r.Path]) || extra[r.Path]
				}
				if !selected || seen[r.Path] {
					continue
				}
				seen[r.Path] = true
				fi, err := lr.lstatNoLinks(r.Path)
				switch {
				case errors.Is(err, fs.ErrNotExist):
					up.goneIDs = append(up.goneIDs, r.ID)
					up.gonePaths = append(up.gonePaths, r.Path)
					continue
				case err != nil:
					up.kept = append(up.kept, r.Path)
					continue
				}
				if !unchangedOnDisk(lr, fi, r) {
					up.kept = append(up.kept, r.Path)
					up.keptIDs = append(up.keptIDs, r.ID)
					continue
				}
				rel := relSlash(folder.Path, r.Path)
				items = append(items, planItem{kind: "file", path: r.Path, rel: rel, quick: r.QuickHash, size: r.Size, mtime: r.MtimeNs})
				files = append(files, PlanFile{Path: r.Path, Rel: rel, Size: r.Size})
				up.plan.Bytes += r.Size
			case kindDir:
				if only != "" || !(within(book.RootPath, r.Path) || within(r.Path, book.RootPath)) {
					continue
				}
				if _, ok := dirIDs[r.Path]; !ok {
					if fi, err := lr.lstatNoLinks(r.Path); err != nil || !fi.IsDir() {
						continue
					}
				}
				dirIDs[r.Path] = append(dirIDs[r.Path], r.ID)
			}
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].path < items[j].path })
	sort.Slice(files, func(i, j int) bool { return files[i].Path < files[j].Path })
	dirPaths := make([]string, 0, len(dirIDs))
	for p := range dirIDs {
		dirPaths = append(dirPaths, p)
	}
	sort.Strings(dirPaths)
	for _, p := range dirPaths {
		up.dirs = append(up.dirs, holdDir{path: p, idx: -1, ids: dirIDs[p]})
		if len(items) > 0 {
			items = append(items, planItem{kind: "dir", path: p, rel: relSlash(folder.Path, p)})
			up.plan.Dirs = append(up.plan.Dirs, p)
		}
	}
	up.plan.BookID, up.plan.Title, up.plan.LibraryID, up.plan.FolderID = book.ID, book.Title, book.LibraryID, folder.ID
	up.plan.RootPath = root
	up.plan.Files = files
	up.plan.items = items
	up.plan.Hash = planHash(items)
	return up, nil
}

// unchangedOnDisk is true for an ordinary file whose size, time and quick hash are what the ledger recorded.
func unchangedOnDisk(lr *lroot, fi fs.FileInfo, r pathRow) bool {
	if !fi.Mode().IsRegular() || fi.Size() != r.Size || fi.ModTime().UnixNano() != r.MtimeNs || r.QuickHash == "" {
		return false
	}
	f, err := lr.Open(r.Path)
	if err != nil {
		return false
	}
	quick, _, qerr := media.QuickHashFile(f)
	f.Close()
	return qerr == nil && quick == r.QuickHash
}

// removeHeld is Remove for one scope: it plans, moves what is unchanged into the holding folder in one removal
// (all or nothing), and commits the book's rows. keep lists files that stay ("Remove the audiobook only"); extra
// lists ebook files outside the book's root that go with a whole combined book.
func (s *Service) removeHeld(ctx context.Context, userID, bookID, placedBy string, scope holdScope, keep, extra map[string]bool) (RemoveResult, error) {
	res := RemoveResult{Deleted: []string{}, Kept: []string{}}
	book, folder, err := s.removeChecks(ctx, bookID, placedBy, scope == scopeWhole || scope == scopeAudio)
	if err != nil {
		return res, err
	}
	lr, err := openLroot(folder.Path)
	if err != nil {
		return res, err
	}
	defer lr.Close()
	up, err := s.planUpload(ctx, book, folder, lr, "", keep, extra)
	if err != nil {
		return res, err
	}
	res.Kept = append(res.Kept, up.kept...)
	if err := s.settleLedger(ctx, bookID, up); err != nil {
		return res, err
	}
	if !hasFile(up.plan.items) {
		// Nothing to move: the files are already gone, or changed and staying. The book still goes when no audio is left.
		fin := holdFinish{userID: userID, book: book, folder: folder, scope: scope, dirs: up.dirs, kept: len(up.kept)}
		_, err := s.finishUpload(ctx, nil, lr, fin)
		return res, err
	}
	rid, err := s.holdNow(ctx, userID, up, scope)
	if err != nil {
		return res, err
	}
	res.RemovalID = rid
	for _, f := range up.plan.Files {
		res.Deleted = append(res.Deleted, f.Path)
	}
	return res, nil
}

func hasFile(items []planItem) bool {
	for _, it := range items {
		if it.kind == "file" {
			return true
		}
	}
	return false
}

// settleLedger records what planUpload learned: changed files are 'kept', files already gone are 'removed' (and an
// ebook row for one is deleted before the book row, so the ebook service cannot make a new book from it).
func (s *Service) settleLedger(ctx context.Context, bookID string, up uploadPlan) error {
	if len(up.keptIDs) == 0 && len(up.goneIDs) == 0 {
		return nil
	}
	return s.write(ctx, nil, func(tx *sql.Tx) error {
		for _, id := range up.keptIDs {
			if err := s.markPathTx(ctx, tx, id, pathKept); err != nil {
				return err
			}
		}
		for _, id := range up.goneIDs {
			if err := s.markPathTx(ctx, tx, id, pathRemoved); err != nil {
				return err
			}
		}
		for _, p := range up.gonePaths {
			if _, err := tx.ExecContext(ctx, `DELETE FROM ebook_files WHERE book_id = ? AND path = ?`, bookID, p); err != nil {
				return err
			}
		}
		return nil
	})
}

// holdNow records one removal of the planned files and runs the move in the request (decision 0045 addendum, D1):
// the caller needs the book and edition gone when its POST returns. The move holds the removal's lock for its whole
// run, so the sweep can tell a dead mover from a live one. It returns the removal's id.
func (s *Service) holdNow(ctx context.Context, userID string, up uploadPlan, scope holdScope) (string, error) {
	folder, err := s.repo.Folder(ctx, up.plan.FolderID)
	if err != nil {
		return "", fail(409, "not_removable", "The folder this book is in is no longer part of the library.")
	}
	rid := db.NewID("rmv")
	err = s.write(ctx, nil, func(tx *sql.Tx) error {
		return s.insertRemoval(ctx, tx, rid, up.plan, ModeFiles, SourceUpload, userID, folder.Path, RemovalPlanned)
	})
	if err != nil {
		return "", mapBusy(err)
	}
	_, err = s.moveRemoval(context.WithoutCancel(ctx), nil, rid, scope)
	var je *jobs.Error
	if errors.As(err, &je) {
		return "", fail(409, je.Code, je.Text)
	}
	if err != nil {
		return "", err
	}
	return rid, nil
}

// holdFinish is what the commit of an upload removal needs.
type holdFinish struct {
	rid    string // "" when nothing was moved and no removal was recorded
	userID string
	book   library.Book
	folder library.Folder
	scope  holdScope
	moved  []string
	dirs   []holdDir
	kept   int
}

// finishUpload forgets the book (when no audio is left), writes the audit entry, marks the removal held, removes
// the empty folders and queues the scoped scan when the book stays. It reports whether files of the book are left.
func (s *Service) finishUpload(ctx context.Context, g guard, lr *lroot, f holdFinish) (bool, error) {
	scope := f.scope
	if scope == "" {
		scope = scopeBook
	}
	book := f.book
	left := scope == scopeText
	if !left {
		audio, err := s.repo.AudioFiles(ctx, book.ID)
		if err != nil {
			return false, err
		}
		for _, a := range audio {
			if _, err := lr.Lstat(a.Path); err == nil {
				left = true
				break
			}
		}
	}
	if !left && scope != scopeWhole {
		ebooks, err := s.ebookPaths(ctx, book.ID)
		if err != nil {
			return false, err
		}
		for _, p := range ebooks {
			if slices.Contains(f.moved, p) {
				continue
			}
			if _, err := lr.Lstat(p); err == nil {
				left = true
				break
			}
		}
	}
	now := s.nowMs()
	err := s.repo.UpdateGuarded(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if g != nil {
			if err := g(ctx, tx); err != nil {
				return err
			}
		}
		if scope == scopeText {
			if err := s.dropMovedRows(ctx, tx, book.ID, f.moved); err != nil {
				return err
			}
		} else if err := s.dropBookRows(ctx, tx, book.ID, f.moved, false); err != nil {
			return err
		}
		if scope == scopeWhole {
			// What is left of the text only loses its place on the book (rows only, as when a book is deleted).
			if _, err := tx.ExecContext(ctx, `UPDATE ebook_files SET book_id = NULL, paired_by = NULL, book_default = 0 WHERE book_id = ?`, book.ID); err != nil {
				return err
			}
		}
		if !left && scope.dropsOwners() {
			if f.rid != "" {
				if err := s.snapshotOwners(ctx, tx, f.rid, book.RootPath); err != nil {
					return err
				}
			}
			if _, err := tx.ExecContext(ctx, `DELETE FROM upload_books WHERE root_path = ?1 OR (root_path > ?1 || '/' AND root_path < ?1 || '0')`, book.RootPath); err != nil {
				return err
			}
		}
		detail := map[string]any{"deleted": len(f.moved), "kept": f.kept, "title": book.Title}
		if f.rid != "" {
			detail["removal"] = f.rid
			if _, err := tx.ExecContext(ctx, `UPDATE removals SET state = 'held', error_code = NULL, updated_at = ?, purge_after = ? WHERE id = ?`,
				now, now+int64(RemovedKeepDays)*24*time.Hour.Milliseconds(), f.rid); err != nil {
				return err
			}
		}
		return s.audit(ctx, tx, f.userID, scope.action(), "book:"+book.ID, detail)
	}, func(tx *library.Tx) error {
		if left || scope == scopeText {
			return nil
		}
		return tx.DeleteBookAndFiles(book.ID)
	})
	if err != nil {
		return left, err
	}
	// Folders that are empty now go, deepest first; one that is not empty stays.
	dirs := append([]holdDir(nil), f.dirs...)
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i].path) > len(dirs[j].path) })
	for _, d := range dirs {
		err := lr.Remove(d.path) // rmdir: fails when anything is inside
		state := ""
		switch {
		case err == nil || errors.Is(err, fs.ErrNotExist):
			state = pathRemoved
		case errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, fs.ErrExist):
			state = pathKept
		default:
			s.log.Warn("upload: cannot remove folder", "path", d.path, "err", err)
		}
		if state == "" {
			continue
		}
		for _, id := range d.ids {
			_ = s.markPath(ctx, nil, id, state)
		}
		if f.rid != "" && d.idx >= 0 {
			held := "held"
			if state == pathKept {
				held = "kept"
			}
			_, _ = s.st.Writer().ExecContext(ctx, `UPDATE removal_paths SET state = ? WHERE removal_id = ? AND idx = ?`, held, f.rid, d.idx)
		}
	}
	if left && scope != scopeText {
		s.queueRootScan(ctx, f.userID, book.LibraryID, f.folder, book.RootPath, lr)
	}
	return left, nil
}

// queueRootScan queues a scan of the book's folder (or of its parent, for a book that is one file), so the library
// matches what is left on disk.
func (s *Service) queueRootScan(ctx context.Context, userID, libraryID string, folder library.Folder, root string, lr *lroot) {
	sub := root
	if fi, err := lr.Lstat(sub); err != nil || !fi.IsDir() {
		sub = filepath.Dir(sub)
	}
	if rel, err := filepath.Rel(folder.Path, sub); err == nil && within(folder.Path, sub) && rel != "." {
		if _, err := s.enq.Enqueue(ctx, scan.KindScan, scan.ScanPayload{LibraryID: libraryID, FolderID: folder.ID, Subdir: filepath.ToSlash(rel)},
			jobs.EnqueueOptions{CreatedBy: userID}); err != nil {
			s.log.Warn("upload: cannot queue the scan after a remove", "err", err)
		}
	}
}

// removeChecks runs every refusal Remove has before it touches anything, so a caller that does something
// destructive first (drops a stored upload, splits the text off) can learn beforehand that Remove would refuse:
// the book exists, the files are Tendshelf's (and the contributor's), the folder is still in the library and can
// be opened. filesAreSetAside skips the file checks for a caller that takes the files it does not own off the
// book before it calls Remove (the whole-book and audio-only removals of a combined book); it has checked the
// audio itself.
func (s *Service) removeChecks(ctx context.Context, bookID, placedBy string, filesAreSetAside bool) (library.Book, library.Folder, error) {
	var folder library.Folder
	book, err := s.repo.Book(ctx, bookID)
	if errors.Is(err, library.ErrNotFound) {
		return book, folder, errNotFound
	} else if err != nil {
		return book, folder, err
	}
	if !filesAreSetAside {
		if !s.Removable(ctx, bookID) {
			return book, folder, fail(409, "not_removable", "Tendshelf did not add this book, so it will not remove it.")
		}
		if placedBy != "" {
			if theirs, err := s.allFilesAddedBy(ctx, bookID, placedBy); err != nil {
				return book, folder, err
			} else if !theirs {
				return book, folder, fail(409, "not_theirs", "Someone else added part of this book, so only an admin can remove it.")
			}
		}
	}
	folder, err = s.repo.Folder(ctx, book.FolderID)
	if err != nil {
		return book, folder, fail(409, "not_removable", "The folder this book is in is no longer part of the library.")
	}
	lr, err := openLroot(folder.Path)
	if err != nil {
		return book, folder, err
	}
	lr.Close()
	return book, folder, nil
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
