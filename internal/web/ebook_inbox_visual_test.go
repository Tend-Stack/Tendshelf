//go:build dev

package web

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// TestEbookInboxVisual captures the Ebooks to pair inbox (M5 R2 P3) beside the boards AddEbook* and
// NoMatchPhone: empty, rows (a match, no match, kept on its own), the pick page with close matches
// and the pick page with none. It runs at 390, 768, 1024 and 1440 px in dark and light and fails on
// horizontal overflow or a target under 44 px.
// Opt-in: TENDSHELF_TEST_BROWSER. Everything on screen is synthetic and seeded.
func TestEbookInboxVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	for _, state := range []string{"empty", "rows", "pick-matches", "pick-none"} {
		l := newTextLab(t)
		l.run(t)
		path := "/admin/ebooks"
		switch state {
		case "rows":
			l.textPairAdd(t, "salt-and-ember.epub", "Textword Lantern", "Imogen Textauthor", "")
			l.textPairAdd(t, "other-tale.epub", "A Different Tale", "Zed Else", "")
			kept := l.textPairAdd(t, "kept-tale.epub", "Kept Almanac", "Zed Else", "")
			l.inboxPost(t, l.owner, "/admin/ebooks/"+kept.ID+"/keep", nil, false)
		case "pick-matches":
			path += "/" + l.textPairAdd(t, "salt-and-ember.epub", "Textword Lantern", "Imogen Textauthor", "").ID
		case "pick-none":
			path += "/" + l.textPairAdd(t, "other-tale.epub", "A Different Tale", "Zed Else", "").ID + "?q=Lantern"
		}
		base := themedServer(t, l.a.signedIn(t))
		sep := "?"
		if containsQuery(path) {
			sep = "&"
		}
		for _, theme := range []string{"dark", "light"} {
			for _, w := range []int{390, 768, 1024, 1440} {
				t.Run(fmt.Sprintf("inbox-%s-%s-%d", state, theme, w), func(t *testing.T) {
					tab := b.NewTab(w, 900, w == 390, false)
					tab.Navigate(base + path + sep + "theme=" + theme)
					tab.WaitFor(`document.querySelector(".ts-admin") !== null`, 5*time.Second)
					time.Sleep(300 * time.Millisecond)
					var m browseProbe
					tab.Eval(browseProbeJS, &m)
					if m.Over {
						t.Errorf("horizontal overflow")
					}
					if len(m.Small) > 0 {
						t.Errorf("targets under 44 px: %v", m.Small)
					}
					tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("inbox-%s-%s-%d.png", state, theme, w)))
				})
			}
		}
	}
	t.Logf("screenshots in %s", out)
}

func containsQuery(p string) bool {
	for _, c := range p {
		if c == '?' {
			return true
		}
	}
	return false
}
