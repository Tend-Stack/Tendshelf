//go:build dev

package web

import (
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// The bug class (operator, 2026-11-03): an admin or settings action that answered htmx with
// HX-Redirect reloaded the window and stopped the book that was playing. This plays a book and
// does representative actions on the pages of "Your server" and Settings; after each, no page load
// happened and the book still plays and has moved on. Needs TENDSHELF_TEST_BROWSER.
func TestAdminActionsKeepPlayingBrowser(t *testing.T) {
	b := chromium(t)
	for _, vp := range keepsPlayingViewports {
		t.Run(vp.name, func(t *testing.T) {
			e := playerApp(t)
			extra := sweepUserID(t, e.a, e.a.as(t, users.RoleMember))
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
				deadline := time.Now().Add(4 * time.Second)
				for s.PositionMs <= last && time.Now().Before(deadline) {
					time.Sleep(100 * time.Millisecond)
					s = playerStateOf(tab)
				}
				if s.PositionMs <= last {
					t.Errorf("%s: position %d did not advance past %d", step, s.PositionMs, last)
				}
				last = s.PositionMs
			}
			check("playing")
			// goBoosted follows a link the way a link in the page would: through htmx.
			goBoosted := func(href, ready string) {
				t.Helper()
				tab.Eval(`(() => { const a = document.createElement("a"); a.id = "go"; a.href = "`+href+`"; a.textContent = "go";
				  a.style.cssText = "position:fixed;left:8px;top:80px;width:120px;height:60px;z-index:50;background:white;color:black";
				  document.getElementById("main").append(a); htmx.process(a); })()`, nil)
				tab.Eval(`document.getElementById("go").click()`, nil) // a sheet may cover the link
				tab.WaitFor(`location.pathname + location.search === "`+href+`" && (`+ready+`)`, 8*time.Second)
				check("open " + href)
			}
			submit := func(sel string) {
				t.Helper()
				tab.Eval(`document.querySelector(`+cdp.Quote(sel)+`).requestSubmit()`, nil)
			}

			// Users & invites: change a person's role.
			goBoosted("/admin/users/"+extra, `document.querySelector('input[name="role"][value="admin"]') !== null`)
			tab.Eval(`document.querySelector('input[name="role"][value="admin"]').checked = true`, nil)
			submit(`form[action$="/role"]`)
			tab.WaitFor(`location.pathname === "/admin/users/`+extra+`" && document.querySelector('input[name="role"][value="admin"]').checked && document.body.textContent.includes("Admin")`, 8*time.Second)
			var role string
			for i := 0; i < 40 && role != "admin"; i++ {
				_ = e.a.st.Reader().QueryRow(`SELECT role FROM users WHERE id = ?`, extra).Scan(&role)
				time.Sleep(50 * time.Millisecond)
			}
			if role != "admin" {
				t.Errorf("role = %q, want admin", role)
			}
			check("role changed")

			// Email settings: save.
			goBoosted("/admin/email", `document.querySelector("#email-host") !== null`)
			tab.Eval(`(() => {
			  document.querySelector("#email-host").value = "smtp.example.test";
			  document.querySelector("#email-port").value = "587";
			  document.querySelector("#email-from").value = "books@example.test";
			})()`, nil)
			submit(`form.ts-email__form`)
			tab.WaitFor(`location.search.includes("saved=1")`, 8*time.Second)
			check("email saved")

			// Share links: make one, then stop it.
			goBoosted("/books/"+e.book.ID+"/share", `document.querySelector("form.ts-share__linkform") !== null`)
			submit(`form.ts-share__linkform`)
			tab.WaitFor(`document.querySelector('form[action$="/stop"]') !== null`, 8*time.Second)
			check("link made")
			submit(`form[action$="/stop"]`)
			tab.WaitFor(`document.querySelector('form[action$="/stop"]') === null`, 8*time.Second)
			check("link stopped")

			// Settings: change the usual speed (a theme change reloads on purpose, so not that).
			goBoosted("/me/settings", `document.querySelector("#st-speed") !== null`)
			tab.Eval(`(() => { const s = document.querySelector("#st-speed"); s.selectedIndex = s.options.length - 1; s.dispatchEvent(new Event("change", {bubbles: true})); })()`, nil)
			time.Sleep(700 * time.Millisecond)
			check("settings saved")

			// Shelves: put the playing book on a new shelf from its "Add to shelf" sheet, rename the
			// shelf, take the book off it.
			goBoosted("/books/"+e.book.ID, `document.querySelector(".ts-book__shelf") !== null`)
			playerClickSel(t, tab, ".ts-book__shelf")
			tab.WaitFor(`document.querySelector("#sheet form.ts-shelf-sheet__new") !== null`, 8*time.Second)
			check("shelf sheet open")
			tab.Eval(`document.querySelector("#shs-name").value = "Evening listens"`, nil)
			submit(`#sheet form.ts-shelf-sheet__new`)
			var shelfID string
			for i := 0; i < 80 && shelfID == ""; i++ {
				_ = e.a.st.Reader().QueryRow(`SELECT id FROM shelves WHERE name = 'Evening listens'`).Scan(&shelfID)
				time.Sleep(50 * time.Millisecond)
			}
			if shelfID == "" {
				t.Fatal("the shelf was not made")
			}
			check("shelf made")
			goBoosted("/shelves/"+shelfID, `document.querySelector("#shelf-rename") !== null`)
			tab.Eval(`document.querySelector("#shelf-rename").value = "Late listens"`, nil)
			submit(`form[action$="/rename"]`)
			tab.WaitFor(`document.body.textContent.includes("Late listens")`, 8*time.Second)
			check("shelf renamed")
			submit(`form[action$="/remove"]`)
			tab.WaitFor(`document.body.textContent.includes("This shelf is empty")`, 8*time.Second)
			check("book taken off the shelf")
		})
	}
}

