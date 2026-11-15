package web

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// fitTwins lists the key and words of each twin in the three-dot menu of body, in order.
func fitTwins(body string) [][2]string {
	var out [][2]string
	re := regexp.MustCompile(`(?s)data-ts-fit-for="([a-z]+)" hidden>.*?<span data-ts-fit-label>([^<]*)</span>`)
	for _, m := range re.FindAllStringSubmatch(body, -1) {
		out = append(out, [2]string{m[1], m[2]})
	}
	return out
}

// fitItems lists the keys of the row's collapsible buttons (a key shared by the Download pair counts once).
func fitItems(body string) []string {
	var out []string
	seen := map[string]bool{}
	for _, m := range regexp.MustCompile(`data-ts-fit-item="([a-z]+)"`).FindAllStringSubmatch(body, -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	return out
}

func fitKeys(tw [][2]string) string {
	var keys []string
	for _, t := range tw {
		keys = append(keys, t[0]+"="+t[1])
	}
	return strings.Join(keys, "|")
}

// TestBookPlayFitTwins: every collapsible button of the play region has a hidden twin in the three-dot menu with the
// same words, in the row's order; the play button is the only resume button; the text book's Read never collapses;
// the region carries the module for js/fit-row.js.
func TestBookPlayFitTwins(t *testing.T) {
	l := fmt2Seed(t)
	owner := l.owner
	get := func(path string) string {
		rec := l.a.do(http.MethodGet, path, nil, proxyDefault, owner)
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s = %d", path, rec.Code)
		}
		return rec.Body.String()
	}
	body := get("/hx/books/" + l.book.ID + "/play")
	if !strings.Contains(body, "data-ts-fit") || !strings.Contains(body, `data-ts-module="/static/`) {
		t.Errorf("the region does not carry the fit module")
	}
	tw := fitTwins(body)
	items := fitItems(body)
	if len(tw) != len(items) || len(items) == 0 {
		t.Fatalf("twins %v for row items %v", fitKeys(tw), items)
	}
	for i, k := range items {
		if tw[i][0] != k {
			t.Errorf("twin %d is %q, the row's item is %q (same order)", i, tw[i][0], k)
		}
	}
	got := fitKeys(tw)
	for _, want := range []string{"shelf=Add to shelf", "mark=Mark as finished"} {
		if !strings.Contains(got, want) {
			t.Errorf("twins %q lack %q", got, want)
		}
	}
	if n := strings.Count(body, "data-ts-play="); n != 1 {
		t.Errorf("%d play buttons, want exactly one", n)
	}
	// The text book: Read is the primary one and nothing else plays, so neither collapses.
	body = get("/hx/books/" + l.tbook.ID + "/play")
	for _, k := range fitItems(body) {
		if k == "read" || k == "play" {
			t.Errorf("the primary button %q of a text book can collapse", k)
		}
	}
	if !strings.Contains(body, "data-ts-read") {
		t.Errorf("the text book lost its Read button")
	}
}
