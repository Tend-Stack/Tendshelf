//go:build dev

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

// fixedSignInToken stands in for the random token in every capture, so two runs give the same pixels.
var fixedSignInToken = strings.Repeat("Lm9-", 11)[:43]

var signInUntilRe = regexp.MustCompile(`until [A-Z][a-z]{2} \d{1,2} [A-Z][a-z]{2}, \d\d:\d\d`)

// TestVisualSignIn captures the sign-in link pages (decision 0030) at 390, 768, 1024 and 1440 in the
// themes of visualMatrix: the confirm page, the page for a link that does not work, the new-password
// page, the person's page with the one-time panel (and the QR) and the person's page without it. The
// token and the "until" time are replaced by fixed ones (the QR is drawn again for the fixed address),
// so a second run is pixel-identical.
func TestVisualSignIn(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	e := inviteNewWeb(t)
	a := e.a
	e.exec(`UPDATE users SET display_name = 'Ana Reyes' WHERE id = ?`, e.ownerID)
	e.exec(`INSERT OR REPLACE INTO settings(key, value_json) VALUES ('server.name', '"The Reyes family shelf"')`)
	rosa, _ := e.person(users.RoleMember)
	e.exec(`UPDATE users SET display_name = 'Rosa Vega', username = 'rosa' WHERE id = ?`, rosa)
	hash, err := a.srv.users.HashPassword(t.Context(), "an old password 1")
	if err != nil {
		t.Fatal(err)
	}
	e.exec(`UPDATE users SET password_hash = ? WHERE id = ?`, hash, rosa)
	_, confirmTok := e.makeLink(e.owner, rosa)

	// A second person spends a link of her own, so /me/new-password opens.
	ana, _ := e.person(users.RoleMember)
	e.exec(`UPDATE users SET display_name = 'Ana Lund', username = 'analund', password_hash = ? WHERE id = ?`, hash, ana)
	_, spendTok := e.makeLink(e.owner, ana)
	_, linked := e.spendAnon(spendTok)
	if linked == nil {
		t.Fatal("no link session")
	}

	var calls atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		r.Host = "shelf.example.org"
		r.RemoteAddr = fmt.Sprintf("198.51.100.%d:4000", 1+calls.Add(1)%250) // each capture is a new visitor: no failure limit
		if theme := q.Get("theme"); theme != "" {
			r.AddCookie(&http.Cookie{Name: themeCookie, Value: theme})
		}
		switch q.Get("as") {
		case "owner":
			r.AddCookie(&http.Cookie{Name: e.owner.Name, Value: e.owner.Value})
		case "linked":
			r.AddCookie(&http.Cookie{Name: linked.Name, Value: linked.Value})
		}
		if r.URL.Path == "/vis/panel" { // the person's page after "Make a sign-in link"
			form := url.Values{"_csrf": {a.tokenFor(t, e.owner)}}
			req := httptest.NewRequest(http.MethodPost, "/admin/users/"+rosa+"/signin-link", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			req.AddCookie(e.owner)
			req.Host = r.Host
			req.RemoteAddr = r.RemoteAddr
			if c, err := r.Cookie(themeCookie); err == nil {
				req.AddCookie(c)
			}
			r = req
		}
		rec := httptest.NewRecorder()
		a.h.ServeHTTP(rec, r)
		body := rec.Body.String()
		if strings.HasPrefix(rec.Header().Get("Content-Type"), "text/html") {
			for _, m := range signInTokenRe.FindAllStringSubmatch(body, -1) {
				body = strings.ReplaceAll(body, m[1], fixedSignInToken)
			}
			body = signInUntilRe.ReplaceAllString(body, "until Sat 8 Nov, 09:00")
			link := "http://shelf.example.org/signin/" + fixedSignInToken
			if strings.Contains(body, link) {
				if v, ok := qrView(link, ""); ok {
					body = regexp.MustCompile(`(<path class="ts-qr__ink" d=")[^"]*(")`).ReplaceAllString(body, "${1}"+v.Path+"${2}")
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
		panel      bool
	}{
		{"confirm", "/signin/" + confirmTok, false},
		{"broken", "/signin/" + strings.Repeat("A", 43), false},
		{"new-password", "/me/new-password?as=linked", false},
		{"person-panel", "/vis/panel?as=owner", true},
		{"person", "/admin/users/" + rosa + "?as=owner", false},
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
				// The grant form's two checkboxes (us-dl, us-rd) are older than sign-in links and are 32 px by design.
				m.Small = slices.DeleteFunc(m.Small, func(s string) bool { return strings.HasPrefix(s, "us-dl:") || strings.HasPrefix(s, "us-rd:") })
				// The new-password page's username field is a clipped, aria-hidden input only a password manager reads.
				m.Small = slices.DeleteFunc(m.Small, func(s string) bool { return strings.HasPrefix(s, "ts-sr:") })
				m.Unlabelled = slices.DeleteFunc(m.Unlabelled, func(s string) bool { return s == "" && pg.name == "new-password" })
				if len(m.Small) > 0 {
					t.Errorf("targets under 44 px: %v", m.Small)
				}
				if len(m.Unlabelled) > 0 {
					t.Errorf("inputs without a label: %v", m.Unlabelled)
				}
				if m.H1 != 1 {
					t.Errorf("%d h1 elements", m.H1)
				}
				if pg.panel {
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
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("signin-%s-%s-%d.png", pg.name, theme, w)))
			})
		}
	}
	t.Logf("screenshots in %s", out)
}
