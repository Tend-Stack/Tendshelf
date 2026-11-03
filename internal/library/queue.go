package library

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Up next: a person's ordered list of books to play after the current one.
//
// Rules every function here keeps:
//   - The queue is per account: rows are (user_id, book_id), one per book, and a call only ever
//     reads or changes the given person's rows.
//   - Reads join books and libraries through the caller's Scope and Listable, so a book the
//     person cannot open, or that is refused or missing, never shows, never counts and is never
//     popped. Its row stays (it comes back if access returns) until the book or person is deleted.
//   - Order is position, then book id. Play next takes min(position)-1 and Add to queue takes
//     max(position)+1, so positions are signed and nothing is ever renumbered.
//   - A person holds at most QueueMax rows, hidden ones included.
//
// Whether the person may play a book is the caller's check (share.ListenBook) before QueueAdd.

// QueueMax is the most books one person can hold in Up next.
const QueueMax = 100

// ErrQueueFull is returned when adding a new book to a queue that already holds QueueMax rows.
var ErrQueueFull = errors.New("library: up next is full")

// QueueEntry is one visible queued book, in play order.
type QueueEntry struct {
	Book    BookCard
	AddedAt int64
}

// Queue lists the person's queued books the scope may open, in play order.
func (r *Repo) Queue(ctx context.Context, sc Scope, userID string) ([]QueueEntry, error) {
	w := visible(sc, true)
	type row struct {
		id string
		at int64
	}
	rows, err := many(ctx, r.st.Reader(), func(s scanner) (row, error) {
		var x row
		return x, s.Scan(&x.id, &x.at)
	}, `SELECT q.book_id, q.added_at FROM queue_items q JOIN books b ON b.id = q.book_id
  JOIN libraries l ON l.id = b.library_id WHERE q.user_id = ? AND `+w.sql()+` ORDER BY q.position, q.book_id`,
		append([]any{userID}, w.args...)...)
	if err != nil {
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
	at := make(map[string]int64, len(rows))
	for _, x := range rows {
		at[x.id] = x.at
	}
	out := make([]QueueEntry, 0, len(cards))
	for _, c := range cards {
		out = append(out, QueueEntry{Book: c, AddedAt: at[c.ID]})
	}
	return out, nil
}

// QueueCount counts the person's queued books the scope may open (the same filter as Queue).
func (r *Repo) QueueCount(ctx context.Context, sc Scope, userID string) (int, error) {
	w := visible(sc, true)
	var n int
	err := r.st.Reader().QueryRowContext(ctx, `SELECT count(*) FROM queue_items q JOIN books b ON b.id = q.book_id
  JOIN libraries l ON l.id = b.library_id WHERE q.user_id = ? AND `+w.sql(), append([]any{userID}, w.args...)...).Scan(&n)
	return n, err
}

// QueueAdd puts a book at the front (front) or the end of the person's queue. A book already
// queued stays where it is, unless front moves it to the top; either way added is false. A new book
// past QueueMax rows is ErrQueueFull; an unknown book is ErrNotFound. It does not check access.
func (r *Repo) QueueAdd(ctx context.Context, userID, bookID string, front bool) (added bool, err error) {
	now := r.nowMs()
	err = r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		var n int
		var lo, hi int64
		if err := tx.QueryRowContext(ctx, `SELECT count(*), COALESCE(min(position), 0), COALESCE(max(position), 0)
  FROM queue_items WHERE user_id = ?`, userID).Scan(&n, &lo, &hi); err != nil {
			return err
		}
		var have int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM queue_items WHERE user_id = ? AND book_id = ?`, userID, bookID).Scan(&have)
		switch {
		case err == nil:
			if front {
				_, err = tx.ExecContext(ctx, `UPDATE queue_items SET position = ? WHERE user_id = ? AND book_id = ?`, lo-1, userID, bookID)
			}
			return err
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		if n >= QueueMax {
			return ErrQueueFull
		}
		pos := hi + 1
		if front {
			pos = lo - 1
		}
		if n == 0 {
			pos = 0
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO queue_items(user_id, book_id, position, added_at) VALUES (?,?,?,?)`,
			userID, bookID, pos, now); err != nil {
			return mapErr(err, "queue book")
		}
		added = true
		return nil
	})
	if err != nil {
		return false, err
	}
	return added, nil
}

