package mediajobs

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

// KindWriteDetails is the job that writes a book's details into its audio files.
const KindWriteDetails = "media.write_details"

// WritePayload is the payload of a media.write_details job.
type WritePayload struct {
	BookID string `json:"book_id"`
}

// Names of the two hidden siblings of a file being written (decision S9). They start with a dot
// and have no audio extension, so the scan, and other apps reading the folder, never see them.
const (
	tempSuffix   = ".tendshelf-new"
	backupSuffix = ".tendshelf-before"
)

// writeSettle is how long a file must have been left alone before Tendshelf rewrites it, in case
// something else is still copying or downloading it.
const writeSettle = 10 * time.Minute

// Codes of a refused or failed write.
const (
	WriteBusy        = "busy"
	WriteNoFiles     = "no_files"
	WriteProtected   = "protected"
	WriteUnsupported = "unsupported"
	WriteMissing     = "missing"
	WriteRecent      = "recent"
	WriteNoSpace     = "no_space"
	WriteLeftover    = "leftover"
	WriteChanged     = "changed"
	WriteCopyFailed  = "write_failed"
	WriteSwapFailed  = "swap_failed"
	WriteInterrupted = "interrupted"
	WriteAttention   = "needs_attention"
	VerifyAudio      = "verify_audio"
	VerifyDuration   = "verify_duration"
	VerifyChapters   = "verify_chapters"
	VerifyTags       = "verify_tags"
)

// writeText is the plain-words text of each code.
var writeText = map[string]string{
	WriteBusy:        "Tendshelf is already working on this book's files.",
	WriteNoFiles:     "This book has no audio files to write details into.",
	WriteProtected:   "This book's files are copy-protected or unreadable, so Tendshelf will not change them.",
	WriteUnsupported: "Tendshelf can write details into m4b, m4a, mp3 and FLAC files only.",
	WriteMissing:     "A file of this book is missing from its folder.",
	WriteRecent:      "A file of this book changed in the last 10 minutes, so Tendshelf is waiting in case it is still being copied.",
	WriteNoSpace:     "There is not enough free space next to the book's files to write a copy.",
	WriteLeftover:    "A working copy or a backup from an earlier write is still next to this book's files. Choose Looks good or Undo for it first.",
	WriteChanged:     "The file changed while Tendshelf was copying it, so nothing was replaced.",
	WriteCopyFailed:  "Tendshelf could not write the details into a copy of the file, so the original was left as it was.",
	WriteSwapFailed:  "Tendshelf could not put the new file in place, so the original was left as it was.",
	WriteInterrupted: "Writing was interrupted, so the original was left as it was.",
	WriteAttention:   "This file changed after the details were written, so Tendshelf kept the backup and did nothing more. Please look at the files in the folder.",
	VerifyAudio:      "The copy's sound did not match the original, so the original was left as it was.",
	VerifyDuration:   "The copy's length did not match the original, so the original was left as it was.",
	VerifyChapters:   "The copy's chapters did not match what was meant to be written, so the original was left as it was.",
	VerifyTags:       "The copy's details did not match what was meant to be written, so the original was left as it was.",
}

// WriteRefusal is a reason a book's files cannot be written. It is also an error whose text is the plain
// words to show.
type WriteRefusal struct {
	AudioFileID, Path string
	Code, Text        string
}

func (r *WriteRefusal) Error() string { return r.Text }

func refuse(f library.AudioFile, code string) WriteRefusal {
	return WriteRefusal{AudioFileID: f.ID, Path: f.Path, Code: code, Text: writeText[code]}
}

