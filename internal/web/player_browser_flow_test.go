//go:build dev

package web

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/mediajobs"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
	"github.com/Tend-Stack/tendshelf/internal/testutil/wav"
)

// playerKey sends a real key press (down and up) to the page.
func playerKey(t *testing.T, tab *cdp.Tab, key string) {
	t.Helper()
	vk := map[string]int{" ": 32, "ArrowLeft": 37, "ArrowRight": 39, "Escape": 27, "?": 191, "/": 191, "g": 71, "l": 76, "h": 72}[key]
	code := map[string]string{" ": "Space", "ArrowLeft": "ArrowLeft", "ArrowRight": "ArrowRight", "Escape": "Escape"}[key]
	down := map[string]any{"type": "keyDown", "key": key, "code": code, "windowsVirtualKeyCode": vk}
	if len(key) == 1 {
		down["text"] = key
	}
	if err := tab.Call("Input.dispatchKeyEvent", down, nil); err != nil {
		t.Fatal(err)
	}
	if err := tab.Call("Input.dispatchKeyEvent", map[string]any{"type": "keyUp", "key": key, "code": code, "windowsVirtualKeyCode": vk}, nil); err != nil {
		t.Fatal(err)
	}
}

// playerDropLink removes the injected play link and waits until the player is usable again (the
// link for the loaded book hides it while it is on screen).
func playerDropLink(t *testing.T, tab *cdp.Tab) {
	t.Helper()
	tab.Eval(`document.getElementById("play-link").remove()`, nil)
	tab.WaitFor(`!document.getElementById("player").inert && !document.getElementById("player").hidden`, 5*time.Second)
	time.Sleep(450 * time.Millisecond) // the bar slides back in over 250 ms: click it once it rests
}

// playerSavedCounter counts progress-saved events in window.__saved.
func playerSavedCounter(tab *cdp.Tab) {
	tab.Eval(`(() => { window.__saved = 0; document.body.addEventListener("progress-saved", (e) => { window.__saved++; window.__lastSaved = e.detail; }); })()`, nil)
}

// playerToastText is the text of the first toast (its own text, without the buttons).
func playerToastText(tab *cdp.Tab) string {
	var s string
	tab.Eval(`(() => { const t = document.querySelector("#toasts .ts-toast"); return t && t.firstChild ? t.firstChild.textContent : ""; })()`, &s)
	return s
}

