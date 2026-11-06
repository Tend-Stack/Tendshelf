package users

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Per-book speed (table book_settings, migration 0024). A person's own speed for one book; a book
// with no row plays at the person's usual speed (Settings.Speed). Rows are (user_id, book_id), cascade
// away with either, and every call touches only the given person's rows. Whether the person may open
// the book is the caller's check.

// ErrNoBookSpeed means the person has no speed of their own for the book.
var ErrNoBookSpeed = errors.New("users: no speed for this book")

// BookSpeed returns the person's speed for the book, or ErrNoBookSpeed.
func (r *Repo) BookSpeed(ctx context.Context, userID, bookID string) (float64, error) {
	var v float64
	err := r.st.Reader().QueryRowContext(ctx, `SELECT speed FROM book_settings WHERE user_id = ? AND book_id = ?`,
		userID, bookID).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNoBookSpeed
	}
	return v, err
}

// SetBookSpeed remembers speed for the book. A speed outside the allowed steps is ErrInvalidSetting.
func (r *Repo) SetBookSpeed(ctx context.Context, userID, bookID string, speed float64) error {
	if !validSpeed(speed) {
		return ErrInvalidSetting
	}
	return r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO book_settings (user_id, book_id, speed, updated_at) VALUES (?, ?, ?, ?)
			ON CONFLICT(user_id, book_id) DO UPDATE SET speed = excluded.speed, updated_at = excluded.updated_at`,
			userID, bookID, speed, r.nowMs())
		return err
	})
}

// UseAsUsualSpeed makes speed the person's usual speed and drops the book's own, in one transaction,
// so the book now follows the usual speed like every book without a row.
func (r *Repo) UseAsUsualSpeed(ctx context.Context, userID, bookID string, speed float64) error {
	if !validSpeed(speed) {
		return ErrInvalidSetting
	}
	raw, err := json.Marshal(speed)
	if err != nil {
		return err
	}
	return r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO user_settings (user_id, key, value_json, updated_at) VALUES (?, ?, ?, ?)
			ON CONFLICT(user_id, key) DO UPDATE SET value_json = excluded.value_json, updated_at = excluded.updated_at`,
			userID, keySpeed, string(raw), r.nowMs()); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `DELETE FROM book_settings WHERE user_id = ? AND book_id = ?`, userID, bookID)
		return err
	})
}
