package library

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

// Where a cover came from.
const (
	CoverEmbedded = "embedded"
	CoverFolder   = "folder"
	CoverOnline   = "online"
	CoverUpload   = "upload"
)

// Cover is one picture of a book. The files live under the data directory: Path is relative to it
// and names the stored original; thumbnails are derived from SHA256. A book has at most one row per
// SHA256, and Book.CoverID names the one in use.
type Cover struct {
	ID, BookID, Source, Mime string
	Width, Height            int
	SHA256, Path             string
	Tint                     string          // "#rrggbb" or "" when the cover has no usable colour
	Accent                   json.RawMessage // per-theme accent colours as JSON, nil when none
	CreatedAt                int64
}

const coverCols = `id, book_id, source, mime, width, height, sha256, path, tint, accent_json, created_at`

func scanCover(s scanner) (Cover, error) {
	var c Cover
	var tint, accent sql.NullString
	err := s.Scan(&c.ID, &c.BookID, &c.Source, &c.Mime, &c.Width, &c.Height, &c.SHA256, &c.Path, &tint, &accent, &c.CreatedAt)
	c.Tint = tint.String
	if accent.Valid {
		c.Accent = json.RawMessage(accent.String)
	}
	return c, err
}

// Covers returns a book's cover rows, oldest first.
func (r *Repo) Covers(ctx context.Context, bookID string) ([]Cover, error) {
	return many(ctx, r.st.Reader(), scanCover, `SELECT `+coverCols+` FROM covers WHERE book_id = ? ORDER BY created_at, id`, bookID)
}

// Cover returns one cover, or ErrNotFound.
func (r *Repo) Cover(ctx context.Context, id string) (Cover, error) {
	return one(r.st.Reader().QueryRowContext(ctx, `SELECT `+coverCols+` FROM covers WHERE id = ?`, id), scanCover, "cover")
}

// CoversBySHA returns every cover row, of any book, whose picture has this hash.
func (r *Repo) CoversBySHA(ctx context.Context, sha string) ([]Cover, error) {
	return many(ctx, r.st.Reader(), scanCover, `SELECT `+coverCols+` FROM covers WHERE sha256 = ? ORDER BY created_at, id`, sha)
}

