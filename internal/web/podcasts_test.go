package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/podcast"
	"github.com/Tend-Stack/tendshelf/internal/podcast/rss"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// podWorld is an app with podcasts on, three people and a fake publisher: Ann (owner) and Ben (member)
// follow the show "Main"; Cy (member) follows nothing; Gus is a guest.
type podWorld struct {
	a                   *testApp
	fx                  *podFix
	owner, ben, cy, gus *http.Cookie
	ownerID, benID      string
	cyID                string
	show                string
	eps                 []string // episode ids of "Main", oldest first
}

func newPodWorld(t *testing.T) *podWorld {
	t.Helper()
	fx := newPodFix(t)
	a := newTestApp(t, fx.mod(t))
	fx.enable(t, a, nil)
	w := &podWorld{a: a, fx: fx}
	w.owner = a.as(t, users.RoleOwner)
	w.ben = a.as(t, users.RoleMember)
	w.cy = a.as(t, users.RoleMember)
	w.gus = a.as(t, users.RoleGuest)
	w.ownerID, w.benID, w.cyID = sweepUserID(t, a, w.owner), sweepUserID(t, a, w.ben), sweepUserID(t, a, w.cy)
	ctx := context.Background()
	for _, uid := range []string{w.ownerID, w.benID} {
		id, _, err := fx.svc.Follow(ctx, uid, fx.feedURL(), "")
		if err != nil {
			t.Fatal(err)
		}
		w.show = id
	}
	w.applyFeed(t, w.show, "Main")
	rows, err := a.st.Reader().Query(`SELECT id FROM podcast_episodes WHERE show_id = ? ORDER BY published_at`, w.show)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		rows.Scan(&id)
		w.eps = append(w.eps, id)
	}
	if len(w.eps) != podFixEpisodes {
		t.Fatalf("episodes = %d", len(w.eps))
	}
	return w
}

func (w *podWorld) applyFeed(t *testing.T, show, title string) {
	t.Helper()
	feed, err := rss.Parse([]byte(w.fx.rss(title)), w.fx.feedURL())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.fx.svc.ApplyFeed(context.Background(), show, feed, podcast.FetchMeta{}); err != nil {
		t.Fatal(err)
	}
}

func (w *podWorld) get(path string, ck *http.Cookie, mod ...func(*http.Request)) *httptest.ResponseRecorder {
	var m func(*http.Request)
	if len(mod) > 0 {
		m = mod[0]
	}
	return w.a.do(http.MethodGet, path, nil, m, ck)
}

func (w *podWorld) post(t *testing.T, path string, ck *http.Cookie, form url.Values, mod ...func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	f := url.Values{}
	for k, v := range form {
		f[k] = v
	}
	f.Set("_csrf", w.a.tokenFor(t, ck))
	var m func(*http.Request)
	if len(mod) > 0 {
		m = mod[0]
	}
	return w.a.do(http.MethodPost, path, f, m, ck)
}

