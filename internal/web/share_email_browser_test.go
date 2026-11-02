//go:build dev

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
	"github.com/Tend-Stack/tendshelf/internal/testutil/smtpfake"
)

// mailServer serves the app with the owner's session in every request, for one browser.
func mailServer(t *testing.T, e *mailWeb) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		if theme := r.URL.Query().Get("theme"); theme != "" {
			r.AddCookie(&http.Cookie{Name: themeCookie, Value: theme})
		}
		r.AddCookie(&http.Cookie{Name: e.owner.Name, Value: e.owner.Value})
		e.a.h.ServeHTTP(rw, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestShareEmailOwnAppBrowser: with no mail server set up, "Send invitation" makes the invite and
// navigates to a mailto: address with the subject and message; Copy link and Copy message put the
// right text on the clipboard; and without script the answer still offers the email app and the text.
func TestShareEmailOwnAppBrowser(t *testing.T) {
	for _, w := range []int{390, 1440} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			b := chromium(t)
			e := mailNewWeb(t, smtpfake.Options{})
			srv := mailServer(t, e)
			tab := b.NewTab(w, 900, w < 768, false)
			// Clipboard access for this origin (the test reads back what the button wrote).
			_ = tab.Call("Browser.grantPermissions", map[string]any{"origin": srv.URL, "permissions": []string{"clipboardReadWrite", "clipboardSanitizedWrite"}}, nil)
			tab.Navigate(srv.URL + "/share?via=email")
			tab.WaitFor(`document.querySelector("#share-email") !== null`, 10*time.Second)

			// The tabs are links, at least 44 px high.
			var tabs struct {
				N   int     `json:"n"`
				Min float64 `json:"min"`
			}
			tab.Eval(`(() => { const t = [...document.querySelectorAll("a.ts-share__tab")]; return {n: t.length, min: Math.min(...t.map(a => a.getBoundingClientRect().height))}; })()`, &tabs)
			if tabs.N != 2 || tabs.Min < 43.5 {
				t.Errorf("tabs: %d links, smallest %.1f px", tabs.N, tabs.Min)
			}
			var m struct {
				Over       bool     `json:"over"`
				Small      []string `json:"small"`
				Unlabelled []string `json:"unlabelled"`
			}
			tab.Eval(shareProbeJS, &m)
			if m.Over || len(m.Small) > 0 || len(m.Unlabelled) > 0 {
				t.Errorf("email tab: overflow %v, small %v, unlabelled %v", m.Over, m.Small, m.Unlabelled)
			}

			// Catch the navigation to mailto: instead of leaving the page.
			tab.Eval(`window.__mailto = ""; navigation.addEventListener("navigate", (e) => { if (e.destination.url.startsWith("mailto:")) { window.__mailto = e.destination.url; e.preventDefault(); } })`, nil)
			inviteFill(tab, "#who", "Rosa Reyes")
			inviteFill(tab, "#share-email", "rosa@example.org")
			inviteFill(tab, "#msg", "Come & listen? #1 — café\nSecond line")
			playerClickSel(t, tab, `label[for="scope-all"]`)
			playerClickSel(t, tab, `.ts-share__create button[value=app]`)
			tab.WaitFor(`document.querySelector("#share-link") !== null && window.__mailto !== ""`, 10*time.Second)
			var mailto, link, message string
			tab.Eval(`window.__mailto`, &mailto)
			tab.Eval(`document.querySelector("#share-link").value`, &link)
			tab.Eval(`document.querySelector("#share-message").value`, &message)
			rest := strings.TrimPrefix(mailto, "mailto:")
			addr, query, _ := strings.Cut(rest, "?")
			q, err := url.ParseQuery(query)
			if err != nil || addr != "rosa@example.org" {
				t.Fatalf("mailto = %q (%v)", mailto, err)
			}
			if !strings.Contains(q.Get("subject"), "invited you") || !strings.Contains(q.Get("body"), link) ||
				!strings.Contains(q.Get("body"), "Come & listen? #1 — café") || !strings.Contains(message, link) {
				t.Errorf("mailto subject %q, body %q (message %q, link %q)", q.Get("subject"), q.Get("body"), message, link)
			}

			// Copy link and Copy message: visible now, and the clipboard holds the right text.
			for _, c := range []struct{ button, want string }{{`[data-copy="#share-link"]`, link}, {`[data-copy="#share-message"]`, message}} {
				var shown bool
				tab.Eval(`getComputedStyle(document.querySelector(`+cdp.Quote(c.button)+`)).display !== "none"`, &shown)
				if !shown {
					t.Fatalf("%s is still hidden", c.button)
				}
				playerClickSel(t, tab, c.button)
				tab.WaitFor(`document.querySelector("[data-copy-status]").textContent === "Copied"`, 5*time.Second)
				var got string
				if err := tab.TryEval(`navigator.clipboard.readText()`, &got); err != nil {
					t.Fatalf("read clipboard: %v", err)
				}
				if got != c.want {
					t.Errorf("%s copied %q, want %q", c.button, got, c.want)
				}
				tab.Eval(`document.querySelector("[data-copy-status]").textContent = ""`, nil)
			}
			var small []string
			tab.Eval(`[...document.querySelectorAll(".ts-share__mailpanel .ts-btn")].filter(b => b.getBoundingClientRect().height < 43.5).map(b => b.textContent.trim())`, &small)
			if len(small) > 0 {
				t.Errorf("buttons under 44 px: %v", small)
			}

			// Without script: the same answer from a plain form post.
			tab.Navigate(srv.URL + "/share?via=email")
			tab.WaitFor(`document.querySelector("#share-email") !== null`, 10*time.Second)
			inviteFill(tab, "#who", "Tío")
			inviteFill(tab, "#share-email", "tio@example.org")
			tab.Eval(`document.querySelector("#scope-all").checked = true`, nil)
			tab.Eval(`(() => { const f = document.querySelector("#share-email").form; const h = document.createElement("input"); h.type = "hidden"; h.name = "how"; h.value = "app"; f.append(h); })()`, nil)
			if err := tab.Call("Emulation.setScriptExecutionDisabled", map[string]any{"value": true}, nil); err != nil {
				t.Fatal(err)
			}
			tab.Eval(`HTMLFormElement.prototype.submit.call(document.querySelector("#share-email").form)`, nil)
			tab.WaitFor(`document.querySelector("#share-link") !== null`, 10*time.Second)
			var plain struct {
				Href, Link, Message string
				CopyShown           bool
			}
			tab.Eval(`({Href: document.querySelector("#share-mailto").getAttribute("href"), Link: document.querySelector("#share-link").value,
			  Message: document.querySelector("#share-message").value, CopyShown: [...document.querySelectorAll("[data-copy]")].some(b => getComputedStyle(b).display !== "none")})`, &plain)
			if !strings.HasPrefix(plain.Href, "mailto:tio@example.org?") || !strings.Contains(plain.Message, plain.Link) || plain.CopyShown {
				t.Errorf("without script: %+v", plain)
			}
		})
	}
}

