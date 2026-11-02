package auth

import (
	"context"
	"database/sql"
	"testing"
)

func TestEndAllForUser(t *testing.T) {
	e := newEnv(t).withOwner(t)
	e.login(t)
	e.login(t)
	now := e.clk.Now().UnixMilli()
	for _, q := range []string{
		`INSERT INTO devices(id, user_id, name, kind, token_hash, created_at) VALUES ('d1', '` + e.owner.ID + `', 'Phone', 'mobile', zeroblob(32), 1)`,
		`INSERT INTO devices(id, user_id, name, kind, token_hash, created_at) VALUES ('d2', '` + e.owner.ID + `', 'Web', 'web', NULL, 1)`,
	} {
		if _, err := e.st.Writer().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	var n int64
	err := e.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		n, err = e.sess.EndAllForUser(context.Background(), tx, e.owner.ID, now)
		return err
	})
	if err != nil || n < 2 {
		t.Fatalf("deleted %d sessions, err %v", n, err)
	}
	if c := e.count(t, `SELECT count(*) FROM sessions WHERE user_id = ?`, e.owner.ID); c != 0 {
		t.Fatalf("%d sessions left", c)
	}
	if c := e.count(t, `SELECT count(*) FROM devices WHERE revoked_at = ? AND token_hash IS NULL`, now); c != 2 {
		t.Fatalf("revoked devices = %d, want 2", c)
	}
}
