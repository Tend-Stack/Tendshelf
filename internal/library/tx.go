package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

func invalid(format string, a ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalid, fmt.Sprintf(format, a...))
}

// mapErr turns constraint failures into the package's errors; other errors pass through.
func mapErr(err error, what string) error {
	switch {
	case err == nil:
		return nil
	case db.IsUniqueViolation(err):
		return fmt.Errorf("%w: %s", ErrConflict, what)
	case db.IsForeignKeyViolation(err):
		return fmt.Errorf("%w: %s", ErrNotFound, what)
	}
	return err
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(n int64) any {
	if n == 0 {
		return nil
	}
	return n
}

func validRefused(s string) bool {
	switch s {
	case "", RefusedDRM, RefusedUnsupported, RefusedDamaged, RefusedNoAudio:
		return true
	}
	return false
}

func validChapterSource(s string) bool {
	switch s {
	case ChapterFile, ChapterFiles, ChapterSidecar, ChapterOnline, ChapterUser:
		return true
	}
	return false
}

// CreateLibrary adds a library. The name is trimmed and must be 1 to 200 characters; the scan
// interval is 0 (on request) to 10080 minutes (a week).
func (r *Repo) CreateLibrary(ctx context.Context, name string, scanIntervalMin int) (Library, error) {
	l, err := r.newLibrary(name, scanIntervalMin)
	if err != nil {
		return Library{}, err
	}
	if err := r.st.WriteTx(ctx, func(tx *sql.Tx) error { return insertLibrary(ctx, tx, l) }); err != nil {
		return Library{}, err
	}
	return l, nil
}

// newLibrary validates the fields and returns the library row to insert.
func (r *Repo) newLibrary(name string, scanIntervalMin int) (Library, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 200 {
		return Library{}, invalid("name must be 1 to 200 characters")
	}
	if scanIntervalMin < 0 || scanIntervalMin > 10080 {
		return Library{}, invalid("scan interval must be 0 to 10080 minutes")
	}
	return Library{ID: db.NewID("lib"), Name: name, ScanIntervalMin: scanIntervalMin, CreatedAt: r.nowMs()}, nil
}

func insertLibrary(ctx context.Context, tx *sql.Tx, l Library) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO libraries(id, name, scan_interval_min, created_at) VALUES (?,?,?,?)`,
		l.ID, l.Name, l.ScanIntervalMin, l.CreatedAt)
	return err
}

// withSlash returns p with exactly one trailing separator, for prefix comparison.
func withSlash(p string) string {
	if strings.HasSuffix(p, string(filepath.Separator)) {
		return p
	}
	return p + string(filepath.Separator)
}

// AddFolder adds a folder to a library. The path must be absolute; it is stored cleaned. A folder
// that equals, contains or lies inside any existing folder (of any library) is refused, so one
// book is never scanned twice. It does not touch the file system.
func (r *Repo) AddFolder(ctx context.Context, libraryID, path string) (Folder, error) {
	f, err := r.newFolder(libraryID, path)
	if err != nil {
		return Folder{}, err
	}
	if err := r.st.WriteTx(ctx, func(tx *sql.Tx) error { return r.insertFolder(ctx, tx, f) }); err != nil {
		return Folder{}, err
	}
	return f, nil
}

// newFolder validates path and returns the folder row to insert.
func (r *Repo) newFolder(libraryID, path string) (Folder, error) {
	if !filepath.IsAbs(path) {
		return Folder{}, invalid("folder path must be absolute")
	}
	return Folder{ID: db.NewID("lf"), LibraryID: libraryID, Path: filepath.Clean(path), CreatedAt: r.nowMs()}, nil
}

// insertFolder is the body of AddFolder inside a write transaction: the library must exist and no
// existing folder may overlap f.
func (r *Repo) insertFolder(ctx context.Context, tx *sql.Tx, f Folder) error {
	clean := f.Path
	var one int
	if err := tx.QueryRowContext(ctx, `SELECT 1 FROM libraries WHERE id = ?`, f.LibraryID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: library", ErrNotFound)
		}
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT path FROM library_folders`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return err
		}
		if p == clean || strings.HasPrefix(p, withSlash(clean)) || strings.HasPrefix(clean, withSlash(p)) {
			return fmt.Errorf("%w: %s", ErrFolderOverlap, p)
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	rows.Close()
	_, err = tx.ExecContext(ctx, `INSERT INTO library_folders(id, library_id, path, created_at) VALUES (?,?,?,?)`,
		f.ID, f.LibraryID, f.Path, f.CreatedAt)
	return mapErr(err, "folder path")
}

