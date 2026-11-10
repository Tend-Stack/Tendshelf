//go:build dev

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/auth"
	"github.com/Tend-Stack/tendshelf/internal/auth/oidc"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// TestVisualOIDC captures sign-in with a provider (decision 0037) at 390, 768, 1024 and 1440 in the
// themes of visualMatrix: the sign-in page with the button (a short and a 38-letter provider name, which must be cut with an
// ellipsis and not wrapped), the owner's settings, the Account page unlinked and linked, and the confirm card. The
// provider is never called: the pages only read settings and rows, so the values on screen are fixed.
func TestVisualOIDC(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	o := newOIDCWeb(t, func(in *oidc.ConfigInput) {
		in.Name, in.Issuer, in.ServerAddress = "Authelia", "https://auth.example.org", "https://shelf.example.org"
	})
	e := o.usersEnv
	a := e.a
	e.exec(`UPDATE users SET display_name = 'Ana Reyes' WHERE id = ?`, e.ownerID)
	e.exec(`INSERT OR REPLACE INTO settings(key, value_json) VALUES ('server.name', '"The Reyes family shelf"')`)
	rosa, rosaCk := e.person(users.RoleMember)
	e.exec(`UPDATE users SET display_name = 'Rosa Vega', username = 'rosa' WHERE id = ?`, rosa)
	e.exec(`INSERT INTO oidc_identities (issuer, subject, user_id, email, linked_via, created_at) VALUES (?, 'sub-rosa', ?, 'rosa@example.org', 'account', ?)`,
		"https://auth.example.org", rosa, int64(1762560000000))
	ownerHash, _ := auth.HashToken(e.owner.Value)
	e.exec(`INSERT INTO oidc_logins (id, state_hash, purpose, issuer, nonce, verifier, next, user_id, session_hash, subject, email, email_verified, created_at, expires_at, used_at)
		VALUES ('oil_vis1', ?, 'link', 'https://auth.example.org', ?, ?, '/me/sso', ?, ?, 'sub-ana', 'ana@example.org', 1, ?, ?, ?)`,
		make([]byte, 32), strings.Repeat("n", 43), strings.Repeat("v", 43), e.ownerID, ownerHash[:], int64(4102444800000), int64(4102444800000), int64(4102444800000))

	var calls atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		r.Host = "shelf.example.org"
		r.RemoteAddr = fmt.Sprintf("198.51.100.%d:4000", 1+calls.Add(1)%250)
		if theme := q.Get("theme"); theme != "" {
			r.AddCookie(&http.Cookie{Name: themeCookie, Value: theme})
		}
		switch q.Get("as") {
		case "owner":
			r.AddCookie(&http.Cookie{Name: e.owner.Name, Value: e.owner.Value})
		case "rosa":
			r.AddCookie(&http.Cookie{Name: rosaCk.Name, Value: rosaCk.Value})
		}
		name := "Authelia"
		if q.Get("long") == "1" {
			name = "Reyes Family Identity Provider Service"
		}
		e.exec(`INSERT OR REPLACE INTO settings(key, value_json) VALUES ('oidc.name', ?)`, `"`+name+`"`)
		rec := httptest.NewRecorder()
		a.h.ServeHTTP(rec, r)
		for k, vs := range rec.Header() {
			w.Header()[k] = vs
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	})
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	pgs := []struct{ name, path string }{
		{"login", "/login"},
		{"login-long", "/login?long=1"},
		{"admin", "/admin/sso?as=owner"},
		{"me-unlinked", "/me/sso?as=owner"},
		{"me-linked", "/me/sso?as=rosa"},
		{"me-linked-long", "/me/sso?as=rosa&long=1"},
		{"confirm", "/me/sso/confirm?as=owner&id=oil_vis1"},
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
				// The switches are the design's 32 px control inside a row whose label is the 44 px target, as on the Libraries page.
				m.Small = slices.DeleteFunc(m.Small, func(s string) bool { return s == "" || strings.HasPrefix(s, "sso-") })
				if len(m.Small) > 0 {
					t.Errorf("targets under 44 px: %v", m.Small)
				}
				if len(m.Unlabelled) > 0 {
					t.Errorf("inputs without a label: %v", m.Unlabelled)
				}
				if m.H1 != 1 {
					t.Errorf("%d h1 elements", m.H1)
				}
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("oidc-%s-%s-%d.png", pg.name, theme, w)))
			})
		}
	}
	t.Logf("screenshots in %s", out)
}
