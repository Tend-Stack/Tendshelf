package progress

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

// Bookmark is a saved moment in a book with an optional note. DeletedAt is 0 for a live bookmark.
type Bookmark struct {
	ID, UserID, BookID string
	PositionMs         int64
	Note               string
	CreatedAt          int64
	UpdatedAt          int64
	DeletedAt          int64
}

// cleanNote trims space, turns CRLF into LF and refuses other control characters (newline and tab are fine)
// and notes longer than MaxNoteRunes.
func cleanNote(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", &InvalidError{"note"}
	}
	s = strings.TrimSpace(strings.ReplaceAll(s, "\r", ""))
	for _, r := range s {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return "", &InvalidError{"note"}
		}
	}
	if utf8.RuneCountInString(s) > MaxNoteRunes {
		return "", &InvalidError{"note"}
	}
	return s, nil
}

const selectBookmark = `SELECT id, user_id, book_id, position_ms, note, created_at, updated_at, deleted_at FROM bookmarks`

func scanBookmark(r rowScanner) (Bookmark, error) {
	var b Bookmark
	var del sql.NullInt64
	err := r.Scan(&b.ID, &b.UserID, &b.BookID, &b.PositionMs, &b.Note, &b.CreatedAt, &b.UpdatedAt, &del)
	b.DeletedAt = del.Int64
	if errors.Is(err, sql.ErrNoRows) {
		return Bookmark{}, ErrNotFound
	}
	return b, err
}

// AddBookmark saves a moment of the book for the person, with an optional note.
func (s *Service) AddBookmark(ctx context.Context, userID, bookID string, positionMs int64, note string) (Bookmark, error) {
	switch {
	case userID == "":
		return Bookmark{}, &InvalidError{"user_id"}
	case bookID == "":
		return Bookmark{}, &InvalidError{"book_id"}
	case positionMs < 0:
		return Bookmark{}, &InvalidError{"position_ms"}
	}
	note, err := cleanNote(note)
	if err != nil {
		return Bookmark{}, err
	}
	now := s.nowMs()
	b := Bookmark{ID: db.NewID("bm"), UserID: userID, BookID: bookID, PositionMs: positionMs, Note: note, CreatedAt: now, UpdatedAt: now}
	err = s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM bookmarks WHERE user_id = ? AND book_id = ? AND deleted_at IS NULL`,
			userID, bookID).Scan(&n); err != nil {
			return err
		}
		if n >= MaxBookmarksPerBook {
			return ErrLimit
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO bookmarks(id, user_id, book_id, position_ms, note, created_at, updated_at) VALUES (?,?,?,?,?,?,?)`,
			b.ID, userID, bookID, positionMs, note, now, now)
		return err
	})
	if db.IsForeignKeyViolation(err) {
		return Bookmark{}, ErrNotFound
	}
	if err != nil {
		return Bookmark{}, err
	}
	return b, nil
}

// Bookmarks lists the person's live bookmarks on a book, newest first; withNotes keeps only those with a note.
func (s *Service) Bookmarks(ctx context.Context, userID, bookID string, withNotes bool) ([]Bookmark, error) {
	q := selectBookmark + ` WHERE user_id = ? AND book_id = ? AND deleted_at IS NULL`
	if withNotes {
		q += ` AND note <> ''`
	}
	rows, err := s.st.Reader().QueryContext(ctx, q+` ORDER BY created_at DESC, id DESC`, userID, bookID)
	if err != nil {
		return nil, err
	}
	var out []Bookmark
	for rows.Next() {
		b, err := scanBookmark(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, b)
	}
	return out, errors.Join(rows.Err(), rows.Close())
}

// BookmarkCounts returns how many live bookmarks the person has on a book, and how many of them carry a note.
func (s *Service) BookmarkCounts(ctx context.Context, userID, bookID string) (all, withNotes int, err error) {
	err = s.st.Reader().QueryRowContext(ctx, `SELECT count(*), coalesce(sum(note <> ''), 0) FROM bookmarks
WHERE user_id = ? AND book_id = ? AND deleted_at IS NULL`, userID, bookID).Scan(&all, &withNotes)
	return all, withNotes, err
}

// Bookmark returns one live bookmark of the person, or ErrNotFound (also for another person's bookmark).
func (s *Service) Bookmark(ctx context.Context, userID, id string) (Bookmark, error) {
	return scanBookmark(s.st.Reader().QueryRowContext(ctx, selectBookmark+` WHERE id = ? AND user_id = ? AND deleted_at IS NULL`, id, userID))
}

// SetBookmarkNote replaces the note of a live bookmark of the person (an empty note clears it).
func (s *Service) SetBookmarkNote(ctx context.Context, userID, id, note string) (Bookmark, error) {
	note, err := cleanNote(note)
	if err != nil {
		return Bookmark{}, err
	}
	now := s.nowMs()
	var b Bookmark
	err = s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `UPDATE bookmarks SET note = ?, updated_at = ? WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
			note, now, id, userID)
		if err != nil {
			return err
		}
		if n, err := r.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return ErrNotFound
		}
		b, err = scanBookmark(tx.QueryRowContext(ctx, selectBookmark+` WHERE id = ? AND user_id = ?`, id, userID))
		return err
	})
	return b, err
}

// DeleteBookmark soft-deletes a live bookmark of the person: the tombstone keeps id, position and times, and the note is cleared.
func (s *Service) DeleteBookmark(ctx context.Context, userID, id string) error {
	now := s.nowMs()
	return s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `UPDATE bookmarks SET deleted_at = ?, updated_at = ?, note = '' WHERE id = ? AND user_id = ? AND deleted_at IS NULL`,
			now, now, id, userID)
		if err != nil {
			return err
		}
		if n, err := r.RowsAffected(); err != nil {
			return err
		} else if n == 0 {
			return ErrNotFound
		}
		return nil
	})
}
