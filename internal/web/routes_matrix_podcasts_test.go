package web

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/podcast"
	"github.com/Tend-Stack/tendshelf/internal/podcast/rss"
)

// podSweep names what the podcast rows act on (plan P2, step 10). The owner, the admin and the member follow
// the show "Main"; only the owner follows "Other". Podcasts are on. The guest follows nothing and is refused
// by role. So on "Main" every signed-in non-guest answers 200, and on "Other" only the owner does.
type podSweep struct {
	fx            *podFix
	show, ep      string // Main and its first episode
	other, otherE string // Other and its first episode
	station       string // a station the owner, the admin and the member keep (radio, plan R1)
	otherStation  string // a station only the owner keeps
}

// seedPodcasts turns podcasts on and gives the rows their shows, episodes and a stored picture.
func (e *sweepEnv) seedPodcasts(t *testing.T, fx *podFix) {
	t.Helper()
	ctx := context.Background()
	fx.enable(t, e.a, func(s *podcast.Settings) { s.RadioEnabled = true })
	e.pod = podSweep{fx: fx}
	e.seedRadio(t, fx)
	seed := func(feedURL, title string, people ...int) (string, string) {
		var show string
		for _, i := range people {
			id, _, err := fx.svc.Follow(ctx, e.uid[i], feedURL, "")
			if err != nil {
				t.Fatal(err)
			}
			show = id
		}
		feed, err := rss.Parse([]byte(fx.rss(title)), feedURL)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fx.svc.ApplyFeed(ctx, show, feed, podcast.FetchMeta{}); err != nil {
			t.Fatal(err)
		}
		var ep string
		if err := e.a.st.Reader().QueryRow(`SELECT id FROM podcast_episodes WHERE show_id = ? ORDER BY published_at LIMIT 1`, show).Scan(&ep); err != nil {
			t.Fatal(err)
		}
		return show, ep
	}
	e.pod.show, e.pod.ep = seed(fx.feedURL(), "Main", sweepOwner, sweepAdmin, sweepMember)
	e.pod.other, e.pod.otherE = seed(fx.otherFeedURL(), "Other", sweepOwner)
	// An old fetch, so a Refresh press is accepted; and a stored picture for the art rows.
	if _, err := e.a.st.Writer().Exec(`UPDATE podcast_shows SET fetched_at = 1`); err != nil {
		t.Fatal(err)
	}
	sha := fmt.Sprintf("%064x", 0xfeed)
	var b bytes.Buffer
	png.Encode(&b, image.NewRGBA(image.Rect(0, 0, 8, 8)))
	dir := fx.svc.ArtDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sha+".png"), b.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{e.pod.show, e.pod.other} {
		if err := fx.svc.SetArt(ctx, id, "cache/podcast-art/"+sha+".png", sha); err != nil {
			t.Fatal(err)
		}
	}
}

