package library

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// WordListed is one saved word with the book it came from (G1). Edition and SID are empty when the place is gone.
type WordListed struct {
	ID, BookID, BookTitle, EditionID string
	Word, Quote, SID                 string
	Off                              int
	CreatedAt                        int64
}

// WordListMax is the most words one WordList call returns.
const WordListMax = 200

// WordList lists the person's saved words, newest first, from books the scope lets them see: the grants are in the
// SQL, so a book that was revoked lists nothing and its title never leaves the database. bookID narrows it to one
// book ("" for all). cursor is the Next of the previous page ("" for the first); Next is "" on the last page.
func (r *Repo) WordList(ctx context.Context, sc Scope, userID, bookID, cursor string, limit int) (words []WordListed, next string, err error) {
	limit = min(max(limit, 1), WordListMax)
	w := visible(sc, true)
	w.add("w.user_id = ?", userID)
	if bookID != "" {
		w.add("w.book_id = ?", bookID)
	}
	if cursor != "" {
		at, id, ok := strings.Cut(cursor, ":")
		ms, perr := strconv.ParseInt(at, 10, 64)
		if !ok || perr != nil || id == "" {
			return nil, "", invalid("cursor is not valid")
		}
		w.add("(w.created_at < ? OR (w.created_at = ? AND w.id < ?))", ms, ms, id)
	}
	rows, err := many(ctx, r.st.Reader(), func(s scanner) (WordListed, error) {
		var x WordListed
		err := s.Scan(&x.ID, &x.BookID, &x.BookTitle, &x.EditionID, &x.Word, &x.Quote, &x.SID, &x.Off, &x.CreatedAt)
		return x, err
	}, `SELECT w.id, w.book_id, b.title, COALESCE(w.edition_id, ''), w.word, w.quote, COALESCE(w.sid, ''), w.off, w.created_at`+
		fromVisible+` JOIN word_list w ON w.book_id = b.id WHERE `+w.sql()+
		` ORDER BY w.created_at DESC, w.id DESC LIMIT ?`, append(append([]any(nil), w.args...), limit+1)...)
	if err != nil {
		return nil, "", err
	}
	if len(rows) > limit {
		rows = rows[:limit]
		last := rows[limit-1]
		next = fmt.Sprintf("%d:%s", last.CreatedAt, last.ID)
	}
	return rows, next, nil
}