// Tx is the write view handed to Update. It is valid only inside the callback.
type Tx struct {
	ctx context.Context
	tx  *sql.Tx
	r   *Repo
}

// Update runs fn in one write transaction. Any error from fn rolls every change back.
func (r *Repo) Update(ctx context.Context, fn func(*Tx) error) error {
	return r.UpdateGuarded(ctx, nil, fn)
}

// UpdateGuarded is Update with a guard that runs first, inside the same transaction. A job handler
// passes its fence check (jobs.Task.Fence) so a run that lost its lease writes nothing; an error
// from the guard rolls back and is returned as is. A nil guard is allowed.
func (r *Repo) UpdateGuarded(ctx context.Context, guard func(context.Context, *sql.Tx) error, fn func(*Tx) error) error {
	return r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if guard != nil {
			if err := guard(ctx, tx); err != nil {
				return err
			}
		}
		return fn(&Tx{ctx: ctx, tx: tx, r: r})
	})
}

func (t *Tx) exec(query string, args ...any) (sql.Result, error) {
	return t.tx.ExecContext(t.ctx, query, args...)
}

func checkBook(b *Book) error {
	if !filepath.IsAbs(b.RootPath) {
		return invalid("book root path must be absolute")
	}
	if !validRefused(b.RefusedReason) {
		return invalid("unknown refused reason %q", b.RefusedReason)
	}
	if b.DurationMs < 0 || b.PublishedYear < 0 {
		return invalid("duration and year cannot be negative")
	}
	return nil
}

func bookValues(b *Book) []any {
	return []any{b.FolderID, b.RootPath, b.Title, b.SortTitle, b.Subtitle, b.Description, b.Publisher,
		nullInt(int64(b.PublishedYear)), b.Language, b.ISBN, b.ASIN, b.Abridged, b.Dramatized, b.Explicit,
		b.DurationMs, nullStr(b.CoverID), nullStr(b.RefusedReason), b.UpdatedAt, nullInt(b.MissingSince)}
}