// Canceller is the part of jobs.Runner that recovery uses.
type Canceller interface {
	Cancel(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (jobs.Job, error)
	ActiveFor(ctx context.Context, kind, field, value string) (jobs.Job, bool, error)
}

// Writes writes a book's details into its audio files and looks after the result. With Confirm and
// Undo it is the only code that renames, writes or deletes anything in a library folder, and it
// touches only a file's own path and its two hidden siblings. The ledger row (file_writes) is
// committed before each file-system step, so after a crash the row names the step that may be half
// done; Recover decides from the files and their hashes alone and never runs ffmpeg.
type Writes struct {
	repo   *library.Repo
	tools  media.Tools
	data   string
	jobs   Enqueuer
	runner Canceller
	log    *slog.Logger
	now    func() time.Time
	free   func(dir string) (uint64, error)

	// OnChanged is called with a library id after files of that library were swapped or restored,
	// so it is scanned again. Nil means nobody is told.
	OnChanged func(ctx context.Context, libraryID string)

	mu     sync.Mutex      // guards active
	active map[string]bool // books this process is writing, confirming or undoing

	// crash is for tests only: it is called at named points between steps, and an error it returns
	// stops the operation on the spot, as a crash would, with no clean-up.
	crash func(point string) error
}

// NewWrites returns the write service. data is the data directory; the chapter files for ffmpeg go
// under it.
func NewWrites(repo *library.Repo, tools media.Tools, data string, enq Enqueuer, runner Canceller, log *slog.Logger) *Writes {
	if log == nil {
		log = slog.Default()
	}
	return &Writes{repo: repo, tools: tools, data: data, jobs: enq, runner: runner, log: log,
		now: time.Now, free: media.FreeBytes, active: map[string]bool{}}
}

// WritesDir is where the chapter files for ffmpeg are made, outside every library.
func WritesDir(data string) string { return filepath.Join(data, "tmp", "writes") }

func (w *Writes) at(point string) error {
	if w.crash != nil {
		return w.crash(point)
	}
	return nil
}

// begin marks a book busy in this process; the returned func releases it.
func (w *Writes) begin(bookID string) (func(), error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.active[bookID] {
		return nil, &WriteRefusal{Code: WriteBusy, Text: writeText[WriteBusy]}
	}
	w.active[bookID] = true
	return func() {
		w.mu.Lock()
		delete(w.active, bookID)
		w.mu.Unlock()
	}, nil
}

func (w *Writes) isActive(bookID string) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.active[bookID]
}

// everything is the scope of a job: it may see any book.
var everything = library.Scope{Books: share.Filter{SQL: "1=1"}, Libraries: share.Filter{SQL: "1=1"}}

func hiddenNames(original string) (temp, backup string) {
	dir, base := filepath.Split(original)
	return filepath.Join(dir, "."+base+tempSuffix), filepath.Join(dir, "."+base+backupSuffix)
}

func present(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// Preflight lists why the book's files cannot be written now; an empty list means they can. It
// reads only.
func (w *Writes) Preflight(ctx context.Context, bookID string) ([]WriteRefusal, error) {
	book, err := w.repo.Book(ctx, bookID)
	if err != nil {
		return nil, err
	}
	files, err := w.repo.AudioFiles(ctx, bookID)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return []WriteRefusal{{Code: WriteNoFiles, Text: writeText[WriteNoFiles]}}, nil
	}
	open, err := w.repo.OpenFileWrites(ctx)
	if err != nil {
		return nil, err
	}
	openPaths := map[string]bool{}
	for _, o := range open {
		openPaths[o.OriginalPath] = true
	}
	var out []WriteRefusal
	need := map[string]int64{} // bytes of copies needed per folder: the backups stay, so they add up
	for _, f := range files {
		temp, backup := hiddenNames(f.Path)
		st, lerr := os.Lstat(f.Path)
		switch {
		case book.RefusedReason != "" || f.Encrypted || f.ProbeError != "":
			out = append(out, refuse(f, WriteProtected))
		case f.MissingSince != 0 || lerr != nil:
			out = append(out, refuse(f, WriteMissing))
		case !st.Mode().IsRegular():
			out = append(out, refuse(f, WriteUnsupported))
		default:
			if _, ok := media.WriteFormat(f.Container, f.Path); !ok {
				out = append(out, refuse(f, WriteUnsupported))
				continue
			}
			if w.now().Sub(st.ModTime()) < writeSettle {
				out = append(out, refuse(f, WriteRecent))
			}
			if present(temp) || present(backup) || openPaths[f.Path] {
				out = append(out, refuse(f, WriteLeftover))
			}
			need[filepath.Dir(f.Path)] += st.Size() + st.Size()/20
		}
	}
	for dir, n := range need {
		free, ferr := w.free(dir)
		if errors.Is(ferr, media.ErrNoSpaceInfo) {
			continue // the platform cannot say; do not refuse on a guess
		}
		if ferr != nil || free < uint64(n) {
			f := library.AudioFile{Path: dir}
			out = append(out, refuse(f, WriteNoSpace))
		}
	}
	return out, nil
}

