//go:build dev

package web

import (
	"context"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The chapter bar (chapter-bar.js): hover, focus and press name a chapter above the book ruler; a
// click, a tap or Enter goes to its start. Fixture: the full player's (six chapters of 30 s, "Chapter 1"
// to "Chapter 6", stored place 100 s). Run by make player.

const (
	chbarFull = "#player-full [data-ts-ruler]"
	chbarTip  = " .ts-chtip"
)

// chbarPoint is the viewport point at fraction f along the first element matching sel.
func chbarPoint(tab *cdp.Tab, sel string, f float64) (x, y float64) {
	var p struct{ X, Y float64 }
	tab.Eval(`(() => { const r = document.querySelector(`+cdp.Quote(sel)+`).getBoundingClientRect(); return {X: r.x + r.width * `+cdpFloat(f)+`, Y: r.y + r.height / 2}; })()`, &p)
	return p.X, p.Y
}

func cdpFloat(f float64) string { return strconv.FormatFloat(f, 'f', 3, 64) }

func chbarMove(t *testing.T, tab *cdp.Tab, x, y float64) {
	t.Helper()
	if err := tab.Call("Input.dispatchMouseEvent", map[string]any{"type": "mouseMoved", "x": x, "y": y}, nil); err != nil {
		t.Fatal(err)
	}
}

// chbarTipText is the visible tip's text, or "" when it is not shown.
func chbarTipText(tab *cdp.Tab, bar string) string {
	var s string
	tab.Eval(`(() => { const t = document.querySelector(`+cdp.Quote(bar+chbarTip)+`); return t && getComputedStyle(t).display !== "none" ? t.textContent : ""; })()`, &s)
	return s
}

// chbarTipBox is where the tip sits and whether its title is cut with an ellipsis.
type chbarBox struct {
	Left, Right, Width float64
	Cut                bool
}

func chbarTipBox(tab *cdp.Tab, bar string) chbarBox {
	var b chbarBox
	tab.Eval(`(() => { const t = document.querySelector(`+cdp.Quote(bar+chbarTip)+`); const r = t.getBoundingClientRect(); const h = t.querySelector(".ts-chtip__title");
	  return {Left: r.left, Right: r.right, Width: innerWidth, Cut: h.scrollWidth > h.clientWidth}; })()`, &b)
	return b
}

func chbarPlayerTab(t *testing.T, e *playerEnv, w, h int) *cdp.Tab {
	t.Helper()
	tab := fullpOpen(t, e, w, h, false)
	fullpOpenPlayer(t, tab)
	return tab
}

func TestChapterBarHoverNamesTheChapter(t *testing.T) {
	e := playerApp(t)
	tab := chbarPlayerTab(t, e, 1440, 900) // the side player
	var role string
	tab.Eval(`document.querySelector("`+chbarFull+`").getAttribute("role")`, &role)
	if role != "slider" {
		t.Fatalf("role %q, want slider", role)
	}
	x, y := chbarPoint(tab, chbarFull, 0.55) // 99 s: chapter 4, which starts at 1:30
	chbarMove(t, tab, x, y)
	tab.WaitFor(`document.querySelector("`+chbarFull+chbarTip+`") && getComputedStyle(document.querySelector("`+chbarFull+chbarTip+`")).display !== "none"`, 3*time.Second)
	if got := chbarTipText(tab, chbarFull); got != "Chapter 4 · 1:30" {
		t.Errorf("tip %q, want %q", got, "Chapter 4 · 1:30")
	}
	x, y = chbarPoint(tab, chbarFull, 0.05)
	chbarMove(t, tab, x, y)
	tab.WaitFor(`document.querySelector("`+chbarFull+chbarTip+`").textContent.startsWith("Chapter 1")`, 2*time.Second)
	b := chbarTipBox(tab, chbarFull)
	if b.Left < 0 || b.Right > b.Width {
		t.Errorf("tip leaves the window: %+v", b)
	}
	chbarMove(t, tab, 5, 5)
	tab.WaitFor(`getComputedStyle(document.querySelector("`+chbarFull+chbarTip+`")).display === "none"`, 2*time.Second)
	// the target is 44 px tall though the line is thin, and the layout did not grow
	var h struct{ Target, Layout, Line float64 }
	tab.Eval(`(() => { const e = document.querySelector("`+chbarFull+`"); const c = getComputedStyle(e);
	  return {Target: e.getBoundingClientRect().height, Layout: e.getBoundingClientRect().height + parseFloat(c.marginTop) + parseFloat(c.marginBottom),
	    Line: e.querySelector(".ts-ruler__line").getBoundingClientRect().height}; })()`, &h)
	if h.Target < 44 || h.Layout > 14.5 || h.Line > 2.5 {
		t.Errorf("target %v px, takes %v px of layout, line %v px; want 44, 14 and 2", h.Target, h.Layout, h.Line)
	}
}

func TestChapterBarKeyboard(t *testing.T) {
	e := playerApp(t)
	tab := chbarPlayerTab(t, e, 390, 844)
	tab.Eval(`document.querySelector("`+chbarFull+`").focus()`, nil)
	tab.WaitFor(`document.querySelector("`+chbarFull+chbarTip+`") && getComputedStyle(document.querySelector("`+chbarFull+chbarTip+`")).display !== "none"`, 3*time.Second)
	if got := chbarTipText(tab, chbarFull); got != "Chapter 4 · 1:30" {
		t.Errorf("focus tip %q (the place is in chapter 4)", got)
	}
	fullpKey(t, tab, "ArrowRight", 39)
	tab.WaitFor(`document.querySelector("`+chbarFull+`").getAttribute("aria-valuenow") === "5"`, 2*time.Second)
	var v string
	tab.Eval(`document.querySelector("`+chbarFull+`").getAttribute("aria-valuetext")`, &v)
	if v != "Chapter 5 of 6: Chapter 5" {
		t.Errorf("value text %q", v)
	}
	if p := fullpPos(tab); p < 99000 || p > 110000 {
		t.Errorf("an arrow moved the book to %d ms; it only chooses", p)
	}
	fullpKey(t, tab, "Enter", 13)
	tab.WaitFor(`window.tsPlayer.state().positionMs >= 119000 && window.tsPlayer.state().positionMs < 125000`, 2*time.Second)
	if st := playerStateOf(tab); st.Playing {
		t.Error("choosing a chapter in a paused player started it")
	}
}

func TestChapterBarClickJumpsSavesAndOffersUndo(t *testing.T) {
	e := playerApp(t)
	tab := chbarPlayerTab(t, e, 390, 844)
	x, y := chbarPoint(tab, chbarFull, 0.1) // chapter 1
	playerClickAt(t, tab, x, y)
	tab.WaitFor(`window.tsPlayer.state().positionMs < 2000`, 3*time.Second)
	tab.WaitFor(`document.querySelector(`+"`"+undoButton+"`"+`) !== null`, 3*time.Second)
	deadline := time.Now().Add(8 * time.Second)
	saved := false
	for time.Now().Before(deadline) && !saved {
		for _, r := range e.spy.posts() {
			if strings.Contains(r.Body, "position_ms=0") || strings.Contains(r.Body, `"position_ms":0`) {
				saved = true
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	if !saved {
		t.Error("the jump was not saved")
	}
	playerClickSel(t, tab, undoButton)
	tab.WaitFor(`window.tsPlayer.state().positionMs > 98000`, 2*time.Second)
}

func TestChapterBarTouchTapAndHold(t *testing.T) {
	e := playerApp(t)
	tab := chbarPlayerTab(t, e, 390, 844)
	touch := func(typ string, x, y float64) {
		points := []map[string]any{{"x": x, "y": y}}
		if typ == "touchEnd" {
			points = []map[string]any{}
		}
		if err := tab.Call("Input.dispatchTouchEvent", map[string]any{"type": typ, "touchPoints": points}, nil); err != nil {
			t.Fatal(err)
		}
	}
	// hold: the label shows while the finger is down, and the book has not moved yet
	x, y := chbarPoint(tab, chbarFull, 0.95) // chapter 6, 2:30
	touch("touchStart", x, y)
	time.Sleep(300 * time.Millisecond)
	if got := chbarTipText(tab, chbarFull); got != "Chapter 6 · 2:30" {
		t.Errorf("held tip %q", got)
	}
	if p := fullpPos(tab); p > 110000 {
		t.Errorf("holding moved the book to %d ms", p)
	}
	touch("touchEnd", x, y)
	tab.WaitFor(`window.tsPlayer.state().positionMs >= 149000`, 3*time.Second)
	tab.WaitFor(`getComputedStyle(document.querySelector("`+chbarFull+chbarTip+`")).display === "none"`, 2*time.Second) // the tip goes when the finger lifts
	// a drag down from the bar cancels: nothing jumps and the swipe does not minimise the player
	before := fullpPos(tab)
	x, y = chbarPoint(tab, chbarFull, 0.2)
	swipeTouch(t, tab, x, y, x, y+250)
	time.Sleep(600 * time.Millisecond)
	var open bool
	tab.Eval(fullpDialog+`.open`, &open)
	if !open {
		t.Error("a drag down from the chapter bar minimised the player")
	}
	fullpNear(t, "after a cancelled press", fullpPos(tab), before, 1500)
	// a tap
	x, y = chbarPoint(tab, chbarFull, 0.3) // chapter 2
	touch("touchStart", x, y)
	touch("touchEnd", x, y)
	tab.WaitFor(`window.tsPlayer.state().positionMs >= 29000 && window.tsPlayer.state().positionMs < 36000`, 3*time.Second)
}

// The chapter scrubber is untouched: a drag on it still scrubs inside its chapter.
func TestChapterBarDragStillScrubs(t *testing.T) {
	e := playerApp(t)
	tab := chbarPlayerTab(t, e, 390, 844)
	x0, y := chbarPoint(tab, "#player-full [data-ts-range]", 0.1)
	x1, _ := chbarPoint(tab, "#player-full [data-ts-range]", 0.9)
	fullpDrag(t, tab, x0, y, x1, y)
	tab.WaitFor(`window.tsPlayer.state().positionMs > 115000 && window.tsPlayer.state().positionMs <= 120000`, 3*time.Second)
}

func TestChapterBarHomeHeroStartsTheBook(t *testing.T) {
	e := playerApp(t)
	e.store(t, 10_000, "chbardevice01", "Other device", time.Now().UnixMilli()-5000, false)
	b := chromium(t)
	tab := b.NewTab(1440, 900, false, false)
	tab.Navigate(e.URL + "/")
	sel := "#home-hero .ts-ruler"
	tab.WaitFor(`document.querySelector("`+sel+`") !== null`, 10*time.Second)
	var role string
	tab.Eval(`document.querySelector("`+sel+`").getAttribute("role")`, &role)
	if role != "slider" {
		t.Fatalf("the hero ruler's role is %q", role)
	}
	x, y := chbarPoint(tab, sel, 0.7) // 126 s: chapter 5 at 2:00
	chbarMove(t, tab, x, y)
	tab.WaitFor(`document.querySelector("`+sel+chbarTip+`") && document.querySelector("`+sel+chbarTip+`").textContent === "Chapter 5 · 2:00"`, 5*time.Second)
	playerClickAt(t, tab, x, y)
	tab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().positionMs >= 119000`, 10*time.Second)
}

func TestChapterBarNoChapters(t *testing.T) {
	e := playerApp(t)
	if err := e.a.lib.Update(context.Background(), func(tx *library.Tx) error { return tx.ReplaceChapters(e.book.ID, nil) }); err != nil {
		t.Fatal(err)
	}
	tab := chbarPlayerTab(t, e, 390, 844)
	var r struct {
		Hidden   bool
		Tab      bool
		Progress bool
	}
	tab.Eval(`(() => { const e = document.querySelector("`+chbarFull+`"); return {Hidden: e.getAttribute("aria-hidden") === "true", Tab: e.hasAttribute("tabindex")}; })()`, &r)
	if !r.Hidden || r.Tab {
		t.Errorf("a book without chapters: %+v, want the picture as before", r)
	}
	home := fullpOpen(t, e, 1440, 900, false)
	home.Navigate(e.URL + "/")
	home.WaitFor(`document.querySelector("#home-hero .ts-ruler") !== null`, 10*time.Second)
	home.Eval(`document.querySelector("#home-hero .ts-ruler").getAttribute("role") === "progressbar" && !document.querySelector("#home-hero [data-ts-chbar]")`, &r.Progress)
	if !r.Progress {
		t.Error("the hero's ruler of a book without chapters is not the plain progress bar")
	}
}

func TestChapterBarLongTitleStaysInside(t *testing.T) {
	e := playerApp(t)
	long := strings.Repeat("The very long name of a chapter ", 12)
	chs := []library.Chapter{
		{StartMs: 0, EndMs: 90_000, Title: long, Source: library.ChapterFile},
		{StartMs: 90_000, EndMs: 180_000, Title: long, Source: library.ChapterFile},
	}
	if err := e.a.lib.Update(context.Background(), func(tx *library.Tx) error { return tx.ReplaceChapters(e.book.ID, chs) }); err != nil {
		t.Fatal(err)
	}
	for _, size := range [][2]int{{390, 844}, {1440, 900}} {
		tab := chbarPlayerTab(t, e, size[0], size[1])
		for _, f := range []float64{0.01, 0.99} {
			x, y := chbarPoint(tab, chbarFull, f)
			chbarMove(t, tab, x, y)
			tab.WaitFor(`document.querySelector("`+chbarFull+chbarTip+`") && getComputedStyle(document.querySelector("`+chbarFull+chbarTip+`")).display !== "none"`, 3*time.Second)
			time.Sleep(100 * time.Millisecond)
			b := chbarTipBox(tab, chbarFull)
			var panel struct{ L, R float64 }
			tab.Eval(`(() => { const r = document.querySelector("#player-full .ts-player__body").getBoundingClientRect(); return {L: r.left, R: r.right}; })()`, &panel)
			if b.Left < panel.L-0.5 || b.Right > panel.R+0.5 || !b.Cut {
				t.Errorf("%dpx at %.2f: tip %+v panel %+v", size[0], f, b, panel)
			}
			chbarMove(t, tab, 5, 5)
		}
	}
}
