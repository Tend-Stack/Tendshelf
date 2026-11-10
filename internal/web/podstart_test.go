package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/podcast"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// First use and the starter lists (decision 0038, addendum 2026-11-10).

type podStartWorld struct {
	t                      *testing.T
	fx                     *radioFix
	a                      *testApp
	owner, member, contrib *http.Cookie
	guest                  *http.Cookie
}

// newPodStartWorld is an app with a podcast and a radio service and both switches off.
func newPodStartWorld(t *testing.T) *podStartWorld {
	t.Helper()
	fx := newRadioFix(t)
	a := newTestApp(t, fx.mod(t))
	fx.enable(t, a, false)
	w := &podStartWorld{t: t, fx: fx, a: a, owner: a.owner(t), member: a.as(t, users.RoleMember), contrib: a.as(t, users.RoleMember), guest: a.as(t, users.RoleGuest)}
	if _, err := a.st.Writer().Exec(`UPDATE users SET adds_books = 1 WHERE id = ?`, sweepUserID(t, a, w.contrib)); err != nil {
		t.Fatal(err)
	}
	return w
}

func (w *podStartWorld) get(path string, ck *http.Cookie) (int, string) {
	rec := w.a.do(http.MethodGet, path, nil, nil, ck)
	return rec.Code, rec.Body.String()
}

func (w *podStartWorld) post(path string, ck *http.Cookie, form url.Values) *httptest.ResponseRecorder {
	f := url.Values{"_csrf": {w.a.tokenFor(w.t, ck)}}
	for k, v := range form {
		f[k] = v
	}
	return w.a.do(http.MethodPost, path, f, nil, ck)
}

func (w *podStartWorld) settings() podcast.Settings {
	return podcast.LoadSettings(context.Background(), w.a.st)
}

func TestPodStartOnboardingPage(t *testing.T) {
	w := newPodStartWorld(t)
	for _, p := range []string{"/podcasts", "/podcasts/search", "/podcasts/shows/ps_x", "/podcasts/episodes/pe_x", "/radio", "/radio/search"} {
		code, body := w.get(p, w.owner)
		if code != http.StatusOK || !strings.Contains(body, "What turning it on does") || !strings.Contains(body, "ondacast.com") {
			t.Errorf("owner %s: %d", p, code)
		}
		for _, want := range []string{`action="/podcasts/start"`, `name="podcasts" value="1" checked`, `name="radio" value="1" checked`, ">Turn on<", `name="_csrf"`} {
			if !strings.Contains(body, want) {
				t.Errorf("owner %s lacks %q", p, want)
			}
		}
		for _, ck := range []*http.Cookie{w.member, w.contrib} {
			code, body = w.get(p, ck)
			if code != http.StatusOK || strings.Contains(body, `action="/podcasts/start"`) || !strings.Contains(body, "The owner can turn this on in Settings.") {
				t.Errorf("non-owner %s: %d form=%v", p, code, strings.Contains(body, `action="/podcasts/start"`))
			}
		}
		if code, _ = w.get(p, w.guest); code != http.StatusNotFound {
			t.Errorf("guest %s: %d, want 404", p, code)
		}
	}
	// The nav item is there for every signed-in person, and for no guest.
	for _, ck := range []*http.Cookie{w.owner, w.member, w.contrib} {
		if _, body := w.get("/", ck); !strings.Contains(body, `data-nav="/podcasts"`) {
			t.Error("Podcasts nav item missing while both are off")
		}
	}
	if _, body := w.get("/", w.guest); strings.Contains(body, `data-nav="/podcasts"`) {
		t.Error("a guest sees the Podcasts item")
	}
	// The playback APIs answer as before: 404 while off.
	for _, p := range []string{"/api/v1/podcasts", "/api/v1/radio/stations", "/podcasts/opml", "/radio/live/rs_x", "/podcasts/audio/pe_x"} {
		if code, _ := w.get(p, w.owner); code != http.StatusNotFound {
			t.Errorf("%s while off: %d, want 404", p, code)
		}
	}
	if n := w.fx.ondaAll.Load(); n != 0 {
		t.Errorf("%d requests reached OndaCast before Turn on", n)
	}
}

func TestPodStartTurnOn(t *testing.T) {
	w := newPodStartWorld(t)
	for _, ck := range []*http.Cookie{w.member, w.contrib, w.guest} {
		if rec := w.post("/podcasts/start", ck, url.Values{"podcasts": {"1"}, "radio": {"1"}}); rec.Code != http.StatusNotFound {
			t.Errorf("non-owner POST: %d, want 404", rec.Code)
		}
	}
	if s := w.settings(); s.Enabled || s.RadioEnabled {
		t.Fatalf("a refused POST changed settings: %+v", s)
	}
	if rec := w.post("/podcasts/start", w.owner, nil); rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "Pick podcasts, radio, or both.") {
		t.Errorf("nothing ticked: %d", rec.Code)
	}
	if s := w.settings(); s.Enabled || s.RadioEnabled {
		t.Fatalf("an empty POST changed settings: %+v", s)
	}
	if rec := w.post("/podcasts/start", w.owner, url.Values{"radio": {"1"}}); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/radio" {
		t.Errorf("radio only: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if s := w.settings(); s.Enabled || !s.RadioEnabled {
		t.Errorf("radio only: %+v", s)
	}
	// Radio is on now: /podcasts keeps today's behaviour (404), /radio is the real page, not onboarding.
	if code, _ := w.get("/podcasts", w.owner); code != http.StatusNotFound {
		t.Errorf("podcasts off, radio on: /podcasts %d, want 404", code)
	}
	if code, body := w.get("/radio", w.owner); code != http.StatusOK || strings.Contains(body, "What turning it on does") {
		t.Errorf("radio on: %d", code)
	}
	if rec := w.post("/podcasts/start", w.owner, url.Values{"podcasts": {"1"}, "radio": {"1"}}); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/podcasts" {
		t.Errorf("both: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if s := w.settings(); !s.Enabled || !s.RadioEnabled {
		t.Errorf("both: %+v", s)
	}
	// Turning on never turns the other one off.
	if rec := w.post("/podcasts/start", w.owner, url.Values{"podcasts": {"1"}}); rec.Code != http.StatusSeeOther {
		t.Errorf("repeat: %d", rec.Code)
	}
	if s := w.settings(); !s.Enabled || !s.RadioEnabled {
		t.Errorf("repeat: %+v", s)
	}
}

