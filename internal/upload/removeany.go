package upload

// Removing a book whose files were put in the library folder by hand (decision 0045). Upload-placed books keep
// their own path in remove.go; this file is the second, owner-and-admin path. The files are never deleted at
// confirm time: each one is moved by a single rename into <folder>/.tendshelf-removed/<removal id>/<path below the
// folder>, can be put back, and is deleted only by Purge (after RemovedKeepDays, or "Delete now").
//
// The order is what keeps a person's files safe:
//
//  1. PlanRemoval only looks (Lstat, never a link) and builds the exact list the person confirms, with a hash.
//  2. StartRemoval re-plans, refuses when the hash no longer matches, and records the removal and every path
//     (including the holding path each file will get) in one transaction before anything on disk changes.
//  3. The job verifies every file (regular file, size, mtime, quick hash) before the first move, moves them one by
//     one, and puts every moved file back if any move fails. A crash leaves state 'moving'; the next job run or the
//     sweep puts the moved files back.
//  4. Only when every file is held does one transaction delete the book's rows and mark the removal held.

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/scan"
)

// KindRemoveFiles is the job that moves a hand-added book's files into the holding folder.
const KindRemoveFiles = "upload.remove_files"

// RemovedKeepDays is how long moved files wait in the holding folder before they are deleted.
const RemovedKeepDays = 30

// HoldingDir is the hidden folder, inside the library folder, that holds removed files. The scanner skips it
// because its name starts with a dot.
const HoldingDir = ".tendshelf-removed"

// The modes and states of a removal (removals.mode, removals.state).
const (
	ModeFiles     = "files"
	ModeKeepFiles = "keep_files"

	RemovalPlanned  = "planned"
	RemovalMoving   = "moving"
	RemovalHeld     = "held"
	RemovalRestored = "restored"
	RemovalPurged   = "purged"
	RemovalFailed   = "failed"
)

const (
	maxExtraBytes = 50 << 20
	maxWalk       = 5000 // entries looked at below a book's folder
	movingStale   = time.Hour
)

// extraExts are the cover and sidecar files that move with a hand-added book (decision 0045, rule 7).
var extraExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true, ".gif": true, ".opf": true, ".nfo": true, ".json": true,
	".txt": true, ".cue": true, ".m3u": true, ".m3u8": true, ".log": true, ".sfv": true, ".md5": true,
}

// PlanFile is one file a removal moves. Rel is the path below the library folder, in slash form.
type PlanFile struct {
	Path string
	Rel  string
	Size int64
}

// RemovalPlan is the exact list a person confirms. Hash covers every file and folder in it.
type RemovalPlan struct {
	BookID, Title, LibraryID, FolderID, RootPath string
	Files                                        []PlanFile // the book's scanned audio and ebook files
	Extras                                       []PlanFile // cover and sidecar files that go with them
	Dirs                                         []string   // folders removed afterwards when empty
	Missing                                      []string   // scanned files already gone from the disk
	Staying                                      int        // other files in the book's folder; they stay
	Bytes                                        int64
	Hash                                         string
	Listeners, Bookmarkers                       int  // people with a listening place, and with bookmarks, who lose them
	RootIsFolder                                 bool // the book's root is the library folder itself

	items []planItem
}

type planItem struct {
	kind, path, rel, quick string
	size, mtime            int64
}

// Removal is one row of removals.
type Removal struct {
	ID, LibraryID, FolderID, BookID, Title, RootPath string
	Mode, State, ErrorCode, PlanHash                 string
	Files                                            int
	Bytes                                            int64
	JobID, RemovedBy                                 string
	CreatedAt, UpdatedAt, PurgeAfter                 int64 // Unix ms; PurgeAfter is 0 when not set
}

// RestoreResult says what "Put back" did: the files that returned, and the ones that stayed in the holding folder
// because something else now has their name.
type RestoreResult struct {
	Restored []string `json:"restored"`
	Kept     []string `json:"kept"`
}

type removeFilesPayload struct {
	RemovalID string `json:"removal_id"`
}

// RemoveFilesResult is the stored result of an upload.remove_files job.
type RemoveFilesResult struct {
	RemovalID string `json:"removal_id"`
	Moved     int    `json:"moved"`
}

// The refusals a removal can give (Error.Code).
const (
	CodePlanChanged      = "plan_changed"
	CodeChangedSinceScan = "changed_since_scan"
	CodeBusy             = "busy"
	CodeNotConfirmed     = "not_confirmed"
	CodeReadOnly         = "read_only"
	CodeCannotMove       = "cannot_move"
	CodeNotRemovable     = "not_removable"
	CodeInterrupted      = "interrupted"
)

func errPlanChanged() *Error {
	return fail(409, CodePlanChanged, "The files changed since you opened this page. Look at the list again.")
}
func errChangedSinceScan() *Error {
	return fail(409, CodeChangedSinceScan, "Some files changed since the last scan, so nothing was moved. Scan the library, then try again.")
}
func errReadOnly() *Error {
	return fail(409, CodeReadOnly, "Tendshelf cannot change files in this folder, so nothing was moved.")
}
func errCannotMove() *Error {
	return fail(409, CodeCannotMove, "Tendshelf could not move every file, so it put them all back. Nothing was removed.")
}
func errNotRemovable(msg string) *Error { return fail(409, CodeNotRemovable, msg) }

var errRemovalNotFound = fail(404, "not_found", "That removal does not exist.")