// Start checks the book and queues the write. A refusal is returned as a *WriteRefusal with its plain
// text; nothing is written then.
func (w *Writes) Start(ctx context.Context, bookID, userID string) (string, error) {
	refs, err := w.Preflight(ctx, bookID)
	if err != nil {
		return "", err
	}
	if len(refs) > 0 {
		return "", &refs[0]
	}
	keys, err := w.jobs.ActiveKeys(ctx, KindWriteDetails, "book_id")
	if err != nil {
		return "", err
	}
	if keys[bookID] || w.isActive(bookID) {
		return "", &WriteRefusal{Code: WriteBusy, Text: writeText[WriteBusy]}
	}
	return w.jobs.Enqueue(ctx, KindWriteDetails, WritePayload{BookID: bookID}, jobs.EnqueueOptions{CreatedBy: userID})
}

// bookInfo maps a book page's data to the details that go into the file.
func bookInfo(d library.BookDetail) media.BookInfo {
	info := media.BookInfo{
		Title: d.Book.Title, Subtitle: d.Book.Subtitle, Publisher: d.Book.Publisher, Description: d.Book.Description,
		Language: d.Book.Language, ASIN: d.Book.ASIN, ISBN: d.Book.ISBN, Year: d.Book.PublishedYear,
		Genres: d.Genres,
	}
	for _, p := range d.Card.Authors {
		info.Authors = append(info.Authors, p.Name)
	}
	for _, p := range d.Card.Narrators {
		info.Narrators = append(info.Narrators, p.Name)
	}
	if len(d.Series) > 0 {
		info.Series, info.Sequence = d.Series[0].Name, d.Series[0].Sequence
	}
	return info
}

// handle is the media.write_details handler.
func (w *Writes) handle(ctx context.Context, t *jobs.Task) (any, error) {
	var p WritePayload
	if err := json.Unmarshal(t.Payload, &p); err != nil || p.BookID == "" {
		return nil, badPayload(err)
	}
	end, err := w.begin(p.BookID)
	if err != nil {
		return nil, permanent(WriteBusy)
	}
	defer end()
	// The refusals are checked again here: time passed between the click and the run.
	refs, err := w.Preflight(ctx, p.BookID)
	if err != nil {
		return nil, err
	}
	if len(refs) > 0 {
		return nil, permanent(refs[0].Code)
	}
	detail, err := w.repo.BookDetail(ctx, everything, p.BookID)
	if err != nil {
		return nil, err
	}
	files, err := w.repo.AudioFiles(ctx, p.BookID)
	if err != nil {
		return nil, err
	}
	createdBy := ""
	if j, err := w.runner.Get(ctx, t.ID); err == nil {
		createdBy = j.CreatedBy
	}
	multi := len(files) > 1
	job := writeJob{
		task: t, bookID: p.BookID, createdBy: createdBy,
		tags: media.BookTags(bookInfo(detail), multi),
	}
	if !multi {
		for _, c := range detail.Chapters {
			job.chapters = append(job.chapters, media.WriteChapter{StartMs: c.StartMs, EndMs: c.EndMs, Title: c.Title})
		}
	}
	done, failedCode := 0, ""
	for i, f := range files {
		t.Progress(map[string]int{"file": i + 1, "of": len(files)})
		code, err := w.writeOne(ctx, job, f)
		if err != nil {
			return nil, err
		}
		if code != "" {
			failedCode = code
			break
		}
		done++
	}
	if done > 0 && w.OnChanged != nil {
		w.OnChanged(ctx, detail.Book.LibraryID)
	}
	if failedCode != "" {
		return nil, permanent(failedCode)
	}
	return map[string]int{"files": done}, nil
}

