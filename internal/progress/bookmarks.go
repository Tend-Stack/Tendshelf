package progress

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/ebook"
)

// Bookmark is a saved moment in a book with an optional note. DeletedAt is 0 for a live bookmark.
type Bookmark struct {
	ID, UserID, BookID string
	PositionMs         int64
	Note               string
	CreatedAt          int64
	UpdatedAt          int64
	DeletedAt          int64
	ClientID           string // the id the app chose when it created the bookmark, "" if none
	EditionID, SID     string // set for a bookmark made in the text: the edition and the sentence id; "" for a moment in the audio
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

const selectBookmark = `SELECT id, user_id, book_id, position_ms, note, created_at, updated_at, deleted_at, coalesce(client_id, ''), coalesce(edition_id, ''), coalesce(sid, '') FROM bookmarks`

func scanBookmark(r rowScanner) (Bookmark, error) {
	var b Bookmark
	var del sql.NullInt64
	err := r.Scan(&b.ID, &b.UserID, &b.BookID, &b.PositionMs, &b.Note, &b.CreatedAt, &b.UpdatedAt, &del, &b.ClientID, &b.EditionID, &b.SID)
	b.DeletedAt = del.Int64
	if errors.Is(err, sql.ErrNoRows) {
		return Bookmark{}, ErrNotFound
	}
	return b, err
}

// AddBookmark saves a moment of the book for the person, with an optional note.
func (s *Service) AddBookmark(ctx context.Context, userID, bookID string, positionMs int64, note string) (Bookmark, error) {
	b, _, err := s.AddBookmarkWithClientID(ctx, userID, bookID, positionMs, note, "")
	return b, err
}

// ValidClientID reports whether id is usable as a client chosen bookmark id: 1 to 64 of letters, digits, "-" and "_".
func ValidClientID(id string) bool {
	if id == "" || len(id) > 64 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

// AddBookmarkWithClientID is AddBookmark with an optional client id that makes a retry safe: the same
// person sending the same id for the same book gets the existing bookmark back (created false) and nothing
// is written. An id already used by another person or for another book is ErrClientIDTaken.
func (s *Service) AddBookmarkWithClientID(ctx context.Context, userID, bookID string, positionMs int64, note, clientID string) (b Bookmark, created bool, err error) {
	return s.insertBookmark(ctx, userID, bookID, "", "", positionMs, note, clientID)
}

// AddTextBookmark saves a place in the text of an edition: the sentence id (sid) and an audio moment
// (positionMs, 0 for a text place). editionID and sid are both required and sid must parse.
// The client id rules are those of AddBookmarkWithClientID.
func (s *Service) AddTextBookmark(ctx context.Context, userID, bookID, editionID, sid string, positionMs int64, note, clientID string) (Bookmark, bool, error) {
	if editionID == "" {
		return Bookmark{}, false, &InvalidError{"edition_id"}
	}
	if _, _, _, ok := ebook.ParseSID(sid); !ok {
		return Bookmark{}, false, &InvalidError{"sid"}
	}
	return s.insertBookmark(ctx, userID, bookID, editionID, sid, positionMs, note, clientID)
}

// insertBookmark is the one insert behind both kinds of bookmark; editionID and sid are "" for a moment in the audio.
func (s *Service) insertBookmark(ctx context.Context, userID, bookID, editionID, sid string, positionMs int64, note, clientID string) (b Bookmark, created bool, err error) {
	switch {
	case userID == "":
		return Bookmark{}, false, &InvalidError{"user_id"}
	case bookID == "":
		return Bookmark{}, false, &InvalidError{"book_id"}
	case positionMs < 0:
		return Bookmark{}, false, &InvalidError{"position_ms"}
	case clientID != "" && !ValidClientID(clientID):
		return Bookmark{}, false, &InvalidError{"client_id"}
	}
	note, err = cleanNote(note)
	if err != nil {
		return Bookmark{}, false, err
	}
	now := s.nowMs()
	b = Bookmark{ID: db.NewID("bm"), UserID: userID, BookID: bookID, PositionMs: positionMs, Note: note, CreatedAt: now, UpdatedAt: now, ClientID: clientID, EditionID: editionID, SID: sid}
	err = s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		created = false
		// A merged-away book id lands on the book that took over (see resolveBook).
		resolved, err := resolveBook(ctx, tx, bookID)
		if err != nil {
			return err
		}
		bookID, b.BookID = resolved, resolved
		if clientID != "" {
			old, err := scanBookmark(tx.QueryRowContext(ctx, selectBookmark+` WHERE client_id = ? ORDER BY (user_id = ?) DESC LIMIT 1`, clientID, userID))
			switch {
			case err == nil && old.UserID == userID && old.BookID == bookID:
				b = old
				return nil
			case err == nil:
				return ErrClientIDTaken
			case !errors.Is(err, ErrNotFound):
				return err
			}
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM bookmarks WHERE user_id = ? AND book_id = ? AND deleted_at IS NULL`,
			userID, bookID).Scan(&n); err != nil {
			return err
		}
		if n >= MaxBookmarksPerBook {
			return ErrLimit
		}
		var cid any
		if clientID != "" {
			cid = clientID
		}
		var eid, bsid any
		if editionID != "" {
			eid, bsid = editionID, sid
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO bookmarks(id, user_id, book_id, position_ms, note, created_at, updated_at, client_id, edition_id, sid) VALUES (?,?,?,?,?,?,?,?,?,?)`,
			b.ID, userID, bookID, positionMs, note, now, now, cid, eid, bsid)
		created = err == nil
		return err
	})
	if db.IsForeignKeyViolation(err) {
		return Bookmark{}, false, ErrNotFound
	}
	if err != nil {
		return Bookmark{}, false, err
	}
	return b, created, nil
}

