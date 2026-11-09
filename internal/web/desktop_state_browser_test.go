//go:build dev

package web

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// deskStateStub stands in for the desktop window's message channel, so a browser can show what
// desktop-shell.js tells Go. Needs TENDSHELF_TEST_BROWSER (system Chromium); this is a Chromium page
// with a stub channel, not the real WebKit window.
const deskStateStub = `window.webkit = {messageHandlers: {external: {postMessage: (m) => { (window.__posts = window.__posts || []).push(m); }}}};`

// deskStatePosts returns the parsed "tendshelf:state" messages posted so far.
func deskStatePosts(t *testing.T, tab interface{ Eval(string, any) }) []map[string]any {
	t.Helper()
	var posts []string
	tab.Eval(`window.__posts || []`, &posts)
	var out []map[string]any
	for _, p := range posts {
		body, ok := strings.CutPrefix(p, "tendshelf:state ")
		if !ok {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(body), &m); err != nil {
			t.Fatalf("state %q is not JSON: %v", body, err)
		}
		out = append(out, m)
	}
	return out
}

// TestDeskStateBrowser plays a book near its end in the desktop shell and checks the states the page
// posts: the idle one with the theme, the playing one with the book, and the end of the book.
func TestDeskStateBrowser(t *testing.T) {
	e := playerAppMods(t, nil)
	e.store(t, 177_500, "deskstate01", "Other device", 0, false)
	b := chromium(t)
	tab := b.NewTab(1440, 900, false, true)
	_ = tab.Call("Page.enable", nil, nil)
	if err := tab.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": deskStateStub}, nil); err != nil {
		t.Fatal(err)
	}
	if err := tab.Call("Network.setCookie", map[string]any{"name": shellCookie, "value": "desktop", "url": e.URL}, nil); err != nil {
		t.Fatal(err)
	}
	tab.Navigate(e.URL + "/libraries")
	tab.WaitFor(`window.tsPlayer && window.tsPlayer.state().bookId !== "" && !document.getElementById("player").hidden`, 10*time.Second)
	tab.WaitFor(`(window.__posts || []).some((p) => p.includes('"title":"Harbour Lamp"'))`, 5*time.Second)

	first := deskStatePosts(t, tab)
	last := first[len(first)-1]
	if last["playing"] != false || last["stop"] != "" || last["title"] != "Harbour Lamp" {
		t.Errorf("paused book state = %v", last)
	}
	if th, _ := last["theme"].(string); th != "dark" && th != "light" && th != "sepia" && th != "contrast" {
		t.Errorf("theme = %q", th)
	}
	if last["canAdd"] != false {
		t.Errorf("canAdd = %v before the server says so", last["canAdd"])
	}
	// The mini-player's own line ("Chapter 6 · 1 min left") is the chapter; left is the bar's
	// book-wide text and may be empty until the bar variant has drawn.
	if s, _ := last["chapter"].(string); !strings.Contains(s, "Chapter") {
		t.Errorf("chapter = %q", s)
	}

	bmkeyPlay(t, tab, "[data-ts-toggle]")
	tab.WaitFor(`(window.__posts || []).some((p) => p.includes('"playing":true'))`, 5*time.Second)
	tab.WaitFor(`(window.__posts || []).some((p) => p.includes('"stop":"end"'))`, 15*time.Second)
	posts := deskStatePosts(t, tab)
	end := posts[len(posts)-1]
	if end["stop"] != "end" || end["playing"] != false {
		t.Errorf("end of the book: %v", end)
	}

	// Playing again clears the reason, and seconds of progress do not flood Go.
	var n int
	tab.Eval(`(window.__posts || []).length`, &n)
	if n > 20 {
		t.Errorf("%d state messages for a short play", n)
	}
}

// TestDeskStateBrowserSleep: when the sleep timer pauses the book, the state says "sleep"; a pause
// by hand says nothing.
func TestDeskStateBrowserSleep(t *testing.T) {
	e := playerAppMods(t, nil)
	e.store(t, 60_000, "deskstate02", "Other device", 0, false)
	b := chromium(t)
	tab := b.NewTab(1440, 900, false, true)
	_ = tab.Call("Page.enable", nil, nil)
	if err := tab.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": deskStateStub}, nil); err != nil {
		t.Fatal(err)
	}
	if err := tab.Call("Network.setCookie", map[string]any{"name": shellCookie, "value": "desktop", "url": e.URL}, nil); err != nil {
		t.Fatal(err)
	}
	tab.Navigate(e.URL + "/libraries")
	tab.WaitFor(`window.tsPlayer && window.tsPlayer.state().bookId !== "" && !document.getElementById("player").hidden && !!window.tsPlayerFull`, 10*time.Second)

	bmkeyPlay(t, tab, "[data-ts-toggle]")
	tab.WaitFor(`(window.__posts || []).some((p) => p.includes('"playing":true'))`, 5*time.Second)
	// A pause by hand: no reason.
	tab.Eval(`window.tsPlayer.pause()`, nil)
	tab.WaitFor(`(window.__posts || []).some((p) => p.includes('"playing":false') && p.includes('"title":"Harbour Lamp"'))`, 5*time.Second)
	posts := deskStatePosts(t, tab)
	if posts[len(posts)-1]["stop"] != "" {
		t.Errorf("a pause by hand carries a reason: %v", posts[len(posts)-1])
	}

	bmkeyPlay(t, tab, "[data-ts-toggle]")
	// The timer only stops a book that is playing, and on a busy runner the sound can take a while to start.
	tab.WaitFor(`window.tsPlayer.state().playing`, 10*time.Second)
	// The shell must already have said "playing" and seen the timer on: it reads the timer at each tick,
	// and on a busy runner a fixed wait can end before the next tick.
	tab.WaitFor(`(window.__posts || []).at(-1).includes('"playing":true')`, 10*time.Second)
	tab.Eval(`window.tsPlayerFull.sleep("minutes", 1);
	  window.__ticked = false;
	  document.addEventListener("ts:player:tick", () => { window.__ticked = true; }, { once: true });`, nil)
	tab.WaitFor(`window.__ticked`, 10*time.Second)
	tab.Eval(`window.tsPlayerFull.debug.advanceSleep(70000)`, nil)
	tab.WaitFor(`(window.__posts || []).some((p) => p.includes('"stop":"sleep"'))`, 5*time.Second)
}