func permanent(code string) error {
	return &jobs.Error{Code: code, Text: writeText[code], Permanent: true}
}

// writeJob is what the files of one run share.
type writeJob struct {
	task      *jobs.Task
	bookID    string
	createdBy string
	tags      map[string]string
	chapters  []media.WriteChapter
}

// guard is the fence check for the rows a job changes; nil outside a job.
func (j writeJob) guard() func(context.Context, *sql.Tx) error {
	if j.task == nil {
		return nil
	}
	return j.task.Fence
}

// move changes a row's state, in one transaction with the fence check and any audit entry.
func (w *Writes) move(ctx context.Context, guard func(context.Context, *sql.Tx) error, row library.FileWrite, from, to string, after *string, code string, audit *db.AuditEntry) error {
	return w.repo.UpdateGuarded(ctx, guard, func(tx *library.Tx) error {
		if err := tx.SetFileWriteState(row.ID, from, to, after, code); err != nil {
			return err
		}
		if audit != nil {
			audit.At = w.now().UnixMilli()
			return tx.Audit(*audit)
		}
		return nil
	})
}

// writeOne writes one file. It returns a code when the file was refused or failed cleanly (the
// original is as it was), or an error when the run must stop where it is: a crash, a lost lease or
// a cancelled context. After such an error nothing is cleaned up; Recover does that.
func (w *Writes) writeOne(ctx context.Context, job writeJob, f library.AudioFile) (string, error) {
	orig := f.Path
	temp, backup := hiddenNames(orig)
	st0, err := os.Lstat(orig)
	if err != nil {
		return WriteMissing, nil
	}
	sp, err := w.tools.Probe(ctx, orig)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return WriteProtected, nil
	}
	format, ok := media.WriteFormat(sp.Container, orig)
	if !ok {
		return WriteUnsupported, nil
	}
	beforeSHA, err := media.FileSHA256(orig)
	if err != nil {
		return WriteMissing, nil
	}
	audioHash, err := w.tools.AudioHash(ctx, orig)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return WriteProtected, nil
	}
	row := library.FileWrite{
		ID: library.NewFileWriteID(), JobID: job.task.ID, BookID: job.bookID, AudioFileID: f.ID, State: library.WriteCopying,
		OriginalPath: orig, BackupPath: backup, TempPath: temp, BeforeSHA256: beforeSHA, AudioHash: audioHash,
		CreatedBy: job.createdBy,
	}
	err = w.repo.UpdateGuarded(ctx, job.guard(), func(tx *library.Tx) error { return tx.InsertFileWrite(&row) })
	if errors.Is(err, library.ErrConflict) {
		return WriteLeftover, nil
	}
	if err != nil {
		return "", err
	}
	if err := w.at("copied0"); err != nil {
		return "", err
	}

	// fail removes the temp file and closes the row; the original was never touched.
	fail := func(from, code string) (string, error) {
		os.Remove(temp)
		if err := w.move(ctx, job.guard(), row, from, library.WriteFailed, nil, code, nil); err != nil {
			return "", err
		}
		return code, nil
	}

	tw := media.TagWrite{Format: format, Tags: job.tags}
	if format != "flac" && len(job.chapters) > 0 {
		tw.WriteChapters, tw.Chapters = true, job.chapters
	}
	if err := os.MkdirAll(WritesDir(w.data), 0o700); err != nil {
		return "", err
	}
	if err := w.tools.WriteTags(ctx, orig, temp, WritesDir(w.data), tw); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		w.log.Warn("write details: ffmpeg failed", "file", f.ID, "err", err)
		return fail(library.WriteCopying, WriteCopyFailed)
	}
	if err := os.Chmod(temp, st0.Mode().Perm()); err != nil {
		w.log.Warn("write details: chmod", "err", err)
	}
	if err := syncFile(temp); err != nil {
		w.log.Warn("write details: fsync", "err", err)
		return fail(library.WriteCopying, WriteCopyFailed)
	}
	if err := w.move(ctx, job.guard(), row, library.WriteCopying, library.WriteVerifying, nil, "", nil); err != nil {
		return "", err
	}
	if err := w.at("verifying"); err != nil {
		return "", err
	}
	code, err := w.verify(ctx, temp, row.AudioHash, sp, tw)
	if err != nil {
		return "", err
	}
	if code != "" {
		return fail(library.WriteVerifying, code)
	}
	after, err := media.FileSHA256(temp)
	if err != nil {
		return fail(library.WriteVerifying, WriteCopyFailed)
	}
	if st, err := os.Lstat(orig); err != nil || st.Size() != st0.Size() || !st.ModTime().Equal(st0.ModTime()) {
		return fail(library.WriteVerifying, WriteChanged)
	}
	if err := w.move(ctx, job.guard(), row, library.WriteVerifying, library.WriteSwapping, &after, "", nil); err != nil {
		return "", err
	}
	if err := w.at("swapping"); err != nil {
		return "", err
	}
	if err := os.Rename(orig, backup); err != nil {
		w.log.Warn("write details: backup rename", "err", err)
		return fail(library.WriteSwapping, WriteSwapFailed)
	}
	if err := w.at("swap1"); err != nil {
		return "", err
	}
	if err := os.Rename(temp, orig); err != nil {
		w.log.Warn("write details: swap rename", "err", err)
		if rerr := os.Rename(backup, orig); rerr != nil {
			// The original is in the backup and could not be put back: leave it to recovery.
			return "", rerr
		}
		return fail(library.WriteSwapping, WriteSwapFailed)
	}
	syncDir(filepath.Dir(orig))
	if err := w.at("swap2"); err != nil {
		return "", err
	}
	audit := &db.AuditEntry{UserID: job.createdBy, Action: "book.write", Target: job.bookID,
		Detail: map[string]string{"file": f.ID, "write": row.ID}}
	if err := w.move(ctx, job.guard(), row, library.WriteSwapping, library.WriteSwapped, nil, "", audit); err != nil {
		return "", err
	}
	return "", nil
}

