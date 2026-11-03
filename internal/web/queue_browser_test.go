//go:build dev

package web

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// Up next (Q2) in Chromium, at desktop-1440 and mobile-390: while a book plays, the split button on
// another book's page adds to Up next (toast, closed menu, focus back on the toggle), the open menu
// survives the play region's own redraw, and the list moves and removes in place (the Up next tab at
// 1440, the /queue page at 390). Throughout: no page load (window.__tsMarker survives) and the book
// keeps playing. Needs TENDSHELF_TEST_BROWSER.
func TestQueueBrowser(t *testing.T) {
	b := chromium(t)
	for _, vp := range keepsPlayingViewports {
		t.Run(vp.name, func(t *testing.T) {
			e := playerApp(t)
			var ids []string
			for _, title := range []string{"Harbour Dawn", "Salt Road"} {
				bk := librarytest.AddBook(t, e.a.lib, e.folder, librarytest.BookSpec{Title: title, Authors: []string{"Imogen Hale"}, DurationMs: 3_600_000, AddedAt: 1_700_000_000_000})
				// The probe data that makes the book playable (so its page draws the play button).
				streamExec(t, e.a, `UPDATE audio_files SET container='wav', codec='pcm_s16le', duration_ms=3600000 WHERE book_id=?`, bk.ID)
				ids = append(ids, bk.ID)
			}
			base := themedServer(t, e.a.signedIn(t))
			tab := playerOpen(t, b, base, vp.w, vp.h)
			e.playerInject(tab, e.book.ID, 0, 0, "")
			playerClickSel(t, tab, "#play-link")
			tab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().positionMs > 300`, 10*time.Second)
			tab.Eval(`window.__tsMarker = 1`, nil)
			var last int64
			check := func(step string) {
				t.Helper()
				var marker int
				tab.Eval(`window.__tsMarker || 0`, &marker)
				if marker != 1 {
					t.Errorf("%s: the page was reloaded", step)
				}
				s := playerStateOf(tab)
				if !s.Playing || s.BookID != e.book.ID {
					t.Errorf("%s: state %+v, want the book playing", step, s)
				}
				for deadline := time.Now().Add(4 * time.Second); s.PositionMs <= last && time.Now().Before(deadline); s = playerStateOf(tab) {
					time.Sleep(100 * time.Millisecond)
				}
				if s.PositionMs <= last {
					t.Errorf("%s: position %d did not advance past %d", step, s.PositionMs, last)
				}
				last = s.PositionMs
			}
			goBoosted := func(href, ready string) {
				t.Helper()
				tab.Eval(`(() => { const a = document.createElement("a"); a.id = "go"; a.href = "`+href+`"; a.textContent = "go";
				  a.style.cssText = "position:fixed;left:8px;top:80px;width:120px;height:60px;z-index:50;background:white;color:black";
				  document.getElementById("main").append(a); htmx.process(a); })()`, nil)
				tab.Eval(`document.getElementById("go").click()`, nil)
				tab.WaitFor(`location.pathname + location.search === "`+href+`" && (`+ready+`)`, 8*time.Second)
				time.Sleep(500 * time.Millisecond) // the page-to-page view transition
				check("open " + href)
			}
			order := func(sel string) string {
				var s string
				tab.Eval(`[...document.querySelectorAll(`+cdp.Quote(sel+" li")+`)].map((li) => li.id.replace("item-", "")).join(",")`, &s)
				return s
			}
			ready := `document.querySelector(".ts-split__more > summary") !== null`

			// Book B: open the split section, Play next.
			goBoosted("/books/"+ids[0], ready)
			var split struct{ Main, Toggle, Group float64 }
			tab.Eval(`(() => { const m = document.querySelector("#book-play [data-ts-play]").getBoundingClientRect(), t = document.querySelector(".ts-split__more > summary").getBoundingClientRect();
			  return { Main: m.width, Toggle: t.width, Group: document.querySelector(".ts-split").getBoundingClientRect().width }; })()`, &split)
			if split.Toggle < 44 || split.Toggle < split.Group*0.2 || split.Toggle > split.Group*0.3 || split.Main < split.Group*0.7 {
				t.Errorf("split widths %+v: the toggle should be about a quarter of the button (at least 44 px)", split)
			}
			playerClickSel(t, tab, ".ts-split__more > summary")
			tab.WaitFor(`document.querySelector(".ts-split__more").open`, 3*time.Second)

			// The open menu survives the region's redraw (progress-saved fires every 10 s while playing).
			tab.Eval(`document.querySelector(".ts-split__more").__keep = 1; document.getElementById("book-play").__old = 1;
			  document.body.dispatchEvent(new CustomEvent("progress-saved"))`, nil)
			tab.WaitFor(`document.getElementById("book-play").__old === undefined`, 5*time.Second)
			var kept bool
			tab.Eval(`document.querySelector(".ts-split__more").open && document.querySelector(".ts-split__more").__keep === 1`, &kept)
			if !kept {
				t.Error("the open menu did not survive the play region's redraw")
			}
			check("menu kept open")

			playerClickSel(t, tab, `.ts-split__more input[name="where"][value="next"] ~ button`)
			tab.WaitFor(`document.querySelector("#toasts").textContent.includes("Added to the top of Up next")`, 5*time.Second)
			var after struct {
				Open, Focus bool
			}
			tab.Eval(`({ Open: document.querySelector(".ts-split__more").open, Focus: document.activeElement === document.querySelector(".ts-split__more > summary") })`, &after)
			if after.Open || !after.Focus {
				t.Errorf("after Play next: menu open %v, focus on the toggle %v; want closed and focused", after.Open, after.Focus)
			}
			check("play next")

			// Book C: Add to queue; then the same again says it is already there.
			goBoosted("/books/"+ids[1], ready)
			playerClickSel(t, tab, ".ts-split__more > summary")
			tab.WaitFor(`document.querySelector(".ts-split__more").open`, 3*time.Second)
			playerClickSel(t, tab, `.ts-split__more input[name="where"][value="end"] ~ button`)
			tab.WaitFor(`document.querySelector("#toasts").textContent.includes("Added to Up next")`, 5*time.Second)
			check("add to queue")
			// Keyboard only: Enter on the focused toggle opens it, Escape closes it and keeps the focus there.
			tab.Eval(`document.querySelector(".ts-split__more > summary").focus()`, nil)
			// A key that types (text set), so the browser activates the summary like a real keypress.
			for _, typ := range []string{"keyDown", "keyUp"} {
				if err := tab.Call("Input.dispatchKeyEvent", map[string]any{"type": typ, "key": "Enter", "code": "Enter", "windowsVirtualKeyCode": 13, "text": "\r"}, nil); err != nil {
					t.Fatal(err)
				}
			}
			tab.WaitFor(`document.querySelector(".ts-split__more").open`, 3*time.Second)
			fullpKey(t, tab, "Escape", 27)
			tab.WaitFor(`!document.querySelector(".ts-split__more").open && document.activeElement === document.querySelector(".ts-split__more > summary")`, 3*time.Second)

			// The list: the Up next tab on a wide screen, the page elsewhere.
			list, step := "#up-next", ""
			if vp.w >= 1280 {
				list = "#now-up-next"
				fullpOpenPlayer(t, tab)
				playerClickSel(t, tab, "#now-tab-queue")
				step = " tab"
			} else {
				// Through the full player's ⋯ menu, which closes once its Up next link is followed.
				fullpOpenPlayer(t, tab)
				playerClickSel(t, tab, "[data-ts-full-menu] > summary")
				tab.WaitFor(`document.querySelector("[data-ts-full-menu]").open`, 3*time.Second)
				playerClickSel(t, tab, ".ts-player__menuqueue")
				tab.WaitFor(`document.querySelector("#up-next li") !== null && !document.querySelector("[data-ts-full-menu]").open && !document.getElementById("player-full").open &&
					!document.activeViewTransition && !document.documentElement.matches(".ts-vt-mini, .ts-vt-full")`, 5*time.Second)
			}
			tab.WaitFor(`document.querySelector(`+cdp.Quote(list+" li")+`) !== null`, 5*time.Second)
			if got := order(list); got != ids[0]+","+ids[1] {
				t.Fatalf("Up next%s = %s, want %s,%s", step, got, ids[0], ids[1])
			}
			move := ` li:nth-child(2) input[name="dir"][value="up"] ~ button`
			playerClickSel(t, tab, list+move)
			tab.WaitFor(`document.querySelector(`+cdp.Quote(list+" li")+`).id === "item-`+ids[1]+`"`, 5*time.Second)
			check("move up" + step)
			var focusOK bool
			tab.Eval(`document.activeElement && document.activeElement.closest("li") === document.querySelector(`+cdp.Quote(list+" li")+`)`, &focusOK)
			if !focusOK {
				t.Error("after a move the focus should stay on the moved row")
			}
			if strings.Contains(step, "tab") {
				var count string
				tab.Eval(`document.getElementById("now-panel-queue-count").textContent`, &count)
				if count != "2" {
					t.Errorf("tab count %q, want 2", count)
				}
			}
			playerClickSel(t, tab, list+` li:nth-child(1) form[action$="/delete"] button`)
			tab.WaitFor(`document.querySelectorAll(`+cdp.Quote(list+" li")+`).length === 1`, 5*time.Second)
			if got := order(list); got != ids[0] {
				t.Errorf("after remove: %s, want %s", got, ids[0])
			}
			if strings.Contains(step, "tab") {
				tab.WaitFor(`document.getElementById("now-panel-queue-count").textContent === "1"`, 3*time.Second)
			}
			check("remove" + step)
		})
	}
}

// TestQueuePanelCapture captures the Up next tab of the Now playing panel at 1440 px, filled and empty,
// in dark and light, and checks that the tab keeps its count. Needs TENDSHELF_TEST_BROWSER.
func TestQueuePanelCapture(t *testing.T) {
	e := nowPanelEnv(t)
	out := playerOut(t)
	for _, title := range []string{"Salt Road", "Harbour Dawn", "Harbour Night"} {
		page, err := e.a.lib.ListBooks(t.Context(), bookAllScope, library.ListQuery{Title: title, Limit: 5})
		if err != nil || len(page.Books) == 0 {
			t.Fatalf("book %q: %v", title, err)
		}
		if _, err := e.a.lib.QueueAdd(t.Context(), e.ownerID, page.Books[0].ID, false); err != nil {
			t.Fatal(err)
		}
	}
	tab := nowPanelOpen(t, e, 1440, 800)
	playerClickSel(t, tab, "#now-tab-queue")
	tab.WaitFor(`document.querySelectorAll("#now-up-next li").length === 3`, 5*time.Second)
	var sel struct {
		Selected bool
		Count    string
	}
	tab.Eval(`({ Selected: document.getElementById("now-tab-queue").getAttribute("aria-selected") === "true", Count: document.getElementById("now-panel-queue-count").textContent })`, &sel)
	if !sel.Selected || sel.Count != "3" {
		t.Errorf("tab %+v, want selected with count 3", sel)
	}
	for _, theme := range []string{"dark", "light"} {
		nowPanelTheme(tab, theme)
		tab.Screenshot(filepath.Join(out, "queue-panel-"+theme+"-1440.png"))
	}
	for range 3 {
		playerClickSel(t, tab, `#now-up-next li:nth-child(1) form[action$="/delete"] button`)
		time.Sleep(400 * time.Millisecond)
	}
	tab.WaitFor(`document.querySelector("#now-up-next li") === null && document.getElementById("now-panel-queue-count").textContent === "0"`, 5*time.Second)
	tab.Screenshot(filepath.Join(out, "queue-panel-empty-light-1440.png"))
}