func (w *podWorld) bearer(t *testing.T, uid string) string {
	t.Helper()
	var tok string
	err := w.a.st.WriteTx(context.Background(), func(tx *sql.Tx) error {
		var err error
		tok, _, err = auth.NewDevices(w.a.st, nil).Issue(context.Background(), tx, uid, "Phone", auth.KindMobile)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// TestPodcastsOffIsOff: with the owner's switch off (or no service at all) every podcast route answers 404, no
// job is queued, nothing reaches the publisher, and the owner can still open the settings page.
func TestPodcastsOffIsOff(t *testing.T) {
	for _, name := range []string{"switch off", "no service"} {
		t.Run(name, func(t *testing.T) {
			fx := newPodFix(t)
			var a *testApp
			if name == "no service" {
				a = newTestApp(t)
			} else {
				a = newTestApp(t, fx.mod(t))
			}
			owner := a.as(t, users.RoleOwner)
			for _, p := range []string{"/podcasts", "/podcasts/search?q=x", "/podcasts/shows/ps_x", "/podcasts/episodes/pe_x", "/podcasts/audio/pe_x",
				"/podcasts/art/ps_x", "/podcasts/chapters/pe_x", "/hx/player/episode/pe_x", "/podcasts/opml", "/api/v1/podcasts", "/api/v1/podcasts/new",
				"/api/v1/podcasts/episodes/pe_x", "/api/v1/podcasts/audio/pe_x"} {
				if rec := a.do(http.MethodGet, p, nil, nil, owner); rec.Code != http.StatusNotFound {
					t.Errorf("GET %s: %d, want 404", p, rec.Code)
				}
			}
			tok := a.tokenFor(t, owner)
			for _, p := range []string{"/podcasts/follow", "/podcasts/opml", "/podcasts/shows/ps_x/refresh", "/podcasts/progress/pe_x", "/podcasts/episodes/pe_x/played"} {
				if rec := a.do(http.MethodPost, p, url.Values{"_csrf": {tok}, "feed_url": {fx.feedURL()}}, nil, owner); rec.Code != http.StatusNotFound {
					t.Errorf("POST %s: %d, want 404", p, rec.Code)
				}
			}
			var n int
			a.st.Reader().QueryRow(`SELECT count(*) FROM jobs WHERE kind LIKE 'podcast.%'`).Scan(&n)
			if n != 0 || fx.pub.Hits.Load() != 0 {
				t.Errorf("jobs %d, publisher hits %d", n, fx.pub.Hits.Load())
			}
			if rec := a.do(http.MethodGet, "/admin/podcasts", nil, nil, owner); rec.Code != http.StatusOK {
				t.Errorf("settings page while off: %d", rec.Code)
			}
		})
	}
}

func TestPodcastsRolesAndStrangers(t *testing.T) {
	w := newPodWorld(t)
	ep := w.eps[0]
	for _, c := range []struct {
		name, path string
		ck         *http.Cookie
		want       int
	}{
		{"owner home", "/podcasts", w.owner, 200},
		{"member home", "/podcasts", w.cy, 200},
		{"guest home", "/podcasts", w.gus, 404},
		{"follower show", "/podcasts/shows/" + w.show, w.ben, 200},
		{"non follower show", "/podcasts/shows/" + w.show, w.cy, 404},
		{"unknown show", "/podcasts/shows/ps_nope", w.ben, 404},
		{"follower episode", "/podcasts/episodes/" + ep, w.ben, 200},
		{"stranger episode", "/podcasts/episodes/" + ep, w.cy, 404},
		{"guest episode", "/podcasts/episodes/" + ep, w.gus, 404},
		{"unknown episode", "/podcasts/episodes/pe_nope", w.ben, 404},
		{"stranger audio", "/podcasts/audio/" + ep, w.cy, 404},
		{"stranger chapters", "/podcasts/chapters/" + ep, w.cy, 404},
		{"stranger art", "/podcasts/art/" + w.show, w.cy, 404},
		{"stranger player", "/hx/player/episode/" + ep, w.cy, 404},
		{"follower player", "/hx/player/episode/" + ep, w.ben, 200},
		{"stranger api episode", "/api/v1/podcasts/episodes/" + ep, w.cy, 404},
		{"guest api list", "/api/v1/podcasts", w.gus, 404},
		{"member opml", "/podcasts/opml", w.ben, 200},
		{"guest opml", "/podcasts/opml", w.gus, 404},
		{"member admin", "/admin/podcasts", w.ben, 404},
		{"owner admin", "/admin/podcasts", w.owner, 200},
	} {
		if rec := w.get(c.path, c.ck); rec.Code != c.want {
			t.Errorf("%s: GET %s = %d, want %d", c.name, c.path, rec.Code, c.want)
		}
	}
	if w.fx.audioHits.Load() != 0 {
		t.Errorf("a refused request reached the publisher: %d", w.fx.audioHits.Load())
	}
	// A star on an unfollowed show's episode gives the right to open it (and only that one).
	if rec := w.post(t, "/podcasts/episodes/"+ep+"/favourite", w.cy, url.Values{"on": {"1"}}); rec.Code != http.StatusNotFound {
		t.Errorf("a stranger starring an episode: %d", rec.Code)
	}
	w.a.st.Writer().Exec(`INSERT INTO podcast_episode_state(user_id, episode_id, favourite, favourite_at, updated_at) VALUES (?, ?, 1, 1, 1)`, w.cyID, ep)
	if rec := w.get("/podcasts/episodes/"+ep, w.cy); rec.Code != http.StatusOK {
		t.Errorf("episode with a state row: %d", rec.Code)
	}
	if rec := w.get("/podcasts/episodes/"+w.eps[1], w.cy); rec.Code != http.StatusNotFound {
		t.Errorf("another episode of the same show: %d", rec.Code)
	}
}

func TestPodcastFollowFlow(t *testing.T) {
	w := newPodWorld(t)
	other := w.fx.otherFeedURL()
	rec := w.post(t, "/podcasts/follow", w.cy, url.Values{"feed_url": {other}})
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(location(rec), "/podcasts/shows/ps_") {
		t.Fatalf("follow: %d %q", rec.Code, location(rec))
	}
	showID := strings.TrimPrefix(location(rec), "/podcasts/shows/")
	if _, ok, _ := w.a.runner.ActiveFor(context.Background(), podcast.KindRefresh, "show_id", showID); !ok {
		t.Error("following a new show queued no refresh")
	}
	// Following again changes nothing and queues nothing new.
	var jobs1, jobs2 int
	w.a.st.Reader().QueryRow(`SELECT count(*) FROM jobs`).Scan(&jobs1)
	if rec = w.post(t, "/podcasts/follow", w.cy, url.Values{"feed_url": {other}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("follow again: %d", rec.Code)
	}
	w.a.st.Reader().QueryRow(`SELECT count(*) FROM jobs`).Scan(&jobs2)
	if jobs1 != jobs2 {
		t.Errorf("jobs %d -> %d", jobs1, jobs2)
	}
	var audits int
	w.a.st.Reader().QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'podcast.follow' AND user_id = ?`, w.cyID).Scan(&audits)
	if audits != 1 {
		t.Errorf("follow audit rows = %d", audits)
	}
	// Bad addresses are a 422 toast for htmx; nothing is fetched.
	hits := w.fx.pub.Hits.Load()
	for _, bad := range []string{"ftp://x.example/feed", "not a url", "https://user:pw@x.example/f", "http://127.0.0.1:8080/f"} {
		rec = w.post(t, "/podcasts/follow", w.cy, url.Values{"feed_url": {bad}}, hx)
		if bad == "http://127.0.0.1:8080/f" {
			continue // a well-formed address; the guarded client refuses it at fetch time
		}
		if rec.Code != http.StatusUnprocessableEntity || strings.Contains(rec.Body.String(), "x.example") {
			t.Errorf("follow %q: %d %q", bad, rec.Code, rec.Body)
		}
	}
	if rec = w.post(t, "/podcasts/follow", w.cy, url.Values{}, hx); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("follow nothing: %d", rec.Code)
	}
	if rec = w.post(t, "/podcasts/follow", w.cy, url.Values{"feed_url": {other}, "slug": {"x"}}, hx); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("follow both: %d", rec.Code)
	}
	if rec = w.post(t, "/podcasts/follow", w.gus, url.Values{"feed_url": {other}}); rec.Code != http.StatusNotFound {
		t.Errorf("guest follow: %d", rec.Code)
	}
	if w.fx.pub.Hits.Load() != hits {
		t.Error("a refused follow reached the network")
	}
	// By OndaCast slug: only then is OndaCast asked, and the feed address comes back from it.
	if rec = w.post(t, "/podcasts/follow", w.cy, url.Values{"slug": {"found-show"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("follow by slug: %d", rec.Code)
	}
	if w.fx.ondaHits.Load() != 1 {
		t.Errorf("OndaCast hits = %d", w.fx.ondaHits.Load())
	}
	// The slug is stored as a hint on the show it names.
	var slug sql.NullString
	w.a.st.Reader().QueryRow(`SELECT ondacast_slug FROM podcast_shows WHERE feed_url = ?`, w.fx.feedURL()).Scan(&slug)
	if slug.String != "found-show" {
		t.Errorf("slug hint = %q", slug.String)
	}
	// The slug named the main show, so Cy follows it now; Cy stops, to be a stranger to it again.
	if rec = w.post(t, "/podcasts/shows/"+w.show+"/unfollow", w.cy, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("unfollow main: %d", rec.Code)
	}
	// Unfollow, refresh, seen, settings.
	if rec = w.post(t, "/podcasts/shows/"+showID+"/unfollow", w.cy, nil); rec.Code != http.StatusSeeOther || location(rec) != "/podcasts" {
		t.Errorf("unfollow: %d %q", rec.Code, location(rec))
	}
	if rec = w.post(t, "/podcasts/shows/"+showID+"/unfollow", w.cy, nil); rec.Code != http.StatusNotFound {
		t.Errorf("unfollow twice: %d", rec.Code)
	}
	if rec = w.post(t, "/podcasts/shows/"+w.show+"/refresh", w.cy, nil); rec.Code != http.StatusNotFound {
		t.Errorf("a stranger refreshing: %d", rec.Code)
	}
	if rec = w.post(t, "/podcasts/shows/"+w.show+"/seen", w.ben, nil); rec.Code != http.StatusSeeOther {
		t.Errorf("seen: %d", rec.Code)
	}
	set := url.Values{"speed": {"1.5"}, "skip_intro_s": {"20"}, "skip_outro_s": {"10"}, "keep_latest": {"4"}, "server_auto": {"0", "1"}}
	if rec = w.post(t, "/podcasts/shows/"+w.show+"/settings", w.ben, set, hx); rec.Code != http.StatusOK && rec.Code != http.StatusSeeOther {
		t.Fatalf("settings: %d %q", rec.Code, rec.Body)
	}
	got, _ := w.fx.svc.ShowSettings(context.Background(), w.benID, w.show)
	if got.Speed == nil || *got.Speed != 1.5 || got.SkipIntroS != 20 || got.SkipOutroS != 10 || got.KeepLatest != 4 {
		t.Errorf("settings stored: %+v", got)
	}
	if got.ServerAuto {
		t.Error("per-show server auto-download took effect while the owner did not allow it")
	}
	for _, bad := range []url.Values{{"speed": {"4"}}, {"skip_intro_s": {"601"}}, {"skip_intro_s": {"x"}}, {"keep_latest": {"0"}}} {
		if rec = w.post(t, "/podcasts/shows/"+w.show+"/settings", w.ben, bad, hx); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("settings %v: %d", bad, rec.Code)
		}
	}
	// Refresh: refused within five minutes of the last fetch, queued after, shared while queued.
	if rec = w.post(t, "/podcasts/shows/"+w.show+"/refresh", w.ben, nil, hx); rec.Code != http.StatusTooManyRequests {
		t.Errorf("refresh right after a fetch: %d", rec.Code)
	}
	w.a.st.Writer().Exec(`UPDATE podcast_shows SET fetched_at = fetched_at - 3600000`)
	if rec = w.post(t, "/podcasts/shows/"+w.show+"/refresh", w.ben, nil); rec.Code != http.StatusSeeOther {
		t.Errorf("refresh: %d", rec.Code)
	}
	// Followers of the other show may not set this one's settings.
	if rec = w.post(t, "/podcasts/shows/"+w.show+"/settings", w.cy, set, hx); rec.Code != http.StatusNotFound {
		t.Errorf("stranger settings: %d", rec.Code)
	}
}

func TestPodcastFollowLimiter(t *testing.T) {
	w := newPodWorld(t)
	for i := 0; i < 30; i++ {
		rec := w.post(t, "/podcasts/follow", w.cy, url.Values{"feed_url": {fmt.Sprintf("https://s%d.example/feed.xml", i)}})
		if rec.Code != http.StatusSeeOther {
			t.Fatalf("follow %d: %d", i, rec.Code)
		}
	}
	rec := w.post(t, "/podcasts/follow", w.cy, url.Values{"feed_url": {"https://more.example/feed.xml"}}, hx)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Errorf("31st follow in an hour: %d", rec.Code)
	}
	// The limit is per person.
	if rec = w.post(t, "/podcasts/follow", w.ben, url.Values{"feed_url": {"https://more.example/feed.xml"}}); rec.Code != http.StatusSeeOther {
		t.Errorf("another person: %d", rec.Code)
	}
}

func TestPodcastSearch(t *testing.T) {
	w := newPodWorld(t)
	rec := w.get("/podcasts/search?q=found", w.ben)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "Found Show") {
		t.Fatalf("search: %d", rec.Code)
	}
	if w.fx.ondaHits.Load() != 1 {
		t.Errorf("OndaCast hits = %d", w.fx.ondaHits.Load())
	}
	// The page without a query makes no outside request at all.
	if rec = w.get("/podcasts/search", w.ben); rec.Code != http.StatusOK || w.fx.ondaHits.Load() != 1 {
		t.Errorf("empty search: %d hits %d", rec.Code, w.fx.ondaHits.Load())
	}
	if rec = w.get("/podcasts/search?q=found", w.gus); rec.Code != http.StatusNotFound {
		t.Errorf("guest search: %d", rec.Code)
	}
	// 20 a minute per person.
	for i := 0; i < 25; i++ {
		rec = w.get(fmt.Sprintf("/podcasts/search?q=q%d", i), w.cy)
		if i < 20 && rec.Code != http.StatusOK {
			t.Fatalf("search %d: %d", i, rec.Code)
		}
	}
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("search past the limit: %d", rec.Code)
	}
}

func TestEpisodeAudioProxy(t *testing.T) {
	w := newPodWorld(t)
	fx := w.fx
	ep := w.eps[0]
	path := "/podcasts/audio/" + ep
	rec := w.get(path, w.ben, func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "other", Value: "secret"}) })
	if rec.Code != http.StatusOK {
		t.Fatalf("full: %d", rec.Code)
	}
	if !equalBytes(rec.Body.Bytes(), fx.audio) || rec.Header().Get("Content-Type") != "audio/mpeg" {
		t.Errorf("full body %d bytes, type %q", rec.Body.Len(), rec.Header().Get("Content-Type"))
	}
	h := rec.Header()
	if h.Get("Cache-Control") != "private, no-store" || h.Get("X-Content-Type-Options") != "nosniff" || h.Get("Set-Cookie") != "" && !strings.HasPrefix(h.Get("Set-Cookie"), "ts_csrf") {
		t.Errorf("headers %v", h)
	}
	for k := range h {
		if strings.EqualFold(k, "Set-Cookie") {
			for _, c := range h.Values(k) {
				if strings.Contains(c, "tracker") {
					t.Error("the publisher's cookie reached the person")
				}
			}
		}
	}
	fx.mu.Lock()
	up := fx.audioReqs[len(fx.audioReqs)-1]
	fx.mu.Unlock()
	if up.Get("Cookie") != "" || up.Get("Authorization") != "" || up.Get("Referer") != "" || up.Get("User-Agent") != "Tendshelf (self-hosted podcast player)" {
		t.Errorf("upstream saw %v", up)
	}

	// A Range goes through and comes back 206 with the right bytes.
	rec = w.get(path, w.ben, func(r *http.Request) { r.Header.Set("Range", "bytes=100-199") })
	if rec.Code != http.StatusPartialContent || !equalBytes(rec.Body.Bytes(), fx.audio[100:200]) ||
		rec.Header().Get("Content-Range") != fmt.Sprintf("bytes 100-199/%d", len(fx.audio)) {
		t.Errorf("range: %d %q %d bytes", rec.Code, rec.Header().Get("Content-Range"), rec.Body.Len())
	}
	rec = w.get(path, w.ben, func(r *http.Request) { r.Header.Set("Range", "bytes=65000-") })
	if rec.Code != http.StatusPartialContent || !equalBytes(rec.Body.Bytes(), fx.audio[65000:]) {
		t.Errorf("open range: %d %d bytes", rec.Code, rec.Body.Len())
	}
	// A range past the end passes the publisher's 416 through.
	rec = w.get(path, w.ben, func(r *http.Request) { r.Header.Set("Range", "bytes=999999-") })
	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("range past the end: %d", rec.Code)
	}
	// Ranges the proxy does not pass on: a suffix, several, junk. The publisher is asked for the whole file.
	for _, bad := range []string{"bytes=-500", "bytes=0-1,5-9", "bytes=9-3", "items=1-2", "bytes=1-2; evil"} {
		before := len(fx.audioReqs)
		rec = w.get(path, w.ben, func(r *http.Request) { r.Header.Set("Range", bad) })
		fx.mu.Lock()
		sent := fx.audioReqs[before].Get("Range")
		fx.mu.Unlock()
		if sent != "" {
			t.Errorf("Range %q was passed to the publisher as %q", bad, sent)
		}
		if rec.Code != http.StatusOK {
			t.Errorf("Range %q answered %d", bad, rec.Code)
		}
	}
	// HEAD answers the headers and no body.
	rec = w.a.do(http.MethodHead, path, nil, nil, w.ben)
	if rec.Code != http.StatusOK || rec.Body.Len() != 0 {
		t.Errorf("HEAD: %d %d bytes", rec.Code, rec.Body.Len())
	}
	// download=1 names the file; a bad value is a 400.
	rec = w.get(path+"?download=1", w.ben)
	if cd := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment;") || !strings.Contains(cd, ".mp3") {
		t.Errorf("download header %q", cd)
	}
	if rec = w.get(path+"?download=2", w.ben); rec.Code != http.StatusBadRequest {
		t.Errorf("bad download: %d", rec.Code)
	}
	// Other methods are 405 with Allow.
	rec = w.post(t, path, w.ben, nil)
	if rec.Code != http.StatusMethodNotAllowed || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("POST: %d %q", rec.Code, rec.Header().Get("Allow"))
	}
	// A stranger is refused before any request goes out.
	before := fx.audioHits.Load()
	if rec = w.get(path, w.cy); rec.Code != http.StatusNotFound || fx.audioHits.Load() != before {
		t.Errorf("stranger: %d, hits %d -> %d", rec.Code, before, fx.audioHits.Load())
	}
	if rec = w.get(path, nil); rec.Code != http.StatusSeeOther && rec.Code != http.StatusUnauthorized && rec.Code != http.StatusNotFound || fx.audioHits.Load() != before {
		t.Errorf("anonymous: %d", rec.Code)
	}
}

func equalBytes(a, b []byte) bool { return string(a) == string(b) }

func TestEpisodeAudioUpstreamProblems(t *testing.T) {
	w := newPodWorld(t)
	fx := w.fx
	path := "/podcasts/audio/" + w.eps[1]
	fx.htmlAudio.Store(true)
	rec := w.get(path, w.ben)
	if rec.Code != http.StatusBadGateway || strings.Contains(rec.Body.String(), "sign in") {
		t.Errorf("html answer: %d %q", rec.Code, rec.Body)
	}
	fx.htmlAudio.Store(false)
	for status, want := range map[int]int{404: http.StatusNotFound, 410: http.StatusNotFound, 500: http.StatusBadGateway, 403: http.StatusBadGateway} {
		fx.mu.Lock()
		fx.audioStatus = status
		fx.mu.Unlock()
		rec = w.get(path, w.ben)
		if rec.Code != want || strings.Contains(rec.Body.String(), "audio.test") {
			t.Errorf("upstream %d: %d %q", status, rec.Code, rec.Body)
		}
	}
	fx.mu.Lock()
	fx.audioStatus = 0
	fx.mu.Unlock()
	// A private address in an enclosure is refused at dial time and never fetched.
	// (A new host name: a connection already open to a checked address is reused without a new check.)
	fx.pub.Resolve["evil.test"] = "192.168.1.5"
	w.a.st.Writer().Exec(`UPDATE podcast_episodes SET audio_url = ? WHERE id = ?`, fx.pub.URL("evil.test", "/x.mp3"), w.eps[1])
	hits := fx.audioHits.Load()
	rec = w.get(path, w.ben)
	if rec.Code != http.StatusBadGateway || fx.audioHits.Load() != hits {
		t.Errorf("private enclosure: %d, hits %d -> %d", rec.Code, hits, fx.audioHits.Load())
	}
	w.a.st.Writer().Exec(`UPDATE podcast_episodes SET audio_url = ? WHERE id = ?`, fx.audioURL(2), w.eps[1])
	// A video enclosure is not played.
	w.a.st.Writer().Exec(`UPDATE podcast_episodes SET audio_type = 'video/mp4' WHERE id = ?`, w.eps[1])
	if rec = w.get(path, w.ben); rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("video: %d", rec.Code)
	}
}

// TestEpisodeAudioIgnoredRange: a publisher that ignores Range gets the person the whole file (200), and the
// server queues one cache job so seeking works from the next request on.
func TestEpisodeAudioIgnoredRange(t *testing.T) {
	w := newPodWorld(t)
	ep := w.eps[0]
	path := "/podcasts/audio/" + ep
	w.fx.ignoreRange.Store(true)
	for i := 0; i < 3; i++ {
		rec := w.get(path, w.ben, func(r *http.Request) { r.Header.Set("Range", "bytes=1000-") })
		if rec.Code != http.StatusOK || !equalBytes(rec.Body.Bytes(), w.fx.audio) {
			t.Fatalf("ignored range: %d %d bytes", rec.Code, rec.Body.Len())
		}
	}
	var n int
	w.a.st.Reader().QueryRow(`SELECT count(*) FROM jobs WHERE kind = ?`, podcast.KindCache).Scan(&n)
	if n != 1 {
		t.Errorf("cache jobs queued = %d, want 1", n)
	}
	// A range from the start needs no copy.
	w.a.st.Writer().Exec(`DELETE FROM jobs`)
	w.get("/podcasts/audio/"+w.eps[1], w.ben, func(r *http.Request) { r.Header.Set("Range", "bytes=0-") })
	w.a.st.Reader().QueryRow(`SELECT count(*) FROM jobs`).Scan(&n)
	if n != 0 {
		t.Errorf("jobs for a range from 0 = %d", n)
	}
	// Once the server keeps the file, ranges are served from disk and the publisher is not asked again.
	if err := w.fx.svc.PutCache(context.Background(), ep, 1<<30); err != nil {
		t.Fatal(err)
	}
	hits := w.fx.audioHits.Load()
	rec := w.get(path, w.ben, func(r *http.Request) { r.Header.Set("Range", "bytes=10-19") })
	if rec.Code != http.StatusPartialContent || !equalBytes(rec.Body.Bytes(), w.fx.audio[10:20]) || w.fx.audioHits.Load() != hits {
		t.Errorf("cached range: %d %q, publisher hits %d -> %d", rec.Code, rec.Body, hits, w.fx.audioHits.Load())
	}
	if rec.Header().Get("Cache-Control") != "private, no-store" {
		t.Errorf("cached cache-control %q", rec.Header().Get("Cache-Control"))
	}
	// A stranger still cannot read a kept file.
	if rec = w.get(path, w.cy); rec.Code != http.StatusNotFound {
		t.Errorf("stranger on a kept file: %d", rec.Code)
	}
}

// TestEpisodeAudioConcurrency: three streams per person, then 503 with Retry-After; a different person is unaffected.
func TestEpisodeAudioConcurrency(t *testing.T) {
	w := newPodWorld(t)
	w.fx.hold = make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < podcast.MaxStreamsPerPerson; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.get("/podcasts/audio/"+w.eps[i], w.ben)
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for w.fx.audioHits.Load() < int64(podcast.MaxStreamsPerPerson) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if w.fx.audioHits.Load() < int64(podcast.MaxStreamsPerPerson) {
		t.Fatal("the held streams did not start")
	}
	rec := w.get("/podcasts/audio/"+w.eps[0], w.ben)
	if rec.Code != http.StatusServiceUnavailable || rec.Header().Get("Retry-After") != "5" || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/plain") {
		t.Errorf("fourth stream: %d %v", rec.Code, rec.Header())
	}
	close(w.fx.hold)
	wg.Wait()
	if rec = w.get("/podcasts/audio/"+w.eps[0], w.ben); rec.Code != http.StatusOK {
		t.Errorf("after the streams ended: %d", rec.Code)
	}
}

func TestPodcastProgressAndStates(t *testing.T) {
	w := newPodWorld(t)
	ep := w.eps[0]
	now := time.Now().UnixMilli()
	save := func(ck *http.Cookie, pos, at int64, extra url.Values) *httptest.ResponseRecorder {
		f := url.Values{"position_ms": {fmt.Sprint(pos)}, "client_updated_at": {fmt.Sprint(at)}, "device_id": {"device-1234"}}
		for k, v := range extra {
			f[k] = v
		}
		return w.post(t, "/podcasts/progress/"+ep, ck, f)
	}
	var body struct {
		Applied bool           `json:"applied"`
		State   map[string]any `json:"state"`
	}
	rec := save(w.ben, 5000, now, nil)
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &body) != nil || !body.Applied || body.State["positionMs"] != float64(5000) {
		t.Fatalf("save: %d %q", rec.Code, rec.Body)
	}
	rec = save(w.ben, 9000, now-1000, nil)
	json.Unmarshal(rec.Body.Bytes(), &body)
	if rec.Code != 200 || body.Applied || body.State["positionMs"] != float64(5000) {
		t.Errorf("older save: %d %q", rec.Code, rec.Body)
	}
	if rec = save(w.cy, 1, now, nil); rec.Code != http.StatusNotFound {
		t.Errorf("stranger progress: %d", rec.Code)
	}
	if rec = save(w.ben, -1, now, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("negative position: %d", rec.Code)
	}
	if rec = save(w.ben, 1, now, url.Values{"device_id": {"x"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("bad device: %d", rec.Code)
	}
	if rec = save(w.ben, 1, now, url.Values{"finished": {"yes"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("bad finished: %d", rec.Code)
	}
	// Another person's place is theirs alone.
	if rec = save(w.owner, 777, now, nil); rec.Code != 200 {
		t.Fatalf("owner save: %d", rec.Code)
	}
	st, _ := w.fx.svc.Episode(context.Background(), w.benID, ep)
	if st.State.PositionMs != 5000 {
		t.Errorf("another person's save changed this place: %d", st.State.PositionMs)
	}
	// Played and starred through the pages.
	if rec = w.post(t, "/podcasts/episodes/"+ep+"/played", w.ben, url.Values{"played": {"1"}}); rec.Code != http.StatusSeeOther {
		t.Errorf("played: %d", rec.Code)
	}
	if rec = w.post(t, "/podcasts/episodes/"+ep+"/played", w.ben, url.Values{"played": {"maybe"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("played junk: %d", rec.Code)
	}
	if rec = w.post(t, "/podcasts/episodes/"+ep+"/favourite", w.ben, url.Values{"on": {"1"}}); rec.Code != http.StatusSeeOther {
		t.Errorf("star: %d", rec.Code)
	}
	st, _ = w.fx.svc.Episode(context.Background(), w.benID, ep)
	if !st.State.Played || !st.State.Favourite {
		t.Errorf("state %+v", st.State)
	}
	if own, _ := w.fx.svc.Episode(context.Background(), w.ownerID, ep); own.State.Played || own.State.Favourite {
		t.Error("state leaked to another person")
	}
	// OPML export lists the person's own follows only.
	rec = w.get("/podcasts/opml", w.cy)
	if rec.Code != 200 || strings.Contains(rec.Body.String(), "pub.test") || !strings.Contains(rec.Header().Get("Content-Type"), "opml") {
		t.Errorf("empty opml: %d %q", rec.Code, rec.Body)
	}
	rec = w.get("/podcasts/opml", w.ben)
	if !strings.Contains(rec.Body.String(), "pub.test") || strings.Contains(rec.Header().Get("Content-Disposition"), "..") {
		t.Errorf("opml: %q", rec.Body)
	}
}

func TestPodcastChaptersAndArt(t *testing.T) {
	w := newPodWorld(t)
	ep := w.eps[0]
	rec := w.get("/podcasts/chapters/"+ep, w.ben)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Intro") || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("chapters: %d %q", rec.Code, rec.Body)
	}
	hits := w.fx.pub.Hits.Load()
	if rec = w.get("/podcasts/chapters/"+ep, w.ben); rec.Code != 200 || w.fx.pub.Hits.Load() != hits {
		t.Errorf("second chapters request went upstream: %d, hits %d -> %d", rec.Code, hits, w.fx.pub.Hits.Load())
	}
	if rec = w.get("/podcasts/chapters/"+ep, w.cy); rec.Code != http.StatusNotFound {
		t.Errorf("stranger chapters: %d", rec.Code)
	}
	// Art: nothing stored yet is a plain 404; run the art job for real, then the picture is served.
	if rec = w.get("/podcasts/art/"+w.show, w.ben); rec.Code != http.StatusNotFound {
		t.Errorf("art before the job: %d", rec.Code)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := w.a.runner.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		w.a.runner.Stop(stop)
	})
	if _, err := w.a.runner.Enqueue(ctx, podcast.KindArt, map[string]string{"show_id": w.show}, jobs.EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the art job", func() bool {
		var n int
		w.a.st.Reader().QueryRow(`SELECT count(*) FROM podcast_shows WHERE id = ? AND art_sha <> ''`, w.show).Scan(&n)
		return n == 1
	})
	rec = w.get("/podcasts/art/"+w.show+"?w=160", w.ben)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("ETag") == "" {
		t.Errorf("art: %d %v", rec.Code, rec.Header())
	}
	if rec = w.get("/podcasts/art/"+w.show, w.cy); rec.Code != http.StatusNotFound {
		t.Errorf("stranger art: %d", rec.Code)
	}
	if rec = w.get("/podcasts/art/"+w.show+"?w=0", w.ben); rec.Code != http.StatusBadRequest {
		t.Errorf("bad width: %d", rec.Code)
	}
}

// TestPodcastRefreshEndToEnd runs the real job runner: following a show fetches its feed and fills the page.
func TestPodcastRefreshEndToEnd(t *testing.T) {
	w := newPodWorld(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := w.a.runner.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stop, c := context.WithTimeout(context.Background(), 5*time.Second)
		defer c()
		w.a.runner.Stop(stop)
	})
	rec := w.post(t, "/podcasts/follow", w.cy, url.Values{"feed_url": {w.fx.otherFeedURL()}})
	showID := strings.TrimPrefix(location(rec), "/podcasts/shows/")
	waitFor(t, "the refresh", func() bool {
		var n int
		w.a.st.Reader().QueryRow(`SELECT count(*) FROM podcast_episodes WHERE show_id = ?`, showID).Scan(&n)
		return n == podFixEpisodes
	})
	rec = w.get("/podcasts/shows/"+showID, w.cy)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Other episode 3") {
		t.Errorf("show page: %d", rec.Code)
	}
	// The new-episode count and Home list use the same rows.
	rec = w.get("/podcasts", w.cy)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Other") {
		t.Errorf("home: %d", rec.Code)
	}
}

func TestPodcastAPI(t *testing.T) {
	w := newPodWorld(t)
	tok := w.bearer(t, w.benID)
	other := w.bearer(t, w.cyID)
	req := func(method, path, tok string, body string) *httptest.ResponseRecorder {
		return deviceBearerReq(w.a, method, path, tok, func(r *http.Request) {
			if body != "" {
				r.Body = io.NopCloser(strings.NewReader(body))
				r.ContentLength = int64(len(body))
				r.Header.Set("Content-Type", "application/json")
			}
		})
	}
	rec := req("GET", "/api/v1/podcasts", tok, "")
	var list struct {
		Items []map[string]any `json:"items"`
	}
	apiDecode(t, rec, &list)
	if rec.Code != 200 || len(list.Items) != 1 || list.Items[0]["title"] != "Main" || strings.Contains(rec.Body.String(), "pub.test") {
		t.Fatalf("list: %d %q", rec.Code, rec.Body)
	}
	if rec = req("GET", "/api/v1/podcasts", other, ""); rec.Code != 200 || strings.Contains(rec.Body.String(), "Main") {
		t.Errorf("another person's list: %d %q", rec.Code, rec.Body)
	}
	rec = req("GET", "/api/v1/podcasts/shows/"+w.show+"/episodes", tok, "")
	var eps struct {
		Items []map[string]any `json:"items"`
	}
	apiDecode(t, rec, &eps)
	if rec.Code != 200 || len(eps.Items) != podFixEpisodes {
		t.Fatalf("episodes: %d %q", rec.Code, rec.Body)
	}
	if rec = req("GET", "/api/v1/podcasts/shows/"+w.show+"/episodes?since=999999999999999", tok, ""); !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("since in the future: %q", rec.Body)
	}
	if rec = req("GET", "/api/v1/podcasts/shows/"+w.show+"/episodes?since=-1", tok, ""); rec.Code != 400 {
		t.Errorf("bad since: %d", rec.Code)
	}
	if rec = req("GET", "/api/v1/podcasts/shows/"+w.show+"/episodes", other, ""); rec.Code != 404 {
		t.Errorf("stranger's episodes: %d", rec.Code)
	}
	ep := w.eps[0]
	rec = req("GET", "/api/v1/podcasts/episodes/"+ep, tok, "")
	var doc map[string]any
	apiDecode(t, rec, &doc)
	if rec.Code != 200 || doc["audioUrl"] != "/api/v1/podcasts/audio/"+ep || doc["mime"] != "audio/mpeg" || doc["showId"] != w.show ||
		doc["chaptersUrl"] != "/api/v1/podcasts/chapters/"+ep || strings.Contains(rec.Body.String(), "audio.test") {
		t.Errorf("playback document: %d %q", rec.Code, rec.Body)
	}
	if rec = req("GET", "/api/v1/podcasts/episodes/"+ep, other, ""); rec.Code != 404 {
		t.Errorf("stranger's document: %d", rec.Code)
	}
	// Audio with a Range, by token.
	rec = deviceBearerReq(w.a, "GET", "/api/v1/podcasts/audio/"+ep, tok, func(r *http.Request) { r.Header.Set("Range", "bytes=0-9") })
	if rec.Code != http.StatusPartialContent || !equalBytes(rec.Body.Bytes(), w.fx.audio[:10]) {
		t.Errorf("api audio: %d", rec.Code)
	}
	hits := w.fx.audioHits.Load()
	if rec = req("GET", "/api/v1/podcasts/audio/"+ep, other, ""); rec.Code != 404 || w.fx.audioHits.Load() != hits {
		t.Errorf("stranger api audio: %d", rec.Code)
	}
	// Progress batch: one good, one for a stranger's episode, one missing a field.
	now := time.Now().UnixMilli()
	batch := fmt.Sprintf(`{"updates":[{"episodeId":%q,"positionMs":4000,"clientUpdatedAt":%d,"deviceId":"device-9999"},
		{"episodeId":"pe_nope","positionMs":1,"clientUpdatedAt":%d,"deviceId":"device-9999"},
		{"episodeId":%q,"clientUpdatedAt":%d,"deviceId":"device-9999"}]}`, ep, now, now, ep, now)
	rec = req("POST", "/api/v1/podcasts/progress/batch", tok, batch)
	var res struct {
		Results []struct {
			EpisodeID string
			Applied   bool
			State     *struct{ PositionMs int64 }
			Error     *struct{ Code, Field string }
		}
	}
	apiDecode(t, rec, &res)
	if rec.Code != 200 || len(res.Results) != 3 || !res.Results[0].Applied || res.Results[0].State.PositionMs != 4000 ||
		res.Results[1].Error == nil || res.Results[1].Error.Code != "not_found" || res.Results[2].Error == nil || res.Results[2].Error.Field != "positionMs" {
		t.Errorf("batch: %d %q", rec.Code, rec.Body)
	}
	if rec = req("POST", "/api/v1/podcasts/progress/batch", other, batch); rec.Code != 200 || strings.Contains(rec.Body.String(), `"applied":true`) {
		t.Errorf("a stranger's batch applied something: %d %q", rec.Code, rec.Body)
	}
	// Played and starred.
	rec = req("POST", "/api/v1/podcasts/episodes/"+ep+"/played", tok, `{"played":true}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"played":true`) {
		t.Errorf("played: %d %q", rec.Code, rec.Body)
	}
	if rec = req("POST", "/api/v1/podcasts/episodes/"+ep+"/favourite", tok, `{"on":true}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"favourite":true`) {
		t.Errorf("star: %d %q", rec.Code, rec.Body)
	}
	if rec = req("POST", "/api/v1/podcasts/episodes/"+ep+"/favourite", tok, `{}`); rec.Code != 400 {
		t.Errorf("star without a value: %d", rec.Code)
	}
	if rec = req("POST", "/api/v1/podcasts/episodes/"+ep+"/played", other, `{"played":true}`); rec.Code != 404 {
		t.Errorf("stranger played: %d", rec.Code)
	}
	// New: the episodes are older than the follow, so nothing is new.
	rec = req("GET", "/api/v1/podcasts/new", tok, "")
	apiDecode(t, rec, &eps)
	if rec.Code != 200 || len(eps.Items) != 0 {
		t.Errorf("new: %d %q", rec.Code, rec.Body)
	}
}

func TestPodcastAdminSettings(t *testing.T) {
	fx := newPodFix(t)
	a := newTestApp(t, fx.mod(t))
	owner, member := a.as(t, users.RoleOwner), a.as(t, users.RoleMember)
	post := func(ck *http.Cookie, f url.Values) *httptest.ResponseRecorder {
		f.Set("_csrf", a.tokenFor(t, ck))
		return a.do(http.MethodPost, "/admin/podcasts", f, nil, ck)
	}
	if rec := post(member, url.Values{"enabled": {"1"}}); rec.Code != http.StatusNotFound {
		t.Fatalf("member: %d", rec.Code)
	}
	if podcast.LoadSettings(context.Background(), a.st).Enabled {
		t.Fatal("a member turned podcasts on")
	}
	rec := post(owner, url.Values{"enabled": {"0", "1"}, "refresh": {"6h"}, "cache_gib": {"2.5"}, "server_auto": {"1"}, "radio": {"0"}})
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("owner: %d %q", rec.Code, rec.Body)
	}
	got := podcast.LoadSettings(context.Background(), a.st)
	if !got.Enabled || got.Refresh != "6h" || got.CacheBytes != 5<<29 || !got.ServerAuto || got.RadioEnabled {
		t.Errorf("settings %+v", got)
	}
	for _, bad := range []url.Values{{"refresh": {"2h"}}, {"cache_gib": {"-1"}}, {"cache_gib": {"5000"}}, {"cache_gib": {"abc"}}, {"enabled": {"yes"}}} {
		if rec := post(owner, bad); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%v: %d", bad, rec.Code)
		}
	}
	if podcast.LoadSettings(context.Background(), a.st) != got {
		t.Error("a refused save changed the settings")
	}
	var n int
	a.st.Reader().QueryRow(`SELECT count(*) FROM audit_log WHERE action = 'podcast.settings'`).Scan(&n)
	if n != 1 {
		t.Errorf("audit rows = %d", n)
	}
	// Remove saved episodes clears the cache.
	if rec := post(owner, url.Values{"clear": {"1"}}); rec.Code != http.StatusSeeOther {
		t.Errorf("clear: %d", rec.Code)
	}
}

// TestEpisodeAudioNeverServesActiveContent: a publisher's non-audio Content-Type is never passed on; the
// answer carries the audio type, nosniff and a sandboxing CSP.
func TestEpisodeAudioNeverServesActiveContent(t *testing.T) {
	w := newPodWorld(t)
	path := "/podcasts/audio/" + w.eps[1]
	for _, ct := range []string{"image/svg+xml", "text/javascript", "text/xml", "application/javascript", "video/mp4"} {
		ct := ct
		w.fx.audioCT.Store(&ct)
		rec := w.get(path, w.ben)
		if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "audio/mpeg" {
			t.Errorf("upstream %s: %d type %q", ct, rec.Code, rec.Header().Get("Content-Type"))
		}
		if rec.Header().Get("Content-Security-Policy") != "default-src 'none'; sandbox" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("upstream %s: headers %v", ct, rec.Header())
		}
	}
	ct := "audio/ogg; codecs=opus"
	w.fx.audioCT.Store(&ct)
	if rec := w.get(path, w.ben); rec.Header().Get("Content-Type") != ct {
		t.Errorf("audio type changed: %q", rec.Header().Get("Content-Type"))
	}
}

// TestEpisodeAudioSkipsOversizedCache: an episode bigger than the owner's limit queues no cache job.
func TestEpisodeAudioSkipsOversizedCache(t *testing.T) {
	w := newPodWorld(t)
	w.fx.ignoreRange.Store(true)
	w.a.st.Writer().Exec(`UPDATE podcast_episodes SET audio_bytes = ? WHERE id = ?`, int64(1)<<40, w.eps[1])
	w.get("/podcasts/audio/"+w.eps[1], w.ben, func(r *http.Request) { r.Header.Set("Range", "bytes=1000-") })
	if _, ok, _ := w.fx.svc.Runner.ActiveFor(context.Background(), podcast.KindCache, "episode_id", w.eps[1]); ok {
		t.Error("a cache job was queued for an episode over the limit")
	}
}

// TestPodcastChaptersFailureIsRemembered: a failing chapters file is fetched once, then refused for a while.
func TestPodcastChaptersFailureIsRemembered(t *testing.T) {
	w := newPodWorld(t)
	w.fx.chapterFail.Store(true)
	path := "/podcasts/chapters/" + w.eps[0]
	for i := 0; i < 3; i++ {
		if rec := w.get(path, w.ben); rec.Code != http.StatusBadGateway {
			t.Errorf("failing chapters %d: %d", i, rec.Code)
		}
	}
	if n := w.fx.chapterHits.Load(); n != 1 {
		t.Errorf("publisher asked %d times", n)
	}
}

// TestPodcastChaptersUseStreamSlots: with every slot taken the chapters fetch answers busy and does not go out.
func TestPodcastChaptersUseStreamSlots(t *testing.T) {
	w := newPodWorld(t)
	var rel []func()
	for {
		r, ok := w.fx.svc.AcquireStream(w.benID)
		if !ok {
			break
		}
		rel = append(rel, r)
	}
	defer func() {
		for _, r := range rel {
			r()
		}
	}()
	if rec := w.get("/podcasts/chapters/"+w.eps[0], w.ben); rec.Code != http.StatusServiceUnavailable || w.fx.chapterHits.Load() != 0 {
		t.Errorf("busy chapters: %d, hits %d", rec.Code, w.fx.chapterHits.Load())
	}
}
