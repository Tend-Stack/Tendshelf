//go:build dev

package web

import (
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// Browser tests of Eyes-free mode and the shake check (M3b, decision 0013), run by make player. The
// fixture is the player's (fullpOpen): one book, the stored place in chapter 4.

const (
	efDialog  = `document.getElementById("eyes-free")`
	efSummary = `#player-full [data-ts-full-menu] summary`
)

// efOpen opens the full player at 390 px, starts the sound and opens Eyes-free from the ⋯ menu.
func efOpen(t *testing.T, e *playerEnv) *cdp.Tab {
	t.Helper()
	tab := fullpOpen(t, e, 390, 844, true)
	fullpOpenPlayer(t, tab)
	bmkeyPlay(t, tab, "[data-ts-full-toggle]")
	efOpenMode(t, tab)
	return tab
}

func efOpenMode(t *testing.T, tab *cdp.Tab) {
	t.Helper()
	playerClickSel(t, tab, efSummary)
	playerClickSel(t, tab, "[data-ts-eyes-free-open]")
	tab.WaitFor(efDialog+`.open && `+efDialog+`.matches(":modal")`, 5*time.Second)
}

func efText(tab *cdp.Tab, part string) string {
	var s string
	tab.Eval(`document.querySelector("#eyes-free [data-ts-ef-`+part+`]").textContent.trim()`, &s)
	return s
}

func efFocusIs(tab *cdp.Tab, sel string) bool {
	var ok bool
	tab.Eval(`document.activeElement === document.querySelector(`+cdp.Quote(sel)+`)`, &ok)
	return ok
}

func TestEyesFreeOpenClose(t *testing.T) {
	e := playerApp(t)
	tab := efOpen(t, e)
	if !efFocusIs(tab, "#eyes-free [data-ts-ef-toggle]") {
		t.Error("focus is not on the play and pause zone after opening")
	}
	var title string
	tab.Eval(`document.querySelector("#eyes-free-title").textContent`, &title)
	if title == "" || efText(tab, "chapter") == "" {
		t.Errorf("title %q chapter %q: the heading is empty", title, efText(tab, "chapter"))
	}
	if !playerStateOf(tab).Playing {
		t.Error("opening Eyes-free stopped the sound")
	}

	// Escape closes only Eyes-free: focus returns to the ⋯ button, the sound and the full player stay.
	fullpKey(t, tab, "Escape", 27)
	tab.WaitFor(`!`+efDialog+`.open`, 3*time.Second)
	tab.WaitFor(`document.activeElement === document.querySelector(`+cdp.Quote(efSummary)+`)`, 2*time.Second)
	var still bool
	tab.Eval(fullpDialog+`.open`, &still)
	if !still || !playerStateOf(tab).Playing {
		t.Errorf("after Escape: full player open %v, playing %v", still, playerStateOf(tab).Playing)
	}

	// The Back to the player button closes it the same way.
	efOpenMode(t, tab)
	playerClickSel(t, tab, "[data-ts-ef-close]")
	tab.WaitFor(`!`+efDialog+`.open`, 3*time.Second)
	tab.WaitFor(`document.activeElement === document.querySelector(`+cdp.Quote(efSummary)+`)`, 2*time.Second)
	if !efFocusIs(tab, efSummary) || !playerStateOf(tab).Playing {
		t.Errorf("after Back: focus on the menu button %v, playing %v", efFocusIs(tab, efSummary), playerStateOf(tab).Playing)
	}

	// The script and the stylesheet were loaded once, on first use.
	var loaded int
	tab.Eval(`document.querySelectorAll('link[rel="stylesheet"][href*="pages-eyes-free"]').length`, &loaded)
	if loaded != 1 {
		t.Errorf("Eyes-free stylesheets linked = %d, want 1", loaded)
	}
}

func TestEyesFreeLazyLoad(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 844, true)
	fullpOpenPlayer(t, tab)
	var before struct{ Script, Style int }
	probe := `(() => ({Script: performance.getEntriesByType("resource").filter((r) => r.name.includes("/js/eyes-free")).length,
	  Style: document.querySelectorAll('link[rel="stylesheet"][href*="pages-eyes-free"]').length}))()`
	tab.Eval(probe, &before)
	if before.Script != 0 || before.Style != 0 {
		t.Fatalf("before the first opening: %+v loaded, want nothing", before)
	}
	efOpenMode(t, tab)
	var after struct{ Script, Style int }
	tab.Eval(probe, &after)
	if after.Script != 1 || after.Style != 1 {
		t.Errorf("after the first opening: %+v, want one script and one stylesheet", after)
	}
}

