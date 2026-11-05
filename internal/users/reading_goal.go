package users

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

const (
	keyReadingGoal = "reading.v1"
	MaxReadingGoal = 365 // the most books a yearly goal can ask for
)

// ReadingGoal is a person's yearly goal of books finished (G2). 0 means no goal. It is kept in
// user_settings as reading.v1, for that person only.
type ReadingGoal struct {
	Books int `json:"goalBooks"`
}

// Validate keeps the goal between 0 and 365.
func (g ReadingGoal) Validate() error {
	if g.Books < 0 || g.Books > MaxReadingGoal {
		return ErrInvalidSetting
	}
	return nil
}

// LoadReadingGoal returns the person's goal; a missing, broken or out-of-range row means no goal.
func (r *Repo) LoadReadingGoal(ctx context.Context, userID string) (ReadingGoal, error) {
	var raw string
	err := r.st.Reader().QueryRowContext(ctx, `SELECT value_json FROM user_settings WHERE user_id = ? AND key = ?`,
		userID, keyReadingGoal).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return ReadingGoal{}, nil
	}
	if err != nil {
		return ReadingGoal{}, err
	}
	var g ReadingGoal
	if json.Unmarshal([]byte(raw), &g) != nil || g.Validate() != nil {
		return ReadingGoal{}, nil
	}
	return g, nil
}

// SaveReadingGoal validates g and stores it as the person's reading.v1 row.
func (r *Repo) SaveReadingGoal(ctx context.Context, userID string, g ReadingGoal) error {
	if err := g.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return err
	}
	return r.st.WriteTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO user_settings (user_id, key, value_json, updated_at) VALUES (?, ?, ?, ?)
			ON CONFLICT(user_id, key) DO UPDATE SET value_json = excluded.value_json, updated_at = excluded.updated_at`,
			userID, keyReadingGoal, string(raw), r.nowMs())
		return err
	})
}
