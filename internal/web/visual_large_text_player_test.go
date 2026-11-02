//go:build dev

package web

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// TestVisualLargeTextPlayer captures the bar (the mini-player below 960 px, the flat tablet bar from there)
// and the full player in large text, runs the three probes on each, and checks that the settings bar is
// two rows: Speed and Sleep side by side, Chapters across the whole second row.
func TestVisualLargeTextPlayer(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	e := playerApp(t)
	e.store(t, 100_000, "ltdevice0001", "Other device", 0, false)
	ltSetSize(t, e.a, e.ownerID, true)
	for _, sh := range ltShots() {
		theme, w := sh.Theme, sh.Width
		t.Run(fmt.Sprintf("%s-%d", theme, w), func(t *testing.T) {
			streamExec(t, e.a, `INSERT OR REPLACE INTO user_settings(user_id, key, value_json, updated_at) VALUES (?, 'ui.theme', ?, 1)`, e.ownerID, `"`+theme+`"`)
			tab := b.NewTab(w, 900, w == 390, true)
			tab.Navigate(e.URL + "/libraries")
			tab.WaitFor(`window.tsPlayer && window.tsPlayer.state().bookId !== "" && !document.getElementById("player").hidden`, 10*time.Second)
			time.Sleep(600 * time.Millisecond)
			check := func(name string) {
				var m ltProbe
				ltProbeAt(tab, w, &m)
				if m.Over {
					t.Errorf("%s: horizontal overflow, past the window: %v", name, m.Wide)
				}
				if len(m.Small) > 0 {
					t.Errorf("%s: targets under 44 px: %v", name, m.Small)
				}
				if len(m.Clip) > 0 {
					t.Errorf("%s: time left cut: %v", name, m.Clip)
				}
				if m.Attr != "large" {
					t.Errorf("%s: data-text-size = %q", name, m.Attr)
				}
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("largetext-%s-%s-%d.png", name, theme, w)))
			}
			check("bar")
			var seen int
			tab.Eval(`document.querySelectorAll("[data-ts-bar-left]:not(:empty), [data-ts-meta]").length`, &seen)
			if seen == 0 {
				t.Errorf("no time left on the bar")
			}
			fullpOpenPlayer(t, tab)
			check("player")
			var rows struct{ A, B, C float64 }
			tab.Eval(`(() => { const t = [...document.querySelectorAll("#player-full .ts-player-bar__item")].filter(i => i.getBoundingClientRect().width > 0)
			  .map(i => Math.round(i.getBoundingClientRect().top)); return {A: t[0], B: t[1], C: t[2]}; })()`, &rows)
			if rows.A != rows.B || rows.C <= rows.A {
				t.Errorf("settings bar rows = %+v, want Speed and Sleep on one row and Chapters below", rows)
			}
		})
	}
}
