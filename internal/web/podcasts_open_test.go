package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/podcast"
)

// episodesOf lists a show's episode ids, oldest first.
func (w *podWorld) episodesOf(t *testing.T, show string) []string {
	t.Helper()
	rows, err := w.a.st.Reader().Query(`SELECT id FROM podcast_episodes WHERE show_id = ? ORDER BY published_at`, show)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var eps []string
	for rows.Next() {
		var e string
		rows.Scan(&e)
		eps = append(eps, e)
	}
	return eps
}

// openOther has Cy open the "other-show" slug and returns the show id and its episode ids once a feed was applied.
func (w *podWorld) openOther(t *testing.T) (string, []string) {
	t.Helper()
	rec := w.post(t, "/podcasts/open", w.cy, url.Values{"slug": {"other-show"}})
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(location(rec), "/podcasts/shows/ps_") {
		t.Fatalf("open: %d %q", rec.Code, location(rec))
	}
	id := strings.TrimPrefix(location(rec), "/podcasts/shows/")
	w.applyFeed(t, id, "Other")
	eps := w.episodesOf(t, id)
	if len(eps) == 0 {
		t.Fatal("no episodes")
	}
	return id, eps
}

func TestPodcastOpenFlow(t *testing.T) {
	w := newPodWorld(t)
	rec := w.post(t, "/podcasts/open", w.cy, url.Values{"slug": {"other-show"}}, hx)
	if rec.Code != http.StatusOK || rec.Header().Get("HX-Push-Url") == "" {
		t.Fatalf("htmx open: %d push %q", rec.Code, rec.Header().Get("HX-Push-Url"))
	}
	id := strings.TrimPrefix(rec.Header().Get("HX-Push-Url"), "/podcasts/shows/")
	// The first fetch is still running: the poll says wait.
	if rec = w.get("/hx/podcasts/shows/"+id+"/status", w.cy); rec.Code != http.StatusNoContent {
		t.Errorf("status while fetching: %d", rec.Code)
	}
	if _, ok, _ := w.a.runner.ActiveFor(context.Background(), podcast.KindRefresh, "show_id", id); !ok {
		t.Error("opening queued no refresh")
	}
	w.applyFeed(t, id, "Other")
	eps := w.episodesOf(t, id)
	rec = w.get("/podcasts/shows/"+id, w.cy)
	body := rec.Body.String()
	if rec.Code != 200 || !strings.Contains(body, `name="show_id" value="`+id+`"`) || !strings.Contains(body, podNotFollowingHint) {
		t.Fatalf("show page: %d", rec.Code)
	}
	for _, no := range []string{"/unfollow", "/refresh", "/settings", ">Following<"} {
		if strings.Contains(body, no) {
			t.Errorf("a non-follower's page has %q", no)
		}
	}
	if rec = w.get("/hx/podcasts/shows/"+id+"/status", w.cy); rec.Code != 200 {
		t.Errorf("status after: %d", rec.Code)
	}
	ep := eps[0]
	for _, path := range []string{"/podcasts/audio/" + ep, "/podcasts/chapters/" + ep, "/podcasts/episodes/" + ep, "/hx/player/episode/" + ep} {
		if rec = w.get(path, w.cy); rec.Code != 200 {
			t.Errorf("opener GET %s: %d", path, rec.Code)
		}
	}
	prog := url.Values{"position_ms": {"5000"}, "client_updated_at": {fmt.Sprint(time.Now().UnixMilli())}, "device_id": {"device-1234"}}
	if rec = w.post(t, "/podcasts/progress/"+ep, w.cy, prog); rec.Code != 200 {
		t.Errorf("progress: %d", rec.Code)
	}
	if rec = w.post(t, "/queue", w.cy, url.Values{"episode_id": {ep}, "where": {"end"}}); rec.Code != http.StatusSeeOther && rec.Code != 200 {
		t.Errorf("queue add: %d", rec.Code)
	}
	// Art, once the art job ran.
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
	if _, err := w.a.runner.Enqueue(ctx, podcast.KindArt, map[string]string{"show_id": id}, jobs.EnqueueOptions{}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the art job", func() bool {
		var n int
		w.a.st.Reader().QueryRow(`SELECT count(*) FROM podcast_shows WHERE id = ? AND art_sha <> ''`, id).Scan(&n)
		return n == 1
	})
	if rec = w.get("/podcasts/art/"+id+"?w=160", w.cy); rec.Code != 200 {
		t.Errorf("opener art: %d", rec.Code)
	}
	if rec = w.get("/podcasts/art/"+id+"?w=160", w.ben); rec.Code != http.StatusNotFound {
		t.Errorf("other person's art: %d", rec.Code)
	}
	// Reopening a known slug asks OndaCast nothing.
	hits := w.fx.ondaHits.Load()
	if rec = w.post(t, "/podcasts/open", w.cy, url.Values{"slug": {"other-show"}}); rec.Code != http.StatusSeeOther {
		t.Errorf("reopen: %d", rec.Code)
	}
	if w.fx.ondaHits.Load() != hits {
		t.Error("reopening a known slug asked OndaCast")
	}
	// Opening a show already followed writes no open row.
	if rec = w.post(t, "/podcasts/open", w.ben, url.Values{"slug": {"found-show"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("open followed: %d", rec.Code)
	}
	var n int
	w.a.st.Reader().QueryRow(`SELECT count(*) FROM podcast_opens WHERE user_id = ?`, w.benID).Scan(&n)
	if n != 0 {
		t.Errorf("open rows for a follower = %d", n)
	}
}

func TestPodcastOpenForbidden(t *testing.T) {
	w := newPodWorld(t)
	slug := url.Values{"slug": {"other-show"}}
	if rec := w.post(t, "/podcasts/open", w.gus, slug); rec.Code != http.StatusNotFound {
		t.Errorf("guest: %d", rec.Code)
	}
	if rec := w.a.do(http.MethodPost, "/podcasts/open", slug, nil, w.cy); rec.Code != http.StatusForbidden { // no CSRF token
		t.Errorf("no CSRF: %d", rec.Code)
	}
	if rec := w.get("/podcasts/open", w.cy); rec.Code != http.StatusMethodNotAllowed && rec.Code != http.StatusNotFound { // the mux answers 405 or the app 404; never an open
		t.Errorf("GET: %d", rec.Code)
	}
	for name, f := range map[string]url.Values{
		"empty": {"slug": {""}}, "long": {"slug": {strings.Repeat("a", 201)}}, "feed only": {"feed_url": {w.fx.otherFeedURL()}},
	} {
		if rec := w.post(t, "/podcasts/open", w.cy, f, hx); rec.Code != http.StatusUnprocessableEntity {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if rec := w.post(t, "/podcasts/open", w.cy, url.Values{"slug": {"no-such-show"}}, hx); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("not found at OndaCast: %d", rec.Code)
	}
	w.fx.ondaDown.Store(true)
	if rec := w.post(t, "/podcasts/open", w.cy, slug, hx); rec.Code != http.StatusBadGateway {
		t.Errorf("OndaCast down: %d", rec.Code)
	}
	w.fx.ondaDown.Store(false)
	id, _ := w.openOther(t)
	for _, p := range []string{"unfollow", "refresh", "seen", "settings"} {
		if rec := w.post(t, "/podcasts/shows/"+id+"/"+p, w.cy, nil); rec.Code != http.StatusNotFound {
			t.Errorf("non-follower %s: %d", p, rec.Code)
		}
	}
}

func TestPodcastOpenCrossPerson(t *testing.T) {
	w := newPodWorld(t)
	id, eps := w.openOther(t)
	ep := eps[0]
	for _, path := range []string{"/podcasts/shows/" + id, "/hx/podcasts/shows/" + id + "/status", "/podcasts/art/" + id,
		"/podcasts/audio/" + ep, "/podcasts/chapters/" + ep, "/podcasts/episodes/" + ep, "/hx/player/episode/" + ep} {
		if rec := w.get(path, w.ben); rec.Code != http.StatusNotFound {
			t.Errorf("other person GET %s: %d", path, rec.Code)
		}
	}
	prog := url.Values{"position_ms": {"5"}, "client_updated_at": {fmt.Sprint(time.Now().UnixMilli())}, "device_id": {"device-1234"}}
	if rec := w.post(t, "/podcasts/progress/"+ep, w.ben, prog); rec.Code != http.StatusNotFound {
		t.Errorf("progress: %d", rec.Code)
	}
	if rec := w.post(t, "/podcasts/follow", w.ben, url.Values{"show_id": {id}}); rec.Code != http.StatusNotFound {
		t.Errorf("follow by id: %d", rec.Code)
	}
	if rec := w.post(t, "/queue", w.ben, url.Values{"episode_id": {ep}, "where": {"end"}}); rec.Code == http.StatusSeeOther || rec.Code == 200 {
		t.Errorf("queue add: %d", rec.Code)
	}
	if rec := w.post(t, "/podcasts/open", w.ben, url.Values{"slug": {"other-show"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("second person opens: %d", rec.Code)
	}
	if rec := w.get("/podcasts/shows/"+id, w.ben); rec.Code != 200 {
		t.Errorf("after opening: %d", rec.Code)
	}
}

func TestPodcastOpenLimiter(t *testing.T) {
	w := newPodWorld(t)
	for i := 0; i < 60; i++ {
		if !w.a.srv.podLim.open.Take(w.cyID) {
			t.Fatalf("take %d refused", i)
		}
	}
	hits := w.fx.ondaHits.Load()
	rec := w.post(t, "/podcasts/open", w.cy, url.Values{"slug": {"other-show"}}, hx)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("61st new open: %d", rec.Code)
	}
	if w.fx.ondaHits.Load() != hits {
		t.Error("a refused open reached OndaCast")
	}
	// A show the person follows needs no budget.
	if rec = w.post(t, "/podcasts/open", w.ben, url.Values{"slug": {"found-show"}}); rec.Code != http.StatusSeeOther {
		t.Errorf("other person: %d", rec.Code)
	}
}

func TestPodcastFollowByShowID(t *testing.T) {
	w := newPodWorld(t)
	id, _ := w.openOther(t)
	if rec := w.post(t, "/podcasts/follow", w.ben, url.Values{"show_id": {id}}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown show: %d", rec.Code)
	}
	if rec := w.post(t, "/podcasts/follow", w.cy, url.Values{"show_id": {id}, "slug": {"x"}}, hx); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("two inputs: %d", rec.Code)
	}
	if rec := w.post(t, "/podcasts/follow", w.cy, url.Values{"show_id": {id}}); rec.Code != http.StatusSeeOther || location(rec) != "/podcasts/shows/"+id {
		t.Fatalf("follow by id: %d %q", rec.Code, location(rec))
	}
	var n int
	w.a.st.Reader().QueryRow(`SELECT count(*) FROM podcast_opens WHERE user_id = ?`, w.cyID).Scan(&n)
	if n != 0 {
		t.Errorf("open rows after follow = %d", n)
	}
	if body := w.get("/podcasts/shows/"+id, w.cy).Body.String(); !strings.Contains(body, "/unfollow") {
		t.Error("followed page has no Stop following")
	}
}
