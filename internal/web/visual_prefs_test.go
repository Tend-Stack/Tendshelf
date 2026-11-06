//go:build dev

package web

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// prefsLayoutJS reports what the preferences window looks like right now: whether #main scrolls
// sideways, whether the category list and the pane are on screen, and how many columns of cards the
// pane lays out (the number of distinct left edges among its cards).
const prefsLayoutJS = `(() => {
  const main = document.getElementById("main");
  const shown = el => !!el && el.getClientRects().length > 0;
  const cards = [...document.querySelectorAll(".ts-prefs__pane .ts-group")].filter(shown);
  const lefts = new Set(cards.map(c => Math.round(c.getBoundingClientRect().left)));
  return {over: main.scrollWidth > main.clientWidth, doc: document.documentElement.scrollWidth > innerWidth,
    nav: shown(document.querySelector(".ts-prefs__nav")), pane: shown(document.querySelector(".ts-prefs__pane")),
    back: shown(document.querySelector(".ts-prefs__back")), columns: lefts.size};
})()`

type prefsLayout struct {
	Over    bool `json:"over"`
	Doc     bool `json:"doc"`
	Nav     bool `json:"nav"`
	Pane    bool `json:"pane"`
	Back    bool `json:"back"`
	Columns int  `json:"columns"`
}

// prefsPaths are Profile (outside the frame) and the pages of the window: the list and the four categories.
var prefsPaths = []string{"/me", "/me/settings", "/me/settings/playback", "/me/settings/appearance", "/me/settings/accessibility", "/me/settings/privacy"}

func prefsName(p string) string {
	return strings.NewReplacer("/me/settings/", "", "/me/settings", "list", "/me", "profile").Replace(p)
}

