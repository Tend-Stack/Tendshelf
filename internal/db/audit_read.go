package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
)

// AuditQueryer is what reading the audit log needs; a *sql.DB reader satisfies it.
type AuditQueryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

// AuditFilter narrows ListAudit. An empty field matches everything.
type AuditFilter struct {
	UserID   string   // the person who did it
	Prefixes []string // action name prefixes such as "auth." (any one matches)
	Offset   int
	Limit    int
}

// AuditRow is one stored audit entry. Detail is nil when it is empty or not an object.
type AuditRow struct {
	ID     string
	At     int64  // Unix milliseconds UTC
	UserID string // empty when no person did it
	Action string
	Target string
	Detail map[string]any
}

// ListAudit returns the entries that match f, newest first, and whether more follow. The filter runs in SQL.
func ListAudit(ctx context.Context, q AuditQueryer, f AuditFilter) (rows []AuditRow, more bool, err error) {
	var where []string
	var args []any
	if f.UserID != "" {
		where = append(where, "user_id = ?")
		args = append(args, f.UserID)
	}
	if len(f.Prefixes) > 0 {
		var ors []string
		for _, p := range f.Prefixes {
			// substr compares the prefix exactly, so '_' and '%' in a name are never wildcards.
			ors = append(ors, "substr(action, 1, ?) = ?")
			args = append(args, len([]rune(p)), p)
		}
		where = append(where, "("+strings.Join(ors, " OR ")+")")
	}
	query := `SELECT id, at, coalesce(user_id, ''), action, target, detail_json FROM audit_log`
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	query += " ORDER BY at DESC, id DESC LIMIT ? OFFSET ?"
	args = append(args, f.Limit+1, f.Offset)
	rs, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rs.Close()
	for rs.Next() {
		var r AuditRow
		var raw string
		if err := rs.Scan(&r.ID, &r.At, &r.UserID, &r.Action, &r.Target, &raw); err != nil {
			return nil, false, err
		}
		if json.Unmarshal([]byte(raw), &r.Detail) != nil {
			r.Detail = nil
		}
		rows = append(rows, r)
	}
	if err := rs.Err(); err != nil {
		return nil, false, err
	}
	if len(rows) > f.Limit {
		rows, more = rows[:f.Limit], true
	}
	return rows, more, nil
}
