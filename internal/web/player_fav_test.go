package web

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/radio"
)

// askJSON is how the full player's save / star button asks: the Accept header names JSON, as the bookmark does.
func askJSON(r *http.Request) { r.Header.Set("Accept", "application/json") }

func askHTMX(r *http.Request) { r.Header.Set("HX-Request", "true") }

// TestRadioSaveRemovePlayerAsked: the player's request gets 204 and the row changes; a plain form keeps its 303 and
// an htmx request keeps its page; refusals keep their codes with a short plain-text body.
func TestRadioSaveRemovePlayerAsked(t *testing.T) {
	w := newRadioWorld(t)
	ctx := context.Background()
	id := w.add(w.ben, w.fx.liveURL())
	saved := func() bool {
		st, err := w.fx.svc.Get(ctx, w.benID, id)
		return err == nil && st.Favourite
	}
	// A station in the player has been played, so it stays among Recent once removed.
	if err := w.fx.svc.NotePlay(ctx, w.benID, id); err != nil {
		t.Fatal(err)
	}
	rec := w.post("/radio/stations/"+id+"/remove", w.ben, nil, askJSON)
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 || saved() {
		t.Fatalf("remove: %d %q saved=%v", rec.Code, rec.Body, saved())
	}
	if rec = w.post("/radio/stations/"+id+"/save", w.ben, nil, askJSON); rec.Code != http.StatusNoContent || !saved() {
		t.Fatalf("save: %d %q saved=%v", rec.Code, rec.Body, saved())
	}
	// The other answers are today's: 303 for a form, the page for htmx.
	if rec = w.post("/radio/stations/"+id+"/remove", w.ben, nil); rec.Code != http.StatusSeeOther || rec.Header().Get("Location") != "/radio" {
		t.Errorf("form remove: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec = w.post("/radio/stations/"+id+"/save", w.ben, nil, askHTMX); rec.Code != http.StatusOK || rec.Header().Get("HX-Push-Url") != "/radio" {
		t.Errorf("htmx save: %d %v", rec.Code, rec.Header())
	}
	// Refusals: same codes, plain words and no page.
	for name, c := range map[string]struct {
		path string
		ck   *http.Cookie
	}{
		"stranger": {"/radio/stations/" + id + "/save", w.cy},
		"guest":    {"/radio/stations/" + id + "/save", w.guest},
		"unknown":  {"/radio/stations/rs_nope/save", w.ben},
		"remove":   {"/radio/stations/rs_nope/remove", w.ben},
	} {
		rec = w.post(c.path, c.ck, nil, askJSON)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "<") || strings.TrimSpace(rec.Body.String()) == "" {
			t.Errorf("%s: %d %q", name, rec.Code, rec.Body)
		}
	}
	// A full list: 422 with the words.
	if err := w.fx.svc.NotePlay(ctx, w.cyID, id); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < radio.MaxFavourites; i++ {
		in := radio.Input{StreamURL: w.fx.pub.URL("radio.test", "/fav"+strconv.Itoa(i)+".mp3")}
		if _, _, err := w.fx.svc.Save(ctx, w.cyID, in); err != nil {
			t.Fatal(err)
		}
	}
	rec = w.post("/radio/stations/"+id+"/save", w.cy, nil, askJSON)
	if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), "100 stations") || strings.Contains(rec.Body.String(), "<") {
		t.Errorf("full list: %d %q", rec.Code, rec.Body)
	}
	// The hourly allowance: the 31st attempt is 429 with words.
	for i := 0; i < 30; i++ {
		w.post("/radio/stations/"+id+"/save", w.ben, nil, askJSON)
	}
	rec = w.post("/radio/stations/"+id+"/save", w.ben, nil, askJSON)
	if rec.Code != http.StatusTooManyRequests || strings.Contains(rec.Body.String(), "<") || strings.TrimSpace(rec.Body.String()) == "" {
		t.Errorf("rate limit: %d %q", rec.Code, rec.Body)
	}
}

// TestPodcastFavouritePlayerAsked: the player's request gets 204 and the star changes; a form keeps its 303.
func TestPodcastFavouritePlayerAsked(t *testing.T) {
	w := newPodWorld(t)
	ctx := context.Background()
	ep := w.eps[0]
	starred := func() bool {
		e, err := w.fx.svc.Episode(ctx, w.benID, ep)
		return err == nil && e.State.Favourite
	}
	rec := w.post(t, "/podcasts/episodes/"+ep+"/favourite", w.ben, url.Values{"on": {"1"}}, askJSON)
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 || !starred() {
		t.Fatalf("star: %d %q %v", rec.Code, rec.Body, starred())
	}
	if rec = w.post(t, "/podcasts/episodes/"+ep+"/favourite", w.ben, url.Values{"on": {"0"}}, askJSON); rec.Code != http.StatusNoContent || starred() {
		t.Fatalf("unstar: %d %v", rec.Code, starred())
	}
	if rec = w.post(t, "/podcasts/episodes/"+ep+"/favourite", w.ben, url.Values{"on": {"1"}}); rec.Code != http.StatusSeeOther {
		t.Errorf("form star: %d", rec.Code)
	}
	if rec = w.post(t, "/podcasts/episodes/"+ep+"/favourite", w.ben, url.Values{"on": {"1"}}, askHTMX); rec.Code != http.StatusOK || rec.Header().Get("HX-Push-Url") == "" {
		t.Errorf("htmx star: %d %v", rec.Code, rec.Header())
	}
	rec = w.post(t, "/podcasts/episodes/"+ep+"/favourite", w.gus, url.Values{"on": {"1"}}, askJSON)
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "<") {
		t.Errorf("guest: %d %q", rec.Code, rec.Body)
	}
	rec = w.post(t, "/podcasts/episodes/nope/favourite", w.ben, url.Values{"on": {"1"}}, askJSON)
	if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "<") {
		t.Errorf("unknown: %d %q", rec.Code, rec.Body)
	}
}

// TestPlayerDataFavourite: data-favourite="1" only for a saved station and a starred episode.
func TestPlayerDataFavourite(t *testing.T) {
	r := newRadioWorld(t)
	ctx := context.Background()
	id := r.add(r.ben, r.fx.liveURL())
	if b := r.get("/hx/player/radio/"+id, r.ben).Body.String(); !strings.Contains(b, `data-favourite="1"`) {
		t.Errorf("saved station: %s", b)
	}
	if err := r.fx.svc.Remove(ctx, r.benID, id); err != nil {
		t.Fatal(err)
	}
	if b := r.get("/hx/player/radio/"+id, r.ben).Body.String(); strings.Contains(b, "data-favourite") {
		t.Errorf("removed station still favourite: %s", b)
	}
	p := newPodWorld(t)
	ep := p.eps[0]
	if b := p.get("/hx/player/episode/"+ep, p.ben).Body.String(); strings.Contains(b, "data-favourite") {
		t.Errorf("plain episode: %s", b)
	}
	if err := p.fx.svc.SetFavourite(ctx, p.benID, ep, true); err != nil {
		t.Fatal(err)
	}
	if b := p.get("/hx/player/episode/"+ep, p.ben).Body.String(); !strings.Contains(b, `data-favourite="1"`) {
		t.Errorf("starred episode: %s", b)
	}
}
