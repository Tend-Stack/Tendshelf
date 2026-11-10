package podnet

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// FeedResult is what Feed returns. Body is empty when NotModified is true.
type FeedResult struct {
	Body         []byte
	FinalURL     string
	ETag         string
	LastModified string
	NotModified  bool
	Moved        bool // every redirect on the way was permanent (301 or 308); FinalURL is the new address
}

// Upstream is an open publisher answer. Header carries only the headers a player needs.
type Upstream struct {
	Status int
	Header http.Header
	Body   io.ReadCloser // gives up after 60 s without a byte
}

const (
	acceptFeed    = "application/rss+xml, application/atom+xml, application/xml;q=0.9, text/xml;q=0.8, */*;q=0.5"
	acceptPicture = "image/jpeg, image/png, image/webp"
	acceptJSON    = "application/json"
	acceptAudio   = "audio/*, */*;q=0.5"
)

// readCapped reads a whole body of at most max bytes.
func readCapped(ctx context.Context, resp *http.Response, max int64) ([]byte, error) {
	if resp.ContentLength > max {
		return nil, &Error{Code: "too_large"}
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, mapErr(ctx, err)
	}
	if int64(len(b)) > max {
		return nil, &Error{Code: "too_large"}
	}
	return b, nil
}

// Feed fetches a feed with a conditional GET. 304 gives NotModified, 404 and 410 not_found, 429 busy.
func (c *Client) Feed(ctx context.Context, rawURL, etag, lastMod string) (FeedResult, error) {
	ctx, cancel := context.WithTimeout(ctx, c.feedTimeout)
	defer cancel()
	h := map[string]string{"Accept": acceptFeed}
	if etag != "" {
		h["If-None-Match"] = etag
	}
	if lastMod != "" {
		h["If-Modified-Since"] = lastMod
	}
	resp, tr, err := c.do(ctx, rawURL, KindPublisher, h)
	if err != nil {
		return FeedResult{}, err
	}
	defer resp.Body.Close()
	res := FeedResult{
		FinalURL:     resp.Request.URL.String(),
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		Moved:        tr.hops > 0 && tr.permanent,
	}
	if resp.StatusCode == http.StatusNotModified {
		res.NotModified = true
		return res, nil
	}
	if err := statusErr(resp.StatusCode); err != nil {
		return FeedResult{}, err
	}
	if resp.StatusCode != http.StatusOK {
		return FeedResult{}, &Error{Code: "bad_answer"}
	}
	if res.Body, err = readCapped(ctx, resp, feedMax); err != nil {
		return FeedResult{}, err
	}
	return res, nil
}

// Picture fetches artwork (5 MiB; JPEG, PNG or WebP, decided by the bytes, not the header).
func (c *Client) Picture(ctx context.Context, rawURL string) ([]byte, string, error) {
	ctx, cancel := context.WithTimeout(ctx, c.feedTimeout)
	defer cancel()
	resp, _, err := c.do(ctx, rawURL, KindPublisher, map[string]string{"Accept": acceptPicture})
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
	b, err := readCapped(ctx, resp, pictureMax)
	if err != nil {
		return nil, "", err
	}
	switch mime := http.DetectContentType(b); mime {
	case "image/jpeg", "image/png", "image/webp":
		return b, mime, nil
	}
	return nil, "", &Error{Code: "bad_answer"}
}

// Chapters fetches a Podcasting 2.0 chapters file (1 MiB of JSON).
func (c *Client) Chapters(ctx context.Context, rawURL string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.feedTimeout)
	defer cancel()
	resp, _, err := c.do(ctx, rawURL, KindPublisher, map[string]string{"Accept": acceptJSON})
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
	b, err := readCapped(ctx, resp, chaptersMax)
	if err != nil {
		return nil, err
	}
	if !json.Valid(b) {
		return nil, &Error{Code: "bad_answer"}
	}
	return b, nil
}

// singleRange accepts one byte range and nothing else, so no other client text reaches a publisher.
var singleRange = regexp.MustCompile(`^bytes=\d*-\d*$`)

// OpenAudio opens an episode's audio, passing one Range through. The caller closes Body.
// Status is 200, 206 or 416; every other failure is an Error.
func (c *Client) OpenAudio(ctx context.Context, rawURL, rangeHdr string) (*Upstream, error) {
	h := map[string]string{"Accept": acceptAudio, "Accept-Encoding": "identity"}
	if singleRange.MatchString(rangeHdr) {
		h["Range"] = rangeHdr
	}
	up, _, err := c.openStream(ctx, rawURL, h)
	return up, err
}

// OpenLive opens a station's stream, asking for in-stream titles. metaint is the number of audio
// bytes between title blocks, or 0 when the station sends none.
func (c *Client) OpenLive(ctx context.Context, rawURL string) (*Upstream, int, error) {
	h := map[string]string{"Accept": acceptAudio, "Accept-Encoding": "identity", "Icy-MetaData": "1"}
	up, resp, err := c.openStream(ctx, rawURL, h)
	if err != nil {
		return nil, 0, err
	}
	metaint := 0
	if v := resp.Header.Get("Icy-Metaint"); v != "" {
		// A station that names a block size outside the bound is not speaking the protocol; passing its
		// bytes through would put title blocks into the audio.
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 1<<20 {
			up.Body.Close()
			return nil, 0, &Error{Code: "bad_answer"}
		}
		metaint = n
	}
	return up, metaint, nil
}

func (c *Client) openStream(ctx context.Context, rawURL string, h map[string]string) (*Upstream, *http.Response, error) {
	sctx, cancel := context.WithCancel(ctx)
	resp, _, err := c.do(sctx, rawURL, KindPublisher, h)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if resp.StatusCode != 416 {
		if err := statusErr(resp.StatusCode); err != nil {
			resp.Body.Close()
			cancel()
			return nil, nil, err
		}
		if resp.StatusCode != 200 && resp.StatusCode != 206 {
			resp.Body.Close()
			cancel()
			return nil, nil, &Error{Code: "bad_answer"}
		}
	}
	hdr := http.Header{}
	for _, k := range []string{"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges", "Last-Modified", "ETag"} {
		if v := resp.Header.Get(k); v != "" {
			hdr.Set(k, v)
		}
	}
	body := &idleBody{rc: resp.Body, cancel: cancel, idle: c.idleTimeout}
	body.timer = time.AfterFunc(c.idleTimeout, cancel)
	return &Upstream{Status: resp.StatusCode, Header: hdr, Body: body}, resp, nil
}

// idleBody cancels the request when no byte arrives for the idle time.
type idleBody struct {
	rc     io.ReadCloser
	cancel context.CancelFunc
	timer  *time.Timer
	idle   time.Duration
	once   sync.Once
}

func (b *idleBody) Read(p []byte) (int, error) {
	b.timer.Reset(b.idle)
	return b.rc.Read(p)
}

func (b *idleBody) Close() error {
	b.once.Do(func() { b.timer.Stop() })
	err := b.rc.Close()
	b.cancel()
	return err
}
