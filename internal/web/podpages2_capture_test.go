//go:build dev

package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/podcast"
)

// radioServe serves a radio world to the browser as Ben.
func radioServe(t *testing.T, w *radioWorld) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		r.AddCookie(w.ben)
		w.a.h.ServeHTTP(rw, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestPodPages2Captures records the Radio, search, show and episode-panel pages for a look (TENDSHELF_VISUAL_OUT picks the
// folder) and checks what a capture cannot say: no sideways overflow, touch targets of 44 px, and whether the longer rail
// label "Podcasts & radio" fits on one line at 1280 and 1440.
func TestPodPages2Captures(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	noOverflow := func(t *testing.T, tab interface{ Eval(string, any) }, what string) {
		t.Helper()
		var over bool
		tab.Eval(`document.documentElement.scrollWidth > innerWidth + 1`, &over)
		if over {
			t.Errorf("%s: horizontal overflow", what)
		}
	}

	t.Run("radio", func(t *testing.T) {
		w, id, base := radioBrowserWorld(t, "Harbour FM")
		keepTestLogo(t, w, id)
		// podcasts and radio both on, as the label check needs
		s := podcast.Defaults()
		s.Enabled, s.RadioEnabled = true, true
		if err := podcast.SaveSettings(context.Background(), w.a.st, "", s, time.Now()); err != nil {
			t.Fatal(err)
		}
		for _, width := range []int{390, 1440} {
			tab := b.NewTab(width, 900, width < 768, true)
			tab.Navigate(base + "/radio")
			tab.WaitFor(`document.querySelector(".ts-stncard") !== null`, 10*time.Second)
			noOverflow(t, tab, fmt.Sprint("radio ", width))
			tab.Screenshot(filepath.Join(out, fmt.Sprintf("radio-stations-%d.png", width)))
			tab.Navigate(base + "/radio/search?q=cool")
			tab.WaitFor(`document.querySelector(".ts-podhit") !== null`, 10*time.Second)
			tab.WaitFor(`[...document.querySelectorAll(".ts-podhit img")].every((i) => i.complete)`, 10*time.Second)
			noOverflow(t, tab, fmt.Sprint("radio search ", width))
			tab.Screenshot(filepath.Join(out, fmt.Sprintf("radio-search-%d.png", width)))
		}
		// The longer rail label: set it in the rail and measure whether it is one line and whole.
		for _, width := range []int{1280, 1440} {
			tab := b.NewTab(width, 900, false, true)
			tab.Navigate(base + "/radio")
			tab.WaitFor(`document.querySelector('[data-nav="/podcasts"]') !== null`, 10*time.Second)
			var m struct {
				Visible  bool    `json:"visible"`
				Lines    float64 `json:"lines"`
				Cut      bool    `json:"cut"`
				LabelW   float64 `json:"labelW"`
				RailW    float64 `json:"railW"`
				Original string  `json:"original"`
			}
			tab.Eval(`(() => {
				const a = [...document.querySelectorAll('[data-nav="/podcasts"]')].find((e) => e.offsetWidth > 0);
				if (!a) return {visible: false};
				const walker = document.createTreeWalker(a, NodeFilter.SHOW_TEXT);
				let node = null, n;
				while ((n = walker.nextNode())) { if (n.textContent.trim() === "Podcasts") node = n; }
				const label = node.parentElement;
				const original = node.textContent;
				node.textContent = "Podcasts & radio";
				const lh = parseFloat(getComputedStyle(label).lineHeight) || 20;
				const r = label.getBoundingClientRect();
				const out = {visible: true, lines: r.height / lh, cut: label.scrollWidth > label.clientWidth, labelW: label.scrollWidth, railW: a.getBoundingClientRect().width, original};
				node.textContent = original;
				return out;
			})()`, &m)
			t.Logf("rail %d: %+v", width, m)
			if !m.Visible {
				t.Fatalf("rail %d: no Podcasts item", width)
			}
			tab.Screenshot(filepath.Join(out, fmt.Sprintf("rail-%d.png", width)))
		}
	})

	t.Run("radio empty", func(t *testing.T) {
		w := newRadioWorld(t)
		srvBase := radioServe(t, w)
		for _, width := range []int{390, 1440} {
			tab := b.NewTab(width, 900, width < 768, true)
			tab.Navigate(srvBase + "/radio")
			tab.WaitFor(`document.querySelector("main, #main") !== null`, 10*time.Second)
			noOverflow(t, tab, fmt.Sprint("radio empty ", width))
			tab.Screenshot(filepath.Join(out, fmt.Sprintf("radio-empty-%d.png", width)))
		}
	})

	t.Run("podcasts", func(t *testing.T) {
		w := podWavWorld(t)
		base, _ := podPlayServer(t, w)
		tab := b.NewTab(390, 844, true, true)
		tab.Navigate(base + "/podcasts/search?q=found")
		tab.WaitFor(`document.querySelector(".ts-podhit") !== null`, 10*time.Second)
		tab.WaitFor(`[...document.querySelectorAll(".ts-podhit img")].length > 0 && [...document.querySelectorAll(".ts-podhit img")].every((i) => i.complete && i.naturalWidth > 0)`, 10*time.Second)
		noOverflow(t, tab, "podcast search 390")
		tab.Screenshot(filepath.Join(out, "podcast-search-390.png"))
		for _, width := range []int{768, 1024} {
			tab := b.NewTab(width, 900, false, true)
			tab.Navigate(base + "/podcasts/shows/" + w.show)
			tab.WaitFor(`document.querySelector(".ts-podshow__head") !== null`, 10*time.Second)
			noOverflow(t, tab, fmt.Sprint("show ", width))
			var small []string
			tab.Eval(`[...document.querySelectorAll(".ts-podshow__actions a, .ts-podshow__actions button, .ts-eprow a, .ts-eprow button")].filter((e) => e.offsetWidth > 0 && (e.offsetWidth < 44 || e.offsetHeight < 44)).map((e) => e.className + " " + e.offsetWidth + "x" + e.offsetHeight)`, &small)
			if len(small) > 0 {
				t.Errorf("show %d: small targets %v", width, small)
			}
			tab.Screenshot(filepath.Join(out, fmt.Sprintf("show-%d.png", width)))
		}
		tab = b.NewTab(1440, 900, false, true)
		ep := w.eps[0]
		tab.Navigate(base + "/podcasts/shows/" + w.show)
		tab.WaitFor(`window.tsPlayer !== undefined`, 10*time.Second)
		playerClickSel(t, tab, `[data-ts-play="`+ep+`"]`)
		tab.WaitFor(`window.tsPlayer.state().playing`, 15*time.Second)
		fullpOpenPlayer(t, tab)
		tab.WaitFor(`document.querySelector("#now-panel.ts-np--episode") !== null`, 10*time.Second)
		time.Sleep(800 * time.Millisecond)
		noOverflow(t, tab, "episode panel 1440")
		tab.Screenshot(filepath.Join(out, "episode-panel-1440.png"))
		tab.Eval(`window.tsPlayer.pause()`, nil)
	})
}