// TestShareEmailRelayBrowser: the owner fills the settings from a preset, saves, sends a test, then
// "Send it for me" and the invite reads "Sent to ...".
func TestShareEmailRelayBrowser(t *testing.T) {
	for _, w := range []int{390, 1440} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			b := chromium(t)
			e := mailNewWeb(t, smtpfake.Options{User: "mailer", Pass: mailTestPassword})
			srv := mailServer(t, e)
			tab := b.NewTab(w, 900, w < 768, false)
			wait := func(js string) { t.Helper(); tab.WaitFor(js, 15*time.Second) }

			// A preset fills the server, port and security; the person can still change every field.
			tab.Navigate(srv.URL + "/admin/email")
			wait(`document.querySelector("#email-provider") !== null`)
			tab.Eval(`document.querySelector("#email-provider").value = "fastmail"`, nil)
			playerClickSel(t, tab, `.ts-email__fill button`)
			wait(`document.querySelector("#email-host").value === "smtp.fastmail.com"`)
			var filled struct{ Port, TLS string }
			tab.Eval(`({Port: document.querySelector("#email-port").value, TLS: document.querySelector("#email-tls").value})`, &filled)
			if filled.Port != "465" || filled.TLS != "tls" {
				t.Errorf("fastmail preset filled port %q, security %q", filled.Port, filled.TLS)
			}
			var over bool
			tab.Eval(`document.documentElement.scrollWidth > innerWidth`, &over)
			if over {
				t.Error("settings page overflows sideways")
			}
			inviteFill(tab, "#email-host", e.fake.Host())
			inviteFill(tab, "#email-port", strconv.Itoa(e.fake.Port()))
			tab.Eval(`document.querySelector("#email-tls").value = "starttls"`, nil)
			inviteFill(tab, "#email-user", "mailer")
			inviteFill(tab, "#email-pass", mailTestPassword)
			inviteFill(tab, "#email-from-name", "Home")
			inviteFill(tab, "#email-from", "noreply@example.org")
			playerClickSel(t, tab, `form.ts-email__form button[type=submit]`)
			wait(`document.querySelector("#email-test-to") !== null`)
			inviteFill(tab, "#email-test-to", "me@example.org")
			playerClickSel(t, tab, `.ts-email__testbox button[type=submit]`)
			wait(`document.querySelector("#email-test") !== null && document.querySelector("#email-test").textContent.includes("Sent.")`)

			// "Send it for me" is offered now; the row reads "Sent to ..." once the job has run.
			tab.Navigate(srv.URL + "/share?via=email")
			wait(`document.querySelector('button[value=relay]') !== null`)
			inviteFill(tab, "#who", "Rosa")
			inviteFill(tab, "#share-email", "rosa@example.org")
			playerClickSel(t, tab, `label[for="scope-all"]`)
			playerClickSel(t, tab, `button[value=relay]`)
			wait(`document.querySelector("#share-link") !== null && !document.querySelector("a[data-mailto-auto]")`)
			sent := false
			for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && !sent; {
				tab.Navigate(srv.URL + "/share")
				tab.Eval(`document.body.innerText.includes("Sent to rosa@example.org")`, &sent)
				time.Sleep(200 * time.Millisecond)
			}
			if !sent {
				t.Error(`the invite never read "Sent to rosa@example.org"`)
			}
			if n := len(e.fake.Messages()); n != 2 {
				t.Errorf("the server received %d messages, want the test and the invite", n)
			}
		})
	}
}

