//go:build dev

package web

import (
	"strings"
	"testing"
	"time"
)

// fakeChannel is the app window's message channel, recording what the page posts, installed before any
// page script runs (the real one is webkit.messageHandlers.external).
const fakeChannel = `window.__posted = [];
window.webkit = { messageHandlers: { external: { postMessage: (m) => window.__posted.push(String(m)) } } };`

// TestDeskBarBrowser drives the bar in Chromium with a fake window channel: the window buttons and the
// drag post their messages, More is a keyboard menu, Back and Forward follow real boosted navigation,
// the place follows the page, and nothing is covered by the bar at 390 and 1440 px.
func TestDeskBarBrowser(t *testing.T) {
	b := chromium(t)
	app := newTestApp(t)
	app.owner(t)
	base := themedServer(t, app.signedIn(t))
	for _, vp := range []struct {
		name          string
		width, height int
	}{{"mobile-390", 390, 844}, {"desktop-1440", 1440, 900}} {
		t.Run(vp.name, func(t *testing.T) {
			tab := b.NewTab(vp.width, vp.height, false, false)
			_ = tab.Call("Page.enable", map[string]any{}, nil)
			if err := tab.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": fakeChannel}, nil); err != nil {
				t.Fatal(err)
			}
			tab.Navigate(base + "/?shell=desktop")
			tab.WaitFor(`window.htmx !== undefined && document.querySelector("[data-wbar-back]") !== null`, 5*time.Second)

			var first struct {
				BarTop, BarBottom, SideTop    float64
				BackDisabled, ForwardDisabled bool
				Title                         string
				Scrolls                       bool
				Targets                       []string
			}
			tab.Eval(`(async () => {
			  await new Promise(r => setTimeout(r, 300));
			  const bar = document.querySelector("[data-wbar]").getBoundingClientRect();
			  const side = document.querySelector(".ts-nav-side, .ts-nav-rail");
			  const small = [...document.querySelectorAll(".ts-wbar__btn")].filter(b => b.offsetParent).filter(b => { const r = b.getBoundingClientRect(); return r.width < 44 || r.height < 44; }).map(b => b.getAttribute("aria-label"));
			  return {
			    barTop: bar.top, barBottom: bar.bottom,
			    sideTop: side && getComputedStyle(side).display !== "none" ? side.getBoundingClientRect().top : -1,
			    backDisabled: document.querySelector("[data-wbar-back]").disabled,
			    forwardDisabled: document.querySelector("[data-wbar-forward]").disabled,
			    title: document.querySelector("[data-wbar-title]").textContent,
			    scrolls: document.documentElement.scrollHeight > innerHeight + 1,
			    targets: small
			  };
			})()`, &first)
			if first.BarTop != 0 || first.BarBottom != 44 {
				t.Errorf("bar spans %v to %v, want 0 to 44", first.BarTop, first.BarBottom)
			}
			if first.SideTop != -1 && first.SideTop != 44 {
				t.Errorf("sidebar starts at %v, want under the bar at 44", first.SideTop)
			}
			if !first.BackDisabled || !first.ForwardDisabled {
				t.Errorf("first page: back disabled %v, forward disabled %v", first.BackDisabled, first.ForwardDisabled)
			}
			if first.Title != "Home" {
				t.Errorf("place shows %q", first.Title)
			}
			if len(first.Targets) > 0 {
				t.Errorf("window buttons under 44 px: %v", first.Targets)
			}

			// Window buttons and the drag.
			var posted []string
			tab.Eval(`(async () => {
			  for (const label of ["Minimise", "Maximise", "Close"]) document.querySelector('[aria-label="' + label + '"]').click();
			  const bar = document.querySelector("[data-wbar]");
			  bar.dispatchEvent(new MouseEvent("mousedown", { bubbles: true, button: 0, buttons: 1, detail: 1 }));
			  bar.dispatchEvent(new MouseEvent("mousemove", { bubbles: true, buttons: 1 }));
			  bar.dispatchEvent(new MouseEvent("mousemove", { bubbles: true, buttons: 1 }));
			  bar.dispatchEvent(new MouseEvent("dblclick", { bubbles: true, detail: 2 }));
			  return window.__posted.filter(m => m !== "wails:runtime:ready" && m !== "tendshelf:ready" && !m.startsWith("tendshelf:state "));
			})()`, &posted)
			want := "tendshelf:window minimise,tendshelf:window maximise,tendshelf:window close,wails:drag,tendshelf:window maximise"
			if got := strings.Join(posted, ","); got != want {
				t.Errorf("posted %q, want %q", got, want)
			}

			// The More menu from the keyboard.
			var menu struct {
				Opened, Focused, Closed, FocusBack bool
				Items                              int
			}
			tab.Eval(`(async () => {
			  const more = document.querySelector("[data-wbar-more]");
			  more.focus(); more.click();
			  const items = [...document.querySelectorAll("#desktop-menu [role^=menuitem]:not([hidden])")];
			  const opened = !document.querySelector("[data-wbar-menu]").hidden && more.getAttribute("aria-expanded") === "true";
			  const focused = document.activeElement === items[0];
			  document.activeElement.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowDown", bubbles: true }));
			  const second = document.activeElement === items[1];
			  document.activeElement.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true }));
			  return { opened, focused: focused && second, closed: document.querySelector("[data-wbar-menu]").hidden, focusBack: document.activeElement === more, items: items.length };
			})()`, &menu)
			if !menu.Opened || !menu.Focused || !menu.Closed || !menu.FocusBack || menu.Items != 7 {
				t.Errorf("menu %+v", menu)
			}

			// Real boosted navigation: Library, then Back, then Forward.
			var nav struct {
				AfterTitle, AfterBack, AfterForward string
				BackOn, ForwardOnAfterBack          bool
				Path                                string
			}
			tab.Eval(`(async () => {
			  const wait = (ms) => new Promise(r => setTimeout(r, ms));
			  const link = [...document.querySelectorAll('a[href="/libraries"]')].find(a => a.offsetParent);
			  link.click();
			  await wait(900);
			  const title = document.querySelector("[data-wbar-title]").textContent;
			  const backOn = !document.querySelector("[data-wbar-back]").disabled;
			  document.querySelector("[data-wbar-back]").click();
			  await wait(900);
			  const afterBack = document.querySelector("[data-wbar-title]").textContent + "|" + location.pathname;
			  const fwdOn = !document.querySelector("[data-wbar-forward]").disabled;
			  document.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowRight", altKey: true, bubbles: true }));
			  await wait(900);
			  return { afterTitle: title, afterBack, afterForward: document.querySelector("[data-wbar-title]").textContent, backOn, forwardOnAfterBack: fwdOn, path: location.pathname };
			})()`, &nav)
			if nav.AfterTitle == "Home" || nav.AfterTitle == "" {
				t.Errorf("place after navigation = %q", nav.AfterTitle)
			}
			if !nav.BackOn || !nav.ForwardOnAfterBack {
				t.Errorf("back enabled %v, forward enabled after back %v", nav.BackOn, nav.ForwardOnAfterBack)
			}
			if nav.AfterBack != "Home|/" {
				t.Errorf("after Back: %q", nav.AfterBack)
			}
			if nav.Path != "/libraries" || nav.AfterForward != nav.AfterTitle {
				t.Errorf("after Alt+Right: %q at %q, want %q at /libraries", nav.AfterForward, nav.Path, nav.AfterTitle)
			}
		})
	}
}
