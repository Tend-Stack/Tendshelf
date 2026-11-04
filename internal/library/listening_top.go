package library

import (
	"context"
	"fmt"
)

// Reads for the listening dashboard that name a book, an author or a narrator. They go through the
// scope SQL, so a book the person can no longer see never appears by name; its time still counts in
// the totals that internal/progress computes without naming anything.

const (
	// topMinMs is the least a book, author or narrator needs to be listed: under a minute is noise.
	topMinMs = 60_000
	// clipLookbackMs is progress.SplitLookbackMs; this package does not import internal/progress.
	clipLookbackMs = 86_400_000
)

// TopBook is a visible book with the time listened to it in a period.
type TopBook struct {
	Book BookCard
	Ms   int64
}

// TopName is an author or a narrator with the time listened to their visible books.
type TopName struct {
	ID, Name string
	Ms       int64
}

// TopListened is the most listened books, authors and narrators of a period.
type TopListened struct {
	Books              []TopBook
	Authors, Narrators []TopName
}

// listenedCTE sums, per book, the part of the person's listening that falls in [from, to) ms. A
// session's listened_ms is spread evenly over its wall-clock span, which is capped to its last 24 hours
// and clipped to the period: the same rule as progress.splitSession, up to 1 ms per row of rounding.
// Its arguments, in order: to, from (clip), user, from-lookback, to, from, from (row filter).
func listenedCTE(userID string, from, to int64) (string, []any) {
	es := fmt.Sprintf("MAX(s.started_at, s.ended_at - %d)", clipLookbackMs)
	q := `WITH t(book_id, ms) AS (SELECT s.book_id, SUM(CASE WHEN s.ended_at <= ` + es + ` THEN s.listened_ms
  ELSE s.listened_ms * MAX(0, MIN(s.ended_at, ?) - MAX(` + es + `, ?)) / (s.ended_at - ` + es + `) END)
  FROM listening_sessions s WHERE s.user_id = ? AND s.started_at >= ? AND s.started_at < ?
  AND (s.ended_at > ? OR s.started_at >= ?) GROUP BY s.book_id) `
	return q, []any{to, from, userID, from - clipLookbackMs, to, from, from}
}

// TopListened lists the person's top books, authors and narrators in [from, to) (Unix ms), visible
// books only, longest first, ties by title or name. A person is credited the full time of each of their
// visible books. Sums under a minute are dropped.
func (r *Repo) TopListened(ctx context.Context, sc Scope, userID string, from, to int64, books, people int) (TopListened, error) {
	var out TopListened
	books, people = listeningLimit(books), listeningLimit(people)
	cte, cteArgs := listenedCTE(userID, from, to)
	w := visible(sc, true)
	type row struct {
		id string
		ms int64
	}
	bookRows, err := many(ctx, r.st.Reader(), func(s scanner) (row, error) {
		var x row
		return x, s.Scan(&x.id, &x.ms)
	}, cte+`SELECT b.id, t.ms`+fromVisible+` JOIN t ON t.book_id = b.id WHERE `+w.sql()+` AND t.ms >= ?
  ORDER BY t.ms DESC, `+titleKey+` COLLATE NOCASE, b.id LIMIT ?`,
		append(append(append([]any(nil), cteArgs...), w.args...), topMinMs, books)...)
	if err != nil {
		return out, err
	}
	if len(bookRows) > 0 {
		ids := make([]string, len(bookRows))
		for i, x := range bookRows {
			ids[i] = x.id
		}
		cards, err := r.cards(ctx, ids)
		if err != nil {
			return out, err
		}
		byID := make(map[string]BookCard, len(cards))
		for _, c := range cards {
			byID[c.ID] = c
		}
		for _, x := range bookRows {
			if c, ok := byID[x.id]; ok {
				out.Books = append(out.Books, TopBook{Book: c, Ms: x.ms})
			}
		}
	}
	for _, role := range []string{"author", "narrator"} {
		w := visible(sc, true)
		names, err := many(ctx, r.st.Reader(), func(s scanner) (TopName, error) {
			var n TopName
			return n, s.Scan(&n.ID, &n.Name, &n.Ms)
		}, cte+`SELECT p.id, p.name, SUM(t.ms) AS ms`+fromVisible+` JOIN t ON t.book_id = b.id
  JOIN book_people bp ON bp.book_id = b.id AND bp.role = ? JOIN people p ON p.id = bp.person_id
  WHERE `+w.sql()+` GROUP BY p.id HAVING ms >= ? ORDER BY ms DESC, p.name COLLATE NOCASE, p.id LIMIT ?`,
			append(append(append([]any(nil), cteArgs...), role), append(w.args, topMinMs, people)...)...)
		if err != nil {
			return out, err
		}
		if role == "author" {
			out.Authors = names
		} else {
			out.Narrators = names
		}
	}
	return out, nil
}

// FinishedBook is a visible book the person finished, with when.
type FinishedBook struct {
	Book BookCard
	At   int64
}

// FinishedBetween lists the visible books the person finished in [from, to) (Unix ms), newest first.
func (r *Repo) FinishedBetween(ctx context.Context, sc Scope, userID string, from, to int64, limit int) ([]FinishedBook, error) {
	w := visible(sc, true)
	w.add("p.user_id = ?", userID)
	w.add("p.finished_at >= ? AND p.finished_at < ?", from, to)
	type row struct {
		id string
		at int64
	}
	rows, err := many(ctx, r.st.Reader(), func(s scanner) (row, error) {
		var x row
		return x, s.Scan(&x.id, &x.at)
	}, `SELECT b.id, p.finished_at`+fromVisible+` JOIN progress p ON p.book_id = b.id WHERE `+w.sql()+`
  ORDER BY p.finished_at DESC, b.id LIMIT ?`, append(append([]any(nil), w.args...), listeningLimit(limit))...)
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	ids := make([]string, len(rows))
	for i, x := range rows {
		ids[i] = x.id
	}
	cards, err := r.cards(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]BookCard, len(cards))
	for _, c := range cards {
		byID[c.ID] = c
	}
	var out []FinishedBook
	for _, x := range rows {
		if c, ok := byID[x.id]; ok {
			out = append(out, FinishedBook{Book: c, At: x.at})
		}
	}
	return out, nil
}
