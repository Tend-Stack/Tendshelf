package web

import (
	"net/http"
	"testing"
)

// Static assets are precompressed, but they still go through the security-headers wrapper: the CSP and
// nosniff stay on the gzip answer. /static/ bypasses the sign-in gate, so a signed-out client is fine.
func TestStaticGzipThroughHandler(t *testing.T) {
	app := newTestApp(t)
	url := app.srv.assets.URL("js/player.js")
	rec := send(app.h, http.MethodGet, url, nil, func(r *http.Request) { r.Header.Set("Accept-Encoding", "gzip") })
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	if rec.Header().Get("Content-Encoding") != "gzip" {
		t.Errorf("Content-Encoding %q, want gzip", rec.Header().Get("Content-Encoding"))
	}
	if rec.Header().Get("Content-Security-Policy") != contentSecurityPolicy {
		t.Errorf("CSP missing or wrong: %q", rec.Header().Get("Content-Security-Policy"))
	}
	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff missing")
	}
	if rec.Header().Get("Vary") != "Accept-Encoding" {
		t.Errorf("Vary %q", rec.Header().Get("Vary"))
	}
}
