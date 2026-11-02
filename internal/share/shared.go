package share

import "github.com/Tend-Stack/tendshelf/internal/users"

// SharedBooks returns a SELECT of (book_id, shared_at, sharer_id) for the
// books shared with u: active book grants, active shelf grants on a shelf of
// an enabled owner or admin, and, for owner, admin and member viewers only,
// the shelves another enabled owner or admin marked shared. Rows are not
// deduplicated (a book shared twice appears twice) and the args are
// positional. Library and "all" grants are whole libraries, not shares, and
// are absent. It lists sources only: callers still apply the visibility filter.
func (c *Checker) SharedBooks(u users.User) Filter {
	empty := Filter{SQL: "SELECT NULL AS book_id, NULL AS shared_at, NULL AS sharer_id WHERE 0"}
	if u.Disabled {
		return empty
	}
	switch u.Role {
	case users.RoleOwner, users.RoleAdmin, users.RoleMember, users.RoleGuest:
	default:
		return empty
	}
	now := c.now().UnixMilli()
	sql := `SELECT share_g.book_id, share_g.created_at, share_g.created_by FROM grants share_g
WHERE ` + activeSQL + ` AND share_g.scope = 'book'
UNION ALL SELECT share_si.book_id, max(share_g.created_at, share_si.added_at), share_g.created_by FROM grants share_g
JOIN shelf_items share_si ON share_si.shelf_id = share_g.shelf_id
JOIN shelves share_s ON share_s.id = share_g.shelf_id
JOIN users share_o ON share_o.id = share_s.owner_id
WHERE ` + activeSQL + ` AND share_g.scope = 'shelf' AND ` + sharerSQL("share_o")
	args := []any{u.ID, now, u.ID, now}
	if u.Role != users.RoleGuest {
		sql += `
UNION ALL SELECT share_si.book_id, share_si.added_at, share_s.owner_id FROM shelves share_s
JOIN shelf_items share_si ON share_si.shelf_id = share_s.id
JOIN users share_o ON share_o.id = share_s.owner_id
WHERE share_s.shared = 1 AND share_s.owner_id <> ? AND ` + sharerSQL("share_o")
		args = append(args, u.ID)
	}
	return Filter{SQL: sql, Args: args}
}
