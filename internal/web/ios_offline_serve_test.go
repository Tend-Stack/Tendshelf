package web

import (
	"bytes"
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

// TestIOSOfflineServe is the opt-in simulator check of decision 0048 (package I3c). It serves the app handler, with a followed
// show whose fake publisher gives three 10-second silent MP3 episodes, on the address in TENDSHELF_IOS_OFFLINE_SERVE (host:port)
// for TENDSHELF_IOS_OFFLINE_SERVE_FOR (default 20m). It prints the origin, the user, the password, the show id and the episode
// ids (oldest first), then every ten seconds how many requests reached the episode audio
// (`/api/v1/podcasts/audio/`) and the sorted header names they carried: `Authorization` belongs there, `Cookie` never does.
// Skipped unless the variable is set.
func TestIOSOfflineServe(t *testing.T) {
	addr := os.Getenv("TENDSHELF_IOS_OFFLINE_SERVE")
	if addr == "" {
		t.Skip("set TENDSHELF_IOS_OFFLINE_SERVE=<host:port> to serve a show for the iPhone simulator")
	}
	life := 20 * time.Minute
	if v := os.Getenv("TENDSHELF_IOS_OFFLINE_SERVE_FOR"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			t.Fatal(err)
		}
		life = d
	}
	w := newPodWorld(t)
	// Ten seconds of silence (26 ms a frame), so an episode plays on the simulator; the feed is applied again so its
	// announced length is the real one.
	w.fx.audio = bytes.Repeat(silentMP3Frame(), 385)
	w.applyFeed(t, w.show, "Main")

	var requests atomic.Int64
	var mu sync.Mutex
	names := map[string]bool{}
	counted := http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/podcasts/audio/") {
			requests.Add(1)
			mu.Lock()
			for k := range r.Header {
				names[k] = true
			}
			mu.Unlock()
		}
		w.a.h.ServeHTTP(rw, r)
	})
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: counted}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })

	fmt.Printf("origin=http://%s user=ana password=%s show=%s episodes=%s\n", addr, testPassword, w.show, strings.Join(w.eps, ","))
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
			fmt.Printf("audio_requests=%d header_names=%v\n", requests.Load(), list)
			mu.Unlock()
		}
	}
}
