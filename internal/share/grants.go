package share

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// GrantScope says how much of the library a grant opens.
type GrantScope string

// The scopes, as stored in grants.scope.
const (
	GrantAll     GrantScope = "all"     // everything on the server
	GrantLibrary GrantScope = "library" // one library
	GrantShelf   GrantScope = "shelf"   // the books on one shelf of an owner or admin
	GrantBook    GrantScope = "book"    // one book
)

// Errors the callers match on.
var (
	ErrGrantNotFound = errors.New("share: grant or grantee not found")
	ErrGrantScope    = errors.New("share: unknown scope, or the target does not fit it")
	ErrGrantRole     = errors.New("share: only a member or a guest takes a grant")
	ErrGrantTarget   = errors.New("share: the target does not exist or cannot be shared")
	ErrGrantExpiry   = errors.New("share: the end of a grant must be in the future")
)

// Grant is what one person may open. Members and guests see only what a grant
// covers; the Checker reads the same rows.
type Grant struct {
	ID          string
	UserID      string
	Scope       GrantScope
	TargetID    string // library, shelf or book id; empty for GrantAll
	CanDownload bool
	CanRead     bool
	ExpiresAt   int64 // Unix milliseconds UTC; 0 means never
	CreatedBy   string
	CreatedAt   int64
	UpdatedAt   int64
}

// Active reports whether the grant is still in force at nowMs. A grant that
// ends exactly now is over, as in the SQL the filters use.
func (g Grant) Active(nowMs int64) bool { return g.ExpiresAt == 0 || g.ExpiresAt > nowMs }

// GrantSpec is what Put needs to write one grant.
type GrantSpec struct {
	UserID      string
	Scope       GrantScope
	TargetID    string
	CanDownload bool
	CanRead     bool
	ExpiresAt   int64 // 0 means never
}

// Grants reads and writes the grants table. It does not authorize its caller:
// handlers check Can(ManageUsers, Target{Kind: TargetUser, ...}) first.
type Grants struct {
	st  *db.Store
	now func() time.Time
}

// NewGrants returns the grants repository on st.
func NewGrants(st *db.Store) *Grants { return &Grants{st: st, now: time.Now} }

const grantColumns = `id, user_id, scope, coalesce(library_id, shelf_id, book_id, ''), can_download, can_read,
	coalesce(expires_at, 0), coalesce(created_by, ''), created_at, updated_at`

type rowScanner interface{ Scan(dest ...any) error }

func scanGrant(r rowScanner) (Grant, error) {
	var g Grant
	var scope string
	var dl, rd int
	err := r.Scan(&g.ID, &g.UserID, &scope, &g.TargetID, &dl, &rd, &g.ExpiresAt, &g.CreatedBy, &g.CreatedAt, &g.UpdatedAt)
	g.Scope, g.CanDownload, g.CanRead = GrantScope(scope), dl == 1, rd == 1
	return g, err
}

