package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSoundSpikeServes(t *testing.T) {
	dir := t.TempDir()
	audioFile := filepath.Join(dir, "x.mp3")
	if err := os.WriteFile(audioFile, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(dir, "log.jsonl")
	srv := httptest.NewServer(newHandler(audioFile, logPath))
	defer srv.Close()

	for _, path := range []string{"/", "/spike.js", "/spike.css", "/manifest.webmanifest", "/audio/a", "/audio/b"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil || resp.StatusCode != 200 {
			t.Fatalf("GET %s: %v %v", path, err, resp)
		}
		if !strings.Contains(resp.Header.Get("Content-Security-Policy"), "script-src 'self'") {
			t.Errorf("GET %s: the app's CSP is missing", path)
		}
		resp.Body.Close()
	}

	req, _ := http.NewRequest("GET", srv.URL+"/audio/a", nil)
	req.Header.Set("Range", "bytes=2-4")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("Range: %v %v", err, resp)
	}
	resp.Body.Close()

	resp, err = http.Get(srv.URL + "/etc/passwd")
	if err != nil || resp.StatusCode != 404 {
		t.Errorf("unknown path: %v %v", err, resp)
	}

	resp, err = http.Post(srv.URL+"/log", "application/json", strings.NewReader(`{"mode":"chain"}`))
	if err != nil || resp.StatusCode != 204 {
		t.Fatalf("POST /log: %v %v", err, resp)
	}
	resp, err = http.Post(srv.URL+"/log", "application/json", strings.NewReader(`nope`))
	if err != nil || resp.StatusCode != 400 {
		t.Errorf("bad log line: %v %v", err, resp)
	}
	got, _ := os.ReadFile(logPath)
	if !strings.Contains(string(got), `"page":{"mode":"chain"}`) || strings.Count(string(got), "\n") != 1 {
		t.Errorf("log = %q", got)
	}
}

func TestSoundSpikePageHasNoInlineCode(t *testing.T) {
	b, _ := assets.ReadFile("page.html")
	page := string(b)
	for _, bad := range []string{" style=", " onclick=", "<style"} {
		if strings.Contains(page, bad) {
			t.Errorf("page.html contains %q", bad)
		}
	}
	if !strings.Contains(page, `<script src="/spike.js"`) || !strings.Contains(page, "apple-mobile-web-app-capable") {
		t.Error("page.html lacks the external script or the Home Screen meta")
	}
}

func writeAudio(t *testing.T, dir string) string {
	t.Helper()
	p := filepath.Join(dir, "x.mp3")
	if err := os.WriteFile(p, []byte("0123456789"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSoundSpikeContentTypes(t *testing.T) {
	dir := t.TempDir()
	srv := httptest.NewServer(newHandler(writeAudio(t, dir), filepath.Join(dir, "log.jsonl")))
	defer srv.Close()
	for path, want := range map[string]string{
		"/":                     "text/html",
		"/spike.js":             "text/javascript",
		"/spike.css":            "text/css",
		"/manifest.webmanifest": "application/manifest+json",
		"/audio/a":              "audio/mpeg",
		"/audio/b":              "audio/mpeg",
	} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 200 || !strings.HasPrefix(resp.Header.Get("Content-Type"), want) {
			t.Errorf("GET %s = %d %q, want %q", path, resp.StatusCode, resp.Header.Get("Content-Type"), want)
		}
	}
}

func TestSoundSpikeLogRefusals(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "log.jsonl")
	srv := httptest.NewServer(newHandler(writeAudio(t, dir), logPath))
	defer srv.Close()
	big := `{"x":"` + strings.Repeat("a", maxLogBody) + `"}`
	for name, body := range map[string]string{"not json": "nope", "oversized": big} {
		resp, err := http.Post(srv.URL+"/log", "application/json", strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400", name, resp.StatusCode)
		}
	}
	if _, err := os.Stat(logPath); err == nil {
		t.Error("a refused body wrote a log line")
	}
}

func TestSoundSpikeCheckFile(t *testing.T) {
	dir := t.TempDir()
	named := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	tests := []struct {
		name string
		path string
		ok   bool
	}{
		{"audio", named("a.mp3"), true},
		{"missing", filepath.Join(dir, "none.mp3"), false},
		{"directory", dir, false},
		{"aax", named("a.aax"), false},
		{"aaxc", named("a.aaxc"), false},
		{"aa upper case", named("a.AA"), false},
		{"m4p", named("a.m4p"), false},
	}
	for _, tc := range tests {
		if err := checkFile(tc.path); (err == nil) != tc.ok {
			t.Errorf("%s: checkFile = %v, want ok=%v", tc.name, err, tc.ok)
		}
	}
}
