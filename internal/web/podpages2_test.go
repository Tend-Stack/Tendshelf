package web

import (
	"context"
	"html"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/podcast"
)

// The first-use package (2026-11-10): the show page fills in by itself after Follow, search results show their
// artwork through the server, Radio has its own navigation item, and the show header reads like the book page.

// TestPodPages2ShowStatus: a followed show whose first fetch has not ended draws a polling page with a neutral
// heading and no "No episodes yet."; the status fragment answers 204 until the fetch ends, then the whole page block
// without the poll; a failed first fetch ends the poll with the plain-words failure; a stranger and a guest get 404.
func TestPodPages2ShowStatus(t *testing.T) {
	w := newPodWorld(t)
	ctx := context.Background()
	id, _, err := w.fx.svc.Follow(ctx, w.benID, w.fx.otherFeedURL(), "")
	if err != nil {
		t.Fatal(err)
	}
	page := w.get("/podcasts/shows/"+id, w.ben).Body.String()
	for _, want := range []string{`hx-get="/hx/podcasts/shows/` + id + `/status"`, `hx-trigger="every 2s"`, "Getting episodes", `>pub.test</h1>`} {
		if !strings.Contains(page, want) {
			t.Errorf("fetching page lacks %q", want)
		}
	}
	if strings.Contains(page, "No episodes yet.") || strings.Contains(page, "other.xml") {
		t.Error("fetching page shows the empty line or the feed address")
	}
	status := "/hx/podcasts/shows/" + id + "/status"
	if rec := w.get(status, w.ben); rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Errorf("fetching status: %d %q, want 204 with no body", rec.Code, rec.Body)
	}
	if rec := w.get(status, w.cy); rec.Code != http.StatusNotFound {
		t.Errorf("stranger: %d, want 404", rec.Code)
	}
	if rec := w.get(status, w.gus); rec.Code != http.StatusNotFound {
		t.Errorf("guest: %d, want 404", rec.Code)
	}
	w.applyFeed(t, id, "Other")
	rec := w.get(status, w.ben)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, "Other episode 1") || !strings.Contains(body, `id="pod-show"`) {
		t.Fatalf("done status: %d %.200s", rec.Code, body)
	}
	if strings.Contains(body, "hx-trigger") || strings.Contains(body, "Getting episodes") {
		t.Error("the finished block still polls or shows the banner")
	}

	failed, _, err := w.fx.svc.Follow(ctx, w.benID, w.fx.feedURL()+"?x=1", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := w.fx.svc.MarkFailed(ctx, failed, "not_a_feed"); err != nil {
		t.Fatal(err)
	}
	rec = w.get("/hx/podcasts/shows/"+failed+"/status", w.ben)
	body = rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, html.EscapeString(podcast.FailText("not_a_feed"))) || !strings.Contains(body, ">Refresh<") || strings.Contains(body, "hx-trigger") {
		t.Errorf("failed status: %d %.300s", rec.Code, body)
	}
}

// TestPodPages2ShowHeader: the meta line counts the episodes and dates the newest, one Play latest button leads the
// actions, and the head never carries the feed address.
func TestPodPages2ShowHeader(t *testing.T) {
	w := newPodWorld(t)
	body := w.get("/podcasts/shows/"+w.show, w.ben).Body.String()
	for _, want := range []string{"3 episodes · newest 3 Jan 2024", "Play latest", `data-ts-play="` + w.eps[2] + `"`} {
		if !strings.Contains(body, want) {
			t.Errorf("show page lacks %q", want)
		}
	}
	if strings.Contains(body, "feed.xml</h1>") {
		t.Error("the feed address is a headline")
	}
}

var podSearchArtSrc = regexp.MustCompile(`src="(/(?:podcasts|radio)/search/art/[0-9a-f]{32})"`)

// TestPodPages2SearchArt: a search names same-origin picture keys; the key serves the publisher's picture to the
// person who searched, and is 404 for another person, a guest, an unknown key, a private address and an oversize file.
func TestPodPages2SearchArt(t *testing.T) {
	w := newPodWorld(t)
	m := podSearchArtSrc.FindStringSubmatch(w.get("/podcasts/search?q=found", w.ben).Body.String())
	if m == nil {
		t.Fatal("the search result names no picture route")
	}
	rec := w.get(m[1], w.ben)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || rec.Header().Get("X-Content-Type-Options") != "nosniff" ||
		!strings.HasPrefix(rec.Header().Get("Cache-Control"), "private") {
		t.Errorf("picture: %d %v", rec.Code, rec.Header())
	}
	if again := w.get(m[1], w.ben); again.Code != http.StatusOK {
		t.Errorf("second fetch (cache): %d", again.Code)
	}
	for name, ck := range map[string]*http.Cookie{"another person": w.cy, "guest": w.gus} {
		if rec := w.get(m[1], ck); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404", name, rec.Code)
		}
	}
	if rec := w.get("/podcasts/search/art/0123456789abcdef0123456789abcdef", w.ben); rec.Code != http.StatusNotFound {
		t.Errorf("unknown key: %d", rec.Code)
	}
	if rec := w.get("/radio/search/art/"+m[1][strings.LastIndex(m[1], "/")+1:], w.ben); rec.Code != http.StatusNotFound {
		t.Errorf("a podcast key on the radio route: %d", rec.Code)
	}
	n := 0
	for name, img := range map[string]string{"private address": "http://127.0.0.1:9/x.png", "oversize": w.fx.pub.URL("pics.test", "/big.png")} {
		n++ // a new query each time: the search answer is cached per query
		w.fx.mu.Lock()
		w.fx.searchImage = img
		w.fx.mu.Unlock()
		bad := podSearchArtSrc.FindStringSubmatch(w.get("/podcasts/search?q=found"+strings.Repeat("x", n), w.ben).Body.String())
		if bad == nil {
			t.Fatalf("%s: no picture route", name)
		}
		if rec := w.get(bad[1], w.ben); rec.Code != http.StatusNotFound {
			t.Errorf("%s: %d, want 404 (the plain tile stays)", name, rec.Code)
		}
	}
}

