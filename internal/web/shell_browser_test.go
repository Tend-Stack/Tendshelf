//go:build dev

package web

import (
	"testing"
	"time"
)

// TestShellBrowser opens the app the way the desktop shell does (GET /?shell=desktop) in Chromium at
// 390 and 1440 px: data-shell is set, no service worker is registered, Downloads is hidden, and
// desktop.js exits at once because a browser has no webkit.messageHandlers.external (the context menu
// stays the browser's own). Needs TENDSHELF_TEST_BROWSER.
func TestShellBrowser(t *testing.T) {
	b := chromium(t)
	app := newTestApp(t)
	app.owner(t)
	base := themedServer(t, app.signedIn(t))
	for _, vp := range []struct {
		name          string
		width, height int
		mobile        bool
	}{{"mobile-390", 390, 844, true}, {"desktop-1440", 1440, 900, false}} {
		t.Run(vp.name, func(t *testing.T) {
			tab := b.NewTab(vp.width, vp.height, vp.mobile, false)
			tab.Navigate(base + "/?shell=desktop&next=/me/downloads")
			tab.WaitFor(`window.htmx !== undefined`, 5*time.Second)
			var d struct {
				Shell, Path             string
				DesktopScript, Sheet    bool
				Workers                 int
				Manifest                bool
				DownloadsLinkDisplay    string
				ContextMenuPrevented    bool
				ManagerHidden, Sentence bool
			}
			tab.Eval(`(async () => {
			  await new Promise(r => setTimeout(r, 500));
			  const probe = document.createElement("a");
			  probe.href = "/me/downloads";
			  document.body.append(probe);
			  const menu = new MouseEvent("contextmenu", { bubbles: true, cancelable: true });
			  document.body.dispatchEvent(menu);
			  const regs = navigator.serviceWorker ? await navigator.serviceWorker.getRegistrations() : [];
			  return {
			    shell: document.documentElement.dataset.shell || "",
			    path: location.pathname,
			    desktopScript: Boolean(document.querySelector('script[src*="desktop"]')),
			    sheet: Boolean(document.querySelector('link[href*="pages-desktop"]')),
			    workers: regs.length,
			    manifest: Boolean(document.querySelector('link[rel="manifest"]')),
			    downloadsLinkDisplay: getComputedStyle(probe).display,
			    contextMenuPrevented: menu.defaultPrevented,
			    managerHidden: !document.querySelector("[data-ts-storage]"),
			    sentence: document.body.textContent.includes("This app plays straight from your library or server.")
			  };
			})()`, &d)
			if d.Shell != "desktop" || d.Path != "/me/downloads" {
				t.Errorf("shell %q at %q", d.Shell, d.Path)
			}
			if !d.DesktopScript || !d.Sheet {
				t.Errorf("desktop.js %v, pages-desktop.css %v", d.DesktopScript, d.Sheet)
			}
			if d.Workers != 0 || d.Manifest {
				t.Errorf("%d workers, manifest %v", d.Workers, d.Manifest)
			}
			if d.DownloadsLinkDisplay != "none" {
				t.Errorf("Downloads link display = %q", d.DownloadsLinkDisplay)
			}
			if d.ContextMenuPrevented {
				t.Error("desktop.js ran without the app channel")
			}
			if !d.ManagerHidden || !d.Sentence {
				t.Errorf("download manager hidden %v, sentence %v", d.ManagerHidden, d.Sentence)
			}
		})
	}
}
