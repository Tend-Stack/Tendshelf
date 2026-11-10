package web

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/podcast"
)

// TestRadioPages: the Radio page lists a person's own stations and nobody else's, never draws a stream address,
// carries a remove form and a play control, and a guest or a switched-off radio gets the plain 404.
func TestRadioPages(t *testing.T) {
	w := newRadioWorld(t)
	id := w.add(w.ben, w.fx.liveURL())

	empty := w.get("/radio", w.cy)
	if empty.Code != http.StatusOK || !strings.Contains(empty.Body.String(), "No stations yet.") || !strings.Contains(empty.Body.String(), `href="/radio/search"`) {
		t.Errorf("empty Radio: %d", empty.Code)
	}
	rec := w.get("/radio", w.ben)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("Radio: %d", rec.Code)
	}
	for _, want := range []string{"Your stations", `data-ts-play="` + id + `"`, `action="/radio/stations/` + id + `/remove"`, `href="/radio/live/` + id + `"`} {
		if !strings.Contains(body, want) {
			t.Errorf("Radio page lacks %q", want)
		}
	}
	for _, leak := range []string{"live.mp3", "radio.test", "Secret Upstream Name"} {
		if strings.Contains(body, leak) {
			t.Errorf("Radio page leaks %q", leak)
		}
	}
	if strings.Contains(empty.Body.String(), id) {
		t.Error("another person's station is on Cy's page")
	}
	if rec := w.get("/radio", w.guest); rec.Code != http.StatusNotFound {
		t.Errorf("guest Radio: %d, want 404", rec.Code)
	}
	if rec := w.get("/radio/search?q=cool", w.guest); rec.Code != http.StatusNotFound {
		t.Errorf("guest search: %d, want 404", rec.Code)
	}

	// Remove: the station leaves the list; the page answers the list again for htmx.
	if rec := w.post("/radio/stations/"+id+"/remove", w.ben, nil); rec.Code != http.StatusSeeOther {
		t.Errorf("remove: %d", rec.Code)
	}
	if got := w.get("/radio", w.ben).Body.String(); strings.Contains(got, `data-ts-play="`+id+`"`) && strings.Contains(got, "Your stations") {
		t.Error("a removed station is still listed as kept")
	}
}

// TestRadioSearchPage: the search asks OndaCast for stations, offers each hit with an Add form that posts only its
// slug, and still shows the stream address form when OndaCast is down.
func TestRadioSearchPage(t *testing.T) {
	w := newRadioWorld(t)
	page := w.get("/radio/search", w.ben).Body.String()
	for _, want := range []string{`name="stream_url"`, "Add by stream address", "Paste the station&#39;s stream address.", "Search stations"} {
		if !strings.Contains(page, want) {
			t.Errorf("search page lacks %q", want)
		}
	}
	if w.fx.searches.Load() != 0 {
		t.Error("an empty search page asked OndaCast")
	}
	hits := w.get("/radio/search?q=cool", w.ben).Body.String()
	if !strings.Contains(hits, "Cool FM") || !strings.Contains(hits, `name="slug" value="cool-fm"`) || !strings.Contains(hits, `action="/radio/stations"`) {
		t.Errorf("hits: %q", hits)
	}
	if strings.Contains(hits, "logo.test") {
		t.Error("a station logo address reached the page")
	}
	w.fx.ondaDown.Store(true)
	down := w.get("/radio/search?q=jazz", w.ben).Body.String()
	if !strings.Contains(down, "Station search isn&#39;t working right now.") || !strings.Contains(down, "<details") || !strings.Contains(down, " open") {
		t.Errorf("OndaCast down: the failure words or the open stream address form are missing")
	}
	// Adding a hit by slug saves it and lands on the list.
	w.fx.ondaDown.Store(false)
	if rec := w.post("/radio/stations", w.ben, url.Values{"slug": {"cool-fm"}}); rec.Code != http.StatusSeeOther {
		t.Fatalf("add by slug: %d %q", rec.Code, rec.Body)
	}
	if got := w.get("/radio", w.ben).Body.String(); !strings.Contains(got, "Cool FM") || !strings.Contains(got, "Pop") {
		t.Errorf("the added hit is not listed: %q", got)
	}
}

// TestRadioPlayerFragment: the live player's data is for the person's own station only, carries kind radio and the
// relay address, and no stream address or title of the station.
func TestRadioPlayerFragment(t *testing.T) {
	w := newRadioWorld(t)
	id := w.add(w.ben, w.fx.liveURL())
	rec := w.get("/hx/player/radio/"+id, w.ben)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("fragment: %d", rec.Code)
	}
	for _, want := range []string{`data-kind="radio"`, `data-href="/radio/live/` + id + `"`, `data-book-id="` + id + `"`, `data-duration-ms="0"`} {
		if !strings.Contains(body, want) {
			t.Errorf("fragment lacks %q: %s", want, body)
		}
	}
	if strings.Contains(body, "live.mp3") || strings.Contains(body, "radio.test") {
		t.Error("the stream address reached the fragment")
	}
	if rec := w.get("/hx/player/radio/"+id, w.cy); rec.Code != http.StatusNotFound {
		t.Errorf("stranger: %d, want 404", rec.Code)
	}
	if rec := w.get("/hx/player/radio/"+id, w.guest); rec.Code != http.StatusNotFound {
		t.Errorf("guest: %d, want 404", rec.Code)
	}
}

// TestRadioNavAndTabs: with podcasts off the nav item reads Radio and leads to /radio; with podcasts on it stays Podcasts
// and Radio is the last tab of the Podcasts area, on both pages; with radio off no Radio tab or item is drawn.
func TestRadioNavAndTabs(t *testing.T) {
	w := newRadioWorld(t)
	page := w.get("/radio", w.ben).Body.String()
	if !strings.Contains(page, `href="/radio"`) || strings.Contains(page, `href="/podcasts"`) || strings.Contains(page, `aria-label="Podcasts and radio"`) {
		t.Error("radio only: the nav item should be Radio and there is no tab strip")
	}
	s := podcast.Defaults()
	s.Enabled, s.RadioEnabled = true, true
	if err := podcast.SaveSettings(context.Background(), w.a.st, "", s, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"/radio", "/podcasts"} {
		body := w.get(p, w.ben).Body.String()
		if !strings.Contains(body, `aria-label="Podcasts and radio"`) && !strings.Contains(body, `class="ts-tabs__item" href="/radio"`) {
			t.Errorf("%s: no Radio tab", p)
		}
		if !strings.Contains(body, `href="/podcasts"`) {
			t.Errorf("%s: the Podcasts item is gone", p)
		}
	}
	s.RadioEnabled = false
	if err := podcast.SaveSettings(context.Background(), w.a.st, "", s, time.Now()); err != nil {
		t.Fatal(err)
	}
	if body := w.get("/podcasts", w.ben).Body.String(); strings.Contains(body, `class="ts-tabs__item" href="/radio"`) {
		t.Error("a Radio tab is drawn with radio off")
	}
}
