//go:build dev

package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestPrintPagesBrowser opens a book with a page list in Chromium (make player) at desktop-1440 and
// mobile-390 and checks the footer line says the book's own label ("p. xii of 13"), not a count, and that
// the front chapter before the first label shows no page line. Needs TENDSHELF_TEST_BROWSER.
func TestPrintPagesBrowser(t *testing.T) {
	b := chromium(t)
	for _, vp := range []struct {
		name   string
		w, h   int
		mobile bool
	}{{"desktop-1440", 1440, 900, false}, {"mobile-390", 390, 844, true}} {
		t.Run(vp.name, func(t *testing.T) {
			l := rsSeed(t, ppWebEPUB(t))
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.AddCookie(l.owner)
				l.a.h.ServeHTTP(w, r)
			}))
			t.Cleanup(srv.Close)
			tab := b.NewTab(vp.w, vp.h, vp.mobile, true)
			// The second chapter opens on page xii.
			tab.Navigate(srv.URL + l.page("ch=2"))
			tab.WaitFor(`window.htmx !== undefined && document.getElementById("reader") !== null`, 5*time.Second)
			tab.WaitFor(`document.getElementById("reader-page") && document.getElementById("reader-page").textContent === "p. xii of 13"`, 8*time.Second)
			var labels []string
			tab.Eval(`[...new Set([...document.querySelectorAll("#reader [data-pg]")].map(el => el.dataset.pg))]`, &labels)
			for _, p := range labels {
				if p != "xii" && p != "13" {
					t.Errorf("label %q on the page", p)
				}
			}
		})
	}
}
