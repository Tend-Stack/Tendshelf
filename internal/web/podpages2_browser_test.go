//go:build dev

package web

import (
	"testing"
	"time"
)

type liveButtonState struct {
	Label     string `json:"label"`
	Playing   bool   `json:"playing"`
	PlayIcon  bool   `json:"playIcon"`
	PauseIcon bool   `json:"pauseIcon"`
	Session   string `json:"session"`
}

// TestPodPages2LiveButtonFollowsAudio: with a live station loaded the bar's round button shows Pause while the
// stream plays, Play after Stop (the stream is let go), and Pause again once Play joins live; its label,
// the bar's is-playing mark and the Media Session playbackState follow the audio element every time.
func TestPodPages2LiveButtonFollowsAudio(t *testing.T) {
	b := chromium(t)
	_, id, base := radioBrowserWorld(t, "Button FM")
	tab := b.NewTab(1440, 900, false, false)
	tab.Navigate(base + "/radio")
	tab.WaitFor(`window.tsPlayer !== undefined`, 10*time.Second)
	playerClickSel(t, tab, `[data-ts-play="`+id+`"]`)
	tab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().itemKind === "radio"`, 15*time.Second)

	read := func() (r liveButtonState) {
		tab.Eval(`(() => { const t = document.querySelector("#player [data-ts-toggle]");
		  const shown = (sel) => t.querySelector(sel).offsetWidth > 0;
		  return {label: t.getAttribute("aria-label"), playing: document.querySelector(".ts-player-slot").classList.contains("is-playing"),
		    playIcon: shown("[data-ts-icon-play]"), pauseIcon: shown("[data-ts-icon-pause]"), session: navigator.mediaSession.playbackState}; })()`, &r)
		return r
	}
	on := read()
	if !on.Playing || !on.PauseIcon || on.PlayIcon || on.Session != "playing" {
		t.Errorf("while playing: %+v", on)
	}
	tab.Eval(`window.tsPlayer.toggle()`, nil)
	tab.WaitFor(`!window.tsPlayer.state().playing`, 5*time.Second)
	time.Sleep(300 * time.Millisecond)
	off := read()
	if off.Playing || !off.PlayIcon || off.PauseIcon || off.Session != "paused" || off.Label == on.Label {
		t.Errorf("after stop: %+v (was %+v)", off, on)
	}
	tab.Eval(`window.tsPlayer.toggle()`, nil)
	tab.WaitFor(`window.tsPlayer.state().playing`, 15*time.Second)
	again := read()
	if !again.Playing || !again.PauseIcon || again.Label != on.Label {
		t.Errorf("after play: %+v", again)
	}
}