// TestPrefsCategoriesKeepPlayingBrowser clicks the real category list of the preferences window (no
// reload, no stop): Playback, then Appearance, then Users & invites. The page-load marker and the
// playing book must survive each move.
func TestPrefsCategoriesKeepPlayingBrowser(t *testing.T) {
	b := chromium(t)
	e := playerApp(t)
	base := themedServer(t, e.a.signedIn(t))
	tab := playerOpen(t, b, base, 1440, 900)
	e.playerInject(tab, e.book.ID, 0, 0, "")
	playerClickSel(t, tab, "#play-link")
	tab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().positionMs > 300`, 10*time.Second)
	tab.Eval(`window.__tsMarker = 1`, nil)
	check := func(step string) {
		t.Helper()
		var marker int
		tab.Eval(`window.__tsMarker || 0`, &marker)
		if marker != 1 {
			t.Errorf("%s: the page was reloaded", step)
		}
		if s := playerStateOf(tab); !s.Playing || s.BookID != e.book.ID {
			t.Errorf("%s: state %+v, want the book playing", step, s)
		}
	}
	// Open the window through the sidebar's Profile link, then use only the list.
	tab.Eval(`document.querySelector('a[href="/me/settings"], a[href="/me"]').click()`, nil)
	tab.WaitFor(`document.querySelector(".ts-prefs__item") !== null`, 8*time.Second)
	check("settings opened")
	for _, step := range []struct{ href, ready string }{
		{"/me/settings/playback", `document.querySelector("#st-speed") !== null`},
		{"/me/settings/appearance", `document.querySelector("#st-theme-dark") !== null`},
		{"/admin/users", `location.pathname === "/admin/users"`},
	} {
		tab.Eval(`document.querySelector('.ts-prefs__item[href="`+step.href+`"]').click()`, nil)
		tab.WaitFor(`location.pathname === "`+step.href+`" && (`+step.ready+`)`, 8*time.Second)
		time.Sleep(200 * time.Millisecond)
		check("open " + step.href)
	}
}
