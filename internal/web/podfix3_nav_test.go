//go:build dev

package web

import (
	"context"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/podcast"
)

// TestPodFix3RadioKeepsPodcastsCurrent boosts from /podcasts to /radio through the Radio tab (only #main swaps) and
// reads aria-current on every Podcasts item of the navigation: the rail and the phone tab bar must still mark it.
func TestPodFix3RadioKeepsPodcastsCurrent(t *testing.T) {
	b := chromium(t)
	w, _, base := radioBrowserWorld(t, "Harbour FM")
	s := podcast.Defaults()
	s.Enabled, s.RadioEnabled = true, true
	if err := podcast.SaveSettings(context.Background(), w.a.st, "", s, time.Now()); err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{390, 1440} {
		tab := b.NewTab(width, 900, width < 768, true)
		tab.Navigate(base + "/podcasts")
		tab.WaitFor(`document.querySelector('.ts-tabs__item[href="/radio"]') !== null`, 10*time.Second)
		tab.Eval(`document.querySelector('.ts-tabs__item[href="/radio"]').click()`, nil)
		tab.WaitFor(`location.pathname === "/radio" && document.querySelector(".ts-radio") !== null`, 10*time.Second)
		var cur []string
		tab.Eval(`[...document.querySelectorAll('nav [data-nav="/podcasts"]')].filter((e) => e.offsetWidth > 0).map((e) => e.getAttribute("aria-current") || "")`, &cur)
		if len(cur) == 0 {
			t.Fatalf("%d px: no visible Podcasts item", width)
		}
		for _, c := range cur {
			if c != "page" {
				t.Errorf("%d px: Podcasts item aria-current = %q after boosting to /radio", width, c)
			}
		}
	}
}
