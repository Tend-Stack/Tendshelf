package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// probeJS is test-only. It records whether the page overflows horizontally.
const probeJS = `document.documentElement.dataset.probe = JSON.stringify({overflowX: document.documentElement.scrollWidth > window.innerWidth});`

// TestVisualShell captures the empty shell in every theme at four widths and fails on horizontal
// overflow. Skipped unless TENDSHELF_TEST_BROWSER names a Chromium-family binary. Output goes
// under .local/tests/visual/<run-id>/ (git-ignored) for a human to review.
func TestVisualShell(t *testing.T) {
	browser := os.Getenv("TENDSHELF_TEST_BROWSER")
	if browser == "" {
		t.Skip("TENDSHELF_TEST_BROWSER not set")
	}
	app := newTestApp(t).signedIn(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/__probe.js" {
			w.Header().Set("Content-Type", "text/javascript; charset=utf-8")
			_, _ = io.WriteString(w, probeJS)
			return
		}
		if theme := r.URL.Query().Get("theme"); theme != "" {
			r.AddCookie(&http.Cookie{Name: themeCookie, Value: theme})
		}
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, r)
		for k, v := range rec.Header() {
			w.Header()[k] = v
		}
		body := rec.Body.Bytes()
		if strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			body = bytes.Replace(body, []byte("</head>"), []byte(`<script type="module" src="/__probe.js"></script></head>`), 1)
			w.Header().Del("Content-Length")
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	runID := time.Now().UTC().Format("20060102T150405Z")
	out := filepath.Join("..", "..", ".local", "tests", "visual", runID)
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	profile := t.TempDir() // removed by the test framework
	probe := regexp.MustCompile(`data-probe="([^"]*)"`)

	for _, theme := range []string{"dark", "light", "sepia", "contrast"} {
		for _, w := range []int{390, 768, 1024, 1440} {
			t.Run(fmt.Sprintf("%s-%d", theme, w), func(t *testing.T) {
				url := fmt.Sprintf("%s/?theme=%s", srv.URL, theme)
				base := []string{"--headless", "--disable-gpu", "--hide-scrollbars", "--no-first-run",
					"--user-data-dir=" + profile, fmt.Sprintf("--window-size=%d,900", w)}
				shot := filepath.Join(out, fmt.Sprintf("shell-%s-%d.png", theme, w))
				if b, err := exec.Command(browser, append(base, "--screenshot="+shot, url)...).CombinedOutput(); err != nil {
					t.Fatalf("screenshot: %v\n%s", err, b)
				}
				dom, err := exec.Command(browser, append(base, "--dump-dom", "--virtual-time-budget=3000", url)...).Output()
				if err != nil {
					t.Fatalf("dump-dom: %v", err)
				}
				m := probe.FindSubmatch(dom)
				if m == nil {
					t.Fatal("probe result missing from the DOM")
				}
				var res struct {
					OverflowX bool `json:"overflowX"`
				}
				if err := json.Unmarshal([]byte(strings.ReplaceAll(string(m[1]), "&quot;", `"`)), &res); err != nil {
					t.Fatalf("probe %q: %v", m[1], err)
				}
				if res.OverflowX {
					t.Fatalf("horizontal overflow at %d px in theme %s", w, theme)
				}
			})
		}
	}
	t.Logf("screenshots in %s", out)
}
