//go:build dev

package web

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

// TestDuplicatesVisual captures the Duplicate books pages (S4) at 390, 768, 1024 and 1440 px in dark and light:
// the empty page, the list with a same-audio and a same-details group, the confirm pages (same files and different
// files, with a share link) and the list of copies that were merged. No board exists for them, so the page is
// checked against BRAND.md and the other admin pages. It fails on horizontal overflow or a target under 44 px.
// Opt-in: TENDSHELF_TEST_BROWSER. Everything on screen is synthetic and seeded.
func TestDuplicatesVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	for _, state := range []string{"empty", "list", "confirm-same", "confirm-different", "merged"} {
		a := newTestApp(t)
		seedVisualIDs(t)
		owner := sweepUserID(t, a, a.owner(t))
		l, f := librarytest.NewLibrary(t, a.lib, "Visual library", visualTempDir(t))
		_ = l
		add := func(title, author, narrator string, dur int64, root string) string {
			bk := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: title, Authors: []string{author}, Narrators: []string{narrator}, DurationMs: dur, Files: 2})
			if _, err := a.st.Writer().Exec(`UPDATE books SET root_path = ? WHERE id = ?`, filepath.Join(f.Path, root), bk.ID); err != nil {
				t.Fatal(err)
			}
			return bk.ID
		}
		same := func(hash string, ids ...string) {
			for _, id := range ids {
				if _, err := a.st.Writer().Exec(`UPDATE audio_files SET size = 20000000 + idx, quick_hash = ? WHERE book_id = ?`, strings.Repeat(hash, 64), id); err != nil {
					t.Fatal(err)
				}
			}
		}
		path := "/admin/duplicates"
		if state != "empty" {
			k1 := add("The Salt Road", "Imogen Dupauthor", "Theo Dupvoice", 11*sweepHourMs, "Imogen Dupauthor/The Salt Road")
			d1 := add("The Salt Road", "Imogen Dupauthor", "Theo Dupvoice", 11*sweepHourMs, "Downloads/The Salt Road (copy)")
			same("a", k1, d1)
			k2 := add("A Lantern in March", "Imogen Dupauthor", "Nora Dupvoice", 7*sweepHourMs, "Imogen Dupauthor/A Lantern in March")
			d2 := add("A Lantern in March", "Imogen Dupauthor", "Nora Dupvoice", 7*sweepHourMs+20_000, "Old rip/A Lantern in March")
			for _, id := range []string{d1, d2} {
				if _, err := a.st.Writer().Exec(`INSERT INTO progress(user_id, book_id, position_ms, duration_ms, client_updated_at, server_updated_at, device_id) VALUES (?, ?, 600000, ?, 5, 5, 'device-test-1')`, owner, id, 7*sweepHourMs); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := a.st.Writer().Exec(`INSERT INTO bookmarks(id, user_id, book_id, position_ms, created_at, updated_at) VALUES ('bm_vis', ?, ?, 9, 1, 1)`, owner, d1); err != nil {
				t.Fatal(err)
			}
			switch state {
			case "confirm-same":
				path += "/merge?keep=" + k1 + "&drop=" + d1
			case "confirm-different":
				if _, err := a.st.Writer().Exec(`INSERT INTO share_links(id, token_hash, book_id, created_by, expires_at, created_at) VALUES ('sl_vis', randomblob(32), ?, ?, 1000, 1)`, d2, owner); err != nil {
					t.Fatal(err)
				}
				path += "/merge?keep=" + k2 + "&drop=" + d2
			case "merged":
				if err := a.lib.MergeBooks(context.Background(), k1, d1, owner); err != nil {
					t.Fatal(err)
				}
				path += "/merged"
			}
		}
		base := themedServer(t, a.signedIn(t))
		sep := "?"
		if strings.Contains(path, "?") {
			sep = "&"
		}
		for _, theme := range []string{"dark", "light"} {
			for _, w := range []int{390, 768, 1024, 1440} {
				t.Run(fmt.Sprintf("duplicates-%s-%s-%d", state, theme, w), func(t *testing.T) {
					tab := b.NewTab(w, 900, w == 390, false)
					tab.Navigate(base + path + sep + "theme=" + theme)
					tab.WaitFor(`document.querySelector(".ts-dup") !== null`, 5*time.Second)
					time.Sleep(300 * time.Millisecond)
					var m browseProbe
					tab.Eval(browseProbeJS, &m)
					if m.Over {
						t.Errorf("horizontal overflow")
					}
					if len(m.Small) > 0 {
						t.Errorf("targets under 44 px: %v", m.Small)
					}
					tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("duplicates-%s-%s-%d.png", state, theme, w)))
				})
			}
		}
	}
	t.Logf("screenshots in %s", out)
}
