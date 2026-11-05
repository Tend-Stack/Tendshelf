//go:build dev

package web

import (
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

// bookActionsProbeJS measures every visible button and link in the book page's play region.
const bookActionsProbeJS = `(() => {
  const out = [];
  for (const el of document.querySelectorAll("#book-play .ts-btn, #book-play .ts-split > *")) {
    const r = el.getBoundingClientRect();
    if (r.width === 0 || r.height === 0) continue;
    const cs = getComputedStyle(el);
    out.push({inBar: Boolean(el.closest(".ts-book__acts")), name: (el.textContent || "").trim().replace(/\s+/g, " "), cls: el.className, h: Math.round(r.height * 10) / 10, w: Math.round(r.width),
      radius: cs.borderTopLeftRadius, bg: cs.backgroundColor, border: cs.borderTopWidth + " " + cs.borderTopColor});
  }
  return out;
})()`

type bookActionProbe struct {
	Name, Cls, Radius, Bg, Border string
	InBar                         bool
	H                             float64
	W                             int
}

// TestVisualBookActionRow checks that every button in the action row has the same height, corner radius
// and style family, for a play-first and a read-first book, in the shared theme and width matrix (make visual-full: four themes at four widths). It also
// writes a capture of each at 390 and 1440 for the report.
func TestVisualBookActionRow(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	l := fmt2Seed(t)
	fmt2Listened(t, l, l.owner, l.book.ID, 5000)
	owner := l.owner
	h := themedServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.AddCookie(&http.Cookie{Name: owner.Name, Value: owner.Value})
		l.a.h.ServeHTTP(w, r)
	}))
	for _, first := range []string{"play", "read"} {
		if first == "read" {
			fmt2Reading(t, l, owner, l.book.ID, 4200, 9000)
		}
		for _, sh := range visualMatrix(true) {
			theme, w := sh.Theme, sh.Width
			{
				t.Run(fmt.Sprintf("%s-%s-%d", first, theme, w), func(t *testing.T) {
					tab := b.NewTab(w, 900, w == 390, false)
					tab.Navigate(h + "/books/" + l.book.ID + "?theme=" + theme)
					var got []bookActionProbe
					tab.Eval(bookActionsProbeJS, &got)
					if len(got) < 3 {
						t.Fatalf("only %d buttons: %+v", len(got), got)
					}
					for _, p := range got {
						t.Logf("%-22q h=%v w=%d radius=%s bg=%s border=%s", p.Name, p.H, p.W, p.Radius, p.Bg, p.Border)
					}
					// Every secondary button (Read, Listen, Add to shelf, Share, Download, Mark as finished) is the
					// same height with the same corner. On the phone the actions are one bar of segments (a
					// deliberate design), so only the buttons above the bar are compared there.
					var want *bookActionProbe
					for i, p := range got {
						if !strings.Contains(p.Cls, "ts-btn--secondary") || (w < 768 && p.InBar) {
							continue
						}
						if p.H < 44 || p.W < 44 {
							t.Errorf("%q is %vx%d, under 44 px", p.Name, p.W, int(p.H))
						}
						if want == nil {
							want = &got[i]
						} else if p.H != want.H || p.Radius != want.Radius || p.Border != want.Border {
							t.Errorf("%q (h=%v radius=%s border=%s) differs from %q (h=%v radius=%s border=%s)",
								p.Name, p.H, p.Radius, p.Border, want.Name, want.H, want.Radius, want.Border)
						}
					}
					if w == 390 || w == 1440 {
						if theme == "dark" {
							tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("book-actions-%s-%s-%d.png", first, theme, w)))
						}
					}
				})
			}
		}
	}
}