// QueueMove moves a visible queued book one place up (delta -1) or down (+1) by swapping places
// with the nearest neighbour the scope may open, so a hidden book is stepped over. At either end it
// does nothing. Any other delta is ErrInvalid; a book not queued or not visible is ErrNotFound.
func (r *Repo) QueueMove(ctx context.Context, sc Scope, userID, bookID string, delta int) error {
	if delta != -1 && delta != 1 {
		return invalid("a book moves one place at a time")
	}
	return r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		w := visible(sc, true)
		from := ` FROM queue_items q JOIN books b ON b.id = q.book_id JOIN libraries l ON l.id = b.library_id
  WHERE q.user_id = ? AND ` + w.sql()
		var pos int64
		err := tx.QueryRowContext(ctx, `SELECT q.position`+from+` AND q.book_id = ?`,
			append(append([]any{userID}, w.args...), bookID)...).Scan(&pos)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: queued book", ErrNotFound)
		}
		if err != nil {
			return err
		}
		cond := ` AND (q.position < ? OR (q.position = ? AND q.book_id < ?)) ORDER BY q.position DESC, q.book_id DESC LIMIT 1`
		if delta > 0 {
			cond = ` AND (q.position > ? OR (q.position = ? AND q.book_id > ?)) ORDER BY q.position, q.book_id LIMIT 1`
		}
		var nb string
		var npos int64
		err = tx.QueryRowContext(ctx, `SELECT q.book_id, q.position`+from+cond,
			append(append([]any{userID}, w.args...), pos, pos, bookID)...).Scan(&nb, &npos)
		if errors.Is(err, sql.ErrNoRows) {
			return nil // already at that end
		}
		if err != nil {
			return err
		}
		for _, u := range []struct {
			pos  int64
			book string
		}{{npos, bookID}, {pos, nb}} {
			if _, err := tx.ExecContext(ctx, `UPDATE queue_items SET position = ? WHERE user_id = ? AND book_id = ?`, u.pos, userID, u.book); err != nil {
				return err
			}
		}
		return nil
	})
}

// QueueRemove takes a book off the person's queue; a book that is not queued is ErrNotFound.
func (r *Repo) QueueRemove(ctx context.Context, userID, bookID string) error {
	return r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM queue_items WHERE user_id = ? AND book_id = ?`, userID, bookID)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return fmt.Errorf("%w: queued book", ErrNotFound)
		}
		return nil
	})
}

// QueuePop removes and returns the first queued book the scope may open. When ended is set, that
// book's row is removed first (it finished playing). Hidden, refused and missing rows are skipped
// and kept. With nothing to play it is ErrNotFound (an ended row is still removed).
func (r *Repo) QueuePop(ctx context.Context, sc Scope, userID, ended string) (string, error) {
	var id string
	err := r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if ended != "" {
			if _, err := tx.ExecContext(ctx, `DELETE FROM queue_items WHERE user_id = ? AND book_id = ?`, userID, ended); err != nil {
				return err
			}
		}
		w := visible(sc, true)
		err := tx.QueryRowContext(ctx, `SELECT q.book_id FROM queue_items q JOIN books b ON b.id = q.book_id
  JOIN libraries l ON l.id = b.library_id WHERE q.user_id = ? AND `+w.sql()+` ORDER BY q.position, q.book_id LIMIT 1`,
			append([]any{userID}, w.args...)...).Scan(&id)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `DELETE FROM queue_items WHERE user_id = ? AND book_id = ?`, userID, id)
		return err
	})
	if err != nil {
		return "", err
	}
	if id == "" {
		return "", fmt.Errorf("%w: queued book", ErrNotFound)
	}
	return id, nil
}
