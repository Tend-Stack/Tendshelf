//go:build dev

package web

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/podcast"
	"github.com/Tend-Stack/tendshelf/internal/podcast/rss"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// podVisualWorld is an app with podcasts on and three followed shows (one with a very long title and no picture, one with
// a generated picture), a few dozen episodes, some played, started or starred. Dates are fixed so captures repeat. The
// ids are seeded (seedVisualIDs), and the data folder has a fixed path.
type podVisual struct {
	a     *testApp
	owner *http.Cookie
	shows map[string]string // title -> id
	eps   []string          // episode ids of the first show, newest first
}

func newPodVisual(t *testing.T) *podVisual {
	t.Helper()
	seedVisualIDs(t)
	folder := visualTempDir(t)
	fx := newPodFix(t)
	clock := time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)
	a := newTestApp(t, func(o *Options) {
		o.DataDir = folder
		o.Now = func() time.Time { return clock }
		fx.mod(t)(o)
	})
	fx.enable(t, a, nil)
	v := &podVisual{a: a, owner: a.as(t, users.RoleOwner), shows: map[string]string{}}
	uid := sweepUserID(t, a, v.owner)
	ctx := context.Background()
	defs := []struct {
		title, author, notes string
		n                    int
		art                  bool
	}{
		{"The Harbour Hour", "Ann Author", "<p>A weekly walk along the quay with people who keep the boats afloat.</p><p>New episodes every <strong>Tuesday</strong>. Say hello at <a href=\"https://example.com/harbour\">example.com/harbour</a>.</p>", 24, true},
		{"A Very Long Podcast Title About Lighthouses, Fog Signals and the People Who Kept Them Burning Through Every Winter", "Brian Beacon and the Northern Lights Society of Keepers", "<p>Short notes.</p>", 6, false},
		{"Quiet Shelves", "", "", 3, true},
	}
	for i, d := range defs {
		feed := "https://pub.test/" + fmt.Sprint(i) + ".xml"
		id, _, err := fx.svc.Follow(ctx, uid, feed, "")
		if err != nil {
			t.Fatal(err)
		}
		f := rss.Feed{Title: d.title, Author: d.author, Description: d.notes, Link: "https://example.com/" + fmt.Sprint(i)}
		for k := 1; k <= d.n; k++ {
			f.Episodes = append(f.Episodes, rss.Item{
				GUID: fmt.Sprintf("g%d-%d", i, k), GUIDKey: fmt.Sprintf("g%d-%d", i, k),
				Title:       fmt.Sprintf("Episode %d: %s", k, []string{"The tide tables", "Why the foghorn fell silent and what the harbour master did about it the next morning", "Rope", "A short one"}[k%4]),
				AudioURL:    fmt.Sprintf("https://pub.test/a%d-%d.mp3", i, k),
				AudioType:   "audio/mpeg",
				DurationMs:  int64(20+k*3) * 60 * 1000,
				PublishedAt: time.Date(2026, 10, 1+k%28, 6, 0, 0, 0, time.UTC).UnixMilli() + int64(k),
				NotesHTML:   "<p>Notes for episode " + fmt.Sprint(k) + ", with a <a href=\"https://example.com/e\">link</a>.</p>",
			})
		}
		if _, err := fx.svc.ApplyFeed(ctx, id, f, podcast.FetchMeta{}); err != nil {
			t.Fatal(err)
		}
		v.shows[d.title] = id
		if d.art {
			v.art(t, folder, id, i)
		}
	}
	main := v.shows[defs[0].title]
	rows, err := a.st.Reader().Query(`SELECT id FROM podcast_episodes WHERE show_id = ? ORDER BY published_at DESC, id`, main)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		rows.Scan(&id)
		v.eps = append(v.eps, id)
	}
	// Some state: two played, one started, two starred. The two newest stay new for the badge.
	for _, id := range v.eps[4:6] {
		if err := fx.svc.SetPlayed(ctx, uid, id, true); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := fx.svc.SaveProgress(ctx, uid, v.eps[3], podcast.Progress{PositionMs: 9 * 60 * 1000, ClientUpdatedAt: clock.UnixMilli(), DeviceID: "visual-device"}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{v.eps[2], v.eps[8]} {
		if err := fx.svc.SetFavourite(ctx, uid, id, true); err != nil {
			t.Fatal(err)
		}
	}
	// The person last looked before October 20, so the later episodes are new.
	if _, err := a.st.Writer().Exec(`UPDATE podcast_subscriptions SET seen_at = ?`, time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	return v
}

// art writes a generated picture into the art cache for a show and records it, so the local art route serves it.
func (v *podVisual) art(t *testing.T, folder, showID string, seed int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 400, 400))
	base := []color.RGBA{{200, 90, 50, 255}, {40, 110, 140, 255}, {90, 140, 80, 255}}[seed%3]
	for y := 0; y < 400; y++ {
		for x := 0; x < 400; x++ {
			k := uint8((x + y) / 4)
			img.Set(x, y, color.RGBA{base.R/2 + k/2, base.G/2 + k/3, base.B/2 + k/4, 255})
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, img); err != nil {
		t.Fatal(err)
	}
	sha := fmt.Sprintf("%064x", seed+1)
	rel := "cache/podcast-art/" + sha + ".png"
	if err := os.MkdirAll(filepath.Join(folder, "cache", "podcast-art"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, filepath.FromSlash(rel)), b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := v.a.st.Writer().Exec(`UPDATE podcast_shows SET art_path = ?, art_sha = ? WHERE id = ?`, rel, sha, showID); err != nil {
		t.Fatal(err)
	}
}

// TestPodcastsVisual captures the podcast pages at 390, 768, 1024 and 1440 px in the visualMatrix themes (and the show
// page at 360), and checks no overflow, targets of 44 px and labelled fields.
func TestPodcastsVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	v := newPodVisual(t)
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if theme := r.URL.Query().Get("theme"); theme != "" {
			r.AddCookie(&http.Cookie{Name: themeCookie, Value: theme})
		}
		r.AddCookie(v.owner)
		v.a.h.ServeHTTP(rw, r)
	}))
	t.Cleanup(srv.Close)
	main := v.shows["The Harbour Hour"]
	pagesToShoot := []struct{ name, path, ready string }{
		{"home", "/podcasts", ".ts-podcasts"},
		{"new", "/podcasts?tab=new", ".ts-eprow"},
		{"favourites", "/podcasts?tab=favourites", ".ts-eprow"},
		{"search", "/podcasts/search?q=found", ".ts-podsearch"},
		{"show", "/podcasts/shows/" + main, ".ts-podshow"},
		{"show-oldest-unplayed", "/podcasts/shows/" + main + "?sort=oldest&filter=unplayed", ".ts-podshow"},
		{"show-long", "/podcasts/shows/" + v.shows["A Very Long Podcast Title About Lighthouses, Fog Signals and the People Who Kept Them Burning Through Every Winter"], ".ts-podshow"},
		{"episode", "/podcasts/episodes/" + v.eps[3], ".ts-podep"},
		{"admin", "/admin/podcasts", ".ts-podadmin"},
	}
	for _, p := range pagesToShoot {
		shots := visualMatrix(p.name == "home" || p.name == "show")
		if p.name == "show" || p.name == "show-long" {
			shots = append(shots, visualShot{"dark", 360})
		}
		for _, sh := range shots {
			theme, w := sh.Theme, sh.Width
			t.Run(fmt.Sprintf("%s-%s-%d", p.name, theme, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w < 700, false)
				sep := "?"
				if len(p.path) > 0 && containsQuery(p.path) {
					sep = "&"
				}
				tab.Navigate(srv.URL + p.path + sep + "theme=" + theme)
				tab.WaitFor(`document.querySelector("`+p.ready+`") !== null`, 10*time.Second)
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
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("podcasts-%s-%s-%d.png", p.name, theme, w)))
			})
		}
	}
	t.Logf("screenshots in %s", out)
}
