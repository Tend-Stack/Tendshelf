//go:build dev

package web

import (
	"strconv"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// Browser tests of P1c-5b (make player): speed, sleep timer and volume. The fixture is the full
// player's (fullpOpen): a 180 s book, six chapters of 30 s, the stored place at 100 s.

const speedSheetSel = `#player-full [data-ts-speed-sheet]`
const sleepSheetSel = `#player-full [data-ts-sleep-sheet]`

// speedWaitSheet waits until the sheet matching sel is shown.
func speedWaitSheet(tab *cdp.Tab, sel string) {
	tab.WaitFor(`!document.querySelector(`+cdp.Quote(sel)+`).hidden`, 3*time.Second)
}

func speedWaitSheetClosed(tab *cdp.Tab, sel string) {
	tab.WaitFor(`document.querySelector(`+cdp.Quote(sel)+`).hidden`, 3*time.Second)
}

// speedCountSettings wraps fetch so the test can see the speed saves.
func speedCountSettings(tab *cdp.Tab) {
	tab.Eval(`(() => { window.speedCalls = []; const f = window.fetch;
	  window.fetch = (u, o) => { if (String(u).includes("/speed")) window.speedCalls.push(o.body); return f(u, o); }; })()`, nil)
}

func speedCalls(tab *cdp.Tab) []string {
	var calls []string
	tab.Eval(`window.speedCalls`, &calls)
	return calls
}

func TestFullPlayerBrowserSpeed(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 844, false)
	fullpOpenPlayer(t, tab)
	speedCountSettings(tab)

	// The Speed item opens the sheet with focus on the current speed.
	playerClickSel(t, tab, "[data-ts-open-speed]")
	speedWaitSheet(tab, speedSheetSel)
	var focus string
	tab.Eval(`document.activeElement.dataset.tsSpeedChoice || ""`, &focus)
	if focus != "1.0" {
		t.Errorf("focus on speed %q, want 1.0", focus)
	}
	// A preset applies, closes the sheet and updates the item and its spoken label.
	playerClickSel(t, tab, `[data-ts-speed-choice="1.5"]`)
	speedWaitSheetClosed(tab, speedSheetSel)
	var got struct {
		Rate  float64
		Pitch bool
		Item  string
		Label string
	}
	tab.Eval(`(() => { const a = document.getElementById("audio"), i = document.querySelector("#player-full [data-ts-open-speed]");
	  return {Rate: a.playbackRate, Pitch: a.preservesPitch, Item: i.textContent.trim().split(/\s+/)[0], Label: i.getAttribute("aria-label")}; })()`, &got)
	if got.Rate != 1.5 || !got.Pitch || got.Item != "1.5×" || got.Label != "Playback speed, 1.5 times" {
		t.Errorf("after choosing 1.5: %+v", got)
	}

	// Saved a second after the last change: the preset, then three slider moves become one more save.
	time.Sleep(1400 * time.Millisecond)
	if calls := speedCalls(tab); len(calls) != 1 || calls[0] != "speed=1.50" {
		t.Fatalf("saves after the preset = %v", calls)
	}
	for _, v := range []string{"2.0", "2.2", "2.35"} {
		tab.Eval(`(() => { const r = document.querySelector("#player-full [data-ts-speed-range]"); r.value = `+v+`; r.dispatchEvent(new Event("input", {bubbles: true})); })()`, nil)
		time.Sleep(100 * time.Millisecond)
	}
	time.Sleep(1500 * time.Millisecond)
	if calls := speedCalls(tab); len(calls) != 2 || calls[1] != "speed=2.35" {
		t.Errorf("saves after the slider = %v, want two, the last speed=2.35", calls)
	}

	// The speed follows the person: a reload starts at 2.35.
	tab.Navigate(e.URL + "/libraries")
	tab.WaitFor(`window.tsPlayer && window.tsPlayer.state().bookId !== ""`, 10*time.Second)
	if s := playerStateOf(tab); s.Speed != 2.35 {
		t.Errorf("speed after reload = %v, want 2.35", s.Speed)
	}

	// A held press on the bar opens the player with the speed sheet and does not navigate.
	tab.Eval(`(() => { const o = document.querySelector("#player [data-ts-open]");
	  o.dispatchEvent(new PointerEvent("pointerdown", {bubbles: true, button: 0, pointerId: 7, clientX: 100, clientY: 700})); })()`, nil)
	time.Sleep(700 * time.Millisecond)
	tab.Eval(`document.querySelector("#player [data-ts-open]").dispatchEvent(new PointerEvent("pointerup", {bubbles: true, button: 0, pointerId: 7}))`, nil)
	speedWaitSheet(tab, speedSheetSel)
	var path string
	tab.Eval(`location.pathname`, &path)
	if path != "/libraries" {
		t.Errorf("a long press went to %s", path)
	}

	// At 1440 the bar's Speed button opens the side panel with the sheet.
	wide := fullpOpen(t, e, 1440, 900, false)
	playerClickSel(t, wide, "[data-ts-bar-speed]")
	wide.WaitFor(fullpDialog+`.open`, 3*time.Second)
	speedWaitSheet(wide, speedSheetSel)
}