func TestPodStartStartersOnShows(t *testing.T) {
	w := newPodWorld(t)
	if body := w.get("/podcasts", w.cy).Body.String(); true {
		for _, want := range []string{"Popular shows", "True crime", "More to try", "not a chart", `name="slug" value="crime-junkie"`, `action="/podcasts/follow"`, "/podcasts/search/art/"} {
			if !strings.Contains(body, want) {
				t.Errorf("empty Shows lacks %q", want)
			}
		}
		if strings.Index(body, "True crime") > strings.Index(body, "More to try") {
			t.Error("More to try comes before True crime")
		}
		if strings.Contains(body, "ondacast.com") || strings.Contains(body, "simplecastcdn") {
			t.Error("a picture address of another site is on the page")
		}
	}
	if w.fx.ondaHits.Load() != 0 || w.fx.audioHits.Load() != 0 {
		t.Errorf("suggestions reached OndaCast (%d) or a publisher (%d)", w.fx.ondaHits.Load(), w.fx.audioHits.Load())
	}
	// Someone who follows a show still sees the suggestions under it (item 5, 2026-11-10), and the search page too.
	if body := w.get("/podcasts", w.ben).Body.String(); !strings.Contains(body, "Popular shows") {
		t.Error("starters missing for someone who follows a show")
	}
	if body := w.get("/podcasts/search", w.ben).Body.String(); !strings.Contains(body, "Popular shows") || !strings.Contains(body, `value="crime-junkie"`) {
		t.Error("search page without a query lacks the starters")
	}
	if body := w.get("/podcasts/search?q=found", w.ben).Body.String(); strings.Contains(body, "Popular shows") {
		t.Error("starters shown beside search results")
	}
	// Following a suggestion goes through the same route a search result uses: by slug.
	rec := w.post(t, "/podcasts/follow", w.cy, url.Values{"slug": {"found-show"}})
	if rec.Code != http.StatusSeeOther {
		t.Errorf("follow by slug: %d", rec.Code)
	}
}

func TestPodStartPopularStations(t *testing.T) {
	w := newRadioWorld(t)
	if code := w.get("/radio", w.cy).Code; code != http.StatusOK {
		t.Fatalf("radio: %d", code)
	}
	body := w.get("/radio", w.cy).Body.String()
	for _, want := range []string{"Popular stations", "Pop Central", `name="slug" value="pop-central"`, `action="/radio/stations"`, "/radio/search/art/", "Pop · US"} {
		if !strings.Contains(body, want) {
			t.Errorf("Radio lacks %q", want)
		}
	}
	for _, not := range []string{"Plain Http", "ondacast.com/api/logos"} {
		if strings.Contains(body, not) {
			t.Errorf("Radio shows %q", not)
		}
	}
	if n := w.fx.popular.Load(); n != 1 {
		t.Errorf("popular asked %d times for two views, want 1 (cached)", n)
	}
	if b := w.get("/radio/search", w.cy).Body.String(); !strings.Contains(b, "Popular stations") {
		t.Error("station search before a search lacks the popular list")
	}
	if b := w.get("/radio/search?q=cool", w.cy).Body.String(); strings.Contains(b, "Popular stations") {
		t.Error("popular list shown beside search results")
	}
	// A person with a saved station still sees the suggestions (item 5, 2026-11-10).
	w.add(w.ben, w.fx.liveURL())
	if b := w.get("/radio", w.ben).Body.String(); !strings.Contains(b, "Popular stations") {
		t.Error("popular list missing for someone with saved stations")
	}
}

func TestPodStartPopularDownIsAbsentAndNotRetried(t *testing.T) {
	w := newRadioWorld(t)
	w.fx.ondaDown.Store(true)
	for i := 0; i < 3; i++ {
		rec := w.get("/radio", w.cy)
		if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "Popular stations") || strings.Contains(rec.Body.String(), "role=\"alert\"") {
			t.Fatalf("view %d with OndaCast down: %d", i, rec.Code)
		}
	}
	if n := w.fx.ondaAll.Load(); n != 1 {
		t.Errorf("%d requests to a down OndaCast over three views, want 1", n)
	}
}

// A show followed by its feed address has no OndaCast slug; it is matched to a suggestion by title, whatever the
// case and spacing, and never to a slug that happens to read the same.
func TestPodStarterTitleKey(t *testing.T) {
	for _, c := range []struct {
		a, b string
		same bool
	}{
		{"Crime Junkie", "crime  junkie ", true},
		{"Dateline NBC", "Dateline NBC", true},
		{"Morbid", "Morbid: A True Crime Podcast", false},
	} {
		if got := podStarterTitleKey(c.a) == podStarterTitleKey(c.b); got != c.same {
			t.Errorf("%q and %q: same = %v, want %v", c.a, c.b, got, c.same)
		}
	}
	if podStarterTitleKey("morbid") == "morbid" {
		t.Error("a title key reads like a slug")
	}
}