func isReadOnly(err error) bool {
	return errors.Is(err, syscall.EROFS) || errors.Is(err, syscall.EACCES) || errors.Is(err, syscall.EPERM) || errors.Is(err, fs.ErrPermission)
}

// lstatNoLinks is Lstat that also refuses a path with a symbolic link anywhere in it (below the folder). The
// error is errLink for a link, so a caller can say not_removable.
var errLink = errors.New("upload: a link is in the way")

func (l *lroot) lstatNoLinks(abs string) (fs.FileInfo, error) {
	r, err := l.rel(abs)
	if err != nil {
		return nil, err
	}
	if r == "." {
		return l.r.Lstat(".")
	}
	var fi fs.FileInfo
	cur := ""
	for _, part := range strings.Split(filepath.ToSlash(r), "/") {
		cur = filepath.Join(cur, part)
		fi, err = l.r.Lstat(cur)
		if err != nil {
			return nil, err
		}
		if fi.Mode()&fs.ModeSymlink != 0 {
			return nil, errLink
		}
	}
	return fi, nil
}

func relSlash(folder, abs string) string {
	r, err := filepath.Rel(folder, abs)
	if err != nil {
		return filepath.ToSlash(abs)
	}
	return filepath.ToSlash(r)
}

// PlanRemoval looks at the book and its files and returns what a removal would move. It changes nothing. Errors:
// 404 not_found, 409 not_removable (no scanned file left, the folder or root is gone or a link, a path leaves the
// library folder) and 409 changed_since_scan (a file's size or time differs from the scan's record).
func (s *Service) PlanRemoval(ctx context.Context, bookID string) (RemovalPlan, error) {
	var plan RemovalPlan
	book, err := s.repo.Book(ctx, bookID)
	if errors.Is(err, library.ErrNotFound) {
		return plan, fail(404, "not_found", "That book does not exist.")
	} else if err != nil {
		return plan, err
	}
	folder, err := s.repo.Folder(ctx, book.FolderID)
	if err != nil {
		return plan, errNotRemovable("The folder this book is in is no longer part of the library.")
	}
	plan = RemovalPlan{BookID: book.ID, Title: book.Title, LibraryID: book.LibraryID, FolderID: folder.ID, RootPath: filepath.Clean(book.RootPath)}
	root := plan.RootPath
	if !filepath.IsAbs(root) || !within(folder.Path, root) {
		return plan, errNotRemovable("This book is not inside its library folder, so Tendshelf will not touch its files.")
	}
	lr, err := openLroot(folder.Path)
	if err != nil {
		return plan, errNotRemovable("Tendshelf cannot open the folder this book is in.")
	}
	defer lr.Close()

	records, missing, err := s.liveFiles(ctx, bookID)
	if err != nil {
		return plan, err
	}
	plan.Missing = missing
	rootFI, err := lr.lstatNoLinks(root)
	switch {
	case errors.Is(err, errLink):
		return plan, errNotRemovable("This book's folder is a link, so Tendshelf will not touch it.")
	case errors.Is(err, fs.ErrNotExist):
		return plan, errNotRemovable("This book's files are no longer on the disk.")
	case err != nil:
		return plan, errNotRemovable("Tendshelf cannot read this book's folder.")
	}
	rootIsDir := rootFI.IsDir()
	plan.RootIsFolder = filepath.Clean(folder.Path) == root

	have := map[string]bool{}
	for _, rec := range records {
		p := filepath.Clean(rec.path)
		inRoot := within(root, p)
		if rootIsDir && !inRoot || !rootIsDir && p != root {
			if rec.audio || !within(folder.Path, p) {
				return plan, errNotRemovable("One of this book's files is outside its folder, so Tendshelf will not touch it.")
			}
			plan.Staying++ // a text edition paired from elsewhere in the library is left alone
			continue
		}
		fi, err := lr.lstatNoLinks(p)
		switch {
		case errors.Is(err, errLink):
			return plan, errNotRemovable("One of this book's files is a link, so Tendshelf will not touch it.")
		case errors.Is(err, fs.ErrNotExist):
			plan.Missing = append(plan.Missing, p)
			continue
		case err != nil:
			return plan, errNotRemovable("Tendshelf cannot read one of this book's files.")
		}
		if !fi.Mode().IsRegular() {
			return plan, errNotRemovable("One of this book's files is not an ordinary file, so Tendshelf will not touch it.")
		}
		if fi.Size() != rec.size || fi.ModTime().UnixNano() != rec.mtime || rec.quick == "" {
			return plan, errChangedSinceScan()
		}
		have[p] = true
		plan.items = append(plan.items, planItem{kind: "file", path: p, rel: relSlash(folder.Path, p), quick: rec.quick, size: rec.size, mtime: rec.mtime})
		plan.Files = append(plan.Files, PlanFile{Path: p, Rel: relSlash(folder.Path, p), Size: rec.size})
		plan.Bytes += rec.size
	}
	if len(plan.Files) == 0 {
		return plan, errNotRemovable("None of this book's scanned files are on the disk any more.")
	}

	if rootIsDir && !plan.RootIsFolder {
		shared, err := s.sharedFolder(ctx, bookID, book.LibraryID, root)
		if err != nil {
			return plan, err
		}
		if shared {
			plan.Staying++ // the folder holds another book: only this book's own files move
		} else {
			s.walkExtras(lr, folder.Path, root, have, &plan)
		}
	}
	sort.Slice(plan.items, func(i, j int) bool { return plan.items[i].path < plan.items[j].path })
	sort.Slice(plan.Files, func(i, j int) bool { return plan.Files[i].Path < plan.Files[j].Path })
	sort.Slice(plan.Extras, func(i, j int) bool { return plan.Extras[i].Path < plan.Extras[j].Path })
	sort.Strings(plan.Dirs)
	plan.Hash = planHash(plan.items)
	_ = s.st.Reader().QueryRowContext(ctx, `SELECT count(DISTINCT user_id) FROM progress WHERE book_id = ?`, bookID).Scan(&plan.Listeners)
	_ = s.st.Reader().QueryRowContext(ctx, `SELECT count(DISTINCT user_id) FROM bookmarks WHERE book_id = ? AND deleted_at IS NULL`, bookID).Scan(&plan.Bookmarkers)
	return plan, nil
}

