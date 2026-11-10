package web

import (
	"image"
	"image/jpeg"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/radio"
)

// Podcast and radio page fixes, round 3 (2026-11-10).

func TestPodFix3SearchFieldOnEveryTab(t *testing.T) {
	w := newPodWorld(t)
	for _, tab := range []string{"", "?tab=new", "?tab=favourites", "?tab=history"} {
		b := w.get("/podcasts"+tab, w.ben).Body.String()
		for _, want := range []string{`role="search"`, `action="/podcasts/search"`, `placeholder="Search podcasts"`, `class="ts-podsearch__form"`} {
			if !strings.Contains(b, want) {
				t.Errorf("tab %q lacks %q", tab, want)
			}
		}
		if strings.Contains(b, "Find a podcast") {
			t.Errorf("tab %q still has the Find a podcast button", tab)
		}
		if tab != "" && strings.Contains(b, "Popular shows") {
			t.Errorf("tab %q shows suggestions", tab)
		}
	}
}

func TestPodFix3ShowsSuggestionsLeaveOutFollowed(t *testing.T) {
	w := newPodWorld(t)
	b := w.get("/podcasts", w.ben).Body.String()
	if !strings.Contains(b, "Popular shows") || !strings.Contains(b, `value="crime-junkie"`) {
		t.Fatal("suggestions missing under the followed shows")
	}
	if _, err := w.a.st.Writer().Exec(`UPDATE podcast_shows SET ondacast_slug = 'crime-junkie' WHERE id = ?`, w.show); err != nil {
		t.Fatal(err)
	}
	b = w.get("/podcasts", w.ben).Body.String()
	if strings.Contains(b, `value="crime-junkie"`) {
		t.Error("a followed show is still suggested")
	}
	if !strings.Contains(b, "Popular shows") {
		t.Error("the other suggestions vanished")
	}
}

func TestPodFix3RadioSuggestionsLeaveOutSaved(t *testing.T) {
	w := newRadioWorld(t)
	id := w.add(w.ben, w.fx.liveURL())
	b := w.get("/radio", w.ben).Body.String()
	if !strings.Contains(b, "Popular stations") || !strings.Contains(b, `value="pop-central"`) {
		t.Fatal("suggestions missing under the saved stations")
	}
	if !strings.Contains(b, "ts-stnlogo") {
		t.Error("a station without a kept logo has no generated tile")
	}
	for _, set := range []string{`ondacast_slug = 'pop-central'`, `stream_url = 'https://s.example/pop'`} {
		if _, err := w.a.st.Writer().Exec(`UPDATE radio_stations SET ondacast_slug = NULL WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
		if _, err := w.a.st.Writer().Exec(`UPDATE radio_stations SET `+set+` WHERE id = ?`, id); err != nil {
			t.Fatal(err)
		}
		if b := w.get("/radio", w.ben).Body.String(); strings.Contains(b, `value="pop-central"`) {
			t.Errorf("a saved station is still suggested (matched by %s)", set)
		}
	}
}

func TestPodFix3RadioSuggestionsAbsentWhenDown(t *testing.T) {
	w := newRadioWorld(t)
	w.add(w.ben, w.fx.liveURL())
	w.fx.ondaDown.Store(true)
	rec := w.get("/radio", w.ben)
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "Popular stations") {
		t.Errorf("OndaCast down: %d", rec.Code)
	}
}

func TestPodFix3LogoSizeFallback(t *testing.T) {
	dir := t.TempDir()
	s := &server{dataDir: dir}
	sha := strings.Repeat("ab", 32)
	st := radio.Station{ID: "x", Name: "Z100", ArtPath: "cache/podcast-art/" + sha + ".png"}
	if w, h := s.radioLogoSize(st); w != 0 || h != 0 {
		t.Fatalf("size before a thumbnail exists: %d x %d", w, h)
	}
	p := mediajobs.ThumbPath(dir, sha, mediajobs.ThumbSizes[len(mediajobs.ThumbSizes)-1])
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Create(p)
	if err := jpeg.Encode(f, image.NewRGBA(image.Rect(0, 0, 32, 32)), nil); err != nil {
		t.Fatal(err)
	}
	f.Close()
	if w, h := s.radioLogoSize(st); w != 32 || h != 32 {
		t.Fatalf("size = %d x %d", w, h)
	}
	if c, small := s.radioArt(st); c.CoverSrc != "" || small {
		t.Errorf("a 32 px logo is drawn: %+v", c)
	}
	if u := s.playerArtURL(st); u != "" {
		t.Errorf("player gets a 32 px logo: %q", u)
	}
}
