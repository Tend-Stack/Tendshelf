// Package podnet is the guarded client for podcasts and radio (decision 0038). It is a sub-package of
// internal/metadata/online and, with it, the only place in the server that opens connections for
// them. Every connection is checked after DNS, on the address actually dialled; there are no cookies,
// no proxies, at most 5 redirects, a fixed User-Agent and caps on size and time. No upstream address,
// header or text is put into an Error.
package podnet

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	userAgent    = "Tendshelf (self-hosted podcast player)"
	maxRedirects = 5
	defaultOnda  = "https://ondacast.com"

	feedTimeout   = 30 * time.Second
	feedMax       = 15 << 20
	pictureMax    = 5 << 20
	chaptersMax   = 1 << 20
	ondaMax       = 2 << 20
	ondaTimeout   = 10 * time.Second
	audioIdle     = 60 * time.Second
	headerTimeout = 15 * time.Second
	ondaCacheTTL  = 10 * time.Minute
	ondaCacheSize = 500
)

// Options configures New. The zero value is the production client.
type Options struct {
	Now      func() time.Time
	Resolver *net.Resolver
	// DialContext replaces the dialler in tests only. It is still handed an address that passed checkAddr.
	DialContext func(ctx context.Context, network, addr string) (net.Conn, error)
	// LookupIP replaces DNS in tests only; its answers are checked like any others.
	LookupIP func(ctx context.Context, host string) ([]netip.Addr, error)
	// OndaCastBase replaces "https://ondacast.com" in tests only.
	OndaCastBase string
}

// Client is safe for concurrent use.
type Client struct {
	hc       *http.Client
	now      func() time.Time
	resolver *net.Resolver
	dial     func(ctx context.Context, network, addr string) (net.Conn, error)
	lookup   func(ctx context.Context, host string) ([]netip.Addr, error)
	onda     *url.URL

	// Test hooks: shortened timeouts.
	feedTimeout time.Duration
	idleTimeout time.Duration

	mu         sync.Mutex
	cache      map[string]cacheEntry
	cacheOrder []string
}

// New builds a client.
func New(o Options) *Client {
	c := &Client{
		now: o.Now, resolver: o.Resolver, dial: o.DialContext, lookup: o.LookupIP,
		feedTimeout: feedTimeout, idleTimeout: audioIdle,
		cache: map[string]cacheEntry{},
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.resolver == nil {
		c.resolver = net.DefaultResolver
	}
	base := o.OndaCastBase
	if base == "" {
		base = defaultOnda
	}
	c.onda, _ = url.Parse(base)
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           c.guardedDial,
		TLSHandshakeTimeout:   10 * time.Second,
		ResponseHeaderTimeout: headerTimeout,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       30 * time.Second,
		ForceAttemptHTTP2:     true,
	}
	c.hc = &http.Client{Transport: tr, Jar: nil, CheckRedirect: c.checkRedirect}
	return c
}

// trace follows one request across its redirects.
type trace struct {
	kind      Kind
	hops      int
	permanent bool // every hop so far was a 301 or 308
}

type traceKey struct{}

func (c *Client) checkRedirect(req *http.Request, via []*http.Request) error {
	if len(via) > maxRedirects {
		return refused()
	}
	tr, _ := req.Context().Value(traceKey{}).(*trace)
	kind := KindPublisher
	if tr != nil {
		kind = tr.kind
	}
	if err := checkURL(req.URL, kind, c.onda); err != nil {
		return err
	}
	req.Header.Del("Referer")
	if tr != nil {
		tr.hops++
		if req.Response == nil || (req.Response.StatusCode != 301 && req.Response.StatusCode != 308) {
			tr.permanent = false
		}
	}
	return nil
}

// guardedDial resolves the host itself, checks every answer and dials one of the allowed addresses
// directly, so the address that was checked is the address that is used.
func (c *Client) guardedDial(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, refused()
	}
	var ips []netip.Addr
	if a, perr := netip.ParseAddr(host); perr == nil {
		ips = []netip.Addr{a}
	} else if c.lookup != nil {
		ips, err = c.lookup(ctx, host)
	} else {
		ips, err = c.resolver.LookupNetIP(ctx, "ip", host)
	}
	if err != nil || len(ips) == 0 {
		return nil, &Error{Code: "offline"}
	}
	var allowed []netip.Addr
	for _, a := range ips {
		if checkAddr(a) == nil {
			allowed = append(allowed, a.Unmap())
		}
	}
	if len(allowed) == 0 {
		return nil, refused()
	}
	d := &net.Dialer{Timeout: 10 * time.Second, Control: controlCheck}
	var last error
	for _, a := range allowed {
		target := net.JoinHostPort(a.String(), port)
		var conn net.Conn
		if c.dial != nil {
			conn, err = c.dial(ctx, network, target)
		} else {
			conn, err = d.DialContext(ctx, network, target)
		}
		if err == nil {
			return conn, nil
		}
		last = err
		if ctx.Err() != nil {
			break
		}
	}
	return nil, last
}

// controlCheck runs inside the dialler on the socket address about to be connected.
func controlCheck(network, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil {
		return refused()
	}
	return checkAddr(ap.Addr())
}

// do sends one GET with the fixed headers and maps transport errors to plain Errors.
func (c *Client) do(ctx context.Context, rawURL string, kind Kind, h map[string]string) (*http.Response, *trace, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, nil, refused()
	}
	if err := checkURL(u, kind, c.onda); err != nil {
		return nil, nil, err
	}
	tr := &trace{kind: kind, permanent: true}
	req, err := http.NewRequestWithContext(context.WithValue(ctx, traceKey{}, tr), http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, nil, refused()
	}
	req.Header.Set("User-Agent", userAgent)
	for k, v := range h {
		req.Header.Set(k, v)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, nil, mapErr(ctx, err)
	}
	return resp, tr, nil
}

func mapErr(ctx context.Context, err error) error {
	var pe *Error
	if errors.As(err, &pe) {
		return pe
	}
	if errors.Is(err, context.DeadlineExceeded) || ctx.Err() == context.DeadlineExceeded {
		return &Error{Code: "slow"}
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return &Error{Code: "slow"}
	}
	return &Error{Code: "offline"}
}

// statusErr maps a failure status to an Error, or nil for a usable one.
func statusErr(code int) error {
	switch {
	case code == 404 || code == 410:
		return &Error{Code: "not_found"}
	case code == 429 || code == 503:
		return &Error{Code: "busy"}
	case code >= 400:
		return &Error{Code: "bad_answer"}
	}
	return nil
}
