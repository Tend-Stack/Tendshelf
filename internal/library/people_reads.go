package library

import (
	"context"
	"strings"
)

// PersonDuration is the total length, in milliseconds, of the visible listable books a person has
// in one role (RoleAuthor or RoleNarrator). Each book counts once. Scope and Listable apply in
// SQL, so a book the person cannot open adds nothing; an unknown role is ErrInvalid.
func (r *Repo) PersonDuration(ctx context.Context, sc Scope, personID, role string) (int64, error) {
	if role != RoleAuthor && role != RoleNarrator {
		return 0, invalid("unknown role %q", role)
	}
	w := visible(sc, true)
	w.add(`EXISTS (SELECT 1 FROM book_people x WHERE x.book_id = b.id AND x.role = ? AND x.person_id = ?)`, role, personID)
	var ms int64
	err := r.st.Reader().QueryRowContext(ctx, `SELECT COALESCE(SUM(b.duration_ms), 0)`+fromVisible+` WHERE `+w.sql(), w.args...).Scan(&ms)
	return ms, err
}

// SeriesSequences maps the visible listable books of a series to their place in it ("1", "0.5"),
// as the series page and the book page need it: BookCard.Series only carries a book's first
// series. A book with no sequence text maps to "".
func (r *Repo) SeriesSequences(ctx context.Context, sc Scope, seriesID string) (map[string]string, error) {
	w := visible(sc, true)
	w.add("bs.series_id = ?", seriesID)
	rows, err := r.st.Reader().QueryContext(ctx, `SELECT b.id, bs.sequence`+fromVisible+
		` JOIN book_series bs ON bs.book_id = b.id WHERE `+w.sql(), w.args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var id, seq string
		if err := rows.Scan(&id, &seq); err != nil {
			return nil, err
		}
		out[id] = strings.TrimSpace(seq)
	}
	return out, rows.Err()
}
