//go:build dev

package web

import (
	"fmt"
	"testing"
	"time"
)

// longPage makes every page tall without an inline style (the CSP forbids them): a constructed stylesheet adds
// 4000 px after #main's content, and it survives boosted navigation because it lives on the document.
const longPage = `(() => {
  const sheet = new CSSStyleSheet();
  sheet.replaceSync('#main::after, .ts-prefs__pane::after { content: ""; display: block; height: 4000px; }');
  document.adoptedStyleSheets = [sheet];
})()`

// pressKey sends one real key press to the focused page.
func pressKey(t *testing.T, tab interface {
	Call(string, any, any) error
}, key string, code int) {
	t.Helper()
	for _, typ := range []string{"rawKeyDown", "keyUp"} {
		if err := tab.Call("Input.dispatchKeyEvent", map[string]any{"type": typ, "key": key, "code": key, "windowsVirtualKeyCode": code}, nil); err != nil {
			t.Fatal(err)
		}
	}
}

// TestDeskScrollerBrowser: in the desktop shell #main is the scroller (decision 0011 addendum, 2026-11-15). The
// document never scrolls, the rail and the player bar do not move while #main scrolls, #main ends on top of the
// player bar and the bar runs to the window's right edge, #main's scrollbar shows left of the Now playing panel,
// a boosted navigation opens at the top and Back restores the position, PageDown works at first load, and the
// settings menu sticks under the title bar. Run at 1440 and 1024 in full, and the scroller and navigation
// checks at 768 and 390.
func TestDeskScrollerBrowser(t *testing.T) {
	b := chromium(t)
	app := newTestApp(t)
	app.owner(t)
	base := themedServer(t, app.signedIn(t))
	for _, vp := range []struct {
		width, height int
		full          bool
	}{{1440, 900, true}, {1024, 800, true}, {768, 800, false}, {390, 844, false}} {
		t.Run(fmt.Sprint(vp.width), func(t *testing.T) {
			tab := b.NewTab(vp.width, vp.height, false, true) // reduced motion: margins settle at once
			_ = tab.Call("Page.enable", map[string]any{}, nil)
			if err := tab.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": fakeChannel + longPage}, nil); err != nil {
				t.Fatal(err)
			}
			tab.Navigate(base + "/?shell=desktop")
			tab.WaitFor(`window.htmx !== undefined && document.querySelector("[data-wbar-back]") !== null`, 5*time.Second)
			tab.WaitFor(`document.activeElement === document.getElementById("main")`, 3*time.Second)

			// (7) a key press scrolls the page at once, nobody clicked it.
			pressKey(t, tab, "PageDown", 34)
			tab.WaitFor(`document.getElementById("main").scrollTop > 0`, 3*time.Second)
			tab.Eval(`document.getElementById("main").scrollTop = 0`, nil)

			// (1) the document stays put; #main holds the page.
			var root struct {
				DocTop, DocRange, MainRange float64
				Overflow                    string
			}
			tab.Eval(`(async () => {
			  const m = document.getElementById("main");
			  m.scrollTop = 2000;
			  await new Promise(r => setTimeout(r, 100));
			  const d = document.scrollingElement;
			  return { docTop: d.scrollTop, docRange: d.scrollHeight - d.clientHeight, mainRange: m.scrollHeight - m.clientHeight, overflow: getComputedStyle(m).overflowY };
			})()`, &root)
			if root.DocTop != 0 || root.DocRange != 0 {
				t.Errorf("the document scrolls: top %v, range %v", root.DocTop, root.DocRange)
			}
			if root.Overflow != "auto" || root.MainRange < 3000 {
				t.Errorf("#main is not the scroller: overflow-y %q, range %v", root.Overflow, root.MainRange)
			}

			// (2) nothing pinned moves with the page; (3) and (4) the bottom edge meets the bar.
			tab.Eval(`(() => { const p = document.getElementById("player"); p.hidden = false; document.getElementById("main").scrollTop = 0; })()`, nil)
			const rects = `(async () => {
			  await new Promise(r => setTimeout(r, 150));
			  const m = document.getElementById("main");
			  const box = (e) => { if (!e || getComputedStyle(e).display === "none") return null; const r = e.getBoundingClientRect(); return [r.left, r.top, r.right, r.bottom]; };
			  const out = { rail: box(document.querySelector(".ts-nav-side, .ts-nav-rail")), slot: box(document.getElementById("player")), main: box(m), inner: [innerWidth, innerHeight] };
			  return out;
			})()`
			type pinned struct {
				Rail, Slot, Main []float64
				Inner            []float64
			}
			var before, after pinned
			tab.Eval(rects, &before)
			tab.Eval(`document.getElementById("main").scrollTop = 1500`, nil)
			tab.Eval(rects, &after)
			if fmt.Sprint(before.Rail) != fmt.Sprint(after.Rail) || fmt.Sprint(before.Slot) != fmt.Sprint(after.Slot) {
				t.Errorf("pinned layers moved: rail %v -> %v, bar %v -> %v", before.Rail, after.Rail, before.Slot, after.Slot)
			}
			if vp.width >= 768 {
				if before.Slot == nil {
					t.Fatal("no player bar")
				}
				if got, want := before.Main[3], before.Slot[1]; got < want-0.5 || got > want+0.5 {
					t.Errorf("#main ends at %v, the bar starts at %v", got, want)
				}
				if before.Slot[2] != before.Inner[0] {
					t.Errorf("the bar's right edge is %v, the window's is %v", before.Slot[2], before.Inner[0])
				}
				tab.Eval(`document.getElementById("player").hidden = true`, nil)
				var hidden pinned
				tab.Eval(rects, &hidden)
				if hidden.Main[3] != hidden.Inner[1] {
					t.Errorf("without the bar #main ends at %v, the window at %v", hidden.Main[3], hidden.Inner[1])
				}
				tab.Eval(`document.getElementById("player").hidden = false`, nil)
			}

			// (5) the panel takes a column of its own: #main and its scrollbar end where the panel begins.
			if vp.full && vp.width >= 1280 {
				var panel struct {
					MainRight, PanelLeft, Gutter float64
				}
				tab.Eval(`(async () => {
				  const d = document.querySelector("dialog.ts-player");
				  d.show();
				  await new Promise(r => setTimeout(r, 200));
				  const m = document.getElementById("main");
				  return { mainRight: m.getBoundingClientRect().right, panelLeft: d.getBoundingClientRect().left, gutter: m.offsetWidth - m.clientWidth };
				})()`, &panel)
				if panel.MainRight > panel.PanelLeft+0.5 || panel.Gutter < 1 {
					t.Errorf("with the panel open #main ends at %v (panel at %v), scrollbar lane %v px", panel.MainRight, panel.PanelLeft, panel.Gutter)
				}
				tab.Eval(`document.querySelector("dialog.ts-player").close()`, nil)
			}

			// (6) a boosted navigation opens at the top; Back brings the old position back.
			tab.Eval(`document.getElementById("main").scrollTop = 700`, nil)
			tab.Eval(`document.querySelector('a[data-nav="/me"]').click()`, nil)
			tab.WaitFor(`location.pathname === "/me"`, 5*time.Second)
			tab.WaitFor(`document.getElementById("main").classList.contains("ts-entering") === false && document.querySelector("#main h1, #main h2") !== null`, 5*time.Second)
			var nav struct{ Top float64 }
			tab.Eval(`(async () => { await new Promise(r => setTimeout(r, 300)); return { top: document.getElementById("main").scrollTop }; })()`, &nav)
			if nav.Top != 0 {
				t.Errorf("the new page opened %v px down", nav.Top)
			}
			tab.Eval(`history.back()`, nil)
			tab.WaitFor(`location.pathname === "/"`, 5*time.Second)
			var back struct{ Top float64 }
			tab.Eval(`(async () => { await new Promise(r => setTimeout(r, 500)); return { top: document.getElementById("main").scrollTop }; })()`, &back)
			if back.Top != 700 {
				t.Errorf("Back restored %v px, want 700", back.Top)
			}

			// (8) the settings menu sticks 24 px under #main's top.
			if vp.full {
				tab.Eval(`history.pushState(null, "", location.href)`, nil)
				tab.Eval(`document.querySelector('a[data-nav="/me"]').click()`, nil)
				tab.WaitFor(`location.pathname === "/me"`, 5*time.Second)
				tab.Eval(`htmx.ajax("GET", "/me/settings", { target: "#main", select: "#main", swap: "outerSync" })`, nil)
				tab.WaitFor(`document.querySelector(".ts-prefs__nav") !== null`, 5*time.Second)
				var sticky struct {
					Nav, Main float64
				}
				tab.Eval(`(async () => {
				  await new Promise(r => setTimeout(r, 400));
				  const m = document.getElementById("main");
				  m.scrollTop = 600;
				  await new Promise(r => setTimeout(r, 150));
				  return { nav: document.querySelector(".ts-prefs__nav").getBoundingClientRect().top, main: m.getBoundingClientRect().top };
				})()`, &sticky)
				if sticky.Nav != sticky.Main+24 {
					t.Errorf("settings menu sits at %v, want %v (24 px under #main)", sticky.Nav, sticky.Main+24)
				}
			}
		})
	}
}

// TestDeskScrollerBrowserShellOnly: outside the desktop shell the document is still the scroller and #main has
// no overflow of its own.
func TestDeskScrollerBrowserShellOnly(t *testing.T) {
	b := chromium(t)
	app := newTestApp(t)
	app.owner(t)
	base := themedServer(t, app.signedIn(t))
	tab := b.NewTab(1440, 900, false, true)
	_ = tab.Call("Page.enable", map[string]any{}, nil)
	if err := tab.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": longPage}, nil); err != nil {
		t.Fatal(err)
	}
	tab.Navigate(base + "/")
	tab.WaitFor(`document.getElementById("main") !== null`, 5*time.Second)
	var m struct {
		Overflow string
		Range    float64
	}
	tab.Eval(`({ overflow: getComputedStyle(document.getElementById("main")).overflowY,
	  range: document.scrollingElement.scrollHeight - document.scrollingElement.clientHeight })`, &m)
	if m.Overflow != "visible" || m.Range < 3000 {
		t.Errorf("browser shell: #main overflow-y %q, document range %v", m.Overflow, m.Range)
	}
}