// InsertBook adds a book. An empty ID gets a new one; zero AddedAt and UpdatedAt become now.
func (t *Tx) InsertBook(b *Book) error {
	if err := checkBook(b); err != nil {
		return err
	}
	var folderLib string
	err := t.tx.QueryRowContext(t.ctx, `SELECT library_id FROM library_folders WHERE id = ?`, b.FolderID).Scan(&folderLib)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("%w: folder", ErrNotFound)
	} else if err != nil {
		return err
	}
	if folderLib != b.LibraryID {
		return invalid("folder belongs to another library")
	}
	if b.ID == "" {
		b.ID = db.NewID("bk")
	}
	now := t.r.nowMs()
	if b.AddedAt == 0 {
		b.AddedAt = now
	}
	if b.UpdatedAt == 0 {
		b.UpdatedAt = now
	}
	if b.Kind == "" {
		b.Kind = BookAudio
	}
	args := append([]any{b.ID, b.LibraryID}, bookValues(b)...)
	args = append(args, b.AddedAt, b.Kind)
	_, err = t.exec(`INSERT INTO books(id, library_id, folder_id, root_path, title, sort_title, subtitle, description,
  publisher, published_year, language, isbn, asin, abridged, dramatized, explicit, duration_ms, cover_id,
  refused_reason, updated_at, missing_since, added_at, kind) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, args...)
	return mapErr(err, "book root path")
}

// UpdateBook rewrites every column except id, library_id and added_at, and sets updated_at to now.
func (t *Tx) UpdateBook(b Book) error {
	if err := checkBook(&b); err != nil {
		return err
	}
	b.UpdatedAt = t.r.nowMs()
	args := append(bookValues(&b), b.ID)
	res, err := t.exec(`UPDATE books SET folder_id=?, root_path=?, title=?, sort_title=?, subtitle=?, description=?,
  publisher=?, published_year=?, language=?, isbn=?, asin=?, abridged=?, dramatized=?, explicit=?, duration_ms=?,
  cover_id=?, refused_reason=?, updated_at=?, missing_since=? WHERE id = ?`, args...)
	if err != nil {
		return mapErr(err, "book root path, folder or cover")
	}
	return needRow(res, "book")
}

func needRow(res sql.Result, what string) error {
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("%w: %s", ErrNotFound, what)
	}
	return nil
}

// SetBookMissing marks a book missing since the given time; 0 clears the mark. It does not touch
// updated_at: a scan noticing absence is not an edit.
func (t *Tx) SetBookMissing(bookID string, since int64) error {
	res, err := t.exec(`UPDATE books SET missing_since = ? WHERE id = ?`, nullInt(since), bookID)
	if err != nil {
		return err
	}
	return needRow(res, "book")
}

// PutAudioFile inserts or updates (by ID) an audio file. An empty ID gets a new one. UpdatedAt is
// set to now. A path used by another file is ErrConflict.
func (t *Tx) PutAudioFile(f *AudioFile) error {
	switch {
	case !filepath.IsAbs(f.Path):
		return invalid("audio file path must be absolute")
	case len(f.QuickHash) != 64:
		return invalid("quick hash must be 64 characters")
	case f.SHA256 != "" && len(f.SHA256) != 64:
		return invalid("sha256 must be 64 characters")
	case f.Idx < 0 || f.Size < 0 || f.DurationMs < 0 || f.OffsetMs < 0:
		return invalid("index, size, duration and offset cannot be negative")
	case f.ProbeJSON != nil && !json.Valid(f.ProbeJSON):
		return invalid("probe data is not JSON")
	}
	if f.ID == "" {
		f.ID = db.NewID("af")
	}
	f.UpdatedAt = t.r.nowMs()
	var probe any
	if f.ProbeJSON != nil {
		probe = string(f.ProbeJSON)
	}
	_, err := t.exec(`INSERT INTO audio_files(id, book_id, path, idx, size, mtime_ns, quick_hash, sha256, container, codec,
  bitrate, channels, sample_rate, duration_ms, offset_ms, encrypted, probe_json, probe_error, missing_since, updated_at)
  VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
  ON CONFLICT(id) DO UPDATE SET book_id=excluded.book_id, path=excluded.path, idx=excluded.idx, size=excluded.size,
  mtime_ns=excluded.mtime_ns, quick_hash=excluded.quick_hash, sha256=excluded.sha256, container=excluded.container,
  codec=excluded.codec, bitrate=excluded.bitrate, channels=excluded.channels, sample_rate=excluded.sample_rate,
  duration_ms=excluded.duration_ms, offset_ms=excluded.offset_ms, encrypted=excluded.encrypted,
  probe_json=excluded.probe_json, probe_error=excluded.probe_error, missing_since=excluded.missing_since, updated_at=excluded.updated_at`,
		f.ID, f.BookID, f.Path, f.Idx, f.Size, f.MtimeNs, f.QuickHash, nullStr(f.SHA256), f.Container, f.Codec,
		f.Bitrate, f.Channels, f.SampleRate, f.DurationMs, f.OffsetMs, f.Encrypted, probe, nullStr(f.ProbeError),
		nullInt(f.MissingSince), f.UpdatedAt)
	if db.IsForeignKeyViolation(err) {
		return fmt.Errorf("%w: book", ErrNotFound)
	}
	return mapErr(err, "audio file path")
}

// SetAudioFileMissing marks a file missing since the given time; 0 clears the mark.
func (t *Tx) SetAudioFileMissing(id string, since int64) error {
	res, err := t.exec(`UPDATE audio_files SET missing_since = ? WHERE id = ?`, nullInt(since), id)
	if err != nil {
		return err
	}
	return needRow(res, "audio file")
}

// ReplaceChapters swaps a book's whole chapter list; the index is the slice position.
func (t *Tx) ReplaceChapters(bookID string, chs []Chapter) error {
	for i, c := range chs {
		switch {
		case c.StartMs < 0 || c.EndMs < c.StartMs:
			return invalid("chapter %d ends before it starts", i)
		case !validChapterSource(c.Source):
			return invalid("chapter %d has unknown source %q", i, c.Source)
		}
	}
	var one int
	if err := t.tx.QueryRowContext(t.ctx, `SELECT 1 FROM books WHERE id = ?`, bookID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: book", ErrNotFound)
		}
		return err
	}
	if _, err := t.exec(`DELETE FROM chapters WHERE book_id = ?`, bookID); err != nil {
		return err
	}
	for i, c := range chs {
		if _, err := t.exec(`INSERT INTO chapters(book_id, idx, start_ms, end_ms, title, source) VALUES (?,?,?,?,?,?)`,
			bookID, i, c.StartMs, c.EndMs, c.Title, c.Source); err != nil {
			return err
		}
	}
	return nil
}

// Reads on the transaction see its own uncommitted writes.

// Book returns one book, or ErrNotFound.
func (t *Tx) Book(id string) (Book, error) {
	return one(t.tx.QueryRowContext(t.ctx, `SELECT `+bookCols+` FROM books WHERE id = ?`, id), scanBook, "book")
}

// BooksInLibrary returns every book of a library by root path, including books marked missing.
func (t *Tx) BooksInLibrary(libraryID string) ([]Book, error) {
	return many(t.ctx, t.tx, scanBook,
		`SELECT `+bookCols+` FROM books WHERE library_id = ? ORDER BY root_path`, libraryID)
}

// AudioFile returns one audio file, or ErrNotFound.
func (t *Tx) AudioFile(id string) (AudioFile, error) {
	return one(t.tx.QueryRowContext(t.ctx, `SELECT `+audioCols+` FROM audio_files WHERE id = ?`, id), scanAudio, "audio file")
}

// AudioFilesInLibrary returns every audio file of a library, including missing ones, ordered by
// book ID, then index, then path.
func (t *Tx) AudioFilesInLibrary(libraryID string) ([]AudioFile, error) {
	return many(t.ctx, t.tx, scanAudio, `SELECT `+audioColsA+`
  FROM audio_files a JOIN books b ON b.id = a.book_id WHERE b.library_id = ? ORDER BY a.book_id, a.idx, a.path`, libraryID)
}

// AudioFiles returns the files of a book in play order (index, then path).
func (t *Tx) AudioFiles(bookID string) ([]AudioFile, error) {
	return many(t.ctx, t.tx, scanAudio,
		`SELECT `+audioCols+` FROM audio_files WHERE book_id = ? ORDER BY idx, path`, bookID)
}

// Chapters returns a book's chapters in order.
func (t *Tx) Chapters(bookID string) ([]Chapter, error) {
	return many(t.ctx, t.tx, scanChapter,
		`SELECT idx, start_ms, end_ms, title, source FROM chapters WHERE book_id = ? ORDER BY idx`, bookID)
}

// DeleteEmptyBook deletes the book (and, by cascade, its chapters and metadata) only when it has no
// audio file rows at all, and reports whether it did.
func (t *Tx) DeleteEmptyBook(id string) (bool, error) {
	res, err := t.exec(`DELETE FROM books WHERE id = ? AND NOT EXISTS (SELECT 1 FROM audio_files WHERE book_id = ?)`, id, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}
