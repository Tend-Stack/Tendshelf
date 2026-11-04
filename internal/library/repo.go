package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// scanner is what *sql.Row and *sql.Rows share.
type scanner interface{ Scan(dest ...any) error }

const libraryCols = `id, name, scan_interval_min, created_at, COALESCE(auto_lookup_at, 0), COALESCE(upload_folder_id, '')`

func scanLibrary(s scanner) (Library, error) {
	var l Library
	err := s.Scan(&l.ID, &l.Name, &l.ScanIntervalMin, &l.CreatedAt, &l.AutoLookupAt, &l.UploadFolderID)
	return l, err
}

const folderCols = `id, library_id, path, created_at`

func scanFolder(s scanner) (Folder, error) {
	var f Folder
	err := s.Scan(&f.ID, &f.LibraryID, &f.Path, &f.CreatedAt)
	return f, err
}

const bookCols = `id, library_id, folder_id, root_path, title, sort_title, subtitle, description, publisher,
  published_year, language, isbn, asin, abridged, dramatized, explicit, duration_ms, cover_id, refused_reason,
  added_at, updated_at, missing_since, kind`

func scanBook(s scanner) (Book, error) {
	var b Book
	var year, missing sql.NullInt64
	var cover, refused sql.NullString
	err := s.Scan(&b.ID, &b.LibraryID, &b.FolderID, &b.RootPath, &b.Title, &b.SortTitle, &b.Subtitle,
		&b.Description, &b.Publisher, &year, &b.Language, &b.ISBN, &b.ASIN, &b.Abridged, &b.Dramatized,
		&b.Explicit, &b.DurationMs, &cover, &refused, &b.AddedAt, &b.UpdatedAt, &missing, &b.Kind)
	b.PublishedYear = int(year.Int64)
	b.CoverID = cover.String
	b.RefusedReason = refused.String
	b.MissingSince = missing.Int64
	return b, err
}

const audioCols = `id, book_id, path, idx, size, mtime_ns, quick_hash, sha256, container, codec, bitrate, channels,
  sample_rate, duration_ms, offset_ms, encrypted, probe_json, probe_error, missing_since, updated_at`

// audioColsA is audioCols qualified with the alias a, for queries that join books.
const audioColsA = `a.id, a.book_id, a.path, a.idx, a.size, a.mtime_ns, a.quick_hash, a.sha256, a.container, a.codec, a.bitrate,
  a.channels, a.sample_rate, a.duration_ms, a.offset_ms, a.encrypted, a.probe_json, a.probe_error, a.missing_since, a.updated_at`

func scanAudio(s scanner) (AudioFile, error) {
	var f AudioFile
	var sum, probe, probeErr sql.NullString
	var missing sql.NullInt64
	err := s.Scan(&f.ID, &f.BookID, &f.Path, &f.Idx, &f.Size, &f.MtimeNs, &f.QuickHash, &sum, &f.Container,
		&f.Codec, &f.Bitrate, &f.Channels, &f.SampleRate, &f.DurationMs, &f.OffsetMs, &f.Encrypted, &probe,
		&probeErr, &missing, &f.UpdatedAt)
	f.SHA256 = sum.String
	f.ProbeError = probeErr.String
	if probe.Valid {
		f.ProbeJSON = []byte(probe.String)
	}
	f.MissingSince = missing.Int64
	return f, err
}

func scanChapter(s scanner) (Chapter, error) {
	var c Chapter
	err := s.Scan(&c.Idx, &c.StartMs, &c.EndMs, &c.Title, &c.Source)
	return c, err
}

// one runs a single-row query and maps sql.ErrNoRows to ErrNotFound.
func one[T any](row *sql.Row, scan func(scanner) (T, error), what string) (T, error) {
	v, err := scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		var zero T
		return zero, fmt.Errorf("%w: %s", ErrNotFound, what)
	}
	return v, err
}

