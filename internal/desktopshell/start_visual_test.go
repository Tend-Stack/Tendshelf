package desktopshell

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

const startProbeJS = `(() => {
  const small = [];
  for (const el of document.querySelectorAll("input:not([type=hidden]), button")) {
    const r = el.getBoundingClientRect();
    if (r.height < 43.5) small.push((el.id || el.className || el.tagName) + ":" + Math.round(r.height));
  }
  const unlabelled = [...document.querySelectorAll("input:not([type=hidden])")]
    .filter(i => !document.querySelector('label[for="' + i.id + '"]')).map(i => i.id);
  return {over: document.documentElement.scrollWidth > innerWidth, theme: document.documentElement.dataset.theme || "",
    small, unlabelled, h1: document.querySelectorAll("h1").length, shell: document.documentElement.dataset.shell};
})()`

// TestDesktopStartVisual captures the picker in the four themes at 390, 1024 and 1440 px (opt-in:
// TENDSHELF_TEST_BROWSER) and fails on overflow, a target under 44 px, an unlabelled field or a
// missing shell mark.
func TestDesktopStartVisual(t *testing.T) {
	path := os.Getenv("TENDSHELF_TEST_BROWSER")
	if path == "" {
		t.Skip("TENDSHELF_TEST_BROWSER not set")
	}
	b := cdp.Start(t, path)
	out := os.Getenv("TENDSHELF_VISUAL_OUT")
	if out == "" {
		out = filepath.Join(os.TempDir(), "tendshelf-visual")
	}
	out = filepath.Join(out, "chromium")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}

	h, _ := newStart(t, &fakeStarter{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if theme := r.URL.Query().Get("theme"); theme != "" {
			r.AddCookie(&http.Cookie{Name: "ts_theme", Value: theme})
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	for _, theme := range []string{"dark", "light", "sepia", "contrast"} {
		for _, w := range []int{390, 1024, 1440} {
			t.Run(fmt.Sprintf("%s-%d", theme, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w == 390, false)
				tab.Navigate(srv.URL + "/?theme=" + theme)
				var m struct {
					Over       bool     `json:"over"`
					Theme      string   `json:"theme"`
					Small      []string `json:"small"`
					Unlabelled []string `json:"unlabelled"`
					H1         int      `json:"h1"`
					Shell      string   `json:"shell"`
				}
				tab.Eval(startProbeJS, &m)
				if m.Over {
					t.Error("horizontal overflow")
				}
				if m.Theme != theme || m.Shell != "desktop" {
					t.Errorf("data-theme %q, data-shell %q", m.Theme, m.Shell)
				}
				if len(m.Small) > 0 {
					t.Errorf("targets under 44 px: %v", m.Small)
				}
				if len(m.Unlabelled) > 0 {
					t.Errorf("inputs without a label: %v", m.Unlabelled)
				}
				if m.H1 != 1 {
					t.Errorf("%d h1 elements", m.H1)
				}
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("desktop-start-%s-%d.png", theme, w)))
			})
		}
	}
}
