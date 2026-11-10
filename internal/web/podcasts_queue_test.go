package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/podcast"
)

func (w *podWorld) hx(t *testing.T, path string, ck *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	t.Helper()
	return w.post(t, path, ck, form, func(r *http.Request) { r.Header.Set("HX-Request", "true") })
}

// queueOrder lists the item ids on the /queue page, in the order they are drawn.
func (w *podWorld) queueOrder(t *testing.T, ck *http.Cookie) []string {
	t.Helper()
	rec := w.get("/queue", ck)
	if rec.Code != 200 {
		t.Fatalf("/queue: %d", rec.Code)
	}
	var ids []string
	for _, m := range regexp.MustCompile(`<li id="item-([^"]+)"`).FindAllStringSubmatch(rec.Body.String(), -1) {
		ids = append(ids, m[1])
	}
	return ids
}

func sameIDs(a, b []string) bool { return strings.Join(a, ",") == strings.Join(b, ",") }

// TestEpisodeQueueWeb: Play next and Add to queue work from an episode (plain post and htmx), the Up next page lists
// episodes in order with working move and remove, and a person who cannot listen to the episode gets the plain 404.
func TestEpisodeQueueWeb(t *testing.T) {
	w := newPodWorld(t)
	e0, e1, e2 := w.eps[0], w.eps[1], w.eps[2]

	if rec := w.post(t, "/queue", w.ben, url.Values{"episode_id": {e0}, "where": {"end"}, "next": {"/podcasts"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("add (plain): %d %q", rec.Code, rec.Body)
	}
	rec := w.hx(t, "/queue", w.ben, url.Values{"episode_id": {e1}, "where": {"end"}, "next": {"/podcasts"}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), queueAddedEnd) || !strings.Contains(rec.Body.String(), `id="ep-menu-`+e1+`"`) {
		t.Fatalf("add (htmx): %d %q", rec.Code, rec.Body)
	}
	rec = w.hx(t, "/queue", w.ben, url.Values{"episode_id": {e2}, "where": {"next"}, "next": {"/podcasts"}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), queueAddedFront) {
		t.Fatalf("play next: %d %q", rec.Code, rec.Body)
	}
	if rec = w.hx(t, "/queue", w.ben, url.Values{"episode_id": {e0}, "where": {"end"}}); !strings.Contains(rec.Body.String(), queueAlready) {
		t.Errorf("re-add: %q", rec.Body)
	}
	if got := w.queueOrder(t, w.ben); !sameIDs(got, []string{e2, e0, e1}) {
		t.Fatalf("order = %v, want %v", got, []string{e2, e0, e1})
	}
	page := w.get("/queue", w.ben).Body.String()
	if !strings.Contains(page, "Main episode 3") || !strings.Contains(page, `/podcasts/episodes/`+e2) {
		t.Errorf("the page does not name the episode: %q", page)
	}
	// Move e1 up one place, then remove e2.
	if rec = w.hx(t, "/queue/"+e1+"/move", w.ben, url.Values{"dir": {"up"}, "in": {"page"}}); rec.Code != 200 {
		t.Fatalf("move: %d %q", rec.Code, rec.Body)
	}
	if got := w.queueOrder(t, w.ben); !sameIDs(got, []string{e2, e1, e0}) {
		t.Errorf("after move = %v", got)
	}
	if rec = w.hx(t, "/queue/"+e2+"/delete", w.ben, url.Values{"in": {"page"}}); rec.Code != 200 {
		t.Fatalf("remove: %d %q", rec.Code, rec.Body)
	}
	if got := w.queueOrder(t, w.ben); !sameIDs(got, []string{e1, e0}) {
		t.Errorf("after remove = %v", got)
	}
	// Each person's queue is their own.
	if got := w.queueOrder(t, w.owner); len(got) != 0 {
		t.Errorf("the owner sees %v", got)
	}

	// Forbidden: a member who does not follow the show, a guest, an unknown id and a book id sent as an episode.
	for name, c := range map[string]*http.Cookie{"stranger": w.cy, "guest": w.gus} {
		if rec := w.post(t, "/queue", c, url.Values{"episode_id": {e0}, "where": {"end"}}); rec.Code != http.StatusNotFound {
			t.Errorf("%s add: %d", name, rec.Code)
		}
	}
	rec = w.hx(t, "/queue", w.cy, url.Values{"episode_id": {e0}, "where": {"end"}})
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "find that") {
		t.Errorf("stranger add (htmx): %d %q", rec.Code, rec.Body)
	}
	if rec := w.post(t, "/queue", w.ben, url.Values{"episode_id": {"pe_nope"}, "where": {"end"}}); rec.Code != http.StatusNotFound {
		t.Errorf("unknown episode: %d", rec.Code)
	}
	if rec := w.post(t, "/queue/"+e0+"/delete", w.cy, nil); rec.Code != http.StatusNotFound {
		t.Errorf("stranger remove: %d", rec.Code)
	}
	if rec := w.post(t, "/queue", w.ben, url.Values{"episode_id": {e0}, "where": {"sideways"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("bad where: %d", rec.Code)
	}

	// With podcasts off, episodes are not offered, listed or accepted; the rows stay for when it is back on.
	w.fx.enable(t, w.a, func(s *podcast.Settings) { s.Enabled = false })
	if rec := w.post(t, "/queue", w.ben, url.Values{"episode_id": {e0}, "where": {"end"}}); rec.Code != http.StatusNotFound {
		t.Errorf("add while off: %d", rec.Code)
	}
	if got := w.queueOrder(t, w.ben); len(got) != 0 {
		t.Errorf("queue while off lists %v", got)
	}
	w.fx.enable(t, w.a, nil)
	if got := w.queueOrder(t, w.ben); !sameIDs(got, []string{e1, e0}) {
		t.Errorf("queue after turning back on = %v", got)
	}
}

// TestEpisodeRowsOfferQueueAndDownload: the episode row has the menu with both choices, and the episode page has the
// menu and the Download control the download manager fills in.
func TestEpisodeRowsOfferQueueAndDownload(t *testing.T) {
	w := newPodWorld(t)
	ep := w.eps[0]
	show := w.get("/podcasts/shows/"+w.show, w.ben).Body.String()
	for _, want := range []string{`id="ep-menu-` + ep + `"`, `name="episode_id" value="` + ep + `"`, queuePlayNext, queueAddEnd, `More for Main episode 1`} {
		if !strings.Contains(show, want) {
			t.Errorf("show page lacks %q", want)
		}
	}
	page := w.get("/podcasts/episodes/"+ep, w.ben).Body.String()
	for _, want := range []string{`data-ts-download="` + ep + `"`, `id="ep-menu-` + ep + `"`, `data-ts-download-status`} {
		if !strings.Contains(page, want) {
			t.Errorf("episode page lacks %q", want)
		}
	}
}

func apiReq(w *podWorld, method, path, tok, body string) *httptest.ResponseRecorder {
	return deviceBearerReq(w.a, method, path, tok, func(r *http.Request) {
		if body != "" {
			r.Body = io.NopCloser(strings.NewReader(body))
			r.ContentLength = int64(len(body))
			r.Header.Set("Content-Type", "application/json")
		}
	})
}

// TestEpisodeQueueAPI: the app's queue answers books exactly as before unless it asks for ?kinds=book,episode; with it,
// episodes are listed, added, moved, removed and popped, and the pop gives the playback document.
func TestEpisodeQueueAPI(t *testing.T) {
	w := newPodWorld(t)
	tok, other := w.bearer(t, w.benID), w.bearer(t, w.cyID)
	e0, e1 := w.eps[0], w.eps[1]

	rec := apiReq(w, "POST", "/api/v1/queue?kinds=book,episode", tok, `{"episodeId":"`+e0+`","where":"end"}`)
	var list struct {
		Items []struct {
			Kind    string `json:"kind"`
			Episode *struct {
				ID, Title, Show string
			} `json:"episode"`
			Book *struct{ ID string } `json:"book"`
		} `json:"items"`
		Total int `json:"total"`
	}
	apiDecode(t, rec, &list)
	if rec.Code != 200 || len(list.Items) != 1 || list.Items[0].Kind != "episode" || list.Items[0].Episode == nil || list.Items[0].Episode.ID != e0 ||
		list.Items[0].Book != nil || list.Items[0].Episode.Show != "Main" {
		t.Fatalf("add: %d %q", rec.Code, rec.Body)
	}
	if rec = apiReq(w, "POST", "/api/v1/queue?kinds=book,episode", tok, `{"episodeId":"`+e1+`","where":"next"}`); rec.Code != 200 {
		t.Fatalf("play next: %d %q", rec.Code, rec.Body)
	}
	// Without kinds the queue is books only: an older app sees nothing it cannot read, and nothing is popped.
	rec = apiReq(w, "GET", "/api/v1/queue", tok, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"items":[]`) || !strings.Contains(rec.Body.String(), `"total":0`) {
		t.Errorf("books-only list: %d %q", rec.Code, rec.Body)
	}
	if rec = apiReq(w, "POST", "/api/v1/queue/next", tok, `{}`); rec.Code != http.StatusNoContent {
		t.Errorf("books-only pop: %d %q", rec.Code, rec.Body)
	}
	if rec = apiReq(w, "GET", "/api/v1/queue/next", tok, ""); rec.Code != http.StatusNoContent {
		t.Errorf("books-only peek: %d %q", rec.Code, rec.Body)
	}
	if rec = apiReq(w, "GET", "/api/v1/queue?kinds=bogus", tok, ""); rec.Code != 400 {
		t.Errorf("bad kinds: %d", rec.Code)
	}
	rec = apiReq(w, "GET", "/api/v1/queue?kinds=book,episode", tok, "")
	apiDecode(t, rec, &list)
	if len(list.Items) != 2 || list.Items[0].Episode.ID != e1 || list.Items[1].Episode.ID != e0 {
		t.Fatalf("mixed list: %q", rec.Body)
	}
	// Peek does not take it; move and remove take an episode id in the path.
	rec = apiReq(w, "GET", "/api/v1/queue/next?kinds=book,episode", tok, "")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"kind":"episode"`) || !strings.Contains(rec.Body.String(), `"audioUrl":"/api/v1/podcasts/audio/`+e1+`"`) {
		t.Errorf("peek: %d %q", rec.Code, rec.Body)
	}
	if rec = apiReq(w, "POST", "/api/v1/queue/"+e0+"/move?kinds=book,episode", tok, `{"dir":"up"}`); rec.Code != 200 {
		t.Fatalf("move: %d %q", rec.Code, rec.Body)
	}
	apiDecode(t, apiReq(w, "GET", "/api/v1/queue?kinds=episode", tok, ""), &list)
	if len(list.Items) != 2 || list.Items[0].Episode.ID != e0 {
		t.Errorf("after move: %+v", list.Items)
	}
	// Pop: drops the ended one, gives the first, with the playback document.
	rec = apiReq(w, "POST", "/api/v1/queue/next?kinds=book,episode", tok, `{"ended":"`+e0+`"}`)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"kind":"episode"`) || !strings.Contains(rec.Body.String(), `"id":"`+e1+`"`) ||
		!strings.Contains(rec.Body.String(), `"mime":"audio/mpeg"`) || strings.Contains(rec.Body.String(), "audio.test") {
		t.Errorf("pop: %d %q", rec.Code, rec.Body)
	}
	if rec = apiReq(w, "POST", "/api/v1/queue/next?kinds=book,episode", tok, `{"ended":"`+e1+`"}`); rec.Code != http.StatusNoContent {
		t.Errorf("pop of an empty queue: %d %q", rec.Code, rec.Body)
	}
	if rec = apiReq(w, "POST", "/api/v1/queue", tok, `{"episodeId":"`+e0+`","bookId":"bk_x"}`); rec.Code != 400 {
		t.Errorf("both ids: %d", rec.Code)
	}
	if rec = apiReq(w, "DELETE", "/api/v1/queue/"+e0, tok, ""); rec.Code != 404 {
		t.Errorf("remove of an item not queued: %d", rec.Code)
	}

	// Forbidden: a stranger cannot add, move or remove it; an unknown id is the same 404.
	for name, rq := range map[string]*httptest.ResponseRecorder{
		"add":     apiReq(w, "POST", "/api/v1/queue?kinds=book,episode", other, `{"episodeId":"`+e0+`"}`),
		"move":    apiReq(w, "POST", "/api/v1/queue/"+e0+"/move", other, `{"dir":"up"}`),
		"remove":  apiReq(w, "DELETE", "/api/v1/queue/"+e0, other, ""),
		"unknown": apiReq(w, "POST", "/api/v1/queue", tok, `{"episodeId":"pe_nope"}`),
	} {
		if rq.Code != http.StatusNotFound {
			t.Errorf("%s: %d %q", name, rq.Code, rq.Body)
		}
	}
	if rec = apiReq(w, "GET", "/api/v1/queue?kinds=book,episode", other, ""); !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("a stranger's list: %q", rec.Body)
	}
	// Off: the add is a 404 and the list omits episodes even when asked for.
	apiReq(w, "POST", "/api/v1/queue?kinds=book,episode", tok, `{"episodeId":"`+e0+`"}`)
	w.fx.enable(t, w.a, func(s *podcast.Settings) { s.Enabled = false })
	if rec = apiReq(w, "POST", "/api/v1/queue?kinds=book,episode", tok, `{"episodeId":"`+e1+`"}`); rec.Code != 404 {
		t.Errorf("add while off: %d", rec.Code)
	}
	if rec = apiReq(w, "GET", "/api/v1/queue?kinds=book,episode", tok, ""); !strings.Contains(rec.Body.String(), `"items":[]`) {
		t.Errorf("list while off: %q", rec.Body)
	}
}

// TestPlayerNextAfterAnEpisode: the web player's autoplay gives the next queued episode's fragment, drops the ended
// one, and is a 204 when nothing is left; an older queued book-only caller (no podcasts) is unaffected.
func TestPlayerNextAfterAnEpisode(t *testing.T) {
	w := newPodWorld(t)
	e0, e1, e2 := w.eps[0], w.eps[1], w.eps[2]
	for _, id := range []string{e1, e2} {
		w.post(t, "/queue", w.ben, url.Values{"episode_id": {id}, "where": {"end"}})
	}
	rec := w.post(t, "/hx/player/next", w.ben, url.Values{"ended": {e0}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `data-kind="episode"`) || !strings.Contains(rec.Body.String(), `data-book-id="`+e1+`"`) {
		t.Fatalf("next: %d %q", rec.Code, rec.Body)
	}
	rec = w.post(t, "/hx/player/next", w.ben, url.Values{"ended": {e1}})
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `data-book-id="`+e2+`"`) {
		t.Fatalf("next 2: %d %q", rec.Code, rec.Body)
	}
	if rec = w.post(t, "/hx/player/next", w.ben, url.Values{"ended": {e2}}); rec.Code != http.StatusNoContent {
		t.Errorf("next 3: %d %q", rec.Code, rec.Body)
	}
	// A queued episode the person lost access to is skipped and kept, not played.
	w.post(t, "/queue", w.ben, url.Values{"episode_id": {e0}, "where": {"end"}})
	if _, err := w.a.st.Writer().Exec(`DELETE FROM podcast_subscriptions WHERE user_id = ?`, w.benID); err != nil {
		t.Fatal(err)
	}
	if rec = w.post(t, "/hx/player/next", w.ben, url.Values{"ended": {e1}}); rec.Code != http.StatusNoContent {
		t.Errorf("next without access: %d %q", rec.Code, rec.Body)
	}
	var n int
	w.a.st.Reader().QueryRow(`SELECT count(*) FROM queue_items WHERE user_id = ?`, w.benID).Scan(&n)
	if n != 1 {
		t.Errorf("queue rows after a skipped item = %d, want 1", n)
	}
}

// TestOfflineShellListsDownloadedEpisodes: the shell names the episodes the phone holds that the person may still
// play, and leaves out anyone else's, unknown ids and anything while podcasts are off.
func TestOfflineShellListsDownloadedEpisodes(t *testing.T) {
	w := newPodWorld(t)
	ep := w.eps[0]
	body := w.get("/offline?episodes="+ep+",pe_nope", w.ben).Body.String()
	if !strings.Contains(body, "Main episode 1") || !strings.Contains(body, `data-ts-play="`+ep+`"`) || !strings.Contains(body, offlineReady) {
		t.Errorf("shell lacks the episode: %q", body)
	}
	if strings.Contains(body, "pe_nope") {
		t.Error("shell names an unknown episode")
	}
	if body = w.get("/offline?episodes="+ep, w.cy).Body.String(); strings.Contains(body, "Main episode 1") {
		t.Errorf("shell shows a stranger's episode: %q", body)
	}
	if rec := w.get("/offline?episodes=bad%20id", w.ben); rec.Code != http.StatusBadRequest {
		t.Errorf("bad id: %d", rec.Code)
	}
	w.fx.enable(t, w.a, func(s *podcast.Settings) { s.Enabled = false })
	if body = w.get("/offline?episodes="+ep, w.ben).Body.String(); strings.Contains(body, "Main episode 1") {
		t.Errorf("shell shows an episode with podcasts off: %q", body)
	}
}

// TestServiceWorkerEpisodeRules reads the worker's source: it answers the four episode addresses of decision 0038
// (only from the person's cache), nothing else under /podcasts/ or /radio/, and never stores anything itself.
func TestServiceWorkerEpisodeRules(t *testing.T) {
	src, err := os.ReadFile("static/js/sw.js")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	for _, want := range []string{
		`url.pathname.startsWith("/podcasts/audio/")`,
		`url.pathname.startsWith("/podcasts/art/") || url.pathname.startsWith("/podcasts/chapters/")`,
		`/^\/hx\/player\/(episode\/)?[^/]+$/`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("sw.js lacks %s", want)
		}
	}
	// Every code mention of /podcasts or /radio is one of the allowed branches; a comment may say anything.
	for _, line := range strings.Split(s, "\n") {
		code := strings.TrimSpace(line)
		if strings.HasPrefix(code, "//") || !(strings.Contains(code, "/podcasts") || strings.Contains(code, "/radio")) {
			continue
		}
		if !strings.Contains(code, `"/podcasts/audio/"`) && !strings.Contains(code, `"/podcasts/art/"`) {
			t.Errorf("sw.js touches an address it should not: %s", code)
		}
	}
	// The worker stores nothing itself except the revalidated player fragment and chapter parts (one put).
	if n := strings.Count(s, ".put("); n != 1 {
		t.Errorf("the worker has %d puts, want the one in freshOrCached", n)
	}
}
