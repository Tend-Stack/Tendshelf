package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/jobs"
	"github.com/Tend-Stack/tendshelf/internal/podcast"
)

// TestEpisodePlayerFragment: the episode fragment has the book fragment's data attributes plus the episode's own
// addresses, takes the show's speed and skips, reads the place back (newest save wins), and is refused to a stranger.
func TestEpisodePlayerFragment(t *testing.T) {
	w := newPodWorld(t)
	ep := w.eps[0]
	speed := 1.5
	if err := w.fx.svc.SetShowSettings(context.Background(), w.benID, w.show, podcast.ShowSettings{Speed: &speed, SkipIntroS: 30, SkipOutroS: 20, KeepLatest: 10}); err != nil {
		t.Fatal(err)
	}
	rec := w.get("/hx/player/episode/"+ep, w.ben)
	body := rec.Body.String()
	if rec.Code != 200 {
		t.Fatalf("fragment: %d %q", rec.Code, body)
	}
	for _, want := range []string{
		`id="player-data"`, `data-kind="episode"`, `data-book-id="` + ep + `"`, `data-progress-href="/podcasts/progress/` + ep + `"`,
		`data-href="/podcasts/audio/` + ep + `"`, `data-offset-ms="0"`, `data-duration-ms="600000"`, `data-mime="audio/mpeg"`, `data-playable="1"`,
		`data-speed="1.50"`, `data-skip-intro-ms="30000"`, `data-skip-outro-ms="20000"`, `data-show-href="/podcasts/shows/` + w.show + `"`,
		`data-chapters-href="/podcasts/chapters/` + ep + `"`, `data-title="Main episode 1"`, `data-author="Main"`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("fragment lacks %s", want)
		}
	}
	if strings.Contains(body, "audio.test") || strings.Contains(body, "chap.test") {
		t.Errorf("fragment leaks an upstream address: %q", body)
	}
	// The first ask for chapters stores them; the fragment then carries them and no longer asks.
	if rec = w.get("/podcasts/chapters/"+ep, w.ben); rec.Code != 200 {
		t.Fatalf("chapters: %d", rec.Code)
	}
	body = w.get("/hx/player/episode/"+ep, w.ben).Body.String()
	if !strings.Contains(body, `>Intro</li>`) || strings.Contains(body, "data-chapters-href") {
		t.Errorf("fragment after chapters: %q", body)
	}
	// The place: the newer client time wins, finished marks it played.
	now := time.Now().UnixMilli()
	save := func(pos, at int64, extra url.Values) {
		f := url.Values{"position_ms": {fmt.Sprint(pos)}, "client_updated_at": {fmt.Sprint(at)}, "device_id": {"device-1234"}}
		for k, v := range extra {
			f[k] = v
		}
		if rec := w.post(t, "/podcasts/progress/"+ep, w.ben, f); rec.Code != 200 {
			t.Fatalf("save: %d %q", rec.Code, rec.Body)
		}
	}
	save(5000, now, nil)
	save(9000, now-1000, nil) // older: ignored
	body = w.get("/hx/player/episode/"+ep, w.ben).Body.String()
	if !strings.Contains(body, `data-position-ms="5000"`) || !strings.Contains(body, `data-updated-at="`+fmt.Sprint(now)+`"`) || !strings.Contains(body, `data-finished="0"`) {
		t.Errorf("place after saves: %q", body)
	}
	save(600000, now+1000, url.Values{"finished": {"1"}})
	if body = w.get("/hx/player/episode/"+ep, w.ben).Body.String(); !strings.Contains(body, `data-finished="1"`) {
		t.Errorf("finished episode: %q", body)
	}
	// A stranger, an unknown id and a book route for an episode id are all the plain 404.
	for _, path := range []string{"/hx/player/episode/" + ep, "/hx/player/episode/pe_nope", "/hx/player/" + ep} {
		ck := w.cy
		if strings.HasSuffix(path, "pe_nope") || path == "/hx/player/"+ep {
			ck = w.ben
		}
		if rec := w.get(path, ck); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d", path, rec.Code)
		}
	}
}

