package library

import "context"

// SharedBook is a visible book that someone shared with the person: when, and by whom.
type SharedBook struct {
	Book       BookCard
	SharedAt   int64  // Unix ms of the newest source
	SharerName string // "" when the sharer is unknown (the grant's creator was removed)
}

// SharedRow is the Home "Shared with you" row: the newest books, how many in all, and who shared.
type SharedRow struct {
	Books []SharedBook
	// Total counts the visible books shared with the person, not the cards in Books.
	Total int
	// Sharers are the display names of everyone with at least one visible shared book,
	// newest share first. A sharer whose only shares are hidden is not named.
	Sharers []string
}

const maxSharers = 50

// SharedWithYou lists the newest visible books shared with the person bound in sc.Shared. One card
// per book: the newest source wins, ties by sharer id. The zero Scope.Shared shares nothing.
func (r *Repo) SharedWithYou(ctx context.Context, sc Scope, limit int) (SharedRow, error) {
	var out SharedRow
	if sc.Shared.SQL == "" {
		return out, nil
	}
	w := visible(sc, true)
	type row struct {
		id, sharer string
		at         int64
	}
	var total int
	rows, err := many(ctx, r.st.Reader(), func(s scanner) (row, error) {
		var x row
		err := s.Scan(&x.id, &x.at, &x.sharer, &total)
		return x, err
	}, `WITH src(book_id, shared_at, sharer_id) AS (`+sc.Shared.SQL+`),
pick AS (SELECT book_id, shared_at, sharer_id, ROW_NUMBER() OVER (PARTITION BY book_id
  ORDER BY shared_at DESC, coalesce(sharer_id, '')) AS rn FROM src)
SELECT b.id, p.shared_at, coalesce(nullif(u.display_name, ''), u.username, ''), COUNT(*) OVER ()
FROM pick p JOIN books b ON b.id = p.book_id JOIN libraries l ON l.id = b.library_id LEFT JOIN users u ON u.id = p.sharer_id
WHERE p.rn = 1 AND `+w.sql()+` ORDER BY p.shared_at DESC, b.id LIMIT ?`,
		append(append(append([]any(nil), sc.Shared.Args...), w.args...), listeningLimit(limit))...)
	if err != nil || len(rows) == 0 {
		return out, err
	}
	out.Total = total
	ids := make([]string, len(rows))
	for i, x := range rows {
		ids[i] = x.id
	}
	cards, err := r.cards(ctx, ids)
	if err != nil {
		return SharedRow{}, err
	}
	byID := make(map[string]BookCard, len(cards))
	for _, c := range cards {
		byID[c.ID] = c
	}
	for _, x := range rows {
		if c, ok := byID[x.id]; ok {
			out.Books = append(out.Books, SharedBook{Book: c, SharedAt: x.at, SharerName: x.sharer})
		}
	}

	names, err := many(ctx, r.st.Reader(), func(s scanner) (string, error) {
		var n string
		err := s.Scan(&n)
		return n, err
	}, `WITH src(book_id, shared_at, sharer_id) AS (`+sc.Shared.SQL+`)
SELECT coalesce(nullif(u.display_name, ''), u.username) FROM src p
JOIN books b ON b.id = p.book_id JOIN libraries l ON l.id = b.library_id JOIN users u ON u.id = p.sharer_id
WHERE `+w.sql()+` GROUP BY u.id ORDER BY max(p.shared_at) DESC, u.id LIMIT ?`,
		append(append(append([]any(nil), sc.Shared.Args...), w.args...), maxSharers)...)
	if err != nil {
		return SharedRow{}, err
	}
	out.Sharers = names
	return out, nil
}
