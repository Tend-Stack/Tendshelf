//go:build dev

package web

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// nightApp opens the full player on a page whose night window starts fromOffset minutes from now
// (negative: already started) and lasts length minutes. The browser and the test share the machine's
// clock, so the window is computed here and nothing random reaches the screen.
func nightApp(t *testing.T, fromOffset, length int) (*playerEnv, int, int) {
	t.Helper()
	e := playerApp(t)
	now := time.Now()
	nowMin := now.Hour()*60 + now.Minute()
	from := ((nowMin+fromOffset)%1440 + 1440) % 1440
	to := (from + length) % 1440
	set, err := e.a.srv.users.LoadSettings(context.Background(), e.ownerID)
	if err != nil {
		t.Fatal(err)
	}
	set.NightSleep, set.NightFrom, set.NightTo, set.NightMinutes = true, from, to, 30
	if err := e.a.srv.users.SaveSettings(context.Background(), e.ownerID, set); err != nil {
		t.Fatal(err)
	}
	return e, from, to
}

func nightToasts(tab *cdp.Tab) string {
	var s string
	tab.Eval(`[...document.querySelectorAll(".ts-toast")].map((t) => t.textContent.replace(/\s+/g, " ").trim()).join("|")`, &s)
	return s
}

func nightMode(tab *cdp.Tab) (mode, choice string) {
	var s struct{ Mode, Choice string }
	tab.Eval(`(() => { const i = window.tsPlayerFull.sleepInfo(); return {Mode: i.mode, Choice: i.choice}; })()`, &s)
	return s.Mode, s.Choice
}

func TestNightSleepBrowserStarts(t *testing.T) {
	e, _, _ := nightApp(t, -60, 120)
	tab := fullpOpen(t, e, 390, 844, false)
	fullpOpenPlayer(t, tab)
	playerClickSel(t, tab, "[data-ts-full-toggle]")
	tab.WaitFor(`window.tsPlayer.state().playing`, 5*time.Second)
	tab.WaitFor(`window.tsPlayerFull.sleepInfo().mode === "minutes"`, 3*time.Second)
	active, value := sleepItem(tab)
	if !active || (value != "29:59" && value != "30:00") {
		t.Errorf("after playing in the window: active %v, value %q", active, value)
	}
	if text := nightToasts(tab); !strings.Contains(text, "Sleep timer on, 30 minutes") || !strings.Contains(text, "Turn off") {
		t.Errorf("toast = %q", text)
	}
}

func TestNightSleepBrowserOutside(t *testing.T) {
	e, _, _ := nightApp(t, 120, 60)
	tab := fullpOpen(t, e, 390, 844, false)
	fullpOpenPlayer(t, tab)
	playerClickSel(t, tab, "[data-ts-full-toggle]")
	tab.WaitFor(`window.tsPlayer.state().playing`, 5*time.Second)
	time.Sleep(700 * time.Millisecond)
	if mode, _ := nightMode(tab); mode != "off" {
		t.Errorf("sleep mode = %q outside the window, want off", mode)
	}
	if text := nightToasts(tab); strings.Contains(text, "Sleep timer on") {
		t.Errorf("toast = %q", text)
	}
}

func TestNightSleepBrowserOffStays(t *testing.T) {
	e, _, _ := nightApp(t, -60, 120)
	tab := fullpOpen(t, e, 390, 844, false)
	fullpOpenPlayer(t, tab)
	playerClickSel(t, tab, "[data-ts-full-toggle]")
	tab.WaitFor(`window.tsPlayerFull.sleepInfo().mode === "minutes"`, 5*time.Second)
	sleepChoose(t, tab, "off")
	if mode, _ := nightMode(tab); mode != "off" {
		t.Fatalf("mode after Off = %q", mode)
	}
	tab.Eval(`window.tsPlayer.pause()`, nil)
	tab.WaitFor(`!window.tsPlayer.state().playing`, 3*time.Second)
	tab.Eval(`window.tsPlayer.play()`, nil)
	tab.WaitFor(`window.tsPlayer.state().playing`, 5*time.Second)
	time.Sleep(700 * time.Millisecond)
	if mode, _ := nightMode(tab); mode != "off" {
		t.Errorf("night sleep started again after the person turned it off: mode %q", mode)
	}
}

func TestNightSleepBrowserKeepsManual(t *testing.T) {
	e, _, _ := nightApp(t, -60, 120)
	tab := fullpOpen(t, e, 390, 844, false)
	fullpOpenPlayer(t, tab)
	sleepChoose(t, tab, "15")
	playerClickSel(t, tab, "[data-ts-full-toggle]")
	tab.WaitFor(`window.tsPlayer.state().playing`, 5*time.Second)
	time.Sleep(700 * time.Millisecond)
	if mode, choice := nightMode(tab); mode != "minutes" || choice != "15" {
		t.Errorf("manual timer replaced: mode %q, choice %q", mode, choice)
	}
	if text := nightToasts(tab); strings.Contains(text, "Sleep timer on") {
		t.Errorf("toast = %q", text)
	}
}

// TestNightSleepBrowserWindow checks the window function through the page: the midnight wrap, an
// empty window, and both edges (the start is in, the end is out).
func TestNightSleepBrowserWindow(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 844, false)
	tab.WaitFor(`window.tsPlayerFull && window.tsPlayerFull.debug.inNightWindow`, 5*time.Second)
	rows := []struct {
		name          string
		now, from, to int
		want          bool
	}{
		{"same day inside", 600, 540, 720, true},
		{"same day before", 500, 540, 720, false},
		{"same day after", 800, 540, 720, false},
		{"start edge is in", 540, 540, 720, true},
		{"end edge is out", 720, 540, 720, false},
		{"wrap late evening", 1380, 1320, 360, true},
		{"wrap after midnight", 30, 1320, 360, true},
		{"wrap start edge", 1320, 1320, 360, true},
		{"wrap end edge", 360, 1320, 360, false},
		{"wrap midday", 720, 1320, 360, false},
		{"wrap just before start", 1319, 1320, 360, false},
		{"empty window", 600, 600, 600, false},
		{"empty window at midnight", 0, 0, 0, false},
	}
	for _, r := range rows {
		var got bool
		tab.Eval(`window.tsPlayerFull.debug.inNightWindow(`+strconv.Itoa(r.now)+`, `+strconv.Itoa(r.from)+`, `+strconv.Itoa(r.to)+`)`, &got)
		if got != r.want {
			t.Errorf("%s: inNightWindow(%d, %d, %d) = %v, want %v", r.name, r.now, r.from, r.to, got, r.want)
		}
	}
}
