package share

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

type feedLab struct {
	*fixture
	r   *Feeds
	now time.Time
}

func newFeedLab(t *testing.T) *feedLab {
	t.Helper()
	l := &feedLab{fixture: openFixture(t)}
	l.now = l.t0
	l.r = NewFeeds(l.st, func() time.Time { return l.now })
	return l
}

func (l *feedLab) create(user, name, book, series string) (tok string, f Feed, err error) {
	err = l.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var e error
		tok, f, e = l.r.Create(context.Background(), tx, user, name, book, series)
		return e
	})
	return
}

func (l *feedLab) revoke(user, id string) (f Feed, err error) {
	err = l.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var e error
		f, e = l.r.Revoke(context.Background(), tx, user, id)
		return e
	})
	return
}

func TestFeedsCreateLookup(t *testing.T) {
	l := newFeedLab(t)
	ctx := context.Background()
	tok, f, err := l.create("u_none", "  Pocket Casts  ", "bk_a1", "")
	if err != nil || tok == "" || f.Name != "Pocket Casts" || f.Target() != "book:bk_a1" {
		t.Fatalf("create = %q %+v %v", tok, f, err)
	}
	// Only the hash is stored: the token appears nowhere in the row.
	var hash []byte
	if err := l.st.Reader().QueryRow(`SELECT token_hash FROM podcast_feeds WHERE id = ?`, f.ID).Scan(&hash); err != nil || len(hash) != 32 {
		t.Fatalf("hash = %d bytes, %v", len(hash), err)
	}
	if bytes.Contains(hash, []byte(tok)) {
		t.Error("token stored")
	}
	var raw int
	if err := l.st.Reader().QueryRow(`SELECT count(*) FROM podcast_feeds WHERE name = ? OR id = ? OR book_id = ?`, tok, tok, tok).Scan(&raw); err != nil || raw != 0 {
		t.Errorf("a column holds the token: %d, %v", raw, err)
	}
	got, reason, err := l.r.Lookup(ctx, tok)
	if err != nil || reason != "" || got.ID != f.ID || got.UserID != "u_none" || got.BookID != "bk_a1" {
		t.Fatalf("lookup = %+v %q %v", got, reason, err)
	}
	if _, reason, err := l.r.Lookup(ctx, "short"); !errors.Is(err, ErrFeedUnknown) || reason != FeedMalformed {
		t.Errorf("malformed = %q %v", reason, err)
	}
	other, _, _ := l.create("u_none", "", "bk_a1", "")
	if _, reason, err := l.r.Lookup(ctx, other[:len(other)-1]+"A"); !errors.Is(err, ErrFeedUnknown) || (reason != FeedUnknown && other[len(other)-1] != 'A') {
		t.Errorf("unknown = %q %v", reason, err)
	}
	for _, c := range [][2]string{{"", ""}, {"bk_a1", "s1"}} {
		if _, _, err := l.create("u_none", "", c[0], c[1]); !errors.Is(err, ErrFeedUnknown) {
			t.Errorf("targets %v: %v", c, err)
		}
	}
	long, fl, err := l.create("u_none", strings.Repeat("é", 100), "", "s1")
	if err != nil || long == "" || len([]rune(fl.Name)) != 64 {
		t.Errorf("long name = %d, %v", len([]rune(fl.Name)), err)
	}
}

