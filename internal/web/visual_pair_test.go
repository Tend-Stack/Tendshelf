//go:build dev

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/auth"
)

// fixedPairToken stands in for the random token in every capture, so two runs give the same pixels.
var fixedPairToken = strings.Repeat("Qr7-", 11)[:43]

// qrFillProbeJS reads the colours a QR is painted with: they must be black on white in every theme.
const qrFillProbeJS = `(() => {
  const ink = document.querySelector(".ts-qr__ink"), paper = document.querySelector(".ts-qr__paper");
  if (!ink || !paper) return null;
  return {ink: getComputedStyle(ink).fill, paper: getComputedStyle(paper).fill, w: document.querySelector(".ts-qr").getBoundingClientRect().width};
})()`

// TestVisualPair captures the pages of adding a phone by QR and the QR on the invite pages, at 390, 768,
// 1024 and 1440 in the themes of visualMatrix. Random tokens and numbers in the answers are replaced
// by fixed ones (the QR is drawn again for the fixed address), so a second run is pixel-identical.
func TestVisualPair(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	e := inviteNewWeb(t)
	e.exec(`UPDATE users SET display_name = 'Ana Reyes' WHERE id = ?`, e.ownerID)
	e.exec(`INSERT OR REPLACE INTO settings(key, value_json) VALUES ('server.name', '"The Reyes family shelf"')`)
	a := e.a
	a.srv.pairLim = auth.NewLimiter(100000, time.Hour, 16, nil) // every capture starts a code
	_, inviteTok := e.memberInvite(e.ownerID)
	fixedInvite := strings.Repeat("Kp3_", 11)[:43]

	// Pairings in each state, started by the owner's session.
	waiting := pairStart(t, a, e.owner)
	claimedShowOwner := a.freshSession(t)
	claimed := pairStart(t, a, claimedShowOwner)
	claimedPhone := newPhone(t, a, true)
	claimed.claimed(claimedPhone)
	approved := pairStart(t, a, a.freshSession(t))
	approvedPhone := newPhone(t, a, true)
	approved.approved(approvedPhone)
	waitingToken := waiting.token

	var calls atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		r.Host = "shelf.example.org"                                         // a name a phone could reach, so the QR is drawn
		r.RemoteAddr = fmt.Sprintf("198.51.100.%d:4000", 1+calls.Add(1)%250) // each capture is a new visitor: no failure limit
		if theme := q.Get("theme"); theme != "" {
			r.AddCookie(&http.Cookie{Name: themeCookie, Value: theme})
		}
		if q.Get("shell") == "android" {
			r.AddCookie(a1Android)
		}
		if q.Get("ua") == "android" {
			r.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 14; Pixel 8) Chrome/120 Mobile")
		}
		var cookieOwner *http.Cookie
		switch q.Get("as") {
		case "owner":
			cookieOwner = e.owner
		case "claimed":
			cookieOwner = claimedShowOwner
		case "approved-phone":
			r.AddCookie(approvedPhone.jar[pairCookie])
		case "claimed-phone":
			r.AddCookie(claimedPhone.jar[pairCookie])
		}
		if cookieOwner != nil {
			r.AddCookie(&http.Cookie{Name: cookieOwner.Name, Value: cookieOwner.Value})
		}
		// The owner's "Show a code" page is a POST: run it here so a plain navigation opens it.
		if r.URL.Path == "/vis/owner-code" {
			tok := a.tokenFor(t, e.owner)
			req := httptest.NewRequest(http.MethodPost, "/me/devices/add", strings.NewReader(url.Values{"_csrf": {tok}}.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(e.owner)
			req.Host = r.Host
			if c, err := r.Cookie(themeCookie); err == nil {
				req.AddCookie(c)
			}
			r = req
		}
		if r.URL.Path == "/vis/invite-panel" {
			tok := a.tokenFor(t, e.owner)
			inv, _ := e.memberInvite(e.ownerID)
			req := httptest.NewRequest(http.MethodPost, "/admin/invites/"+inv.ID+"/link", strings.NewReader(url.Values{"_csrf": {tok}}.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(e.owner)
			req.Host = r.Host
			if c, err := r.Cookie(themeCookie); err == nil {
				req.AddCookie(c)
			}
			r = req
		}
		rec := httptest.NewRecorder()
		a.h.ServeHTTP(rec, r)
		body := rec.Body.String()
		host := r.Host
		if strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			// Fix the random parts: tokens in addresses and the number on the phone's page.
			for _, m := range pairTokenRe.FindAllStringSubmatch(body, -1) {
				body = strings.ReplaceAll(body, m[1], fixedPairToken)
			}
			for _, m := range regexp.MustCompile(`/invite/([A-Za-z0-9_-]{43})`).FindAllStringSubmatch(body, -1) {
				body = strings.ReplaceAll(body, m[1], fixedInvite)
			}
			if num := pairNumberRe.FindStringSubmatch(body); num != nil {
				body = strings.ReplaceAll(body, num[1], "4821")
			}
			// Draw the QR again for the fixed address.
			for _, link := range []string{"http://" + host + "/pair/" + fixedPairToken, "http://" + host + "/invite/" + fixedInvite} {
				if strings.Contains(body, `value="`+link+`"`) || strings.Contains(body, "action=\"/invite/"+fixedInvite) || strings.Contains(body, link) {
					if v, ok := qrView(link, ""); ok {
						body = regexp.MustCompile(`(<path class="ts-qr__ink" d=")[^"]*(")`).ReplaceAllString(body, "${1}"+v.Path+"${2}")
					}
				}
			}
		}
		for k, vs := range rec.Header() {
			if k != "Content-Length" {
				w.Header()[k] = vs
			}
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write([]byte(body))
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	pgs := []struct {
		name, path string
		qr         bool
		open       bool
	}{
		{"owner-intro", "/me/devices/add?as=owner", false, false},
		{"owner-code", "/vis/owner-code?as=owner", true, false},
		{"owner-claimed", "/me/devices/add/" + claimed.id + "?as=claimed", false, false},
		{"phone-handover", "/pair/" + waitingToken + "?ua=android", false, false},
		{"phone-confirm", "/pair/" + waitingToken + "?shell=android&challenge=" + a1Challenge + "&name=Pixel+8", false, false},
		{"phone-number", "/pair/wait?as=claimed-phone&shell=android", false, false},
		{"phone-connect", "/pair/wait?as=approved-phone&shell=android", false, false},
		{"phone-ended", "/pair/" + strings.Repeat("A", 43), false, false},
		{"invite-qr", "/invite/" + inviteTok, true, true},
		{"invite-android", "/invite/" + inviteTok + "?ua=android", false, false},
		{"invite-panel", "/vis/invite-panel?as=owner", true, false},
	}
	for _, pg := range pgs {
		for _, sh := range visualMatrix(true) {
			theme, w := sh.Theme, sh.Width
			t.Run(fmt.Sprintf("%s-%s-%d", pg.name, theme, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w == 390, false)
				sep := "?"
				if strings.Contains(pg.path, "?") {
					sep = "&"
				}
				tab.Navigate(srv.URL + pg.path + sep + "theme=" + theme)
				if pg.open {
					tab.Eval(`(() => { const d = document.querySelector("details"); if (d) d.open = true; })()`, nil)
				}
				var m struct {
					Over       bool     `json:"over"`
					Small      []string `json:"small"`
					Unlabelled []string `json:"unlabelled"`
					H1         int      `json:"h1"`
				}
				tab.Eval(inviteProbeJS, &m)
				if m.Over {
					t.Errorf("horizontal overflow")
				}
				if len(m.Small) > 0 {
					t.Errorf("targets under 44 px: %v", m.Small)
				}
				if len(m.Unlabelled) > 0 {
					t.Errorf("inputs without a label: %v", m.Unlabelled)
				}
				if pg.name != "invite-panel" && m.H1 != 1 {
					t.Errorf("%d h1 elements", m.H1)
				}
				if pg.qr {
					var f *struct {
						Ink   string  `json:"ink"`
						Paper string  `json:"paper"`
						W     float64 `json:"w"`
					}
					tab.Eval(qrFillProbeJS, &f)
					if f == nil || f.Ink != "rgb(0, 0, 0)" || f.Paper != "rgb(255, 255, 255)" || f.W < 120 {
						t.Errorf("QR paint = %+v, want black on white at least 120 px wide", f)
					}
				}
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("pair-%s-%s-%d.png", pg.name, theme, w)))
			})
		}
	}
	t.Logf("screenshots in %s", out)
}
