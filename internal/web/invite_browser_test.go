//go:build dev

package web

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// inviteProbeJS is authProbeJS with radios counted through their label (a finger hits the whole card)
// and fields that are hidden skipped.
const inviteProbeJS = `(() => {
  const small = [];
  for (const el of document.querySelectorAll("input:not([type=hidden]), button, a.ts-btn")) {
    const box = el.closest("label") || el;
    const r = box.getBoundingClientRect();
    if (r.width > 0 && r.height < 43.5) small.push((el.id || el.className || el.tagName) + ":" + Math.round(r.height));
  }
  const labels = [...document.querySelectorAll("input:not([type=hidden])")].filter(i => !i.closest("label") && !document.querySelector('label[for="' + i.id + '"]')).map(i => i.id);
  return {over: document.documentElement.scrollWidth > innerWidth, small, unlabelled: labels, h1: document.querySelectorAll("h1").length};
})()`

// inviteFill sets a field the way typing would, then fires the event a form listens to.
func inviteFill(tab *cdp.Tab, sel, value string) {
	tab.Eval(`(() => { const el = document.querySelector(`+cdp.Quote(sel)+`); el.value = `+cdp.Quote(value)+`; el.dispatchEvent(new Event("input", {bubbles: true})); })()`, nil)
}

// TestInviteAcceptBrowser walks an invited person through the pages in Chromium at 390 and 1440:
// open the link signed out, accept, land on the book signed in, keep the place with a name and
// password, sign out and sign in again with them; then a second invite taken with "Just this
// phone for now", which leaves the account without a password.
func TestInviteAcceptBrowser(t *testing.T) {
	for _, w := range []int{390, 1440} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			b := chromium(t)
			e := inviteNewWeb(t)
			srv := httptest.NewServer(e.a.h)
			t.Cleanup(srv.Close)
			waitPath := func(tab *cdp.Tab, prefix string) {
				t.Helper()
				tab.WaitFor(`location.pathname.startsWith(`+cdp.Quote(prefix)+`)`, 10*time.Second)
			}
			probe := func(tab *cdp.Tab, what string) {
				t.Helper()
				var m struct {
					Over       bool     `json:"over"`
					Small      []string `json:"small"`
					Unlabelled []string `json:"unlabelled"`
					H1         int      `json:"h1"`
				}
				tab.Eval(inviteProbeJS, &m)
				if m.Over || len(m.Small) > 0 || len(m.Unlabelled) > 0 || m.H1 != 1 {
					t.Errorf("%s: overflow %v, targets under 44 px %v, unlabelled %v, h1 %d", what, m.Over, m.Small, m.Unlabelled, m.H1)
				}
			}
			tabKey := func(tab *cdp.Tab) {
				for _, typ := range []string{"keyDown", "keyUp"} {
					if err := tab.Call("Input.dispatchKeyEvent", map[string]any{"type": typ, "key": "Tab", "code": "Tab", "windowsVirtualKeyCode": 9}, nil); err != nil {
						t.Fatal(err)
					}
				}
			}
			newInvite := func(name string) string {
				_, tok := e.create(e.ownerID, share.InviteSpec{
					Name: name, Note: "Start with this one.", Role: users.RoleMember,
					Grants: []share.InviteGrant{{Scope: share.GrantLibrary, Target: e.lib.ID, Download: true}},
				})
				return tok
			}

			// Rosa: a name and password.
			tab := b.NewTab(w, 900, w < 768, false)
			tab.Navigate(srv.URL + "/invite/" + newInvite("Rosa"))
			tab.WaitFor(`document.querySelector(".ts-invite__cta button") !== null`, 10*time.Second)
			probe(tab, "invite page")
			tabKey(tab)
			var focused bool
			tab.Eval(`document.activeElement.matches(":focus-visible") && getComputedStyle(document.activeElement).boxShadow !== "none"`, &focused)
			if !focused {
				t.Error("invite page: no visible focus ring on the first Tab stop")
			}
			playerClickSel(t, tab, `.ts-invite__cta button`)
			waitPath(tab, "/welcome")
			tab.WaitFor(`document.querySelector("#keep-password") !== null`, 10*time.Second)
			probe(tab, "welcome page")
			playerClickSel(t, tab, `#keep-password`)
			inviteFill(tab, "#username", "rosa-keeps")
			inviteFill(tab, "#password", "a long quiet password")
			playerClickSel(t, tab, `form[action="/welcome"] button[type=submit]`)
			waitPath(tab, "/books/")
			tab.WaitFor(`document.body.innerText.includes("Usersword")`, 10*time.Second)
			if e.count(`SELECT count(*) FROM users WHERE username = 'rosa-keeps' AND password_hash IS NOT NULL AND password_hash != ''`) != 1 {
				t.Error("the chosen name and password were not stored")
			}

			// Sign out, then sign in again with the new name and password.
			tab.Navigate(srv.URL + "/me")
			tab.WaitFor(`document.querySelector("#pf-menu-account summary") !== null`, 10*time.Second)
			playerClickSel(t, tab, `#pf-menu-account summary`)
			tab.WaitFor(`document.querySelector("#pf-menu-account").open`, 10*time.Second)
			playerClickSel(t, tab, `.ts-profile__signout button`)
			waitPath(tab, "/login")
			tab.WaitFor(`document.querySelector("#username") !== null`, 10*time.Second)
			inviteFill(tab, "#username", "rosa-keeps")
			inviteFill(tab, "#password", "a long quiet password")
			playerClickSel(t, tab, `form[action="/login"] button[type=submit]`)
			tab.WaitFor(`!location.pathname.startsWith("/login")`, 10*time.Second)
			tab.Navigate(srv.URL + "/me")
			var at string
			tab.Eval(`location.pathname`, &at)
			if at != "/me" {
				t.Errorf("signed in again, /me answered at %q", at)
			}

			// Tomas: just this phone.
			tab2 := b.NewTab(w, 900, w < 768, false)
			tab2.Navigate(srv.URL + "/invite/" + newInvite("Tomas"))
			tab2.WaitFor(`document.querySelector(".ts-invite__cta button") !== null`, 10*time.Second)
			playerClickSel(t, tab2, `.ts-invite__cta button`)
			waitPath(tab2, "/welcome")
			tab2.WaitFor(`document.querySelector("#keep-phone") !== null`, 10*time.Second)
			var shown bool
			tab2.Eval(`getComputedStyle(document.querySelector(".ts-invite__fields")).display !== "none"`, &shown)
			if shown {
				t.Error("the name and password fields show while 'Just this phone' is on")
			}
			playerClickSel(t, tab2, `#keep-password`)
			tab2.Eval(`getComputedStyle(document.querySelector(".ts-invite__fields")).display !== "none"`, &shown)
			if !shown {
				t.Error("the fields stay hidden after choosing 'A name and password'")
			}
			playerClickSel(t, tab2, `#keep-phone`)
			playerClickSel(t, tab2, `form[action="/welcome"] button[type=submit]`)
			waitPath(tab2, "/books/")
			tab2.WaitFor(`document.body.innerText.includes("Usersword")`, 10*time.Second)
			if n := e.count(`SELECT count(*) FROM users WHERE display_name = 'Tomas' AND (password_hash IS NULL OR password_hash = '')`); n != 1 {
				t.Errorf("Tomas without a password: %d accounts, want 1", n)
			}
		})
	}
}