// Put creates the grant for (user, scope, target) or updates it in place,
// inside tx, and writes an audit row. It keeps the id and created_at of an
// existing grant.
func (r *Grants) Put(ctx context.Context, tx *sql.Tx, by users.User, s GrantSpec) (Grant, error) {
	now := r.now().UnixMilli()
	if err := r.validate(ctx, tx, s, now); err != nil {
		return Grant{}, err
	}
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM grants WHERE user_id = ? AND scope = ?
		AND coalesce(library_id, shelf_id, book_id, '') = ?`, s.UserID, string(s.Scope), s.TargetID).Scan(&id)
	var exp any
	if s.ExpiresAt != 0 {
		exp = s.ExpiresAt
	}
	switch {
	case err == nil:
		_, err = tx.ExecContext(ctx, `UPDATE grants SET can_download = ?, can_read = ?, expires_at = ?, updated_at = ? WHERE id = ?`,
			b2i(s.CanDownload), b2i(s.CanRead), exp, now, id)
	case errors.Is(err, sql.ErrNoRows):
		id = db.NewID("gr")
		var lib, shelf, book any
		switch s.Scope {
		case GrantLibrary:
			lib = s.TargetID
		case GrantShelf:
			shelf = s.TargetID
		case GrantBook:
			book = s.TargetID
		}
		var creator any
		if by.ID != "" {
			creator = by.ID
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO grants(id, user_id, scope, library_id, shelf_id, book_id, can_download, can_read,
			expires_at, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			id, s.UserID, string(s.Scope), lib, shelf, book, b2i(s.CanDownload), b2i(s.CanRead), exp, creator, now, now)
	}
	if err != nil {
		return Grant{}, err
	}
	if err := db.Audit(ctx, tx, db.AuditEntry{At: now, UserID: by.ID, Action: "grant.put", Target: "grant:" + id, Detail: grantDetail(s)}); err != nil {
		return Grant{}, err
	}
	return scanGrant(tx.QueryRowContext(ctx, `SELECT `+grantColumns+` FROM grants WHERE id = ?`, id))
}

// validate checks a spec against the scope rules and the rows it points at.
func (r *Grants) validate(ctx context.Context, tx *sql.Tx, s GrantSpec, now int64) error {
	switch s.Scope {
	case GrantAll:
		if s.TargetID != "" {
			return ErrGrantScope
		}
	case GrantLibrary, GrantShelf, GrantBook:
		if s.TargetID == "" {
			return ErrGrantScope
		}
	default:
		return ErrGrantScope
	}
	if s.ExpiresAt != 0 && s.ExpiresAt <= now {
		return ErrGrantExpiry
	}
	var role string
	if err := tx.QueryRowContext(ctx, `SELECT role FROM users WHERE id = ?`, s.UserID).Scan(&role); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrGrantNotFound
		}
		return err
	}
	if role != string(users.RoleMember) && role != string(users.RoleGuest) {
		return ErrGrantRole
	}
	var q string
	switch s.Scope {
	case GrantLibrary:
		q = `SELECT 1 FROM libraries WHERE id = ?`
	case GrantBook:
		q = `SELECT 1 FROM books WHERE id = ?`
	case GrantShelf:
		q = `SELECT 1 FROM shelves s JOIN users o ON o.id = s.owner_id WHERE s.id = ? AND o.role IN ('owner','admin')`
	default:
		return nil
	}
	var one int
	if err := tx.QueryRowContext(ctx, q, s.TargetID).Scan(&one); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrGrantTarget
		}
		return err
	}
	return nil
}

func grantDetail(s GrantSpec) map[string]any {
	return map[string]any{"user": s.UserID, "scope": s.Scope, "target": s.TargetID,
		"can_download": s.CanDownload, "can_read": s.CanRead, "expires_at": s.ExpiresAt}
}

// Revoke deletes the grant inside tx and writes an audit row; the audit log
// keeps the history. It returns ErrGrantNotFound for an unknown id.
func (r *Grants) Revoke(ctx context.Context, tx *sql.Tx, by users.User, id string) error {
	g, err := scanGrant(tx.QueryRowContext(ctx, `SELECT `+grantColumns+` FROM grants WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return ErrGrantNotFound
	}
	if err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM grants WHERE id = ?`, id); err != nil {
		return err
	}
	return db.Audit(ctx, tx, db.AuditEntry{At: r.now().UnixMilli(), UserID: by.ID, Action: "grant.revoke", Target: "grant:" + id,
		Detail: grantDetail(GrantSpec{UserID: g.UserID, Scope: g.Scope, TargetID: g.TargetID, CanDownload: g.CanDownload, CanRead: g.CanRead, ExpiresAt: g.ExpiresAt})})
}

// Get returns one grant, ErrGrantNotFound when there is none.
func (r *Grants) Get(ctx context.Context, id string) (Grant, error) {
	g, err := scanGrant(r.st.Reader().QueryRowContext(ctx, `SELECT `+grantColumns+` FROM grants WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return Grant{}, ErrGrantNotFound
	}
	return g, err
}

// ForUser lists a person's grants, oldest first, expired ones included.
func (r *Grants) ForUser(ctx context.Context, userID string) ([]Grant, error) {
	rows, err := r.st.Reader().QueryContext(ctx, `SELECT `+grantColumns+` FROM grants WHERE user_id = ? ORDER BY created_at, id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Grant
	for rows.Next() {
		g, err := scanGrant(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}