// TestPlayerLastPlayedMayBeAnEpisode: with no book started, the bar's last played is the newest unfinished episode.
func TestPlayerLastPlayedMayBeAnEpisode(t *testing.T) {
	w := newPodWorld(t)
	if rec := w.get("/hx/player", w.ben); rec.Code != http.StatusNoContent {
		t.Fatalf("nothing played: %d", rec.Code)
	}
	now := time.Now().UnixMilli()
	for i, ep := range w.eps[:2] {
		f := url.Values{"position_ms": {"4000"}, "client_updated_at": {fmt.Sprint(now + int64(i)*1000)}, "device_id": {"device-1234"}}
		if rec := w.post(t, "/podcasts/progress/"+ep, w.ben, f); rec.Code != 200 {
			t.Fatal(rec.Code)
		}
	}
	body := w.get("/hx/player", w.ben).Body.String()
	if !strings.Contains(body, `data-kind="episode"`) || !strings.Contains(body, `data-book-id="`+w.eps[1]+`"`) {
		t.Errorf("last played: %q", body)
	}
	// Finishing it moves the bar to the other one.
	w.post(t, "/podcasts/progress/"+w.eps[1], w.ben, url.Values{"position_ms": {"600000"}, "client_updated_at": {fmt.Sprint(now + 5000)}, "device_id": {"device-1234"}, "finished": {"1"}})
	if body = w.get("/hx/player", w.ben).Body.String(); !strings.Contains(body, `data-book-id="`+w.eps[0]+`"`) {
		t.Errorf("after finishing: %q", body)
	}
	if rec := w.get("/hx/player", w.cy); rec.Code != http.StatusNoContent {
		t.Errorf("another person: %d", rec.Code)
	}
}

// TestHomeNewEpisodesShelf: Home shows the shelf for new episodes, links the New tab, and hides it when none are new.
func TestHomeNewEpisodesShelf(t *testing.T) {
	w := newPodWorld(t)
	if _, err := w.a.st.Writer().Exec(`UPDATE podcast_subscriptions SET seen_at = 1 WHERE show_id = ?`, w.show); err != nil {
		t.Fatal(err)
	}
	body := w.get("/", w.ben).Body.String()
	if !strings.Contains(body, `id="row-episodes"`) || !strings.Contains(body, "New episodes") || !strings.Contains(body, `href="/podcasts?tab=new"`) ||
		!strings.Contains(body, `data-ts-play="`+w.eps[0]+`"`) || strings.Count(body, "ts-eprow__play") != podFixEpisodes {
		t.Errorf("home shelf: %d rows", strings.Count(body, "ts-eprow__play"))
	}
	if body = w.get("/", w.cy).Body.String(); strings.Contains(body, "row-episodes") {
		t.Errorf("a person who follows nothing sees the shelf")
	}
	w.get("/podcasts/shows/"+w.show, w.ben)
	if _, err := w.a.st.Writer().Exec(`UPDATE podcast_subscriptions SET seen_at = ? WHERE show_id = ?`, time.Now().UnixMilli(), w.show); err != nil {
		t.Fatal(err)
	}
	if body = w.get("/", w.ben).Body.String(); strings.Contains(body, "row-episodes") {
		t.Errorf("shelf shows with nothing new")
	}
	// Played from the shelf returns to Home.
	rec := w.post(t, "/podcasts/episodes/"+w.eps[0]+"/played", w.ben, url.Values{"played": {"1"}, "back": {"/"}})
	if rec.Code != http.StatusSeeOther || location(rec) != "/" {
		t.Errorf("played from home: %d %q", rec.Code, location(rec))
	}
}

// TestPodcastChaptersKeepOnlyStartAndTitle: the stored and served chapters carry no picture, link or other field.
func TestPodcastChaptersKeepOnlyStartAndTitle(t *testing.T) {
	raw := []byte(`{"version":"1.2.0","chapters":[{"startTime":60.5,"title":" Two ","img":"http://x/y.png","url":"http://x"},{"startTime":0,"title":"One"},{"startTime":-1,"title":"neg"},{"startTime":10,"title":"hidden","toc":false},{"startTime":0,"title":"dup"}]}`)
	got := podcast.ParseChapters(raw)
	if len(got) != 2 || got[0] != (podcast.Chapter{StartMs: 0, Title: "One"}) || got[1] != (podcast.Chapter{StartMs: 60500, Title: "Two"}) {
		t.Fatalf("chapters = %+v", got)
	}
	out := string(podcast.ChaptersJSON(got))
	if strings.Contains(out, "img") || strings.Contains(out, "http") || !strings.Contains(out, `"startTime":60.5`) {
		t.Errorf("stored form = %s", out)
	}
	if podcast.ParseChapters([]byte("nope")) != nil {
		t.Error("garbage parsed")
	}
}

