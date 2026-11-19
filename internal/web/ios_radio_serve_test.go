package web

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestIOSRadioServe is the opt-in simulator check of decision 0049 (package I2r). It serves the app handler, with one
// station that streams silent MP3 at real time, on the address in TENDSHELF_IOS_RADIO_SERVE (host:port) for
// TENDSHELF_IOS_RADIO_SERVE_FOR (default 20m), and prints what AVPlayer does to /cast/ every ten seconds: the number of
// requests, how many are open, and the sorted header names it sent. Skipped unless the variable is set.
func TestIOSRadioServe(t *testing.T) {
	addr := os.Getenv("TENDSHELF_IOS_RADIO_SERVE")
	if addr == "" {
		t.Skip("set TENDSHELF_IOS_RADIO_SERVE=<host:port> to serve a station for the iPhone simulator")
	}
	life := 20 * time.Minute
	if v := os.Getenv("TENDSHELF_IOS_RADIO_SERVE_FOR"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatal(err)
		}
		life = d
	}
	w := newRadioWorld(t)
	station := w.add(w.owner, w.fx.pub.URL("radio.test", "/paced.mp3"))

	var requests, open atomic.Int64
	var mu sync.Mutex
	names := map[string]bool{}
	var statuses []string
	counted := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/cast/") {
			w.a.h.ServeHTTP(rw, r)
			return
		}
		requests.Add(1)
		open.Add(1)
		defer open.Add(-1)
		mu.Lock()
		for k := range r.Header {
			names[k] = true
		}
		mu.Unlock()
		rec := &serveStatusWriter{ResponseWriter: rw}
		w.a.h.ServeHTTP(rec, r)
		mu.Lock()
		statuses = append(statuses, fmt.Sprint(rec.code))
		mu.Unlock()
	})
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: counted}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	fmt.Printf("origin=http://%s user=ana password=%s station=%s\n", addr, testPassword, station)
	end := time.After(life)
	tick := time.NewTicker(10 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-end:
			return
		case <-tick.C:
			mu.Lock()
			var list []string
			for k := range names {
				list = append(list, k)
			}
			sort.Strings(list)
			fmt.Printf("cast_requests=%d open=%d statuses=%v header_names=%v\n", requests.Load(), open.Load(), statuses, list)
			mu.Unlock()
		}
	}
}

// serveStatusWriter remembers the status a response was given and still lets the relay flush.
type serveStatusWriter struct {
	http.ResponseWriter
	code int
}

func (s *serveStatusWriter) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}

func (s *serveStatusWriter) Write(b []byte) (int, error) {
	if s.code == 0 {
		s.code = http.StatusOK
	}
	return s.ResponseWriter.Write(b)
}

func (s *serveStatusWriter) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