// sleepCard reads the visible state of the sleep card in the player.
func sleepCard(tab *cdp.Tab) (shown bool, text string) {
	var c struct {
		Hidden bool
		Text   string
	}
	tab.Eval(`(() => { const c = document.querySelector('#player-full [data-ts-sleep-card="full"]');
	  return {Hidden: c.hidden, Text: c.querySelector("[data-ts-sleep-count]").textContent}; })()`, &c)
	return !c.Hidden, c.Text
}

func sleepVolume(tab *cdp.Tab) float64 {
	var v float64
	tab.Eval(`document.getElementById("audio").volume`, &v)
	return v
}

func sleepItem(tab *cdp.Tab) (active bool, value string) {
	var s struct {
		Active bool
		Value  string
	}
	tab.Eval(`(() => { const i = document.querySelector("#player-full [data-ts-open-sleep]");
	  return {Active: i.classList.contains("is-active"), Value: i.querySelector("[data-ts-full-sleep-left]").textContent}; })()`, &s)
	return s.Active, s.Value
}

func sleepNear(t *testing.T, what string, got, want, tol float64) {
	t.Helper()
	if got < want-tol || got > want+tol {
		t.Errorf("%s = %.3f, want %.3f ± %.3f", what, got, want, tol)
	}
}

// sleepChoose opens the sleep sheet and picks a choice with real clicks.
func sleepChoose(t *testing.T, tab *cdp.Tab, choice string) {
	t.Helper()
	playerClickSel(t, tab, sleepOpenSel(tab))
	speedWaitSheet(tab, sleepSheetSel)
	playerClickSel(t, tab, `[data-ts-sleep-choice="`+choice+`"]`)
	speedWaitSheetClosed(tab, sleepSheetSel)
}

