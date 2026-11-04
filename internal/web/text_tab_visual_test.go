//go:build dev

package web

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// TestTextTabVisual captures the Text tab (M5 R1) beside the boards AddEbookPhone, AddEbookTablet,
// AddEbookDesktop and ProtectedPhone, and the chapter list beside ContentsPhone: empty (the add form),
// listed (a ready edition and a waiting read) and refused (the copy-protection card). It runs at 390,
// 768, 1024 and 1440 px in dark and light and fails on horizontal overflow or a target under 44 px.
// Opt-in: TENDSHELF_TEST_BROWSER. Everything on screen is synthetic and seeded.
func TestTextTabVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	for _, state := range []string{"empty", "list", "refused"} {
		l := newTextLab(t)
		switch state {
		case "list":
			l.run(t)
			l.addReady(t, "salt-and-ember.epub", "Salt and Ember", 14)
			l.addPlaced(t, "waiting.epub", "pending", "")
		case "refused":
			l.addPlaced(t, "the-glass-orchard.epub", "refused", "drm")
		}
		base := themedServer(t, l.a.signedIn(t))
		for _, theme := range []string{"dark", "light"} {
			for _, w := range []int{390, 768, 1024, 1440} {
				t.Run(fmt.Sprintf("text-%s-%s-%d", state, theme, w), func(t *testing.T) {
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
					tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("text-%s-%s-%d.png", state, theme, w)))
				})
			}
		}
	}
	t.Logf("screenshots in %s", out)
}
