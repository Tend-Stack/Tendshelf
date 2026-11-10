package podnet

import (
	"context"
	"io"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"
)

const popularJSON = `{"success":true,"data":[
 {"name":"Good FM","slug":"good-fm","stream_url":"https://s.example/live.mp3","logo":"/api/logos/good.png","genre":"Pop","country":"US","city":"Austin","listeners":9},
 {"name":"Plain","slug":"plain","stream_url":"http://s.example/live"},
 {"name":"Hls","slug":"hls","stream_url":"https://s.example/live/index.M3U8"},
 {"name":"","slug":"nameless","stream_url":"https://s.example/x"},
 {"name":"Bad","slug":"../x","stream_url":"https://s.example/x"},
 {"name":"Far Logo","slug":"far-logo","stream_url":"https://s.example/y","logo":"https://l.example/l.png"}]}`

func TestPodStartPopularStations(t *testing.T) {
	var asks int
	var gotQuery string
	fail := false
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {
		asks++
		gotQuery = q.URL.RawQuery
		if q.Header.Get("Cookie") != "" || q.Header.Get("Authorization") != "" {
			t.Error("identifier sent")
		}
		if fail {
			w.WriteHeader(500)
			return
		}
		io.WriteString(w, popularJSON)
	})
	now := time.Unix(5000, 0)
	r.c.now = func() time.Time { return now }
	ctx := context.Background()
	got, err := r.c.PopularStations(ctx, 100)
	if err != nil || len(got) != 2 || got[0].Slug != "good-fm" || got[1].Slug != "far-logo" {
		t.Fatalf("parse: %v %+v", err, got)
	}
	if got[0].Logo != "https://ondacast.com/api/logos/good.png" || got[0].City != "Austin" || got[1].Logo != "https://l.example/l.png" {
		t.Errorf("logos: %+v", got)
	}
	if !strings.Contains(gotQuery, "limit=24") || !strings.Contains(gotQuery, "sort=popular") {
		t.Errorf("query %q", gotQuery)
	}
	now = now.Add(50 * time.Minute)
	if _, err = r.c.PopularStations(ctx, 10); err != nil || asks != 1 {
		t.Errorf("cache: %v asks=%d", err, asks)
	}
	now = now.Add(20 * time.Minute)
	fail = true
	if _, err = r.c.PopularStations(ctx, 24); err == nil || asks != 2 {
		t.Fatalf("expiry: %v asks=%d", err, asks)
	}
	// The failure is remembered for a few minutes, then asked again.
	now = now.Add(2 * time.Minute)
	if _, err = r.c.PopularStations(ctx, 24); err == nil || asks != 2 {
		t.Errorf("failure hold: %v asks=%d", err, asks)
	}
	now = now.Add(4 * time.Minute)
	fail = false
	if got, err = r.c.PopularStations(ctx, 24); err != nil || len(got) != 2 || asks != 3 {
		t.Errorf("retry: %v %d asks=%d", err, len(got), asks)
	}
}

func TestPodStartStarterShows(t *testing.T) {
	shows := StarterShows()
	if len(shows) < 20 {
		t.Fatalf("only %d starter shows", len(shows))
	}
	slugOK := regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)
	seen := map[string]bool{}
	moreSeen := false
	for i, s := range shows {
		if !slugOK.MatchString(s.Slug) {
			t.Errorf("slug %q", s.Slug)
		}
		if seen[s.Slug] {
			t.Errorf("duplicate %q", s.Slug)
		}
		seen[s.Slug] = true
		if strings.TrimSpace(s.Title) == "" {
			t.Errorf("%s: empty title", s.Slug)
		}
		if s.Group != StarterTrueCrime && s.Group != StarterMore {
			t.Errorf("%s: group %q", s.Slug, s.Group)
		}
		if s.Group == StarterMore {
			moreSeen = true
		} else if moreSeen {
			t.Errorf("%s (#%d): true crime after more", s.Slug, i)
		}
		if s.ImageURL != "" && !strings.HasPrefix(s.ImageURL, "https://") {
			t.Errorf("%s: image %q is not absolute https", s.Slug, s.ImageURL)
		}
	}
}