// TestShareEmailRelayFailureBrowser: a relay that refuses the address leaves "Couldn't send" on the invite.
func TestShareEmailRelayFailureBrowser(t *testing.T) {
	b := chromium(t)
	e := mailNewWeb(t, smtpfake.Options{User: "mailer", Pass: mailTestPassword})
	e.ready()
	// From here the server refuses every recipient.
	e.fake.SetFail(map[string]int{"rcpt": 550})
	srv := mailServer(t, e)
	tab := b.NewTab(1440, 900, false, false)
	tab.Navigate(srv.URL + "/share?via=email")
	tab.WaitFor(`document.querySelector('button[value=relay]') !== null`, 10*time.Second)
	inviteFill(tab, "#who", "Rosa")
	inviteFill(tab, "#share-email", "rosa@example.org")
	playerClickSel(t, tab, `label[for="scope-all"]`)
	playerClickSel(t, tab, `button[value=relay]`)
	tab.WaitFor(`document.querySelector("#share-link") !== null`, 10*time.Second)
	failed := false
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline) && !failed; {
		tab.Navigate(srv.URL + "/share")
		tab.Eval(`document.body.innerText.includes("Couldn't send to rosa@example.org")`, &failed)
		time.Sleep(200 * time.Millisecond)
	}
	if !failed {
		t.Error(`the invite never read "Couldn't send"`)
	}
	var styled bool
	tab.Eval(`document.querySelector(".ts-share__mail--failed") !== null`, &styled)
	if !styled {
		t.Error("the failed line has no failed style hook")
	}
}