// verify compares the finished copy with what was meant. It returns a code for a mismatch and an
// error only when the context ended.
func (w *Writes) verify(ctx context.Context, temp, audioHash string, src media.Probe, tw media.TagWrite) (string, error) {
	got, err := w.tools.AudioHash(ctx, temp)
	if err != nil && ctx.Err() != nil {
		return "", ctx.Err()
	}
	if err != nil || got != audioHash {
		return VerifyAudio, nil
	}
	tp, err := w.tools.Probe(ctx, temp)
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return VerifyTags, nil
	}
	if math.Abs(float64(tp.DurationMs-src.DurationMs)) > 50 {
		return VerifyDuration, nil
	}
	if tw.WriteChapters {
		if len(tp.Chapters) != len(tw.Chapters) {
			return VerifyChapters, nil
		}
		for i, c := range tp.Chapters {
			if math.Abs(float64(c.StartMs-tw.Chapters[i].StartMs)) > 1 {
				return VerifyChapters, nil
			}
		}
	} else if len(tp.Chapters) != len(src.Chapters) {
		return VerifyChapters, nil
	}
	for k, want := range tw.Tags {
		if tw.Format == "flac" && k == "description" {
			k = "comment" // the flac muxer stores the description there
		}
		if strings.TrimSpace(tp.Tags[k]) != strings.TrimSpace(want) {
			return VerifyTags, nil
		}
	}
	return "", nil
}

func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = f.Sync()
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// syncDir makes the renames in a folder durable. Where a folder cannot be synced it does nothing.
func syncDir(dir string) {
	if d, err := os.Open(dir); err == nil {
		_ = d.Sync()
		d.Close()
	}
}

// changed tells the library service that files of the book's library were swapped or restored.
func (w *Writes) changed(ctx context.Context, bookID string) {
	if w.OnChanged == nil {
		return
	}
	if b, err := w.repo.Book(ctx, bookID); err == nil {
		w.OnChanged(ctx, b.LibraryID)
	}
}