// TestPodPages2Nav: the four on/off combinations of podcasts and radio, and a guest.
func TestPodPages2Nav(t *testing.T) {
	w := newRadioWorld(t)
	set := func(pod, radio bool) {
		s := podcast.Defaults()
		s.Enabled, s.RadioEnabled = pod, radio
		if err := podcast.SaveSettings(context.Background(), w.a.st, "", s, time.Now()); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range []struct {
		pod, radio       bool
		hasPod, hasRadio bool
	}{{false, false, false, false}, {true, false, true, false}, {false, true, false, true}, {true, true, true, true}} {
		set(c.pod, c.radio)
		body := w.get("/", w.ben).Body.String()
		if got := strings.Contains(body, `data-nav="/podcasts"`); got != c.hasPod {
			t.Errorf("podcasts=%v radio=%v: Podcasts item %v, want %v", c.pod, c.radio, got, c.hasPod)
		}
		if got := strings.Contains(body, `data-nav="/radio"`); got != c.hasRadio {
			t.Errorf("podcasts=%v radio=%v: Radio item %v, want %v", c.pod, c.radio, got, c.hasRadio)
		}
	}
	set(true, true)
	if body := w.get("/", w.guest).Body.String(); strings.Contains(body, `data-nav="/podcasts"`) || strings.Contains(body, `data-nav="/radio"`) {
		t.Error("a guest sees a Podcasts or Radio item")
	}
	// On a Radio page the Radio item is current and Podcasts is not (the phone bar, with no Radio tab, marks Podcasts).
	body := w.get("/radio", w.ben).Body.String()
	if !regexp.MustCompile(`(?s)<a[^>]*data-nav="/radio"[^>]*aria-current="page"`).MatchString(body) {
		t.Error("the rail's Radio item is not current on /radio")
	}
}

// TestPodPages2EpisodePanel: the desktop panel of a playing episode holds the meta line, the sanitised show notes, the
// stored chapters (only when there are some) and the episode's action forms, and is 404 for a stranger and a guest.
func TestPodPages2EpisodePanel(t *testing.T) {
	w := newPodWorld(t)
	ctx := context.Background()
	ep := w.eps[0]
	path := "/hx/player/episode-panel/" + ep
	if _, err := w.a.st.Writer().Exec(`UPDATE podcast_episodes SET notes_html = ?, chapters_json = '' WHERE id = ?`,
		`<p>Hello <b>there</b></p><script>alert(1)</script><a href="javascript:alert(2)">bad</a>`, ep); err != nil {
		t.Fatal(err)
	}
	rec := w.get(path, w.ben)
	body := rec.Body.String()
	if rec.Code != http.StatusOK || !strings.Contains(body, `id="now-panel"`) || !strings.Contains(body, `data-book-id="`+ep+`"`) {
		t.Fatalf("panel: %d %.200s", rec.Code, body)
	}
	for _, want := range []string{"Hello <b>there</b>", "Mark as played", "Star", "Go to show", "Open episode page", "/podcasts/episodes/" + ep} {
		if !strings.Contains(body, want) {
			t.Errorf("panel lacks %q", want)
		}
	}
	if strings.Contains(body, "<script") || strings.Contains(body, "javascript:") {
		t.Error("the panel carries unsanitised notes")
	}
	if strings.Contains(body, "Chapters") || strings.Contains(body, "data-ts-play-at") {
		t.Error("a chapters section is drawn with no stored chapters")
	}
	if strings.Contains(body, "data-ts-play=") {
		t.Error("the panel has its own Play button; the bar plays")
	}
	if _, err := w.a.st.Writer().Exec(`UPDATE podcast_episodes SET chapters_json = ? WHERE id = ?`,
		`{"version":"1.2.0","chapters":[{"startTime":0,"title":"Intro"},{"startTime":60,"title":"Second part"}]}`, ep); err != nil {
		t.Fatal(err)
	}
	_ = ctx
	body = w.get(path, w.ben).Body.String()
	if !strings.Contains(body, "Second part") || !strings.Contains(body, `data-ts-play-at="60000"`) || !strings.Contains(body, `data-ts-book="`+ep+`"`) {
		t.Errorf("stored chapters are not in the panel: %.400s", body)
	}
	if rec := w.get(path, w.cy); rec.Code != http.StatusNotFound {
		t.Errorf("stranger: %d, want 404", rec.Code)
	}
	if rec := w.get(path, w.gus); rec.Code != http.StatusNotFound {
		t.Errorf("guest: %d, want 404", rec.Code)
	}
}
