//go:build dev

package web

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const fbProbeJS = `(() => {
  const small = [];
  for (const el of document.querySelectorAll(".ts-folders a, .ts-folders button, .ts-sheet a:not(.ts-scrim), main a.ts-btn, main input:not([type=hidden]), body.ts-frame input:not([type=hidden]), body.ts-frame a.ts-btn, body.ts-frame button")) {
    const r = el.getBoundingClientRect();
    if (r.width > 0 && r.height < 43.5) small.push((el.id || el.className || el.tagName) + ":" + Math.round(r.height));
  }
  const layer = document.querySelector("#sheet .ts-sheet");
  const sheetBottom = layer ? Math.round(layer.getBoundingClientRect().bottom) : -1;
  return {over: document.documentElement.scrollWidth > innerWidth, theme: document.documentElement.dataset.theme || "",
    small, rows: document.querySelectorAll(".ts-folders__row").length, sheet: !!layer, sheetBottom, h: innerHeight,
    panel: !!document.querySelector(".ts-folders--panel")};
})()`

// TestVisualFolderBrowser captures the folder browser in the Add a folder sheet and inline on setup
// step 3, in every theme at four widths, and fails on overflow or a touch target under 44 px.
// The theme and width combinations come from visualMatrix (fast by default, full with make visual-full);
// the sentence above describes the full run.
func TestVisualFolderBrowser(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")

	root := visualTempDir(t)
	for _, d := range []string{"Audiobooks", "Podcasts", "Kids", "Radio plays", "Courses", "Archive 2019", "Downloads"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "one.mp3"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if src, err := os.Open(filepath.Join("..", "..", "docs", "design", "tendshelf-handoff", "screens", "people", "OnboardFolder.png")); err == nil {
		if dst, err := os.Create(filepath.Join(out, "folders-board-OnboardFolder.png")); err == nil {
			_, _ = io.Copy(dst, src)
			_ = dst.Close()
		}
		_ = src.Close()
	}

	app := newTestApp(t)
	base := themedServer(t, app.signedIn(t))

	pgs := []struct {
		name, path string
		sheet      bool
	}{
		{"sheet", addFolderURL + "?browse=" + url.QueryEscape(root), true},
		{"sheet-places", addFolderURL + "?browse=", true},
		{"setup", setupFolderURL + "?browse=" + url.QueryEscape(root), false},
		{"setup-places", setupFolderURL + "?browse=", false},
		{"field", setupFolderURL, false},
	}
	for _, pg := range pgs {
		for _, sh := range visualMatrix(pg.name == "sheet") {
			theme, w := sh.Theme, sh.Width
			t.Run(fmt.Sprintf("%s-%s-%d", pg.name, theme, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w == 390, false)
				sep := "?"
				if strings.Contains(pg.path, "?") {
					sep = "&"
				}
				tab.Navigate(base + pg.path + sep + "theme=" + theme)
				var m struct {
					Over        bool     `json:"over"`
					Theme       string   `json:"theme"`
					Small       []string `json:"small"`
					Rows        int      `json:"rows"`
					Sheet       bool     `json:"sheet"`
					SheetBottom int      `json:"sheetBottom"`
					H           int      `json:"h"`
					Panel       bool     `json:"panel"`
				}
				time.Sleep(450 * time.Millisecond) // the sheet slides up in 200 ms
				tab.Eval(fbProbeJS, &m)
				if m.Over {
					t.Errorf("horizontal overflow")
				}
				if m.Theme != theme {
					t.Errorf("data-theme = %q, want %q", m.Theme, theme)
				}
				if len(m.Small) > 0 {
					t.Errorf("targets under 44 px: %v", m.Small)
				}
				if pg.name != "field" && m.Rows == 0 {
					t.Errorf("no folder rows")
				}
				if pg.sheet && (!m.Sheet || m.SheetBottom > m.H) {
					t.Errorf("sheet missing or taller than the window: %+v", m)
				}
				if pg.name == "setup" && !m.Panel {
					t.Errorf("no inline panel")
				}
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("folders-%s-%s-%d.png", pg.name, theme, w)))
			})
		}
	}
	t.Logf("screenshots in %s", out)
}
