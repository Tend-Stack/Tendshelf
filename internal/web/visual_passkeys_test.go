//go:build dev

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/users"
)

// TestVisualPasskeys captures the passkey pages (decision 0041) at 390, 768, 1024 and 1440 in the
// themes of visualMatrix: the sign-in page with its button (the script shows it on localhost), the
// Passkeys page with two rows (one synced and used, one on one device and marked as maybe copied), the
// empty list, and the page where passkeys cannot be added. Times are fixed and older than a week, so
// the words on screen never change from day to day.
func TestVisualPasskeys(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	e := usersNewEnv(t)
	a := e.a
	e.exec(`UPDATE users SET display_name = 'Ana Reyes' WHERE id = ?`, e.ownerID)
	e.exec(`INSERT OR REPLACE INTO settings(key, value_json) VALUES ('server.name', '"The Reyes family shelf"')`)
	_, rosaCk := e.person(users.RoleMember)
	for i, row := range []struct {
		name         string
		eligible     int
		created, use any
		copied       any
	}{
		{"Phone", 1, int64(1762300800000), int64(1762387200000), nil},
		{"Work laptop with a rather long name that must wrap inside the row", 0, int64(1762214400000), nil, int64(1762473600000)},
	} {
		e.exec(`INSERT INTO passkeys (id, user_id, credential_id, rp_id, public_key, alg, sign_count, backup_eligible, backed_up, transports, name, created_at, last_used_at, copied_at)
			VALUES (?, ?, ?, 'localhost', zeroblob(77), -7, 0, ?, ?, 'internal', ?, ?, ?, ?)`,
			fmt.Sprintf("pk_vis%d", i+1), e.ownerID, []byte(fmt.Sprintf("visual-credential-%02d-0123456789", i+1)), row.eligible, row.eligible, row.name, row.created, row.use, row.copied)
	}

	var calls atomic.Int64
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		r.Host = "localhost"
		if q.Get("host") == "ip" {
			r.Host = "127.0.0.1"
		}
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
		{"me", "/me/passkeys?as=owner"},
		{"me-empty", "/me/passkeys?as=rosa"},
		{"me-ip", "/me/passkeys?as=owner&host=ip"},
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
				if len(m.Small) > 0 {
					t.Errorf("targets under 44 px: %v", m.Small)
				}
				if len(m.Unlabelled) > 0 {
					t.Errorf("inputs without a label: %v", m.Unlabelled)
				}
				if m.H1 != 1 {
					t.Errorf("%d h1 elements", m.H1)
				}
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("passkeys-%s-%s-%d.png", pg.name, theme, w)))
			})
		}
	}
	t.Logf("screenshots in %s", out)
}