func TestFeedsRevokeOwnLive(t *testing.T) {
	l := newFeedLab(t)
	tok, f, _ := l.create("u_none", "", "bk_a1", "")
	if _, err := l.revoke("u_own", f.ID); !errors.Is(err, ErrFeedUnknown) {
		t.Errorf("foreign revoke = %v", err)
	}
	if _, _, err := l.r.Lookup(context.Background(), tok); err != nil {
		t.Errorf("foreign revoke ended the feed: %v", err)
	}
	if _, err := l.revoke("u_none", f.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := l.revoke("u_none", f.ID); !errors.Is(err, ErrFeedUnknown) {
		t.Errorf("second revoke = %v", err)
	}
	if _, reason, err := l.r.Lookup(context.Background(), tok); !errors.Is(err, ErrFeedEnded) || reason != FeedEnded {
		t.Errorf("revoked lookup = %q %v", reason, err)
	}
}

func TestFeedsOwn(t *testing.T) {
	l := newFeedLab(t)
	ctx := context.Background()
	l.exec(t, `INSERT INTO series(id, name, created_at) VALUES ('s1', 'Saga', 1)`)
	l.exec(t, `UPDATE books SET title = 'First' WHERE id = 'bk_a1'`)
	_, a, _ := l.create("u_none", "", "bk_a1", "")
	l.now = l.now.Add(time.Minute)
	_, b, _ := l.create("u_none", "", "", "s1")
	l.now = l.now.Add(time.Minute)
	_, c, _ := l.create("u_none", "", "gone", "")
	_, dead, _ := l.create("u_none", "", "bk_a1", "")
	l.revoke("u_none", dead.ID)
	l.create("u_own", "", "bk_a1", "")
	rows, err := l.r.Own(ctx, "u_none")
	if err != nil || len(rows) != 3 {
		t.Fatalf("own = %+v %v", rows, err)
	}
	if rows[0].ID != c.ID || rows[1].ID != b.ID || rows[2].ID != a.ID {
		t.Errorf("order = %s %s %s", rows[0].ID, rows[1].ID, rows[2].ID)
	}
	if rows[0].TargetName != "" || rows[1].TargetName != "Saga" || rows[2].TargetName != "First" {
		t.Errorf("target names = %q %q %q", rows[0].TargetName, rows[1].TargetName, rows[2].TargetName)
	}
}

func TestFeedsTouchOnceAnHour(t *testing.T) {
	l := newFeedLab(t)
	ctx := context.Background()
	tok, f, _ := l.create("u_none", "", "bk_a1", "")
	used := func() int64 {
		var n sql.NullInt64
		l.st.Reader().QueryRow(`SELECT last_used_at FROM podcast_feeds WHERE id = ?`, f.ID).Scan(&n)
		return n.Int64
	}
	step := func(d time.Duration) int64 {
		l.now = l.now.Add(d)
		got, _, _ := l.r.Lookup(ctx, tok)
		l.r.Touch(ctx, got)
		return used()
	}
	first := step(0)
	if first != l.now.UnixMilli() {
		t.Fatalf("first touch = %d", first)
	}
	if got := step(30 * time.Minute); got != first {
		t.Errorf("touched again within the hour: %d", got)
	}
	if got := step(31 * time.Minute); got == first {
		t.Error("not touched after the hour")
	}
}

func TestFeedsLimitAndPrune(t *testing.T) {
	l := newFeedLab(t)
	var ids []string
	for i := 0; i < maxLiveFeeds; i++ {
		_, f, err := l.create("u_none", "", "bk_a1", "")
		if err != nil {
			t.Fatalf("feed %d: %v", i, err)
		}
		ids = append(ids, f.ID)
	}
	if _, _, err := l.create("u_none", "", "bk_a1", ""); !errors.Is(err, ErrTooManyFeeds) {
		t.Fatalf("51st = %v", err)
	}
	if _, _, err := l.create("u_own", "", "bk_a1", ""); err != nil {
		t.Errorf("another person's limit: %v", err)
	}
	l.revoke("u_none", ids[0])
	l.now = l.now.Add(31 * 24 * time.Hour)
	if _, _, err := l.create("u_none", "", "bk_a1", ""); err != nil {
		t.Fatalf("after a revoke: %v", err)
	}
	var n int
	l.st.Reader().QueryRow(`SELECT count(*) FROM podcast_feeds WHERE id = ?`, ids[0]).Scan(&n)
	if n != 0 {
		t.Error("a feed revoked over 30 days ago was kept")
	}
}