// Confirm is "Looks good": the backups of the book's swapped files are removed and the rows
// closed. It returns how many files were confirmed.
func (w *Writes) Confirm(ctx context.Context, bookID, userID string) (int, error) {
	end, err := w.begin(bookID)
	if err != nil {
		return 0, err
	}
	defer end()
	rows, err := w.repo.BookFileWrites(ctx, bookID, library.WriteSwapped)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, row := range rows {
		if err := w.move(ctx, nil, row, library.WriteSwapped, library.WriteConfirming, nil, "", nil); err != nil {
			return n, err
		}
		if err := w.at("confirm1"); err != nil {
			return n, err
		}
		if err := os.Remove(row.BackupPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return n, err
		}
		syncDir(filepath.Dir(row.BackupPath))
		audit := &db.AuditEntry{UserID: userID, Action: "book.write.confirm", Target: bookID, Detail: map[string]string{"write": row.ID}}
		if err := w.move(ctx, nil, row, library.WriteConfirming, library.WriteConfirmed, nil, "", audit); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// undoSteps puts the backup back as the original: the new file goes to the temp name, the backup
// takes its place, and the new file is removed. A step that already happened is skipped.
func (w *Writes) undoSteps(row library.FileWrite) error {
	if err := os.Rename(row.OriginalPath, row.TempPath); err != nil {
		return err
	}
	if err := w.at("undo1"); err != nil {
		return err
	}
	if err := os.Rename(row.BackupPath, row.OriginalPath); err != nil {
		return err
	}
	if err := w.at("undo2"); err != nil {
		return err
	}
	if err := os.Remove(row.TempPath); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	syncDir(filepath.Dir(row.OriginalPath))
	return nil
}

// ErrNeedsAttention means a file changed after its details were written, so Undo kept the backup
// and restored nothing for it.
var ErrNeedsAttention = &WriteRefusal{Code: WriteAttention, Text: writeText[WriteAttention]}

// Undo restores the originals of the book's swapped files. A file that is no longer the one
// Tendshelf wrote is not touched: its row is marked needs_attention, the backup is kept, and Undo
// stops with ErrNeedsAttention. It returns how many files were restored.
func (w *Writes) Undo(ctx context.Context, bookID, userID string) (int, error) {
	end, err := w.begin(bookID)
	if err != nil {
		return 0, err
	}
	defer end()
	rows, err := w.repo.BookFileWrites(ctx, bookID, library.WriteSwapped)
	if err != nil {
		return 0, err
	}
	n := 0
	defer func() {
		if n > 0 {
			w.changed(ctx, bookID)
		}
	}()
	for _, row := range rows {
		sha, serr := media.FileSHA256(row.OriginalPath)
		if serr != nil || sha != row.AfterSHA256 {
			audit := &db.AuditEntry{UserID: userID, Action: "book.write.undo", Target: bookID,
				Detail: map[string]string{"write": row.ID, "outcome": WriteAttention}}
			if err := w.move(ctx, nil, row, library.WriteSwapped, library.WriteFailed, nil, WriteAttention, audit); err != nil {
				return n, err
			}
			return n, ErrNeedsAttention
		}
		if err := w.move(ctx, nil, row, library.WriteSwapped, library.WriteUndoing, nil, "", nil); err != nil {
			return n, err
		}
		if err := w.at("undoing"); err != nil {
			return n, err
		}
		if err := w.undoSteps(row); err != nil {
			return n, err
		}
		audit := &db.AuditEntry{UserID: userID, Action: "book.write.undo", Target: bookID, Detail: map[string]string{"write": row.ID}}
		if err := w.move(ctx, nil, row, library.WriteUndoing, library.WriteUndone, nil, "", audit); err != nil {
			return n, err
		}
		n++
	}
	return n, nil
}

// Recover settles every ledger row a crash left half done. It decides with Lstat and file hashes
// only and never runs ffmpeg. Rows of books this process is working on, and rows whose job is
// still running, are left alone. Held write jobs are cancelled afterwards: their rows are settled,
// and the person starts a new write if they still want one.
func (w *Writes) Recover(ctx context.Context) error {
	rows, err := w.repo.OpenFileWrites(ctx)
	if err != nil {
		return err
	}
	books := map[string]bool{}
	var firstErr error
	for _, row := range rows {
		if row.State == library.WriteSwapped || w.isActive(row.BookID) {
			continue
		}
		if j, err := w.runner.Get(ctx, row.JobID); err == nil && j.State == jobs.StateRunning {
			continue
		}
		if err := w.recoverRow(ctx, row); err != nil {
			w.log.Warn("write details: recovery", "write", row.ID, "err", err)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		books[row.BookID] = true
	}
	for id := range books {
		w.changed(ctx, id)
	}
	w.cancelHeld(ctx)
	return firstErr
}

func (w *Writes) cancelHeld(ctx context.Context) {
	keys, err := w.jobs.ActiveKeys(ctx, KindWriteDetails, "book_id")
	if err != nil {
		return
	}
	for bookID := range keys {
		if j, ok, err := w.runner.ActiveFor(ctx, KindWriteDetails, "book_id", bookID); err == nil && ok && j.State == jobs.StateHeld {
			if err := w.runner.Cancel(ctx, j.ID); err != nil {
				w.log.Warn("write details: cancel held job", "job", j.ID, "err", err)
			}
		}
	}
}

// recoverRow settles one row by the table in the plan (docs/plans/m4-details.md, M4-7 step 4).
func (w *Writes) recoverRow(ctx context.Context, row library.FileWrite) error {
	hasOrig, hasBackup := present(row.OriginalPath), present(row.BackupPath)
	shaIs := func(want string) bool {
		sha, err := media.FileSHA256(row.OriginalPath)
		return err == nil && want != "" && sha == want
	}
	settle := func(to, code string) error {
		audit := &db.AuditEntry{Action: "book.write.recover", Target: row.BookID,
			Detail: map[string]string{"write": row.ID, "from": row.State, "to": to, "code": code}}
		return w.move(ctx, nil, row, row.State, to, nil, code, audit)
	}
	attention := func() error { return settle(library.WriteFailed, WriteAttention) }
	switch row.State {
	case library.WriteCopying, library.WriteVerifying:
		if err := removeIfThere(row.TempPath); err != nil {
			return err
		}
		return settle(library.WriteFailed, WriteInterrupted)
	case library.WriteSwapping:
		switch {
		case hasOrig && !hasBackup:
			if err := removeIfThere(row.TempPath); err != nil {
				return err
			}
			return settle(library.WriteFailed, WriteInterrupted)
		case !hasOrig && hasBackup:
			if err := os.Rename(row.BackupPath, row.OriginalPath); err != nil {
				return err
			}
			if err := removeIfThere(row.TempPath); err != nil {
				return err
			}
			syncDir(filepath.Dir(row.OriginalPath))
			return settle(library.WriteFailed, WriteInterrupted)
		case hasOrig && hasBackup:
			if shaIs(row.AfterSHA256) {
				return settle(library.WriteSwapped, "")
			}
			return attention()
		}
		return attention()
	case library.WriteConfirming:
		if err := removeIfThere(row.BackupPath); err != nil {
			return err
		}
		return settle(library.WriteConfirmed, "")
	case library.WriteUndoing:
		switch {
		case hasBackup && hasOrig && shaIs(row.AfterSHA256):
			if err := w.undoSteps(row); err != nil {
				return err
			}
			return settle(library.WriteUndone, "")
		case hasBackup && !hasOrig:
			if err := os.Rename(row.BackupPath, row.OriginalPath); err != nil {
				return err
			}
			if err := removeIfThere(row.TempPath); err != nil {
				return err
			}
			syncDir(filepath.Dir(row.OriginalPath))
			return settle(library.WriteUndone, "")
		case !hasBackup && hasOrig && shaIs(row.BeforeSHA256):
			if err := removeIfThere(row.TempPath); err != nil {
				return err
			}
			return settle(library.WriteUndone, "")
		}
		return attention()
	}
	return fmt.Errorf("mediajobs: unexpected ledger state %q", row.State)
}

func removeIfThere(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// Run recovers once and then every 30 seconds until ctx ends.
func (w *Writes) Run(ctx context.Context) {
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		if err := w.Recover(ctx); err != nil && ctx.Err() == nil {
			w.log.Warn("write details: recovery pass", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
	}
}