func TestEyesFreeButtons(t *testing.T) {
	e := playerApp(t)
	tab := efOpen(t, e)
	var skip struct{ Back, Forward float64 }
	tab.Eval(`({Back: window.tsPlayer.info().skipBack, Forward: window.tsPlayer.info().skipForward})`, &skip)
	if efText(tab, "back-text") == "" || efText(tab, "forward-text") == "" {
		t.Error("the skip zones have no words")
	}

	// Back and forward move by the person's seconds (the sound runs on, so a tolerance).
	before := fullpPos(tab)
	playerClickSel(t, tab, "[data-ts-ef-forward]")
	fullpNear(t, "forward", fullpPos(tab), before+int64(skip.Forward*1000), 1500)
	before = fullpPos(tab)
	playerClickSel(t, tab, "[data-ts-ef-back]")
	fullpNear(t, "back", fullpPos(tab), before-int64(skip.Back*1000), 1500)

	// The big zone pauses and plays.
	playerClickSel(t, tab, "[data-ts-ef-toggle]")
	tab.WaitFor(`!window.tsPlayer.state().playing`, 3*time.Second)
	if got := efText(tab, "toggle-text"); got != "Play" {
		t.Errorf("paused: the zone says %q, want Play", got)
	}
	playerClickSel(t, tab, "[data-ts-ef-toggle]")
	tab.WaitFor(`window.tsPlayer.state().playing`, 3*time.Second)
	if got := efText(tab, "toggle-text"); got != "Pause" {
		t.Errorf("playing: the zone says %q, want Pause", got)
	}

	// Bookmark: one request, and a toast with no link inside the dialog.
	bmkeyCount(tab)
	playerClickSel(t, tab, "[data-ts-ef-bookmark]")
	tab.WaitFor(`document.querySelector("#eyes-free .ts-toast") !== null`, 3*time.Second)
	text, href := bmkeyToast(tab, "#eyes-free")
	if len(text) < len("Bookmarked at ") || text[:len("Bookmarked at ")] != "Bookmarked at " || href != "" {
		t.Errorf("toast %q link %q: want the words only, no link", text, href)
	}
	var elsewhere int
	tab.Eval(`document.querySelectorAll("#toasts .ts-toast, #player-full .ts-toast").length`, &elsewhere)
	if n := bmkeyCalls(tab); n != 1 || elsewhere != 0 {
		t.Errorf("bookmark requests %d (want 1), toasts outside Eyes-free %d (want 0)", n, elsewhere)
	}

	// Sleep steps through off, 15, 30, 60, end of chapter and off again.
	for i, want := range []string{"Sleep", "15 min", "30 min", "60 min", "End of chapter", "Sleep"} {
		if i > 0 {
			playerClickSel(t, tab, "[data-ts-ef-sleep]")
		}
		if got := efText(tab, "sleep-text"); got != want {
			t.Errorf("sleep press %d: the zone says %q, want %q", i, got, want)
		}
	}
	if efText(tab, "status") != "Turn off" {
		t.Errorf("status after the last press %q, want Turn off", efText(tab, "status"))
	}

	// The zones are big: far above 44 px.
	var small int
	tab.Eval(`[...document.querySelectorAll("#eyes-free .ts-eyesfree__zone")].filter((z) => { const r = z.getBoundingClientRect(); return r.width < 88 || r.height < 88; }).length`, &small)
	if small != 0 {
		t.Errorf("%d zones are smaller than 88 px", small)
	}
}