func TestFullPlayerBrowserSleep(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 844, false)
	fullpOpenPlayer(t, tab)
	playerClickSel(t, tab, "[data-ts-full-toggle]")
	tab.WaitFor(`window.tsPlayer.state().playing`, 5*time.Second)
	tab.Eval(`window.tsPlayer.setVolume(0.6)`, nil)
	advance := func(ms int) {
		tab.Eval(`window.tsPlayerFull.debug.advanceSleep(`+strconv.Itoa(ms)+`)`, nil)
	}

	// Motion needs the person's permission on iOS; answer it as a phone would, inside the tap.
	tab.Eval(`DeviceMotionEvent.requestPermission = () => Promise.resolve("granted")`, nil)

	// 15 minutes: the item shows the time left and is active.
	sleepChoose(t, tab, "15")
	active, value := sleepItem(tab)
	if !active || (value != "14:59" && value != "15:00") {
		t.Errorf("after choosing 15 min: active %v, value %q", active, value)
	}
	// Twelve seconds left: no card, the volume is the person's. Then the fade starts.
	advance(15*60000 - 12000)
	if shown, _ := sleepCard(tab); shown {
		t.Error("the card is shown 12 s before the end")
	}
	sleepNear(t, "volume before the fade", sleepVolume(tab), 0.6, 0.001)
	advance(3000)
	shown, text := sleepCard(tab)
	if !shown || !(text == "Sleeping in 9 seconds" || text == "Sleeping in 8 seconds") {
		t.Errorf("fading card: shown %v, text %q", shown, text)
	}
	if v := sleepVolume(tab); v <= 0.45 || v >= 0.58 {
		t.Errorf("volume in the fade = %.3f, want between 0.45 and 0.58", v)
	}
	var inside bool
	tab.Eval(`(() => { const b = document.querySelector('#player-full [data-ts-sleep-card="full"] [data-ts-sleep-plus]').getBoundingClientRect();
	  return b.width >= 44 && b.height >= 44 && b.bottom <= innerHeight && b.right <= innerWidth; })()`, &inside)
	if !inside {
		t.Error("+10 min is under 44 px or outside the screen")
	}

	// +10 min: the card goes, the volume returns, about ten minutes are left.
	playerClickSel(t, tab, `#player-full [data-ts-sleep-card="full"] [data-ts-sleep-plus]`)
	if shown, _ := sleepCard(tab); shown {
		t.Error("the card stays after +10 min")
	}
	sleepNear(t, "volume after +10 min", sleepVolume(tab), 0.6, 0.001)
	var left float64
	tab.Eval(`window.tsPlayerFull.debug.sleep().leftMs / 1000`, &left)
	sleepNear(t, "seconds left after +10 min", left, 608, 4)

	// A shake (two hard jolts 300 ms apart) during the fade does the same.
	tab.Eval(`window.tsPlayerFull.debug.advanceSleep(window.tsPlayerFull.debug.sleep().leftMs - 9000)`, nil)
	if shown, _ := sleepCard(tab); !shown {
		t.Fatal("the second fade did not start")
	}
	for i := 0; i < 2; i++ {
		tab.Eval(`window.dispatchEvent(new DeviceMotionEvent("devicemotion", {acceleration: {x: 20, y: 0, z: 0}}))`, nil)
		time.Sleep(300 * time.Millisecond)
	}
	if shown, _ := sleepCard(tab); shown {
		t.Error("a shake did not extend the timer")
	}
	sleepNear(t, "volume after a shake", sleepVolume(tab), 0.6, 0.001)

	// Past the end: paused, the level restored, the timer off and "Turn off" gone.
	advance(700000)
	var end struct {
		Playing, Off bool
	}
	tab.Eval(`({Playing: window.tsPlayer.state().playing, Off: document.querySelector('#player-full [data-ts-sleep-choice="off"]').hidden})`, &end)
	if end.Playing || !end.Off {
		t.Errorf("after the end: %+v", end)
	}
	sleepNear(t, "volume after the end", sleepVolume(tab), 0.6, 0.001)
	if active, _ := sleepItem(tab); active {
		t.Error("the item is still active after the end")
	}

	// End of chapter: from 3 s before chapter 2's end the player stops at chapter 3's start.
	tab.Eval(`window.tsPlayer.seek(57000)`, nil)
	sleepChoose(t, tab, "chapter")
	tab.Eval(`window.tsPlayer.play()`, nil)
	tab.WaitFor(`!window.tsPlayer.state().playing`, 6*time.Second)
	fullpNear(t, "position at the end of the chapter", fullpPos(tab), 60000, 100)
	if active, _ := sleepItem(tab); active {
		t.Error("the item is still active after the chapter ended")
	}

	// 30 minutes, then Turn off.
	sleepChoose(t, tab, "30")
	if active, _ := sleepItem(tab); !active {
		t.Error("30 min did not start the timer")
	}
	sleepChoose(t, tab, "off")
	if active, _ := sleepItem(tab); active {
		t.Error("Turn off left the timer on")
	}

	// Escape closes the sheet first, then the player.
	playerClickSel(t, tab, sleepOpenSel(tab))
	speedWaitSheet(tab, sleepSheetSel)
	fullpKey(t, tab, "Escape", 27)
	speedWaitSheetClosed(tab, sleepSheetSel)
	var open bool
	tab.Eval(fullpDialog+`.open`, &open)
	if !open {
		t.Error("Escape closed the player together with the sheet")
	}
}

