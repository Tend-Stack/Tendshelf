package auth

import (
	"fmt"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
)

// Proxies is the set of reverse proxies whose X-Forwarded-For and
// X-Forwarded-Proto headers are believed. The zero value trusts none, so a
// client can never choose its own address.
type Proxies struct{ p []netip.Prefix }

// ParseProxies reads a comma-separated list of IP addresses and CIDR ranges.
// An empty string is fine and trusts nothing.
func ParseProxies(csv string) (Proxies, error) {
	var out Proxies
	for _, part := range strings.Split(csv, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if pfx, err := netip.ParsePrefix(part); err == nil {
			out.p = append(out.p, netip.PrefixFrom(pfx.Addr().Unmap(), unmapBits(pfx)).Masked())
			continue
		}
		a, err := netip.ParseAddr(part)
		if err != nil {
			return Proxies{}, fmt.Errorf("auth: %q is not an IP address or CIDR range", part)
		}
		a = a.Unmap()
		out.p = append(out.p, netip.PrefixFrom(a, a.BitLen()))
	}
	return out, nil
}

// unmapBits adjusts the prefix length of an IPv4-mapped IPv6 prefix.
func unmapBits(p netip.Prefix) int {
	if p.Addr().Is4In6() && p.Bits() >= 96 {
		return p.Bits() - 96
	}
	return p.Bits()
}

func (x Proxies) trusts(a netip.Addr) bool {
	a = a.Unmap().WithZone("")
	for _, p := range x.p {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func remoteAddr(r *http.Request) (netip.Addr, bool) {
	ap, err := netip.ParseAddrPort(r.RemoteAddr)
	if err != nil {
		a, err2 := netip.ParseAddr(r.RemoteAddr)
		if err2 != nil {
			return netip.Addr{}, false
		}
		return a.Unmap().WithZone(""), true
	}
	return ap.Addr().Unmap().WithZone(""), true
}

// warnUntrustedForwarded makes the untrusted-proxy warning appear once per run.
var warnUntrustedForwarded sync.Once

// ClientIP returns the address of the client. It is the peer address unless the
// peer is a trusted proxy, in which case it is the right-most
// X-Forwarded-For entry that is not itself trusted. A malformed header falls
// back to the peer address. The result is the zero Addr only when the peer
// address can not be read at all.
func (x Proxies) ClientIP(r *http.Request) netip.Addr {
	peer, ok := remoteAddr(r)
	if !ok {
		return peer
	}
	if !x.trusts(peer) {
		if r.Header.Get("X-Forwarded-For") != "" {
			warnUntrustedForwarded.Do(func() {
				slog.Warn("a request carried X-Forwarded-For from a peer that is not a trusted proxy, so the header was ignored; "+
					"behind a reverse proxy every visitor shares one address (sign-in limits hit everyone) and cookies lose Secure. "+
					"Start with -trusted-proxy set to the proxy's address", "peer", peer.String())
			})
		}
		return peer
	}
	var entries []string
	for _, h := range r.Header.Values("X-Forwarded-For") {
		for _, e := range strings.Split(h, ",") {
			entries = append(entries, strings.TrimSpace(e))
		}
	}
	if len(entries) == 0 {
		return peer
	}
	var leftmost netip.Addr
	for i := len(entries) - 1; i >= 0; i-- {
		a, err := netip.ParseAddr(entries[i])
		if err != nil {
			return peer
		}
		a = a.Unmap().WithZone("")
		if !x.trusts(a) {
			return a
		}
		leftmost = a
	}
	return leftmost // every hop is a trusted proxy: the left-most is the client
}

// Secure reports whether the request reached the server over HTTPS: directly,
// or through a trusted proxy that says so.
func (x Proxies) Secure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	peer, ok := remoteAddr(r)
	if !ok || !x.trusts(peer) {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(firstHeader(r, "X-Forwarded-Proto")), "https")
}

func firstHeader(r *http.Request, name string) string {
	v := r.Header.Get(name)
	if i := strings.IndexByte(v, ','); i >= 0 {
		return v[:i]
	}
	return v
}

// IPKey is the rate-limit key for an address: IPv4 in full, IPv6 as its /64 (a
// single customer commonly owns a whole /64). IPv4-mapped IPv6 is unmapped
// first. The zero Addr gets a fixed key.
func IPKey(a netip.Addr) string {
	if !a.IsValid() {
		return "invalid"
	}
	a = a.Unmap().WithZone("")
	if a.Is4() {
		return a.String()
	}
	p, err := a.Prefix(64)
	if err != nil {
		return a.String()
	}
	return p.String()
}
