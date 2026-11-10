package podnet

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"
	"time"
)

func TestPodnetHLSPlaylist(t *testing.T) {
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {
		switch q.URL.Path {
		case "/old":
			http.Redirect(w, q, "/live/index.m3u8", 302)
		case "/big":
			w.Write(bytes.Repeat([]byte("#"), playlistMax+1))
		case "/missing":
			http.NotFound(w, q)
		case "/to-private":
			http.Redirect(w, q, "http://127.0.0.1:8000/x.m3u8", 302)
		default:
			if q.Header.Get("Accept") != acceptPlaylist {
				t.Errorf("Accept = %q", q.Header.Get("Accept"))
			}
			io.WriteString(w, "#EXTM3U\n")
		}
	})
	ctx := context.Background()
	body, final, err := r.c.Playlist(ctx, r.url("radio.example", "/old"))
	if err != nil || string(body) != "#EXTM3U\n" {
		t.Fatalf("got %q %v", body, err)
	}
	if want := r.url("radio.example", "/live/index.m3u8"); final != want {
		t.Fatalf("final = %q, want %q", final, want)
	}
	if _, _, err := r.c.Playlist(ctx, r.url("radio.example", "/big")); code(err) != "too_large" {
		t.Fatalf("big: %v", err)
	}
	if _, _, err := r.c.Playlist(ctx, r.url("radio.example", "/missing")); code(err) != "not_found" {
		t.Fatalf("missing: %v", err)
	}
	if _, _, err := r.c.Playlist(ctx, r.url("radio.example", "/to-private")); code(err) != "refused" {
		t.Fatalf("redirect to loopback: %v", err)
	}
}

func TestPodnetHLSSegment(t *testing.T) {
	var gotEnc, gotAccept string
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {
		gotEnc, gotAccept = q.Header.Get("Accept-Encoding"), q.Header.Get("Accept")
		switch q.URL.Path {
		case "/big":
			w.Write(bytes.Repeat([]byte("a"), segmentMax+1))
		case "/to-private":
			http.Redirect(w, q, "http://127.0.0.1:8000/x.ts", 302)
		default:
			w.Write(bytes.Repeat([]byte("a"), segmentMax))
		}
	})
	ctx := context.Background()
	b, err := r.c.Segment(ctx, r.url("radio.example", "/seg.ts"), time.Second)
	if err != nil || len(b) != segmentMax {
		t.Fatalf("exact cap: %d %v", len(b), err)
	}
	if gotEnc != "identity" || gotAccept != "*/*" {
		t.Fatalf("headers %q %q", gotEnc, gotAccept)
	}
	if _, err := r.c.Segment(ctx, r.url("radio.example", "/big"), time.Second); code(err) != "too_large" {
		t.Fatalf("big: %v", err)
	}
	if _, err := r.c.Segment(ctx, r.url("radio.example", "/to-private"), time.Second); code(err) != "refused" {
		t.Fatalf("redirect: %v", err)
	}
}

func TestPodnetHLSPrivateTargetsRefused(t *testing.T) {
	for _, ip := range []string{"127.0.0.1", "10.1.2.3", "169.254.169.254", "100.64.0.1", "64:ff9b::a00:1", "::1"} {
		r := newRig(t, func(w http.ResponseWriter, q *http.Request) { io.WriteString(w, "#EXTM3U\n") })
		r.resolve["evil.example"] = ip
		if _, _, err := r.c.Playlist(context.Background(), r.url("evil.example", "/a.m3u8")); code(err) != "refused" {
			t.Errorf("%s playlist: %v", ip, err)
		}
		if _, err := r.c.Segment(context.Background(), r.url("evil.example", "/a.ts"), time.Second); code(err) != "refused" {
			t.Errorf("%s segment: %v", ip, err)
		}
		if r.hits.Load() != 0 {
			t.Errorf("%s: server reached", ip)
		}
	}
}

func TestPodnetHLSNoCookies(t *testing.T) {
	r := newRig(t, func(w http.ResponseWriter, q *http.Request) {
		if q.Header.Get("Cookie") != "" || q.Header.Get("Referer") != "" || q.Header.Get("User-Agent") == "" {
			t.Errorf("headers: %v", q.Header)
		}
		http.SetCookie(w, &http.Cookie{Name: "a", Value: "b"})
		io.WriteString(w, "x")
	})
	for i := 0; i < 2; i++ {
		if _, err := r.c.Segment(context.Background(), r.url("radio.example", "/s.ts"), time.Second); err != nil {
			t.Fatal(err)
		}
	}
}
