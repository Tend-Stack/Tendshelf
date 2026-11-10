package podnet

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Hit is one OndaCast search result. OndaCast gives no feed address in a search; PodcastFeedURL asks for it.
type Hit struct {
	Slug      string
	Title     string
	Subtitle  string
	ImageURL  string
	StreamURL string
}

// StationHit is one OndaCast station.
type StationHit struct {
	Name      string
	StreamURL string
	Logo      string
	Genre     string
	Country   string
}

type cacheEntry struct {
	at    time.Time
	value any
}

var slugRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,127}$`)

// getJSON asks OndaCast for one path and decodes the answer into out. Answers are kept in memory for 10 minutes.
func (c *Client) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	if c.onda == nil {
		return refused()
	}
	u := *c.onda
	u.Path = strings.TrimRight(c.onda.Path, "/") + path
	u.RawQuery = q.Encode()
	key := u.String()
	if v, ok := c.cacheGet(key); ok {
		return json.Unmarshal(v, out)
	}
	ctx, cancel := context.WithTimeout(ctx, ondaTimeout)
	defer cancel()
	resp, _, err := c.do(ctx, key, KindOndaCast, map[string]string{"Accept": acceptJSON})
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := statusErr(resp.StatusCode); err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return &Error{Code: "bad_answer"}
	}
	b, err := readCapped(ctx, resp, ondaMax)
	if err != nil {
		return err
	}
	if json.Unmarshal(b, out) != nil {
		return &Error{Code: "bad_answer"}
	}
	c.cachePut(key, b)
	return nil
}

func (c *Client) cacheGet(key string) ([]byte, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.cache[key]
	if !ok || c.now().Sub(e.at) > ondaCacheTTL {
		return nil, false
	}
	return e.value.([]byte), true
}

func (c *Client) cachePut(key string, b []byte) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.cache[key]; !ok {
		c.cacheOrder = append(c.cacheOrder, key)
		for len(c.cacheOrder) > ondaCacheSize {
			delete(c.cache, c.cacheOrder[0])
			c.cacheOrder = c.cacheOrder[1:]
		}
	}
	c.cache[key] = cacheEntry{at: c.now(), value: b}
}

// Search asks OndaCast for podcasts or stations. Only the typed words and the kind are sent.
func (c *Client) Search(ctx context.Context, q, kind string, limit int) ([]Hit, error) {
	q = strings.TrimSpace(q)
	if n := utf8.RuneCountInString(q); n < 1 || n > 100 || !utf8.ValidString(q) {
		return nil, &Error{Code: "bad_answer"}
	}
	if kind != "podcasts" && kind != "stations" {
		return nil, &Error{Code: "bad_answer"}
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 25 {
		limit = 25
	}
	var ans struct {
		Success bool `json:"success"`
		Data    []struct {
			Title     string `json:"title"`
			Subtitle  string `json:"subtitle"`
			Slug      string `json:"slug"`
			ImageURL  string `json:"image_url"`
			StreamURL string `json:"stream_url"`
		} `json:"data"`
	}
	v := url.Values{"q": {q}, "limit": {strconv.Itoa(limit)}, "type": {kind}}
	if err := c.getJSON(ctx, "/api/v2/search", v, &ans); err != nil {
		return nil, err
	}
	if !ans.Success {
		return nil, &Error{Code: "bad_answer"}
	}
	out := make([]Hit, 0, len(ans.Data))
	for _, d := range ans.Data {
		if !slugRE.MatchString(d.Slug) || strings.TrimSpace(d.Title) == "" {
			continue
		}
		out = append(out, Hit{Slug: d.Slug, Title: d.Title, Subtitle: d.Subtitle, ImageURL: d.ImageURL, StreamURL: d.StreamURL})
	}
	return out, nil
}

// PodcastFeedURL returns the publisher's RSS address for an OndaCast slug.
func (c *Client) PodcastFeedURL(ctx context.Context, slug string) (string, error) {
	if !slugRE.MatchString(slug) {
		return "", &Error{Code: "not_found"}
	}
	var ans struct {
		Success bool `json:"success"`
		Data    struct {
			FeedURL string `json:"feed_url"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, "/api/v2/podcasts/"+slug, nil, &ans); err != nil {
		return "", err
	}
	if !ans.Success || strings.TrimSpace(ans.Data.FeedURL) == "" {
		return "", &Error{Code: "not_found"}
	}
	return strings.TrimSpace(ans.Data.FeedURL), nil
}

// Station returns a station's name and stream address for an OndaCast slug.
func (c *Client) Station(ctx context.Context, slug string) (StationHit, error) {
	if !slugRE.MatchString(slug) {
		return StationHit{}, &Error{Code: "not_found"}
	}
	var ans struct {
		Success bool `json:"success"`
		Data    struct {
			Name      string `json:"name"`
			StreamURL string `json:"streamUrl"`
			Logo      string `json:"logo"`
			Genre     string `json:"genre"`
			Country   string `json:"country"`
		} `json:"data"`
	}
	if err := c.getJSON(ctx, "/api/v2/embed/stations/"+slug, nil, &ans); err != nil {
		return StationHit{}, err
	}
	d := ans.Data
	if !ans.Success || strings.TrimSpace(d.StreamURL) == "" {
		return StationHit{}, &Error{Code: "not_found"}
	}
	return StationHit{Name: d.Name, StreamURL: strings.TrimSpace(d.StreamURL), Logo: AbsoluteOndaCast(strings.TrimSpace(d.Logo)), Genre: d.Genre, Country: d.Country}, nil
}
