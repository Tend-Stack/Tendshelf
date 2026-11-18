//go:build dev

package web

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// Casting in the full player (decision 0047, plan P2). Google's sender script is never reached: the tests block
// gstatic and, where a session is needed, inject testdata/cast/cast_stub.js (a test double that the server never
// serves). Needs TENDSHELF_TEST_BROWSER.

// castTurnOn flips the owner's casting setting directly, so every page of the run carries cast.js.
func castTurnOn(t *testing.T, e *playerEnv) {
	t.Helper()
	streamExec(t, e.a, `INSERT OR REPLACE INTO user_settings(user_id, key, value_json, updated_at) VALUES (?, 'playback.cast', 'true', 1)`, e.ownerID)
}

// castBlockGoogle keeps the tab from reaching Google, whatever the network allows.
func castBlockGoogle(t *testing.T, tab *cdp.Tab) {
	t.Helper()
	if err := tab.Call("Network.enable", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	if err := tab.Call("Network.setBlockedURLs", map[string]any{"urls": []string{"*gstatic.com*"}}, nil); err != nil {
		t.Fatal(err)
	}
}

// castInjectStub installs the test double and records violations and page errors.
func castInjectStub(t *testing.T, tab *cdp.Tab) {
	t.Helper()
	stub, err := os.ReadFile("testdata/cast/cast_stub.js")
	if err != nil {
		t.Fatal(err)
	}
	tab.Eval(string(stub)+`;window.__errs = [];
	  addEventListener("error", (e) => window.__errs.push(String(e.message)));
	  addEventListener("unhandledrejection", (e) => window.__errs.push(String(e.reason)));
	  document.addEventListener("securitypolicyviolation", (e) => window.__errs.push("csp " + e.blockedURI));
	  window.__cmds = [];
	  document.addEventListener("ts:cast:command", (e) => window.__cmds.push(e.detail.action));
	  window.__posts = [];
	  const realFetch = window.fetch;
	  window.fetch = (url, init) => {
	    if (String(url).includes("/progress/")) window.__posts.push(String(init && init.body));
	    return realFetch(url, init);
	  };
	  true`, nil)
}

// A person with casting on, in a browser that cannot reach Google: the SDK is requested when the full player opens,
// the button stays hidden, nothing throws and the book still plays here.
func TestPlayerCastHiddenWithoutSDK(t *testing.T) {
	e := playerApp(t)
	castTurnOn(t, e)
	tab := fullpOpen(t, e, 390, 900, true)
	castBlockGoogle(t, tab)
	tab.Eval(`window.__errs = []; addEventListener("error", (e) => window.__errs.push(String(e.message))); true`, nil)

	var before bool
	tab.Eval(`!!document.querySelector('script[src*="gstatic.com"]')`, &before)
	if before {
		t.Fatal("Google's script was requested before the full player opened")
	}
	fullpOpenPlayer(t, tab)
	tab.WaitFor(`!!document.querySelector('script[src*="gstatic.com"]')`, 3*time.Second)
	time.Sleep(600 * time.Millisecond)

	var r struct {
		Hidden bool
		Errs   []string
	}
	tab.Eval(`({Hidden: [...document.querySelectorAll("[data-ts-cast]")].every((b) => b.hidden), Errs: window.__errs})`, &r)
	if !r.Hidden {
		t.Error("a Cast button shows without the SDK")
	}
	if len(r.Errs) != 0 {
		t.Errorf("page errors: %v", r.Errs)
	}
	playerClickSel(t, tab, "[data-ts-full-toggle]")
	tab.WaitFor(`window.tsPlayer.state().playing`, 5*time.Second)
}

// Casting off: the page never asks for the script (the full player's open is not a trigger).
func TestPlayerCastAbsentWhenOff(t *testing.T) {
	e := playerApp(t)
	tab := fullpOpen(t, e, 390, 900, true)
	fullpOpenPlayer(t, tab)
	time.Sleep(300 * time.Millisecond)
	var r struct {
		Script, Google bool
	}
	tab.Eval(`({Script: !!document.querySelector('script[src*="cast.js"]'), Google: !!document.querySelector('script[src*="gstatic.com"]')})`, &r)
	if r.Script || r.Google {
		t.Errorf("casting is off but cast.js loaded %v, Google's script %v", r.Script, r.Google)
	}
}

// The events cast.js sends drive the player: the local audio pauses, the clock follows the device, a save carries the
// device's place, the commands go out as events, and the end hands the place back, paused.
func TestPlayerCastEventsDrivePlayer(t *testing.T) {
	e := playerApp(t)
	castTurnOn(t, e)
	tab := fullpOpen(t, e, 390, 900, true)
	castBlockGoogle(t, tab)
	castInjectStub(t, tab)

	tab.Eval(`document.dispatchEvent(new CustomEvent("ts:cast:begin", {detail: {device: "Living room TV", positionMs: 61000}}))`, nil)
	var s struct {
		Casting    string  `json:"casting"`
		PositionMs int64   `json:"positionMs"`
		Playing    bool    `json:"playing"`
		Speed      float64 `json:"speed"`
	}
	tab.Eval(`window.tsPlayer.state()`, &s)
	if s.Casting != "Living room TV" || s.PositionMs != 61000 || !s.Playing || s.Speed != 1 {
		t.Fatalf("after begin: %+v", s)
	}
	var localPaused bool
	tab.Eval(`document.getElementById("audio").paused`, &localPaused)
	if !localPaused {
		t.Error("the local audio plays while casting")
	}

	tab.Eval(`document.dispatchEvent(new CustomEvent("ts:cast:position", {detail: {positionMs: 70000, paused: false}}))`, nil)
	tab.Eval(`window.tsPlayer.state()`, &s)
	if s.PositionMs != 70000 || !s.Playing {
		t.Fatalf("after position: %+v", s)
	}

	// Transport, scrub and the sleep timer's pause become commands.
	tab.Eval(`window.tsPlayer.pause(); window.tsPlayer.seek(100000)`, nil)
	var cmds []string
	tab.Eval(`window.__cmds`, &cmds)
	if strings.Join(cmds, ",") != "pause,seek" {
		t.Errorf("commands = %v, want pause then seek", cmds)
	}

	// The device reports paused: the save carries its place, not the local element's.
	tab.Eval(`document.dispatchEvent(new CustomEvent("ts:cast:position", {detail: {positionMs: 100000, paused: true}}))`, nil)
	tab.WaitFor(`window.__posts.some((b) => b.includes("position_ms=100000"))`, 5*time.Second)
	tab.Eval(`window.tsPlayer.state()`, &s)
	if s.Playing {
		t.Error("the player says playing while the device is paused")
	}

	// The device stops at 95 s: the local player takes that place and stays paused.
	tab.Eval(`document.dispatchEvent(new CustomEvent("ts:cast:end", {detail: {positionMs: 95000}}))`, nil)
	tab.Eval(`window.tsPlayer.state()`, &s)
	if s.Casting != "" || s.PositionMs != 95000 || s.Playing {
		t.Fatalf("after end: %+v", s)
	}
	tab.WaitFor(`window.__posts.some((b) => b.includes("position_ms=95000"))`, 5*time.Second)
	tab.Eval(`document.getElementById("audio").paused`, &localPaused)
	if !localPaused {
		t.Error("the local audio plays after the device stopped")
	}
}

// The button with the test double: it shows once a device is around, a refused address ends the session with words,
// and a session with an address loads the book's files as one queue and follows the device.
func TestPlayerCastStubSession(t *testing.T) {
	e := playerApp(t)
	castTurnOn(t, e)
	tab := fullpOpen(t, e, 390, 900, true)
	castBlockGoogle(t, tab)
	castInjectStub(t, tab)
	fullpOpenPlayer(t, tab)
	tab.WaitFor(`window.__castStub.options !== null`, 3*time.Second)

	var hidden bool
	tab.Eval(`document.querySelector(".ts-player-bar [data-ts-cast]").hidden`, &hidden)
	if !hidden {
		t.Fatal("the Cast button shows with no device around")
	}
	tab.Eval(`window.__castStub.discover(); true`, nil)
	tab.Eval(`document.querySelector(".ts-player-bar [data-ts-cast]").hidden`, &hidden)
	if hidden {
		t.Fatal("the Cast button stays hidden with a device around")
	}

	// The test server answers 409 (a loopback host): told in words, the session ends, the book plays here.
	playerClickSel(t, tab, ".ts-player-bar [data-ts-cast]")
	tab.WaitFor(`window.__castStub.ended === 1`, 5*time.Second)
	var toast string
	tab.Eval(`document.querySelector("#player-full [data-ts-full-toasts]").textContent`, &toast)
	if !strings.Contains(toast, "can't reach this server") {
		t.Errorf("toast = %q", toast)
	}
	var casting string
	tab.Eval(`window.tsPlayer.state().casting`, &casting)
	if casting != "" {
		t.Errorf("casting = %q after a refused address", casting)
	}

	// With an address the queue holds every playable file, starting at the one with the place.
	tab.Eval(`const real = window.fetch;
	  window.fetch = (url, init) => String(url).includes("/casting/address")
	    ? Promise.resolve(new Response(JSON.stringify({base: "https://tv.example.test/cast/tok", expiresAt: Date.now() + 3600000}), {status: 200, headers: {"Content-Type": "application/json"}}))
	    : real(url, init);
	  true`, nil)
	playerClickSel(t, tab, ".ts-player-bar [data-ts-cast]")
	tab.WaitFor(`window.tsPlayer.state().casting === "Test TV"`, 5*time.Second)
	var load struct {
		N     int
		Start int
		First string
		Art   string
	}
	tab.Eval(`(() => { const l = window.__castStub.loads[0]; return {N: l.items.length, Start: l.startIndex, First: l.items[0].media.contentId, Art: l.items[0].media.metadata.images[0].url}; })()`, &load)
	if load.N != 2 || !strings.HasPrefix(load.First, "https://tv.example.test/cast/tok/audio/") || load.Art != "https://tv.example.test/cast/tok/art" {
		t.Errorf("queue = %+v", load)
	}
	var where string
	tab.Eval(`document.querySelector("#player-full [data-ts-cast-where]").textContent`, &where)
	if where != "Playing on Test TV" {
		t.Errorf("where = %q", where)
	}
	var pressed string
	tab.Eval(`document.querySelector(".ts-player-bar [data-ts-cast]").getAttribute("aria-pressed") + " " + document.querySelector(".ts-player-bar [data-ts-cast-label]").textContent`, &pressed)
	if pressed != "true Casting" {
		t.Errorf("button = %q", pressed)
	}

	// The device's clock moves the book clock; the device failing hands the place back with words.
	tab.Eval(`window.__castStub.play(30, false); true`, nil)
	var pos int64
	tab.WaitFor(`window.tsPlayer.state().positionMs === 120000`, 3*time.Second)
	tab.Eval(`window.__castStub.fail(); true`, nil)
	tab.WaitFor(`window.tsPlayer.state().casting === ""`, 3*time.Second)
	tab.Eval(`window.tsPlayer.state().positionMs`, &pos)
	if pos != 120000 { // the second file starts at 90 s
		t.Errorf("place after the failure = %d", pos)
	}
	tab.Eval(`document.querySelector("#player-full [data-ts-full-toasts]").textContent`, &toast)
	if !strings.Contains(toast, "TV couldn't play this") {
		t.Errorf("toast = %q", toast)
	}
	var errs []string
	tab.Eval(`window.__errs`, &errs)
	if len(errs) != 0 {
		t.Errorf("page errors: %v", errs)
	}
}

// The row with the Cast button showing: every visible item is at least 44 px, no label is cut, and nothing overflows,
// at 390 and 768 px, in large text at 390 and 1440 px, and the desktop bar's button at 1440 px.
const castRowProbe = `(() => {
  const d = document.getElementById("player-full");
  for (const el of d.querySelectorAll("[data-ts-cast], [data-ts-full-read]")) el.hidden = false;
  const items = [...d.querySelectorAll(".ts-player-bar__item")].filter((el) => el.offsetParent !== null).map((el) => {
    const r = el.getBoundingClientRect(), l = el.querySelector(".ts-player-bar__label");
    return {W: r.width, H: r.height, Label: l.textContent.trim(), Cut: l.scrollWidth > l.clientWidth, Right: r.right};
  });
  return {Items: items, Vw: window.innerWidth, Overflow: document.documentElement.scrollWidth > window.innerWidth};
})()`

func TestPlayerCastRow(t *testing.T) {
	e := playerApp(t)
	castTurnOn(t, e)
	check := func(tab *cdp.Tab, what string) {
		t.Helper()
		var r struct {
			Items []struct {
				W, H, Right float64
				Label       string
				Cut         bool
			}
			Vw       float64
			Overflow bool
		}
		tab.Eval(castRowProbe, &r)
		sawCast := false
		for _, it := range r.Items {
			if it.W < 44 || it.H < 44 {
				t.Errorf("%s: %s is %.0fx%.0f px, want at least 44x44", what, it.Label, it.W, it.H)
			}
			if it.Cut {
				t.Errorf("%s: %s label is cut off (item %.0f px wide, %d items, viewport %.0f)", what, it.Label, it.W, len(r.Items), r.Vw)
			}
			if it.Right > r.Vw+0.5 {
				t.Errorf("%s: %s runs past the viewport", what, it.Label)
			}
			sawCast = sawCast || it.Label == "Cast"
		}
		if !sawCast {
			t.Errorf("%s: no item labelled Cast in %+v", what, r.Items)
		}
		if r.Overflow {
			t.Errorf("%s: the page overflows sideways", what)
		}
	}
	for _, w := range []int{390, 768} {
		tab := fullpOpen(t, e, w, 900, true)
		fullpOpenPlayer(t, tab)
		check(tab, "normal text")
		tab.Close()
	}
	ltSetSize(t, e.a, e.ownerID, true)
	defer ltSetSize(t, e.a, e.ownerID, false)
	for _, w := range []int{390, 1440} {
		tab := fullpOpen(t, e, w, 900, true)
		fullpOpenPlayer(t, tab)
		check(tab, "large text")
		tab.Close()
	}
}

// From 1280 px the side panel hides the row and the desktop bar carries the tools: its Cast button is a 44 px icon.
func TestPlayerCastDesktopBar(t *testing.T) {
	e := playerApp(t)
	castTurnOn(t, e)
	tab := fullpOpen(t, e, 1440, 900, true)
	var r struct{ W, H float64 }
	tab.Eval(`(() => { const b = document.querySelector(".ts-pbar__tools [data-ts-cast]"); b.hidden = false; const r = b.getBoundingClientRect(); return {W: r.width, H: r.height}; })()`, &r)
	if r.W < 44 || r.H < 44 {
		t.Errorf("the desktop bar's Cast button is %.0fx%.0f px, want at least 44x44", r.W, r.H)
	}
}
