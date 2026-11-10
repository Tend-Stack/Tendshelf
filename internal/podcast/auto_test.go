package podcast

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"
)

// autoRig follows a show of five episodes for us_a (and us_b), with the publisher serving 1000-byte audio.
func autoRig(t *testing.T) (*rig, string, map[string]string) {
	t.Helper()
	audio := bytes.Repeat([]byte("a"), 1000)
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.Write(audio)
	})
	ctx := context.Background()
	show := r.follow(t, "us_a", "https://a.example/f.xml")
	f := feedOf(5, 0)
	for i := range f.Episodes {
		f.Episodes[i].AudioURL = r.pub.URL("aud.test", fmt.Sprintf("/%d.mp3", i))
		f.Episodes[i].AudioBytes = 1000
	}
	if _, err := r.s.ApplyFeed(ctx, show, f, FetchMeta{}); err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	rows, _ := r.st.Reader().Query(`SELECT guid_key, id FROM podcast_episodes`)
	for rows.Next() {
		var k, id string
		rows.Scan(&k, &id)
		ids[k] = id
	}
	rows.Close()
	return r, show, ids
}

func (r *rig) cacheQueued(id string) bool {
	_, ok, _ := r.svc.Runner.ActiveFor(context.Background(), KindCache, "episode_id", id)
	return ok
}

func (r *rig) kept(t *testing.T, id string) bool {
	t.Helper()
	var p *string
	if err := r.st.Reader().QueryRow(`SELECT cache_path FROM podcast_episodes WHERE id = ?`, id).Scan(&p); err != nil {
		t.Fatal(err)
	}
	return p != nil
}

// Off by default twice: the owner's switch and the follower's per-show switch.
func TestAutoDownloadOffByDefault(t *testing.T) {
	r, show, ids := autoRig(t)
	ctx := context.Background()
	r.enable(t, func(s *Settings) { s.ServerAuto = true })
	if q, rm, err := r.svc.AutoDownload(ctx); err != nil || q != 0 || rm != 0 {
		t.Fatalf("no follower switch: %d %d %v", q, rm, err)
	}
	if err := r.s.SetShowSettings(ctx, "us_a", show, ShowSettings{ServerAuto: true, KeepLatest: 2}); err != nil {
		t.Fatal(err)
	}
	r.enable(t, nil) // the owner's switch off
	if q, _, err := r.svc.AutoDownload(ctx); err != nil || q != 0 {
		t.Fatalf("no owner switch: %d %v", q, err)
	}
	r.enable(t, func(s *Settings) { s.ServerAuto = true; s.Enabled = false })
	if q, _, err := r.svc.AutoDownload(ctx); err != nil || q != 0 {
		t.Fatalf("podcasts off: %d %v", q, err)
	}
	r.svc.Tick(ctx, time.Time{})
	if r.cacheQueued(ids["k0005"]) {
		t.Error("the scheduler queued a download with podcasts off")
	}
}

// The newest keep_latest unplayed episodes are queued; once played they go, and the next one is queued.
func TestAutoDownloadKeepsLatestAndRemovesPlayed(t *testing.T) {
	r, show, ids := autoRig(t)
	ctx := context.Background()
	r.enable(t, func(s *Settings) { s.ServerAuto = true })
	if err := r.s.SetShowSettings(ctx, "us_a", show, ShowSettings{ServerAuto: true, KeepLatest: 2}); err != nil {
		t.Fatal(err)
	}
	q, _, err := r.svc.AutoDownload(ctx)
	if err != nil || q != 2 {
		t.Fatalf("first pass queued %d: %v", q, err)
	}
	if !r.cacheQueued(ids["k0005"]) || !r.cacheQueued(ids["k0004"]) || r.cacheQueued(ids["k0003"]) {
		t.Fatal("the newest two should be queued, no more")
	}
	// The same pass again queues nothing more (the jobs are waiting, and are remembered).
	if q, _, _ := r.svc.AutoDownload(ctx); q != 0 {
		t.Errorf("second pass queued %d", q)
	}
	for _, k := range []string{"k0005", "k0004"} {
		if _, err := r.run(r.svc.handleCache, map[string]string{"episode_id": ids[k]}); err != nil {
			t.Fatal(err)
		}
	}
	if !r.kept(t, ids["k0005"]) || !r.kept(t, ids["k0004"]) {
		t.Fatal("not kept")
	}
	// Playing k0005 removes it and brings k0003 in.
	r.s.SetPlayed(ctx, "us_a", ids["k0005"], true)
	q, rm, err := r.svc.AutoDownload(ctx)
	if err != nil || q != 1 || rm != 1 {
		t.Fatalf("after a play: queued %d removed %d %v", q, rm, err)
	}
	if r.kept(t, ids["k0005"]) || !r.kept(t, ids["k0004"]) || !r.cacheQueued(ids["k0003"]) {
		t.Error("kept/queued set wrong after the play")
	}
	// A kept episode of a show with no auto follower is never touched by it.
	r.s.SetShowSettings(ctx, "us_a", show, ShowSettings{ServerAuto: false, KeepLatest: 2})
	r.svc.PutCache(ctx, ids["k0001"], 1<<30)
	if _, rm, _ := r.svc.AutoDownload(ctx); rm != 0 || !r.kept(t, ids["k0001"]) {
		t.Errorf("removed %d with no auto follower", rm)
	}
}