// TestPodcastImportResultWords: the import status in plain words, including the stop at the follow limit.
func TestPodcastImportResultWords(t *testing.T) {
	job := func(state jobs.State, result string) jobs.Job {
		return jobs.Job{ID: "jb_x", State: state, Result: json.RawMessage(result), Progress: json.RawMessage(`{"done":3,"of":8}`)}
	}
	for _, c := range []struct {
		j    jobs.Job
		want []string
	}{
		{job(jobs.StateRunning, ""), []string{"Following your shows… 3 of 8 done."}},
		{job(jobs.StateDone, `{"followed":5,"skipped":0,"too_many":0}`), []string{"Done. Now following 5 shows."}},
		{job(jobs.StateDone, `{"followed":1}`), []string{"Done. Now following 1 show."}},
		{job(jobs.StateDone, `{"followed":0}`), []string{"Done. You already follow every show in that file."}},
		{job(jobs.StateDone, `{"followed":2,"skipped":3}`), []string{"Done. Now following 2 shows.", "3 shows couldn't be followed."}},
		{job(jobs.StateDone, `{"followed":4,"stopped":1,"not_done":6}`), []string{"Done. Now following 4 shows.", "Stopped early: you have followed a lot of shows in a short time. 6 shows not added yet. Import the file again in an hour to add the rest."}},
		{job(jobs.StateFailed, ""), []string{"The import didn't finish. Try the file again."}},
	} {
		v := podImportStatus(c.j)
		if strings.Join(v.Lines, "|") != strings.Join(c.want, "|") {
			t.Errorf("%s %s: lines = %q, want %q", c.j.State, c.j.Result, v.Lines, c.want)
		}
		if v.Pending != (c.j.State == jobs.StateRunning) {
			t.Errorf("pending = %v for %s", v.Pending, c.j.State)
		}
	}
}

// TestPodcastImportStatusPage: after an import, the job's fragment and the Podcasts page show the result to the person who
// started it and to nobody else.
func TestPodcastImportStatusPage(t *testing.T) {
	w := newPodWorld(t)
	opmlDoc := fmt.Sprintf(`<?xml version="1.0"?><opml version="2.0"><body><outline type="rss" text="Other" xmlUrl=%q/><outline type="rss" text="Main" xmlUrl=%q/></body></opml>`, w.fx.otherFeedURL(), w.fx.feedURL())
	if rec := w.multipartPost(t, "/podcasts/opml", w.cy, []byte(opmlDoc)); rec.Code != http.StatusSeeOther {
		t.Fatalf("import: %d", rec.Code)
	}
	var id string
	if err := w.a.st.Reader().QueryRow(`SELECT id FROM jobs WHERE kind = ?`, podcast.KindImport).Scan(&id); err != nil {
		t.Fatal(err)
	}
	page := w.get("/podcasts", w.cy).Body.String()
	if !strings.Contains(page, `id="pod-import-status"`) || !strings.Contains(page, `hx-get="/hx/jobs/`+id+`"`) || !strings.Contains(page, `hx-trigger="every 2s"`) || !strings.Contains(page, `enctype="multipart/form-data"`) {
		t.Errorf("page while queued lacks the polling status: %q", page)
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
	waitFor(t, "the import result", func() bool {
		return strings.Contains(w.get("/hx/jobs/"+id, w.cy).Body.String(), "Now following 2 shows")
	})
	frag := w.get("/hx/jobs/"+id, w.cy).Body.String()
	if strings.Contains(frag, "hx-trigger") {
		t.Errorf("a finished import keeps polling: %q", frag)
	}
	if rec := w.get("/hx/jobs/"+id, w.ben); rec.Code != http.StatusNotFound {
		t.Errorf("another person's import job: %d", rec.Code)
	}
	if page = w.get("/podcasts", w.cy).Body.String(); !strings.Contains(page, "Now following 2 shows") {
		t.Errorf("page after the import: %q", page)
	}
}