func TestFullPlayerBrowserVolume(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 1440, 900, false)
	tab.Eval(`(() => { const r = document.querySelector("#player [data-ts-volume]"); r.value = 40; r.dispatchEvent(new Event("input", {bubbles: true})); })()`, nil)
	var got struct {
		Volume float64
		Stored string
	}
	tab.Eval(`({Volume: document.getElementById("audio").volume, Stored: localStorage.getItem("ts.volume")})`, &got)
	if got.Volume != 0.4 || got.Stored != "0.4" {
		t.Errorf("after the slider: %+v", got)
	}
	tab.Navigate(e.URL + "/libraries")
	tab.WaitFor(`window.tsPlayer && window.tsPlayer.state().bookId !== ""`, 10*time.Second)
	var after struct {
		Volume float64
		Slider string
	}
	tab.Eval(`({Volume: document.getElementById("audio").volume, Slider: document.querySelector("#player [data-ts-volume]").value})`, &after)
	if after.Volume != 0.4 || after.Slider != "40" {
		t.Errorf("after a reload: %+v", after)
	}

	// The phone bar does not carry the slider.
	phone := fullpOpen(t, e, 390, 844, false)
	var hidden bool
	phone.Eval(`document.querySelector("#player [data-ts-volume-wrap]").offsetParent === null`, &hidden)
	if !hidden {
		t.Error("the volume slider shows on the phone")
	}
}

// sleepOpenSel is the control that opens the sleep sheet where the test runs: from 1280 px the bar's (the
// side panel's own row of tools is hidden, PB1), below it the full player's.
func sleepOpenSel(tab *cdp.Tab) string {
	var wide bool
	tab.Eval(`innerWidth >= 1280`, &wide)
	if wide {
		return "[data-ts-bar-sleep]"
	}
	return "[data-ts-open-sleep]"
}

// TestBarToolClosesItsSheet: on a wide screen the bar's Chapters button opens the list, and pressed again
// closes it (operator, 2026-11-10); another bar tool switches to its own sheet.
// sheetLeaveWait is longer than player-full.js's SHEET_LEAVE_MS (400), after which a closed sheet is hidden.
const sheetLeaveWait = 600 * time.Millisecond

func TestBarToolClosesItsSheet(t *testing.T) {
	e := nowPanelEnv(t)
	wide := fullpOpen(t, e, 1440, 900, false)
	const chaptersShown = `!document.querySelector("#player-full [data-ts-chapters-sheet]").hidden`
	playerClickSel(t, wide, "[data-ts-bar-chapters]")
	wide.WaitFor(chaptersShown, 3*time.Second)
	playerClickSel(t, wide, "[data-ts-bar-chapters]")
	// The sheet slides down before it is hidden, so wait out the leave.
	time.Sleep(sheetLeaveWait)
	var state struct {
		Shown, Focused bool
	}
	wide.Eval(`({Shown: `+chaptersShown+`, Focused: document.activeElement === document.querySelector("[data-ts-bar-chapters]")})`, &state)
	if state.Shown {
		t.Fatal("the second press on the bar's Chapters button left the list open")
	}
	if !state.Focused {
		t.Error("focus did not return to the bar's Chapters button")
	}
	playerClickSel(t, wide, "[data-ts-bar-chapters]")
	wide.WaitFor(chaptersShown, 3*time.Second)
	playerClickSel(t, wide, "[data-ts-bar-speed]")
	speedWaitSheet(wide, speedSheetSel)
	time.Sleep(sheetLeaveWait)
	var both bool
	wide.Eval(chaptersShown, &both)
	if both {
		t.Error("Chapters stayed open under the Speed sheet")
	}
}
