package library

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
)

// TextBookIn is what the ebook service gives to make a text book (decision 0018). RootPath is the
// default file's path for a library file, or a unique absolute path under the data folder for an upload.
type TextBookIn struct {
	LibraryID, FolderID, RootPath, Title string
	ID                                   string // empty gets a new id; a split passes the id the book had before
}

// CreateTextBook adds a book that belongs to the ebook service: kind 'text', no audio, no duration.
func (t *Tx) CreateTextBook(in TextBookIn) (string, error) {
	b := Book{ID: in.ID, LibraryID: in.LibraryID, FolderID: in.FolderID, RootPath: in.RootPath,
		Title: in.Title, SortTitle: in.Title, Kind: BookText}
	if err := t.InsertBook(&b); err != nil {
		return "", err
	}
	return b.ID, nil
}

// CreateTextBook is Tx.CreateTextBook in its own transaction.
func (r *Repo) CreateTextBook(ctx context.Context, in TextBookIn) (id string, err error) {
	err = r.Update(ctx, func(tx *Tx) error {
		id, err = tx.CreateTextBook(in)
		return err
	})
	return id, err
}

// TxOn wraps a transaction the caller already holds, so a service can write its own rows and the
// library's in one commit.
func (r *Repo) TxOn(ctx context.Context, tx *sql.Tx) *Tx { return &Tx{ctx: ctx, tx: tx, r: r} }

// MergedInto says which book took over a merged-away text book's id.
func (r *Repo) MergedInto(ctx context.Context, id string) (string, bool) {
	var to string
	err := r.st.Reader().QueryRowContext(ctx, `SELECT to_id FROM merged_books WHERE from_id = ?`, id).Scan(&to)
	return to, err == nil
}

// TextMeta is what a text book's default file says about it.
type TextMeta struct {
	Live                      bool // a file that is ready or pending and not missing
	Title, Subtitle, Language string
	ISBN, ASIN                string
	Authors                   []string
	Description, Publisher    string
	Year                      int
	Series                    []TextSeries
	HasEdition                bool
}

// TextSeries is a series named inside an ebook.
type TextSeries struct{ Name, Sequence string }

// TextMeta reads the default live file's current edition: its columns and meta_json (description,
// publisher, year, series, series_index). Live is false when the book has no live file.
func (t *Tx) TextMeta(bookID string) (TextMeta, error) {
	var m TextMeta
	var fileID string
	var sha sql.NullString
	err := t.tx.QueryRowContext(t.ctx, `SELECT id, sha256 FROM ebook_files
WHERE book_id = ? AND missing_since IS NULL AND state IN ('ready','pending')
ORDER BY (state = 'ready') DESC, book_default DESC, created_at, id LIMIT 1`, bookID).Scan(&fileID, &sha)
	if errors.Is(err, sql.ErrNoRows) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	m.Live = true
	var authors, meta string
	err = t.tx.QueryRowContext(t.ctx, `SELECT title, subtitle, authors_json, language, isbn, asin, meta_json FROM editions
WHERE ebook_file_id = ? AND source_sha256 = ? ORDER BY created_at DESC, id DESC LIMIT 1`, fileID, sha.String).
		Scan(&m.Title, &m.Subtitle, &authors, &m.Language, &m.ISBN, &m.ASIN, &meta)
	if errors.Is(err, sql.ErrNoRows) {
		return m, nil
	}
	if err != nil {
		return m, err
	}
	m.HasEdition = true
	_ = json.Unmarshal([]byte(authors), &m.Authors)
	var extra struct {
		Description string          `json:"description"`
		Publisher   string          `json:"publisher"`
		Year        int             `json:"year"`
		Series      string          `json:"series"`
		SeriesIndex json.RawMessage `json:"series_index"` // a number or a string
	}
	if json.Unmarshal([]byte(meta), &extra) == nil {
		m.Description, m.Publisher, m.Year = extra.Description, extra.Publisher, extra.Year
		if extra.Series != "" {
			m.Series = []TextSeries{{Name: extra.Series, Sequence: strings.Trim(string(extra.SeriesIndex), `"null`)}}
		}
	}
	return m, nil
}
