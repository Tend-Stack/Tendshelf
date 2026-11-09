package podnet

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

const searchJSON = `{"success":true,"data":[
 {"id":1,"type":"podcast","title":"Show One","subtitle":"Sub","slug":"show-one","image_url":"https://img.example/1.jpg","stream_url":null,"rank":3},
 {"id":2,"type":"podcast","title":"Bad Slug","slug":"../etc","image_url":null},
 {"id":"x3","type":"podcast","title":"Show Two","slug":"show-two"}]}`

func ondaHandler(t *testing.T, queries *[]url.Values) http.HandlerFunc {
	return func(w http.ResponseWriter, q *http.Request) {
		if q.Header.Get("Cookie") != "" || q.Header.Get("Authorization") != "" {
			t.Error("identifier sent")
		}
		*queries = append(*queries, q.URL.Query())
		switch {
		case q.URL.Path == "/api/v2/search":
			io.WriteString(w, searchJSON)
		case q.URL.Path == "/api/v2/podcasts/show-one":
			io.WriteString(w, `{"success":true,"data":{"feed_url":"https://pub.example/feed.xml"}}`)
		case q.URL.Path == "/api/v2/podcasts/no-feed":
			io.WriteString(w, `{"success":true,"data":{"feed_url":""}}`)
		case q.URL.Path == "/api/v2/embed/stations/radio-1":
			io.WriteString(w, `{"success":true,"data":{"streamUrl":"http://s.example:8000/live","name":"Radio 1","logo":"https://l.example/l.png","genre":"Pop","country":"US"}}`)
		default:
			w.WriteHeader(404)
		}
	}
}

func TestOndaCast(t *testing.T) {
	var queries []url.Values
	r := newRig(t, ondaHandler(t, &queries))
	ctx := context.Background()
	hits, err := r.c.Search(ctx, "  my show ", "podcasts", 100)
	if err != nil || len(hits) != 2 || hits[0].Slug != "show-one" || hits[0].Subtitle != "Sub" || hits[1].Slug != "show-two" {
		t.Fatalf("search: %v %+v", err, hits)
	}
	q := queries[0]
	if q.Get("q") != "my show" || q.Get("limit") != "25" || q.Get("type") != "podcasts" || len(q) != 3 {
		t.Errorf("query %v", q)
	}
	if _, err = r.c.Search(ctx, "my show", "podcasts", 100); err != nil || len(queries) != 1 {
		t.Errorf("cache miss: %v %d", err, len(queries))
	}
	feed, err := r.c.PodcastFeedURL(ctx, "show-one")
	if err != nil || feed != "https://pub.example/feed.xml" {
		t.Errorf("feed: %v %q", err, feed)
	}
	if _, err = r.c.PodcastFeedURL(ctx, "no-feed"); code(err) != "not_found" {
		t.Errorf("no feed: %v", err)
	}
	if _, err = r.c.PodcastFeedURL(ctx, "missing"); code(err) != "not_found" {
		t.Errorf("missing: %v", err)
	}
	st, err := r.c.Station(ctx, "radio-1")
	if err != nil || st.Name != "Radio 1" || st.StreamURL != "http://s.example:8000/live" || st.Country != "US" || st.Genre != "Pop" {
		t.Errorf("station: %v %+v", err, st)
	}
}

func TestOndaCastBadInputMakesNoRequest(t *testing.T) {
	var queries []url.Values
	r := newRig(t, ondaHandler(t, &queries))
	ctx := context.Background()
	for _, slug := range []string{"", "../x", "A", "-a", "a/b", "a?b", strings.Repeat("a", 129)} {
		if _, err := r.c.PodcastFeedURL(ctx, slug); err == nil {
			t.Errorf("slug %q accepted", slug)
		}
		if _, err := r.c.Station(ctx, slug); err == nil {
			t.Errorf("station slug %q accepted", slug)
		}
	}
	for _, q := range []string{"", "   ", strings.Repeat("é", 101)} {
		if _, err := r.c.Search(ctx, q, "podcasts", 5); err == nil {
			t.Errorf("query %q accepted", q)
		}
	}
	if _, err := r.c.Search(ctx, "x", "movies", 5); err == nil {
		t.Error("kind accepted")
	}
	if r.hits.Load() != 0 {
		t.Errorf("%d requests", r.hits.Load())
	}
}

func TestOndaCastCacheExpiresAndIsBounded(t *testing.T) {
	var queries []url.Values
	r := newRig(t, ondaHandler(t, &queries))
	now := time.Unix(1000, 0)
	r.c.now = func() time.Time { return now }
	ctx := context.Background()
	r.c.Search(ctx, "a", "podcasts", 5)
	now = now.Add(9 * time.Minute)
	r.c.Search(ctx, "a", "podcasts", 5)
	if len(queries) != 1 {
		t.Fatalf("expected a hit, %d requests", len(queries))
	}
	now = now.Add(2 * time.Minute)
	r.c.Search(ctx, "a", "podcasts", 5)
	if len(queries) != 2 {
		t.Fatalf("expected expiry, %d requests", len(queries))
	}
	for i := 0; i < 600; i++ {
		r.c.Search(ctx, fmt.Sprintf("q%d", i), "podcasts", 5)
	}
	if len(r.c.cache) > ondaCacheSize || len(r.c.cacheOrder) > ondaCacheSize {
		t.Errorf("cache grew to %d", len(r.c.cache))
	}
}

func TestOndaCastOnlyOnItsHost(t *testing.T) {
	// The default client talks https to ondacast.com only: a redirect elsewhere is refused.
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {
		http.Redirect(w, q, "http://other.example:8000/x", 302)
	})
	if _, err := r.c.Search(context.Background(), "x", "podcasts", 5); code(err) != "refused" {
		t.Fatalf("got %v", err)
	}
	def := New(Options{})
	if def.onda.String() != "https://ondacast.com" {
		t.Fatalf("default base %v", def.onda)
	}
}
