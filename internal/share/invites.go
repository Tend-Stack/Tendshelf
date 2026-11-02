package share

import (
	"context"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

// WaitingInvites counts the invites that can still be used at nowMs: not revoked, not used up,
// not expired. Nothing writes invites yet (P3-3), so it reads 0 until then.
func WaitingInvites(ctx context.Context, st *db.Store, nowMs int64) (int, error) {
	var n int
	err := st.Reader().QueryRowContext(ctx, `SELECT count(*) FROM invites
		WHERE revoked_at IS NULL AND use_count < max_uses AND (expires_at IS NULL OR expires_at > ?)`, nowMs).Scan(&n)
	return n, err
}