type fileRecord struct {
	path, quick string
	size, mtime int64
	audio       bool
}

// liveFiles returns the book's scanned audio files and library ebook files that are not marked missing, and the
// paths of the ones that are.
func (s *Service) liveFiles(ctx context.Context, bookID string) (live []fileRecord, missing []string, err error) {
	audio, err := s.repo.AudioFiles(ctx, bookID)
	if err != nil {
		return nil, nil, err
	}
	for _, a := range audio {
		if a.MissingSince != 0 {
			missing = append(missing, a.Path)
			continue
		}
		live = append(live, fileRecord{path: a.Path, quick: a.QuickHash, size: a.Size, mtime: a.MtimeNs, audio: true})
	}
	rows, err := s.st.Reader().QueryContext(ctx, `SELECT path, size, mtime_ns, quick_hash, missing_since IS NOT NULL FROM ebook_files
  WHERE book_id = ? AND origin = 'library' AND path IS NOT NULL ORDER BY path`, bookID)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var r fileRecord
		var gone bool
		if err := rows.Scan(&r.path, &r.size, &r.mtime, &r.quick, &gone); err != nil {
			return nil, nil, err
		}
		if gone {
			missing = append(missing, r.path)
		} else {
			live = append(live, r)
		}
	}
	return live, missing, rows.Err()
}

// sharedFolder reports whether another book has its root, or a recorded file, inside root.
func (s *Service) sharedFolder(ctx context.Context, bookID, libraryID, root string) (bool, error) {
	var n int
	err := s.st.Reader().QueryRowContext(ctx, `SELECT
  (SELECT count(*) FROM books WHERE id <> ?1 AND library_id = ?2 AND (root_path = ?3 OR (root_path > ?3 || '/' AND root_path < ?3 || '0')))
  + (SELECT count(*) FROM audio_files WHERE book_id <> ?1 AND path > ?3 || '/' AND path < ?3 || '0')
  + (SELECT count(*) FROM ebook_files WHERE book_id IS NOT ?1 AND path IS NOT NULL AND path > ?3 || '/' AND path < ?3 || '0')`,
		bookID, libraryID, root).Scan(&n)
	return n > 0, err
}

// walkExtras looks through a book's own folder for cover and sidecar files (decision 0045, rule 7) and for the
// folders to remove afterwards. Anything else counts as staying.
func (s *Service) walkExtras(lr *lroot, folder, root string, have map[string]bool, plan *RemovalPlan) {
	seen := 0
	var walk func(dir string)
	walk = func(dir string) {
		entries, err := lr.ReadDir(dir)
		if err != nil {
			plan.Staying++
			return
		}
		plan.Dirs = append(plan.Dirs, dir)
		plan.items = append(plan.items, planItem{kind: "dir", path: dir, rel: relSlash(folder, dir)})
		for _, e := range entries {
			seen++
			if seen > maxWalk {
				plan.Staying++
				continue
			}
			p := filepath.Join(dir, e.Name())
			if have[p] {
				continue
			}
			fi, err := lr.Lstat(p)
			switch {
			case err != nil || fi.Mode()&fs.ModeSymlink != 0:
				plan.Staying++
			case fi.IsDir():
				if strings.HasPrefix(e.Name(), ".") {
					plan.Staying++
				} else {
					walk(p)
				}
			case fi.Mode().IsRegular() && !strings.HasPrefix(e.Name(), ".") && extraExts[strings.ToLower(filepath.Ext(e.Name()))] && fi.Size() <= maxExtraBytes:
				plan.items = append(plan.items, planItem{kind: "extra", path: p, rel: relSlash(folder, p), size: fi.Size(), mtime: fi.ModTime().UnixNano()})
				plan.Extras = append(plan.Extras, PlanFile{Path: p, Rel: relSlash(folder, p), Size: fi.Size()})
				plan.Bytes += fi.Size()
			default:
				plan.Staying++
			}
		}
	}
	walk(root)
}

func planHash(items []planItem) string {
	lines := make([]string, 0, len(items))
	for _, it := range items {
		size, mtime := it.size, it.mtime
		if it.kind == "dir" {
			size, mtime = 0, 0 // a folder's time moves whenever anything in it does
		}
		lines = append(lines, fmt.Sprintf("%s|%s|%d|%d\n", it.kind, it.path, size, mtime))
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "")))
	return hex.EncodeToString(sum[:])
}

