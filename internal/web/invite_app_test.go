package web

import (
	"context"
	"html"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

var androidUA = func(r *http.Request) {
	r.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit/537.36 Chrome/120 Mobile Safari/537.36")
}

// TestInviteAppPage: the invite page gains "Open in the app" on an Android browser, a QR in a closed
// <details> on a computer, and nothing in the app; the button and the form stay as they were.
func TestInviteAppPage(t *testing.T) {
	e := inviteNewWeb(t)
	_, tok := e.memberInvite(e.ownerID)
	link := "http://example.com/invite/" + tok
	wantIntent := "intent://open?link=" + url.QueryEscape(link) + "#Intent;scheme=tendshelf;package=app.tendshelf.android;S.browser_fallback_url=" + url.QueryEscape(link+"?noapp=1") + ";end"

	get := func(path string, mod func(*http.Request), cks ...*http.Cookie) string {
		rec := e.a.do(http.MethodGet, path, nil, mod, cks...)
		usersWant(t, rec, http.StatusOK, path)
		return html.UnescapeString(rec.Body.String())
	}
	phone := get("/invite/"+tok, androidUA)
	if !strings.Contains(phone, `href="`+wantIntent+`"`) || !strings.Contains(phone, "Open in the app") || !strings.Contains(phone, `hx-boost="false"`) {
		t.Errorf("android page lacks the exact intent address %q", wantIntent)
	}
	if strings.Contains(phone, "<details") || strings.Contains(phone, "ts-qr") {
		t.Error("a phone's browser is offered a QR")
	}
	if !strings.Contains(phone, "Start listening") {
		t.Error("the join button is gone")
	}
	noapp := get("/invite/"+tok+"?noapp=1", androidUA)
	if strings.Contains(noapp, "Open in the app") || !strings.Contains(noapp, "The Tendshelf app isn't on this phone yet. You can join here in your browser.") || !strings.Contains(noapp, "Start listening") {
		t.Error("noapp page")
	}
	desk := get("/invite/"+tok, nil)
	if strings.Contains(desk, "Open in the app") || !strings.Contains(desk, "<details") || !strings.Contains(desk, "Continue on your phone") || !strings.Contains(desk, `class="ts-qr"`) {
		t.Error("desktop page lacks the QR in details")
	}
	if strings.Contains(desk, "<details open") {
		t.Error("the details start open")
	}
	inApp := get("/invite/"+tok, androidUA, a1Android)
	if strings.Contains(inApp, "Open in the app") || strings.Contains(inApp, "ts-qr") || strings.Contains(inApp, "intent://") {
		t.Error("the app's own window offers the handover")
	}
	local := get("/invite/"+tok, func(r *http.Request) { r.Host = "localhost:8095" })
	if strings.Contains(local, "ts-qr") || !strings.Contains(local, "Start listening") {
		t.Error("a loopback address shows a QR")
	}
}

// TestInviteAppBoundSession: a connected app (its session is bound to a device) cannot join an
// invitation, so the WebView and the token stay one person. No form, 409 on POST, no account made,
// the invite unused.
func TestInviteAppBoundSession(t *testing.T) {
	e := inviteNewWeb(t)
	inv, tok := e.memberInvite(e.ownerID)
	ph := newPhone(t, e.a, true)
	bindSession(t, e.a, ph)
	users0 := e.a.count(t, `SELECT COUNT(*) FROM users`)
	sessions0 := e.a.count(t, `SELECT COUNT(*) FROM sessions`)

	page := ph.do(http.MethodGet, "/invite/"+tok, nil, nil)
	body := html.UnescapeString(page.Body.String())
	if page.Code != http.StatusOK || strings.Contains(body, `action="/invite/`) || !strings.Contains(body, "This phone is signed in. Sign out first to use this invitation.") {
		t.Fatalf("bound page = %d", page.Code)
	}
	rec := ph.do(http.MethodPost, "/invite/"+tok, url.Values{"_csrf": {e.a.tokenFor(t, ph.jar[auth.CookieName])}}, nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("POST = %d", rec.Code)
	}
	if rec.Header().Get("Location") != "" || cookieNamed(rec, "ts_session") != nil {
		t.Error("the refused POST set a session or redirected")
	}
	if e.a.count(t, `SELECT COUNT(*) FROM users`) != users0 || e.a.count(t, `SELECT COUNT(*) FROM sessions`) != sessions0 {
		t.Error("a bound session made an account or a session")
	}
	if got, err := e.a.srv.invites.Lookup(context.Background(), tok); err != nil || got.ID != inv.ID || got.State(e.a.srv.now().UnixMilli()) != share.InvitePending {
		t.Errorf("the invite was used: %+v, %v", got, err)
	}
	// A browser (no device bound) still joins as before.
	if rec := e.accept(tok); rec.Code != http.StatusSeeOther {
		t.Errorf("a plain browser join = %d", rec.Code)
	}
}

// TestInviteAppQRInPanel: the new-link panel gains a QR on an address a phone can reach, and only the
// link on a loopback address; share links never get one.
func TestInviteAppQRInPanel(t *testing.T) {
	e := inviteNewWeb(t)
	member, _ := e.memberInvite(e.ownerID)
	tok := e.a.tokenFor(t, e.owner)
	post := func(host string) string {
		rec := e.a.do(http.MethodPost, "/admin/invites/"+member.ID+"/link", url.Values{"_csrf": {tok}}, func(r *http.Request) {
			if host != "" {
				r.Host = host
			}
		}, e.owner)
		usersWant(t, rec, http.StatusOK, "rotate")
		return rec.Body.String()
	}
	if body := post(""); !strings.Contains(body, `class="ts-qr"`) || !strings.Contains(body, "Copy link") {
		t.Error("the panel lacks the QR")
	}
	if body := post("localhost:8095"); strings.Contains(body, `class="ts-qr"`) || !strings.Contains(body, `value="http://localhost:8095/invite/`) {
		t.Error("loopback: a QR, or no link")
	}
}
