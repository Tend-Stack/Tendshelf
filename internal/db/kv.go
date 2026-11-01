package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
)

// Execer is what a write needs: a *sql.Tx, a *sql.DB or a *sql.Conn.
type Execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

// RowQueryer is what a single-row read needs.
type RowQueryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// GetSetting reads the server setting key into dst (JSON). It returns false
// and leaves dst alone when the key is absent.
func GetSetting(ctx context.Context, q RowQueryer, key string, dst any) (bool, error) {
	var raw string
	err := q.QueryRowContext(ctx, `SELECT value_json FROM settings WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := json.Unmarshal([]byte(raw), dst); err != nil {
		return false, err
	}
	return true, nil
}

// PutSetting stores v (as JSON) under the server setting key, replacing any
// earlier value.
func PutSetting(ctx context.Context, ex Execer, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = ex.ExecContext(ctx, `INSERT INTO settings (key, value_json) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value_json = excluded.value_json`, key, string(raw))
	return err
}

// AuditEntry is one row of the audit log. Detail must never carry a token, a
// password or a setup code.
type AuditEntry struct {
	At     int64 // Unix milliseconds UTC
	UserID string
	Action string
	Target string
	Detail any
}

// Audit appends e to audit_log. An empty UserID is stored as NULL and a nil
// Detail as {}.
func Audit(ctx context.Context, ex Execer, e AuditEntry) error {
	detail := "{}"
	if e.Detail != nil {
		raw, err := json.Marshal(e.Detail)
		if err != nil {
			return err
		}
		detail = string(raw)
	}
	var uid any
	if e.UserID != "" {
		uid = e.UserID
	}
	_, err := ex.ExecContext(ctx, `INSERT INTO audit_log (id, at, user_id, action, target, detail_json)
		VALUES (?, ?, ?, ?, ?, ?)`, NewID("aud"), e.At, uid, e.Action, e.Target, detail)
	return err
}
