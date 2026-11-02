//go:build dev

package web

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// TestVisualMePages captures Profile and Settings in dark and light at four widths, the Password
// form in dark at 390 and 1440, and Settings in sepia and contrast at 390 and 1440. It fails on
// horizontal overflow, a target under 44 px, an unlabelled input or a page without exactly one h1.
// One more capture checks that the person's saved theme, not the cookie, sets data-theme. The
// ProfilePhone and SettingsPhone boards are copied beside the captures for a side-by-side look.
// The theme and width combinations come from visualMatrix (fast by default, full with make visual-full);
// the sentence above describes the full run.
func TestVisualMePages(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")

	app := newTestApp(t)
	app.owner(t)
	if err := db.PutSetting(context.Background(), app.st.Writer(), settingServerName, "The Reyes family shelf"); err != nil {
		t.Fatal(err)
	}
	settingsLogin(t, app, "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Mobile Safari/537.36")
	settingsLogin(t, app, "Mozilla/5.0 (X11; Linux x86_64; rv:130.0) Gecko/20100101 Firefox/130.0")
	deviceFor(t, app, sweepUserID(t, app, app.owner(t)), "Living room speaker", auth.KindMobile)
	base := themedServer(t, app.signedIn(t))

	for _, name := range []string{"ProfilePhone", "SettingsPhone"} {
		src, err := os.Open(filepath.Join("..", "..", "docs", "design", "tendshelf-handoff", "screens", "people", name+".png"))
		if err != nil {
			continue
		}
		if dst, err := os.Create(filepath.Join(out, "me-board-"+name+".png")); err == nil {
			_, _ = io.Copy(dst, src)
			_ = dst.Close()
		}
		_ = src.Close()
	}

	type capture struct {
		name, path, theme string
		w                 int
	}
	var caps []capture
	for _, sh := range visualMatrix(true) {
		caps = append(caps, capture{"settings", "/me/settings", sh.Theme, sh.Width})
	}
	for _, sh := range visualMatrix(false) {
		caps = append(caps, capture{"profile", "/me", sh.Theme, sh.Width}, capture{"password", "/me/password", sh.Theme, sh.Width})
	}
	for _, c := range caps {
		t.Run(fmt.Sprintf("%s-%s-%d", c.name, c.theme, c.w), func(t *testing.T) {
			tab := b.NewTab(c.w, 900, c.w == 390, false)
			tab.Navigate(base + c.path + "?theme=" + c.theme)
			var m browseProbe
			tab.Eval(browseProbeJS, &m)
			if m.Over {
				t.Errorf("horizontal overflow")
			}
			if m.Theme != c.theme {
				t.Errorf("data-theme = %q, want %q", m.Theme, c.theme)
			}
			if len(m.Small) > 0 {
				t.Errorf("targets under 44 px: %v", m.Small)
			}
			if len(m.Unlabelled) > 0 {
				t.Errorf("inputs without a label: %v", m.Unlabelled)
			}
			if m.H1 != 1 {
				t.Errorf("%d h1 elements", m.H1)
			}
			tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("me-%s-%s-%d.png", c.name, c.theme, c.w)))
		})
	}

	// The saved theme wins over the cookie the capture helper sends.
	t.Run("user-theme", func(t *testing.T) {
		u, _, err := app.srv.users.ByUsername(context.Background(), "ana")
		if err != nil {
			t.Fatal(err)
		}
		s := users.DefaultSettings
		s.Theme = "sepia"
		if err := app.srv.users.SaveSettings(context.Background(), u.ID, s); err != nil {
			t.Fatal(err)
		}
		tab := b.NewTab(390, 900, true, false)
		tab.Navigate(base + "/me/settings?theme=dark")
		var m browseProbe
		tab.Eval(browseProbeJS, &m)
		if m.Theme != "sepia" {
			t.Errorf("data-theme = %q, want the saved sepia", m.Theme)
		}
		tab.ScreenshotFull(filepath.Join(out, "me-settings-usertheme-sepia-390.png"))
	})
	t.Logf("screenshots in %s", out)
}
