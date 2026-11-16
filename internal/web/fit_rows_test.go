package web

import (
	"net/http"
	"strings"
	"testing"
)

// TestEpisodeActionsFitTwins: every collapsible control of the episode page's action row (the played mark, the star,
// Download, Save the file) has a hidden twin in the episode menu with the same words, in the row's order; the row
// carries the module for js/fit-row.js; the menu is marked as a fit menu and answers a queue choice without replacing
// itself; and the Play button is the only one that plays.
func TestEpisodeActionsFitTwins(t *testing.T) {
	w := newPodWorld(t)
	rec := w.get("/podcasts/episodes/"+w.eps[0], w.ben)
	if rec.Code != http.StatusOK {
		t.Fatalf("episode page = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `data-ts-module="/static/`) || !strings.Contains(body, "data-ts-fit-menu") {
		t.Errorf("the row does not carry the fit module and a fit menu")
	}
	tw := fitTwins(body)
	items := fitItems(body)
	if len(tw) != len(items) || len(items) != 4 {
		t.Fatalf("twins %v for row items %v, want four", fitKeys(tw), items)
	}
	for i, k := range items {
		if tw[i][0] != k {
			t.Errorf("twin %d is %q, the row's item is %q (same order)", i, tw[i][0], k)
		}
	}
	got := fitKeys(tw)
	for _, want := range []string{"played=Mark as played", "star=Star", "download=Download", "save="} {
		if !strings.Contains(got, want) {
			t.Errorf("twins %q lack %q", got, want)
		}
	}
	if strings.Contains(body, `hx-swap="outerHTML"`) && strings.Contains(body, "ep-menu-") {
		// The row menu's forms must not swap the menu out: the twins would be lost.
		for _, part := range strings.Split(body, "<form") {
			if strings.Contains(part, `hx-post="/queue"`) && !strings.Contains(part, `hx-swap="none"`) {
				t.Errorf("a queue form of the episode menu replaces the menu: %.120s", part)
			}
		}
	}
	if n := strings.Count(body, "data-ts-play="); n != 1 {
		t.Errorf("%d play buttons, want exactly one", n)
	}
}

// TestEpisodeRowMenuStillSwaps: an episode row in a list (no twins) keeps answering with the closed menu.
func TestEpisodeRowMenuStillSwaps(t *testing.T) {
	w := newPodWorld(t)
	rec := w.get("/podcasts/shows/"+w.show, w.ben)
	if rec.Code != http.StatusOK {
		t.Fatalf("show page = %d", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, `hx-swap="outerHTML"`) || strings.Contains(body, `data-ts-fit-for="played"`) {
		t.Errorf("a list row's menu lost its swap or gained twins")
	}
}
