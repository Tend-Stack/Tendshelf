package library

import (
	"database/sql"
	"strings"
)

// MergeRowsSQL are the statements that move one person's data from the book :from onto the book :to when two
// books become one. They are shared by the two merges, a text book into an audiobook (internal/ebook) and one
// audiobook copy into another (MergeBooks). They use the named arguments :from, :to and :now; run them
// through BindFor, which keeps only the arguments a statement mentions. Rows only: no file is opened.
//
// The newest reading place wins whichever format it was made in, and ties keep what the kept book already had.
// A row that would collide with one the kept book has (OR IGNORE) stays behind and goes with the dropped book.
var MergeRowsSQL = []string{
	`INSERT INTO reading_positions SELECT user_id, :to, edition_id, chapter_idx, sid, off, quote, char_pos, percent_bp, finished_at,
  client_updated_at, server_updated_at, device_id, device_label FROM reading_positions WHERE book_id=:from AND true
  ON CONFLICT(user_id, book_id) DO UPDATE SET edition_id=excluded.edition_id, chapter_idx=excluded.chapter_idx, sid=excluded.sid,
    off=excluded.off, quote=excluded.quote, char_pos=excluded.char_pos, percent_bp=excluded.percent_bp, finished_at=excluded.finished_at,
    client_updated_at=excluded.client_updated_at, server_updated_at=excluded.server_updated_at, device_id=excluded.device_id,
    device_label=excluded.device_label
  WHERE excluded.client_updated_at > reading_positions.client_updated_at`,
	`UPDATE reading_sessions SET book_id=:to WHERE book_id=:from`,
	`UPDATE highlights SET book_id=:to WHERE book_id=:from`,
	`UPDATE bookmarks SET book_id=:to WHERE book_id=:from`,
	`UPDATE OR IGNORE word_list SET book_id=:to WHERE book_id=:from`,
	`UPDATE OR IGNORE shelf_items SET book_id=:to WHERE book_id=:from`,
	`UPDATE OR IGNORE text_choices SET book_id=:to WHERE book_id=:from`,
	`UPDATE merged_books SET to_id=:to WHERE to_id=:from`,
	`INSERT OR REPLACE INTO merged_books(from_id, to_id, merged_at) VALUES (:from, :to, :now)`,
}

// BindFor keeps the named arguments a statement uses: the driver refuses a named argument the SQL does not mention.
func BindFor(q string, all []any) []any {
	var out []any
	for _, a := range all {
		if n, ok := a.(sql.NamedArg); ok && strings.Contains(q, ":"+n.Name) {
			out = append(out, a)
		}
	}
	return out
}
