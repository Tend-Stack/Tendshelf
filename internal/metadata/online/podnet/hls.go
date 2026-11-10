package podnet

import (
	"context"
	"net/http"
	"time"
)

// HLS radio fetches (decision 0039). Playlists, initialisation sections and segments all go through
// the same guarded client as every other publisher request.
const (
	acceptPlaylist  = "application/vnd.apple.mpegurl, application/x-mpegurl, audio/mpegurl;q=0.9, */*;q=0.5"
	playlistMax     = 256 << 10
	playlistTimeout = 10 * time.Second
	segmentMax      = 4 << 20
	segmentMinWait  = 10 * time.Second
	segmentMaxWait  = 30 * time.Second
)

// Playlist fetches an HLS playlist (256 KiB, 10 s). finalURL is the address after redirects: relative
// addresses inside the playlist resolve against it.
func (c *Client) Playlist(ctx context.Context, rawURL string) (body []byte, finalURL string, err error) {
	ctx, cancel := context.WithTimeout(ctx, playlistTimeout)
	defer cancel()
	resp, _, err := c.do(ctx, rawURL, KindPublisher, map[string]string{"Accept": acceptPlaylist})
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if err := statusErr(resp.StatusCode); err != nil {
		return nil, "", err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", &Error{Code: "bad_answer"}
	}
	b, err := readCapped(ctx, resp, playlistMax)
	if err != nil {
		return nil, "", err
	}
	return b, resp.Request.URL.String(), nil
}

// Segment fetches one media segment or initialisation section (4 MiB). timeout is clamped to 10-30 s.
func (c *Client) Segment(ctx context.Context, rawURL string, timeout time.Duration) ([]byte, error) {
	timeout = min(max(timeout, segmentMinWait), segmentMaxWait)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	h := map[string]string{"Accept": "*/*", "Accept-Encoding": "identity"}
	resp, _, err := c.do(ctx, rawURL, KindPublisher, h)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := statusErr(resp.StatusCode); err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, &Error{Code: "bad_answer"}
	}
	return readCapped(ctx, resp, segmentMax)
}
