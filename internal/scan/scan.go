package scan

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
)

// Enqueuer is the part of jobs.Runner the scanner uses to queue probes.
type Enqueuer interface {
	Enqueue(ctx context.Context, kind string, payload any, o jobs.EnqueueOptions) (string, error)
	ActiveKeys(ctx context.Context, kind, field string) (map[string]bool, error)
}

// Scanner turns the files of a library's folders into books and queues their probes.
type Scanner struct {
	repo  *library.Repo
	tools media.Tools
	jobs  Enqueuer
	log   *slog.Logger
	now   func() time.Time

	followers []Follower
	finishers []Finisher
	sinks     []EbookSink

	afterProbe func() // test hook: runs between ffprobe and the write transaction
}

// New returns a Scanner. tools may lack ffprobe: files are still grouped into books, and every
// probe job then fails with ffprobe_missing.
func New(repo *library.Repo, tools media.Tools, jobs Enqueuer, log *slog.Logger) *Scanner {
	if log == nil {
		log = slog.Default()
	}
	return &Scanner{repo: repo, tools: tools, jobs: jobs, log: log, now: time.Now}
}

// Follower is told which books just settled: every file has an answer and the book's order,
// duration and chapters are written. It runs after the write transaction has committed, so it may
// read the book. Followers log their own errors and must not call into scan.
type Follower func(ctx context.Context, bookIDs []string)

// OnBookSettled adds a follower. Call it only before Register.
func (s *Scanner) OnBookSettled(f Follower) { s.followers = append(s.followers, f) }

// Finisher is told which library a scan has just finished, after the followers. It is how a feature
// catches up on books the scan found unchanged (so no follower heard of them), for example covers
// stored before a better colour counter existed. Finishers log their own errors.
type Finisher func(ctx context.Context, libraryID string)

// OnScanFinished adds a finisher. Call it only before Register.
func (s *Scanner) OnScanFinished(f Finisher) { s.finishers = append(s.finishers, f) }

// notify tells every follower, in registration order, about the sorted, distinct book ids.
func (s *Scanner) notify(ctx context.Context, ids []string) {
	if len(ids) == 0 || len(s.followers) == 0 {
		return
	}
	ids = append([]string(nil), ids...)
	sort.Strings(ids)
	out := ids[:0]
	for i, id := range ids {
		if i == 0 || id != ids[i-1] {
			out = append(out, id)
		}
	}
	for _, f := range s.followers {
		f(ctx, out)
	}
}

// Result is what one library scan did. It is stored as the scan job's result.
type Result struct {
	Folders         int `json:"folders"`
	FoldersSkipped  int `json:"folders_skipped"`
	FilesSeen       int `json:"files_seen"`
	FilesAdded      int `json:"files_added"`
	FilesMoved      int `json:"files_moved"`
	FilesChanged    int `json:"files_changed"`
	FilesMissing    int `json:"files_missing"`
	FilesRestored   int `json:"files_restored"`
	BooksAdded      int `json:"books_added"`
	BooksMoved      int `json:"books_moved"`
	BooksMissing    int `json:"books_missing"`
	BooksRestored   int `json:"books_restored"`
	BooksRefused    int `json:"books_refused"`
	SymlinksSkipped int `json:"symlinks_skipped"`
	UnreadableDirs  int `json:"unreadable_dirs"`
	ProbesEnqueued  int `json:"probes_enqueued"`
	BooksSettled    int `json:"books_settled"`
}

// RefusalText is the plain sentence for a book's refused reason, empty for any other reason.
func RefusalText(reason string) string {
	switch reason {
	case library.RefusedDRM:
		return media.ErrorText(media.CodeDRM)
	case library.RefusedDamaged:
		return media.ErrorText(media.CodeNotAudio)
	}
	return ""
}

type walkedFolder struct {
	folder library.Folder
	files  []Found
}

// plan is everything the scan learned outside the transaction.
type plan struct {
	libraryID string
	walked    []walkedFolder
	hashes    map[string]string // quick hash of files new or changed since the last scan, by path
	skip      []string          // paths and folders whose absence proves nothing
	scope     string            // when set, only rows under this path can vanish (ScanSubtree)
	start     int64
	settled   []string      // books finalize settled in the apply transaction
	ebooks    []EbookFolder // ebook files per walked folder, handed to the sinks after the write
}

