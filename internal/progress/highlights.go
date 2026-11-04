package progress

import (
	"context"
	"database/sql"
	"errors"
	"unicode/utf8"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/ebook"
)

// Highlight is a passage of an edition's text the person marked: a sentence id and character offset at each end
// (offsets count UTF-16 units of the block text), the quote with a little context, and, when the alignment knew the
// passage, its time range. DeletedAt is 0 for a live highlight.
type Highlight struct {
	ID, UserID, BookID, EditionID, AlignmentID string
	StartSID, EndSID                           string
	Quote, Prefix, Suffix                      string
	Colour, Note, ClientID                     string
	Chapter, StartOff, EndOff                  int
	StartMs, EndMs                             *int64
	CreatedAt, UpdatedAt, DeletedAt            int64
}

// MaxHighlightsPerBook is how many live highlights one person may keep on one book. It is a variable so tests can lower it.
var MaxHighlightsPerBook = 2000

const (
	maxQuoteRunes   = 4000
	maxContextRunes = 32
	defaultColour   = "yellow"
)

func validColour(c string) bool {
	switch c {
	case "yellow", "green", "blue", "pink":
		return true
	}
	return false
}

const selectHighlight = `SELECT id, user_id, book_id, coalesce(edition_id, ''), coalesce(alignment_id, ''), chapter_idx, start_sid, start_off,
end_sid, end_off, quote, quote_prefix, quote_suffix, start_ms, end_ms, colour, note, coalesce(client_id, ''), created_at, updated_at, deleted_at FROM highlights`

func scanHighlight(r rowScanner) (Highlight, error) {
	var h Highlight
	var del, startMs, endMs sql.NullInt64
	err := r.Scan(&h.ID, &h.UserID, &h.BookID, &h.EditionID, &h.AlignmentID, &h.Chapter, &h.StartSID, &h.StartOff,
		&h.EndSID, &h.EndOff, &h.Quote, &h.Prefix, &h.Suffix, &startMs, &endMs, &h.Colour, &h.Note, &h.ClientID,
		&h.CreatedAt, &h.UpdatedAt, &del)
	if errors.Is(err, sql.ErrNoRows) {
		return Highlight{}, ErrNotFound
	}
	h.DeletedAt = del.Int64
	if startMs.Valid {
		h.StartMs = &startMs.Int64
	}
	if endMs.Valid {
		h.EndMs = &endMs.Int64
	}
	return h, err
}

// checkHighlight validates h and normalises its colour and note.
func checkHighlight(h *Highlight) error {
	switch {
	case h.UserID == "":
		return &InvalidError{"user_id"}
	case h.BookID == "":
		return &InvalidError{"book_id"}
	case h.Chapter < 1:
		return &InvalidError{"chapter"}
	case h.StartOff < 0 || h.EndOff < 0:
		return &InvalidError{"offset"}
	case h.ClientID != "" && !ValidClientID(h.ClientID):
		return &InvalidError{"client_id"}
	}
	sch, _, _, ok1 := ebook.ParseSID(h.StartSID)
	ech, _, _, ok2 := ebook.ParseSID(h.EndSID)
	if !ok1 || !ok2 {
		return &InvalidError{"sid"}
	}
	if sch != h.Chapter || ech != h.Chapter {
		return &InvalidError{"chapter"}
	}
	switch c := ebook.CompareSID(h.StartSID, h.EndSID); {
	case c > 0, c == 0 && h.StartOff >= h.EndOff:
		return &InvalidError{"range"}
	}
	if !utf8.ValidString(h.Quote) || !utf8.ValidString(h.Prefix) || !utf8.ValidString(h.Suffix) {
		return &InvalidError{"quote"}
	}
	if n := utf8.RuneCountInString(h.Quote); n < 1 || n > maxQuoteRunes {
		return &InvalidError{"quote"}
	}
	if utf8.RuneCountInString(h.Prefix) > maxContextRunes || utf8.RuneCountInString(h.Suffix) > maxContextRunes {
		return &InvalidError{"quote"}
	}
	if (h.StartMs == nil) != (h.EndMs == nil) || h.StartMs != nil && (*h.StartMs < 0 || *h.EndMs < *h.StartMs) {
		return &InvalidError{"time"}
	}
	if h.Colour == "" {
		h.Colour = defaultColour
	}
	if !validColour(h.Colour) {
		return &InvalidError{"colour"}
	}
	note, err := cleanNote(h.Note)
	if err != nil {
		return err
	}
	h.Note = note
	return nil
}

