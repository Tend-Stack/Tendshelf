//go:build dev

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/metadata"
)

// TestVisualProvidersPage captures "Your own lookup sites" (empty, with two sites and a refused add) in the
// visualMatrix themes and widths. The page has no board (it follows the Email page), so the captures are only
// recorded; every shot is also checked for overflow, targets under 44 px and inputs without a label.
func TestVisualProvidersPage(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	e := sweepSeed(t)
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if theme := r.URL.Query().Get("theme"); theme != "" {
			r.AddCookie(&http.Cookie{Name: themeCookie, Value: theme})
		}
		r.AddCookie(e.ck[sweepOwner])
		e.a.h.ServeHTTP(rw, r)
	}))
	t.Cleanup(srv.Close)
	capture := func(state, path string) {
		for _, sh := range visualMatrix(true) {
			theme, w := sh.Theme, sh.Width
			t.Run(fmt.Sprintf("providers-%s-%s-%d", state, theme, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w == 390, false)
				tab.Navigate(srv.URL + path + "&theme=" + url.QueryEscape(theme))
				tab.WaitFor(`document.querySelector(".ts-providers") !== null`, 10*time.Second)
				time.Sleep(500 * time.Millisecond)
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
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("providers-%s-%s-%d.png", state, theme, w)))
			})
		}
	}
	capture("empty", "/admin/providers?x=1")
	owner := e.uid[sweepOwner]
	for _, in := range []metadata.CustomProviderInput{
		{Name: "Audiobookshelf at home", Address: "http://192.168.1.20:3001", Auth: "Bearer x"},
		{Name: "A very long name for a site online ok", Address: "https://books.a-rather-long-hostname.example.org", Auth: ""},
	} {
		if _, err := e.a.srv.details.AddCustomProvider(t.Context(), in, owner); err != nil {
			t.Fatal(err)
		}
	}
	capture("list", "/admin/providers?added=1")
}