func underAny(path string, prefixes []string) bool {
	for _, p := range prefixes {
		if path == p || strings.HasPrefix(path, p+string(filepath.Separator)) {
			return true
		}
	}
	return false
}

// ScanLibrary walks every folder of the library and brings the database in line: new files and
// books are added, moved and renamed files and folders keep their IDs, changed files are reset for
// probing, and vanished files are marked missing, never deleted. Walking and hashing happen before
// one write transaction that starts with guard (a job's fence check; nil is allowed). A folder that
// cannot be read, or that suddenly shows no files at all, changes nothing. A scan that finds
// nothing new writes nothing and queues nothing.
func (s *Scanner) ScanLibrary(ctx context.Context, libraryID string, guard func(context.Context, *sql.Tx) error) (Result, error) {
	return s.scanScoped(ctx, libraryID, nil, guard)
}

// subtree is the part of a library folder a scoped scan reads (decision 0015, the import of one
// uploaded book).
type subtree struct {
	folder library.Folder
	subdir string // slash form, below folder.Path
	root   string // folder.Path joined with subdir
}

// ScanSubtree is ScanLibrary for one directory (subdir, slash form) of one folder of the library:
// the same walk, grouping, hashing, write transaction, probes, followers and finishers, but only the
// files under that directory are read and only rows under it can be marked missing, so nothing
// else in the library changes. A subdir that is unavailable or holds no files changes nothing.
func (s *Scanner) ScanSubtree(ctx context.Context, libraryID, folderID, subdir string, guard func(context.Context, *sql.Tx) error) (Result, error) {
	var res Result
	fo, err := s.repo.Folder(ctx, folderID)
	if err != nil {
		return res, err
	}
	if fo.LibraryID != libraryID {
		return res, fmt.Errorf("%w: folder", library.ErrNotFound)
	}
	clean := path.Clean("/" + subdir)
	if subdir == "" || clean == "/" {
		return s.scanScoped(ctx, libraryID, nil, guard) // no subdir: the whole library, as before
	}
	rel := strings.TrimPrefix(clean, "/")
	return s.scanScoped(ctx, libraryID, &subtree{folder: fo, subdir: rel, root: filepath.Join(fo.Path, filepath.FromSlash(rel))}, guard)
}

