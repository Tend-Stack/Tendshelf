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
				playerClickSel(t, tab, "#go")
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
		})
	}
}
