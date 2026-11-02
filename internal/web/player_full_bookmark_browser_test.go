//go:build dev

package web

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// Browser tests of the bookmark button and the B key (P1c-5b, make player).

// bmkeyCount wraps fetch so the test can count the bookmark requests.
func bmkeyCount(tab *cdp.Tab) {
	tab.Eval(`(() => { window.bmkeyCalls = 0; const f = window.fetch;
	  window.fetch = (u, o) => { if (String(u).includes("/bookmarks") && o && o.method === "POST") window.bmkeyCalls++; return f(u, o); }; })()`, nil)
}

func bmkeyCalls(tab *cdp.Tab) int {
	var n int
	tab.Eval(`window.bmkeyCalls`, &n)
	return n
}

// bmkeyPlay starts sound with a real click on the toggle (headless needs a gesture).
func bmkeyPlay(t *testing.T, tab *cdp.Tab, sel string) {
	t.Helper()
	playerClickSel(t, tab, sel)
	tab.WaitFor(`window.tsPlayer.state().playing`, 5*time.Second)
}

// bmkeyStored is the stored position of the bookmark a toast link names.
func bmkeyStored(t *testing.T, e *playerEnv, href string) int64 {
	t.Helper()
	id := href[strings.LastIndex(href, "bookmark=")+len("bookmark="):]
	b, err := e.a.srv.progress.Bookmark(context.Background(), e.ownerID, id)
	if err != nil {
		t.Fatalf("bookmark %q: %v", id, err)
	}
	return b.PositionMs
}

func bmkeyToast(tab *cdp.Tab, host string) (text, href string) {
	var out struct{ Text, Href string }
	tab.Eval(`(() => { const t = document.querySelector(`+cdp.Quote(host+" .ts-toast")+`); if (!t) return {Text: "", Href: ""};
	  const a = t.querySelector("a"); return {Text: t.textContent.trim(), Href: a ? a.getAttribute("href") : ""}; })()`, &out)
	return out.Text, out.Href
}

