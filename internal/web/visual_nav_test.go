package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// TestVisualShellNav captures the desktop shell with the full sidebar and with the rail, in every
// theme at 1280 and 1440 px. Skipped unless TENDSHELF_TEST_BROWSER names a Chromium-family binary.
func TestVisualShellNav(t *testing.T) {
	browser := os.Getenv("TENDSHELF_TEST_BROWSER")
	if browser == "" {
		t.Skip("TENDSHELF_TEST_BROWSER not set")
	}
	app := newTestApp(t)
	signed := app.signedIn(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		signed.ServeHTTP(w, r)
	}))
	defer srv.Close()

	out := os.Getenv("TENDSHELF_VISUAL_OUT")
	if out == "" {
		out = filepath.Join("..", "..", ".local", "tests", "visual", time.Now().UTC().Format("20060102T150405Z"))
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	profile := t.TempDir()
	u, _, err := app.srv.users.ByUsername(context.Background(), "ana")
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"full", "rail"} {
		for _, theme := range []string{"dark", "light", "sepia", "contrast"} {
			cur, err := app.srv.users.LoadSettings(context.Background(), u.ID)
			if err != nil {
				t.Fatal(err)
			}
			cur.Nav, cur.Theme = mode, theme
			if err := app.srv.users.SaveSettings(context.Background(), u.ID, cur); err != nil {
				t.Fatal(err)
			}
			for _, w := range []int{1280, 1440} {
				t.Run(fmt.Sprintf("%s-%s-%d", mode, theme, w), func(t *testing.T) {
					args := []string{"--headless", "--disable-gpu", "--hide-scrollbars", "--no-first-run",
						"--user-data-dir=" + profile, fmt.Sprintf("--window-size=%d,900", w)}
					args = append(args, cdp.SandboxArgs()...)
					shot := filepath.Join(out, fmt.Sprintf("shell-nav-%s-%s-%d.png", mode, theme, w))
					args = append(args, "--screenshot="+shot, srv.URL+"/")
					if b, err := exec.Command(browser, args...).CombinedOutput(); err != nil {
						t.Fatalf("screenshot: %v\n%s", err, b)
					}
				})
			}
		}
	}
	t.Logf("screenshots in %s", out)
}