// StartRemoval records a removal of the book the person confirmed (planHash is the Hash of the plan they saw) and,
// for mode "files", queues the job that moves the files; it returns the removal's id. Mode "keep_files" only takes
// the book out of Tendshelf and keeps the files where they are: it is done when this returns. Errors: 422 bad_mode,
// 409 plan_changed (the disk no longer matches the plan), 409 busy (a removal of this book is under way), and the
// refusals of PlanRemoval.
func (s *Service) StartRemoval(ctx context.Context, userID, bookID, planHash, mode string) (string, error) {
	if mode != ModeFiles && mode != ModeKeepFiles {
		return "", fail(422, "bad_mode", "Choose what to do with the files.")
	}
	plan, err := s.PlanRemoval(ctx, bookID)
	if err != nil {
		return "", err
	}
	if plan.Hash != planHash {
		return "", errPlanChanged()
	}
	folder, err := s.repo.Folder(ctx, plan.FolderID)
	if err != nil {
		return "", err
	}
	rid := db.NewID("rmv")
	now := s.nowMs()
	state := RemovalPlanned
	if mode == ModeKeepFiles {
		state = RemovalHeld
	}
	insert := func(ctx context.Context, tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO removals(id, library_id, folder_id, book_id, title, root_path, mode, state, plan_hash, files, bytes,
  removed_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, (SELECT id FROM users WHERE id = ?), ?, ?)`,
			rid, plan.LibraryID, plan.FolderID, bookID, plan.Title, plan.RootPath, mode, state, plan.Hash, len(plan.Files)+len(plan.Extras), plan.Bytes, userID, now, now)
		if err != nil {
			return err
		}
		for i, it := range plan.items {
			held := ""
			if it.kind != "dir" {
				held = filepath.Join(folder.Path, HoldingDir, rid, filepath.FromSlash(it.rel))
			}
			var quick, heldPath any
			if it.quick != "" {
				quick = it.quick
			}
			if held != "" && mode == ModeFiles {
				heldPath = held
			}
			st := "planned"
			if mode == ModeKeepFiles {
				st = "kept"
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO removal_paths(removal_id, idx, kind, path, held_path, size, mtime_ns, quick_hash, state)
  VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, rid, i, it.kind, it.path, heldPath, it.size, it.mtime, quick, st); err != nil {
				return err
			}
		}
		return nil
	}
	if mode == ModeKeepFiles {
		// One transaction: the record that keeps the scanner away and the deletion of the book's rows.
		err := s.repo.UpdateGuarded(ctx, func(ctx context.Context, tx *sql.Tx) error {
			if err := insert(ctx, tx); err != nil {
				return err
			}
			if err := s.dropBookRows(ctx, tx, bookID, nil, true); err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `UPDATE removals SET purge_after = NULL WHERE id = ?`, rid); err != nil {
				return err
			}
			return s.audit(ctx, tx, userID, "book.remove_keep_files", "book:"+bookID, map[string]any{"title": plan.Title, "removal": rid, "files": len(plan.Files)})
		}, func(tx *library.Tx) error { return tx.DeleteBookAndFiles(bookID) })
		if err != nil {
			return "", mapBusy(err)
		}
		return rid, nil
	}
	if err := s.write(ctx, nil, func(tx *sql.Tx) error { return insert(ctx, tx) }); err != nil {
		return "", mapBusy(err)
	}
	job, err := s.enq.Enqueue(ctx, KindRemoveFiles, removeFilesPayload{RemovalID: rid}, jobs.EnqueueOptions{CreatedBy: userID})
	if err != nil {
		_ = s.failRemoval(ctx, nil, rid, "cannot_start", RemovalPlanned)
		return "", err
	}
	_ = s.write(ctx, nil, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE removals SET job_id = ? WHERE id = ?`, job, rid)
		return err
	})
	return rid, nil
}

func mapBusy(err error) error {
	if err != nil && strings.Contains(err.Error(), "removals.book_id") {
		return fail(409, CodeBusy, "This book is already being removed.")
	}
	return err
}

// dropBookRows removes what belongs to the book beside the library tables: its library ebook rows (the moved ones
// and the missing ones; keepAll also drops every library ebook row of it), the upload ledger rows of the moved
// files, and who-added-it for its root. moved lists the paths that were moved.
func (s *Service) dropBookRows(ctx context.Context, tx *sql.Tx, bookID string, moved []string, keepFiles bool) error {
	if keepFiles {
		if _, err := tx.ExecContext(ctx, `DELETE FROM ebook_files WHERE book_id = ? AND origin = 'library'`, bookID); err != nil {
			return err
		}
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM ebook_files WHERE book_id = ?1 AND origin = 'library' AND missing_since IS NOT NULL`, bookID); err != nil {
		return err
	}
	for _, p := range moved {
		if _, err := tx.ExecContext(ctx, `DELETE FROM ebook_files WHERE book_id = ? AND origin = 'library' AND path = ?`, bookID, p); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE upload_paths SET state = ? WHERE path = ? AND kind = 'file' AND state = 'present'`, pathRemoved, p); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) handleRemoveFiles(ctx context.Context, t *jobs.Task) (any, error) {
	var p removeFilesPayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.RemovalID == "" {
		return nil, &jobs.Error{Code: "bad_payload", Text: "This job was queued with settings Tendshelf cannot read.", Permanent: true, Err: err}
	}
	return s.removeFiles(ctx, t.Fence, p.RemovalID)
}

