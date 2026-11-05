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
