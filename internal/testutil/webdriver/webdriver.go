// Package webdriver is a minimal W3C WebDriver client over net/http, used by Tendshelf's own visual
// tests to drive WebKitGTK (WebKitWebDriver with MiniBrowser). It needs no module and only tests
// import it. MiniBrowser has no headless mode: run it under xvfb-run to keep the window hidden.
package webdriver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Session is one automation session.
type Session struct {
	t    testing.TB
	base string
	id   string
	hc   *http.Client
}

// Start runs the driver on a free port, opens a session with the browser binary and ends both when
// the test finishes.
func Start(t testing.TB, driver, browser string) *Session {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	cmd := exec.Command(driver, fmt.Sprintf("--port=%d", port))
	if err := cmd.Start(); err != nil {
		t.Fatalf("start %s: %v", driver, err)
	}
	t.Cleanup(func() {
		_ = cmd.Process.Kill() // the process this test started, by its own handle
		_ = cmd.Wait()
	})
	s := Attach(t, fmt.Sprintf("http://127.0.0.1:%d", port), browser)
	return s
}

// Attach opens a session against a driver that is already listening at base.
func Attach(t testing.TB, base, browser string) *Session {
	t.Helper()
	s := &Session{t: t, base: base, hc: &http.Client{Timeout: 60 * time.Second}}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := s.do(http.MethodGet, "/status", nil); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("webdriver did not become ready in 10 s")
		}
		time.Sleep(100 * time.Millisecond)
	}
	caps := map[string]any{"capabilities": map[string]any{"alwaysMatch": map[string]any{
		"browserName": "MiniBrowser",
		"webkitgtk:browserOptions": map[string]any{
			"binary": browser,
			"args":   []string{"--automation"},
		},
	}}}
	raw, err := s.do(http.MethodPost, "/session", caps)
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	var v struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(raw, &v); err != nil || v.SessionID == "" {
		t.Fatalf("new session: bad answer %s", raw)
	}
	s.id = v.SessionID
	t.Cleanup(func() { _, _ = s.do(http.MethodDelete, "/session/"+s.id, nil) })
	return s
}

// do sends one request and returns the "value" of the W3C answer.
func (s *Session) do(method, path string, body any) (json.RawMessage, error) {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rdr = bytes.NewReader(b)
	} else if method == http.MethodPost {
		rdr = strings.NewReader("{}")
	}
	req, err := http.NewRequest(method, s.base+path, rdr)
	if err != nil {
		return nil, err
	}
	if rdr != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var env struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("%s %s: status %d, body %.200s", method, path, resp.StatusCode, raw)
	}
	if resp.StatusCode >= 400 {
		var e struct {
			Error   string `json:"error"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(env.Value, &e)
		return nil, fmt.Errorf("%s %s: %s: %s", method, path, e.Error, e.Message)
	}
	return env.Value, nil
}

func (s *Session) call(method, sub string, body any) json.RawMessage {
	s.t.Helper()
	v, err := s.do(method, "/session/"+s.id+sub, body)
	if err != nil {
		s.t.Fatal(err)
	}
	return v
}

// Exec runs a script body (it must `return` its value) and decodes the result into out, which may be nil.
func (s *Session) Exec(script string, out any) {
	s.t.Helper()
	v := s.call(http.MethodPost, "/execute/sync", map[string]any{"script": script, "args": []any{}})
	if out != nil {
		if err := json.Unmarshal(v, out); err != nil {
			s.t.Fatalf("decode %s: %v", v, err)
		}
	}
}

// Eval runs a JavaScript expression and decodes its value.
func (s *Session) Eval(expr string, out any) {
	s.t.Helper()
	s.Exec("return ("+expr+");", out)
}

// ExecAsync runs a script that calls its last argument (arguments[arguments.length - 1]) with the result.
func (s *Session) ExecAsync(script string, out any) {
	s.t.Helper()
	v := s.call(http.MethodPost, "/execute/async", map[string]any{"script": script, "args": []any{}})
	if out != nil {
		if err := json.Unmarshal(v, out); err != nil {
			s.t.Fatalf("decode %s: %v", v, err)
		}
	}
}

// SetViewport resizes the window until innerWidth is w (within 1 px). WebKitGTK has a minimum
// window width, so a width below it fails the test instead of silently measuring another layout.
func (s *Session) SetViewport(w, h int) {
	s.t.Helper()
	width, height := w, h
	for attempt := 0; attempt < 2; attempt++ {
		s.call(http.MethodPost, "/window/rect", map[string]any{"x": 0, "y": 0, "width": width, "height": height})
		var inner struct {
			W float64 `json:"w"`
			H float64 `json:"h"`
		}
		s.Eval(`({w: window.innerWidth, h: window.innerHeight})`, &inner)
		if d := int(inner.W) - w; d >= -1 && d <= 1 {
			return
		}
		width -= int(inner.W) - w
		height -= int(inner.H) - h
	}
	var inner float64
	s.Eval(`window.innerWidth`, &inner)
	s.t.Fatalf("innerWidth = %.0f after resizing, want %d", inner, w)
}

// Navigate loads url and waits until the document and its fonts have loaded.
func (s *Session) Navigate(url string) {
	s.t.Helper()
	s.call(http.MethodPost, "/url", map[string]any{"url": url})
	deadline := time.Now().Add(20 * time.Second)
	for {
		var ok bool
		s.Eval(`document.readyState === "complete" && document.fonts.status === "loaded"`, &ok)
		if ok {
			return
		}
		if time.Now().After(deadline) {
			s.t.Fatal("page did not finish loading in 20 s")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Screenshot writes a PNG of the viewport to path.
func (s *Session) Screenshot(path string) {
	s.t.Helper()
	var b64 string
	if err := json.Unmarshal(s.call(http.MethodGet, "/screenshot", nil), &b64); err != nil {
		s.t.Fatal(err)
	}
	png, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(path, png, 0o644); err != nil {
		s.t.Fatal(err)
	}
}
