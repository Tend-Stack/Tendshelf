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

// TestEbookAdminVisual captures /admin/ebooks (M5 B3) beside the boards AddEbook* and NoMatchPhone: empty,
// "Maybe the same book" with "Could not be opened", the pick page with close matches and the pick page with
// none. It runs at 390, 768, 1024 and 1440 px in dark and light and fails on horizontal overflow or a target
// under 44 px.
// Opt-in: TENDSHELF_TEST_BROWSER. Everything on screen is synthetic and seeded.
func TestEbookAdminVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	for _, state := range []string{"empty", "rows", "guard", "pick-matches", "pick-none"} {
		l := newTextLab(t)
		l.run(t)
		path := "/admin/ebooks"
		switch state {
		case "rows":
			l.eadmText(t, "salt-and-ember.epub", "Salt and Ember", "Imogen Textauthor")
			l.eadmText(t, "other-tale.epub", "A Different Tale", "Imogen Textauthor")
			for _, st := range [][2]string{{"broken-tale.epub", "failed"}, {"the-glass-orchard.epub", "refused"}, {"waiting.epub", "pending"}} {
				reason := ""
				if st[1] == "refused" {
					reason = "drm"
				}
				id := l.addPlaced(t, st[0], st[1], reason)
				if _, err := l.a.st.Writer().Exec(`UPDATE ebook_files SET book_id = NULL, paired_by = NULL WHERE id = ?`, id); err != nil {
					t.Fatal(err)
				}
			}
		case "guard":
			f := l.textPairAdd(t, "guarded.epub", "Textword Lantern", "Imogen Textauthor", "")
			id, err := l.a.lib.CreateTextBook(context.Background(), library.TextBookIn{LibraryID: l.book.LibraryID, FolderID: l.book.FolderID,
				RootPath: filepath.Join(l.a.dir, "ebooks", f.ID), Title: "Textword Lantern"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := l.a.st.Writer().Exec(`UPDATE ebook_files SET book_id = ?, book_default = 1, paired_by = NULL WHERE id = ?`, id, f.ID); err != nil {
				t.Fatal(err)
			}
			if _, err := l.a.st.Writer().Exec(`INSERT INTO grants(id, user_id, scope, book_id, created_at, updated_at) VALUES ('gr_text', ?, 'book', ?, 1, 1)`,
				sweepUserID(t, l.a, l.reader), id); err != nil {
				t.Fatal(err)
			}
		case "pick-matches":
			path += "/" + l.eadmText(t, "salt-and-ember.epub", "Textword Lantern Again", "Imogen Textauthor").ID
		case "pick-none":
			path += "/" + l.eadmText(t, "other-tale.epub", "A Different Tale", "Zed Else").ID + "?q=Lantern"
		}
		base := themedServer(t, l.a.signedIn(t))
		sep := "?"
		if containsQuery(path) {
			sep = "&"
		}
		for _, theme := range []string{"dark", "light"} {
			for _, w := range []int{390, 768, 1024, 1440} {
				t.Run(fmt.Sprintf("ebooks-%s-%s-%d", state, theme, w), func(t *testing.T) {
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
					tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("ebooks-%s-%s-%d.png", state, theme, w)))
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
