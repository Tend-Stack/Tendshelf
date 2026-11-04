//go:build dev

package web

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// statsVisualApp stands the clock on Saturday 2026-11-07 10:00 UTC and seeds the ProfilePhone board's
// numbers: 6 h 12 m this week (Sunday to Friday, 62 minutes each), nine days in a row (three older
// days of 30 minutes), and 23 books finished this year.
func statsVisualApp(t *testing.T) (*testApp, string) {
	t.Helper()
	now := time.Date(2026, 11, 7, 10, 0, 0, 0, time.UTC)
	a := newTestApp(t, func(o *Options) { o.Now = func() time.Time { return now } })
	owner := a.owner(t)
	ownerID := sweepUserID(t, a, owner)
	statsZone(t, a, ownerID, "UTC")
	_, f := librarytest.NewLibrary(t, a.lib, "Audiobooks", adminAudioDir(t, false))
	first := ""
	for i := 0; i < 23; i++ {
		b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: fmt.Sprintf("Book %02d", i), DurationMs: 3_600_000})
		if first == "" {
			first = b.ID
		}
		statsFinish(t, a, ownerID, b.ID, time.Date(2026, 1+time.Month(i%10), 3, 12, 0, 0, 0, time.UTC))
	}
	for d := 1; d <= 6; d++ { // Sunday 1 Nov to Friday 6 Nov
		statsListen(t, a, ownerID, first, time.Date(2026, 11, d, 9, 0, 0, 0, time.UTC), 62)
	}
	for d := 29; d <= 31; d++ {
		statsListen(t, a, ownerID, first, time.Date(2026, 10, d, 9, 0, 0, 0, time.UTC), 30)
	}
	return a, ownerID
}

// TestStatsVisual captures Profile (with the "Your listening" panel) in dark and light at 390, 768, 1024
// and 1440 px (with make visual-full), and the person page an admin sees at 1440, with the ProfilePhone board copied beside
// them. The browser's own zone is pinned to UTC so the page never posts another one.
func TestStatsVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	a, ownerID := statsVisualApp(t)
	member := a.as(t, users.RoleMember)
	memberID := sweepUserID(t, a, member)
	_, f := librarytest.NewLibrary(t, a.lib, "More", adminAudioDir(t, false))
	mb := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "Member book", DurationMs: 3_600_000})
	statsZone(t, a, memberID, "UTC")
	statsListen(t, a, memberID, mb.ID, time.Date(2026, 11, 6, 9, 0, 0, 0, time.UTC), 95)
	base := themedServer(t, a.signedIn(t))

	if src, err := os.Open(filepath.Join("..", "..", "docs", "design", "tendshelf-handoff", "screens", "people", "ProfilePhone.png")); err == nil {
		if dst, err := os.Create(filepath.Join(out, "stats-board-ProfilePhone.png")); err == nil {
			_, _ = io.Copy(dst, src)
			_ = dst.Close()
		}
		_ = src.Close()
	}
	shoot := func(t *testing.T, path, theme string, w int, name string) {
		// A saved theme beats the ?theme= cookie, and statsZone saved "system": save the shot's theme.
		cur, err := a.srv.users.LoadSettings(t.Context(), ownerID)
		if err != nil {
			t.Fatal(err)
		}
		cur.Theme = theme
		if err := a.srv.users.SaveSettings(t.Context(), ownerID, cur); err != nil {
			t.Fatal(err)
		}
		tab := b.NewTab(w, 900, w == 390, false)
		if err := tab.Call("Emulation.setTimezoneOverride", map[string]any{"timezoneId": "UTC"}, nil); err != nil {
			t.Fatal(err)
		}
		tab.Navigate(base + path + "?theme=" + theme)
		tab.WaitFor(`document.getElementById("pf-stats") !== null`, 5*time.Second)
		var m browseProbe
		tab.Eval(browseProbeJS, &m)
		if m.Theme != theme {
			t.Errorf("data-theme = %q, want %q", m.Theme, theme)
		}
		if m.Over {
			t.Errorf("horizontal overflow")
		}
		if len(m.Small) > 0 {
			t.Errorf("targets under 44 px: %v", m.Small)
		}
		time.Sleep(200 * time.Millisecond)
		tab.ScreenshotFull(filepath.Join(out, name))
	}
	// The full matrix (make visual-full) is dark and light at all four widths; the fast one is two shots.
	for _, sh := range visualMatrix(false) {
		t.Run(fmt.Sprintf("profile-%s-%d", sh.Theme, sh.Width), func(t *testing.T) {
			shoot(t, "/me", sh.Theme, sh.Width, fmt.Sprintf("stats-profile-%s-%d.png", sh.Theme, sh.Width))
		})
	}
	t.Run("person-1440", func(t *testing.T) {
		shoot(t, "/admin/users/"+memberID, "dark", 1440, "stats-person-dark-1440.png")
	})
	t.Logf("screenshots in %s", out)
}
