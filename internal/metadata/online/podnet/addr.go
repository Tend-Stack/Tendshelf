package podnet

import (
	"net/netip"
	"net/url"
	"strconv"
	"strings"
)

// Kind says what a request is for: a publisher's address (feed, picture, chapters, audio, stream)
// or the OndaCast directory, which has a much shorter list of allowed addresses.
type Kind int

const (
	KindPublisher Kind = iota
	KindOndaCast
)

// Error is a failure in plain words. Code is one of refused, offline, not_found, not_a_feed,
// too_large, slow, busy, bad_answer. No upstream address, header or text ever goes into it.
type Error struct{ Code string }

func (e *Error) Error() string { return e.Text() }

// Text explains the failure in plain words.
func (e *Error) Text() string {
	switch e.Code {
	case "refused":
		return "Tendshelf will not open that address. It must be a public web address."
	case "offline":
		return "Tendshelf could not reach that site. Try again later."
	case "not_found":
		return "That address does not exist any more."
	case "not_a_feed":
		return "That address is not a podcast feed."
	case "too_large":
		return "That file is too large for Tendshelf to take."
	case "slow":
		return "That site answered too slowly."
	case "busy":
		return "That site is busy right now. Try again in a little while."
	case "bad_answer":
		return "That site sent an answer Tendshelf could not use."
	}
	return "Something went wrong while contacting that site."
}

func refused() error { return &Error{Code: "refused"} }

// refusedPrefixes are the ranges checkAddr refuses besides what netip already classifies.
var refusedPrefixes = mustPrefixes(
	"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15",
	"198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "255.255.255.255/32",
	"2001:db8::/32", "fc00::/7",
)

// nat64 is the well-known NAT64 prefix; its last four bytes are an IPv4 address.
var nat64 = netip.MustParsePrefix("64:ff9b::/96")

func mustPrefixes(s ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(s))
	for i, p := range s {
		out[i] = netip.MustParsePrefix(p)
	}
	return out
}

// checkAddr refuses every address that is not a public unicast address. IPv4-mapped and NAT64 forms
// are reduced to their IPv4 address first, so ::ffff:127.0.0.1 and 64:ff9b::7f00:1 are loopback.
func checkAddr(a netip.Addr) error {
	if !a.IsValid() {
		return refused()
	}
	a = a.WithZone("").Unmap()
	if a.Is6() && nat64.Contains(a) {
		b := a.As16()
		a = netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]})
	}
	if a.IsLoopback() || a.IsPrivate() || a.IsLinkLocalUnicast() || a.IsLinkLocalMulticast() ||
		a.IsMulticast() || a.IsUnspecified() || a.IsInterfaceLocalMulticast() {
		return refused()
	}
	for _, p := range refusedPrefixes {
		if p.Contains(a) {
			return refused()
		}
	}
	return nil
}

// checkURL applies the address rules that need no DNS. ondaBase is the one allowed scheme and host
// for KindOndaCast ("https://ondacast.com"; tests may name their own).
func checkURL(u *url.URL, kind Kind, ondaBase *url.URL) error {
	if u == nil {
		return refused()
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return refused()
	}
	if u.User != nil || u.Opaque != "" {
		return refused()
	}
	host := u.Hostname()
	if host == "" {
		return refused()
	}
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || !(n == 80 || n == 443 || (n >= 1024 && n <= 65535)) {
			return refused()
		}
	}
	if kind == KindOndaCast {
		if ondaBase == nil || scheme != ondaBase.Scheme || !strings.EqualFold(u.Host, ondaBase.Host) {
			return refused()
		}
		return nil
	}
	if a, err := netip.ParseAddr(host); err == nil {
		return checkAddr(a)
	}
	return nil
}
