package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
)

// Outcomes recorded in book_lookups: how the last online lookup of a book ended.
const (
	LookupMatched   = "matched"    // a confident match was applied
	LookupNeedsLook = "needs_look" // candidates, none safe to apply
	LookupNoMatch   = "no_match"
	LookupFailed    = "failed" // the sites had trouble; tried again next time
	LookupKept      = "kept"   // the book already had online or user values
	LookupNoQuery   = "no_query"
	LookupChosen    = "chosen"    // a person applied a match
	LookupDismissed = "dismissed" // a person said "leave it"
)

// Bulk modes of NextLookupBook.
const (
	LookupModeAll = "all"
	LookupModeNew = "new"
	// The two narrower scopes of "all": books with something still missing, and books never looked up.
	LookupModeMissing  = "missing"
	LookupModeUnlooked = "unlooked"
)

// LookupRow is one book on the "needs a look" list.
type LookupRow struct {
	BookID, Title, Author, Outcome string
	CheckedAt                      int64
}

// SetAutoLookup turns "look up details online for new books" on for a library at the given time
// (milliseconds), or off with 0. It is ErrNotFound for an unknown library.
func (r *Repo) SetAutoLookup(ctx context.Context, libraryID string, at int64) error {
	return r.Update(ctx, func(t *Tx) error { return t.SetAutoLookup(libraryID, at) })
}

// SetAutoLookup is Repo.SetAutoLookup inside a transaction.
func (t *Tx) SetAutoLookup(libraryID string, at int64) error {
	var arg any
	if at > 0 {
		arg = at
	}
	res, err := t.exec(`UPDATE libraries SET auto_lookup_at = ? WHERE id = ?`, arg, libraryID)
	if err != nil {
		return mapErr(err, "library")
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return fmt.Errorf("%w: library", ErrNotFound)
	}
	return nil
}

// PutBookLookup records how a book's online lookup ended, replacing any earlier record.
func (t *Tx) PutBookLookup(bookID, outcome, provider, jobID string, at int64) error {
	_, err := t.exec(`INSERT INTO book_lookups(book_id, outcome, provider, job_id, checked_at) VALUES (?,?,?,?,?)
  ON CONFLICT(book_id) DO UPDATE SET outcome = excluded.outcome, provider = excluded.provider,
    job_id = excluded.job_id, checked_at = excluded.checked_at`, bookID, outcome, provider, jobID, at)
	return mapErr(err, "book")
}

// lookupEligible is the part of the WHERE clause both modes share; args: library id, run start.
const lookupEligible = ` FROM books b JOIN libraries l ON l.id = b.library_id
  WHERE b.library_id = ? AND b.missing_since IS NULL AND b.refused_reason IS NULL
    AND EXISTS (SELECT 1 FROM book_sidecars s WHERE s.book_id = b.id)
    AND NOT EXISTS (SELECT 1 FROM book_lookups k WHERE k.book_id = b.id
      AND (k.checked_at >= ? OR k.outcome IN ('chosen','dismissed')))`

// lookupNotLately skips books with a settled outcome checked at or after the placeholder.
const lookupNotLately = ` AND NOT EXISTS (SELECT 1 FROM book_lookups k WHERE k.book_id = b.id
      AND k.outcome <> 'failed' AND k.checked_at >= ?)`

// lookupMode adds the rule of one mode and returns its extra arguments.
func lookupMode(mode string, skipBefore int64) (string, []any, error) {
	switch mode {
	case LookupModeAll:
		return lookupNotLately, []any{skipBefore}, nil
	case LookupModeMissing:
		// A book "misses details" when it has no description, no cover or no author, and nobody has
		// settled it: it holds no online or owner-typed value (a lookup would only mark it kept) and no
		// lookup of the last 30 days is recorded. Chosen and dismissed books are excluded by lookupEligible.
		return lookupNotLately + ` AND (b.description = '' OR b.cover_id IS NULL
      OR NOT EXISTS (SELECT 1 FROM book_people bp WHERE bp.book_id = b.id AND bp.role = 'author'))
    AND NOT EXISTS (SELECT 1 FROM metadata_values m WHERE m.book_id = b.id AND m.source IN ('online','user'))`, []any{skipBefore}, nil
	case LookupModeUnlooked:
		// Never looked up: no record at all, or only "failed" ones (the sites had trouble).
		return lookupNotLately + ` AND NOT EXISTS (SELECT 1 FROM book_lookups k WHERE k.book_id = b.id AND k.outcome <> 'failed')`,
			[]any{skipBefore}, nil
	case LookupModeNew:
		return ` AND l.auto_lookup_at IS NOT NULL AND b.added_at >= l.auto_lookup_at
    AND NOT EXISTS (SELECT 1 FROM book_lookups k WHERE k.book_id = b.id AND k.outcome <> 'failed')`, nil, nil
	}
	return "", nil, fmt.Errorf("%w: lookup mode %q", ErrInvalid, mode)
}

