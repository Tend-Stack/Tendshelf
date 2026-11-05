//go:build dev

package web

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// TestVisualFormats captures the surfaces of books in two formats (B2): a text book's page, an audiobook that
// was last read, the library list and grid of ebooks, and Home with a reading hero. Dark and light at 390, 768,
// 1024 and 1440 px, with the same checks as the book pages (overflow, targets, one h1, no style attribute). The
// three audio-side boards are not copied: no board shows these pages (the plan lists them for a design pass).
func TestVisualFormats(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	l := fmt2Seed(t)
	// Covers make the cards look like cards; the text book's reading place makes the hero.
	bookVisualCover(t, l.a, l.tbook, 1)
	bookVisualCover(t, l.a, l.book, 2)
	fmt2Listened(t, l, l.owner, l.book.ID, 1000)
	fmt2Reading(t, l, l.owner, l.book.ID, 4200, 2000)
	fmt2Reading(t, l, l.owner, l.tbook.ID, 3100, 5000)
	base := themedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(&http.Cookie{Name: l.owner.Name, Value: l.owner.Value})
		l.a.h.ServeHTTP(w, r)
	}))
	type page struct{ name, path string }
	pages := []page{
		{"text-book", "/books/" + l.tbook.ID},
		{"mixed-book", "/books/" + l.book.ID},
		{"text-tab", "/books/" + l.tbook.ID + "/text"},
		{"library-ebooks-grid", "/libraries?format=text"},
		{"library-ebooks-list", "/libraries?format=text&view=list"},
		{"library-all", "/libraries"},
		{"home-reading", "/"},
	}
	for _, p := range pages {
		for _, theme := range []string{"dark", "light"} {
			for _, w := range []int{390, 768, 1024, 1440} {
				t.Run(fmt.Sprintf("%s-%s-%d", p.name, theme, w), func(t *testing.T) {
					tab := b.NewTab(w, 900, w == 390, false)
					sep := "?"
					if len(p.path) > 1 && strings.Contains(p.path, "?") {
						sep = "&"
					}
					tab.Navigate(base + p.path + sep + "theme=" + theme)
					var m bookProbeResult
					tab.Eval(bookProbeJS, &m)
					if m.Over {
						t.Errorf("horizontal overflow")
					}
					if len(m.Small) > 0 {
						t.Errorf("targets under 44 px: %v", m.Small)
					}
					if m.H1 != 1 {
						t.Errorf("%d h1 elements", m.H1)
					}
					if m.Styled > 0 {
						t.Errorf("%d elements with a style attribute", m.Styled)
					}
					tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("formats-%s-%s-%d.png", p.name, theme, w)))
				})
			}
		}
	}
}
