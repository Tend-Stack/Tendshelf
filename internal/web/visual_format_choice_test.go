//go:build dev

package web

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// TestVisualFormatChoice captures the two sheets that ask where to start in the other format: "Listen from"
// on the reader (the speaker) and "Read from" on a book page (the player's book icon fetches the same
// fragment into #sheet). It uses a book of its own with chapters and a duration, because the reader lab's
// audio has none and its listen sheet would be a 404.
func TestVisualFormatChoice(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	seedVisualIDs(t)
	l := pgSeed(t, 6, 6)
	rtocAudio(t, l)
	srv := brServe(t, l, l.owner)
	states := []struct{ name, path, ready string }{
		{"listen", brPath(l, "ch=3", "sheet=listen"), `document.querySelector("#sheet .ts-sheet") !== null`},
		{"read", "/books/" + l.book.ID, `window.htmx !== undefined`},
	}
	for _, s := range states {
		for _, sh := range visualMatrix(true) {
			s, sh := s, sh
			t.Run(fmt.Sprintf("%s-%s-%d", s.name, sh.Theme, sh.Width), func(t *testing.T) {
				tab := b.NewTab(sh.Width, 900, sh.Width == 390, false)
				sep := "?"
				if s.name == "listen" {
					sep = "&"
				}
				tab.Navigate(srv + s.path + sep + "theme=" + sh.Theme)
				tab.WaitFor(s.ready, 10*time.Second)
				if s.name == "read" {
					tab.Eval(`window.htmx.ajax("GET", "/hx/books/`+l.book.ID+`/read-from?ms=45000", { target: "#sheet" })`, nil)
					brSheetOpen(t, tab, nil)
				} else {
					time.Sleep(450 * time.Millisecond)
				}
				var m struct {
					Over  bool
					Theme string
					Small float64
				}
				tab.Eval(`(() => ({
  over: document.documentElement.scrollWidth > innerWidth,
  theme: document.documentElement.dataset.theme || "",
  small: Math.min(...[...document.querySelectorAll("#sheet .ts-choice a, #sheet [data-ts-close]")].map(e => e.getBoundingClientRect().height)),
}))()`, &m)
				switch {
				case m.Over:
					t.Errorf("horizontal overflow")
				case m.Theme != sh.Theme:
					t.Errorf("data-theme = %q, want %q", m.Theme, sh.Theme)
				case m.Small < 44:
					t.Errorf("a control is %.0f px high, under 44", m.Small)
				}
				tab.Screenshot(filepath.Join(out, fmt.Sprintf("fc-%s-%s-%d.png", s.name, sh.Theme, sh.Width)))
			})
		}
	}
}