// AddHighlight saves a highlight for h.UserID. ID, times and DeletedAt are set here. With a ClientID a retry is
// safe: the same person sending the same id for the same book gets the existing highlight back (created false);
// an id used by another person or book is ErrClientIDTaken. More than MaxHighlightsPerBook live ones is ErrLimit.
func (s *Service) AddHighlight(ctx context.Context, h Highlight) (out Highlight, created bool, err error) {
	if err := checkHighlight(&h); err != nil {
		return Highlight{}, false, err
	}
	now := s.nowMs()
	h.ID, h.CreatedAt, h.UpdatedAt, h.DeletedAt = db.NewID("hl"), now, now, 0
	err = s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		created = false
		if h.ClientID != "" {
			old, err := scanHighlight(tx.QueryRowContext(ctx, selectHighlight+` WHERE client_id = ? ORDER BY (user_id = ?) DESC LIMIT 1`, h.ClientID, h.UserID))
			switch {
			case err == nil && old.UserID == h.UserID && old.BookID == h.BookID:
				out = old
				return nil
			case err == nil:
				return ErrClientIDTaken
			case !errors.Is(err, ErrNotFound):
				return err
			}
		}
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM highlights WHERE user_id = ? AND book_id = ? AND deleted_at IS NULL`,
			h.UserID, h.BookID).Scan(&n); err != nil {
			return err
		}
		if n >= MaxHighlightsPerBook {
			return ErrLimit
		}
		var cid, eid, aid, sms, ems any
		if h.ClientID != "" {
			cid = h.ClientID
		}
		if h.EditionID != "" {
			eid = h.EditionID
		}
		if h.AlignmentID != "" {
			aid = h.AlignmentID
		}
		if h.StartMs != nil {
			sms, ems = *h.StartMs, *h.EndMs
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO highlights(id, user_id, book_id, edition_id, chapter_idx, start_sid, start_off, end_sid, end_off,
quote, quote_prefix, quote_suffix, start_ms, end_ms, alignment_id, colour, note, client_id, created_at, updated_at)
VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
			h.ID, h.UserID, h.BookID, eid, h.Chapter, h.StartSID, h.StartOff, h.EndSID, h.EndOff,
			h.Quote, h.Prefix, h.Suffix, sms, ems, aid, h.Colour, h.Note, cid, now, now)
		if err == nil {
			out, created = h, true
		}
		return err
	})
	if db.IsForeignKeyViolation(err) {
		return Highlight{}, false, ErrNotFound
	}
	if err != nil {
		return Highlight{}, false, err
	}
	return out, created, nil
}

func (s *Service) queryHighlights(ctx context.Context, where string, args ...any) ([]Highlight, error) {
	rows, err := s.st.Reader().QueryContext(ctx, selectHighlight+` WHERE deleted_at IS NULL AND `+where, args...)
	if err != nil {
		return nil, err
	}
	var out []Highlight
	for rows.Next() {
		h, err := scanHighlight(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, h)
	}
	return out, errors.Join(rows.Err(), rows.Close())
}

// Highlights lists the person's live highlights on a book, in every edition, oldest first.
func (s *Service) Highlights(ctx context.Context, userID, bookID string) ([]Highlight, error) {
	return s.queryHighlights(ctx, `user_id = ? AND book_id = ? ORDER BY created_at, id`, userID, bookID)
}

// ChapterHighlights lists the person's live highlights in one chapter (1-based) of an edition, in text order.
func (s *Service) ChapterHighlights(ctx context.Context, userID, editionID string, chapter int) ([]Highlight, error) {
	return s.queryHighlights(ctx, `user_id = ? AND edition_id = ? AND chapter_idx = ? ORDER BY start_sid, start_off, id`, userID, editionID, chapter)
}

// Highlight returns one live highlight of the person, or ErrNotFound (also for another person's highlight).
func (s *Service) Highlight(ctx context.Context, userID, id string) (Highlight, error) {
	return scanHighlight(s.st.Reader().QueryRowContext(ctx, selectHighlight+` WHERE id = ? AND user_id = ? AND deleted_at IS NULL`, id, userID))
}

// SetHighlight changes the note and/or colour of a live highlight of the person; a nil argument leaves that field alone.
func (s *Service) SetHighlight(ctx context.Context, userID, id string, note, colour *string) (Highlight, error) {
	var n, c string
	if note != nil {
		var err error
		if n, err = cleanNote(*note); err != nil {
			return Highlight{}, err
		}
	}
	if colour != nil {
		if c = *colour; !validColour(c) {
			return Highlight{}, &InvalidError{"colour"}
		}
	}
	now := s.nowMs()
	var h Highlight
	err := s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `UPDATE highlights SET note = CASE WHEN ? THEN ? ELSE note END, colour = CASE WHEN ? THEN ? ELSE colour END,
updated_at = ? WHERE id = ? AND user_id = ? AND deleted_at IS NULL`, note != nil, n, colour != nil, c, now, id, userID)
		if err != nil {
			return err
		}
		if rows, err := r.RowsAffected(); err != nil {
			return err
		} else if rows == 0 {
			return ErrNotFound
		}
		h, err = scanHighlight(tx.QueryRowContext(ctx, selectHighlight+` WHERE id = ? AND user_id = ?`, id, userID))
		return err
	})
	return h, err
}

// DeleteHighlight soft-deletes a live highlight of the person: the tombstone keeps id, range and times, and the
// note and quote text are cleared (private text goes, as with bookmarks).
func (s *Service) DeleteHighlight(ctx context.Context, userID, id string) error {
	now := s.nowMs()
	return s.st.WriteTx(ctx, func(tx *sql.Tx) error {
		r, err := tx.ExecContext(ctx, `UPDATE highlights SET deleted_at = ?, updated_at = ?, note = '', quote = '', quote_prefix = '', quote_suffix = ''
WHERE id = ? AND user_id = ? AND deleted_at IS NULL`, now, now, id, userID)
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
