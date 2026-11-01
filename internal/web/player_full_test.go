package web

import (
	"strings"
	"testing"
)

// fullpSection returns the HTML of the full player dialog in a page.
func fullpSection(t *testing.T, body string) string {
	t.Helper()
	i := strings.Index(body, `<dialog id="player-full"`)
	if i < 0 {
		t.Fatal("the layout has no full player dialog")
	}
	end := strings.Index(body[i:], "</dialog>")
	if end < 0 {
		t.Fatal("the full player dialog is not closed")
	}
	return body[i : i+end]
}

// TestFullPlayerMarkup: the dialog sits outside #main and is kept by hx-preserve, carries every hook
// player-full.js drives, follows the screen reader order of the HANDOFF, and has no inline style.
func TestFullPlayerMarkup(t *testing.T) {
	rec, body := getPage(t, "/", nil)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	dlg := fullpSection(t, body)
	start := strings.Index(body, `<dialog id="player-full"`)
	if mainEnd := strings.Index(body, "</main>"); mainEnd < 0 || start < mainEnd {
		t.Error("the full player is inside #main")
	}
	if slotAt := strings.Index(body, `<section id="player"`); slotAt < 0 || start < slotAt {
		t.Error("the full player does not follow the mini-player")
	}
	for _, want := range []string{
		`class="ts-player"`, `aria-labelledby="player-full-title"`, ` hx-preserve`,
		`data-ts-close-player`, `aria-label="Close player"`, `data-ts-full-chapter`, `data-ts-bookmark`, `data-ts-chipmenu`,
		`data-ts-full-cover`, `id="player-full-title"`, `data-ts-full-title`, `data-ts-full-author`, `data-ts-full-clock`, `data-ts-full-left`,
		`data-ts-full-prev`, `aria-label="Previous chapter"`, `data-ts-full-back`, `data-ts-full-back-num`, `data-ts-full-toggle`,
		`ts-iconbtn ts-iconbtn--cover ts-iconbtn--xl`, `data-ts-full-forward`, `data-ts-full-forward-num`, `data-ts-full-next`, `aria-label="Next chapter"`,
		`data-ts-scrub="full"`, `data-ts-ticks`, `ts-scrub__fill`, `ts-scrub__chapter`, `ts-scrub__needle`, `ts-scrub__bubble`,
		`type="range"`, `<label class="ts-sr" for="ts-scrub-full">Position in book</label>`, `data-ts-full-elapsed`, `data-ts-full-fine`, `data-ts-full-book-left`,
		`class="ts-player-bar ts-player__bar"`, `data-ts-open-speed`, `data-ts-open-sleep`, `data-ts-open-chapters`,
		`data-ts-full-sheet`, `data-ts-chapters-sheet`, `data-ts-chapter-list`, `data-ts-chapter-row`,
		`data-ts-full-book`, `data-ts-full-finish`, `Go to book`, `Mark as finished`,
	} {
		if !strings.Contains(dlg, want) {
			t.Errorf("the full player lacks %s", want)
		}
	}
	// HANDOFF: title, chapter, time left, play/pause, skip, scrubber, settings.
	order := []string{`data-ts-full-title`, `data-ts-full-chapter`, `data-ts-full-left`, `data-ts-full-toggle`, `data-ts-full-back`,
		`data-ts-full-forward`, `data-ts-range`, `data-ts-open-speed`}
	last := -1
	for _, hook := range order {
		at := strings.Index(dlg, hook)
		if at < 0 || at < last {
			t.Errorf("%s is out of the screen reader order (at %d, previous at %d)", hook, at, last)
		}
		last = at
	}
	if strings.Contains(body, "style=") {
		t.Error("the page carries a style attribute")
	}
	// The mini-player carries the desktop bar's scrubber.
	mini := body[strings.Index(body, `id="mini-player"`):start]
	if !strings.Contains(mini, `ts-scrub ts-scrub--bar`) || !strings.Contains(mini, `data-ts-scrub="bar"`) {
		t.Error("the mini-player lacks the compact scrubber")
	}
	// Words come from the one copy deck, set once on #player.
	for _, attr := range []string{"data-ts-msg-chapter-of", "data-ts-msg-left-in-chapter", "data-ts-msg-left-in-book", "data-ts-msg-scrub-value",
		"data-ts-msg-scrub-bubble", "data-ts-msg-fine-scrub", "data-ts-msg-chapter-played", "data-ts-msg-open-player"} {
		if !strings.Contains(body, attr+`="`) {
			t.Errorf("#player lacks %s", attr)
		}
	}
	// The sign-in frame has no player.
	if strings.Contains(send(newTestApp(t).h, "GET", "/setup", nil, nil).Body.String(), "player-full") {
		t.Error("the sign-in frame carries the full player")
	}
}

// TestFullPlayerCopy: the words of the HANDOFF and the board, exactly.
func TestFullPlayerCopy(t *testing.T) {
	for got, want := range map[string]string{
		msgChapterOf: "Chapter {n} of {total}", msgLeftInChapter: "{t} left in chapter", msgLeftInBook: "{h} h {m} m left in book",
		msgScrubValue: "{h} hours {m} minutes elapsed, chapter {n}", msgFineScrub: "Drag up for fine control", msgClosePlayer: "Close player",
		msgOpenPlayer: "Open player: {title}, chapter {n}", msgMarkFinished: "Mark as finished", msgGoToBook: "Go to book",
	} {
		if got != want {
			t.Errorf("copy %q, want %q", got, want)
		}
	}
}
