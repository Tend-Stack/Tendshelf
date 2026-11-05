//go:build dev

package web

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestVisualBookFirstDevices captures "Your devices" with six identical sign-ins, folded and open,
// at 390, 768, 1024 and 1440 (bookfirst-devices-*.png).
func TestVisualBookFirstDevices(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	a := newTestApp(t)
	owner := a.owner(t)
	for range 6 {
		settingsLogin(t, a, settingsChromeLinux)
	}
	base := devicesAsCookie(t, a, owner)
	for _, w := range []int{390, 768, 1024, 1440} {
		tab := b.NewTab(w, 900, w == 390, false)
		tab.Navigate(base + "/me/devices?theme=dark")
		tab.WaitFor(`document.querySelector(".ts-me__devgroup > summary") !== null`, 10*time.Second)
		tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("bookfirst-devices-folded-%d.png", w)))
		tab.Eval(`document.querySelector(".ts-me__devgroup").open = true`, nil)
		tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("bookfirst-devices-open-%d.png", w)))
	}
}

// TestBookFirstDevicesFold opens the folded sign-ins of "Your devices" by keyboard at 390 and 1440,
// checks every sign-out button is at least 44 px, and signs out one of the folded sessions.
func TestBookFirstDevicesFold(t *testing.T) {
	for _, w := range []int{390, 1440} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			b := chromium(t)
			a := newTestApp(t)
			owner := a.owner(t)
			var ids []string
			for range 3 {
				ids = append(ids, settingsDeviceID(t, settingsLogin(t, a, settingsChromeLinux)))
			}
			tab := b.NewTab(w, 900, w < 768, false)
			tab.Navigate(devicesAsCookie(t, a, owner) + "/me/devices")
			tab.WaitFor(`document.querySelector(".ts-me__devgroup > summary") !== null`, 10*time.Second)

			var open bool
			tab.Eval(`document.querySelector(".ts-me__devgroup").open`, &open)
			if open {
				t.Fatal("the fold is open before any key")
			}
			// Focus the summary and press Enter, as a keyboard person would.
			tab.Eval(`document.querySelector(".ts-me__devgroup > summary").focus()`, nil)
			for _, typ := range []string{"keyDown", "keyUp"} {
				params := map[string]any{"type": typ, "key": "Enter", "code": "Enter", "windowsVirtualKeyCode": 13}
				if typ == "keyDown" {
					params["text"] = "\r"
				}
				if err := tab.Call("Input.dispatchKeyEvent", params, nil); err != nil {
					t.Fatal(err)
				}
			}
			tab.WaitFor(`document.querySelector(".ts-me__devgroup").open === true`, 5*time.Second)

			var small []string
			tab.Eval(`[...document.querySelectorAll(".ts-me__devone button")].filter(e => { const r = e.getBoundingClientRect(); return r.height < 44 || r.width < 44; }).map(e => e.getAttribute("aria-label"))`, &small)
			if len(small) > 0 {
				t.Errorf("sign-out buttons under 44 px: %v", small)
			}
			var n int
			tab.Eval(`document.querySelectorAll(".ts-me__devone button").length`, &n)
			if n != len(ids) {
				t.Fatalf("%d sign-out buttons in the fold, want %d", n, len(ids))
			}

			playerClickSel(t, tab, `form[action="/me/sessions/`+ids[0]+`/signout"] button`)
			tab.WaitFor(`document.querySelectorAll(".ts-me__devone button").length === 2`, 10*time.Second)
			var path string
			tab.Eval(`location.pathname`, &path)
			if !strings.HasPrefix(path, "/me/devices") {
				t.Errorf("after signing out one, the page is %q, want /me/devices", path)
			}
		})
	}
}
