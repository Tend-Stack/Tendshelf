package web

import (
	"net/http"
	"regexp"
	"strings"
	"testing"
)

// TestEyesFreeMarkup: the player's menu offers Eyes-free with the addresses of its hashed script and
// stylesheet, the dialog sits beside the full player, and every word and hook the script needs is there.
func TestEyesFreeMarkup(t *testing.T) {
	rec, body := getPage(t, "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d", rec.Code)
	}
	item := regexp.MustCompile(`<button type="button" class="ts-player__menuitem" data-ts-eyes-free-open data-ts-src="(/static/js/eyes-free\.[0-9a-f]+\.js)" data-ts-css="(/static/css/pages-eyes-free\.[0-9a-f]+\.css)">Eyes-free</button>`)
	if !item.MatchString(body) {
		t.Fatalf("the ⋯ menu has no Eyes-free item with hashed addresses")
	}
	menu := strings.Index(body, "data-ts-eyes-free-open")
	if finish := strings.Index(body, "Mark as finished"); finish < 0 || menu < finish {
		t.Error("the Eyes-free item does not follow Mark as finished")
	}
	start := strings.Index(body, `<dialog id="eyes-free"`)
	if start < 0 {
		t.Fatal("the layout has no Eyes-free dialog")
	}
	if strings.Index(body, `<dialog id="player-full"`) > start {
		t.Error("Eyes-free does not follow the full player")
	}
	dlg := body[start : start+strings.Index(body[start:], "</dialog>")]
	for _, want := range []string{
		`class="ts-eyesfree"`, `aria-labelledby="eyes-free-title"`, ` hx-preserve`, `data-ts-eyes-free`, `id="eyes-free-title"`,
		`data-ts-ef-close`, `Back to the player`, `data-ts-ef-title`, `data-ts-ef-chapter`, `data-ts-ef-back`, `data-ts-ef-forward`,
		`data-ts-ef-toggle`, `ts-eyesfree__zone--main`, `data-ts-ef-bookmark`, `>Bookmark<`, `data-ts-ef-sleep`, `data-ts-full-toasts`,
		`data-ts-ef-status`, `Big buttons, no text to follow. The screen dims after 10 seconds; tap anywhere to wake it.`,
		`data-ts-msg-chapter-line="Chapter {n} · {m} min left in chapter"`, `data-ts-msg-skip-back="Back {n} s"`,
		`data-ts-msg-skip-forward="Forward {n} s"`, `data-ts-msg-skip-back-label="Back {n} seconds"`, `data-ts-msg-sleep-chapter="End of chapter"`,
		`data-ts-msg-sleep-minutes="{n} min"`, `data-ts-msg-sleep-off="Turn off"`,
	} {
		if !strings.Contains(dlg, want) {
			t.Errorf("the Eyes-free dialog lacks %s", want)
		}
	}
	if strings.Contains(dlg, "style=") || strings.Contains(dlg, "hx-on") || strings.Contains(dlg, "<script") {
		t.Error("the Eyes-free dialog has an inline style or script")
	}
}

// TestEyesFreeLinkMode: a share-link page has no account to bookmark with, so the item is hidden and
// the dialog is not rendered at all.
func TestEyesFreeLinkMode(t *testing.T) {
	e := linkNewEnv(t)
	_, tok := e.make(e.book.ID, 7, "")
	rec := e.get("/s/" + tok)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET: %d", rec.Code)
	}
	body := rec.Body.String()
	if strings.Contains(body, `id="eyes-free"`) {
		t.Error("the link page renders the Eyes-free dialog")
	}
	i := strings.Index(body, "data-ts-eyes-free-open")
	if i < 0 {
		return // not rendered at all is fine too
	}
	tag := body[strings.LastIndex(body[:i], "<"):]
	tag = tag[:strings.Index(tag, ">")]
	if !strings.Contains(tag, " hidden") {
		t.Errorf("the Eyes-free menu item is not hidden: %s", tag)
	}
}
