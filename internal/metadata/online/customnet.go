package online

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"syscall"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/metadata/online/podnet"
)

// The second client (decision 0046). It reaches the lookup sites an admin typed, which may be on a
// private network, and cover pictures those sites name. Its transport is its own, so a connection
// opened under these rules is never reused by the allowlisted client and the other way round.

// dialPolicy rides in a request's context and tells the dialler what the request may reach.
// typed is true for the address an admin typed (public and local addresses allowed); every other
// host may reach public addresses only. host is the name the policy was made for.
type dialPolicy struct {
	host, scheme string
	typed        bool
}

type policyKey struct{}

// withPolicy adds the dial policy for rawURL to ctx.
func withPolicy(ctx context.Context, rawURL string, typed bool) context.Context {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ctx
	}
	return context.WithValue(ctx, policyKey{}, dialPolicy{host: strings.ToLower(u.Hostname()), scheme: strings.ToLower(u.Scheme), typed: typed})
}

// allowedClass reports whether an address of this class may be dialled under the policy.
func (p dialPolicy) allowedClass(c podnet.AddrClass) bool {
	return c == podnet.AddrPublic || (p.typed && c == podnet.AddrLocal)
}

func blocked() error { return &Error{Code: "blocked"} }

// newCustomHTTP builds the guarded client: no proxy, no cookies, no keep-alive, every connection
// checked after DNS on the address actually dialled.
func newCustomHTTP(o Options) *http.Client {
	tr := &http.Transport{
		Proxy:                  nil,
		DisableKeepAlives:      true,
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  10 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
	}
	if o.CustomRootCAs != nil {
		tr.TLSClientConfig = &tls.Config{RootCAs: o.CustomRootCAs, MinVersion: tls.VersionTLS12}
	}
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		return guardedDial(ctx, o, network, addr)
	}
	return &http.Client{
		Jar:       nil,
		Timeout:   requestTimeout,
		Transport: tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) > maxRedirects {
				return blocked()
			}
			req.Header.Del("Referer")
			p, _ := req.Context().Value(policyKey{}).(dialPolicy)
			if !p.typed {
				if strings.ToLower(req.URL.Scheme) != "https" {
					return blocked()
				}
				return nil
			}
			if originOf(req.URL) != originOf(via[0].URL) {
				return blocked()
			}
			return nil
		},
	}
}

// originOf names scheme, host and port in lower case, with the scheme's own port filled in.
func originOf(u *url.URL) string {
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	if port == "" {
		port = "80"
		if scheme == "https" {
			port = "443"
		}
	}
	return scheme + "://" + net.JoinHostPort(strings.ToLower(u.Hostname()), port)
}

// guardedDial resolves the name itself, keeps the addresses the policy allows, refuses plain http
// when any kept address is public, and dials one of them literally.
func guardedDial(ctx context.Context, o Options, network, addr string) (net.Conn, error) {
	p, ok := ctx.Value(policyKey{}).(dialPolicy)
	if !ok {
		return nil, blocked()
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil || !podnet.PortOK(port) || (p.typed && !strings.EqualFold(host, p.host)) {
		return nil, blocked()
	}
	var ips []netip.Addr
	if a, perr := netip.ParseAddr(host); perr == nil {
		ips = []netip.Addr{a}
	} else if o.CustomLookup != nil {
		ips, err = o.CustomLookup(ctx, host)
	} else {
		ips, err = net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	}
	if err != nil || len(ips) == 0 {
		return nil, &Error{Code: "offline"}
	}
	var kept []netip.Addr
	anyPublic := false
	for _, a := range ips {
		c := podnet.ClassifyAddr(a)
		if !p.allowedClass(c) {
			continue
		}
		if c == podnet.AddrPublic {
			anyPublic = true
		}
		kept = append(kept, a.WithZone("").Unmap())
	}
	if len(kept) == 0 {
		return nil, blocked()
	}
	if p.scheme == "http" && anyPublic {
		return nil, &Error{Code: "insecure"}
	}
	d := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			ap, err := netip.ParseAddrPort(address)
			if err != nil || !p.allowedClass(podnet.ClassifyAddr(ap.Addr())) {
				return blocked()
			}
			return nil
		},
	}
	var last error
	for _, a := range kept {
		target := net.JoinHostPort(a.String(), port)
		var conn net.Conn
		if o.CustomDial != nil {
			conn, err = o.CustomDial(ctx, network, target)
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
