//go:build dev

package web

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

// TestChapterEditVisual captures the Edit chapters page (nineteen edited chapters, so the reset link
// shows) and the reset confirmation at 390, 768, 1024 and 1440 px, as chapters-edit-*. It fails on
// horizontal overflow, a target under 44 px, an unlabelled field, a page without one h1, a skipped
// heading level or an inline style. No board exists for this page.
func TestChapterEditVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	app := newTestApp(t)
	app.owner(t)
	bookID := bookmarkVisualSeedChapters(t, app)
	base := themedServer(t, app.signedIn(t))
	shots := visualMatrix(true)
	pages := []struct{ name, path string }{
		{"page", "/books/" + bookID + "/chapters/edit"},
		{"reset", "/books/" + bookID + "/chapters/reset"},
	}
	for _, p := range pages {
		for _, sh := range shots {
			p, sh := p, sh
			t.Run(fmt.Sprintf("%s-%s-%d", p.name, sh.Theme, sh.Width), func(t *testing.T) {
				tab := b.NewTab(sh.Width, 900, sh.Width == 390, false)
				tab.Navigate(base + p.path + "?theme=" + sh.Theme)
				time.Sleep(200 * time.Millisecond)
				var m bookProbeResult
				tab.Eval(bookProbeJS, &m)
				switch {
				case m.Over:
					t.Errorf("horizontal overflow")
				case m.Theme != sh.Theme:
					t.Errorf("data-theme = %q, want %q", m.Theme, sh.Theme)
				}
				if len(m.Small) > 0 {
					t.Errorf("targets under 44 px: %v", m.Small)
				}
				if len(m.Unlabelled) > 0 {
					t.Errorf("inputs without a label: %v", m.Unlabelled)
				}
				if m.H1 != 1 || m.Skip || m.Styled > 0 {
					t.Errorf("h1 = %d, skipped level = %v, styled = %d", m.H1, m.Skip, m.Styled)
				}
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("chapters-edit-%s-%s-%d.png", p.name, sh.Theme, sh.Width)))
			})
		}
	}
}

// bookmarkVisualSeedChapters seeds the Harbour Lights book with nineteen chapters saved as the owner's own.
func bookmarkVisualSeedChapters(t *testing.T, a *testApp) string {
	t.Helper()
	bookID, _, _, _ := bookVisualSeed(t, a)
	const chapterMs = 3_300_000
	var chs []library.Chapter
	for i := 0; i < 19; i++ {
		chs = append(chs, library.Chapter{StartMs: int64(i) * chapterMs, EndMs: int64(i+1) * chapterMs, Title: fmt.Sprintf("Chapter %d", i+1), Source: "user"})
	}
	if err := a.lib.Update(context.Background(), func(tx *library.Tx) error { return tx.ReplaceChapters(bookID, chs) }); err != nil {
		t.Fatal(err)
	}
	return bookID
}