// removeFiles is the job body (g is the fence check; nil in tests).
func (s *Service) removeFiles(ctx context.Context, g guard, rid string) (any, error) {
	l := s.lock("rmv:" + rid)
	l.Lock()
	defer l.Unlock()
	rm, err := s.Removal(ctx, rid)
	if err != nil {
		return nil, &jobs.Error{Code: "not_found", Text: "That removal does not exist.", Permanent: true, Err: err}
	}
	switch rm.State {
	case RemovalPlanned:
	case RemovalMoving:
		// A run that died part-way: put the files back and say so.
		if err := s.rollbackRemoval(ctx, rm, CodeInterrupted); err != nil {
			return nil, err
		}
		return nil, &jobs.Error{Code: CodeInterrupted, Text: "The server stopped while the files were moving, so they were put back. Nothing was removed.", Permanent: true}
	default:
		return RemoveFilesResult{RemovalID: rid}, nil
	}
	paths, err := s.removalPaths(ctx, rid)
	if err != nil {
		return nil, err
	}
	folder, err := s.repo.Folder(ctx, rm.FolderID)
	if err != nil {
		return nil, s.failJob(ctx, g, rid, CodeNotRemovable, errNotRemovable("The folder this book is in is no longer part of the library."))
	}
	lr, err := openLroot(folder.Path)
	if err != nil {
		return nil, s.failJob(ctx, g, rid, CodeNotRemovable, errNotRemovable("Tendshelf cannot open the folder this book is in."))
	}
	defer lr.Close()

	if err := s.write(ctx, g, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `UPDATE removals SET state = 'moving', updated_at = ? WHERE id = ? AND state = 'planned'`, s.nowMs(), rid)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fail(409, CodeBusy, "This removal is already running.")
		}
		return nil
	}); err != nil {
		return nil, err
	}

	// Verify everything before the first move.
	for _, pr := range paths {
		if err := s.verifyPath(lr, pr); err != nil {
			return nil, s.failJob(ctx, g, rid, CodeChangedSinceScan, errChangedSinceScan())
		}
	}
	holding := filepath.Join(folder.Path, HoldingDir)
	rdir := filepath.Join(holding, rid)
	for _, d := range []string{holding, rdir} {
		if err := lr.Mkdir(d, 0o755); err != nil && !errors.Is(err, fs.ErrExist) {
			if isReadOnly(err) {
				return nil, s.failJob(ctx, g, rid, CodeReadOnly, errReadOnly())
			}
			return nil, s.failJob(ctx, g, rid, CodeCannotMove, errCannotMove())
		}
	}

	var moved []string
	for _, pr := range paths {
		if pr.kind == "dir" {
			continue
		}
		if err := s.crash(fmt.Sprintf("remove_files:before_move:%d", pr.idx)); err != nil {
			return nil, err
		}
		merr := s.moveOne(ctx, lr, pr.path, pr.heldPath)
		if merr == nil {
			if err := s.crash(fmt.Sprintf("remove_files:after_move:%d", pr.idx)); err != nil {
				return nil, err // a simulated crash: nothing is put back here
			}
			moved = append(moved, pr.path)
			_, _ = s.st.Writer().ExecContext(ctx, `UPDATE removal_paths SET state = 'held' WHERE removal_id = ? AND idx = ?`, rid, pr.idx)
			_, _ = s.st.Writer().ExecContext(ctx, `UPDATE removals SET updated_at = ? WHERE id = ?`, s.nowMs(), rid)
			if ctx.Err() != nil {
				break
			}
			continue
		}
		s.log.Warn("upload: cannot move a file into the holding folder", "removal", rid, "path", pr.path, "err", merr)
		// Cancelled by shutdown: put everything back and let the job run again without using an attempt.
		if ctx.Err() != nil {
			cur, _ := s.Removal(context.WithoutCancel(ctx), rid)
			_ = s.rollbackRemoval(context.WithoutCancel(ctx), cur, "")
			_ = s.write(context.WithoutCancel(ctx), nil, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, `UPDATE removals SET state = 'planned', error_code = NULL, updated_at = ? WHERE id = ?`, s.nowMs(), rid)
				return err
			})
			return nil, ctx.Err()
		}
		code, e := CodeCannotMove, errCannotMove()
		if isReadOnly(merr) {
			code, e = CodeReadOnly, errReadOnly()
		}
		cur, _ := s.Removal(ctx, rid)
		if rerr := s.rollbackRemoval(ctx, cur, code); rerr != nil {
			return nil, rerr
		}
		return nil, &jobs.Error{Code: e.Code, Text: e.Message, Permanent: true, Err: merr}
	}
	if ctx.Err() != nil {
		cur, _ := s.Removal(context.WithoutCancel(ctx), rid)
		_ = s.rollbackRemoval(context.WithoutCancel(ctx), cur, "")
		_ = s.write(context.WithoutCancel(ctx), nil, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE removals SET state = 'planned', error_code = NULL, updated_at = ? WHERE id = ?`, s.nowMs(), rid)
			return err
		})
		return nil, ctx.Err()
	}

	// Everything is held: forget the book. Its files are in the holding folder, not on the disk's book path.
	if err := s.crash("remove_files:before_commit"); err != nil {
		return nil, err
	}
	now := s.nowMs()
	err = s.repo.UpdateGuarded(ctx, func(ctx context.Context, tx *sql.Tx) error {
		if g != nil {
			if err := g(ctx, tx); err != nil {
				return err
			}
		}
		if err := s.dropBookRows(ctx, tx, rm.BookID, moved, false); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM upload_books WHERE root_path = ?1 OR (root_path > ?1 || '/' AND root_path < ?1 || '0')`, rm.RootPath); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE removals SET state = 'held', error_code = NULL, updated_at = ?, purge_after = ? WHERE id = ?`,
			now, now+int64(RemovedKeepDays)*24*time.Hour.Milliseconds(), rid); err != nil {
			return err
		}
		return s.audit(ctx, tx, rm.RemovedBy, "book.remove_files", "book:"+rm.BookID, map[string]any{"title": rm.Title, "removal": rid, "files": rm.Files, "bytes": rm.Bytes})
	}, func(tx *library.Tx) error { return tx.DeleteBookAndFiles(rm.BookID) })
	if err != nil {
		cur, _ := s.Removal(context.WithoutCancel(ctx), rid)
		_ = s.rollbackRemoval(context.WithoutCancel(ctx), cur, CodeCannotMove)
		return nil, err
	}

	// Folders that are empty now go, deepest first; one that is not empty stays.
	dirs := make([]removalPath, 0)
	for _, pr := range paths {
		if pr.kind == "dir" {
			dirs = append(dirs, pr)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return len(dirs[i].path) > len(dirs[j].path) })
	for _, d := range dirs {
		st := "held"
		if err := lr.Remove(d.path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			st = "kept"
		}
		_, _ = s.st.Writer().ExecContext(ctx, `UPDATE removal_paths SET state = ? WHERE removal_id = ? AND idx = ?`, st, rid, d.idx)
	}
	return RemoveFilesResult{RemovalID: rid, Moved: len(moved)}, nil
}

// verifyPath checks one path against its record: an ordinary file of the same size and time (and, for a scanned
// file, the same quick hash), or a folder.
func (s *Service) verifyPath(lr *lroot, pr removalPath) error {
	fi, err := lr.lstatNoLinks(pr.path)
	if err != nil {
		return err
	}
	switch pr.kind {
	case "dir":
		if !fi.IsDir() {
			return errChangedSinceScan()
		}
		return nil
	}
	if !fi.Mode().IsRegular() || fi.Size() != pr.size || fi.ModTime().UnixNano() != pr.mtime {
		return errChangedSinceScan()
	}
	if pr.kind == "file" {
		f, err := lr.Open(pr.path)
		if err != nil {
			return err
		}
		quick, _, qerr := media.QuickHashFile(f)
		f.Close()
		if qerr != nil || quick != pr.quick {
			return errChangedSinceScan()
		}
	}
	return nil
}

// moveOne renames one file into the holding folder: its folders are made first, and nothing is overwritten.
func (s *Service) moveOne(ctx context.Context, lr *lroot, from, to string) error {
	if to == "" {
		return errors.New("upload: no holding path recorded")
	}
	if err := lr.MkdirAll(filepath.Dir(to), 0o755); err != nil {
		return err
	}
	if _, err := lr.Lstat(to); err == nil {
		return fs.ErrExist
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return lr.Rename(from, to)
}

type removalPath struct {
	idx                  int
	kind, path, heldPath string
	size, mtime          int64
	quick, state         string
}

func (s *Service) removalPaths(ctx context.Context, rid string) ([]removalPath, error) {
	rows, err := s.st.Reader().QueryContext(ctx, `SELECT idx, kind, path, COALESCE(held_path, ''), size, mtime_ns, COALESCE(quick_hash, ''), state
  FROM removal_paths WHERE removal_id = ? ORDER BY idx`, rid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []removalPath
	for rows.Next() {
		var r removalPath
		if err := rows.Scan(&r.idx, &r.kind, &r.path, &r.heldPath, &r.size, &r.mtime, &r.quick, &r.state); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// failJob marks the removal failed with code (nothing was moved) and returns the job error to report.
func (s *Service) failJob(ctx context.Context, g guard, rid, code string, e *Error) error {
	_ = s.failRemoval(context.WithoutCancel(ctx), g, rid, code)
	return &jobs.Error{Code: e.Code, Text: e.Message, Permanent: true}
}

func (s *Service) failRemoval(ctx context.Context, g guard, rid, code string, from ...string) error {
	return s.write(ctx, g, func(tx *sql.Tx) error {
		q := `UPDATE removals SET state = 'failed', error_code = ?, updated_at = ? WHERE id = ?`
		args := []any{code, s.nowMs(), rid}
		if len(from) > 0 {
			q += ` AND state IN (` + strings.TrimSuffix(strings.Repeat("?,", len(from)), ",") + `)`
			for _, f := range from {
				args = append(args, f)
			}
		}
		_, err := tx.ExecContext(ctx, q, args...)
		return err
	})
}

// rollbackRemoval puts every file of a removal that is in the holding folder back where it came from, and fails
// the removal with code (code "" leaves the state for the caller). It reads the disk, not the recorded states, so
// a crash between a rename and its record is still undone. A file whose old place is taken stays in the holding
// folder.
func (s *Service) rollbackRemoval(ctx context.Context, rm Removal, code string) error {
	folder, err := s.repo.Folder(ctx, rm.FolderID)
	if err != nil {
		return err
	}
	lr, err := openLroot(folder.Path)
	if err != nil {
		return err
	}
	defer lr.Close()
	paths, err := s.removalPaths(ctx, rm.ID)
	if err != nil {
		return err
	}
	left := 0
	for i := len(paths) - 1; i >= 0; i-- {
		pr := paths[i]
		if pr.kind == "dir" || pr.heldPath == "" {
			continue
		}
		if _, err := lr.Lstat(pr.heldPath); err != nil {
			continue // never moved (or already back)
		}
		if _, err := lr.Lstat(pr.path); err == nil {
			left++
			s.log.Warn("upload: a removed file's place is taken, so it stays in the holding folder", "removal", rm.ID, "path", pr.path)
			continue
		}
		if err := lr.MkdirAll(filepath.Dir(pr.path), 0o755); err != nil {
			left++
			continue
		}
		if err := lr.Rename(pr.heldPath, pr.path); err != nil {
			left++
			s.log.Warn("upload: cannot put a file back", "removal", rm.ID, "path", pr.path, "err", err)
			continue
		}
		_, _ = s.st.Writer().ExecContext(ctx, `UPDATE removal_paths SET state = 'planned' WHERE removal_id = ? AND idx = ?`, rm.ID, pr.idx)
	}
	if left == 0 {
		pruneEmpty(lr, filepath.Join(folder.Path, HoldingDir, rm.ID))
		_ = lr.Remove(filepath.Join(folder.Path, HoldingDir))
	}
	if code == "" {
		return nil
	}
	return s.failRemoval(ctx, nil, rm.ID, code)
}

// pruneEmpty removes the empty folders below and including dir (rmdir only: a folder with anything in it stays).
func pruneEmpty(lr *lroot, dir string) {
	entries, err := lr.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			pruneEmpty(lr, filepath.Join(dir, e.Name()))
		}
	}
	_ = lr.Remove(dir)
}

// Removal returns one removal; 404 not_found when there is none.
func (s *Service) Removal(ctx context.Context, rid string) (Removal, error) {
	rows, err := s.removals(ctx, `WHERE id = ?`, rid)
	if err != nil {
		return Removal{}, err
	}
	if len(rows) == 0 {
		return Removal{}, errRemovalNotFound
	}
	return rows[0], nil
}

// HeldRemovals lists the removals of a library whose files are waiting (or whose book is kept out), newest first.
func (s *Service) HeldRemovals(ctx context.Context, libraryID string) ([]Removal, error) {
	return s.removals(ctx, `WHERE library_id = ? AND state = 'held'`, libraryID)
}

func (s *Service) removals(ctx context.Context, where string, args ...any) ([]Removal, error) {
	rows, err := s.st.Reader().QueryContext(ctx, `SELECT id, library_id, folder_id, book_id, title, root_path, mode, state, COALESCE(error_code, ''),
  plan_hash, files, bytes, COALESCE(job_id, ''), COALESCE(removed_by, ''), created_at, updated_at, COALESCE(purge_after, 0)
  FROM removals `+where+` ORDER BY created_at DESC, id DESC`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Removal
	for rows.Next() {
		var r Removal
		if err := rows.Scan(&r.ID, &r.LibraryID, &r.FolderID, &r.BookID, &r.Title, &r.RootPath, &r.Mode, &r.State, &r.ErrorCode,
			&r.PlanHash, &r.Files, &r.Bytes, &r.JobID, &r.RemovedBy, &r.CreatedAt, &r.UpdatedAt, &r.PurgeAfter); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// RemovalFiles lists the files and extras a removal moved (or would move), for its status page.
func (s *Service) RemovalFiles(ctx context.Context, rid string) ([]PlanFile, error) {
	rm, err := s.Removal(ctx, rid)
	if err != nil {
		return nil, err
	}
	folder, err := s.repo.Folder(ctx, rm.FolderID)
	if err != nil {
		return nil, err
	}
	paths, err := s.removalPaths(ctx, rid)
	if err != nil {
		return nil, err
	}
	var out []PlanFile
	for _, p := range paths {
		if p.kind != "dir" {
			out = append(out, PlanFile{Path: p.path, Rel: relSlash(folder.Path, p.path), Size: p.size})
		}
	}
	return out, nil
}

// RestoreRemoval puts a held removal back ("Put back"). Each file returns only when its old place is free; one
// whose place is taken stays in the holding folder and is named in the result. For a removal that kept the files
// it only lets the scanner see the folder again. A scan is queued either way, so the book comes back.
// Errors: 404 not_found, 409 not_held.
func (s *Service) RestoreRemoval(ctx context.Context, userID, rid string) (RestoreResult, error) {
	res := RestoreResult{Restored: []string{}, Kept: []string{}}
	l := s.lock("rmv:" + rid)
	l.Lock()
	defer l.Unlock()
	rm, err := s.Removal(ctx, rid)
	if err != nil {
		return res, err
	}
	if rm.State != RemovalHeld {
		return res, fail(409, "not_held", "These files are not waiting in the holding folder.")
	}
	folder, err := s.repo.Folder(ctx, rm.FolderID)
	if err != nil {
		return res, errNotRemovable("The folder this book was in is no longer part of the library.")
	}
	lr, err := openLroot(folder.Path)
	if err != nil {
		return res, err
	}
	defer lr.Close()
	if rm.Mode == ModeFiles {
		paths, err := s.removalPaths(ctx, rid)
		if err != nil {
			return res, err
		}
		for _, pr := range paths {
			if pr.kind == "dir" || pr.heldPath == "" {
				continue
			}
			if _, err := lr.Lstat(pr.heldPath); err != nil {
				continue // already gone from the holding folder
			}
			if _, err := lr.Lstat(pr.path); err == nil {
				res.Kept = append(res.Kept, pr.path)
				_, _ = s.st.Writer().ExecContext(ctx, `UPDATE removal_paths SET state = 'kept' WHERE removal_id = ? AND idx = ?`, rid, pr.idx)
				continue
			}
			if err := lr.MkdirAll(filepath.Dir(pr.path), 0o755); err != nil {
				res.Kept = append(res.Kept, pr.path)
				continue
			}
			if err := lr.Rename(pr.heldPath, pr.path); err != nil {
				s.log.Warn("upload: cannot put a file back", "removal", rid, "path", pr.path, "err", err)
				res.Kept = append(res.Kept, pr.path)
				continue
			}
			res.Restored = append(res.Restored, pr.path)
			_, _ = s.st.Writer().ExecContext(ctx, `UPDATE removal_paths SET state = 'restored' WHERE removal_id = ? AND idx = ?`, rid, pr.idx)
		}
		if len(res.Kept) == 0 {
			pruneEmpty(lr, filepath.Join(folder.Path, HoldingDir, rid))
			_ = lr.Remove(filepath.Join(folder.Path, HoldingDir))
		}
	}
	err = s.write(ctx, nil, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `UPDATE removals SET state = 'restored', updated_at = ?, purge_after = NULL WHERE id = ? AND state = 'held'`, s.nowMs(), rid); err != nil {
			return err
		}
		return s.audit(ctx, tx, userID, "removal.restore", "book:"+rm.BookID, map[string]any{"title": rm.Title, "removal": rid, "restored": len(res.Restored), "kept": len(res.Kept)})
	})
	if err != nil {
		return res, err
	}
	// A scan of the book's folder (or the whole library when the book sat at its top) brings the book back.
	payload := scan.ScanPayload{LibraryID: rm.LibraryID}
	sub := rm.RootPath
	if fi, err := lr.Lstat(sub); err != nil || !fi.IsDir() {
		sub = filepath.Dir(sub)
	}
	if rel := relSlash(folder.Path, sub); within(folder.Path, sub) && rel != "." {
		payload.FolderID, payload.Subdir = folder.ID, rel
	}
	if _, err := s.enq.Enqueue(ctx, scan.KindScan, payload, jobs.EnqueueOptions{CreatedBy: userID}); err != nil {
		s.log.Warn("upload: cannot queue the scan after a put back", "err", err)
	}
	return res, nil
}

// PurgeRemoval deletes a held removal's files for good ("Delete now", and the sweep after RemovedKeepDays). Only
// the holding folder of that removal is touched. Errors: 404 not_found, 409 not_held (also for a removal that kept
// the files, which has nothing to delete). userID is "" when the sweep calls it.
func (s *Service) PurgeRemoval(ctx context.Context, userID, rid string) error {
	l := s.lock("rmv:" + rid)
	l.Lock()
	defer l.Unlock()
	rm, err := s.Removal(ctx, rid)
	if err != nil {
		return err
	}
	if rm.State != RemovalHeld || rm.Mode != ModeFiles {
		return fail(409, "not_held", "These files are not waiting in the holding folder.")
	}
	folder, err := s.repo.Folder(ctx, rm.FolderID)
	if err != nil {
		return errNotRemovable("The folder this book was in is no longer part of the library.")
	}
	lr, err := openLroot(folder.Path)
	if err != nil {
		return err
	}
	defer lr.Close()
	dir := filepath.Join(folder.Path, HoldingDir, rid)
	if err := lr.RemoveAll(dir); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	_ = lr.Remove(filepath.Join(folder.Path, HoldingDir))
	return s.write(ctx, nil, func(tx *sql.Tx) error {
		now := s.nowMs()
		if _, err := tx.ExecContext(ctx, `UPDATE removals SET state = 'purged', updated_at = ? WHERE id = ?`, now, rid); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE removal_paths SET state = 'purged' WHERE removal_id = ? AND kind <> 'dir' AND state IN ('planned','held','kept')`, rid); err != nil {
			return err
		}
		return s.audit(ctx, tx, userID, "removal.purge", "book:"+rm.BookID, map[string]any{"title": rm.Title, "removal": rid, "files": rm.Files})
	})
}

