package library

import (
	"context"
	"strings"

	"github.com/Tend-Stack/tendshelf/internal/share"
)

// HighlightsPage is how many highlights one page of "All my highlights" holds.
const HighlightsPage = 50

// highlightColours are the colours the highlights table accepts; any other filter value is ignored.
var highlightColours = map[string]bool{"yellow": true, "green": true, "blue": true, "pink": true}

// HighlightQuery narrows AllHighlights. Text matches the quote or the note, Colour is one of the four colours,
// BookID is one book. Read is the share.BookFilterFor(user, "b", share.ReadText) fragment: a highlight is
// the person's own words from a book's text, so the book must still be readable by them. A zero Read matches nothing.
type HighlightQuery struct {
	Text, Colour, BookID string
	Read                 share.Filter
	Offset, Limit        int
}

// HighlightRow is one highlight with its book's title. ChapterTitle is "" when the edition is gone or has no title.
type HighlightRow struct {
	ID, BookID, BookTitle, EditionID string
	Chapter                          int
	ChapterTitle                     string
	StartSID                         string
	StartOff                         int
	Quote, Note, Colour              string
	CreatedAt                        int64
}

// AllHighlights lists the person's live highlights across books, newest first, from books the scope lets them see
// and q.Read lets them read: both are in the SQL, so a revoked book lists nothing and its title never leaves the
// database (the rows stay stored). total counts every match, not just the page. Text is a bound parameter with
// wildcards escaped.
func (r *Repo) AllHighlights(ctx context.Context, sc Scope, userID string, q HighlightQuery) ([]HighlightRow, int, error) {
	limit := q.Limit
	if limit <= 0 {
		limit = HighlightsPage
	}
	w := visible(sc, true)
	read := q.Read
	if read.SQL == "" {
		read = share.Filter{SQL: "0=1"}
	}
	w.add("("+read.SQL+")", read.Args...)
	w.add("h.user_id = ?", userID)
	w.add("h.deleted_at IS NULL")
	if q.BookID != "" {
		w.add("h.book_id = ?", q.BookID)
	}
	if highlightColours[q.Colour] {
		w.add("h.colour = ?", q.Colour)
	}
	if t := strings.TrimSpace(q.Text); t != "" {
		like := "%" + likeEscape(t) + "%"
		w.add(`(h.quote LIKE ? ESCAPE '\' OR h.note LIKE ? ESCAPE '\')`, like, like)
	}
	from := fromVisible + ` JOIN highlights h ON h.book_id = b.id WHERE ` + w.sql()
	var total int
	if err := r.st.Reader().QueryRowContext(ctx, `SELECT count(*)`+from, w.args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := many(ctx, r.st.Reader(), func(s scanner) (HighlightRow, error) {
		var x HighlightRow
		err := s.Scan(&x.ID, &x.BookID, &x.BookTitle, &x.EditionID, &x.Chapter, &x.ChapterTitle, &x.StartSID, &x.StartOff,
			&x.Quote, &x.Note, &x.Colour, &x.CreatedAt)
		return x, err
	}, `SELECT h.id, h.book_id, b.title, COALESCE(h.edition_id, ''), h.chapter_idx,
  COALESCE((SELECT ec.title FROM edition_chapters ec WHERE ec.edition_id = h.edition_id AND ec.idx = h.chapter_idx), ''),
  h.start_sid, h.start_off, h.quote, h.note, h.colour, h.created_at`+from+
		` ORDER BY h.created_at DESC, h.id DESC LIMIT ? OFFSET ?`, append(append([]any(nil), w.args...), limit, max(q.Offset, 0))...)
	return rows, total, err
}
