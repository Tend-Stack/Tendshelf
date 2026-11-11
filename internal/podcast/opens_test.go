package podcast

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"
)

func (e *env) open(t *testing.T, user, url string) string {
	t.Helper()
	id, following, err := e.s.Open(context.Background(), user, url, "")
	if err != nil || following {
		t.Fatalf("open %s: %v %v", url, following, err)
	}
	return id
}

func (e *env) openCount(t *testing.T, user string) int {
	t.Helper()
	var n int
	if err := e.st.Reader().QueryRow(`SELECT count(*) FROM podcast_opens WHERE user_id = ?`, user).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// queueEpisodeOf puts one episode of the show in the person's Up next.
func (e *env) queueEpisodeOf(t *testing.T, user, show string) {
	t.Helper()
	ep := "pe_q" + show
	e.exec(t, `INSERT OR IGNORE INTO podcast_episodes(id, show_id, guid_key, title, audio_url, created_at, updated_at) VALUES (?, ?, 'q', 'Q', 'https://a.example/q.mp3', 1, 1)`, ep, show)
	e.exec(t, `INSERT INTO queue_items(user_id, item_id, episode_id, position, added_at) VALUES (?, ?, ?, 1, 1)`, user, ep, ep)
}

func TestOpenWithoutFollow(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	for i := 0; i < MaxFollows; i++ {
		e.follow(t, "us_a", fmt.Sprintf("https://f%d.example/f.xml", i))
	}
	id := e.open(t, "us_a", "https://open.example/f.xml")
	shows, _ := e.s.Shows(ctx, "us_a")
	for _, sh := range shows {
		if sh.ID == id {
			t.Error("an open is listed in Shows")
		}
	}
	if e.s.Follows(ctx, "us_a", id) {
		t.Error("an open counts as a follow")
	}
	if !e.s.Knows(ctx, "us_a", id) {
		t.Error("the opener does not know the show")
	}
	if e.s.Knows(ctx, "us_b", id) {
		t.Error("another person knows the show")
	}
	if due, _ := e.s.DueShows(ctx, 500); len(due) != MaxFollows {
		t.Errorf("an open is due for the scheduler: %d", len(due))
	}
}

func TestOpenWhenFollowing(t *testing.T) {
	e := newEnv(t)
	id := e.follow(t, "us_a", "https://a.example/f.xml")
	got, following, err := e.s.Open(context.Background(), "us_a", "https://a.example/f.xml", "")
	if err != nil || !following || got != id {
		t.Fatalf("open of a followed show: %q %v %v", got, following, err)
	}
	if n := e.openCount(t, "us_a"); n != 0 {
		t.Errorf("rows = %d, want 0", n)
	}
	if _, _, err := e.s.Open(context.Background(), "us_a", "ftp://bad", ""); !errors.Is(err, ErrBadURL) {
		t.Errorf("bad address: %v", err)
	}
}

func TestOpenCap(t *testing.T) {
	e := newEnv(t)
	first := e.open(t, "us_a", "https://s0.example/f.xml")
	second := e.open(t, "us_a", "https://s1.example/f.xml")
	e.queueEpisodeOf(t, "us_a", first) // the oldest is queued: it survives
	var last string
	for i := 2; i <= MaxOpens+1; i++ { // 52 opens in all
		e.advance(time.Second)
		last = e.open(t, "us_a", fmt.Sprintf("https://s%d.example/f.xml", i))
	}
	if n := e.openCount(t, "us_a"); n != MaxOpens+1 {
		t.Fatalf("rows = %d, want %d", n, MaxOpens+1)
	}
	has := func(show string) bool {
		var n int
		e.st.Reader().QueryRow(`SELECT count(*) FROM podcast_opens WHERE user_id='us_a' AND show_id=?`, show).Scan(&n)
		return n == 1
	}
	// The cap counts 50 unqueued opens; the queued one is extra, so the oldest unqueued open (second) goes.
	if !has(first) || has(second) || !has(last) {
		t.Errorf("queued kept %v, oldest unqueued kept %v, newest kept %v", has(first), has(second), has(last))
	}
	// Another person's rows do not count against this person's cap.
	e.open(t, "us_b", "https://s0.example/f.xml")
	if n := e.openCount(t, "us_a"); n != MaxOpens+1 {
		t.Errorf("rows after b's open = %d", n)
	}
}

func TestShowKnownPerPerson(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.open(t, "us_a", "https://a.example/f.xml")
	if sh, err := e.s.ShowKnown(ctx, "us_a", id); err != nil || sh.FollowedAt != 0 || sh.NewCount != 0 || sh.ID != id {
		t.Errorf("opener: %+v %v", sh, err)
	}
	if _, err := e.s.ShowKnown(ctx, "us_b", id); !errors.Is(err, ErrNotFound) {
		t.Errorf("stranger: %v", err)
	}
	if _, err := e.s.Show(ctx, "us_a", id); !errors.Is(err, ErrNotFound) {
		t.Errorf("Show must stay follower-only: %v", err)
	}
	e.s.ApplyFeed(ctx, id, feedOf(2, 0), FetchMeta{})
	var ep string
	e.st.Reader().QueryRow(`SELECT id FROM podcast_episodes WHERE show_id = ? LIMIT 1`, id).Scan(&ep)
	e.exec(t, `INSERT INTO podcast_episode_state(user_id, episode_id, updated_at) VALUES ('us_b', ?, 1)`, ep)
	if sh, err := e.s.ShowKnown(ctx, "us_b", id); err != nil || sh.FollowedAt != 0 || sh.NewCount != 0 {
		t.Errorf("state row only: %+v %v", sh, err)
	}
	fid := e.follow(t, "us_b", "https://a.example/f.xml")
	if sh, err := e.s.ShowKnown(ctx, "us_b", fid); err != nil || sh.FollowedAt == 0 {
		t.Errorf("follower: %+v %v", sh, err)
	}
}

func TestTouchOpenNeverInserts(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.open(t, "us_a", "https://a.example/f.xml")
	if err := e.s.TouchOpen(ctx, "us_b", id); err != nil {
		t.Fatal(err)
	}
	if n := e.openCount(t, "us_b"); n != 0 {
		t.Errorf("touch created a row: %d", n)
	}
	e.advance(time.Hour)
	if err := e.s.TouchOpen(ctx, "us_a", id); err != nil {
		t.Fatal(err)
	}
	var at int64
	e.st.Reader().QueryRow(`SELECT opened_at FROM podcast_opens WHERE user_id='us_a'`).Scan(&at)
	if at != e.clk.UnixMilli() {
		t.Errorf("opened_at = %d, want %d", at, e.clk.UnixMilli())
	}
}

func TestExpireOpens(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	old := e.open(t, "us_a", "https://old.example/f.xml")
	queued := e.open(t, "us_a", "https://queued.example/f.xml")
	e.queueEpisodeOf(t, "us_a", queued)
	e.advance(2 * 24 * time.Hour)
	fresh := e.open(t, "us_a", "https://fresh.example/f.xml")
	e.advance(29 * 24 * time.Hour) // old is 31 days, fresh 29
	n, err := e.s.ExpireOpens(ctx, e.clk.Add(-OpenTTL).UnixMilli())
	if err != nil || n != 1 {
		t.Fatalf("expired %d, %v", n, err)
	}
	if e.s.Knows(ctx, "us_a", old) || !e.s.Knows(ctx, "us_a", queued) || !e.s.Knows(ctx, "us_a", fresh) {
		t.Error("wrong opens expired")
	}
}

func TestFollowClearsOpen(t *testing.T) {
	e := newEnv(t)
	e.open(t, "us_a", "https://a.example/f.xml")
	e.open(t, "us_b", "https://a.example/f.xml")
	e.follow(t, "us_a", "https://a.example/f.xml")
	if n := e.openCount(t, "us_a"); n != 0 {
		t.Errorf("a's open stayed: %d", n)
	}
	if n := e.openCount(t, "us_b"); n != 1 {
		t.Errorf("b's open went: %d", n)
	}
}

func TestReopenAndShowForSlug(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id, _, err := e.s.Open(ctx, "us_a", "https://a.example/f.xml", "the-show")
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := e.s.ShowForSlug(ctx, "us_a", "the-show"); !ok || got != id {
		t.Errorf("opener slug = %q %v", got, ok)
	}
	if _, ok := e.s.ShowForSlug(ctx, "us_b", "the-show"); ok {
		t.Error("a slug known only to another person resolved")
	}
	if _, err := e.s.Reopen(ctx, "us_b", "ps_none"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown show: %v", err)
	}
	if following, err := e.s.Reopen(ctx, "us_b", id); err != nil || following {
		t.Errorf("reopen by b: %v %v", following, err)
	}
	if _, ok := e.s.ShowForSlug(ctx, "us_b", "the-show"); !ok {
		t.Error("b's open does not resolve the slug")
	}
}

func TestOpenRefreshIfStale(t *testing.T) {
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {})
	ctx := context.Background()
	r.enable(t, nil)
	jobsFor := func(show string) int {
		var n int
		r.st.Reader().QueryRow(`SELECT count(*) FROM jobs WHERE kind = ? AND payload_json LIKE ?`, KindRefresh, "%"+show+"%").Scan(&n)
		return n
	}
	id, err := r.svc.Open(ctx, "us_a", "https://a.example/f.xml", "")
	if err != nil || jobsFor(id) != 1 {
		t.Fatalf("never fetched: %v, jobs %d", err, jobsFor(id))
	}
	if err := r.svc.OpenKnown(ctx, "us_b", id); err != nil || jobsFor(id) != 1 {
		t.Errorf("second open while queued: %v, jobs %d", err, jobsFor(id))
	}
	r.exec(t, `UPDATE jobs SET state = 'done'`)
	r.exec(t, `UPDATE podcast_shows SET fetched_at = ?`, r.clk.UnixMilli()-10*60_000)
	r.svc.OpenKnown(ctx, "us_a", id)
	if n := jobsFor(id); n != 1 {
		t.Errorf("fetched 10 minutes ago: jobs %d", n)
	}
	r.exec(t, `UPDATE podcast_shows SET fetched_at = ?`, r.clk.UnixMilli()-2*3600_000)
	r.svc.OpenKnown(ctx, "us_a", id)
	if n := jobsFor(id); n != 2 {
		t.Errorf("fetched 2 hours ago: jobs %d", n)
	}
	r.exec(t, `UPDATE jobs SET state = 'done'`)
	r.exec(t, `UPDATE podcast_shows SET fail_count = 2, next_refresh_at = ?`, r.clk.UnixMilli()+3600_000)
	r.svc.OpenKnown(ctx, "us_a", id)
	if n := jobsFor(id); n != 2 {
		t.Errorf("back-off pending: jobs %d", n)
	}
}