// An episode older than the newest keep_latest, kept for another reason, goes too; one still wanted stays.
func TestAutoDownloadDropsAgedOut(t *testing.T) {
	r, show, ids := autoRig(t)
	ctx := context.Background()
	r.enable(t, func(s *Settings) { s.ServerAuto = true })
	r.s.SetShowSettings(ctx, "us_a", show, ShowSettings{ServerAuto: true, KeepLatest: 2})
	for _, k := range []string{"k0001", "k0004"} {
		if err := r.svc.PutCache(ctx, ids[k], 1<<30); err != nil {
			t.Fatal(err)
		}
	}
	if _, rm, err := r.svc.AutoDownload(ctx); err != nil || rm != 1 {
		t.Fatalf("removed %d: %v", rm, err)
	}
	if r.kept(t, ids["k0001"]) || !r.kept(t, ids["k0004"]) {
		t.Error("the aged-out file should go, the wanted one stay")
	}
}

// Nothing is queued that would take the cache past the owner's limit.
func TestAutoDownloadNeverExceedsTheLimit(t *testing.T) {
	r, show, ids := autoRig(t)
	ctx := context.Background()
	r.enable(t, func(s *Settings) { s.ServerAuto = true; s.CacheBytes = 1500 })
	r.s.SetShowSettings(ctx, "us_a", show, ShowSettings{ServerAuto: true, KeepLatest: 4})
	q, _, err := r.svc.AutoDownload(ctx)
	if err != nil || q != 1 {
		t.Fatalf("queued %d under a 1500-byte limit with 1000-byte episodes: %v", q, err)
	}
	if !r.cacheQueued(ids["k0005"]) {
		t.Error("the newest should be the one queued")
	}
	// A zero limit keeps nothing at all.
	r.enable(t, func(s *Settings) { s.ServerAuto = true; s.CacheBytes = 0 })
	r.advance(7 * time.Hour)
	if q, _, _ := r.svc.AutoDownload(ctx); q != 0 {
		t.Errorf("queued %d with the cache off", q)
	}
	// The job itself also trims: kept bytes never pass the limit.
	r.enable(t, func(s *Settings) { s.ServerAuto = true; s.CacheBytes = 1500 })
	for _, k := range []string{"k0005", "k0004"} {
		r.svc.PutCache(ctx, ids[k], 1500)
	}
	if n := r.svc.CacheUsage(ctx); n > 1500 {
		t.Errorf("cache holds %d bytes over a 1500 limit", n)
	}
}

// A failed episode is not queued again every tick; it is tried again after a few hours.
func TestAutoDownloadRetryWindow(t *testing.T) {
	r, show, ids := autoRig(t)
	ctx := context.Background()
	r.enable(t, func(s *Settings) { s.ServerAuto = true })
	r.s.SetShowSettings(ctx, "us_a", show, ShowSettings{ServerAuto: true, KeepLatest: 1})
	if q, _, _ := r.svc.AutoDownload(ctx); q != 1 {
		t.Fatalf("queued %d", q)
	}
	// Pretend the job failed and is gone.
	r.exec(t, `DELETE FROM jobs WHERE kind = ?`, KindCache)
	r.advance(time.Hour)
	if q, _, _ := r.svc.AutoDownload(ctx); q != 0 {
		t.Errorf("retried after an hour: %d", q)
	}
	r.advance(6 * time.Hour)
	if q, _, _ := r.svc.AutoDownload(ctx); q != 1 || !r.cacheQueued(ids["k0005"]) {
		t.Errorf("not retried after 7 hours: %d", q)
	}
}