// BooksWithUntintedCover returns the ids of the library's present books whose current cover came
// from the scan (a folder picture or an embedded one) and has no tint. The cover job fills the tint
// again for them, so a better colour counter reaches covers that are already stored.
func (r *Repo) BooksWithUntintedCover(ctx context.Context, libraryID string) ([]string, error) {
	rows, err := r.st.Reader().QueryContext(ctx, `SELECT b.id FROM books b JOIN covers c ON c.id = b.cover_id
  WHERE b.library_id = ? AND COALESCE(b.missing_since, 0) = 0 AND c.tint IS NULL AND c.source IN (?, ?)
  ORDER BY b.id`, libraryID, CoverFolder, CoverEmbedded)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Covers returns a book's cover rows, oldest first.
func (t *Tx) Covers(bookID string) ([]Cover, error) {
	return many(t.ctx, t.tx, scanCover, `SELECT `+coverCols+` FROM covers WHERE book_id = ? ORDER BY created_at, id`, bookID)
}

// Cover returns one cover, or ErrNotFound.
func (t *Tx) Cover(id string) (Cover, error) {
	return one(t.tx.QueryRowContext(t.ctx, `SELECT `+coverCols+` FROM covers WHERE id = ?`, id), scanCover, "cover")
}

var tintPattern = regexp.MustCompile(`^#[0-9a-f]{6}$`)

func validCoverSource(s string) bool {
	switch s {
	case CoverEmbedded, CoverFolder, CoverOnline, CoverUpload:
		return true
	}
	return false
}

func validCoverMime(s string) bool {
	switch s {
	case "image/jpeg", "image/png", "image/webp":
		return true
	}
	return false
}

// PutCover adds a cover, or updates the book's existing row with the same SHA256 (source, mime,
// size, path, tint and accent; the ID and CreatedAt stay). An empty ID gets a new one; after the
// call ID and CreatedAt describe the stored row. Putting a cover that equals the stored row writes
// nothing at all.
func (t *Tx) PutCover(c *Cover) error {
	switch {
	case c.BookID == "":
		return invalid("cover needs a book")
	case !validCoverSource(c.Source):
		return invalid("unknown cover source %q", c.Source)
	case !validCoverMime(c.Mime):
		return invalid("unknown cover type %q", c.Mime)
	case c.Width <= 0 || c.Height <= 0:
		return invalid("cover size must be positive")
	case len(c.SHA256) != 64:
		return invalid("cover sha256 must be 64 characters")
	case c.Path == "":
		return invalid("cover needs a path")
	case c.Tint != "" && !tintPattern.MatchString(c.Tint):
		return invalid("cover tint must be #rrggbb")
	case c.Accent != nil && !json.Valid(c.Accent):
		return invalid("cover accent is not JSON")
	}
	var accent any
	if c.Accent != nil {
		accent = string(c.Accent)
	}
	cur, err := one(t.tx.QueryRowContext(t.ctx, `SELECT `+coverCols+` FROM covers WHERE book_id = ? AND sha256 = ?`, c.BookID, c.SHA256), scanCover, "cover")
	switch {
	case err == nil:
		c.ID, c.CreatedAt = cur.ID, cur.CreatedAt
		if cur.Source == c.Source && cur.Mime == c.Mime && cur.Width == c.Width && cur.Height == c.Height &&
			cur.Path == c.Path && cur.Tint == c.Tint && bytes.Equal(cur.Accent, c.Accent) {
			return nil
		}
		_, err = t.exec(`UPDATE covers SET source = ?, mime = ?, width = ?, height = ?, path = ?, tint = ?, accent_json = ? WHERE id = ?`,
			c.Source, c.Mime, c.Width, c.Height, c.Path, nullStr(c.Tint), accent, c.ID)
		return err
	case !errors.Is(err, ErrNotFound):
		return err
	}
	if c.ID == "" {
		c.ID = db.NewID("cv")
	}
	c.CreatedAt = t.r.nowMs()
	_, err = t.exec(`INSERT INTO covers(id, book_id, source, mime, width, height, sha256, path, tint, accent_json, created_at)
  VALUES (?,?,?,?,?,?,?,?,?,?,?)`, c.ID, c.BookID, c.Source, c.Mime, c.Width, c.Height, c.SHA256, c.Path, nullStr(c.Tint), accent, c.CreatedAt)
	if db.IsForeignKeyViolation(err) {
		return fmt.Errorf("%w: book", ErrNotFound)
	}
	return mapErr(err, "cover")
}

// DeleteCover deletes a cover row. A book that used it is left without a cover (the database
// clears cover_id). The files are the caller's to remove.
func (t *Tx) DeleteCover(id string) error {
	res, err := t.exec(`DELETE FROM covers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	return needRow(res, "cover")
}

// SetBookCover makes coverID (one of the book's own covers) the book's cover; "" clears it. It does
// not touch updated_at: the cover job is not an edit by the owner.
func (t *Tx) SetBookCover(bookID, coverID string) error {
	if coverID != "" {
		c, err := t.Cover(coverID)
		if err != nil {
			return err
		}
		if c.BookID != bookID {
			return invalid("cover belongs to another book")
		}
	}
	res, err := t.exec(`UPDATE books SET cover_id = ? WHERE id = ?`, nullStr(coverID), bookID)
	if err != nil {
		return err
	}
	return needRow(res, "book")
}

// PruneChosenCovers keeps at most keep covers that someone chose (source upload or online) for the
// book. It deletes the oldest ones first and never the book's current cover, and returns the rows it
// deleted so the caller can remove their files.
func (t *Tx) PruneChosenCovers(bookID string, keep int) ([]Cover, error) {
	if keep < 0 {
		return nil, invalid("keep must not be negative")
	}
	var current sql.NullString
	if err := t.tx.QueryRowContext(t.ctx, `SELECT cover_id FROM books WHERE id = ?`, bookID).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: book", ErrNotFound)
		}
		return nil, err
	}
	rows, err := many(t.ctx, t.tx, scanCover, `SELECT `+coverCols+` FROM covers
  WHERE book_id = ? AND source IN (?, ?) ORDER BY created_at, rowid`, bookID, CoverUpload, CoverOnline)
	if err != nil {
		return nil, err
	}
	var gone []Cover
	for _, r := range rows {
		if len(rows)-len(gone) <= keep {
			break
		}
		if r.ID == current.String {
			continue
		}
		if err := t.DeleteCover(r.ID); err != nil {
			return nil, err
		}
		gone = append(gone, r)
	}
	return gone, nil
}
