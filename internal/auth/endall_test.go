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

// TestEndAllForUserRevokesFeeds: a reset turns off the person's podcast feeds and nobody else's.
func TestEndAllForUserRevokesFeeds(t *testing.T) {
	e := newEnv(t).withOwner(t)
	now := e.clk.Now().UnixMilli()
	for _, q := range []string{
		`INSERT INTO users(id, username, role, created_at) VALUES ('us_other', 'Bo', 'member', 1)`,
		`INSERT INTO podcast_feeds(id, token_hash, user_id, book_id, created_at) VALUES ('f1', randomblob(32), '` + e.owner.ID + `', 'b1', 1)`,
		`INSERT INTO podcast_feeds(id, token_hash, user_id, series_id, created_at) VALUES ('f2', randomblob(32), '` + e.owner.ID + `', 's1', 1)`,
		`INSERT INTO podcast_feeds(id, token_hash, user_id, book_id, created_at) VALUES ('f3', randomblob(32), 'us_other', 'b1', 1)`,
	} {
		if _, err := e.st.Writer().Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	err := e.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		_, err := e.sess.EndAllForUser(context.Background(), tx, e.owner.ID, now)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if c := e.count(t, `SELECT count(*) FROM podcast_feeds WHERE user_id = ? AND revoked_at = ?`, e.owner.ID, now); c != 2 {
		t.Errorf("revoked feeds = %d, want 2", c)
	}
	if c := e.count(t, `SELECT count(*) FROM podcast_feeds WHERE user_id = 'us_other' AND revoked_at IS NULL`); c != 1 {
		t.Errorf("another person's feed was revoked")
	}
}
