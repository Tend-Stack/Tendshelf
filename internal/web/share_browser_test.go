//go:build dev

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// TestShareBrowser walks the Share & invite flow in Chromium at 390 and 1440: the owner opens a
// book's share page, writes a name, creates the link and reads it; a second browser (its own
// profile, no cookies) opens it and accepts; the new person can open that book and no other.
// Then the owner stops sharing the book from the same page and the access is gone.
func TestShareBrowser(t *testing.T) {
	for _, w := range []int{390, 1440} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			owner := chromium(t)
			guest := chromium(t)
			e := inviteNewWeb(t)
			book, other := e.books[0], e.books[1]
			// One server for the owner's browser (it carries the owner's session), one for the guest's.
			ownerSrv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
				r.AddCookie(&http.Cookie{Name: e.owner.Name, Value: e.owner.Value})
				e.a.h.ServeHTTP(rw, r)
			}))
			guestSrv := httptest.NewServer(e.a.h)
			t.Cleanup(ownerSrv.Close)
			t.Cleanup(guestSrv.Close)
			wait := func(tab *cdp.Tab, js string) {
				t.Helper()
				tab.WaitFor(js, 10*time.Second)
			}

			tab := owner.NewTab(w, 900, w < 768, false)
			tab.Navigate(ownerSrv.URL + "/books/" + book.ID)
			wait(tab, `document.querySelector("a.ts-book__share") !== null`)
			playerClickSel(t, tab, `a.ts-book__share`)
			wait(tab, `location.pathname.endsWith("/share") && document.querySelector("#who") !== null`)

			// Every control is a reachable, big-enough, labelled target.
			var m struct {
				Over       bool     `json:"over"`
				Small      []string `json:"small"`
				Unlabelled []string `json:"unlabelled"`
				H1         int      `json:"h1"`
			}
			tab.Eval(shareProbeJS, &m)
			if m.Over || len(m.Small) > 0 || len(m.Unlabelled) > 0 || m.H1 != 1 {
				t.Errorf("share page: overflow %v, small %v, unlabelled %v, h1 %d", m.Over, m.Small, m.Unlabelled, m.H1)
			}
			// Email it is a link to the email tab (it works with no mail server set up).
			var href string
			tab.Eval(`document.querySelectorAll("a.ts-share__tab")[1].getAttribute("href")`, &href)
			if !strings.HasSuffix(href, "/share?via=email") {
				t.Errorf("Email it links to %q", href)
			}

			// Tab order: from the name field every control takes focus, each with a visible ring.
			tab.Eval(`document.getElementById("who").focus()`, nil)
			stops := map[string]bool{}
			for i := 0; i < 6; i++ {
				for _, typ := range []string{"keyDown", "keyUp"} {
					if err := tab.Call("Input.dispatchKeyEvent", map[string]any{"type": typ, "key": "Tab", "code": "Tab", "windowsVirtualKeyCode": 9}, nil); err != nil {
						t.Fatal(err)
					}
				}
				var f struct {
					Name string `json:"name"`
					Ring bool   `json:"ring"`
				}
				tab.Eval(`(() => { const a = document.activeElement, c = a.closest(".ts-share__choice, .ts-share__chip") || a;
				  const s = getComputedStyle(c), s2 = getComputedStyle(a);
				  return {name: a.id || a.name || a.tagName, ring: a.matches(":focus-visible") && (s.boxShadow !== "none" || s2.boxShadow !== "none")}; })()`, &f)
				stops[f.Name] = true
				if !f.Ring {
					t.Errorf("no visible focus ring on %q", f.Name)
				}
			}
			for _, want := range []string{"scope-book", "role", "share-read", "expires", "msg"} {
				if !stops[want] {
					t.Errorf("tab order never reached %q (stops %v)", want, stops)
				}
			}

			// A fresh load of the page, as a person arriving by link would see it.
			tab.Navigate(ownerSrv.URL + "/books/" + book.ID + "/share")
			wait(tab, `document.querySelector("#who") !== null`)
			inviteFill(tab, "#who", "Rosa")
			playerClickSel(t, tab, `.ts-share__create button`)
			wait(tab, `document.querySelector("#invite-link") !== null`)
			var link string
			tab.Eval(`document.querySelector("#invite-link").value`, &link)
			i := strings.Index(link, "/invite/")
			if i < 0 {
				t.Fatalf("link %q", link)
			}
			path := link[i:]

			// The second browser opens the link and keeps the account on this phone only.
			rosa := guest.NewTab(w, 900, w < 768, false)
			rosa.Navigate(guestSrv.URL + path)
			wait(rosa, `document.querySelector(".ts-invite__cta button") !== null`)
			playerClickSel(t, rosa, `.ts-invite__cta button`)
			wait(rosa, `location.pathname.startsWith("/welcome")`)
			wait(rosa, `document.querySelector("#keep-phone") !== null`)
			playerClickSel(t, rosa, `#keep-phone`)
			playerClickSel(t, rosa, `form[action="/welcome"] button[type=submit]`)
			wait(rosa, `location.pathname === "/books/`+book.ID+`"`)
			wait(rosa, `document.body.innerText.includes(`+cdp.Quote(book.Title)+`)`)

			status := func(tab *cdp.Tab, p string) int {
				var code int
				tab.Eval(`fetch(`+cdp.Quote(p)+`, {credentials: "include"}).then(r => r.status)`, &code)
				return code
			}
			if c := status(rosa, "/books/"+book.ID); c != 200 {
				t.Errorf("shared book: %d, want 200", c)
			}
			if c := status(rosa, "/hx/books/"+book.ID+"/play"); c != 200 {
				t.Errorf("shared book play: %d, want 200", c)
			}
			if c := status(rosa, "/books/"+other.ID); c != 404 {
				t.Errorf("other book: %d, want 404", c)
			}

			// Stop sharing this book from its share page: the person's grant on it goes.
			tab.Navigate(ownerSrv.URL + "/books/" + book.ID + "/share")
			wait(tab, `document.querySelector(".ts-share__stop") !== null`)
			playerClickSel(t, tab, `.ts-share__stop`)
			wait(tab, `document.querySelector(".ts-users__confirm") !== null`)
			// The boosted page swap is still settling right after the page appears; a click in that
			// moment is lost, so give it a beat (a person is never this quick).
			time.Sleep(time.Second)
			playerClickSel(t, tab, `.ts-users__confirm button[type=submit]`)
			wait(tab, `location.pathname.endsWith("/share")`)
			if n := e.count(`SELECT count(*) FROM grants WHERE book_id = ?`, book.ID); n != 0 {
				t.Errorf("%d grants left on the book", n)
			}
			if c := status(rosa, "/books/"+book.ID); c != 404 {
				t.Errorf("after stop sharing: %d, want 404", c)
			}
		})
	}
}
