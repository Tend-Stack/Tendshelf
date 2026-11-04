package ebook

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

// File is a row of ebook_files.
type File struct {
	ID, Origin, LibraryID string
	FolderID, Path        string // empty when NULL
	BookID, PairedBy      string // empty when NULL
	Hold                  string // "", "person" or "kept"
	BookDefault           bool
	Ext                   string
	Size, MtimeNS         int64
	QuickHash             string
	SHA256, Format        string // empty when NULL
	State                 string
	RefusedReason         string // empty when NULL
	ErrorCode, JobID      string
	OriginalName          string
	UploadedBy            string
	MissingSince          int64  // 0 when NULL
	MergedFrom            string // the text book this file was merged out of; empty when NULL
	CreatedAt, UpdatedAt  int64
}

// Edition is a row of editions.
type Edition struct {
	ID, EbookFileID, SourceSHA256, Recipe, DocSHA256, Format, Layout string
	DocBytes                                                         int64
	Title, Subtitle, AuthorsJSON, Language, ISBN, ASIN               string
	ChapterCount, WordCount, CharCount, PageCount                    int
	CreatedAt                                                        int64
}

// ChapterRow is a row of edition_chapters.
type ChapterRow struct {
	EditionID                        string
	Idx                              int
	Title                            string
	Front                            bool
	FirstLine, FirstBlockID          string
	BlockCount, WordCount, CharCount int
	Src                              string
}

// Store reads the ebook tables. Writers (scan, upload, job) live in later packages.
type Store struct{ db *db.Store }

// NewStore wraps the database store.
func NewStore(s *db.Store) *Store { return &Store{db: s} }

const fileCols = `id, origin, library_id, COALESCE(folder_id,''), COALESCE(path,''), COALESCE(book_id,''),
COALESCE(paired_by,''), COALESCE(hold,''), book_default, ext, size, mtime_ns, quick_hash, COALESCE(sha256,''), COALESCE(format,''), state,
COALESCE(refused_reason,''), COALESCE(error_code,''), COALESCE(job_id,''), original_name,
COALESCE(uploaded_by,''), COALESCE(missing_since,0), created_at, updated_at, COALESCE(merged_from,'')`

type scanner interface{ Scan(dest ...any) error }

func scanFile(r scanner) (File, error) {
	var f File
	err := r.Scan(&f.ID, &f.Origin, &f.LibraryID, &f.FolderID, &f.Path, &f.BookID, &f.PairedBy, &f.Hold, &f.BookDefault, &f.Ext, &f.Size,
		&f.MtimeNS, &f.QuickHash, &f.SHA256, &f.Format, &f.State, &f.RefusedReason, &f.ErrorCode, &f.JobID,
		&f.OriginalName, &f.UploadedBy, &f.MissingSince, &f.CreatedAt, &f.UpdatedAt, &f.MergedFrom)
	return f, err
}

// FilesForBook lists the ebook files paired with a book, oldest first.
func (s *Store) FilesForBook(ctx context.Context, bookID string) ([]File, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT `+fileCols+` FROM ebook_files WHERE book_id = ? ORDER BY created_at, id`, bookID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []File
	for rows.Next() {
		f, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// File returns one ebook file; ok is false when it does not exist.
func (s *Store) File(ctx context.Context, id string) (File, bool, error) {
	f, err := scanFile(s.db.Reader().QueryRowContext(ctx, `SELECT `+fileCols+` FROM ebook_files WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return File{}, false, nil
	}
	return f, err == nil, err
}

const editionCols = `id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, layout, title, subtitle,
authors_json, language, isbn, asin, chapter_count, word_count, char_count, page_count, created_at`

func scanEdition(r scanner) (Edition, error) {
	var e Edition
	err := r.Scan(&e.ID, &e.EbookFileID, &e.SourceSHA256, &e.Recipe, &e.DocSHA256, &e.DocBytes,
		&e.Format, &e.Layout, &e.Title, &e.Subtitle, &e.AuthorsJSON, &e.Language, &e.ISBN, &e.ASIN,
		&e.ChapterCount, &e.WordCount, &e.CharCount, &e.PageCount, &e.CreatedAt)
	return e, err
}

// CurrentEdition returns the edition read from the file's present bytes by the present recipe.
func (s *Store) CurrentEdition(ctx context.Context, fileID string) (Edition, bool, error) {
	e, err := scanEdition(s.db.Reader().QueryRowContext(ctx, `SELECT `+editionCols+` FROM editions e
WHERE ebook_file_id = ? AND recipe = ? AND source_sha256 = (SELECT sha256 FROM ebook_files WHERE id = ?)`,
		fileID, Recipe, fileID))
	if errors.Is(err, sql.ErrNoRows) {
		return Edition{}, false, nil
	}
	return e, err == nil, err
}

// Edition returns one edition by id; ok is false when it does not exist.
func (s *Store) Edition(ctx context.Context, id string) (Edition, bool, error) {
	e, err := scanEdition(s.db.Reader().QueryRowContext(ctx, `SELECT `+editionCols+` FROM editions WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Edition{}, false, nil
	}
	return e, err == nil, err
}

// Chapters lists an edition's chapters in order.
func (s *Store) Chapters(ctx context.Context, editionID string) ([]ChapterRow, error) {
	rows, err := s.db.Reader().QueryContext(ctx, `SELECT edition_id, idx, title, front, first_line, first_block_id,
block_count, word_count, char_count, src FROM edition_chapters WHERE edition_id = ? ORDER BY idx`, editionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChapterRow
	for rows.Next() {
		var c ChapterRow
		var front int
		if err := rows.Scan(&c.EditionID, &c.Idx, &c.Title, &front, &c.FirstLine, &c.FirstBlockID, &c.BlockCount,
			&c.WordCount, &c.CharCount, &c.Src); err != nil {
			return nil, err
		}
		c.Front = front == 1
		out = append(out, c)
	}
	return out, rows.Err()
}

// ReadyForBook reports whether any ebook file paired with the book is ready to read.
func (s *Store) ReadyForBook(ctx context.Context, bookID string) (bool, error) {
	var n int
	err := s.db.Reader().QueryRowContext(ctx, `SELECT count(*) FROM ebook_files WHERE book_id = ? AND state = 'ready'`, bookID).Scan(&n)
	return n > 0, err
}
