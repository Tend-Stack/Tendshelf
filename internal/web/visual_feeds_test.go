//go:build dev

package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// TestFeedsVisual captures the page that makes a podcast feed address, the one-time page that shows it, and Your
// devices with two feeds, at 390, 768, 1024 and 1440 px in each theme. The address and the QR code hold a random
// token, so the capture replaces both with fixed ones before the screenshot. It also checks no overflow, targets of
// 44 px and labelled fields.
func TestFeedsVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	l := newFeedLab(t)
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if theme := r.URL.Query().Get("theme"); theme != "" {
			r.AddCookie(&http.Cookie{Name: themeCookie, Value: theme})
		}
		r.AddCookie(l.owner)
		r.Host = "tendshelf.example" // a host a phone could reach, so the page shows a QR code
		l.a.h.ServeHTTP(rw, r)
	}))
	t.Cleanup(srv.Close)

	fixed := "https://tendshelf.example/feeds/Zm9vYmFyYmF6cXV4cXV1eGNvcmdlZ3JhdWx0Z2Fy/feed.xml"
	qr, ok := qrView(fixed, feedQRLabel)
	if !ok {
		t.Fatal("no QR code")
	}
	path, err := json.Marshal(qr.Path)
	if err != nil {
		t.Fatal(err)
	}
	probe := func(t *testing.T, tab *cdp.Tab) {
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
	}
	shoot := func(state, route string, make bool) {
		for _, sh := range visualMatrix(false) {
			theme, w := sh.Theme, sh.Width
			t.Run(fmt.Sprintf("%s-%s-%d", state, theme, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w == 390, false)
				tab.Navigate(srv.URL + route + "?theme=" + theme)
				tab.WaitFor(`document.querySelector(".ts-akey") !== null`, 10*time.Second)
				if make {
					tab.Eval(`(document.getElementById("feed-name").value = "Pocket Casts", document.querySelector(".ts-akey__form button").click())`, nil)
					tab.WaitFor(`document.getElementById("feed-address") !== null`, 10*time.Second)
					tab.Eval(fmt.Sprintf(`(document.getElementById("feed-address").value = %q,
						document.querySelector(".ts-qr__ink").setAttribute("d", %s))`, fixed, path), nil)
				}
				probe(t, tab)
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("feeds-%s-%s-%d.png", state, theme, w)))
			})
		}
	}

	shoot("make", "/books/"+l.single.ID+"/feed", false)
	shoot("shown", "/books/"+l.single.ID+"/feed", true)
	l.exec(t, `DELETE FROM podcast_feeds`) // the captures above each made one
	_, f1 := l.mk(t, "owner", l.single.ID, "")
	_, f2 := l.mk(t, "owner", "", l.seriesID)
	used := l.clock.Add(-3 * time.Hour).UnixMilli()
	l.exec(t, `UPDATE podcast_feeds SET name = 'Pocket Casts', last_used_at = ? WHERE id = ?`, used, f1.ID)
	l.exec(t, `UPDATE podcast_feeds SET name = 'Overcast' WHERE id = ?`, f2.ID)
	shoot("devices", "/me/devices", false)
}
