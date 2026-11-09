package oidc

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Errors the sign-in code matches on.
var (
	// ErrProviderDown means the provider did not answer well: a timeout, a dial error, a 5xx or a redirect.
	ErrProviderDown = errors.New("oidc: the provider is not answering")
	// ErrProviderConfig means an answer that cannot be right for the configured address: the wrong
	// content type, too large, an endpoint on another origin, or a document that breaks the rules.
	ErrProviderConfig = errors.New("oidc: the provider's settings cannot be used")
	// ErrProviderRefused means the token endpoint answered 4xx: a wrong secret, a used or wrong code.
	ErrProviderRefused = errors.New("oidc: the provider refused the request")
)

// Caps on what the provider may send back (decision 0037 point 11).
const (
	capDiscovery = 64 << 10
	capJWKS      = 64 << 10
	capToken     = 64 << 10
	capUserinfo  = 32 << 10

	requestTimeout = 5 * time.Second
	cacheTTL       = time.Hour
	jwksRefetch    = 5 * time.Minute
)

// ClientOptions configures a Client.
type ClientOptions struct {
	Now     func() time.Time // nil: time.Now
	Timeout time.Duration    // per request; zero: 5 seconds
}

// Client talks to the configured provider and to nobody else. It is the one http.Client in the
// server besides internal/metadata/online and the desktop shell's health check (decision 0037):
// it reaches only the issuer's origin (discovery, token, JWKS, userinfo; the origin is checked
// before every request), follows no redirects, keeps no cookies, and caps and times every answer.
type Client struct {
	hc  *http.Client
	now func() time.Time

	mu    sync.Mutex
	disco map[string]discoEntry
	jwks  map[string]*jwksEntry
}

type discoEntry struct {
	p  Provider
	at time.Time
}

// NewClient returns a Client.
func NewClient(o ClientOptions) *Client {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Timeout <= 0 {
		o.Timeout = requestTimeout
	}
	return &Client{
		hc: &http.Client{
			Timeout: o.Timeout,
			// A redirect is never followed: the 3xx answer itself is read as "not answering".
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
		now:   o.Now,
		disco: map[string]discoEntry{},
		jwks:  map[string]*jwksEntry{},
	}
}

// originOfURL parses raw and returns its origin.
func originOfURL(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || !secureScheme(u) {
		return "", false
	}
	return originOf(u), true
}

// do runs one request against rawURL, which must lie on origin. A 4xx answer is client4xx.
func (c *Client) do(ctx context.Context, method, origin, rawURL string, form url.Values, header http.Header, limit int64, client4xx error) ([]byte, error) {
	if o, ok := originOfURL(rawURL); !ok || o != origin {
		return nil, ErrProviderConfig
	}
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, ErrProviderConfig
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "Tendshelf")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, ErrProviderDown
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode >= 300 && resp.StatusCode < 400, resp.StatusCode >= 500:
		return nil, ErrProviderDown
	case resp.StatusCode >= 400:
		return nil, client4xx
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, ErrProviderConfig
	}
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		return nil, ErrProviderConfig
	}
	if resp.ContentLength > limit {
		return nil, ErrProviderConfig
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, ErrProviderDown
	}
	if int64(len(raw)) > limit {
		return nil, ErrProviderConfig
	}
	return raw, nil
}

func (c *Client) get(ctx context.Context, origin, rawURL string, header http.Header, limit int64) ([]byte, error) {
	return c.do(ctx, http.MethodGet, origin, rawURL, nil, header, limit, ErrProviderConfig)
}

func (c *Client) postForm(ctx context.Context, origin, rawURL string, form url.Values, header http.Header, limit int64) ([]byte, error) {
	return c.do(ctx, http.MethodPost, origin, rawURL, form, header, limit, ErrProviderRefused)
}
