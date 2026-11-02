//go:build dev

package web

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// TestVisualInvitePages captures the invite page, its four other states, "Keep your place" and the
// Users page with pending invites in the theme and width combinations of visualMatrix. The two
// invite boards and the Users board are copied beside the captures for a side-by-side look. Each
// page is checked for overflow, targets under 44 px, unlabelled inputs and one h1.
func TestVisualInvitePages(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	e := inviteNewWeb(t)
	e.coverBook(e.books[0])
	e.exec(`UPDATE users SET display_name = 'Daniel Reyes' WHERE id = ?`, e.ownerID)
	e.exec(`INSERT OR REPLACE INTO settings(key, value_json) VALUES ('server.name', '"The Reyes family shelf"')`)

	for _, c := range []struct{ dir, name string }{{"people", "Invite"}, {"people", "InviteKeep"}, {"admin", "UsersPage"}} {
		src, err := os.Open(filepath.Join("..", "..", "docs", "design", "tendshelf-handoff", "screens", c.dir, c.name+".png"))
		if err != nil {
			continue
		}
		if dst, err := os.Create(filepath.Join(out, "invite-board-"+c.name+".png")); err == nil {
			_, _ = io.Copy(dst, src)
			_ = dst.Close()
		}
		_ = src.Close()
	}

	spec := func(name string) share.InviteSpec {
		return share.InviteSpec{
			Name: name, Note: "Mamá, start with this one. The narrator sounds like Papá.", Role: users.RoleMember,
			Grants: []share.InviteGrant{{Scope: share.GrantLibrary, Target: e.lib.ID, Download: true}},
		}
	}
	_, tokOpen := e.create(e.ownerID, spec("Rosa"))
	invEnded, tokEnded := e.create(e.ownerID, spec("Maya"))
	invUsed, tokUsed := e.create(e.ownerID, spec("Lena"))
	e.exec(`UPDATE invites SET created_at = 1, expires_at = ? WHERE id = ?`, time.Now().Add(-time.Hour).UnixMilli(), invEnded.ID)
	e.exec(`UPDATE invites SET use_count = max_uses WHERE id = ?`, invUsed.ID)
	_, tokWelcome := e.create(e.ownerID, spec("Rosa"))
	_, _ = e.create(e.ownerID, share.InviteSpec{Name: "Tomás", Role: users.RoleMember, ExpiresAt: time.Now().AddDate(0, 0, 14).UnixMilli(),
		Grants: []share.InviteGrant{{Scope: share.GrantBook, Target: e.books[1].ID}}})

	// Rosa accepts the last invite: her session opens "Keep your place".
	rec := e.accept(tokWelcome)
	if rec.Code != http.StatusSeeOther {
		t.Fatalf("accept: %d", rec.Code)
	}
	rosaWelcome := rec.Header().Get("Location")
	var rosaCookies []*http.Cookie
	rosaCookies = append(rosaCookies, rec.Result().Cookies()...)

	// One server: the path decides who is signed in.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if theme := r.URL.Query().Get("theme"); theme != "" {
			r.AddCookie(&http.Cookie{Name: themeCookie, Value: theme})
		}
		switch {
		case strings.HasPrefix(r.URL.Path, "/welcome"):
			for _, c := range rosaCookies {
				r.AddCookie(c)
			}
		case strings.HasPrefix(r.URL.Path, "/admin"), r.URL.Query().Get("as") == "owner":
			r.AddCookie(&http.Cookie{Name: e.owner.Name, Value: e.owner.Value})
		}
		e.a.h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	pgs := []struct{ name, path string }{
		{"invite", "/invite/" + tokOpen},
		{"ended", "/invite/" + tokEnded},
		{"used", "/invite/" + tokUsed},
		{"unknown", "/invite/not-a-real-link-0000000000000000000000000"},
		{"signedin", "/invite/" + tokOpen + "?as=owner"},
		{"keep", rosaWelcome},
		{"users", "/admin/users#users-invites"},
	}
	for _, pg := range pgs {
		for _, sh := range visualMatrix(true) {
			theme, w := sh.Theme, sh.Width
			t.Run(fmt.Sprintf("%s-%s-%d", pg.name, theme, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w == 390, false)
				path, frag, _ := strings.Cut(pg.path, "#")
				sep := "?"
				if strings.Contains(path, "?") {
					sep = "&"
				}
				u := srv.URL + path + sep + "theme=" + theme
				if frag != "" {
					u += "#" + frag
				}
				tab.Navigate(u)
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
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("invite-%s-%s-%d.png", pg.name, theme, w)))
			})
		}
	}
	t.Logf("screenshots in %s", out)
}
