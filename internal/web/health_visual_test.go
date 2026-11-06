//go:build dev

package web

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

// TestHealthVisual captures Library health (S3) at 390, 768, 1024 and 1440 px in dark and light: the all-clear
// page, the overview with every kind of problem, and one group's page. No board exists for it, so it is checked
// against BRAND.md and the other admin pages. It fails on horizontal overflow or a target under 44 px.
// Opt-in: TENDSHELF_TEST_BROWSER. Everything on screen is synthetic and seeded.
func TestHealthVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	for _, state := range []string{"clear", "overview", "group"} {
		a := newTestApp(t)
		seedVisualIDs(t)
		_, f := librarytest.NewLibrary(t, a.lib, "Visual library", visualTempDir(t))
		path := "/admin/health"
		if state != "clear" {
			add := func(s librarytest.BookSpec) library.Book { return librarytest.AddBook(t, a.lib, f, s) }
			add(librarytest.BookSpec{Title: "The Salt Road", DurationMs: 11 * sweepHourMs, Authors: []string{"Imogen Healthauthor"}})
			add(librarytest.BookSpec{Title: "A Lantern in March", MissingSince: 5})
			add(librarytest.BookSpec{Title: "Locked Away", Refused: library.RefusedDRM})
			bad := add(librarytest.BookSpec{Title: "Broken Reel", DurationMs: sweepHourMs})
			if _, err := a.st.Writer().Exec(`UPDATE audio_files SET probe_error = 'not_audio' WHERE book_id = ?`, bad.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := a.st.Writer().Exec(`INSERT INTO book_lookups (book_id, outcome, checked_at) VALUES (?, 'no_match', 1)`, bad.ID); err != nil {
				t.Fatal(err)
			}
			if state == "group" {
				path += "?group=no_cover"
			}
		}
		base := themedServer(t, a.signedIn(t))
		sep := "?"
		if state == "group" {
			sep = "&"
		}
		for _, theme := range []string{"dark", "light"} {
			for _, w := range []int{390, 768, 1024, 1440} {
				t.Run(fmt.Sprintf("health-%s-%s-%d", state, theme, w), func(t *testing.T) {
					tab := b.NewTab(w, 900, w == 390, false)
					tab.Navigate(base + path + sep + "theme=" + theme)
					tab.WaitFor(`document.querySelector(".ts-health") !== null`, 5*time.Second)
					time.Sleep(300 * time.Millisecond)
					var m browseProbe
					tab.Eval(browseProbeJS, &m)
					if m.Over {
						t.Errorf("horizontal overflow")
					}
					if len(m.Small) > 0 {
						t.Errorf("targets under 44 px: %v", m.Small)
					}
					tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("health-%s-%s-%d.png", state, theme, w)))
				})
			}
		}
	}
	t.Logf("screenshots in %s", out)
}
