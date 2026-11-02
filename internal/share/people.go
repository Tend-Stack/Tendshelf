package share

import (
	"context"
	"database/sql"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

// Person is one row of the Users page: an account with what it has been up to and what it
// may open. It carries no book title from listening history, only times and totals.
type Person struct {
	User         users.User
	LastActive   int64 // the later of last_seen_at and the last listening session; 0 when never
	LastListen   int64 // end of the last listening session; 0 when none
	MonthMs      int64 // milliseconds listened since the month start passed to People
	Devices      int   // live sessions plus device tokens that are not revoked
	Grants       int   // every grant, ended ones included
	ActiveGrants int   // grants still in force
	Books        int   // books the active grants cover (raw: includes refused or missing files)
	Everything   bool  // an active "all" grant
	FirstScope   GrantScope
	FirstName    string // name of the oldest active grant's target
}

// activeForRowSQL is true for a grant aliased share_g (or g) of the user aliased u that has
// not ended. It uses numbered parameter ?1 for "now".
const activeForRowSQL = `share_g.user_id = u.id AND (share_g.expires_at IS NULL OR share_g.expires_at > ?1)`

// People lists every account in one query: owner first, then by name. nowMs decides which grants
// are still in force; monthStartMs starts the "this month" total.
func (r *Grants) People(ctx context.Context, nowMs, monthStartMs int64) ([]Person, error) {
	q := `SELECT u.id, u.username, u.display_name, COALESCE(u.email, ''), u.role, u.disabled, u.created_at, COALESCE(u.last_seen_at, 0),
		coalesce((SELECT max(ls.ended_at) FROM listening_sessions ls WHERE ls.user_id = u.id), 0),
		coalesce((SELECT sum(ls.listened_ms) FROM listening_sessions ls WHERE ls.user_id = u.id AND ls.started_at >= ?2), 0),
		(SELECT count(*) FROM sessions se WHERE se.user_id = u.id AND se.expires_at >= ?1)
			+ (SELECT count(*) FROM devices d WHERE d.user_id = u.id AND d.revoked_at IS NULL),
		(SELECT count(*) FROM grants g WHERE g.user_id = u.id),
		(SELECT count(*) FROM grants share_g WHERE ` + activeForRowSQL + `),
		EXISTS(SELECT 1 FROM grants share_g WHERE ` + activeForRowSQL + ` AND share_g.scope = 'all'),
		(SELECT count(*) FROM books b WHERE EXISTS (SELECT 1 FROM grants share_g WHERE ` + activeForRowSQL + ` AND ` + coversSQL("share_g", "b") + `)),
		coalesce((SELECT share_g.scope FROM grants share_g WHERE ` + activeForRowSQL + ` ORDER BY share_g.created_at, share_g.id LIMIT 1), ''),
		coalesce((SELECT coalesce(l.name, s.name, bk.title, '') FROM grants share_g
			LEFT JOIN libraries l ON l.id = share_g.library_id
			LEFT JOIN shelves s ON s.id = share_g.shelf_id
			LEFT JOIN books bk ON bk.id = share_g.book_id
			WHERE ` + activeForRowSQL + ` ORDER BY share_g.created_at, share_g.id LIMIT 1), '')
		FROM users u
		ORDER BY u.role = 'owner' DESC, lower(coalesce(nullif(u.display_name, ''), u.username)), u.id`
	rows, err := r.st.Reader().QueryContext(ctx, q, nowMs, monthStartMs)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Person
	for rows.Next() {
		var p Person
		var role, scope string
		var disabled, every int
		err := rows.Scan(&p.User.ID, &p.User.Username, &p.User.DisplayName, &p.User.Email, &role, &disabled, &p.User.CreatedAt, &p.User.LastSeenAt,
			&p.LastListen, &p.MonthMs, &p.Devices, &p.Grants, &p.ActiveGrants, &every, &p.Books, &scope, &p.FirstName)
		if err != nil {
			return nil, err
		}
		p.User.Role, p.User.Disabled = users.Role(role), disabled != 0
		p.Everything, p.FirstScope = every != 0, GrantScope(scope)
		p.LastActive = max(p.User.LastSeenAt, p.LastListen)
		out = append(out, p)
	}
	return out, rows.Err()
}

// NamedGrant is a grant with the words the grant editor shows for it.
type NamedGrant struct {
	Grant
	Name      string // library, shelf or book name; empty for "all"
	OwnerName string // owner of the shelf, for a shelf grant
	Books     int    // books the grant covers
}

// ForUserNamed lists a person's grants, oldest first, with target names and covered book counts.
func (r *Grants) ForUserNamed(ctx context.Context, userID string) ([]NamedGrant, error) {
	rows, err := r.st.Reader().QueryContext(ctx, `SELECT `+grantColumns+`,
		coalesce((SELECT name FROM libraries WHERE id = g.library_id), (SELECT name FROM shelves WHERE id = g.shelf_id),
			(SELECT title FROM books WHERE id = g.book_id), ''),
		coalesce((SELECT coalesce(nullif(o.display_name, ''), o.username) FROM shelves s JOIN users o ON o.id = s.owner_id WHERE s.id = g.shelf_id), ''),
		(SELECT count(*) FROM books b WHERE `+coversSQL("g", "b")+`)
		FROM grants g WHERE g.user_id = ? ORDER BY g.created_at, g.id`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []NamedGrant
	for rows.Next() {
		var n NamedGrant
		var scope string
		var dl, rd int
		err := rows.Scan(&n.ID, &n.UserID, &scope, &n.TargetID, &dl, &rd, &n.ExpiresAt, &n.CreatedBy, &n.CreatedAt, &n.UpdatedAt,
			&n.Name, &n.OwnerName, &n.Books)
		if err != nil {
			return nil, err
		}
		n.Scope, n.CanDownload, n.CanRead = GrantScope(scope), dl == 1, rd == 1
		out = append(out, n)
	}
	return out, rows.Err()
}

// ShelfTarget is a shelf that can be shared: one of an enabled owner or admin.
type ShelfTarget struct {
	ID, Name, OwnerName string
	Books               int
}

// ShelfTargets lists the shelves a grant may point at, by owner then name.
func (r *Grants) ShelfTargets(ctx context.Context) ([]ShelfTarget, error) {
	rows, err := r.st.Reader().QueryContext(ctx, `SELECT s.id, s.name, coalesce(nullif(o.display_name, ''), o.username),
		(SELECT count(*) FROM shelf_items si WHERE si.shelf_id = s.id)
		FROM shelves s JOIN users o ON o.id = s.owner_id
		WHERE o.role IN ('owner','admin') AND o.disabled = 0
		ORDER BY lower(coalesce(nullif(o.display_name, ''), o.username)), lower(s.name), s.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ShelfTarget
	for rows.Next() {
		var t ShelfTarget
		if err := rows.Scan(&t.ID, &t.Name, &t.OwnerName, &t.Books); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// RevokeAll removes every grant of userID inside tx, oldest first, each with its own audit row,
// and returns how many it removed.
func (r *Grants) RevokeAll(ctx context.Context, tx *sql.Tx, by users.User, userID string) (int, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id FROM grants WHERE user_id = ? ORDER BY created_at, id`, userID)
	if err != nil {
		return 0, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := r.Revoke(ctx, tx, by, id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}
