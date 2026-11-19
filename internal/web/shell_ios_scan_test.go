package web

import (
	"html"
	"net/http"
	"strings"
	"testing"
)

// I4a (decision 0048, 0028 addendum): an iPhone browser on a pair or invite link is told to scan the code in the
// app, never given an intent: or tendshelf: address. Names are prefixed TestIOSScan.

var iPhoneUA = func(r *http.Request) {
	r.Header.Set("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Version/18.0 Mobile/15E148 Safari/604.1")
}

func TestIOSScanPairPage(t *testing.T) {
	a, _ := newPairApp(t)
	s := pairStart(t, a, a.owner(t))
	br := newPhone(t, a, false)
	body := html.UnescapeString(br.do(http.MethodGet, "/pair/"+s.token, nil, iPhoneUA).Body.String())
	if !strings.Contains(body, pairIPhone) {
		t.Error("iPhone Safari lacks the scan sentence")
	}
	if strings.Contains(body, "intent:") || strings.Contains(body, "tendshelf:") || strings.Contains(body, pairHandoverOthers) {
		t.Error("iPhone Safari gets a handover address or the generic sentence")
	}
	if and := br.do(http.MethodGet, "/pair/"+s.token, nil, androidUA).Body.String(); !strings.Contains(and, "intent:") {
		t.Error("Android lost its intent address")
	}
	if desk := html.UnescapeString(br.do(http.MethodGet, "/pair/"+s.token, nil, nil).Body.String()); !strings.Contains(desk, pairHandoverOthers) || strings.Contains(desk, pairIPhone) {
		t.Error("desktop handover changed")
	}
	inApp := br.do(http.MethodGet, "/pair/"+s.token+"?challenge="+s.phoneCh.challenge, nil, func(r *http.Request) {
		iPhoneUA(r)
		r.AddCookie(a1IOS)
	})
	if strings.Contains(html.UnescapeString(inApp.Body.String()), pairIPhone) {
		t.Error("the iOS app's own page shows the scan note")
	}
}

func TestIOSScanInvitePage(t *testing.T) {
	e := inviteNewWeb(t)
	_, tok := e.memberInvite(e.ownerID)
	get := func(mod func(*http.Request), cks ...*http.Cookie) string {
		rec := e.a.do(http.MethodGet, "/invite/"+tok, nil, mod, cks...)
		usersWant(t, rec, http.StatusOK, "invite")
		return html.UnescapeString(rec.Body.String())
	}
	ph := get(iPhoneUA)
	if !strings.Contains(ph, inviteIPhone) || !strings.Contains(ph, "Start listening") {
		t.Error("iPhone Safari lacks the scan sentence or the join form")
	}
	if strings.Contains(ph, "intent:") || strings.Contains(ph, "tendshelf:") || strings.Contains(ph, "Open in the app") {
		t.Error("iPhone Safari gets a handover address")
	}
	if and := get(androidUA); !strings.Contains(and, "intent:") {
		t.Error("Android lost its intent address")
	}
	if desk := get(nil); !strings.Contains(desk, "Continue on your phone") || strings.Contains(desk, inviteIPhone) {
		t.Error("desktop invite changed")
	}
	if app := get(iPhoneUA, a1IOS); strings.Contains(app, inviteIPhone) {
		t.Error("the iOS app's own page shows the scan note")
	}
}