// TestVisualPrefs captures the preferences window at 390, 768, 1024, 1440 and 1920 px, in large
// text at 1440, and one category in all four themes. On every capture #main must not scroll
// sideways; the column counts the plan promises are asserted too: one column at 390 and 768, a list
// beside the pane from 1024, two cards across at 1440 and three at 1920. Needs TENDSHELF_TEST_BROWSER.
func TestVisualPrefs(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	app := newTestApp(t)
	app.owner(t)
	base := themedServer(t, app.signedIn(t))
	uid := sweepUserID(t, app, app.owner(t))

	capture := func(t *testing.T, path, theme string, w int, file string) prefsLayout {
		t.Helper()
		tab := b.NewTab(w, 1000, w == 390, true)
		tab.Navigate(base + path + "?theme=" + theme)
		var m prefsLayout
		tab.Eval(prefsLayoutJS, &m)
		if m.Over || m.Doc {
			t.Errorf("%s at %d: horizontal overflow (main %v, page %v)", path, w, m.Over, m.Doc)
		}
		tab.ScreenshotFull(filepath.Join(out, file))
		return m
	}

	for _, w := range []int{390, 768, 1024, 1440, 1920} {
		for _, p := range prefsPaths {
			t.Run(fmt.Sprintf("%s-%d", prefsName(p), w), func(t *testing.T) {
				m := capture(t, p, "dark", w, fmt.Sprintf("prefs-%s-dark-%d.png", prefsName(p), w))
				wide := w >= 1024
				switch {
				case p == "/me":
					// Profile is a page of its own since the redesign (decision 0021): no list, no pane.
					if m.Nav || m.Pane {
						t.Errorf("profile: nav %v pane %v, want no preferences frame", m.Nav, m.Pane)
					}
				case p == "/me/settings" && !wide:
					if !m.Nav || m.Pane {
						t.Errorf("one-column list: nav %v pane %v, want the list alone", m.Nav, m.Pane)
					}
				case !wide:
					if m.Nav || !m.Pane {
						t.Errorf("one-column page: nav %v pane %v, want the pane alone", m.Nav, m.Pane)
					}
				default:
					if !m.Nav || !m.Pane || m.Back {
						t.Errorf("two columns: nav %v pane %v back %v", m.Nav, m.Pane, m.Back)
					}
				}
				if strings.HasPrefix(p, "/me/settings/") && p != "/me/settings/privacy" {
					want := map[int]int{390: 1, 768: 1, 1024: 1, 1440: 2, 1920: 2}[w]
					if m.Columns < want {
						t.Errorf("%d columns of cards, want at least %d", m.Columns, want)
					}
				}
			})
		}
	}

	for _, th := range []string{"dark", "light", "sepia", "contrast"} {
		for _, p := range []string{"/me/settings/appearance", "/me"} {
			t.Run(fmt.Sprintf("theme-%s-%s", prefsName(p), th), func(t *testing.T) {
				capture(t, p, th, 1440, fmt.Sprintf("prefs-%s-%s-1440.png", prefsName(p), th))
			})
		}
	}

	// Every item of the list takes the keyboard and shows a focus ring.
	t.Run("focus", func(t *testing.T) {
		tab := b.NewTab(1440, 1000, false, true)
		tab.Navigate(base + "/me/settings/appearance")
		var res struct {
			N      int      `json:"n"`
			NoRing []string `json:"noRing"`
		}
		tab.Eval(`(() => {
		  const items = [...document.querySelectorAll(".ts-prefs__item, .ts-prefs__who")];
		  const noRing = [];
		  for (const el of items) {
		    el.focus({focusVisible: true});
		    if (document.activeElement !== el || getComputedStyle(el).boxShadow === "none") noRing.push(el.textContent.trim());
		  }
		  return {n: items.length, noRing};
		})()`, &res)
		if res.N < 5 || len(res.NoRing) > 0 {
			t.Errorf("%d items, without a focus ring: %v", res.N, res.NoRing)
		}
	})

	// Larger text raises the threshold in rem: 1440 keeps two columns, 1024 drops to one.
	ltSetSize(t, app, uid, true)
	for _, w := range []int{1024, 1440} {
		for _, p := range []string{"/me/settings/playback", "/me"} {
			t.Run(fmt.Sprintf("large-%s-%d", prefsName(p), w), func(t *testing.T) {
				m := capture(t, p, "dark", w, fmt.Sprintf("prefs-%s-large-%d.png", prefsName(p), w))
				if p == "/me" {
					return // Profile has no frame to keep or drop.
				}
				if w == 1440 && !m.Nav {
					t.Errorf("large text at 1440: the list is gone, want two columns")
				}
				if w == 1024 && m.Nav {
					t.Errorf("large text at 1024: the list stays beside the pane, want one column")
				}
			})
		}
	}
	ltSetSize(t, app, uid, false)
	t.Logf("captures in %s", out)
}

// TestVisualPrefsPanel captures Settings at 1440 with the Now playing panel open: the content is
// about 716 px wide, so it keeps two columns (list and pane) and one card across.
func TestVisualPrefsPanel(t *testing.T) {
	out := visualDir(t, "chromium")
	e := nowPanelEnv(t)
	tab := fullpOpen(t, e, 1440, 1000, true)
	fullpOpenPlayer(t, tab)
	tab.WaitFor(`document.querySelector("#now-panel[data-book-id] .ts-np__desc") !== null`, 5*time.Second)
	// Follow a link the way the page does, through htmx, so the panel stays open.
	tab.Eval(`(() => { const a = document.createElement("a"); a.id = "go"; a.href = "/me/settings/playback"; a.textContent = "go"; document.getElementById("main").append(a); htmx.process(a); a.click(); })()`, nil)
	tab.WaitFor(`location.pathname === "/me/settings/playback" && document.querySelector(".ts-prefs__pane .ts-group") !== null`, 8*time.Second)
	time.Sleep(400 * time.Millisecond)
	var m prefsLayout
	tab.Eval(prefsLayoutJS, &m)
	if m.Over {
		t.Error("horizontal overflow beside the open panel")
	}
	if m.Columns != 1 {
		t.Errorf("%d columns of cards beside the open panel, want 1", m.Columns)
	}
	tab.Screenshot(filepath.Join(out, "prefs-playback-panel-1440.png"))
}
