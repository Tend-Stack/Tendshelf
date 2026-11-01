package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRoutes(t *testing.T) {
	h := NewHandler(Options{Version: "test"})
	cases := []struct {
		method, path string
		status       int
		contentType  string
	}{
		{http.MethodGet, "/", http.StatusOK, "text/html; charset=utf-8"},
		{http.MethodHead, "/", http.StatusOK, "text/html; charset=utf-8"},
		{http.MethodGet, "/healthz", http.StatusOK, "application/json"},
		{http.MethodGet, "/nope", http.StatusNotFound, "text/html; charset=utf-8"},
		{http.MethodPost, "/", http.StatusForbidden, "text/html; charset=utf-8"},
		{http.MethodPost, "/healthz", http.StatusForbidden, "text/html; charset=utf-8"},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, nil))
			if rec.Code != c.status {
				t.Fatalf("status = %d, want %d", rec.Code, c.status)
			}
			if c.contentType != "" && rec.Header().Get("Content-Type") != c.contentType {
				t.Fatalf("Content-Type = %q, want %q", rec.Header().Get("Content-Type"), c.contentType)
			}
			if got := rec.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
				t.Fatalf("CSP missing or wrong: %q", got)
			}
			if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("nosniff missing")
			}
		})
	}
}

func TestHealthz(t *testing.T) {
	rec := httptest.NewRecorder()
	NewHandler(Options{Version: "1.2.3"}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	var body map[string]string
	if err := json.NewDecoder(rec.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "ok" || body["version"] != "1.2.3" {
		t.Fatalf("body = %v", body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("healthz must not be cached")
	}
}
