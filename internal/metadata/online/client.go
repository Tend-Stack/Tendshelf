// Ports the Extractor extractor_online.py (Fetcher, ALLOWED_HOSTS, spacing); see NOTICES.md.

// Package online looks up book details on a short list of public sites. It is the only package
// that builds an http.Client: every request, including each redirect hop, passes guardTransport.
// Only the title, author, ASIN and ISBN of a query ever leave the machine.
package online

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	requestTimeout = 10 * time.Second
	maxJSONBytes   = 2 << 20
	maxCoverBytes  = 5 << 20
	cacheTTL       = 7 * 24 * time.Hour
	maxRedirects   = 3
	defaultRetry   = 5 * time.Second
	defaultUA      = "Tendshelf (self-hosted audiobook server; book details lookup)"
)

// apiHosts are the sites a lookup may ask; the value is the plain name used in error texts.
var apiHosts = map[string]string{
	"api.audnex.us":      "Audnexus",
	"api.audible.com":    "Audible",
	"www.googleapis.com": "Google Books",
	"openlibrary.org":    "Open Library",
	"itunes.apple.com":   "Apple Books",
}

// coverHostSuffixes and coverHostsExact are the sites a cover picture may come from.
var (
	coverHostSuffixes = []string{".media-amazon.com", ".mzstatic.com", ".googleusercontent.com", ".gstatic.com", ".archive.org"}
	coverHostsExact   = []string{"covers.openlibrary.org", "books.google.com", "archive.org"}
)

// hostGap is the minimum time between two requests to the same host.
func hostGap(host string) time.Duration {
	if host == "itunes.apple.com" {
		return time.Second
	}
	if _, ok := apiHosts[host]; ok {
		return 500 * time.Millisecond
	}
	return time.Second
}

func isCoverHost(host string) bool {
	for _, h := range coverHostsExact {
		if host == h {
			return true
		}
	}
	for _, s := range coverHostSuffixes {
		if strings.HasSuffix(host, s) {
			return true
		}
	}
	return false
}

func siteName(host string) string {
	if n, ok := apiHosts[host]; ok {
		return n
	}
	if isCoverHost(host) {
		return "the cover site"
	}
	return ""
}

// Cache stores answers between lookups. key is the SHA-256 hex of the full URL.
type Cache interface {
	Get(ctx context.Context, provider, key string, maxAge time.Duration) ([]byte, bool, error)
	Put(ctx context.Context, provider, key string, body []byte, at time.Time) error
}

// Options configures a Client. Transport is for tests only; the guard wraps it anyway.
type Options struct {
	Cache     Cache
	CoverDir  string
	UserAgent string
	Now       func() time.Time
	Transport http.RoundTripper
}

// Client talks to the allowed sites.
type Client struct {
	hc       *http.Client
	cache    Cache
	coverDir string
	ua       string
	now      func() time.Time
	sleep    func(ctx context.Context, d time.Duration) error

	mu   sync.Mutex
	last map[string]time.Time
}

// New builds a Client.
func New(o Options) *Client {
	next := o.Transport
	if next == nil {
		next = http.DefaultTransport.(*http.Transport).Clone()
	}
	c := &Client{cache: o.Cache, coverDir: o.CoverDir, ua: o.UserAgent, now: o.Now, last: map[string]time.Time{}, sleep: sleepCtx}
	if c.ua == "" {
		c.ua = defaultUA
	}
	if c.now == nil {
		c.now = time.Now
	}
	c.hc = &http.Client{
		Timeout:   requestTimeout,
		Transport: guardTransport{next: next},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return &Error{Code: "refused"}
			}
			return nil
		},
	}
	return c
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// guardTransport refuses anything that is not https to a listed host before it is dialled.
type guardTransport struct{ next http.RoundTripper }

func (g guardTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u := req.URL
	host := strings.ToLower(u.Hostname())
	_, api := apiHosts[host]
	if u.Scheme != "https" || u.Port() != "" || u.User != nil || !(api || isCoverHost(host)) {
		if req.Body != nil {
			req.Body.Close()
		}
		return nil, &Error{Code: "refused"}
	}
	return g.next.RoundTrip(req)
}

// Error is a failure in words a listener can read. The text never carries a URL or a Go error.
type Error struct{ Code, Site string }

func (e *Error) Error() string { return e.Text() }

