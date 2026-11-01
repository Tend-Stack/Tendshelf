package cdp

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// TestClientSmoke drives a real browser; it runs only when TENDSHELF_TEST_BROWSER names one.
func TestClientSmoke(t *testing.T) {
	chrome := os.Getenv("TENDSHELF_TEST_BROWSER")
	if chrome == "" {
		t.Skip("TENDSHELF_TEST_BROWSER not set")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = w.Write([]byte(`<!doctype html><meta name=viewport content="width=device-width"><title>t</title><p id=a>hello</p>`))
	}))
	defer srv.Close()
	b := Start(t, chrome)
	tab := b.NewTab(390, 600, true, true)
	tab.Navigate(srv.URL)
	var got struct {
		Text    string `json:"text"`
		Width   int    `json:"w"`
		Reduced bool   `json:"reduced"`
	}
	tab.Eval(`({text: document.getElementById('a').textContent, w: innerWidth, reduced: matchMedia('(prefers-reduced-motion: reduce)').matches})`, &got)
	if got.Text != "hello" || got.Width != 390 || !got.Reduced {
		t.Fatalf("got %+v", got)
	}
	shot := filepath.Join(t.TempDir(), "s.png")
	tab.Screenshot(shot)
	if fi, err := os.Stat(shot); err != nil || fi.Size() < 100 {
		t.Fatalf("screenshot: %v", err)
	}
}
