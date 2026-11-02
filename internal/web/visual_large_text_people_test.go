//go:build dev

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/share"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// TestVisualLargeTextPeople covers the invite and link pages (P3-11). The invite page and the open link
// page are signed out, so they never take the setting: they are captured with the root at 125 %, the way
// a browser's own larger text looks. The Users page with its invites and a book's Share page (links
// listed) are signed in, so they take Larger.
func TestVisualLargeTextPeople(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")

	e := inviteNewWeb(t)
	e.coverBook(e.books[0])
	e.exec(`UPDATE users SET display_name = 'Daniel Reyes' WHERE id = ?`, e.ownerID)
	_, tokOpen := e.create(e.ownerID, share.InviteSpec{
		Name: "Rosa", Note: "Mamá, start with this one. The narrator sounds like Papá.", Role: users.RoleMember,
		Grants: []share.InviteGrant{{Scope: share.GrantLibrary, Target: e.lib.ID, Download: true}},
	})
	ltSetSize(t, e.a, e.ownerID, true)
	inviteSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if theme := r.URL.Query().Get("theme"); theme != "" {
			r.AddCookie(&http.Cookie{Name: themeCookie, Value: theme})
		}
		if strings.HasPrefix(r.URL.Path, "/admin") {
			r.AddCookie(&http.Cookie{Name: e.owner.Name, Value: e.owner.Value})
		}
		e.a.h.ServeHTTP(w, r)
	}))
	t.Cleanup(inviteSrv.Close)

	l := linkNewEnv(t)
	_, open := l.make(l.book.ID, 7, "")
	_, locked := l.make(l.book.ID, 30, "a long quiet password")
	ltSetSize(t, l.a, l.ownerUser.ID, true)
	linkSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if theme := r.URL.Query().Get("theme"); theme != "" {
			r.AddCookie(&http.Cookie{Name: themeCookie, Value: theme})
		}
		if !strings.HasPrefix(r.URL.Path, "/s/") {
			r.AddCookie(&http.Cookie{Name: l.owner.Name, Value: l.owner.Value})
		}
		l.a.h.ServeHTTP(w, r)
	}))
	t.Cleanup(linkSrv.Close)

	pages := []ltPage{
		{"invite", inviteSrv.URL, "/invite/" + tokOpen, "", true},
		{"invite-users", inviteSrv.URL, "/admin/users", "", false},
		{"link-open", linkSrv.URL, "/s/" + open, "", true},
		{"link-locked", linkSrv.URL, "/s/" + locked, `document.querySelector("#link-password") !== null`, true},
		{"link-share", linkSrv.URL, "/books/" + l.book.ID + "/share", `document.querySelector(".ts-share__links") !== null`, false},
	}
	for _, pg := range pages {
		for _, sh := range ltShots() {
			theme, w := sh.Theme, sh.Width
			t.Run(fmt.Sprintf("%s-%s-%d", pg.name, theme, w), func(t *testing.T) {
				ltCheck(t, b, pg, theme, w, "b", filepath.Join(out, fmt.Sprintf("largetext-%s-%s-%d.png", pg.name, theme, w)))
			})
		}
	}
}
