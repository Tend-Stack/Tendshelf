package app

import (
	"net"
	"net/http"
	"testing"
	"time"
)

// A request that never finishes must not turn a clean stop into an error: at the deadline the
// server closes the connection and the shutdown reports success.
func TestStopServerClosesHeldRequest(t *testing.T) {
	held := make(chan struct{})
	release := make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(held)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/")
		if err == nil {
			resp.Body.Close()
		}
	}()
	<-held
	defer close(release)
	start := time.Now()
	if err := stopServer(srv, 300*time.Millisecond); err != nil {
		t.Errorf("stopServer = %v, want nil", err)
	}
	if d := time.Since(start); d > 3*time.Second {
		t.Errorf("stopServer took %v", d)
	}
}
