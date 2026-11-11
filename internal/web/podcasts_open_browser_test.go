//go:build dev

package web

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// TestPodcastOpenRowBusyBrowser: while a show is being opened, its row says "Opening…" in place of the line
// under the title, and goes back to that line when the request is over. The request is held open by a fetch
// that never answers, so the state can be looked at.
func TestPodcastOpenRowBusyBrowser(t *testing.T) {
	b := chromium(t)
	w := newPodWorld(t)
	base, _ := podPlayServer(t, w)
	for _, width := range []int{390, 1440} {
		tab := b.NewTab(width, 900, width < 768, false)
		tab.Navigate(base + "/podcasts/search?q=found")
		tab.WaitFor(`document.querySelector(".ts-podhit__link") !== null && window.htmx !== undefined`, 10*time.Second)
		var before struct {
			Busy string  `json:"busy"`
			H    float64 `json:"h"`
		}
		const probe = `(() => {
  const row = document.querySelector(".ts-podhit");
  return { busy: getComputedStyle(row.querySelector(".ts-podhit__busy")).display, h: row.getBoundingClientRect().height };
})()`
		tab.Eval(probe, &before)
		if before.Busy != "none" {
			t.Errorf("%d px: the row says it is opening before it is tapped (display %q)", width, before.Busy)
		}
		tab.Eval(`(() => { window.fetch = () => new Promise(() => {}); document.querySelector(".ts-podhit__link").click(); return true; })()`, nil)
		tab.WaitFor(`document.querySelector(".ts-podhit__open").classList.contains("htmx-request")`, 5*time.Second)
		var during struct {
			Busy string  `json:"busy"`
			H    float64 `json:"h"`
		}
		tab.Eval(probe, &during)
		if during.Busy == "none" {
			t.Errorf("%d px: the row does not say it is opening while the request runs", width)
		}
		if during.H > before.H+24 {
			t.Errorf("%d px: the row grows from %.0f to %.0f px while it opens", width, before.H, during.H)
		}
		tab.Screenshot(filepath.Join(visualDir(t, "chromium"), fmt.Sprintf("podcast-open-busy-%d.png", width)))
		tab.Close()
	}
}
