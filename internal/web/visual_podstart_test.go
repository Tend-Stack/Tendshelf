//go:build dev

package web

import (
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
)

// TestPodStartVisual captures the first-use page (owner and member), the empty Shows page with the starter list and
// the Radio page with popular stations at 390 and 1440 px, and checks no overflow, targets of 44 px and labelled fields.
func TestPodStartVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	seedVisualIDs(t)

	serve := func(a *testApp, ck *http.Cookie) *httptest.Server {
		srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
			if theme := r.URL.Query().Get("theme"); theme != "" {
				r.AddCookie(&http.Cookie{Name: themeCookie, Value: theme})
			}
			r.AddCookie(ck)
			a.h.ServeHTTP(rw, r)
		}))
		t.Cleanup(srv.Close)
		return srv
	}
	shoot := func(t *testing.T, srv *httptest.Server, name, path, ready string) {
		for _, sh := range visualMatrix(false) {
			theme, w := sh.Theme, sh.Width
			t.Run(fmt.Sprintf("%s-%s-%d", name, theme, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w < 700, false)
				tab.Navigate(srv.URL + path + "?theme=" + theme)
				tab.WaitFor(`document.querySelector("`+ready+`") !== null`, 10*time.Second)
				var m struct {
					Over       bool     `json:"over"`
					Small      []string `json:"small"`
					Unlabelled []string `json:"unlabelled"`
				}
				tab.Eval(shareProbeJS, &m)
				if m.Over {
					t.Errorf("horizontal overflow")
				}
				if len(m.Small) > 0 {
					t.Errorf("targets under 44 px: %v", m.Small)
				}
				if len(m.Unlabelled) > 0 {
					t.Errorf("inputs without a label: %v", m.Unlabelled)
				}
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("podstart-%s-%s-%d.png", name, theme, w)))
			})
		}
	}

	t.Run("onboarding", func(t *testing.T) {
		w := newPodStartWorld(t)
		shoot(t, serve(w.a, w.owner), "onboarding-owner", "/podcasts", ".ts-podstartpage")
		shoot(t, serve(w.a, w.member), "onboarding-member", "/podcasts", ".ts-podstartpage")
	})
	t.Run("shows", func(t *testing.T) {
		w := newPodWorld(t)
		shoot(t, serve(w.a, w.cy), "shows-empty", "/podcasts", ".ts-podstart")
		shoot(t, serve(w.a, w.cy), "podcast-search", "/podcasts/search", ".ts-podstart")
	})
	t.Run("radio", func(t *testing.T) {
		w := newRadioWorld(t)
		shoot(t, serve(w.a, w.cy), "radio-popular", "/radio", ".ts-podstart")
	})
	// Podcast fixes 3: the in-page search field above followed shows with the suggestions under them, and the Radio
	// page with a logo-less station, a small-logo station (kept at its own size) and the suggestions under both.
	t.Run("shows-followed", func(t *testing.T) {
		w := newPodWorld(t)
		shoot(t, serve(w.a, w.ben), "shows-followed", "/podcasts", ".ts-podsearch__form")
	})
	t.Run("radio-saved", func(t *testing.T) {
		w := newRadioWorld(t)
		w.add(w.ben, w.fx.liveURL())
		sha := strings.Repeat("c4", 32)
		img := image.NewRGBA(image.Rect(0, 0, 120, 120))
		for y := 0; y < 120; y++ {
			for x := 0; x < 120; x++ {
				img.Set(x, y, color.RGBA{R: uint8(2 * x), G: 90, B: uint8(2 * y), A: 255})
			}
		}
		thumb := mediajobs.ThumbPath(w.a.srv.dataDir, sha, 640)
		if err := os.MkdirAll(filepath.Dir(thumb), 0o700); err != nil {
			t.Fatal(err)
		}
		f, err := os.Create(thumb)
		if err != nil {
			t.Fatal(err)
		}
		if err := jpeg.Encode(f, img, nil); err != nil {
			t.Fatal(err)
		}
		f.Close()
		wr := w.a.st.Writer()
		if _, err := wr.Exec(`INSERT INTO radio_stations(id, stream_url, name, art_path, created_at, updated_at) VALUES ('st_small','https://s.example/small','Small Logo FM','cache/podcast-art/` + sha + `.png',1,1)`); err != nil {
			t.Fatal(err)
		}
		if _, err := wr.Exec(`INSERT INTO radio_favourites(user_id, station_id, position, added_at) SELECT user_id, 'st_small', 99, 1 FROM radio_favourites LIMIT 1`); err != nil {
			t.Fatal(err)
		}
		shoot(t, serve(w.a, w.ben), "radio-saved", "/radio", ".ts-stncard")
	})
}