// sweepRemovals runs with Sweep: it fails a removal that has been planned for a day with no live job (so the book
// can be removed again), puts back the files of a removal that has been 'moving' for an hour (the job died), and
// purges held removals past their keep period.
func (s *Service) sweepRemovals(ctx context.Context) error {
	now := s.cfg.Now()
	stuck, err := s.ids(ctx, `SELECT id FROM removals WHERE state = 'moving' AND updated_at < ?`, now.Add(-movingStale).UnixMilli())
	if err != nil {
		return err
	}
	for _, id := range stuck {
		l := s.lock("rmv:" + id)
		l.Lock()
		rm, err := s.Removal(ctx, id)
		if err == nil && rm.State == RemovalMoving {
			err = s.rollbackRemoval(ctx, rm, CodeInterrupted)
		}
		l.Unlock()
		if err != nil {
			s.log.Warn("upload: cannot put back an interrupted removal", "removal", id, "err", err)
		}
	}
	planned, err := s.ids(ctx, `SELECT id FROM removals WHERE state = 'planned' AND mode = 'files' AND updated_at < ?
  AND NOT EXISTS (SELECT 1 FROM jobs j WHERE j.id = removals.job_id AND j.state IN ('queued','running','held'))`, now.Add(-staleAfter).UnixMilli())
	if err != nil {
		return err
	}
	for _, id := range planned {
		if err := s.failRemoval(ctx, nil, id, "stuck", RemovalPlanned); err != nil {
			s.log.Warn("upload: cannot fail a stuck removal", "removal", id, "err", err)
		}
	}
	due, err := s.ids(ctx, `SELECT id FROM removals WHERE state = 'held' AND mode = 'files' AND purge_after IS NOT NULL AND purge_after <= ?`, now.UnixMilli())
	if err != nil {
		return err
	}
	for _, id := range due {
		if err := s.PurgeRemoval(ctx, "", id); err != nil {
			s.log.Warn("upload: cannot delete the files of an old removal", "removal", id, "err", err)
		}
	}
	return nil
}
