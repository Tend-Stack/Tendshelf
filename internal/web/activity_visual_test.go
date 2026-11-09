//go:build dev

package web

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// TestActivityVisual captures Activity (S9) at 390, 768, 1024 and 1440 px in dark and light: the full list, one
// person's list and the empty state. No board exists for it, so it is checked against BRAND.md and the other
// admin pages. It fails on horizontal overflow or a target under 44 px. Opt-in: TENDSHELF_TEST_BROWSER.
// Times are old fixed dates, so "2 Jan" style text never changes between runs.
func TestActivityVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	for _, state := range []string{"empty", "list", "person"} {
		a := newTestApp(t)
		seedVisualIDs(t)
		ownerID := sweepUserID(t, a, a.owner(t))
		if _, err := a.st.Writer().Exec(`DELETE FROM audit_log`); err != nil {
			t.Fatal(err)
		}
		path := "/admin/activity"
		if state != "empty" {
			if _, err := a.st.Writer().Exec(`UPDATE users SET display_name = 'Wilkin Santana' WHERE id = ?`, ownerID); err != nil {
				t.Fatal(err)
			}
			anaID := sweepUserID(t, a, a.as(t, users.RoleMember))
			if _, err := a.st.Writer().Exec(`UPDATE users SET display_name = 'Ana María de los Ángeles Fernández-Villanueva Ortega' WHERE id = ?`, anaID); err != nil {
				t.Fatal(err)
			}
			_, f := librarytest.NewLibrary(t, a.lib, "Visual library", visualTempDir(t))
			book := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "The Very Long Title of a Book That Needs an Ellipsis on a Phone", DurationMs: 11 * sweepHourMs})
			day := time.Date(2026, time.January, 2, 9, 0, 0, 0, time.UTC).UnixMilli()
			seed := func(i int64, user, action, target string, detail any) {
				activitySeed(t, a, day+i*60_000, user, action, target, detail)
			}
			seed(1, ownerID, "invite.create", "invite:x", map[string]any{"role": "member"})
			seed(2, anaID, "invite.accept", "invite:x", nil)
			seed(3, anaID, "auth.login", anaID, map[string]string{"via": "invite"})
			seed(4, ownerID, "user.role", "user:"+anaID, map[string]any{"from": "member", "to": "admin"})
			seed(5, ownerID, "book.details.apply", "book:"+book.ID, nil)
			seed(6, "", "setup.code_failed", "", nil)
			seed(7, ownerID, "settings.online", "server", map[string]bool{"on": true})
			seed(8, anaID, "future.thing", "", nil)
			if state == "person" {
				path += "?user=" + anaID
			}
		}
		base := themedServer(t, a.signedIn(t))
		sep := "?"
		if state == "person" {
			sep = "&"
		}
		for _, theme := range []string{"dark", "light"} {
			for _, w := range []int{390, 768, 1024, 1440} {
				t.Run(fmt.Sprintf("activity-%s-%s-%d", state, theme, w), func(t *testing.T) {
					tab := b.NewTab(w, 900, w == 390, false)
					tab.Navigate(base + path + sep + "theme=" + theme)
					tab.WaitFor(`document.querySelector(".ts-activity") !== null`, 5*time.Second)
					time.Sleep(300 * time.Millisecond)
					var m browseProbe
					tab.Eval(browseProbeJS, &m)
					if m.Over {
						t.Errorf("horizontal overflow")
					}
					if len(m.Small) > 0 {
						t.Errorf("targets under 44 px: %v", m.Small)
					}
					tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("activity-%s-%s-%d.png", state, theme, w)))
				})
			}
		}
	}
	t.Logf("screenshots in %s", out)
}
