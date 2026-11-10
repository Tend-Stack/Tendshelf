package podnet

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// PopularStation is one station from OndaCast's popular list.
type PopularStation struct {
	Slug      string
	Name      string
	StreamURL string
	Logo      string // absolute, or ""
	Genre     string
	Country   string
	City      string
}

const (
	popularMax      = 24
	popularTTL      = time.Hour
	popularFailHold = 5 * time.Minute // a failed ask is not repeated on every page view
)

// PopularStations asks OndaCast for its most popular stations (no account, no identifiers). The answer is kept for
// an hour and a failure is remembered for five minutes. Stations whose stream is not https are left out; an HLS
// playlist stays in, since the relay plays HLS stations (decision 0039).
func (c *Client) PopularStations(ctx context.Context, limit int) ([]PopularStation, error) {
	if limit < 1 {
		limit = 1
	}
	if limit > popularMax {
		limit = popularMax
	}
	c.mu.Lock()
	if c.popFail != nil && c.now().Sub(c.popFailAt) < popularFailHold {
		err := c.popFail
		c.mu.Unlock()
		return nil, err
	}
	if c.popList != nil && c.now().Sub(c.popAt) < popularTTL && c.popLimit >= limit {
		out := capStations(c.popList, limit)
		c.mu.Unlock()
		return out, nil
	}
	c.mu.Unlock()

	var ans struct {
		Success bool `json:"success"`
		Data    []struct {
			Name      string `json:"name"`
			Slug      string `json:"slug"`
			StreamURL string `json:"stream_url"`
			Logo      string `json:"logo"`
			Genre     string `json:"genre"`
			Country   string `json:"country"`
			City      string `json:"city"`
		} `json:"data"`
	}
	v := url.Values{"sort": {"popular"}, "limit": {strconv.Itoa(limit)}}
	err := c.getJSON(ctx, "/api/v2/stations", v, &ans)
	if err == nil && !ans.Success {
		err = &Error{Code: "bad_answer"}
	}
	if err != nil {
		c.mu.Lock()
		c.popFail, c.popFailAt = err, c.now()
		c.mu.Unlock()
		return nil, err
	}
	out := make([]PopularStation, 0, len(ans.Data))
	for _, d := range ans.Data {
		stream := strings.TrimSpace(d.StreamURL)
		if !slugRE.MatchString(d.Slug) || strings.TrimSpace(d.Name) == "" || !playableStream(stream) {
			continue
		}
		out = append(out, PopularStation{Slug: d.Slug, Name: d.Name, StreamURL: stream, Logo: AbsoluteOndaCast(strings.TrimSpace(d.Logo)),
			Genre: d.Genre, Country: d.Country, City: d.City})
	}
	c.mu.Lock()
	c.popList, c.popAt, c.popLimit, c.popFail = out, c.now(), limit, nil
	c.mu.Unlock()
	return out, nil
}

func capStations(l []PopularStation, n int) []PopularStation {
	if len(l) > n {
		return l[:n]
	}
	return l
}

// playableStream is true for an https address.
func playableStream(s string) bool {
	u, err := url.Parse(s)
	return err == nil && u.Scheme == "https" && u.Host != ""
}
