//go:build dev

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/radio"
)

// radioBrowserWorld serves the app to the browser as Ben, with a fake live station behind the guarded client.
func radioBrowserWorld(t *testing.T, name string) (w *radioWorld, id, base string) {
	t.Helper()
	w = newRadioWorld(t)
	if _, _, err := radio.NewStore(w.a.st, nil).Save(t.Context(), w.benID, radio.Input{StreamURL: w.fx.pub.URL("radio.test", "/stream.mp3"), Name: name, Genre: "Talk", Country: "NL"}); err != nil {
		t.Fatal(err)
	}
	if err := w.a.st.Reader().QueryRow(`SELECT id FROM radio_stations WHERE name = ?`, name).Scan(&id); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		r.AddCookie(w.ben)
		w.a.h.ServeHTTP(rw, r)
	}))
	t.Cleanup(srv.Close)
	return w, id, srv.URL
}

// TestRadioLivePlayer: a station plays in the real player. The Live badge shows, there is no scrubber, skip or speed, the
// title playing now comes from the stream, the Media Session names the station and the title, a pause lets go of the stream
// and Play joins a new one at the live edge, and the sleep timer still stops it.
func TestRadioLivePlayer(t *testing.T) {
	b := chromium(t)
	w, id, base := radioBrowserWorld(t, "Harbour FM")
	tab := b.NewTab(1440, 900, false, false)
	tab.Navigate(base + "/radio")
	tab.WaitFor(`window.tsPlayer !== undefined`, 10*time.Second)
	playerClickSel(t, tab, `[data-ts-play="`+id+`"]`)
	tab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().itemKind === "radio"`, 15*time.Second)

	var badge struct {
		Hidden bool   `json:"hidden"`
		Text   string `json:"text"`
	}
	tab.Eval(`(() => { const b = document.querySelector("#player [data-ts-live]"); return {hidden: b.hidden, text: b.textContent}; })()`, &badge)
	if badge.Hidden || badge.Text != "Live" {
		t.Errorf("Live badge: %+v", badge)
	}

	// Nothing that needs a length or a place is drawn, in the bar or in the full player.
	fullpOpenPlayer(t, tab)
	var drawn []string
	tab.Eval(`(() => {
		const shown = (sel) => [...document.querySelectorAll(sel)].some((e) => e.offsetWidth > 0 || e.offsetHeight > 0);
		const out = [];
		for (const sel of ["#player [data-ts-back]", "#player [data-ts-forward]", "#player [data-ts-bar-speed]", "#player .ts-wave__input",
			"#player-full [data-ts-full-back]", "#player-full [data-ts-full-forward]", "#player-full [data-ts-full-prev]", "#player-full [data-ts-full-next]",
			"#player-full [data-ts-open-speed]", "#player-full .ts-scrub__input", "#player-full [data-ts-open-chapters]"]) {
			if (shown(sel)) out.push(sel);
		}
		return out;
	})()`, &drawn)
	if len(drawn) > 0 {
		t.Errorf("drawn in live mode: %v", drawn)
	}
	time.Sleep(700 * time.Millisecond) // the Now playing panel would have asked for a book page of the station and got a 404 toast
	var toasts int
	tab.Eval(`document.querySelectorAll(".ts-toast").length`, &toasts)
	if toasts != 0 {
		t.Errorf("%d toast(s) after opening the live player", toasts)
	}
	var chapterWord string
	tab.Eval(`document.querySelector("#player-full [data-ts-full-chapter]").textContent`, &chapterWord)
	if chapterWord != "Live" {
		t.Errorf("full player badge = %q", chapterWord)
	}
	var tools struct {
		Toggle bool `json:"toggle"`
		Sleep  bool `json:"sleep"`
	}
	tab.Eval(`(() => { const shown = (sel) => [...document.querySelectorAll(sel)].some((e) => e.offsetWidth > 0); return {toggle: shown("#player [data-ts-toggle]"), sleep: shown("#player [data-ts-bar-sleep]")}; })()`, &tools)
	if !tools.Toggle || !tools.Sleep {
		t.Errorf("play/pause and sleep must stay: %+v", tools)
	}
	// Keys and calls that would seek do nothing.
	tab.Eval(`window.tsPlayer.skip(1); window.tsPlayer.seek(60000); window.tsPlayer.setSpeed(2)`, nil)
	var speed float64
	tab.Eval(`window.tsPlayer.state().speed`, &speed)
	if speed != 1 || !playerStateOf(tab).Playing {
		t.Errorf("a live station changed with speed %v, playing %v", speed, playerStateOf(tab).Playing)
	}

	// The title playing now is polled and drawn; the Media Session carries the station and the title.
	tab.WaitFor(`document.querySelector("#player-full [data-ts-full-author]").textContent.includes("Ann - Song")`, 20*time.Second)
	var session struct {
		Title  string `json:"title"`
		Artist string `json:"artist"`
	}
	tab.Eval(`({title: navigator.mediaSession.metadata.title, artist: navigator.mediaSession.metadata.artist})`, &session)
	if !strings.HasPrefix(session.Title, "Ann - Song") || session.Artist != "Harbour FM" {
		t.Errorf("media session = %+v", session)
	}
	var meta string
	tab.Eval(`document.querySelector("#player [data-ts-meta]").textContent`, &meta)
	if !strings.HasPrefix(meta, "Ann - Song") {
		t.Errorf("bar meta = %q", meta)
	}

	// Pause lets go of the stream (the relay's connection closes) and Play connects again.
	first := w.fx.conns.Load()
	tab.Eval(`window.tsPlayer.pause()`, nil)
	tab.WaitFor(`!window.tsPlayer.state().playing`, 5*time.Second)
	waitFor(t, "the stream to be let go", func() bool { return w.fx.active.Load() == 0 })
	tab.Eval(`window.tsPlayer.play()`, nil)
	tab.WaitFor(`window.tsPlayer.state().playing`, 15*time.Second)
	waitFor(t, "a new connection", func() bool { return w.fx.conns.Load() > first && w.fx.active.Load() == 1 })

	// The sleep timer still stops the station.
	tab.Eval(`window.tsPlayerFull.sleep("minutes", 1);
	  window.__ticked = false;
	  document.addEventListener("ts:player:tick", () => { window.__ticked = true; }, { once: true });`, nil)
	tab.WaitFor(`window.__ticked`, 10*time.Second)
	tab.Eval(`window.tsPlayerFull.debug.advanceSleep(70000)`, nil)
	tab.WaitFor(`!window.tsPlayer.state().playing`, 10*time.Second)
	waitFor(t, "the stream to be let go after sleep", func() bool { return w.fx.active.Load() == 0 })
}

// TestRadioPlayerVisuals captures the Radio page, the search page, the bar and the full player in live mode at 390, 768, 1024
// and 1440 px with a long station name and title, and checks they are cut with an ellipsis and nothing overflows sideways.
func TestRadioPlayerVisuals(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	long := "Radio Harbour Lighthouse and Fog Signals, the Whole Night Through"
	w, id, base := radioBrowserWorld(t, long)
	_ = w
	for _, size := range [][2]int{{390, 844}, {768, 1024}, {1024, 768}, {1440, 900}} {
		width, height := size[0], size[1]
		t.Run(fmt.Sprint(width), func(t *testing.T) {
			tab := b.NewTab(width, height, width < 768, true)
			overflow := func(what string) {
				var over bool
				tab.Eval(`document.documentElement.scrollWidth > innerWidth + 1`, &over)
				if over {
					t.Errorf("%s: horizontal overflow", what)
				}
			}
			tab.Navigate(base + "/radio")
			tab.WaitFor(`window.tsPlayer !== undefined`, 10*time.Second)
			var rows struct {
				Cut     bool    `json:"cut"`
				Height  float64 `json:"height"`
				Targets []string
			}
			tab.Eval(`(() => {
				const t = document.querySelector(".ts-stnrow .ts-row__title");
				const small = [...document.querySelectorAll(".ts-stnrow a, .ts-stnrow button")].filter((e) => e.offsetWidth < 44 || e.offsetHeight < 44).map((e) => e.className);
				return {cut: t.scrollWidth > t.clientWidth, height: document.querySelector(".ts-stnrow").getBoundingClientRect().height, targets: small};
			})()`, &rows)
			if (!rows.Cut && width < 768) || rows.Height > 100 || len(rows.Targets) > 0 {
				t.Errorf("station row: %+v", rows)
			}
			overflow("radio page")
			tab.Screenshot(filepath.Join(out, fmt.Sprintf("radio-page-%d.png", width)))

			tab.Navigate(base + "/radio/search?q=cool")
			tab.WaitFor(`document.querySelector(".ts-podhit") !== null`, 10*time.Second)
			overflow("radio search")
			tab.Screenshot(filepath.Join(out, fmt.Sprintf("radio-search-%d.png", width)))

			tab.Navigate(base + "/radio")
			tab.WaitFor(`window.tsPlayer !== undefined`, 10*time.Second)
			playerClickSel(t, tab, `[data-ts-play="`+id+`"]`)
			tab.WaitFor(`window.tsPlayer.state().playing`, 15*time.Second)
			tab.WaitFor(`document.querySelector("#player [data-ts-meta]").textContent.includes("Ann - Song")`, 20*time.Second)
			time.Sleep(500 * time.Millisecond)
			var bar struct {
				TitleRows float64 `json:"titleRows"`
				MetaCut   bool    `json:"metaCut"`
			}
			tab.Eval(`(() => { const lh = (e) => parseFloat(getComputedStyle(e).lineHeight) || 20;
				const t = document.querySelector("#player [data-ts-title]"), m = document.querySelector("#player [data-ts-meta]");
				return {titleRows: t.getBoundingClientRect().height / lh(t), metaCut: m.getBoundingClientRect().height <= lh(m) + 1}; })()`, &bar)
			if bar.TitleRows > 1.1 || !bar.MetaCut {
				t.Errorf("bar text wraps: %+v", bar)
			}
			overflow("bar")
			tab.Screenshot(filepath.Join(out, fmt.Sprintf("radio-bar-%d.png", width)))
			fullpOpenPlayer(t, tab)
			if width < 1280 {
				// below 1280 px the full player is the whole screen: play and sleep stay, everything that needs a length is gone
				var seen map[string]bool
				tab.Eval(`(() => { const shown = (sel) => [...document.querySelectorAll(sel)].some((e) => e.offsetWidth > 0 && e.offsetHeight > 0);
					return {toggle: shown("#player-full [data-ts-full-toggle]"), sleep: shown("#player-full [data-ts-open-sleep]"),
					back: shown("#player-full [data-ts-full-back]"), forward: shown("#player-full [data-ts-full-forward]"), prev: shown("#player-full [data-ts-full-prev]"),
					speed: shown("#player-full [data-ts-open-speed]"), scrub: shown("#player-full .ts-scrub__input"), chapters: shown("#player-full [data-ts-open-chapters]")}; })()`, &seen)
				if !seen["toggle"] || !seen["sleep"] || seen["back"] || seen["forward"] || seen["prev"] || seen["speed"] || seen["scrub"] || seen["chapters"] {
					t.Errorf("full player controls in live mode: %v", seen)
				}
			}
			overflow("full player")
			tab.Screenshot(filepath.Join(out, fmt.Sprintf("radio-full-%d.png", width)))
			tab.Eval(`window.tsPlayer.pause()`, nil)
		})
	}
}
