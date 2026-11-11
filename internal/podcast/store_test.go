package podcast

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/podcast/rss"
)

type env struct {
	st  *db.Store
	s   *Store
	clk *time.Time
}

func newEnv(t *testing.T) *env {
	t.Helper()
	st, err := db.Open(context.Background(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	clk := time.UnixMilli(2_000_000_000_000)
	e := &env{st: st, clk: &clk}
	e.s = NewStore(st, func() time.Time { return *e.clk })
	for _, u := range []string{"us_a", "us_b"} {
		e.exec(t, `INSERT INTO users(id, username, role, created_at) VALUES (?, ?, 'member', 1)`, u, u)
	}
	return e
}

func (e *env) exec(t *testing.T, q string, args ...any) {
	t.Helper()
	if _, err := e.st.Writer().Exec(q, args...); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func (e *env) advance(d time.Duration) { *e.clk = e.clk.Add(d) }

// feed builds a parsed feed of n episodes numbered 1..n, episode i published at base+i*1000 ms.
func feedOf(n int, base int64) rss.Feed {
	f := rss.Feed{Title: "Show", Author: "Ann", ImageURL: "https://a.example/art.png"}
	for i := 1; i <= n; i++ {
		f.Episodes = append(f.Episodes, rss.Item{GUIDKey: fmt.Sprintf("k%04d", i), GUID: fmt.Sprintf("g%d", i), Title: fmt.Sprintf("Episode %d", i),
			AudioURL: fmt.Sprintf("https://a.example/%d.mp3", i), AudioType: "audio/mpeg", PublishedAt: base + int64(i)*1000, DurationMs: 600_000})
	}
	return f
}

func (e *env) follow(t *testing.T, user, url string) string {
	t.Helper()
	id, _, err := e.s.Follow(context.Background(), user, url, "")
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestNormalizeFeedURL(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"HTTPS://Example.COM:443/Feed.xml#frag", "https://example.com/Feed.xml"},
		{"  http://example.com:80/a?b=1 ", "http://example.com/a?b=1"},
		{"http://example.com:8080/a", "http://example.com:8080/a"},
		{"https://example.com./f", "https://example.com/f"},
		{"https://[2001:db8::1]:8443/f", "https://[2001:db8::1]:8443/f"},
		{"ftp://example.com/f", ""},
		{"https://user:pw@example.com/f", ""},
		{"https://example.com:22/f", ""},
		{"https:///f", ""},
		{"example.com/feed", ""},
	} {
		got, err := NormalizeFeedURL(c.in)
		if (err != nil) != (c.want == "") || got != c.want {
			t.Errorf("%q = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

func TestFollowLimitAndIdempotence(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id1, created, err := e.s.Follow(ctx, "us_a", "https://a.example/f.xml#x", "slug-one")
	if err != nil || !created {
		t.Fatalf("first follow: %v %v", err, created)
	}
	id2, created, err := e.s.Follow(ctx, "us_a", "https://A.example:443/f.xml", "")
	if err != nil || created || id2 != id1 {
		t.Fatalf("second follow: %q %v %v", id2, created, err)
	}
	id3, created, err := e.s.Follow(ctx, "us_b", "https://a.example/f.xml", "")
	if err != nil || !created || id3 != id1 {
		t.Fatalf("another person follows the same show: %q %v %v", id3, created, err)
	}
	for i := 1; i < MaxFollows; i++ {
		e.follow(t, "us_a", fmt.Sprintf("https://s%d.example/f.xml", i))
	}
	if _, _, err := e.s.Follow(ctx, "us_a", "https://one-more.example/f.xml", ""); !errors.Is(err, ErrTooMany) {
		t.Fatalf("201st follow: %v", err)
	}
	if _, _, err := e.s.Follow(ctx, "us_a", "https://a.example/f.xml", ""); err != nil {
		t.Fatalf("a show already followed at the limit: %v", err)
	}
	if _, _, err := e.s.Follow(ctx, "us_b", "https://one-more.example/f.xml", ""); err != nil {
		t.Fatalf("the limit is per person: %v", err)
	}
	if _, _, err := e.s.Follow(ctx, "us_a", "ftp://x.example/f", ""); !errors.Is(err, ErrBadURL) {
		t.Fatalf("bad scheme: %v", err)
	}
	if got, _ := e.s.Shows(ctx, "us_a"); len(got) != MaxFollows {
		t.Fatalf("shows = %d", len(got))
	}
}

func TestFollowKeepsFirstSlugAndShowsArePerPerson(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id, _, _ := e.s.Follow(ctx, "us_a", "https://a.example/f.xml", "first")
	_, _, _ = e.s.Follow(ctx, "us_b", "https://a.example/f.xml", "second")
	sh, err := e.s.ShowByID(ctx, id)
	if err != nil || sh.OndaCastSlug != "first" {
		t.Fatalf("slug = %q %v", sh.OndaCastSlug, err)
	}
	if err := e.s.Unfollow(ctx, "us_a", id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.Show(ctx, "us_a", id); !errors.Is(err, ErrNotFound) {
		t.Errorf("show after unfollow: %v", err)
	}
	if _, err := e.s.Show(ctx, "us_b", id); err != nil {
		t.Errorf("the other person still follows: %v", err)
	}
	if err := e.s.Unfollow(ctx, "us_a", id); !errors.Is(err, ErrNotFound) {
		t.Errorf("second unfollow: %v", err)
	}
}

func TestApplyFeedUpsertGoneAndPrune(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	show := e.follow(t, "us_a", "https://a.example/f.xml")
	added, err := e.s.ApplyFeed(ctx, show, feedOf(3, 0), FetchMeta{ETag: `"e1"`})
	if err != nil || added != 3 {
		t.Fatalf("first apply: %d %v", added, err)
	}
	sh, _ := e.s.ShowByID(ctx, show)
	if sh.Title != "Show" || sh.ETag != `"e1"` || sh.FetchedAt == 0 || sh.ArtURL != "https://a.example/art.png" {
		t.Fatalf("show = %+v", sh)
	}
	// Same feed again: nothing new, nothing rewritten.
	e.advance(time.Hour)
	before, _, _ := e.s.Episodes(ctx, "us_a", show, EpisodeQuery{})
	if added, err = e.s.ApplyFeed(ctx, show, feedOf(3, 0), FetchMeta{}); err != nil || added != 0 {
		t.Fatalf("second apply: %d %v", added, err)
	}
	after, _, _ := e.s.Episodes(ctx, "us_a", show, EpisodeQuery{})
	for i := range before {
		if before[i].UpdatedAt != after[i].UpdatedAt {
			t.Errorf("episode %s rewritten without a change", before[i].Title)
		}
	}
	// Episode 1 vanishes (gone), episode 2's title changes, episode 4 is new.
	f := feedOf(4, 0)
	f.Episodes = f.Episodes[1:]
	f.Episodes[0].Title = "Renamed"
	if added, err = e.s.ApplyFeed(ctx, show, f, FetchMeta{}); err != nil || added != 1 {
		t.Fatalf("third apply: %d %v", added, err)
	}
	got, _, _ := e.s.Episodes(ctx, "us_a", show, EpisodeQuery{})
	if len(got) != 3 || got[2].Title != "Renamed" {
		t.Fatalf("episodes after change: %d %+v", len(got), got)
	}
	// A gone episode with a place stays listed; one with none is hidden but kept.
	var one Episode
	all, _, _ := e.s.Episodes(ctx, "us_a", show, EpisodeQuery{Sort: "oldest"})
	_ = all
	row := e.st.Reader().QueryRow(`SELECT id FROM podcast_episodes WHERE show_id = ? AND guid_key = 'k0001'`, show)
	if err := row.Scan(&one.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.s.SetFavourite(ctx, "us_a", one.ID, true); err != nil {
		t.Fatal(err)
	}
	got, _, _ = e.s.Episodes(ctx, "us_a", show, EpisodeQuery{})
	if len(got) != 4 {
		t.Errorf("a starred gone episode stays listed: %d", len(got))
	}
	// It comes back when the feed has it again.
	if _, err = e.s.ApplyFeed(ctx, show, feedOf(4, 0), FetchMeta{}); err != nil {
		t.Fatal(err)
	}
	if ep, _ := e.s.Episode(ctx, "us_a", one.ID); ep.GoneAt != 0 {
		t.Error("gone mark not cleared")
	}

	// Prune: 3,002 episodes keep the newest 3,000 plus the older ones somebody has state for.
	big := e.follow(t, "us_a", "https://big.example/f.xml")
	if _, err = e.s.ApplyFeed(ctx, big, feedOf(MaxEpisodesPerShow+2, 0), FetchMeta{}); err != nil {
		t.Fatal(err)
	}
	var n int
	e.st.Reader().QueryRow(`SELECT count(*) FROM podcast_episodes WHERE show_id = ?`, big).Scan(&n)
	if n != MaxEpisodesPerShow {
		t.Fatalf("episodes kept = %d", n)
	}
	var oldest string
	e.st.Reader().QueryRow(`SELECT id FROM podcast_episodes WHERE show_id = ? ORDER BY published_at LIMIT 1`, big).Scan(&oldest)
	if err := e.s.SetFavourite(ctx, "us_b", oldest, true); err != nil {
		t.Fatal(err)
	}
	f = feedOf(MaxEpisodesPerShow+3, 0)
	if _, err = e.s.ApplyFeed(ctx, big, f, FetchMeta{}); err != nil {
		t.Fatal(err)
	}
	e.st.Reader().QueryRow(`SELECT count(*) FROM podcast_episodes WHERE show_id = ?`, big).Scan(&n)
	if n != MaxEpisodesPerShow+1 {
		t.Errorf("episodes after the starred one is kept = %d, want %d", n, MaxEpisodesPerShow+1)
	}
	if _, err := e.s.Episode(ctx, "us_b", oldest); err != nil {
		t.Errorf("an episode with a star was pruned: %v", err)
	}
}

func TestMovedFeedURL(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.follow(t, "us_a", "https://old.example/f.xml")
	b := e.follow(t, "us_a", "https://taken.example/f.xml")
	if _, err := e.s.ApplyFeed(ctx, a, feedOf(1, 0), FetchMeta{MovedTo: "https://new.example/f.xml"}); err != nil {
		t.Fatal(err)
	}
	if sh, _ := e.s.ShowByID(ctx, a); sh.FeedURL != "https://new.example/f.xml" {
		t.Errorf("feed url = %q", sh.FeedURL)
	}
	if _, err := e.s.ApplyFeed(ctx, b, feedOf(1, 0), FetchMeta{MovedTo: "https://new.example/f.xml"}); err != nil {
		t.Fatal(err)
	}
	if sh, _ := e.s.ShowByID(ctx, b); sh.FeedURL != "https://taken.example/f.xml" {
		t.Errorf("a move onto another show's address was applied: %q", sh.FeedURL)
	}
}

func TestMarkFailedBackoff(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.follow(t, "us_a", "https://a.example/f.xml")
	want := []time.Duration{2 * time.Hour, 4 * time.Hour, 8 * time.Hour, 16 * time.Hour, 24 * time.Hour, 24 * time.Hour}
	for i, w := range want {
		if err := e.s.MarkFailed(ctx, id, "offline"); err != nil {
			t.Fatal(err)
		}
		sh, _ := e.s.ShowByID(ctx, id)
		if sh.FailCode != "offline" || sh.FailCount != i+1 || sh.NextRefreshAt != e.clk.Add(w).UnixMilli() {
			t.Errorf("failure %d: %+v (next %v)", i+1, sh.FailCount, time.UnixMilli(sh.NextRefreshAt).Sub(*e.clk))
		}
	}
	if _, err := e.s.ApplyFeed(ctx, id, feedOf(1, 0), FetchMeta{}); err != nil {
		t.Fatal(err)
	}
	if sh, _ := e.s.ShowByID(ctx, id); sh.FailCode != "" || sh.FailCount != 0 {
		t.Errorf("failure not cleared: %+v", sh)
	}
}

func TestEpisodeFiltersSortSearchAndPaging(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	show := e.follow(t, "us_a", "https://a.example/f.xml")
	f := feedOf(5, 0)
	f.Episodes[1].Title = "100% sure_thing"
	if _, err := e.s.ApplyFeed(ctx, show, f, FetchMeta{}); err != nil {
		t.Fatal(err)
	}
	ids := map[int]string{}
	rows, _ := e.st.Reader().Query(`SELECT id, guid_key FROM podcast_episodes`)
	for rows.Next() {
		var id, k string
		rows.Scan(&id, &k)
		var n int
		fmt.Sscanf(k, "k%d", &n)
		ids[n] = id
	}
	rows.Close()
	if err := e.s.SetPlayed(ctx, "us_a", ids[1], true); err != nil {
		t.Fatal(err)
	}
	if _, err := e.s.SaveProgress(ctx, "us_a", ids[2], Progress{PositionMs: 5000, ClientUpdatedAt: e.clk.UnixMilli(), DeviceID: "device-aaaa"}); err != nil {
		t.Fatal(err)
	}
	titles := func(q EpisodeQuery) string {
		got, _, err := e.s.Episodes(ctx, "us_a", show, q)
		if err != nil {
			t.Fatalf("%+v: %v", q, err)
		}
		s := ""
		for _, ep := range got {
			s += ep.Title[len(ep.Title)-1:]
		}
		return s
	}
	if got := titles(EpisodeQuery{}); got != "543g1" {
		t.Errorf("newest first = %q", got)
	}
	if got := titles(EpisodeQuery{Sort: "oldest"}); got != "1g345" {
		t.Errorf("oldest first = %q", got)
	}
	if got := titles(EpisodeQuery{Filter: "unplayed"}); got != "543" {
		t.Errorf("unplayed = %q", got)
	}
	if got := titles(EpisodeQuery{Filter: "in_progress"}); got != "g" {
		t.Errorf("in progress = %q", got)
	}
	if got := titles(EpisodeQuery{Filter: "played"}); got != "1" {
		t.Errorf("played = %q", got)
	}
	if got := titles(EpisodeQuery{Text: "100%"}); got != "g" {
		t.Errorf("search with %% = %q", got)
	}
	if got := titles(EpisodeQuery{Text: "e_t"}); got != "g" {
		t.Errorf("search with _ = %q", got)
	}
	if got := titles(EpisodeQuery{Text: "%"}); got != "g" {
		t.Errorf("search for a bare %% matched more than literal percent signs: %q", got)
	}
	if got := titles(EpisodeQuery{Text: "nothing"}); got != "" {
		t.Errorf("search nothing = %q", got)
	}
	// Paging by cursor, both directions.
	for _, sort := range []string{"newest", "oldest"} {
		var seen []string
		after := ""
		for {
			page, next, err := e.s.Episodes(ctx, "us_a", show, EpisodeQuery{Sort: sort, Limit: 2, After: after})
			if err != nil {
				t.Fatal(err)
			}
			for _, ep := range page {
				seen = append(seen, ep.ID)
			}
			if next == "" {
				break
			}
			after = next
		}
		if len(seen) != 5 {
			t.Errorf("%s paging saw %d episodes", sort, len(seen))
		}
	}
	for _, bad := range []EpisodeQuery{{Filter: "x"}, {Sort: "x"}, {After: "junk"}} {
		if _, _, err := e.s.Episodes(ctx, "us_a", show, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: %v", bad, err)
		}
	}
	// Another person sees none of this person's state.
	if got, _, _ := e.s.Episodes(ctx, "us_b", show, EpisodeQuery{Filter: "played"}); len(got) != 0 {
		t.Errorf("state leaked to another person: %d", len(got))
	}
}

func TestProgressNewerWinsAndFinished(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	show := e.follow(t, "us_a", "https://a.example/f.xml")
	e.s.ApplyFeed(ctx, show, feedOf(1, 0), FetchMeta{})
	var ep string
	e.st.Reader().QueryRow(`SELECT id FROM podcast_episodes`).Scan(&ep)
	now := e.clk.UnixMilli()
	save := func(pos, at int64, fin bool) SaveResult {
		r, err := e.s.SaveProgress(ctx, "us_a", ep, Progress{PositionMs: pos, ClientUpdatedAt: at, DeviceID: "dev-12345", Finished: fin})
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	if r := save(1000, now-100, false); !r.Applied || r.State.PositionMs != 1000 {
		t.Fatalf("first: %+v", r)
	}
	if r := save(9000, now-200, false); r.Applied || r.State.PositionMs != 1000 {
		t.Fatalf("older save won: %+v", r)
	}
	if r := save(2000, now-50, false); !r.Applied || r.State.PositionMs != 2000 {
		t.Fatalf("newer save lost: %+v", r)
	}
	if r := save(99_999_999, now, false); r.State.PositionMs != 600_000 {
		t.Fatalf("position not clamped to the duration: %+v", r)
	}
	if r := save(600_000, now+1, true); !r.State.Played || r.State.PlayedAt == 0 {
		t.Fatalf("finished: %+v", r)
	}
	// An explicit "not played" beats a save from before it.
	if err := e.s.SetPlayed(ctx, "us_a", ep, false); err != nil {
		t.Fatal(err)
	}
	if r := save(5000, now+2, false); r.Applied {
		t.Fatalf("a save older than the mark undid it: %+v", r)
	}
	// A client clock far ahead is clamped to a minute ahead of the server.
	e.advance(time.Hour)
	save(3000, e.clk.UnixMilli()+10*24*60*60*1000, false)
	var c int64
	e.st.Reader().QueryRow(`SELECT client_updated_at FROM podcast_episode_state`).Scan(&c)
	if c > e.clk.UnixMilli()+60_000 {
		t.Errorf("client time not clamped: %d", c-e.clk.UnixMilli())
	}
	for name, p := range map[string]Progress{
		"device":   {PositionMs: 1, ClientUpdatedAt: 1, DeviceID: "x"},
		"position": {PositionMs: -1, ClientUpdatedAt: 1, DeviceID: "dev-12345"},
		"time":     {PositionMs: 1, ClientUpdatedAt: 0, DeviceID: "dev-12345"},
	} {
		if _, err := e.s.SaveProgress(ctx, "us_a", ep, p); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if _, err := e.s.SaveProgress(ctx, "us_a", "pe_nope", Progress{PositionMs: 1, ClientUpdatedAt: 1, DeviceID: "dev-12345"}); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown episode: %v", err)
	}
}

func TestNewCountsHistoryFavouritesAndSeen(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	show := e.follow(t, "us_a", "https://a.example/f.xml") // followed at clock time T
	base := e.clk.UnixMilli()
	// Two episodes older than the follow, three newer.
	f := feedOf(5, base-10_000)
	f.Episodes[0].PublishedAt, f.Episodes[1].PublishedAt = base-5000, base-4000
	for i := 2; i < 5; i++ {
		f.Episodes[i].PublishedAt = base + int64(i)*1000
	}
	if _, err := e.s.ApplyFeed(ctx, show, f, FetchMeta{}); err != nil {
		t.Fatal(err)
	}
	row, _ := e.s.Show(ctx, "us_a", show)
	if row.NewCount != 3 {
		t.Fatalf("new count = %d", row.NewCount)
	}
	if n, _ := e.s.NewTotal(ctx, "us_a"); n != 3 {
		t.Errorf("new total = %d", n)
	}
	var newest string
	e.st.Reader().QueryRow(`SELECT id FROM podcast_episodes ORDER BY published_at DESC LIMIT 1`).Scan(&newest)
	e.s.SetPlayed(ctx, "us_a", newest, true)
	if row, _ = e.s.Show(ctx, "us_a", show); row.NewCount != 2 {
		t.Errorf("new count after playing one = %d", row.NewCount)
	}
	if got, _ := e.s.NewEpisodes(ctx, "us_a", 10, 0); len(got) != 2 {
		t.Errorf("new episodes = %d", len(got))
	}
	e.advance(time.Hour)
	if err := e.s.MarkSeen(ctx, "us_a", show); err != nil {
		t.Fatal(err)
	}
	if row, _ = e.s.Show(ctx, "us_a", show); row.NewCount != 0 {
		t.Errorf("new count after seen = %d", row.NewCount)
	}
	if err := e.s.MarkSeen(ctx, "us_b", show); !errors.Is(err, ErrNotFound) {
		t.Errorf("seen by a stranger: %v", err)
	}
	// History and favourites are per person.
	e.s.SaveProgress(ctx, "us_a", newest, Progress{PositionMs: 10, ClientUpdatedAt: e.clk.UnixMilli(), DeviceID: "dev-12345"})
	e.s.SetFavourite(ctx, "us_a", newest, true)
	if h, _ := e.s.History(ctx, "us_a", 10); len(h) != 1 || h[0].ID != newest {
		t.Errorf("history = %d", len(h))
	}
	if h, _ := e.s.History(ctx, "us_b", 10); len(h) != 0 {
		t.Errorf("another person's history = %d", len(h))
	}
	if f, _ := e.s.Favourites(ctx, "us_a", 10); len(f) != 1 {
		t.Errorf("favourites = %d", len(f))
	}
	e.s.SetFavourite(ctx, "us_a", newest, false)
	if f, _ := e.s.Favourites(ctx, "us_a", 10); len(f) != 0 {
		t.Errorf("favourites after unstar = %d", len(f))
	}
	if err := e.s.SetFavourite(ctx, "us_a", "pe_nope", true); !errors.Is(err, ErrNotFound) {
		t.Errorf("star unknown: %v", err)
	}
}

func TestShowSettings(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	show := e.follow(t, "us_a", "https://a.example/f.xml")
	got, err := e.s.ShowSettings(ctx, "us_a", show)
	if err != nil || got.Speed != nil || got.KeepLatest != 3 {
		t.Fatalf("defaults: %+v %v", got, err)
	}
	sp := 1.75
	if err := e.s.SetShowSettings(ctx, "us_a", show, ShowSettings{Speed: &sp, SkipIntroS: 30, SkipOutroS: 15, ServerAuto: true, KeepLatest: 5}); err != nil {
		t.Fatal(err)
	}
	got, _ = e.s.ShowSettings(ctx, "us_a", show)
	if got.Speed == nil || *got.Speed != 1.75 || got.SkipIntroS != 30 || got.SkipOutroS != 15 || !got.ServerAuto || got.KeepLatest != 5 {
		t.Errorf("saved: %+v", got)
	}
	four := 4.0
	for name, s := range map[string]ShowSettings{
		"speed": {Speed: &four, KeepLatest: 3}, "intro": {SkipIntroS: 601, KeepLatest: 3}, "outro": {SkipOutroS: -1, KeepLatest: 3}, "keep": {KeepLatest: 0},
	} {
		if err := e.s.SetShowSettings(ctx, "us_a", show, s); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: %v", name, err)
		}
	}
	if err := e.s.SetShowSettings(ctx, "us_b", show, ShowSettings{KeepLatest: 3}); !errors.Is(err, ErrNotFound) {
		t.Errorf("a stranger's settings: %v", err)
	}
}

func TestOrphans(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	a := e.follow(t, "us_a", "https://a.example/f.xml")
	b := e.follow(t, "us_a", "https://b.example/f.xml")
	e.s.ApplyFeed(ctx, a, feedOf(1, 0), FetchMeta{})
	e.s.ApplyFeed(ctx, b, feedOf(1, 0), FetchMeta{})
	var ep string
	e.st.Reader().QueryRow(`SELECT id FROM podcast_episodes WHERE show_id = ?`, a).Scan(&ep)
	e.s.SetFavourite(ctx, "us_a", ep, true)
	e.s.Unfollow(ctx, "us_a", a)
	e.s.Unfollow(ctx, "us_a", b)
	got, _ := e.s.Orphans(ctx)
	if len(got) != 1 || got[0] != b {
		t.Errorf("orphans = %v, want only the show without state", got)
	}
}

func TestOrphansSkipOpenedShows(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id := e.open(t, "us_a", "https://a.example/f.xml")
	if got, _ := e.s.Orphans(ctx); len(got) != 0 {
		t.Errorf("an opened show is an orphan: %v", got)
	}
	if deleted, err := e.s.DeleteShow(ctx, id); err != nil || deleted {
		t.Errorf("delete of an opened show: %v %v", deleted, err)
	}
	e.advance(31 * 24 * time.Hour)
	if _, err := e.s.ExpireOpens(ctx, e.clk.Add(-OpenTTL).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if got, _ := e.s.Orphans(ctx); len(got) != 1 || got[0] != id {
		t.Errorf("after the open expired: %v", got)
	}
	if deleted, err := e.s.DeleteShow(ctx, id); err != nil || !deleted {
		t.Errorf("delete after expiry: %v %v", deleted, err)
	}
}
