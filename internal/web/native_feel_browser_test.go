//go:build dev

package web

import (
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// Native feel (decision 0011) in a real browser: fixed edge elements clear the notch, status bar
// and home bar; selection is scoped; the page does not bounce; the theme sets color-scheme.
// Run by: TENDSHELF_TEST_BROWSER=<chromium> go test -tags dev -run NativeFeel ./internal/web/

// nativeInsets is Emulation.setSafeAreaInsetsOverride's parameter (Chromium 156, experimental).
type nativeInsets struct{ Top, Bottom, Left, Right int }

// nativeTab opens the Library at w x h with the safe-area insets in force and the mini player
// shown. The override is set before the navigation so the first layout already sees it.
func nativeTab(t *testing.T, e *playerEnv, w, h int, in nativeInsets) *cdp.Tab {
	t.Helper()
	b := chromium(t)
	e.store(t, 100_000, "nativedevice01", "Other device", 0, false)
	tab := b.NewTab(w, h, w < 768, false)
	nativeInsetsApply(t, tab, in)
	tab.Navigate(e.URL + "/libraries")
	tab.WaitFor(`window.tsPlayer && window.tsPlayer.state().bookId !== "" && !document.getElementById("player").hidden`, 10*time.Second)
	return tab
}

func nativeInsetsApply(t *testing.T, tab *cdp.Tab, in nativeInsets) {
	t.Helper()
	params := map[string]any{"insets": map[string]int{
		"top": in.Top, "topMax": in.Top, "bottom": in.Bottom, "bottomMax": in.Bottom,
		"left": in.Left, "leftMax": in.Left, "right": in.Right, "rightMax": in.Right,
	}}
	if err := tab.Call("Emulation.setSafeAreaInsetsOverride", params, nil); err != nil {
		t.Skipf("no safe-area override in this Chromium (%v); manual check pending", err)
	}
}

type nativeRect struct{ Left, Right, Top, Bottom float64 }

func nativeRectOf(tab *cdp.Tab, sel string) nativeRect {
	var r nativeRect
	tab.Eval(`(() => { const r = document.querySelector(`+cdp.Quote(sel)+`).getBoundingClientRect(); return {Left: r.left, Right: r.right, Top: r.top, Bottom: r.bottom}; })()`, &r)
	return r
}

func nativeStyle(tab *cdp.Tab, sel, prop string) string {
	var v string
	tab.Eval(`getComputedStyle(document.querySelector(`+cdp.Quote(sel)+`)).`+prop, &v)
	return v
}

func TestNativeFeelPortraitInsets(t *testing.T) {
	e := playerApp(t)
	tab := nativeTab(t, e, 390, 844, nativeInsets{Top: 47, Bottom: 34})
	if got := nativeStyle(tab, "#main", "paddingTop"); got != "71px" {
		t.Errorf("#main paddingTop %s, want 71px (24 + 47)", got)
	}
	tabbar := nativeRectOf(tab, "#nav-tabbar")
	var inner float64
	tab.Eval(`innerHeight`, &inner)
	if tabbar.Bottom != inner {
		t.Errorf("tab bar bottom %v, want the window bottom %v", tabbar.Bottom, inner)
	}
	if got := nativeStyle(tab, "#nav-tabbar", "paddingBottom"); got != "44px" { // 10 + 34
		t.Errorf("tab bar paddingBottom %s, want 44px", got)
	}
	if slot := nativeRectOf(tab, ".ts-player-slot"); slot.Bottom > tabbar.Top {
		t.Errorf("mini player bottom %v overlaps the tab bar top %v", slot.Bottom, tabbar.Top)
	}

	// Selection is scoped: chrome is rigid, fields and content are not.
	for sel, want := range map[string]string{".ts-tabbar__item": "none", ".ts-btn": "none", "#nav-tabbar": "none"} {
		if got := nativeStyle(tab, sel, "userSelect"); got != want {
			t.Errorf("%s userSelect %q, want %q", sel, got, want)
		}
	}
	if got := nativeStyle(tab, "html", "overscrollBehaviorY"); got != "none" {
		t.Errorf("html overscrollBehaviorY %q, want none", got)
	}

	// The full player: its body clears the status bar.
	fullpOpenPlayer(t, tab)
	if got := nativeStyle(tab, ".ts-player__body", "paddingTop"); got != "55px" && got != "59px" { // 8 or 12, plus 47
		t.Errorf("player body paddingTop %s, want 55px or 59px", got)
	}
	var locked string
	tab.Eval(`getComputedStyle(document.documentElement).overflow`, &locked)
	if locked != "hidden" {
		t.Errorf("html overflow %q while the modal player is open, want hidden", locked)
	}
}

func TestNativeFeelSheetAndFrameInsets(t *testing.T) {
	e := playerApp(t)
	in := nativeInsets{Top: 47, Bottom: 34}
	tab := nativeTab(t, e, 390, 844, in)
	tab.Navigate(e.URL + "/books/" + e.book.ID + "/bookmarks?sheet=export")
	tab.WaitFor(`document.querySelector(".ts-sheet") !== null`, 5*time.Second)
	if got := nativeStyle(tab, ".ts-sheet", "paddingBottom"); got != "58px" {
		t.Errorf(".ts-sheet paddingBottom %s, want 58px (24 + 34)", got)
	}

	// A sign-in page has no shell: .ts-frame__main carries the insets itself.
	srv := httptest.NewServer(e.a.h)
	t.Cleanup(srv.Close)
	nativeInsetsApply(t, tab, in)
	tab.Navigate(srv.URL + "/login")
	tab.WaitFor(`document.querySelector(".ts-frame__main") !== null`, 5*time.Second)
	if got := nativeStyle(tab, ".ts-frame__main", "paddingTop"); got != "67px" {
		t.Errorf(".ts-frame__main paddingTop %s, want 67px (20 + 47)", got)
	}
}

func TestNativeFeelLandscapeInsets(t *testing.T) {
	e := playerApp(t)
	tab := nativeTab(t, e, 844, 390, nativeInsets{Bottom: 21, Left: 47, Right: 47})
	rail := nativeRectOf(tab, "#nav-rail")
	if rail.Left < 47 {
		t.Errorf("rail left %v, want at least 47", rail.Left)
	}
	if main := nativeRectOf(tab, "#main"); main.Right > 797 {
		t.Errorf("#main right %v, want at most 797 (844 - 47)", main.Right)
	}
	slot := nativeRectOf(tab, ".ts-player-slot")
	if slot.Left < rail.Right || slot.Right > 797 {
		t.Errorf("mini player spans %v..%v, want within %v..797", slot.Left, slot.Right, rail.Right)
	}
	fullpOpenPlayer(t, tab)
	for _, side := range []string{"paddingLeft", "paddingRight"} {
		if got := nativeStyle(tab, ".ts-player__body", side); got != "47px" {
			t.Errorf("player body %s %s, want 47px", side, got)
		}
	}
}

func TestNativeFeelThemesAndSelection(t *testing.T) {
	e := playerApp(t)
	base := themedServer(t, e.a.signedIn(t))
	b := chromium(t)
	scheme := map[string]string{"dark": "dark", "light": "light", "sepia": "light", "contrast": "dark"}
	for _, theme := range themes {
		tab := b.NewTab(390, 844, true, false)
		tab.Navigate(base + "/libraries?theme=" + theme)
		tab.WaitFor(`document.documentElement.dataset.theme !== undefined`, 5*time.Second)
		if got := nativeStyle(tab, "html", "colorScheme"); got != scheme[theme] {
			t.Errorf("theme %s: colorScheme %q, want %q", theme, got, scheme[theme])
		}
	}
	// Readable content stays selectable: the book page's description is not under a user-select: none rule.
	tab := b.NewTab(390, 844, true, false)
	tab.Navigate(base + "/books/" + e.book.ID)
	tab.WaitFor(`document.querySelector("#main h1") !== null`, 5*time.Second)
	var bad []string
	tab.Eval(`[...document.querySelectorAll("#main p, #main h1, #main input, #main textarea")].filter(el => getComputedStyle(el).userSelect === "none").map(el => el.tagName + "." + el.className)`, &bad)
	for _, b := range bad {
		t.Errorf("readable element %s is not selectable", b)
	}
}

// TestNativeFeelNavTransitionLayer: the visible navigation (tab bar, rail or sidebar) has its own view
// transition layer, so the page never shows through it mid-swap (operator, 2026-11-03: the tab bar
// went see-through while navigating on an iPhone). Exactly one named nav shows at each width; two
// at once would abort every transition. A real swap must also run with no aborted transition.
func TestNativeFeelNavTransitionLayer(t *testing.T) {
	e := playerApp(t)
	for _, w := range []int{390, 1024, 1440} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			tab := nativeTab(t, e, w, 900, nativeInsets{})
			var got struct {
				Names   []string
				Aborted string
			}
			tab.Eval(`(async () => {
			  const names = [...document.querySelectorAll(".ts-nav-tabbar, .ts-nav-rail, .ts-nav-side")]
			    .filter((n) => n.offsetParent !== null || getComputedStyle(n).position === "fixed" && getComputedStyle(n).display !== "none")
			    .map((n) => getComputedStyle(n).viewTransitionName);
			  let aborted = "";
			  if (document.startViewTransition) {
			    const vt = document.startViewTransition(() => {});
			    try { await vt.finished; } catch (err) { aborted = String(err); }
			  }
			  return { Names: names, Aborted: aborted };
			})()`, &got)
			if len(got.Names) != 1 || !strings.HasPrefix(got.Names[0], "ts-") {
				t.Errorf("%d: visible nav transition names %v, want exactly one ts-* name", w, got.Names)
			}
			if got.Aborted != "" {
				t.Errorf("%d: a view transition was aborted: %s", w, got.Aborted)
			}
		})
	}
}
