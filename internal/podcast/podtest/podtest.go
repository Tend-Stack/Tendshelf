// Package podtest is a fake publisher for tests: an httptest server reached through the real guarded
// client. DNS is faked to a public-looking address and the dialler connects to the listener, so every
// request still passes the dial-time address check and no test touches the network.
package podtest

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/metadata/online/podnet"
)

// Publisher is the fake. Hits counts requests that reached the handler.
type Publisher struct {
	Srv    *httptest.Server
	Client *podnet.Client
	Hits   atomic.Int64
	// Resolve maps a host name to the address the fake DNS claims (default 93.184.216.34, public).
	Resolve map[string]string
}

// New starts a publisher answering with h and returns it with a client that reaches only it. The
// same server answers OndaCast too (host "ondacast.test"), so a handler can branch on r.Host.
func New(t testing.TB, h http.HandlerFunc) *Publisher {
	t.Helper()
	p := &Publisher{Resolve: map[string]string{}}
	p.Srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p.Hits.Add(1)
		h(w, r)
	}))
	t.Cleanup(p.Srv.Close)
	p.Client = podnet.New(podnet.Options{
		LookupIP: func(_ context.Context, host string) ([]netip.Addr, error) {
			s, ok := p.Resolve[host]
			if !ok {
				s = "93.184.216.34"
			}
			return []netip.Addr{netip.MustParseAddr(s)}, nil
		},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, p.Srv.Listener.Addr().String())
		},
		OndaCastBase: p.URL("ondacast.test", ""),
	})
	return p
}

// URL is an address on the fake publisher under a made-up host name.
func (p *Publisher) URL(host, path string) string {
	_, port, _ := net.SplitHostPort(p.Srv.Listener.Addr().String())
	return "http://" + host + ":" + port + path
}