// NextLookupBook returns the next book a bulk lookup of the library should ask about, oldest added
// first, and how many such books remain (the returned one included). id is "" when none is left.
// A book is eligible when it is present, not refused, has had its details read, was not looked up
// during this run (checked at or after runStart) and was not chosen or dismissed by a person. Mode
// "all" also skips books with a settled outcome checked at or after skipBefore; mode "new" takes
// only books added since the library's auto_lookup_at that have no outcome but "failed".
func (r *Repo) NextLookupBook(ctx context.Context, libraryID, mode string, runStart, skipBefore int64) (string, int, error) {
	extra, eargs, err := lookupMode(mode, skipBefore)
	if err != nil {
		return "", 0, err
	}
	args := append([]any{libraryID, runStart}, eargs...)
	var n int
	if err := r.st.Reader().QueryRowContext(ctx, `SELECT count(*)`+lookupEligible+extra, args...).Scan(&n); err != nil {
		return "", 0, err
	}
	if n == 0 {
		return "", 0, nil
	}
	var id string
	if err := r.st.Reader().QueryRowContext(ctx, `SELECT b.id`+lookupEligible+extra+` ORDER BY b.added_at, b.id LIMIT 1`, args...).Scan(&id); err != nil {
		return "", 0, err
	}
	return id, n, nil
}

// CountLookupBooks says how many books a bulk lookup of the given mode would ask about right now
// (nothing counts as looked up during a run). It uses the very predicate NextLookupBook does, so the
// number on the page and the job's own total cannot disagree.
func (r *Repo) CountLookupBooks(ctx context.Context, libraryID, mode string, skipBefore int64) (int, error) {
	extra, eargs, err := lookupMode(mode, skipBefore)
	if err != nil {
		return 0, err
	}
	var n int
	err = r.st.Reader().QueryRowContext(ctx, `SELECT count(*)`+lookupEligible+extra,
		append([]any{libraryID, int64(math.MaxInt64)}, eargs...)...).Scan(&n)
	return n, err
}

// LookupCounts counts the books by outcome that the given job recorded.
func (r *Repo) LookupCounts(ctx context.Context, jobID string) (map[string]int, error) {
	rows, err := r.st.Reader().QueryContext(ctx, `SELECT outcome, count(*) FROM book_lookups WHERE job_id = ? GROUP BY outcome`, jobID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var o string
		var n int
		if err := rows.Scan(&o, &n); err != nil {
			return nil, err
		}
		out[o] = n
	}
	return out, rows.Err()
}

// NeedsLook lists the library's books whose last lookup ended as needs_look or no_match, by sort
// title, at most limit of them, and how many there are in all.
func (r *Repo) NeedsLook(ctx context.Context, libraryID string, limit int) ([]LookupRow, int, error) {
	const where = ` FROM book_lookups k JOIN books b ON b.id = k.book_id
  WHERE b.library_id = ? AND b.missing_since IS NULL AND k.outcome IN ('needs_look','no_match')`
	var total int
	if err := r.st.Reader().QueryRowContext(ctx, `SELECT count(*)`+where, libraryID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := many(ctx, r.st.Reader(), func(s scanner) (LookupRow, error) {
		var l LookupRow
		err := s.Scan(&l.BookID, &l.Title, &l.Author, &l.Outcome, &l.CheckedAt)
		return l, err
	}, `SELECT b.id, b.title, COALESCE((SELECT p.name FROM book_people bp JOIN people p ON p.id = bp.person_id
      WHERE bp.book_id = b.id AND bp.role = 'author' ORDER BY bp.idx LIMIT 1), ''), k.outcome, k.checked_at`+where+`
  ORDER BY `+titleKey+` COLLATE NOCASE, b.id LIMIT ?`, libraryID, limit)
	return rows, total, err
}

// AutoLookupDue says which library a book belongs to and whether that library has "look up details
// online for new books" on and the book was added since it was turned on and has no lookup record
// other than a failed one. An unknown book is ErrNotFound.
func (r *Repo) AutoLookupDue(ctx context.Context, bookID string) (string, bool, error) {
	var lib string
	var due bool
	err := r.st.Reader().QueryRowContext(ctx, `SELECT b.library_id,
    (l.auto_lookup_at IS NOT NULL AND b.added_at >= l.auto_lookup_at AND b.missing_since IS NULL AND b.refused_reason IS NULL
      AND NOT EXISTS (SELECT 1 FROM book_lookups k WHERE k.book_id = b.id AND k.outcome <> 'failed'))
  FROM books b JOIN libraries l ON l.id = b.library_id WHERE b.id = ?`, bookID).Scan(&lib, &due)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, fmt.Errorf("%w: book", ErrNotFound)
	}
	if err != nil {
		return "", false, err
	}
	return lib, due, nil
}

// SetWatch saves "Watch for new books" and the scheduled scan interval (minutes, 0 = never) of a
// library. It is ErrInvalid for an interval outside 0 to 10080 and ErrNotFound for an unknown library.
func (r *Repo) SetWatch(ctx context.Context, libraryID string, watch bool, intervalMin int) error {
	if intervalMin < 0 || intervalMin > 10080 {
		return invalid("scan interval must be 0 to 10080 minutes")
	}
	on := 0
	if watch {
		on = 1
	}
	return r.Update(ctx, func(t *Tx) error {
		res, err := t.exec(`UPDATE libraries SET watch = ?, scan_interval_min = ? WHERE id = ?`, on, intervalMin, libraryID)
		if err != nil {
			return mapErr(err, "library")
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: library", ErrNotFound)
		}
		return nil
	})
}
