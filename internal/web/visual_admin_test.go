//go:build dev

package web

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

const adminProbeJS = `(() => {
  const small = [];
  for (const el of document.querySelectorAll("main input:not([type=hidden]), main button, main a.ts-btn, main summary, main select, body.ts-frame input:not([type=hidden]), body.ts-frame button, body.ts-frame a.ts-btn")) {
    // A checkbox is 24 px square and counts through its label row, as a finger would hit it.
    const box = el.type === "checkbox" ? el.closest("label") || el : el;
    const r = box.getBoundingClientRect();
    if (r.width > 0 && r.height < 43.5) small.push((el.id || el.className || el.tagName) + ":" + Math.round(r.height));
  }
  const labels = [...document.querySelectorAll("input:not([type=hidden]), select")].filter(i => !document.querySelector('label[for="' + i.id + '"]')).map(i => i.id || i.name);
  return {over: document.documentElement.scrollWidth > innerWidth, theme: document.documentElement.dataset.theme || "",
    small, unlabelled: labels, h1: document.querySelectorAll("h1").length};
})()`

// adminSeed fills a test app the way the board shows it: one library of 14 books (4 still being
// read), a protected one, a missing one, and a scan that is still running. It returns the scan
// job id.
func adminSeed(t *testing.T, a *testApp) string {
	t.Helper()
	l, f := librarytest.NewLibrary(t, a.lib, "Audiobooks", "/srv/media/audiobooks")
	titles := []string{"Saltmarsh", "The Salt Road Letters", "Weather for Beginners", "Lantern Street", "The Long Tide",
		"A Quiet Orchard", "Harbour Lights", "Ninety Winters"}
	for i, title := range titles {
		librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{
			Title: title, DurationMs: int64(i+5) * 3_600_000, Authors: []string{"Imogen Hale"},
			Narrators: []string{"Tomas Reyes"}, Series: map[bool]string{true: "The Tide Books"}[i < 3], Sequence: fmt.Sprint(i + 1),
		})
	}
	for _, title := range []string{"Slow Rivers", "Paper Boats", "Ember", "Glasshouse"} {
		librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: title, Unsettled: true})
	}
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "The Glass Orchard, part 2", Refused: library.RefusedDRM})
	librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Gone Fishing", MissingSince: 1})
	return a.adminEnqueueScan(t, l.ID)
}

// TestVisualAdminPages captures the setup steps and the Libraries admin pages in every theme at
// four widths and fails on horizontal overflow, small touch targets, an unlabelled input or a page
// without exactly one h1. The board PNGs are copied beside the captures for a side-by-side look.
// The theme and width combinations come from visualMatrix (fast by default, full with make visual-full);
// the sentence above describes the full run.
func TestVisualAdminPages(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")

	app := newTestApp(t)
	job := adminSeed(t, app)
	base := themedServer(t, app.signedIn(t))

	boards := map[string]string{
		"Onboarding": "people", "OnboardFolder": "people", "OnboardDone": "people",
		"LibrariesPhone": "admin", "LibrariesTablet": "admin", "LibrariesPage": "admin", "LibProblems": "admin",
	}
	for name, dir := range boards {
		src, err := os.Open(filepath.Join("..", "..", "docs", "design", "tendshelf-handoff", "screens", dir, name+".png"))
		if err != nil {
			continue
		}
		if dst, err := os.Create(filepath.Join(out, "admin-board-"+name+".png")); err == nil {
			_, _ = io.Copy(dst, src)
			_ = dst.Close()
		}
		_ = src.Close()
	}

	pgs := []struct{ name, path string }{
		{"setup-server", "/setup/server"},
		{"setup-folder", "/setup/folder?job=" + job},
		{"setup-done", "/setup/done"},
		{"libraries", "/admin/libraries"},
		{"add-folder", "/admin/libraries/folders/new"},
	}
	for _, pg := range pgs {
		for _, sh := range visualMatrix(pg.name == "libraries") {
			theme, w := sh.Theme, sh.Width
			t.Run(fmt.Sprintf("%s-%s-%d", pg.name, theme, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w == 390, false)
				sep := "?"
				if strings.Contains(pg.path, "?") {
					sep = "&"
				}
				tab.Navigate(base + pg.path + sep + "theme=" + theme)
				var m struct {
					Over       bool     `json:"over"`
					Theme      string   `json:"theme"`
					Small      []string `json:"small"`
					Unlabelled []string `json:"unlabelled"`
					H1         int      `json:"h1"`
				}
				tab.Eval(adminProbeJS, &m)
				if m.Over {
					t.Errorf("horizontal overflow")
				}
				if m.Theme != theme {
					t.Errorf("data-theme = %q, want %q", m.Theme, theme)
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
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("admin-%s-%s-%d.png", pg.name, theme, w)))
			})
		}
	}
	t.Logf("screenshots in %s", out)
}
