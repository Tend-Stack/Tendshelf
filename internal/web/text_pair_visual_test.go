//go:build dev

package web

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// TestTextPairVisual captures the pairing states of the Text tab (M5 R2 P2) beside the boards
// AddEbookPhone, AddEbookTablet and AddEbookDesktop: a file found in a folder with its checks, an
// unpaired suggestion, and two editions with their chips. It runs at 390, 768, 1024 and 1440 px in
// dark and light and fails on horizontal overflow or a target under 44 px.
// Opt-in: TENDSHELF_TEST_BROWSER. Everything on screen is synthetic and seeded.
func TestTextPairVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	for _, state := range []string{"found", "suggestion", "editions"} {
		l := newTextLab(t)
		l.run(t)
		switch state {
		case "found":
			l.textPairAdd(t, "salt-and-ember.epub", "Textword Lantern", "Imogen Textauthor", "folder")
			l.textPairAdd(t, "other-tale.epub", "A Different Tale", "Zed Else", "title")
		case "suggestion":
			l.textPairAdd(t, "salt-and-ember.epub", "Textword Lantern", "Imogen Textauthor", "")
		case "editions":
			l.textPairAdd(t, "first.epub", "Textword Lantern", "Imogen Textauthor", "person")
			l.textPairAdd(t, "second.epub", "Textword Lantern", "Imogen Textauthor", "person")
		}
		base := themedServer(t, l.a.signedIn(t))
		for _, theme := range []string{"dark", "light"} {
			for _, w := range []int{390, 768, 1024, 1440} {
				t.Run(fmt.Sprintf("textpair-%s-%s-%d", state, theme, w), func(t *testing.T) {
					tab := b.NewTab(w, 900, w == 390, false)
					tab.Navigate(base + l.path("") + "?theme=" + theme)
					tab.WaitFor(`document.querySelector(".ts-ebook") !== null`, 5*time.Second)
					time.Sleep(300 * time.Millisecond)
					var m browseProbe
					tab.Eval(browseProbeJS, &m)
					if m.Over {
						t.Errorf("horizontal overflow")
					}
					if len(m.Small) > 0 {
						t.Errorf("targets under 44 px: %v", m.Small)
					}
					tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("textpair-%s-%s-%d.png", state, theme, w)))
				})
			}
		}
	}
	t.Logf("screenshots in %s", out)
}