func (s *Scanner) scanScoped(ctx context.Context, libraryID string, sc *subtree, guard func(context.Context, *sql.Tx) error) (Result, error) {
	var res Result
	if _, err := s.repo.Library(ctx, libraryID); err != nil {
		return res, err
	}
	folders, err := s.repo.Folders(ctx, libraryID)
	if err != nil {
		return res, err
	}
	p := &plan{libraryID: libraryID, hashes: map[string]string{}, start: s.now().UnixMilli()}
	if sc != nil {
		folders = []library.Folder{sc.folder}
		p.scope = sc.root
	}
	res.Folders = len(folders)

	for _, fo := range folders {
		walkRoot := fo.Path
		if sc != nil {
			walkRoot = sc.root
		}
		all, st, werr := walk(ctx, walkRoot)
		files, ebooks := splitEbooks(all)
		if sc != nil {
			for i := range files {
				files[i].RelDir = path.Join(sc.subdir, files[i].RelDir)
			}
			for i := range ebooks {
				ebooks[i].RelDir = path.Join(sc.subdir, ebooks[i].RelDir)
			}
		}
		if ctx.Err() != nil {
			return res, context.Cause(ctx)
		}
		pre, err := s.repo.AudioFilesInFolder(ctx, fo.ID)
		if err != nil {
			return res, err
		}
		live := false
		known := make(map[string]*library.AudioFile, len(pre))
		for i := range pre {
			known[pre[i].Path] = &pre[i]
			live = live || pre[i].MissingSince == 0
		}
		switch {
		case errors.Is(werr, errFolderUnavailable), werr == nil && len(files) == 0 && (live || sc != nil):
			s.log.Warn("scan: folder unavailable or suddenly empty; nothing under it changes", "folder", walkRoot)
			p.skip = append(p.skip, walkRoot)
			res.FoldersSkipped++
			continue
		case werr != nil:
			return res, werr
		}
		p.ebooks = append(p.ebooks, EbookFolder{FolderID: fo.ID, Path: fo.Path, Files: ebooks})
		res.SymlinksSkipped += st.Symlinks
		res.UnreadableDirs += len(st.Unreadable)
		p.skip = append(p.skip, st.Unreadable...)

		kept := files[:0]
		for _, f := range files {
			if ctx.Err() != nil {
				return res, context.Cause(ctx)
			}
			if r, ok := known[f.Path]; ok && r.Size == f.Size && r.MtimeNs == f.MtimeNs {
				kept = append(kept, f)
				continue
			}
			h, size, herr := media.QuickHash(f.Path)
			if herr != nil { // vanished since the walk, or not a regular file now
				p.skip = append(p.skip, f.Path)
				continue
			}
			f.Size = size
			p.hashes[f.Path] = h
			kept = append(kept, f)
		}
		res.FilesSeen += len(kept)
		p.walked = append(p.walked, walkedFolder{folder: fo, files: kept})
	}

	var applied Result
	err = s.repo.UpdateGuarded(ctx, guard, func(tx *library.Tx) error {
		var err error
		p.settled = nil // the transaction may be retried
		applied, err = s.apply(tx, p)
		return err
	})
	if err != nil {
		return res, err
	}
	res.FilesAdded, res.FilesMoved, res.FilesChanged = applied.FilesAdded, applied.FilesMoved, applied.FilesChanged
	res.FilesMissing, res.FilesRestored = applied.FilesMissing, applied.FilesRestored
	res.BooksAdded, res.BooksMoved = applied.BooksAdded, applied.BooksMoved
	res.BooksMissing, res.BooksRestored = applied.BooksMissing, applied.BooksRestored

	refused, err := s.repo.RefusedBooks(ctx, libraryID)
	if err != nil {
		return res, err
	}
	res.BooksRefused = len(refused)

	if len(s.sinks) > 0 {
		es := EbookScan{LibraryID: libraryID, Scope: p.scope, Skip: p.skip, Folders: p.ebooks}
		for _, sink := range s.sinks {
			sink(ctx, es)
		}
	}

	n, err := s.enqueueProbes(ctx, libraryID)
	res.ProbesEnqueued = n
	if err != nil {
		return res, err
	}
	res.BooksSettled = len(p.settled)
	s.notify(ctx, p.settled)
	for _, f := range s.finishers {
		f(ctx, libraryID)
	}
	s.log.Info("scan finished", "library", libraryID, "files", res.FilesSeen, "added", res.FilesAdded,
		"moved", res.FilesMoved, "changed", res.FilesChanged, "missing", res.FilesMissing,
		"books_added", res.BooksAdded, "probes", res.ProbesEnqueued)
	return res, nil
}

// enqueueProbes queues a probe for every live file that has no answer and no waiting probe job.
// Doing this after every scan makes the system crash-safe: a lost job is simply queued again.
func (s *Scanner) enqueueProbes(ctx context.Context, libraryID string) (int, error) {
	ids, err := s.repo.UnsettledAudioFileIDs(ctx, libraryID)
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	active, err := s.jobs.ActiveKeys(ctx, KindProbe, "audio_file_id")
	if err != nil {
		return 0, err
	}
	n := 0
	for _, id := range ids {
		if active[id] {
			continue
		}
		if _, err := s.jobs.Enqueue(ctx, KindProbe, ProbePayload{AudioFileID: id}, jobs.EnqueueOptions{}); err != nil {
			return n, fmt.Errorf("queue probe: %w", err)
		}
		n++
	}
	return n, nil
}

// item is one file the walk found, with the row it maps to (if any).
type item struct {
	f      Found
	folder library.Folder
	hash   string
	row    *library.AudioFile
	moved  bool // the row was matched by hash and got this path
}

type bookGroup struct {
	Group
	folder library.Folder
}