func podcastsSweepRows() []sweepRow {
	const (
		ok  = http.StatusOK
		nf  = http.StatusNotFound
		see = http.StatusSeeOther
	)
	notGuest := sweepNotGuest(ok, nf)
	ownerOnly := [4]int{ok, nf, nf, nf} // someone else's show or episode: only the owner follows Other
	show := func(prefix, suffix string) func(*sweepEnv, int) string {
		return func(e *sweepEnv, _ int) string { return prefix + e.pod.show + suffix }
	}
	ep := func(prefix, suffix string) func(*sweepEnv, int) string {
		return func(e *sweepEnv, _ int) string { return prefix + e.pod.ep + suffix }
	}
	otherShow := func(prefix, suffix string) func(*sweepEnv, int) string {
		return func(e *sweepEnv, _ int) string { return prefix + e.pod.other + suffix }
	}
	otherEp := func(prefix, suffix string) func(*sweepEnv, int) string {
		return func(e *sweepEnv, _ int) string { return prefix + e.pod.otherE + suffix }
	}
	labelled := func(r sweepRow, label string) sweepRow { r.Label = label; return r }
	api := func(r sweepRow) sweepRow { r.Anon = http.StatusUnauthorized; return r }
	showLoc := func(e *sweepEnv) string { return "/podcasts/shows/" + e.pod.show }
	epLoc := func(e *sweepEnv) string { return "/podcasts/episodes/" + e.pod.ep }
	withLoc := func(r sweepRow, f func(*sweepEnv) string) sweepRow { r.LocOf = f; return r }
	form := func(kv ...string) func(*sweepEnv, int) url.Values {
		return func(*sweepEnv, int) url.Values {
			v := url.Values{}
			for i := 0; i < len(kv); i += 2 {
				v.Set(kv[i], kv[i+1])
			}
			return v
		}
	}
	progressForm := func(*sweepEnv, int) url.Values {
		return url.Values{"position_ms": {"4000"}, "client_updated_at": {fmt.Sprint(time.Now().UnixMilli())}, "device_id": {"device-sweep"}}
	}
	notAllowed := [4]int{http.StatusMethodNotAllowed, 0, 0, 0}
	staff415 := sweepNotGuest(http.StatusUnsupportedMediaType, nf)

	rows := []sweepRow{
		sweepGet("GET /podcasts", sweepStatic("/podcasts"), notGuest),
		sweepGet("GET /podcasts/search", sweepStatic("/podcasts/search"), notGuest),
		sweepGet("GET /podcasts/shows/{showID}", show("/podcasts/shows/", ""), notGuest),
		labelled(sweepGet("GET /podcasts/shows/{showID}", otherShow("/podcasts/shows/", ""), ownerOnly), "someone else's show"),
		sweepGet("GET /hx/podcasts/shows/{showID}/status", show("/hx/podcasts/shows/", "/status"), notGuest),
		labelled(sweepGet("GET /hx/podcasts/shows/{showID}/status", otherShow("/hx/podcasts/shows/", "/status"), ownerOnly), "someone else's show"),
		// A search picture's key is made when a search answers and belongs to one person: any other key is unknown.
		sweepGet("GET /podcasts/search/art/{key}", sweepStatic("/podcasts/search/art/0123456789abcdef0123456789abcdef"), [4]int{nf, nf, nf, nf}),
		labelled(sweepPost("/podcasts/search/art/{key}", sweepStatic("/podcasts/search/art/0123456789abcdef0123456789abcdef"), nil, notAllowed, ""), "POST"),
		sweepGet("GET /podcasts/episodes/{episodeID}", ep("/podcasts/episodes/", ""), notGuest),
		labelled(sweepGet("GET /podcasts/episodes/{episodeID}", otherEp("/podcasts/episodes/", ""), ownerOnly), "someone else's episode"),
		sweepGet("GET /podcasts/audio/{episodeID}", ep("/podcasts/audio/", ""), notGuest),
		labelled(sweepGet("GET /podcasts/audio/{episodeID}", otherEp("/podcasts/audio/", ""), ownerOnly), "someone else's episode"),
		labelled(sweepPost("/podcasts/audio/{episodeID}", ep("/podcasts/audio/", ""), nil, notAllowed, ""), "POST"),
		sweepGet("GET /podcasts/art/{showID}", show("/podcasts/art/", ""), notGuest),
		labelled(sweepGet("GET /podcasts/art/{showID}", otherShow("/podcasts/art/", ""), ownerOnly), "someone else's show"),
		labelled(sweepPost("/podcasts/art/{showID}", show("/podcasts/art/", ""), nil, notAllowed, ""), "POST"),
		sweepGet("GET /podcasts/chapters/{episodeID}", ep("/podcasts/chapters/", ""), notGuest),
		labelled(sweepGet("GET /podcasts/chapters/{episodeID}", otherEp("/podcasts/chapters/", ""), ownerOnly), "someone else's episode"),
		labelled(sweepPost("/podcasts/chapters/{episodeID}", ep("/podcasts/chapters/", ""), nil, notAllowed, ""), "POST"),
		sweepGet("GET /hx/player/episode/{episodeID}", ep("/hx/player/episode/", ""), sweepNotGuest(http.StatusOK, nf)),
		labelled(sweepGet("GET /hx/player/episode/{episodeID}", otherEp("/hx/player/episode/", ""), [4]int{http.StatusOK, nf, nf, nf}), "someone else's episode"),
		sweepGet("GET /hx/player/episode-panel/{episodeID}", ep("/hx/player/episode-panel/", ""), sweepNotGuest(http.StatusOK, nf)),
		labelled(sweepGet("GET /hx/player/episode-panel/{episodeID}", otherEp("/hx/player/episode-panel/", ""), [4]int{http.StatusOK, nf, nf, nf}), "someone else's episode"),
		sweepGet("GET /podcasts/opml", sweepStatic("/podcasts/opml"), notGuest),
		sweepGet("GET /admin/podcasts", sweepStatic("/admin/podcasts"), sweepOwnerOnly(ok, nf)),

		withLoc(sweepPost("POST /podcasts/follow", sweepStatic("/podcasts/follow"), nil, sweepNotGuest(see, nf), ""), showLoc),
		withLoc(sweepPost("POST /podcasts/open", sweepStatic("/podcasts/open"), form("slug", "found-show"), sweepNotGuest(see, nf), ""), showLoc),
		sweepPost("POST /podcasts/shows/{showID}/unfollow", show("/podcasts/shows/", "/unfollow"), nil, sweepNotGuest(see, nf), "/podcasts"),
		withLoc(sweepPost("POST /podcasts/shows/{showID}/refresh", show("/podcasts/shows/", "/refresh"), nil, sweepNotGuest(see, nf), ""), showLoc),
		labelled(sweepPost("POST /podcasts/shows/{showID}/refresh", otherShow("/podcasts/shows/", "/refresh"), nil, [4]int{see, nf, nf, nf}, ""), "someone else's show"),
		withLoc(sweepPost("POST /podcasts/shows/{showID}/seen", show("/podcasts/shows/", "/seen"), nil, sweepNotGuest(see, nf), ""), showLoc),
		withLoc(sweepPost("POST /podcasts/shows/{showID}/settings", show("/podcasts/shows/", "/settings"), form("speed", "1.5"), sweepNotGuest(see, nf), ""), showLoc),
		labelled(sweepPost("POST /podcasts/shows/{showID}/settings", otherShow("/podcasts/shows/", "/settings"), form("speed", "1.5"), [4]int{see, nf, nf, nf}, ""), "someone else's show"),
		withLoc(sweepPost("POST /podcasts/episodes/{episodeID}/played", ep("/podcasts/episodes/", "/played"), form("played", "1"), sweepNotGuest(see, nf), ""), epLoc),
		labelled(sweepPost("POST /podcasts/episodes/{episodeID}/played", otherEp("/podcasts/episodes/", "/played"), form("played", "1"), [4]int{see, nf, nf, nf}, ""), "someone else's episode"),
		withLoc(sweepPost("POST /podcasts/episodes/{episodeID}/favourite", ep("/podcasts/episodes/", "/favourite"), form("on", "1"), sweepNotGuest(see, nf), ""), epLoc),
		labelled(sweepPost("POST /podcasts/episodes/{episodeID}/favourite", otherEp("/podcasts/episodes/", "/favourite"), form("on", "1"), [4]int{see, nf, nf, nf}, ""), "someone else's episode"),
		sweepPost("POST /podcasts/progress/{episodeID}", ep("/podcasts/progress/", ""), progressForm, sweepNotGuest(ok, nf), ""),
		labelled(sweepPost("POST /podcasts/progress/{episodeID}", otherEp("/podcasts/progress/", ""), progressForm, ownerOnly, ""), "someone else's episode"),
		sweepPost("POST /podcasts/opml", sweepStatic("/podcasts/opml"), nil, sweepNotGuest(http.StatusUnprocessableEntity, nf), ""),
		sweepPost("POST /admin/podcasts", sweepStatic("/admin/podcasts"), form("refresh", "6h"), sweepOwnerOnly(see, nf), "/admin/podcasts?saved=1"),
		sweepPost("POST /podcasts/start", sweepStatic("/podcasts/start"), form("podcasts", "1"), sweepOwnerOnly(see, nf), "/podcasts"),
	}
	// The follow row names the fake publisher's address, which the seeded app knows.
	for i := range rows {
		if rows[i].Pattern == "POST /podcasts/follow" {
			rows[i].Form = func(e *sweepEnv, _ int) url.Values { return url.Values{"feed_url": {e.pod.fx.feedURL()}} }
		}
	}

	// The app's JSON API: a session works for GET, so the same people are tried; anonymous is 401.
	apiRows := []sweepRow{
		api(sweepGet("GET /api/v1/podcasts", sweepStatic("/api/v1/podcasts"), notGuest)),
		api(sweepGet("GET /api/v1/podcasts/new", sweepStatic("/api/v1/podcasts/new"), notGuest)),
		api(sweepGet("GET /api/v1/podcasts/shows/{showID}/episodes", show("/api/v1/podcasts/shows/", "/episodes"), notGuest)),
		api(labelled(sweepGet("GET /api/v1/podcasts/shows/{showID}/episodes", otherShow("/api/v1/podcasts/shows/", "/episodes"), ownerOnly), "someone else's show")),
		api(sweepGet("GET /api/v1/podcasts/episodes/{episodeID}", ep("/api/v1/podcasts/episodes/", ""), notGuest)),
		api(labelled(sweepGet("GET /api/v1/podcasts/episodes/{episodeID}", otherEp("/api/v1/podcasts/episodes/", ""), ownerOnly), "someone else's episode")),
		api(sweepGet("GET /api/v1/podcasts/audio/{episodeID}", ep("/api/v1/podcasts/audio/", ""), notGuest)),
		api(labelled(sweepGet("GET /api/v1/podcasts/audio/{episodeID}", otherEp("/api/v1/podcasts/audio/", ""), ownerOnly), "someone else's episode")),
		api(sweepGet("GET /api/v1/podcasts/art/{showID}", show("/api/v1/podcasts/art/", ""), notGuest)),
		api(sweepGet("GET /api/v1/podcasts/chapters/{episodeID}", ep("/api/v1/podcasts/chapters/", ""), notGuest)),
		api(sweepPost("POST /api/v1/podcasts/progress/batch", sweepStatic("/api/v1/podcasts/progress/batch"), nil, staff415, "")),
		api(sweepPost("POST /api/v1/podcasts/episodes/{episodeID}/played", ep("/api/v1/podcasts/episodes/", "/played"), nil, staff415, "")),
		api(labelled(sweepPost("POST /api/v1/podcasts/episodes/{episodeID}/played", otherEp("/api/v1/podcasts/episodes/", "/played"), nil,
			[4]int{http.StatusUnsupportedMediaType, nf, nf, nf}, ""), "someone else's episode")),
		api(sweepPost("POST /api/v1/podcasts/episodes/{episodeID}/favourite", ep("/api/v1/podcasts/episodes/", "/favourite"), nil, staff415, "")),
	}
	return append(rows, apiRows...)
}
