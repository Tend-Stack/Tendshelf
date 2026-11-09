package podnet

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// rig is a fake publisher on the loopback. The client reaches it only through guardedDial: DNS is
// faked to a public-looking address, and the dial override connects to the listener.
type rig struct {
	srv   *httptest.Server
	hits  atomic.Int64
	dials atomic.Int64
	c     *Client
	// resolve maps a host name to the address the fake DNS claims.
	resolve map[string]string
}

func (r *rig) url(host, path string) string {
	_, port, _ := net.SplitHostPort(r.srv.Listener.Addr().String())
	return "http://" + host + ":" + port + path
}

func newRig(t *testing.T, h http.HandlerFunc) *rig {
	t.Helper()
	r := &rig{resolve: map[string]string{}}
	r.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, q *http.Request) {
		r.hits.Add(1)
		h(w, q)
	}))
	t.Cleanup(r.srv.Close)
	r.c = New(Options{
		LookupIP: func(_ context.Context, host string) ([]netip.Addr, error) {
			s, ok := r.resolve[host]
			if !ok {
				s = "93.184.216.34"
			}
			return []netip.Addr{netip.MustParseAddr(s)}, nil
		},
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			r.dials.Add(1)
			return (&net.Dialer{}).DialContext(ctx, network, r.srv.Listener.Addr().String())
		},
		OndaCastBase: r.url("ondacast.test", ""),
	})
	return r
}

func code(err error) string {
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Code
	}
	if err == nil {
		return ""
	}
	return "other:" + err.Error()
}

func TestFeedBasics(t *testing.T) {
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {
		if q.Header.Get("If-None-Match") == `"v1"` {
			w.WriteHeader(304)
			return
		}
		w.Header().Set("ETag", `"v1"`)
		w.Header().Set("Last-Modified", "Mon, 02 Jan 2006 15:04:05 GMT")
		io.WriteString(w, "<rss/>")
	})
	res, err := r.c.Feed(context.Background(), r.url("feed.example", "/f.xml"), "", "")
	if err != nil || string(res.Body) != "<rss/>" || res.ETag != `"v1"` || res.LastModified == "" || res.Moved {
		t.Fatalf("%v %+v", err, res)
	}
	res, err = r.c.Feed(context.Background(), r.url("feed.example", "/f.xml"), `"v1"`, "")
	if err != nil || !res.NotModified || len(res.Body) != 0 {
		t.Fatalf("304: %v %+v", err, res)
	}
}

func TestFeedStatuses(t *testing.T) {
	for status, want := range map[int]string{404: "not_found", 410: "not_found", 429: "busy", 500: "bad_answer", 403: "bad_answer"} {
		r := newRig(t, func(w http.ResponseWriter, q *http.Request) { w.WriteHeader(status) })
		_, err := r.c.Feed(context.Background(), r.url("feed.example", "/"), "", "")
		if code(err) != want {
			t.Errorf("%d: %v want %s", status, err, want)
		}
	}
}

func TestDNSToLoopbackRefusedAtDial(t *testing.T) {
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) { io.WriteString(w, "secret") })
	r.resolve["evil.example"] = "127.0.0.1"
	_, err := r.c.Feed(context.Background(), r.url("evil.example", "/"), "", "")
	if code(err) != "refused" {
		t.Fatalf("got %v", err)
	}
	if r.hits.Load() != 0 || r.dials.Load() != 0 {
		t.Fatalf("server reached: hits %d dials %d", r.hits.Load(), r.dials.Load())
	}
	// One public and one private answer: only the public one may be dialled.
	r.c.lookup = func(context.Context, string) ([]netip.Addr, error) {
		return []netip.Addr{netip.MustParseAddr("10.0.0.1"), netip.MustParseAddr("93.184.216.34")}, nil
	}
	if _, err := r.c.Feed(context.Background(), r.url("mixed.example", "/"), "", ""); err != nil {
		t.Fatalf("mixed: %v", err)
	}
}