// Text explains the failure in plain words.
func (e *Error) Text() string {
	site := e.Site
	if site == "" {
		site = "that site"
	}
	switch e.Code {
	case "refused":
		return "Tendshelf only talks to a short list of book sites, and that address is not on it."
	case "offline":
		return fmt.Sprintf("Tendshelf could not reach %s. Check this computer's internet connection and try again.", site)
	case "busy":
		return fmt.Sprintf("%s is asking for a pause. Wait a minute and try again.", site)
	case "too_large":
		return fmt.Sprintf("The answer from %s was too large, so Tendshelf ignored it.", site)
	case "bad_answer":
		return fmt.Sprintf("%s sent an answer Tendshelf could not use.", site)
	default:
		return fmt.Sprintf("%s had a problem and could not answer. Try again later.", site)
	}
}

// toError turns whatever the http client returned into an *Error.
func toError(err error, site string) *Error {
	var oe *Error
	if errors.As(err, &oe) {
		if oe.Site == "" {
			return &Error{Code: oe.Code, Site: site}
		}
		return oe
	}
	return &Error{Code: "offline", Site: site}
}

func cacheKey(rawURL string) string {
	sum := sha256.Sum256([]byte(rawURL))
	return hex.EncodeToString(sum[:])
}

// wait spaces requests to the same host.
func (c *Client) wait(ctx context.Context, host string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if prev, ok := c.last[host]; ok {
		if w := hostGap(host) - c.now().Sub(prev); w > 0 {
			if err := c.sleep(ctx, w); err != nil {
				return err
			}
		}
	}
	c.last[host] = c.now()
	return nil
}

// fetch makes one spaced GET with one retry after a 429. It returns the status and a body capped at limit.
func (c *Client) fetch(ctx context.Context, rawURL, accept string, limit int64) (int, []byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, nil, &Error{Code: "refused"}
	}
	host := strings.ToLower(u.Hostname())
	site := siteName(host)
	for attempt := 0; ; attempt++ {
		if err := c.wait(ctx, host); err != nil {
			return 0, nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return 0, nil, &Error{Code: "refused", Site: site}
		}
		req.Header.Set("User-Agent", c.ua)
		req.Header.Set("Accept", accept)
		resp, err := c.hc.Do(req)
		if err != nil {
			if ctx.Err() != nil {
				return 0, nil, ctx.Err()
			}
			return 0, nil, toError(err, site)
		}
		if resp.StatusCode == http.StatusTooManyRequests && attempt == 0 {
			delay := retryAfter(resp.Header.Get("Retry-After"))
			resp.Body.Close()
			if err := c.sleep(ctx, delay); err != nil {
				return 0, nil, err
			}
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
		resp.Body.Close()
		if err != nil {
			if ctx.Err() != nil {
				return 0, nil, ctx.Err()
			}
			return 0, nil, &Error{Code: "offline", Site: site}
		}
		if int64(len(body)) > limit {
			return 0, nil, &Error{Code: "too_large", Site: site}
		}
		return resp.StatusCode, body, nil
	}
}

// retryAfter reads a Retry-After in seconds, clamped to 1-10 s (5 s when missing or odd).
func retryAfter(h string) time.Duration {
	n, err := strconv.Atoi(strings.TrimSpace(h))
	if err != nil {
		return defaultRetry
	}
	return time.Duration(min(10, max(1, n))) * time.Second
}

// getJSON returns the JSON answer, or `null` when the site has nothing (404).
// Answers, including 404s, are cached; errors never are.
func (c *Client) getJSON(ctx context.Context, provider, rawURL string) (json.RawMessage, error) {
	key := cacheKey(rawURL)
	if c.cache != nil {
		if b, ok, err := c.cache.Get(ctx, provider, key, cacheTTL); err == nil && ok && json.Valid(b) {
			return b, nil
		}
	}
	site := ""
	if u, err := url.Parse(rawURL); err == nil {
		site = siteName(strings.ToLower(u.Hostname()))
	}
	status, body, err := c.fetch(ctx, rawURL, "application/json", maxJSONBytes)
	if err != nil {
		return nil, err
	}
	var out json.RawMessage
	switch {
	case status == http.StatusNotFound:
		out = json.RawMessage("null")
	case status == http.StatusTooManyRequests:
		return nil, &Error{Code: "busy", Site: site}
	case status < 200 || status > 299:
		return nil, &Error{Code: "server", Site: site}
	default:
		if !json.Valid(body) {
			return nil, &Error{Code: "bad_answer", Site: site}
		}
		out = body
	}
	if c.cache != nil {
		_ = c.cache.Put(ctx, provider, key, out, c.now())
	}
	return out, nil
}