// queryer is what *sql.DB and *sql.Tx share, so reads can run on either.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// many runs a list query.
func many[T any](ctx context.Context, d queryer, scan func(scanner) (T, error), query string, args ...any) ([]T, error) {
	rows, err := d.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []T
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// Libraries returns every library by name (case-insensitive), then ID.
func (r *Repo) Libraries(ctx context.Context) ([]Library, error) {
	return many(ctx, r.st.Reader(), scanLibrary,
		`SELECT `+libraryCols+` FROM libraries ORDER BY name COLLATE NOCASE, id`)
}

// Library returns one library, or ErrNotFound.
func (r *Repo) Library(ctx context.Context, id string) (Library, error) {
	return one(r.st.Reader().QueryRowContext(ctx, `SELECT `+libraryCols+` FROM libraries WHERE id = ?`, id),
		scanLibrary, "library")
}

// Folders returns the folders of a library by path.
func (r *Repo) Folders(ctx context.Context, libraryID string) ([]Folder, error) {
	return many(ctx, r.st.Reader(), scanFolder,
		`SELECT `+folderCols+` FROM library_folders WHERE library_id = ? ORDER BY path`, libraryID)
}

// Folder returns one folder, or ErrNotFound.
func (r *Repo) Folder(ctx context.Context, id string) (Folder, error) {
	return one(r.st.Reader().QueryRowContext(ctx, `SELECT `+folderCols+` FROM library_folders WHERE id = ?`, id),
		scanFolder, "folder")
}

// Book returns one book, or ErrNotFound.
func (r *Repo) Book(ctx context.Context, id string) (Book, error) {
	return one(r.st.Reader().QueryRowContext(ctx, `SELECT `+bookCols+` FROM books WHERE id = ?`, id),
		scanBook, "book")
}

// BookByRootPath returns the book at an exact root path, or ErrNotFound.
func (r *Repo) BookByRootPath(ctx context.Context, path string) (Book, error) {
	return one(r.st.Reader().QueryRowContext(ctx, `SELECT `+bookCols+` FROM books WHERE root_path = ?`, path),
		scanBook, "book")
}

// BooksInFolder returns every book of a folder by root path, including books marked missing.
func (r *Repo) BooksInFolder(ctx context.Context, folderID string) ([]Book, error) {
	return many(ctx, r.st.Reader(), scanBook,
		`SELECT `+bookCols+` FROM books WHERE folder_id = ? ORDER BY root_path`, folderID)
}

// AudioFiles returns the files of a book in play order.
func (r *Repo) AudioFiles(ctx context.Context, bookID string) ([]AudioFile, error) {
	return many(ctx, r.st.Reader(), scanAudio,
		`SELECT `+audioCols+` FROM audio_files WHERE book_id = ? ORDER BY idx, path`, bookID)
}

// AudioFilesInFolder returns every audio file of a folder's books, ordered by book root path then index.
func (r *Repo) AudioFilesInFolder(ctx context.Context, folderID string) ([]AudioFile, error) {
	return many(ctx, r.st.Reader(), scanAudio,
		`SELECT `+audioColsA+`
  FROM audio_files a JOIN books b ON b.id = a.book_id WHERE b.folder_id = ? ORDER BY b.root_path, a.idx, a.path`, folderID)
}

// AudioFilesByQuickHash returns the files of one library with this quick hash (move detection).
func (r *Repo) AudioFilesByQuickHash(ctx context.Context, libraryID, hash string) ([]AudioFile, error) {
	return many(ctx, r.st.Reader(), scanAudio,
		`SELECT `+audioColsA+`
  FROM audio_files a JOIN books b ON b.id = a.book_id WHERE b.library_id = ? AND a.quick_hash = ? ORDER BY a.path`,
		libraryID, hash)
}

// Chapters returns a book's chapters in order.
func (r *Repo) Chapters(ctx context.Context, bookID string) ([]Chapter, error) {
	return many(ctx, r.st.Reader(), scanChapter, `SELECT idx, start_ms, end_ms, title, source FROM chapters WHERE book_id = ? ORDER BY idx`, bookID)
}

// AudioFile returns one audio file, or ErrNotFound.
func (r *Repo) AudioFile(ctx context.Context, id string) (AudioFile, error) {
	return one(r.st.Reader().QueryRowContext(ctx, `SELECT `+audioCols+` FROM audio_files WHERE id = ?`, id),
		scanAudio, "audio file")
}

// UnsettledAudioFileIDs returns the IDs of a library's live files that have neither probe data nor
// a probe error, ordered by path: the files that still wait for a probe.
func (r *Repo) UnsettledAudioFileIDs(ctx context.Context, libraryID string) ([]string, error) {
	return many(ctx, r.st.Reader(), func(s scanner) (string, error) {
		var id string
		return id, s.Scan(&id)
	}, `SELECT a.id FROM audio_files a JOIN books b ON b.id = a.book_id
  WHERE b.library_id = ? AND a.missing_since IS NULL AND a.probe_json IS NULL AND a.probe_error IS NULL
  ORDER BY a.path`, libraryID)
}

// QuickHashes returns every quick hash an audio file row carries, missing files included, so a
// cache keyed by quick hash can tell its stale entries from the live ones.
func (r *Repo) QuickHashes(ctx context.Context) (map[string]bool, error) {
	hashes, err := many(ctx, r.st.Reader(), func(s scanner) (string, error) {
		var h string
		return h, s.Scan(&h)
	}, `SELECT DISTINCT quick_hash FROM audio_files`)
	if err != nil {
		return nil, err
	}
	out := make(map[string]bool, len(hashes))
	for _, h := range hashes {
		out[h] = true
	}
	return out, nil
}

// PeaksCandidates returns the live, probed files of books that are not refused, by book and path:
// the files whose waveform may be computed.
func (r *Repo) PeaksCandidates(ctx context.Context) ([]AudioFile, error) {
	return many(ctx, r.st.Reader(), scanAudio,
		`SELECT `+audioColsA+`
  FROM audio_files a JOIN books b ON b.id = a.book_id
  WHERE a.missing_since IS NULL AND a.probe_json IS NOT NULL AND a.probe_error IS NULL AND b.refused_reason IS NULL
  ORDER BY b.id, a.path`)
}

// RefusedBooks returns the library's books with a refused reason, by root path.
func (r *Repo) RefusedBooks(ctx context.Context, libraryID string) ([]Book, error) {
	return many(ctx, r.st.Reader(), scanBook,
		`SELECT `+bookCols+` FROM books WHERE library_id = ? AND refused_reason IS NOT NULL ORDER BY root_path`, libraryID)
}