func TestRedirects(t *testing.T) {
	var self string
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {
		switch q.URL.Path {
		case "/to-private":
			http.Redirect(w, q, "http://127.0.0.1:8000/x", 302)
		case "/to-private-name":
			http.Redirect(w, q, strings.Replace(self, "feed.example", "internal.example", 1)+"/final", 302)
		case "/to-file":
			http.Redirect(w, q, "file:///etc/passwd", 302)
		case "/final":
			io.WriteString(w, "ok")
		case "/perm":
			http.Redirect(w, q, "/final", 301)
		case "/perm2":
			http.Redirect(w, q, "/perm", 308)
		case "/mixed":
			http.Redirect(w, q, "/temp", 301)
		case "/temp":
			http.Redirect(w, q, "/final", 302)
		default: // /loop/N
			var n int
			fmt.Sscanf(q.URL.Path, "/loop/%d", &n)
			http.Redirect(w, q, fmt.Sprintf("/loop/%d", n+1), 302)
		}
	})
	self = r.url("feed.example", "")
	r.resolve["internal.example"] = "10.0.0.9"
	ctx := context.Background()
	if _, err := r.c.Feed(ctx, self+"/to-private", "", ""); code(err) != "refused" {
		t.Errorf("literal private: %v", err)
	}
	before := r.hits.Load()
	if _, err := r.c.Feed(ctx, self+"/to-private-name", "", ""); code(err) != "refused" {
		t.Errorf("name to private: %v", err)
	}
	if r.hits.Load() != before+1 {
		t.Errorf("private hop was reached")
	}
	if _, err := r.c.Feed(ctx, self+"/to-file", "", ""); code(err) != "refused" {
		t.Errorf("file: %v", err)
	}
	if _, err := r.c.Feed(ctx, self+"/loop/0", "", ""); code(err) != "refused" {
		t.Errorf("loop: %v", err)
	}
	// Exactly 5 redirects are fine: /loop/N ends when the hand-off stops, so use a chain that finishes.
	res, err := r.c.Feed(ctx, self+"/perm2", "", "")
	if err != nil || !res.Moved || !strings.HasSuffix(res.FinalURL, "/final") {
		t.Errorf("perm: %v %+v", err, res)
	}
	res, err = r.c.Feed(ctx, self+"/mixed", "", "")
	if err != nil || res.Moved {
		t.Errorf("mixed: %v %+v", err, res)
	}
}

func TestRedirectCapIsFiveHops(t *testing.T) {
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {
		var n int
		fmt.Sscanf(q.URL.Path, "/h/%d", &n)
		if n >= 5 {
			io.WriteString(w, "end")
			return
		}
		http.Redirect(w, q, fmt.Sprintf("/h/%d", n+1), 302)
	})
	if res, err := r.c.Feed(context.Background(), r.url("feed.example", "/h/0"), "", ""); err != nil || string(res.Body) != "end" {
		t.Fatalf("5 hops: %v", err)
	}
	if _, err := r.c.Feed(context.Background(), r.url("feed.example", "/h/-1"), "", ""); code(err) != "refused" {
		t.Fatalf("6 hops: %v", err)
	}
}

func TestFeedTooLarge(t *testing.T) {
	big := bytes.Repeat([]byte("a"), 16<<20)
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) { w.Write(big) })
	if _, err := r.c.Feed(context.Background(), r.url("feed.example", "/"), "", ""); code(err) != "too_large" {
		t.Fatalf("got %v", err)
	}
	// Chunked, no length: still cut.
	r2 := newRig(t, func(w http.ResponseWriter, q *http.Request) {
		w.(http.Flusher).Flush()
		w.Write(big)
	})
	if _, err := r2.c.Feed(context.Background(), r2.url("feed.example", "/"), "", ""); code(err) != "too_large" {
		t.Fatalf("chunked: %v", err)
	}
}

func TestFeedSlowBody(t *testing.T) {
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {
		w.(http.Flusher).Flush()
		for i := 0; i < 50; i++ {
			if _, err := io.WriteString(w, "x"); err != nil {
				return
			}
			w.(http.Flusher).Flush()
			time.Sleep(40 * time.Millisecond)
		}
	})
	r.c.feedTimeout = 250 * time.Millisecond
	start := time.Now()
	_, err := r.c.Feed(context.Background(), r.url("feed.example", "/"), "", "")
	if code(err) != "slow" {
		t.Fatalf("got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("took %v", time.Since(start))
	}
}

func TestNoCookiesNoReferer(t *testing.T) {
	var seen []http.Header
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {
		seen = append(seen, q.Header.Clone())
		if q.URL.Path == "/start" {
			http.SetCookie(w, &http.Cookie{Name: "track", Value: "1"})
			http.Redirect(w, q, "/next", 302)
			return
		}
		io.WriteString(w, "ok")
	})
	up, err := r.c.OpenAudio(context.Background(), r.url("cdn.example", "/start"), "bytes=10-")
	if err != nil {
		t.Fatal(err)
	}
	up.Body.Close()
	if len(seen) != 2 {
		t.Fatalf("requests %d", len(seen))
	}
	allowed := map[string]bool{"User-Agent": true, "Accept": true, "Range": true, "Accept-Encoding": true}
	for i, h := range seen {
		for k := range h {
			if !allowed[k] {
				t.Errorf("request %d carries %s", i, k)
			}
		}
		if h.Get("User-Agent") != "Tendshelf (self-hosted podcast player)" {
			t.Errorf("UA %q", h.Get("User-Agent"))
		}
		if h.Get("Range") != "bytes=10-" {
			t.Errorf("request %d Range %q", i, h.Get("Range"))
		}
	}
}