func TestPlayerBrowserResume(t *testing.T) {
	e := playerApp(t)
	tabA := playerOpen(t, chromium(t), e.URL, 390, 800)
	playerSavedCounter(tabA)
	e.playerInject(tabA, e.book.ID, 0, 0, "")
	playerClickSel(t, tabA, "#play-link")
	tabA.WaitFor(`window.tsPlayer.state().positionMs >= 5000`, 12*time.Second)
	playerDropLink(t, tabA)
	playerClickSel(t, tabA, "[data-ts-toggle]")
	tabA.WaitFor(`!window.tsPlayer.state().playing && window.__saved >= 1`, 5*time.Second)
	posA := playerStateOf(tabA).PositionMs

	tabB := playerOpen(t, chromium(t), e.URL, 390, 800) // a second browser: its own storage and device id
	tabB.WaitFor(`!document.getElementById("player").hidden && window.tsPlayer.state().bookId !== ""`, 8*time.Second)
	posB := playerStateOf(tabB).PositionMs
	if posB < posA-1000 || posB > posA+1000 {
		t.Fatalf("B restored at %d, A paused at %d", posB, posA)
	}
	if s := playerStateOf(tabB); s.Playing {
		t.Error("B started playing by itself")
	}
	playerClickSel(t, tabB, "[data-ts-toggle]")
	tabB.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().positionMs > `+strconv.FormatInt(posB, 10)+`+500`, 8*time.Second)
	if p := playerStateOf(tabB).PositionMs; p < posB || p > posB+4000 {
		t.Errorf("B resumed at %d from %d (no rewind expected after a short break)", p, posB)
	}
}

func TestPlayerBrowserContinueThere(t *testing.T) {
	e := playerApp(t)
	e.store(t, 10_000, "sweepfirstdev", "First device", time.Now().UnixMilli()-5000, false)
	tab := playerOpen(t, chromium(t), e.URL, 390, 800)
	tab.WaitFor(`window.tsPlayer.state().bookId !== ""`, 8*time.Second)
	if p := playerStateOf(tab).PositionMs; p < 9000 || p > 11000 {
		t.Fatalf("restored at %d, want about 10000", p)
	}
	playerClickSel(t, tab, "[data-ts-toggle]")
	tab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().positionMs > 11500`, 8*time.Second)
	playerClickSel(t, tab, "[data-ts-toggle]")
	tab.WaitFor(`!window.tsPlayer.state().playing`, 5*time.Second)
	waitPosts := func(n int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for len(e.spy.posts()) < n && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
	}
	waitPosts(1)

	// Another device saves 80 s; here, after a 61 s break, play asks.
	e.store(t, 80_000, "sweepotherdev", "Laptop test", time.Now().UnixMilli()+500, false)
	tab.Eval(`window.tsPlayer.debug.setPausedAt(Date.now() - 61000)`, nil)
	playerClickSel(t, tab, "[data-ts-toggle]")
	want := "You were at 1:20 on Laptop test. Continue there?"
	tab.WaitFor(`document.querySelector("#toasts .ts-toast") && [...document.querySelectorAll("#toasts .ts-toast")].some(t => t.firstChild && t.firstChild.textContent === `+cdp.Quote(want)+`)`, 8*time.Second)

	// "Continue there": the position goes to the other device's.
	tab.Eval(`[...document.querySelectorAll("#toasts .ts-toast button")].find(b => b.textContent === "Continue there").click()`, nil)
	tab.WaitFor(`Math.abs(window.tsPlayer.state().positionMs - 80000) < 2500`, 5*time.Second)

	// "Stay here": the next save forces this device's place. Save as before, then ask again.
	tab.Eval(`window.tsPlayer.pause()`, nil)
	tab.WaitFor(`!window.tsPlayer.state().playing`, 5*time.Second)
	waitPosts(2)
	e.store(t, 150_000, "sweepotherdev", "Laptop test", time.Now().UnixMilli()+500, false)
	before := len(e.spy.posts())
	tab.Eval(`window.tsPlayer.debug.setPausedAt(Date.now() - 61000); window.tsPlayer.play()`, nil)
	tab.WaitFor(`[...document.querySelectorAll("#toasts .ts-toast button")].some(b => b.textContent === "Stay here")`, 8*time.Second)
	tab.Eval(`[...document.querySelectorAll("#toasts .ts-toast button")].find(b => b.textContent === "Stay here").click()`, nil)
	deadline := time.Now().Add(5 * time.Second)
	var forced bool
	for time.Now().Before(deadline) && !forced {
		for _, p := range e.spy.posts()[before:] {
			v, _ := url.ParseQuery(p.Body)
			forced = forced || v.Get("force") == "1"
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !forced {
		t.Error("Stay here did not send force=1")
	}
	if n := playerStateOf(tab).PositionMs; n > 100_000 {
		t.Errorf("Stay here moved the position to %d", n)
	}
}

func TestPlayerBrowserBeacon(t *testing.T) {
	e := playerApp(t)
	tab := playerOpen(t, chromium(t), e.URL, 390, 800)
	e.playerInject(tab, e.book.ID, 0, 0, "")
	playerClickSel(t, tab, "#play-link")
	tab.WaitFor(`window.tsPlayer.state().positionMs >= 3000`, 10*time.Second)
	last := playerStateOf(tab).PositionMs
	before := len(e.spy.posts())
	if err := tab.Call("Page.navigate", map[string]any{"url": "about:blank"}, nil); err != nil {
		t.Fatal(err)
	}
	var got playerReq
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) && got.Method == "" {
		for _, p := range e.spy.posts()[before:] {
			if p.CSRFHeader == "" {
				got = p
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	if got.Method == "" {
		t.Fatal("no beacon arrived after leaving the page")
	}
	if !strings.HasPrefix(got.ContentType, "application/x-www-form-urlencoded") {
		t.Errorf("beacon content type %q", got.ContentType)
	}
	v, _ := url.ParseQuery(got.Body)
	if v.Get("_csrf") == "" || v.Get("position_ms") == "" || v.Get("device_id") == "" {
		t.Errorf("beacon body %q lacks _csrf, position_ms or device_id", got.Body)
	}
	time.Sleep(300 * time.Millisecond)
	p, err := e.a.srv.progress.Get(context.Background(), e.ownerID, e.book.ID)
	if err != nil {
		t.Fatal(err)
	}
	if p.PositionMs < last-200 || p.PositionMs > last+1500 {
		t.Errorf("stored %d, last played %d", p.PositionMs, last)
	}
}

func TestPlayerBrowserSaves(t *testing.T) {
	e := playerApp(t)
	tab := playerOpen(t, chromium(t), e.URL, 390, 800)
	e.playerInject(tab, e.book.ID, 0, 0, "")
	playerClickSel(t, tab, "#play-link")
	tab.WaitFor(`window.tsPlayer.state().playing`, 10*time.Second)
	// Two periodic saves, about ten seconds apart; the second one carries about ten seconds heard.
	deadline := time.Now().Add(30 * time.Second)
	for len(e.spy.posts()) < 2 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	posts := e.spy.posts()
	if len(posts) < 2 {
		t.Fatalf("%d saves in 30 s, want 2", len(posts))
	}
	if gap := posts[1].At.Sub(posts[0].At); gap < 9*time.Second || gap > 11500*time.Millisecond {
		t.Errorf("saves %v apart, want about 10 s", gap)
	}
	for i, p := range posts[:2] {
		v, _ := url.ParseQuery(p.Body)
		if p.CSRFHeader == "" || !strings.HasPrefix(p.ContentType, "application/x-www-form-urlencoded") {
			t.Errorf("save %d: CSRF header %q, content type %q", i, p.CSRFHeader, p.ContentType)
		}
		if sp, _ := strconv.ParseFloat(v.Get("speed"), 64); sp != 1 {
			t.Errorf("save %d: speed %q", i, v.Get("speed"))
		}
	}
	v, _ := url.ParseQuery(posts[1].Body)
	if l, _ := strconv.Atoi(v.Get("listened_ms")); l < 8500 || l > 11500 {
		t.Errorf("second save listened_ms %d, want about 10000", l)
	}
	playerDropLink(t, tab)
	n := len(e.spy.posts())
	playerClickSel(t, tab, "[data-ts-toggle]")
	deadline = time.Now().Add(5 * time.Second)
	for len(e.spy.posts()) <= n && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	if len(e.spy.posts()) <= n {
		t.Fatal("pause sent no save")
	}
	time.Sleep(400 * time.Millisecond)
	var sessions int
	if err := e.a.st.Reader().QueryRow(`SELECT COUNT(*) FROM listening_sessions WHERE user_id = ?`, e.ownerID).Scan(&sessions); err != nil || sessions == 0 {
		t.Errorf("listening_sessions rows %d, %v", sessions, err)
	}
}

func TestPlayerBrowserSignedOut(t *testing.T) {
	e := playerApp(t)
	tab := playerOpen(t, chromium(t), e.URL, 390, 800)
	e.playerInject(tab, e.book.ID, 0, 0, "")
	playerClickSel(t, tab, "#play-link")
	tab.WaitFor(`window.tsPlayer.state().positionMs >= 1500`, 10*time.Second)
	playerDropLink(t, tab)
	e.spy.setSignedIn(false)
	playerClickSel(t, tab, "[data-ts-toggle]") // pause: the save is redirected to the sign-in page
	tab.WaitFor(`document.querySelector("#toasts .ts-toast")`, 8*time.Second)
	if got := playerToastText(tab); got != msgSignedOut {
		t.Errorf("toast %q, want %q", got, msgSignedOut)
	}
	n := len(e.spy.posts())
	playerClickSel(t, tab, "[data-ts-toggle]") // play on for 25 s: no more saves
	time.Sleep(25 * time.Second)
	if m := len(e.spy.posts()); m != n {
		t.Errorf("%d more saves after signing out", m-n)
	}
	var toasts int
	tab.Eval(`document.querySelectorAll("#toasts .ts-toast").length`, &toasts)
	if toasts != 1 {
		t.Errorf("%d toasts, want 1", toasts)
	}
}

func TestPlayerBrowserReloadAndRewind(t *testing.T) {
	e := playerApp(t)
	tab := playerOpen(t, chromium(t), e.URL, 390, 800)
	e.playerInject(tab, e.book.ID, 20_000, 0, "")
	playerClickSel(t, tab, "#play-link")
	tab.WaitFor(`window.tsPlayer.state().positionMs >= 22500`, 10*time.Second)
	playerDropLink(t, tab)
	tab.Eval(`window.__saved = 0; document.body.addEventListener("progress-saved", () => window.__saved++)`, nil)
	playerClickSel(t, tab, "[data-ts-toggle]")
	tab.WaitFor(`window.__saved >= 1`, 5*time.Second)
	at := playerStateOf(tab).PositionMs

	if err := tab.Call("Page.reload", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	tab.WaitFor(`window.tsPlayer && window.tsPlayer.state().bookId !== ""`, 10*time.Second)
	s := playerStateOf(tab)
	if s.Playing || s.PositionMs < at-1000 || s.PositionMs > at+1000 {
		t.Errorf("after reload: %+v, paused at %d", s, at)
	}

	// A row two minutes old: playing again rewinds 5 s and says so.
	streamExec(t, e.a, `UPDATE progress SET position_ms = 60000, client_updated_at = ?, device_id = 'sweepolderdev', device_label = 'Old phone' WHERE user_id = ? AND book_id = ?`,
		time.Now().UnixMilli()-120_000, e.ownerID, e.book.ID)
	if err := tab.Call("Page.reload", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	tab.WaitFor(`window.tsPlayer && window.tsPlayer.state().bookId !== "" && Math.abs(window.tsPlayer.state().positionMs - 60000) < 1000`, 10*time.Second)
	playerClickSel(t, tab, "[data-ts-toggle]")
	tab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().positionMs > 55000`, 8*time.Second)
	if p := playerStateOf(tab).PositionMs; p < 54_500 || p > 58_500 {
		t.Errorf("after the rewind: %d, want about 55000", p)
	}
	var hint string
	tab.Eval(`(document.querySelector("#toasts .ts-toast--hint") || {textContent: ""}).textContent`, &hint)
	if hint != msgRewound {
		t.Errorf("hint %q, want %q", hint, msgRewound)
	}
}

func TestPlayerBrowserCompat(t *testing.T) {
	e := playerApp(t)
	// The first file is one Chromium cannot play (an unknown container), so the player asks for the copy.
	e.setCodec(t, 0, "asf", "wmav2")
	tab := playerOpen(t, chromium(t), e.URL, 390, 800)
	e.playerInject(tab, e.book.ID, 0, 0, `a.dataset.tsMime = "application/octet-stream"; a.dataset.tsMayNeedCompat = "1";`)
	playerClickSel(t, tab, "#play-link")
	waitHeads := func(n int) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for e.spy.count("HEAD", "compat=1") < n && time.Now().Before(deadline) {
			time.Sleep(100 * time.Millisecond)
		}
		if e.spy.count("HEAD", "compat=1") < n {
			t.Fatalf("%d compat HEADs, want %d", e.spy.count("HEAD", "compat=1"), n)
		}
	}
	waitHeads(2) // two 503s, three seconds apart
	tab.WaitFor(`[...document.querySelectorAll("#toasts .ts-toast--hint")].some(t => t.textContent === `+cdp.Quote(compatWaitText)+`)`, 5*time.Second)
	if e.enq.count() != 1 {
		t.Errorf("%d conversion jobs queued, want 1", e.enq.count())
	}
	// The copy appears (the real worker is not running: the test writes the file).
	dir := mediajobs.CompatDir(e.a.dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, e.files[0].QuickHash+"-r"+strconv.Itoa(media.CompatRecipe)+".m4a"), wav.Bytes(60_000, 440), 0o644); err != nil {
		t.Fatal(err)
	}
	tab.WaitFor(`document.getElementById("audio").currentSrc.includes("compat=1")`, 10*time.Second)
	// Chromium sniffs the WAV bytes served as audio/mp4 and plays them.
	tab.WaitFor(`window.tsPlayer.state().positionMs > 1000`, 8*time.Second)
	var st struct {
		Paused bool    `json:"p"`
		T      float64 `json:"t"`
		Err    int     `json:"e"`
	}
	tab.Eval(`(() => { const a = document.getElementById("audio"); return {p: a.paused, t: a.currentTime, e: a.error ? a.error.code : 0}; })()`, &st)
	if st.Paused || st.T < 1 || st.Err != 0 {
		t.Errorf("compat source: paused %v, currentTime %.2f, media error %d", st.Paused, st.T, st.Err)
	}
}

func TestPlayerBrowserOneResume(t *testing.T) {
	e := playerApp(t)
	e.store(t, 10_000, "sweepfirstdev", "First device", 0, false)
	tab := playerOpen(t, chromium(t), e.URL, 390, 800)
	tab.WaitFor(`window.tsPlayer.state().bookId !== "" && !document.getElementById("player").hidden`, 8*time.Second)
	slot := func() (suppressed, inert bool) {
		var r struct{ S, I bool }
		tab.Eval(`({S: document.getElementById("player").classList.contains("is-suppressed"), I: document.getElementById("player").inert})`, &r)
		return r.S, r.I
	}
	if s, i := slot(); s || i {
		t.Fatalf("suppressed %v inert %v before any control", s, i)
	}
	rescan := `document.dispatchEvent(new Event("htmx:after:settle"))`
	// A control for another book does not hide it.
	e.playerInject(tab, "bk_other", 0, 0, "")
	tab.Eval(rescan, nil)
	time.Sleep(400 * time.Millisecond)
	if s, i := slot(); s || i {
		t.Errorf("a control for another book hid the player (%v %v)", s, i)
	}
	tab.Eval(`document.getElementById("play-link").remove()`, nil)
	// The loaded book's control on screen: suppressed and inert; scrolled out: shown again.
	e.playerInject(tab, e.book.ID, 0, 0, "")
	tab.Eval(rescan, nil)
	tab.WaitFor(`document.getElementById("player").classList.contains("is-suppressed") && document.getElementById("player").inert`, 5*time.Second)
	tab.Eval(`document.getElementById("play-link").style.cssText = "position:absolute;left:0;top:5000px;width:100px;height:40px"`, nil)
	tab.WaitFor(`!document.getElementById("player").classList.contains("is-suppressed") && !document.getElementById("player").inert`, 5*time.Second)
}

func TestPlayerBrowserKeysAndSession(t *testing.T) {
	e := playerApp(t)
	e.store(t, 10_000, "sweepfirstdev", "First device", 0, false)
	tab := playerOpen(t, chromium(t), e.URL, 1440, 900)
	tab.WaitFor(`window.tsPlayer.state().bookId !== "" && !document.getElementById("player").hidden`, 8*time.Second)

	// Space plays and pauses.
	playerKey(t, tab, " ")
	tab.WaitFor(`window.tsPlayer.state().playing`, 6*time.Second)
	playerKey(t, tab, " ")
	tab.WaitFor(`!window.tsPlayer.state().playing`, 5*time.Second)
	// The arrows skip by the settings (30 s forward, 15 s back).
	p0 := playerStateOf(tab).PositionMs
	playerKey(t, tab, "ArrowRight")
	tab.WaitFor(`Math.abs(window.tsPlayer.state().positionMs - `+strconv.FormatInt(p0+30_000, 10)+`) < 400`, 5*time.Second)
	playerKey(t, tab, "ArrowLeft")
	tab.WaitFor(`Math.abs(window.tsPlayer.state().positionMs - `+strconv.FormatInt(p0+15_000, 10)+`) < 400`, 5*time.Second)
	// Keys inside a field do nothing.
	tab.Eval(`(() => { const i = document.createElement("input"); i.id = "probe"; document.getElementById("main").append(i); i.focus(); })()`, nil)
	playerKey(t, tab, " ")
	playerKey(t, tab, "ArrowRight")
	time.Sleep(400 * time.Millisecond)
	if s := playerStateOf(tab); s.Playing || s.PositionMs > p0+16_000 {
		t.Errorf("a key in a field moved the player: %+v", s)
	}
	tab.Eval(`document.getElementById("probe").remove(); document.activeElement.blur()`, nil)

	// ? opens the shortcuts sheet, Escape closes it; g then l goes to the libraries.
	playerKey(t, tab, "?")
	tab.WaitFor(`document.getElementById("keys-help").open`, 3*time.Second)
	playerKey(t, tab, " ") // a key while a dialog is open is ignored by the player
	time.Sleep(300 * time.Millisecond)
	if playerStateOf(tab).Playing {
		t.Error("Space played while the sheet was open")
	}
	playerKey(t, tab, "Escape")
	tab.WaitFor(`!document.getElementById("keys-help").open`, 3*time.Second)
	playerKey(t, tab, "g")
	playerKey(t, tab, "l")
	tab.WaitFor(`location.pathname === "/libraries"`, 6*time.Second)
	tab.WaitFor(`window.tsPlayer.state().bookId !== ""`, 3*time.Second) // the player survived the boosted navigation

	// Media Session.
	var ms struct {
		Title, Artist, Album, Art string
		Actions                   []string
	}
	tab.Eval(`({Title: navigator.mediaSession.metadata.title, Artist: navigator.mediaSession.metadata.artist, Album: navigator.mediaSession.metadata.album,
	  Art: navigator.mediaSession.metadata.artwork[0].src, Actions: window.tsPlayer.debug.actions})`, &ms)
	if ms.Title != "Harbour Lamp" || ms.Artist != "Imogen Hale" || !strings.HasPrefix(ms.Album, "Chapter ") || !strings.Contains(ms.Art, "/covers/"+e.book.ID+"?w=512") {
		t.Errorf("media session %+v", ms)
	}
	if len(ms.Actions) != 8 {
		t.Errorf("media session actions %v, want the 8 handlers", ms.Actions)
	}
	// The timeline across the file boundary and the chapter targets.
	var loc []struct{ F, O int }
	tab.Eval(`[0, 89999, 90000, 179999].map((ms) => { const t = window.tsPlayer.debug.locate(ms); return {F: t.fileIdx, O: t.offsetMs}; })`, &loc)
	for i, want := range [][2]int{{0, 0}, {0, 89999}, {1, 0}, {1, 89999}} {
		if loc[i].F != want[0] || loc[i].O != want[1] {
			t.Errorf("locate row %d = %+v, want %v", i, loc[i], want)
		}
	}
	var ci []int
	tab.Eval(`[0, 29999, 30000, 150000, 179999].map((ms) => window.tsPlayer.debug.chapterIdx(ms))`, &ci)
	if fmt.Sprint(ci) != "[0 0 1 5 5]" {
		t.Errorf("chapter indexes %v", ci)
	}
	tab.Eval(`window.tsPlayer.seek(65000); window.tsPlayer.chapter(1)`, nil)
	tab.WaitFor(`Math.abs(window.tsPlayer.state().positionMs - 90000) < 400`, 4*time.Second)
	tab.Eval(`window.tsPlayer.chapter(-1)`, nil) // more than 3 s into chapter 4 is not true here: it starts at 90000 exactly
	tab.WaitFor(`Math.abs(window.tsPlayer.state().positionMs - 60000) < 400`, 4*time.Second)
}

func TestPlayerBrowserLayout(t *testing.T) {
	e := playerApp(t)
	e.store(t, 100_000, "sweepfirstdev", "First device", 0, false)
	b := chromium(t)
	out := playerOut(t)
	widths := []int{390, 768, 1024, 1440}
	themesToCheck := themes
	if !visualFull() {
		themesToCheck = []string{"dark", "light"}
	}
	for _, w := range widths {
		tab := b.NewTab(w, 900, w < 768, false)
		tab.Navigate(e.URL + "/libraries") // not Home: its hero would hide the bar
		tab.WaitFor(`window.tsPlayer && window.tsPlayer.state().bookId !== "" && !document.getElementById("player").hidden`, 10*time.Second)
		for _, theme := range themesToCheck {
			tab.Eval(`document.documentElement.setAttribute("data-theme", `+cdp.Quote(theme)+`)`, nil)
			time.Sleep(150 * time.Millisecond)
			var r struct {
				Over          bool
				Mini, Tab     [4]float64
				Small         []string
				Labels        map[string]string
				ForwardShown  bool
				VH, CW        float64
				SlotLeft      float64
				ProgressInset string
			}
			tab.Eval(`(() => {
			  const box = (el) => { const r = el.getBoundingClientRect(); return [r.left, r.top, r.right, r.bottom]; };
			  const small = [];
			  // The right edge fixed elements see: headless Chromium reserves a 15 px scrollbar lane for them at some widths.
			  const fixedRight = () => { const p = document.createElement("div"); p.style.cssText = "position:fixed;right:0;top:0;width:1px;height:1px"; document.body.append(p); const r = p.getBoundingClientRect().right; p.remove(); return r; };
			  for (const el of document.querySelectorAll("#player button, #player a")) {
			    if (!el.offsetParent) continue;
			    const r = el.getBoundingClientRect();
			    if (r.width < 43.5 || r.height < 43.5) small.push(el.className + ":" + Math.round(r.width) + "x" + Math.round(r.height));
			  }
			  const tabbar = document.getElementById("nav-tabbar");
			  return {Over: document.documentElement.scrollWidth > innerWidth, Mini: box(document.getElementById("mini-player")),
			    Tab: getComputedStyle(tabbar).display !== "none" ? box(tabbar) : [0, 0, 0, 0], CW: fixedRight(), Small: small, VH: innerHeight,
			    SlotLeft: document.getElementById("player").getBoundingClientRect().left,
			    ForwardShown: !!document.querySelector("[data-ts-forward]").offsetParent,
			    Labels: {toggle: document.querySelector("[data-ts-toggle]").getAttribute("aria-label"), back: document.querySelector("[data-ts-back]").getAttribute("aria-label"),
			      forward: document.querySelector("[data-ts-forward]").getAttribute("aria-label"), open: document.querySelector("[data-ts-open]").getAttribute("aria-label"),
			      meta: document.querySelector("[data-ts-meta]").textContent}};
			})()`, &r)
			name := fmt.Sprintf("%s/layout-%s-%d", out, theme, w)
			if r.Over {
				t.Errorf("%s: horizontal overflow", name)
			}
			if len(r.Small) > 0 {
				t.Errorf("%s: controls under 44 px: %v", name, r.Small)
			}
			if r.Labels["toggle"] != "Play Harbour Lamp" || r.Labels["back"] != "Back 15 seconds" || r.Labels["forward"] != "Forward 30 seconds" {
				t.Errorf("%s: labels %+v", name, r.Labels)
			}
			if r.Labels["open"] != "Open player: Harbour Lamp, chapter 4" || r.Labels["meta"] != "Chapter 4 · 1 min left" {
				t.Errorf("%s: open %q, meta %q", name, r.Labels["open"], r.Labels["meta"])
			}
			near := func(a, b float64) bool { return a-b < 1.5 && b-a < 1.5 }
			switch {
			case w < 768: // floats 8 px above the tab bar, 8 px side insets
				if !near(r.Mini[0], 8) || !near(r.Mini[2], r.CW-8) || !near(r.Mini[3], r.Tab[1]-8) || r.Mini[3] > r.Tab[1] {
					t.Errorf("%s: mini %v, tab bar %v", name, r.Mini, r.Tab)
				}
			case w < 1280: // docked at the bottom of the content area, beside the rail
				if !near(r.Mini[0], 96) || !near(r.Mini[2], r.CW-8) || !near(r.Mini[3], r.VH-8) {
					t.Errorf("%s: mini %v, client width %v, height %v", name, r.Mini, r.CW, r.VH)
				}
			default: // a full-width bar at the bottom of the window
				if !near(r.Mini[0], 0) || !near(r.Mini[2], r.CW) || !near(r.Mini[3], r.VH) || !r.ForwardShown {
					t.Errorf("%s: mini %v, forward shown %v", name, r.Mini, r.ForwardShown)
				}
			}
			if w < 1280 && r.ForwardShown {
				t.Errorf("%s: Forward is shown below 1280 px", name)
			}
			if (theme == "dark" || theme == "light") || visualFull() {
				tab.Screenshot(name + ".png")
			}
		}
	}
}
