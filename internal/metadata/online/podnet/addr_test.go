package podnet

import (
	"net/netip"
	"net/url"
	"testing"
)

func TestCheckAddr(t *testing.T) {
	refusedAddrs := []string{
		"127.0.0.1", "::1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "100.64.0.1",
		"0.0.0.0", "::ffff:127.0.0.1", "64:ff9b::7f00:1", "fd00::1", "fe80::1", "198.18.0.1", "240.0.0.1",
		"::", "224.0.0.1", "ff02::1", "255.255.255.255", "2001:db8::1", "64:ff9b::a00:1", "::ffff:10.0.0.1", "0.1.2.3",
	}
	for _, s := range refusedAddrs {
		if err := checkAddr(netip.MustParseAddr(s)); err == nil {
			t.Errorf("%s allowed", s)
		}
	}
	for _, s := range []string{"93.184.216.34", "2606:4700::1", "64:ff9b::5db8:d822"} {
		if err := checkAddr(netip.MustParseAddr(s)); err != nil {
			t.Errorf("%s refused: %v", s, err)
		}
	}
	if checkAddr(netip.Addr{}) == nil {
		t.Error("invalid address allowed")
	}
}

func TestCheckURL(t *testing.T) {
	onda, _ := url.Parse("https://ondacast.com")
	cases := []struct {
		raw  string
		kind Kind
		ok   bool
	}{
		{"https://example.com/feed.xml", KindPublisher, true},
		{"http://example.com/feed.xml", KindPublisher, true},
		{"http://example.com:8000/stream", KindPublisher, true},
		{"https://example.com:443/x", KindPublisher, true},
		{"http://example.com:22/x", KindPublisher, false},
		{"http://example.com:25/x", KindPublisher, false},
		{"http://example.com:1023/x", KindPublisher, false},
		{"https://user:pass@example.com/x", KindPublisher, false},
		{"https://user@example.com/x", KindPublisher, false},
		{"file:///etc/passwd", KindPublisher, false},
		{"ftp://example.com/x", KindPublisher, false},
		{"gopher://example.com/x", KindPublisher, false},
		{"http://127.0.0.1/x", KindPublisher, false},
		{"http://[::1]:8000/x", KindPublisher, false},
		{"http://10.0.0.5/x", KindPublisher, false},
		{"http://93.184.216.34/x", KindPublisher, true},
		{"https:///nohost", KindPublisher, false},
		{"https://ondacast.com/api/v2/search", KindOndaCast, true},
		{"http://ondacast.com/api/v2/search", KindOndaCast, false},
		{"https://www.ondacast.com/api", KindOndaCast, false},
		{"https://ondacast.com.evil.example/api", KindOndaCast, false},
		{"https://example.com/api", KindOndaCast, false},
	}
	for _, c := range cases {
		u, err := url.Parse(c.raw)
		if err != nil {
			t.Fatal(err)
		}
		if got := checkURL(u, c.kind, onda) == nil; got != c.ok {
			t.Errorf("%s: allowed=%v want %v", c.raw, got, c.ok)
		}
	}
}
