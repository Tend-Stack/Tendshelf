package webdriver

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeDriver answers the few WebDriver calls the client makes, with a window whose inner width is
// 20 px narrower than what was asked for (as a window with decorations is).
func fakeDriver(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	width := 0
	reply := func(w http.ResponseWriter, v any) {
		_ = json.NewEncoder(w).Encode(map[string]any{"value": v})
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		body, _ := io.ReadAll(r.Body)
		seen = append(seen, r.Method+" "+r.URL.Path)
		switch {
		case r.URL.Path == "/status":
			reply(w, map[string]any{"ready": true})
		case r.URL.Path == "/session" && r.Method == http.MethodPost:
			if !strings.Contains(string(body), `"binary":"/bin/browser"`) || !strings.Contains(string(body), `--automation`) {
				w.WriteHeader(http.StatusBadRequest)
				reply(w, map[string]any{"error": "invalid argument", "message": string(body)})
				return
			}
			reply(w, map[string]any{"sessionId": "s1"})
		case strings.HasSuffix(r.URL.Path, "/window/rect"):
			var req struct{ Width int }
			_ = json.Unmarshal(body, &req)
			width = req.Width
			reply(w, map[string]any{})
		case strings.HasSuffix(r.URL.Path, "/execute/sync"):
			if strings.Contains(string(body), "innerWidth") {
				reply(w, map[string]any{"w": width - 20, "h": 800})
				return
			}
			reply(w, 42)
		case strings.HasSuffix(r.URL.Path, "/screenshot"):
			reply(w, base64.StdEncoding.EncodeToString([]byte("png")))
		default:
			reply(w, nil)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func TestClientAgainstFakeDriver(t *testing.T) {
	srv, seen := fakeDriver(t)
	s := Attach(t, srv.URL, "/bin/browser")
	s.SetViewport(447, 844)
	var n int
	s.Eval(`6*7`, &n)
	if n != 42 {
		t.Fatalf("Eval = %d", n)
	}
	path := filepath.Join(t.TempDir(), "s.png")
	s.Screenshot(path)
	if b, err := os.ReadFile(path); err != nil || string(b) != "png" {
		t.Fatalf("screenshot %q, %v", b, err)
	}
	// The window was resized twice: once as asked, once corrected by the 20 px of decoration.
	rects := 0
	for _, r := range *seen {
		if strings.HasSuffix(r, "/window/rect") {
			rects++
		}
	}
	if rects != 2 {
		t.Errorf("%d window/rect calls, want 2 (%v)", rects, *seen)
	}
}