func TestPictureChaptersAudioLive(t *testing.T) {
	png := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0}, 32)...)
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {
		switch q.URL.Path {
		case "/a.png":
			w.Header().Set("Content-Type", "text/html") // the bytes decide
			w.Write(png)
		case "/a.html":
			io.WriteString(w, "<html>")
		case "/big.png":
			w.Write(append(png, bytes.Repeat([]byte{1}, 6<<20)...))
		case "/c.json":
			io.WriteString(w, `{"version":"1.2.0","chapters":[]}`)
		case "/c.bad":
			io.WriteString(w, `nope`)
		case "/c.big":
			w.Write(bytes.Repeat([]byte(" "), 2<<20))
		case "/audio":
			if q.Header.Get("Range") == "bytes=2-" {
				w.Header().Set("Content-Range", "bytes 2-4/5")
				w.Header().Set("X-Secret", "x")
				w.Header().Set("Set-Cookie", "a=b")
				w.WriteHeader(206)
				io.WriteString(w, "cde")
				return
			}
			io.WriteString(w, "abcde")
		case "/gone":
			w.WriteHeader(410)
		case "/live":
			if q.Header.Get("Icy-MetaData") != "1" {
				t.Error("no Icy-MetaData")
			}
			w.Header().Set("Icy-Metaint", "8192")
			io.WriteString(w, "audio")
		}
	})
	ctx := context.Background()
	b, mime, err := r.c.Picture(ctx, r.url("art.example", "/a.png"))
	if err != nil || mime != "image/png" || !bytes.Equal(b, png) {
		t.Errorf("png: %v %s", err, mime)
	}
	if _, _, err = r.c.Picture(ctx, r.url("art.example", "/a.html")); code(err) != "bad_answer" {
		t.Errorf("html: %v", err)
	}
	if _, _, err = r.c.Picture(ctx, r.url("art.example", "/big.png")); code(err) != "too_large" {
		t.Errorf("big: %v", err)
	}
	if b, err = r.c.Chapters(ctx, r.url("c.example", "/c.json")); err != nil || !strings.Contains(string(b), "chapters") {
		t.Errorf("chapters: %v", err)
	}
	if _, err = r.c.Chapters(ctx, r.url("c.example", "/c.bad")); code(err) != "bad_answer" {
		t.Errorf("bad json: %v", err)
	}
	if _, err = r.c.Chapters(ctx, r.url("c.example", "/c.big")); code(err) != "too_large" {
		t.Errorf("big json: %v", err)
	}
	up, err := r.c.OpenAudio(ctx, r.url("a.example", "/audio"), "bytes=2-")
	if err != nil || up.Status != 206 {
		t.Fatalf("audio: %v", err)
	}
	body, _ := io.ReadAll(up.Body)
	up.Body.Close()
	if string(body) != "cde" || up.Header.Get("Content-Range") != "bytes 2-4/5" || up.Header.Get("X-Secret") != "" || up.Header.Get("Set-Cookie") != "" {
		t.Errorf("audio %q %v", body, up.Header)
	}
	// A Range that is not a single byte range is not sent.
	up, err = r.c.OpenAudio(ctx, r.url("a.example", "/audio"), "bytes=0-1, 3-4")
	if err != nil || up.Status != 200 {
		t.Fatalf("multi range: %v", err)
	}
	up.Body.Close()
	if _, err = r.c.OpenAudio(ctx, r.url("a.example", "/gone"), ""); code(err) != "not_found" {
		t.Errorf("gone: %v", err)
	}
	up, metaint, err := r.c.OpenLive(ctx, r.url("radio.example", "/live"))
	if err != nil || metaint != 8192 {
		t.Fatalf("live: %v %d", err, metaint)
	}
	up.Body.Close()
}

func TestAudioIdleTimeout(t *testing.T) {
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {
		io.WriteString(w, "a")
		w.(http.Flusher).Flush()
		select {
		case <-q.Context().Done():
		case <-time.After(3 * time.Second):
		}
	})
	r.c.idleTimeout = 150 * time.Millisecond
	up, err := r.c.OpenAudio(context.Background(), r.url("a.example", "/"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer up.Body.Close()
	start := time.Now()
	_, err = io.ReadAll(up.Body)
	if err == nil || time.Since(start) > 2*time.Second {
		t.Fatalf("err %v after %v", err, time.Since(start))
	}
}

func TestBadSchemesMakeNoRequest(t *testing.T) {
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {})
	for _, raw := range []string{"file:///etc/passwd", "ftp://x.example/f", "http://u:p@x.example/", "http://x.example:22/", "http://127.0.0.1:8000/", "://bad"} {
		if _, err := r.c.Feed(context.Background(), raw, "", ""); code(err) != "refused" {
			t.Errorf("%s: %v", raw, err)
		}
	}
	if r.hits.Load() != 0 || r.dials.Load() != 0 {
		t.Error("something was dialled")
	}
}
