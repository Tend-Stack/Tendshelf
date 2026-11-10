//go:build dev

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"
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
		for _, w := range []int{390, 1440} {
			t.Run(fmt.Sprintf("%s-%d", name, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w < 700, false)
				tab.Navigate(srv.URL + path + "?theme=dark")
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
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("podstart-%s-%d.png", name, w)))
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
}