func TestFullPlayerBrowserBookmark(t *testing.T) {
	e := playerApp(t)

	// Desktop: B while playing, player closed.
	wide := fullpOpen(t, e, 1440, 900, false)
	bmkeyCount(wide)
	bmkeyPlay(t, wide, "[data-ts-toggle]")
	fullpKey(t, wide, "b", 66)
	wide.WaitFor(`document.querySelector("#toasts .ts-toast a") !== null`, 3*time.Second)
	text, href := bmkeyToast(wide, "#toasts")
	if !strings.HasPrefix(text, "Bookmarked at ") || !strings.Contains(href, "sheet=bookmark&bookmark=") {
		t.Errorf("toast %q link %q", text, href)
	}
	live := fullpPos(wide)
	fullpNear(t, "stored position", bmkeyStored(t, e, href), live, 1500)
	if n := bmkeyCalls(wide); n != 1 {
		t.Errorf("requests after B = %d, want 1", n)
	}
	// Not while a field has focus, nor while the keys list is open.
	wide.Eval(`document.querySelector('#player .ts-wave--bar .ts-wave__input').focus()`, nil)
	fullpKey(t, wide, "b", 66)
	wide.Eval(`document.activeElement.blur(); document.getElementById("keys-help").showModal()`, nil)
	fullpKey(t, wide, "b", 66)
	time.Sleep(600 * time.Millisecond)
	if n := bmkeyCalls(wide); n != 1 {
		t.Errorf("requests after B in a field and in the keys list = %d, want 1", n)
	}
	wide.Eval(`document.getElementById("keys-help").close()`, nil)

	// The Bookmarks tab of the playing book shows the live place and saves it.
	playerClickSel(t, wide, `a[href="/books/`+e.book.ID+`"]`)
	// The address changes before the swap lands: wait for the tab itself, not only the path.
	wide.WaitFor(`location.pathname === "/books/`+e.book.ID+`" && document.getElementById("book-tab-bookmarks") !== null`, 5*time.Second)
	wide.Eval(`document.getElementById("book-tab-bookmarks").click()`, nil)
	wide.WaitFor(`document.querySelector("form[data-ts-bookmark-add]") !== null`, 5*time.Second)
	wide.WaitFor(`document.querySelector("[data-ts-bookmark-add-label]").textContent !== "Bookmark this moment · 0:10"`, 2*time.Second)
	var played bool
	wide.Eval(`window.tsPlayer.state().playing`, &played)
	if !played {
		t.Error("navigating stopped the sound")
	}
	wide.Eval(`document.querySelector("form[data-ts-bookmark-add] button").click()`, nil)
	list, err := e.a.srv.progress.Bookmarks(context.Background(), e.ownerID, e.book.ID, false)
	for deadline := time.Now().Add(5 * time.Second); err == nil && len(list) < 2 && time.Now().Before(deadline); {
		time.Sleep(100 * time.Millisecond)
		list, err = e.a.srv.progress.Bookmarks(context.Background(), e.ownerID, e.book.ID, false)
	}
	if err != nil || len(list) != 2 {
		t.Fatalf("bookmarks after the tab button: %v %d", err, len(list))
	}
	var newest int64
	for _, b := range list {
		if b.PositionMs != bmkeyStoredFirst(t, e, href) {
			newest = b.PositionMs
		}
	}
	fullpNear(t, "tab form position", newest, fullpPos(wide), 2500)
	if newest < 101000 {
		t.Errorf("tab form saved %d ms, the stored place (100 s) instead of the live one", newest)
	}

	// Phone: the header button, the toast inside the dialog, "Add a note" opens the sheet.
	phone := fullpOpen(t, e, 390, 844, false)
	fullpOpenPlayer(t, phone)
	bmkeyPlay(t, phone, "[data-ts-full-toggle]")
	playerClickSel(t, phone, "#player-full .ts-player__mark")
	phone.WaitFor(`document.querySelector("#player-full [data-ts-full-toasts] .ts-toast a") !== null`, 3*time.Second)
	var top bool
	phone.Eval(`(() => { const t = document.querySelector("#player-full [data-ts-full-toasts] .ts-toast"), r = t.getBoundingClientRect();
	  const hit = document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2); return t.contains(hit); })()`, &top)
	if !top {
		t.Error("the toast is not on top of the modal player")
	}
	playerClickSel(t, phone, "#player-full [data-ts-full-toasts] .ts-toast a")
	phone.WaitFor(`!`+fullpDialog+`.open`, 3*time.Second)
	phone.WaitFor(`document.querySelector("#sheet .ts-bookmark-sheet") !== null`, 5*time.Second)
	var still bool
	phone.Eval(`window.tsPlayer.state().playing`, &still)
	if !still {
		t.Error("opening the note stopped the sound")
	}

	// A refused request says so in plain words and keeps the screen as it is.
	bad := fullpOpen(t, e, 390, 844, false)
	fullpOpenPlayer(t, bad)
	bad.Eval(`document.body.setAttribute("hx-headers:inherited", JSON.stringify({"X-CSRF-Token": "wrong"}))`, nil)
	playerClickSel(t, bad, "#player-full .ts-player__mark")
	bad.WaitFor(`document.querySelector("#player-full [data-ts-full-toasts] [role=alert]") !== null`, 3*time.Second)
	var msg string
	bad.Eval(`document.querySelector("#player-full [data-ts-full-toasts] [role=alert]").textContent`, &msg)
	if msg != "That bookmark didn't save. Try again." {
		t.Errorf("failure toast %q", msg)
	}
}

// bmkeyStoredFirst is the position of the bookmark the B key made.
func bmkeyStoredFirst(t *testing.T, e *playerEnv, href string) int64 {
	return bmkeyStored(t, e, href)
}
