//go:build dev

package web

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestDeskBarShots captures Home under the desktop shell, with a book "playing" in the bar, in four themes at
// 390, 768, 1024 and 1440 px, for the side-by-side look against docs/design/boards/DesktopShell.html.
// It fails on horizontal overflow, a bar that is not 44 px, a button under 44 px or a bar that covers content.
// Captures go to .local/tests/desktop-bar/ (ignored by Git). Needs TENDSHELF_TEST_BROWSER.
func TestDeskBarShots(t *testing.T) {
	b := chromium(t)
	app := newTestApp(t)
	app.owner(t)
	base := themedServer(t, app.signedIn(t))
	out := filepath.Join("..", "..", ".local", "tests", "desktop-bar")
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, theme := range []string{"dark", "light", "sepia", "contrast"} {
		for _, width := range []int{390, 768, 1024, 1440} {
			t.Run(fmt.Sprintf("%s-%d", theme, width), func(t *testing.T) {
				tab := b.NewTab(width, 800, false, false)
				_ = tab.Call("Page.enable", map[string]any{}, nil)
				if err := tab.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": fakeChannel}, nil); err != nil {
					t.Fatal(err)
				}
				tab.Navigate(base + "/?shell=desktop")
				tab.Navigate(base + "/?theme=" + theme)
				tab.WaitFor(`document.querySelector("[data-wbar-more]") !== null`, 5*time.Second)
				// A book "playing": the mini-player's own fields, which the bar copies.
				tab.Eval(`(async () => {
				  const player = document.getElementById("player");
				  player.querySelector("[data-ts-title]").textContent = "The Cartographer’s Silence";
				  player.querySelector("[data-ts-meta]").textContent = "Chapter 6 · 22 min left";
				  player.querySelector("[data-ts-open]").setAttribute("aria-label", "Open player: The Cartographer’s Silence, chapter 6, 22 min left");
				  player.querySelector("[data-ts-mini-progress]").style.setProperty("--ts-progress", "0.34");
				  player.hidden = false;
				  await new Promise(r => setTimeout(r, 300));
				})()`, nil)
				var m struct {
					Over, Now                bool
					BarHeight                float64
					Small                    []string
					Theme, Title, NowTitle   string
					LineWidth                float64
					FirstContentTop, SideTop float64
				}
				tab.Eval(`(() => {
				  const bar = document.querySelector("[data-wbar]");
				  const small = [...document.querySelectorAll(".ts-wbar__btn, .ts-wbar__brand, .ts-wbar__now")].filter(e => e.offsetParent).filter(e => { const r = e.getBoundingClientRect(); return r.height < 43.5 && !e.matches(".ts-wbar__now") || r.width < 43.5 && e.matches(".ts-wbar__btn"); }).map(e => e.getAttribute("aria-label") || e.className);
				  const h1 = document.querySelector("#main h1, #main h2");
				  const side = document.querySelector(".ts-nav-side");
				  return {
				    over: document.documentElement.scrollWidth > innerWidth,
				    now: !document.querySelector("[data-wbar-now]").hidden,
				    barHeight: bar.getBoundingClientRect().height,
				    small,
				    theme: document.documentElement.dataset.theme || "",
				    title: document.querySelector("[data-wbar-title]").textContent,
				    nowTitle: document.querySelector("[data-wbar-nowtitle]").textContent,
				    lineWidth: document.querySelector(".ts-wbar__fill").getBoundingClientRect().width,
				    firstContentTop: h1 ? h1.getBoundingClientRect().top : 99,
				    sideTop: side && getComputedStyle(side).display !== "none" ? side.getBoundingClientRect().top : 44
				  };
				})()`, &m)
				if m.Over {
					t.Error("horizontal overflow")
				}
				if m.BarHeight != 44 {
					t.Errorf("bar is %v px high", m.BarHeight)
				}
				if len(m.Small) > 0 {
					t.Errorf("targets under 44 px: %v", m.Small)
				}
				if m.Theme != theme {
					t.Errorf("theme %q", m.Theme)
				}
				if !m.Now || m.NowTitle != "The Cartographer’s Silence" || m.LineWidth <= 0 {
					t.Errorf("now playing in the bar: shown %v, %q, line %v px", m.Now, m.NowTitle, m.LineWidth)
				}
				if m.FirstContentTop < 44 || m.SideTop < 44 {
					t.Errorf("content under the bar: content top %v, sidebar top %v", m.FirstContentTop, m.SideTop)
				}
				tab.Screenshot(filepath.Join(out, fmt.Sprintf("home-%s-%d.png", theme, width)))
			})
		}
	}
}
