package desktopshell

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestDesktopCheckNormalize(t *testing.T) {
	ok := map[string]string{
		"books.example.com":          "https://books.example.com",
		"Books.Example.COM/":         "https://books.example.com",
		"HTTP://Host.Local:8095/":    "http://host.local:8095",
		"  https://x.example.org  ":  "https://x.example.org",
		"localhost:8095":             "https://localhost:8095",
		"http://[::1]:8095":          "http://[::1]:8095",
		"https://books.example.com/": "https://books.example.com",
	}
	for in, want := range ok {
		if got, err := NormalizeServer(in); err != nil || got != want {
			t.Errorf("NormalizeServer(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{
		"", "   ", "ftp://x.example.com", "https://user:pw@x.example.com", "https://x.example.com/?a=1",
		"https://x.example.com/?", "https://x.example.com/#top", "https://x.example.com/library", "https://",
		"mailto:me@example.com", "javascript:alert(1)", "two words.com", "https://x.example.com:99999",
	} {
		if got, err := NormalizeServer(in); !errors.Is(err, ErrAddress) {
			t.Errorf("NormalizeServer(%q) = %q, %v; want ErrAddress", in, got, err)
		}
	}
}

func TestDesktopCheckPrivateHost(t *testing.T) {
	for origin, want := range map[string]bool{
		"http://127.0.0.1:8095":     true,
		"http://192.168.1.20":       true,
		"http://10.0.0.5:8095":      true,
		"http://[::1]":              true,
		"http://nas":                true,
		"http://books.local":        true,
		"http://shelf.home.arpa":    true,
		"http://books.example.com":  false,
		"http://8.8.8.8":            false,
		"http://172.32.0.1":         false,
		"http://notlocal.example.x": false,
	} {
		if got := IsPrivateHost(origin); got != want {
			t.Errorf("IsPrivateHost(%q) = %v, want %v", origin, got, want)
		}
	}
}

func TestDesktopCheckTable(t *testing.T) {
	serve := func(h http.HandlerFunc) string {
		srv := httptest.NewServer(h)
		t.Cleanup(srv.Close)
		return srv.URL
	}
	json := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(body))
		}
	}
	tls := httptest.NewTLSServer(json(`{"status":"ok","version":"x","app":"tendshelf","shell":"1"}`))
	t.Cleanup(tls.Close)
	closed := httptest.NewServer(http.NotFoundHandler())
	closedURL := closed.URL
	closed.Close()

	var cookies, auth atomic.Int32
	spy := serve(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Cookie") != "" {
			cookies.Add(1)
		}
		if r.Header.Get("Authorization") != "" {
			auth.Add(1)
		}
		json(`{"status":"ok","version":"x","app":"tendshelf","shell":"1"}`)(w, r)
	})

	cases := []struct {
		name, origin string
		want         error
	}{
		{"current server", serve(json(`{"status":"ok","version":"2026.1.0","app":"tendshelf","shell":"1"}`)), nil},
		{"current server, dev version", serve(json(`{"status":"ok","version":"","app":"tendshelf","shell":"1"}`)), nil},
		{"no connection", closedURL, ErrUnreachable},
		{"untrusted certificate", tls.URL, ErrCertificate},
		{"html page", serve(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>hi</html>")) }), ErrNotTendshelf},
		{"other json", serve(json(`{"hello":"world"}`)), ErrNotTendshelf},
		{"other app", serve(json(`{"status":"ok","version":"1","app":"other","shell":"1"}`)), ErrNotTendshelf},
		{"not found", serve(http.NotFound), ErrNotTendshelf},
		{"redirect", serve(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, "/elsewhere", http.StatusFound) }), ErrNotTendshelf},
		{"old server", serve(json(`{"status":"ok","version":"2026.0.9"}`)), ErrOldServer},
		{"old server with app", serve(json(`{"status":"ok","version":"2026.0.9","app":"tendshelf"}`)), ErrOldServer},
		{"oversized body", serve(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"status":"ok","version":"x","pad":"` + strings.Repeat("a", 8000) + `"}`))
		}), ErrNotTendshelf},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckServer(context.Background(), c.origin)
			if !errors.Is(err, c.want) || (c.want == nil) != (err == nil) {
				t.Errorf("CheckServer = %v, want %v", err, c.want)
			}
		})
	}
	if err := CheckServer(context.Background(), spy); err != nil {
		t.Fatal(err)
	}
	if cookies.Load() != 0 || auth.Load() != 0 {
		t.Errorf("the check sent credentials: %d cookies, %d authorization", cookies.Load(), auth.Load())
	}
}

func TestDesktopCheckWords(t *testing.T) {
	if Words(ErrOldServer) != "That server needs updating before this app can use it." {
		t.Error("old server words")
	}
	if Words(errors.New("secret detail")) != "Something went wrong. Please try again." {
		t.Error("unknown errors must not leak their text")
	}
}