const bookmarksChangedMax = 1000

type changeCursor struct {
	U int64  `json:"u"`
	I string `json:"i"`
}

func (c changeCursor) encode() string {
	raw, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(raw)
}

// BookmarksChanged lists the person's bookmarks on one book changed after since, tombstones included,
// oldest change first, with a cursor for the next page ("" at the end). A bad cursor is ErrInvalid.
func (s *Service) BookmarksChanged(ctx context.Context, userID, bookID string, since int64, cursor string, limit int) ([]Bookmark, string, error) {
	if limit <= 0 {
		limit = bookmarksChangedMax
	}
	limit = min(limit, bookmarksChangedMax)
	q := selectBookmark + ` WHERE user_id = ? AND book_id = ? AND updated_at > ?`
	args := []any{userID, bookID, since}
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		var c changeCursor
		if err != nil || json.Unmarshal(raw, &c) != nil || c.I == "" || c.U < 0 {
			return nil, "", ErrInvalid
		}
		q += ` AND (updated_at, id) > (?, ?)`
		args = append(args, c.U, c.I)
	}
	rows, err := s.st.Reader().QueryContext(ctx, q+` ORDER BY updated_at, id LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return nil, "", err
	}
	var out []Bookmark
	for rows.Next() {
		b, err := scanBookmark(rows)
		if err != nil {
			rows.Close()
			return nil, "", err
		}
		out = append(out, b)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, "", err
	}
	next := ""
	if len(out) > limit {
		out = out[:limit]
		next = changeCursor{out[limit-1].UpdatedAt, out[limit-1].ID}.encode()
	}
	return out, next, nil
}

// BookmarksByIDs returns the person's bookmarks (tombstones included) with the given ids, keyed by id.
func (s *Service) BookmarksByIDs(ctx context.Context, userID string, ids []string) (map[string]Bookmark, error) {
	out := make(map[string]Bookmark, len(ids))
	for len(ids) > 0 {
		n := min(len(ids), forBooksChunk)
		args := []any{userID}
		for _, id := range ids[:n] {
			args = append(args, id)
		}
		ids = ids[n:]
		rows, err := s.st.Reader().QueryContext(ctx, selectBookmark+` WHERE user_id = ? AND id IN (?`+strings.Repeat(",?", n-1)+`)`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			b, err := scanBookmark(rows)
			if err != nil {
				rows.Close()
				return nil, err
			}
			out[b.ID] = b
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// Bookmarks lists the person's live audio bookmarks on a book (the Bookmarks tab), newest first; withNotes keeps
// only those with a note. A place in the text (sid set) belongs to the reader's marks sheet: see TextBookmarks.
func (s *Service) Bookmarks(ctx context.Context, userID, bookID string, withNotes bool) ([]Bookmark, error) {
	return s.bookmarksWhere(ctx, userID, bookID, withNotes, ` AND sid IS NULL`)
}

// TextBookmarks lists the person's live bookmarks made in the text of a book, newest first.
func (s *Service) TextBookmarks(ctx context.Context, userID, bookID string) ([]Bookmark, error) {
	return s.bookmarksWhere(ctx, userID, bookID, false, ` AND sid IS NOT NULL`)
}

func (s *Service) bookmarksWhere(ctx context.Context, userID, bookID string, withNotes bool, extra string) ([]Bookmark, error) {
	q := selectBookmark + ` WHERE user_id = ? AND book_id = ? AND deleted_at IS NULL` + extra
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
WHERE user_id = ? AND book_id = ? AND deleted_at IS NULL AND sid IS NULL`, userID, bookID).Scan(&all, &withNotes)
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