func TestEyesFreeDim(t *testing.T) {
	e := playerApp(t)
	tab := efOpen(t, e)
	var dim bool
	tab.Eval(efDialog+`.classList.contains("is-dim")`, &dim)
	if dim {
		t.Fatal("dim right after opening")
	}
	time.Sleep(10500 * time.Millisecond) // the real 10 seconds: nothing in the shipped markup shortens it
	tab.Eval(efDialog+`.classList.contains("is-dim")`, &dim)
	if !dim {
		t.Fatal("not dimmed after 10.5 s")
	}
	// The first tap only wakes the screen; the second reaches the button.
	playerClickSel(t, tab, "[data-ts-ef-toggle]")
	tab.Eval(efDialog+`.classList.contains("is-dim")`, &dim)
	if dim || !playerStateOf(tab).Playing {
		t.Errorf("after the first tap: dim %v, playing %v; want awake and still playing", dim, playerStateOf(tab).Playing)
	}
	playerClickSel(t, tab, "[data-ts-ef-toggle]")
	tab.WaitFor(`!window.tsPlayer.state().playing`, 3*time.Second)
}

func TestEyesFreeWakeLock(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 844, true)
	fullpOpenPlayer(t, tab)
	tab.Eval(`(() => {
	  window.wl = {requests: 0, releases: 0, vis: "visible"};
	  Object.defineProperty(navigator, "wakeLock", {configurable: true, value: {request: async () => {
	    window.wl.requests++;
	    return {release: async () => { window.wl.releases++; }};
	  }}});
	  Object.defineProperty(document, "visibilityState", {configurable: true, get: () => window.wl.vis});
	})()`, nil)
	efOpenMode(t, tab)
	counts := func() (r, rel int) {
		var c struct{ Requests, Releases int }
		tab.Eval(`({Requests: window.wl.requests, Releases: window.wl.releases})`, &c)
		return c.Requests, c.Releases
	}
	tab.WaitFor(`window.wl.requests === 1`, 3*time.Second)
	tab.Eval(`window.wl.vis = "hidden"; document.dispatchEvent(new Event("visibilitychange"))`, nil)
	tab.WaitFor(`window.wl.releases === 1`, 3*time.Second)
	tab.Eval(`window.wl.vis = "visible"; document.dispatchEvent(new Event("visibilitychange"))`, nil)
	tab.WaitFor(`window.wl.requests === 2`, 3*time.Second)
	fullpKey(t, tab, "Escape", 27)
	tab.WaitFor(`window.wl.releases === 2`, 3*time.Second)
	if r, rel := counts(); r != 2 || rel != 2 {
		t.Errorf("requests %d releases %d, want 2 and 2", r, rel)
	}
}

// TestEyesFreeShake is the proof of decision 0013's shake check: a sleep timer started from Eyes-free
// asks for motion inside the tap, and two jolts during the fade add ten minutes.
func TestEyesFreeShake(t *testing.T) {
	e := playerApp(t)
	tab := efOpen(t, e)
	tab.Eval(`DeviceMotionEvent.requestPermission = () => Promise.resolve("granted")`, nil) // as iOS answers after the tap
	playerClickSel(t, tab, "[data-ts-ef-sleep]")                                            // off to 15 minutes
	var s struct {
		Mode    string
		LeftMs  float64
		Fading  bool
		Choice  string
		Motion  bool
		Playing bool
	}
	read := func() {
		tab.Eval(`(() => { const d = window.tsPlayerFull.debug.sleep(); return {Mode: d.mode, LeftMs: d.leftMs, Fading: d.fading, Choice: d.choice, Motion: d.motion, Playing: window.tsPlayer.state().playing}; })()`, &s)
	}
	read()
	if s.Mode != "minutes" || s.Choice != "15" {
		t.Fatalf("after one press: %+v, want the 15 minute timer", s)
	}
	tab.Eval(`window.tsPlayerFull.debug.advanceSleep(15 * 60000 - 6000)`, nil)
	read()
	if !s.Fading || !s.Motion {
		t.Fatalf("into the fade: %+v, want fading with the motion listener on", s)
	}
	left := s.LeftMs
	jolt := `window.dispatchEvent(new DeviceMotionEvent("devicemotion", {acceleration: {x: 20, y: 0, z: 0}}))`
	tab.Eval(jolt, nil)
	time.Sleep(250 * time.Millisecond)
	tab.Eval(jolt, nil)
	read()
	if s.Fading || s.LeftMs < left+590000 || s.LeftMs > left+610000 {
		t.Errorf("after the shake: %+v (before: %.0f ms left), want about ten minutes added and no fade", s, left)
	}
}
