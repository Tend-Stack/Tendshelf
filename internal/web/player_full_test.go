package web

import (
	"net/http"
	"net/url"
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
		`data-ts-scrub="full"`, `data-ts-ruler`, `data-ts-ruler-ticks`, `ts-scrub__fill`, `ts-scrub__needle`, `ts-scrub__bubble`,
		`type="range"`, `<label class="ts-sr" for="ts-scrub-full">Position in chapter</label>`, `data-ts-full-elapsed`, `data-ts-full-fine`, `data-ts-full-book-left`,
		`class="ts-player-bar ts-player__bar"`, `data-ts-open-speed`, `data-ts-open-sleep`, `data-ts-open-chapters`,
		`data-ts-full-sheet`, `data-ts-chapters-sheet`, `data-ts-chapter-list`, `data-ts-chapter-row`,
		`data-ts-full-book`, `data-ts-full-finish`, `Go to book`, `Mark as finished`,
	} {
		if !strings.Contains(dlg, want) {
			t.Errorf("the full player lacks %s", want)
		}
	}
	// P1c-5b: the speed and sleep sheets, both sleep cards, the toast region and template, the bar's
	// extras; nothing disabled; five speeds and six sleep choices.
	for _, want := range []string{`data-ts-speed-sheet`, `data-ts-sleep-sheet`, `data-ts-sleep-card="full"`, `data-ts-full-toasts`,
		`data-ts-bookmark-toast`, `data-ts-speed-range`, `data-ts-sleep-minutes`, `data-ts-sleep-start`, `data-ts-full-sleep-left`,
		`role="radiogroup"`, `aria-keyshortcuts="B"`, `aria-label="Bookmark this moment"`, `Fine-tune`, `Custom`, `Turn off`, `+10 min`,
		`hx-select="#sheet > *"`} {
		if !strings.Contains(dlg, want) {
			t.Errorf("the full player lacks %s", want)
		}
	}
	if n := strings.Count(dlg, "data-ts-speed-choice="); n != 5 {
		t.Errorf("speed choices = %d, want 5", n)
	}
	if n := strings.Count(dlg, "data-ts-sleep-choice="); n != 6 {
		t.Errorf("sleep choices = %d, want 6", n)
	}
	if strings.Contains(dlg, "disabled") {
		t.Error("a control of the full player is still disabled")
	}
	for _, want := range []string{`data-ts-bar-speed`, `data-ts-bar-sleep`, `data-ts-volume`, `data-ts-sleep-card="bar"`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %s", want)
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
	for _, attr := range []string{"data-ts-msg-chapter-of", "data-ts-msg-left-in-chapter", "data-ts-msg-left-in-book-minutes", "data-ts-msg-left-in-book-hours", "data-ts-msg-left-in-book-both", "data-ts-msg-subtitle-sep", "data-ts-msg-scrub-value",
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
		msgChapterOf: "Chapter {n} of {total}", msgLeftInChapter: "{t} left in chapter", msgLeftInBookMinutes: "{m} m left in book", msgLeftInBookHours: "{h} h left in book", msgLeftInBookBoth: "{h} h {m} m left in book", msgSubtitleSep: " · ",
		msgScrubValue: "Chapter {n}, {m} minutes {s} seconds elapsed", msgFineScrub: "Drag up for fine control", msgClosePlayer: "Close player",
		msgOpenPlayer: "Open player: {title}, chapter {n}", msgMarkFinished: "Mark as finished", msgGoToBook: "Go to book",
	} {
		if got != want {
			t.Errorf("copy %q, want %q", got, want)
		}
	}
}

// TestFullPlayerCopy5b: the speed, sleep timer and bookmark words, and the attributes the player reads.
func TestFullPlayerCopy5b(t *testing.T) {
	for got, want := range map[string]string{
		msgSleeping: "Sleeping in {n} seconds", msgPlusTen: "+10 min", msgSleepOffLabel: "Sleep timer, off",
		msgSpeedLabel: "Playback speed, {v} times", msgSleepFadingShake: "Fading out. Shake your phone to add 10 minutes.",
		msgBookmarkMoment: "Bookmark this moment", msgSleepEndOfChapter: "End of chapter", msgSleepStart: "Start timer",
	} {
		if got != want {
			t.Errorf("copy %q, want %q", got, want)
		}
	}
	_, body := getPage(t, "/", nil)
	for _, attr := range []string{"speed-value", "speed-label", "sleep-in", "sleep-off-label", "sleep-on-label", "sleeping", "sleep-fading",
		"sleep-fading-shake", "bookmark-failed", "bookmarked", "bookmark-moment-at"} {
		if !strings.Contains(body, "data-ts-msg-"+attr+`="`) {
			t.Errorf("#player lacks data-ts-msg-%s", attr)
		}
	}
	for _, want := range []string{`data-ts-msg-bookmarked="Bookmarked at {time} · "`, `Add a note`, `<kbd>B</kbd>`} {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %s", want)
		}
	}
}

// TestFullPlayerSpeedSetting: the speed sheet saves through POST /me/settings (no new endpoint).
func TestFullPlayerSpeedSetting(t *testing.T) {
	a := newTestApp(t)
	ck := a.owner(t)
	if rec := settingsPost(t, a, ck, "/me/settings", url.Values{"speed": {"2.35"}}, true); rec.Code != http.StatusNoContent {
		t.Fatalf("speed 2.35 = %d, want 204", rec.Code)
	}
	if got := settingsOf(t, a).Speed; got != 2.35 {
		t.Errorf("saved speed = %v, want 2.35", got)
	}
	if rec := settingsPost(t, a, ck, "/me/settings", url.Values{"speed": {"2.33"}}, true); rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("speed 2.33 = %d, want 422", rec.Code)
	}
	if got := settingsOf(t, a).Speed; got != 2.35 {
		t.Errorf("speed after a refused post = %v, want 2.35", got)
	}
	rec := a.do(http.MethodPost, "/me/settings", url.Values{"speed": {"1.00"}}, func(r *http.Request) { r.Header.Set("HX-Request", "true") }, ck)
	if rec.Code != http.StatusForbidden {
		t.Errorf("speed without a token = %d, want 403", rec.Code)
	}
}