// apply reads the library's rows inside tx, diffs them against the walk and writes only the
// differences. It returns the counters it can know.
func (s *Scanner) apply(tx *library.Tx, p *plan) (Result, error) {
	var res Result
	books, err := tx.BooksInLibrary(p.libraryID)
	if err != nil {
		return res, err
	}
	fileRows, err := tx.AudioFilesInLibrary(p.libraryID)
	if err != nil {
		return res, err
	}
	bookByRoot := make(map[string]*library.Book, len(books))
	bookByID := make(map[string]*library.Book, len(books))
	for i := range books {
		bookByRoot[books[i].RootPath] = &books[i]
		bookByID[books[i].ID] = &books[i]
	}
	rows := make([]*library.AudioFile, len(fileRows))
	rowByPath := make(map[string]*library.AudioFile, len(fileRows))
	for i := range fileRows {
		rows[i] = &fileRows[i]
		rowByPath[fileRows[i].Path] = &fileRows[i]
	}
	dirty := map[*library.AudioFile]bool{}
	touched := map[string]bool{}

	// The files found, in path order.
	var items []*item
	byPath := map[string]*item{}
	for _, w := range p.walked {
		for _, f := range w.files {
			it := &item{f: f, folder: w.folder, hash: p.hashes[f.Path]}
			items = append(items, it)
			byPath[f.Path] = it
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].f.Path < items[j].f.Path })

	// 1. Path match: same path, same row.
	for _, it := range items {
		row := rowByPath[it.f.Path]
		if row == nil {
			continue
		}
		it.row = row
		if _, hashed := p.hashes[it.f.Path]; hashed && (row.Size != it.f.Size || row.MtimeNs != it.f.MtimeNs) {
			row.Size, row.MtimeNs, row.QuickHash = it.f.Size, it.f.MtimeNs, it.hash
			row.ProbeJSON, row.ProbeError, row.Encrypted = nil, "", false
			protect(row)
			dirty[row] = true
			touched[row.BookID] = true
			res.FilesChanged++
		}
		if row.MissingSince != 0 {
			row.MissingSince = 0
			dirty[row] = true
			touched[row.BookID] = true
			res.FilesRestored++
		}
	}

	// 2. Hash match: a found path with no row takes the identity of a vanished row with the same hash.
	var vanished []*library.AudioFile
	for _, r := range rows {
		if byPath[r.Path] == nil && !underAny(r.Path, p.skip) && (p.scope == "" || underAny(r.Path, []string{p.scope})) {
			vanished = append(vanished, r)
		}
	}
	sort.SliceStable(vanished, func(i, j int) bool {
		li, lj := vanished[i].MissingSince == 0, vanished[j].MissingSince == 0
		if li != lj {
			return li
		}
		return vanished[i].Path < vanished[j].Path
	})
	byHash := map[string][]*library.AudioFile{}
	for _, r := range vanished {
		byHash[r.QuickHash] = append(byHash[r.QuickHash], r)
	}
	taken := map[*library.AudioFile]bool{}
	oldBook := map[*library.AudioFile]string{}
	for _, it := range items {
		if it.row != nil {
			continue
		}
		list := byHash[it.hash]
		if len(list) == 0 {
			continue
		}
		row := list[0]
		byHash[it.hash] = list[1:]
		taken[row] = true
		it.row, it.moved = row, true
		oldBook[row] = row.BookID
		delete(rowByPath, row.Path)
		row.Path, row.Size, row.MtimeNs = it.f.Path, it.f.Size, it.f.MtimeNs
		rowByPath[row.Path] = row
		dirty[row] = true
		touched[row.BookID] = true
		res.FilesMoved++
		if row.MissingSince != 0 {
			row.MissingSince = 0
			res.FilesRestored++
		}
	}

	// 3. Books: one per group.
	var groups []bookGroup
	groupRoots := map[string]bool{}
	for _, w := range p.walked {
		for _, g := range GroupFiles(w.folder.Path, w.files) {
			groups = append(groups, bookGroup{Group: g, folder: w.folder})
			groupRoots[g.Root] = true
		}
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Root < groups[j].Root })
	claimed := map[string]bool{}
	for _, g := range groups {
		book := bookByRoot[g.Root]
		if book == nil {
			counts := map[string]int{}
			for _, f := range g.Files {
				if it := byPath[f.Path]; it.moved {
					counts[oldBook[it.row]]++
				}
			}
			cands := make([]string, 0, len(counts))
			for id := range counts {
				cands = append(cands, id)
			}
			sort.Slice(cands, func(i, j int) bool {
				if counts[cands[i]] != counts[cands[j]] {
					return counts[cands[i]] > counts[cands[j]]
				}
				return cands[i] < cands[j]
			})
			for _, id := range cands {
				if b := bookByID[id]; b != nil && !claimed[id] && !groupRoots[b.RootPath] {
					book = b
					break
				}
			}
			if book != nil {
				oldBase := filepath.Base(book.RootPath)
				if book.Title == oldBase || book.Title == stem(oldBase) {
					book.Title, book.SortTitle = g.Title, g.Title
				}
				delete(bookByRoot, book.RootPath)
				book.RootPath, book.FolderID = g.Root, g.folder.ID
				bookByRoot[g.Root] = book
				if err := tx.UpdateBook(*book); err != nil {
					return res, err
				}
				touched[book.ID] = true
				res.BooksMoved++
			}
		}
		if book == nil {
			nb := &library.Book{LibraryID: p.libraryID, FolderID: g.folder.ID, RootPath: g.Root, Title: g.Title, SortTitle: g.Title}
			if err := tx.InsertBook(nb); err != nil {
				return res, err
			}
			book = nb
			bookByRoot[g.Root], bookByID[nb.ID] = nb, nb
			res.BooksAdded++
		}
		claimed[book.ID] = true
		for _, f := range g.Files {
			it := byPath[f.Path]
			switch {
			case it.row == nil:
				row := &library.AudioFile{BookID: book.ID, Path: f.Path, Size: f.Size, MtimeNs: f.MtimeNs, QuickHash: it.hash}
				protect(row)
				rows = append(rows, row)
				dirty[row] = true
				touched[book.ID] = true
				res.FilesAdded++
			default:
				if it.moved {
					touched[book.ID] = true
				}
				if it.row.BookID != book.ID {
					touched[it.row.BookID], touched[book.ID] = true, true
					it.row.BookID = book.ID
					dirty[it.row] = true
				}
			}
		}
	}

	// 4. Vanished rows that no moved file took are missing now.
	for _, r := range vanished {
		if !taken[r] && r.MissingSince == 0 {
			r.MissingSince = p.start
			dirty[r] = true
			touched[r.BookID] = true
			res.FilesMissing++
		}
	}

	// 5. Provisional play order for books that still wait for a probe.
	perBook := map[string][]*library.AudioFile{}
	for _, r := range rows {
		perBook[r.BookID] = append(perBook[r.BookID], r)
	}
	for id := range touched {
		b := bookByID[id]
		if b == nil {
			continue
		}
		var live []*library.AudioFile
		waiting := false
		for _, r := range perBook[id] {
			if r.MissingSince == 0 {
				live = append(live, r)
				waiting = waiting || !settled(r)
			}
		}
		if !waiting {
			continue
		}
		fs := make([]Found, len(live))
		rowOf := make(map[string]*library.AudioFile, len(live))
		for i, r := range live {
			key := relKey(b.RootPath, r.Path)
			dir := filepath.ToSlash(filepath.Dir(key))
			if dir == "." {
				dir = ""
			}
			fs[i] = Found{Path: r.Path, RelDir: dir, Name: filepath.Base(r.Path)}
			rowOf[r.Path] = r
		}
		provisionalOrder(b.Title, fs)
		for i, f := range fs {
			if r := rowOf[f.Path]; r.Idx != i {
				r.Idx = i
				dirty[r] = true
			}
		}
	}

	// 6. Write the files, then settle books.
	for _, r := range rows {
		if dirty[r] {
			if err := tx.PutAudioFile(r); err != nil {
				return res, err
			}
		}
	}
	ids := make([]string, 0, len(bookByID))
	for id := range bookByID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var final []string
	for _, id := range ids {
		b := bookByID[id]
		total, liveN := len(perBook[id]), 0
		for _, r := range perBook[id] {
			if r.MissingSince == 0 {
				liveN++
			}
		}
		switch {
		case total == 0:
			if touched[id] {
				if _, err := tx.DeleteEmptyBook(id); err != nil {
					return res, err
				}
			}
			continue
		case liveN == 0 && b.MissingSince == 0:
			if err := tx.SetBookMissing(id, p.start); err != nil {
				return res, err
			}
			res.BooksMissing++
		case liveN > 0 && b.MissingSince != 0:
			if err := tx.SetBookMissing(id, 0); err != nil {
				return res, err
			}
			res.BooksRestored++
		}
		if touched[id] {
			final = append(final, id)
		}
	}
	for _, id := range final {
		ok, err := finalize(tx, id, p.start)
		if err != nil {
			return res, err
		}
		if ok {
			p.settled = append(p.settled, id)
		}
	}
	return res, nil
}

// protect marks a row of a copy-protected extension as refused at once: it is never probed.
func protect(r *library.AudioFile) {
	if media.Classify(r.Path) == media.KindProtected {
		r.Encrypted, r.ProbeError = true, media.CodeDRM
	}
}
