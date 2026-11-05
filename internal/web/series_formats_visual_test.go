//go:build dev

package web

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// TestSeriesFormatsVisual captures the surfaces that show reading beside listening (package L): the library
// grid and list with a thin line on started books, a series page, the book page's series rail and Home's
// series row with its Read control. Dark and light at 390, 768, 1024 and 1440 px, with the book page's checks.
// No board shows these (the line was added by the operator's answer on 2026-11-04).
func TestSeriesFormatsVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	l := fmt2Seed(t)
	lsSeries(t, l)
	bookVisualCover(t, l.a, l.tbook, 1)
	bookVisualCover(t, l.a, l.book, 2)
	fmt2Reading(t, l, l.owner, l.tbook.ID, 4200, 5000)
	fmt2Listened(t, l, l.owner, l.book.ID, 9000)
	var seriesID string
	if err := l.a.st.Reader().QueryRow(`SELECT id FROM series WHERE name = 'Saga'`).Scan(&seriesID); err != nil {
		t.Fatal(err)
	}
	base := themedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(&http.Cookie{Name: l.owner.Name, Value: l.owner.Value})
		l.a.h.ServeHTTP(w, r)
	}))
	type page struct{ name, path string }
	pages := []page{
		{"library-grid", "/libraries"},
		{"library-list", "/libraries?view=list"},
		{"series", "/series/" + seriesID},
		{"book-rail", "/books/" + l.tbook.ID},
		{"home", "/"},
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
					tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("series-formats-%s-%s-%d.png", p.name, theme, w)))
				})
			}
		}
	}
}
