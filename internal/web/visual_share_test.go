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

// shareProbeJS checks one Share page: no sideways overflow, every control at least 44 px (a radio
// counts through its card or chip, a switch through its row), every input labelled, one h1.
const shareProbeJS = `(() => {
  const small = [];
  for (const el of document.querySelectorAll("main input:not([type=hidden]), main button, main a.ts-btn, main select, main textarea")) {
    const box = el.closest("label, .ts-share__choice, .ts-switch-row") || el;
    const r = box.getBoundingClientRect();
    if (r.width > 0 && r.height < 43.5) small.push((el.id || el.name || el.className || el.tagName) + ":" + Math.round(r.height));
  }
  const labels = [...document.querySelectorAll("main input:not([type=hidden]), main select, main textarea")].filter(i =>
    !i.closest("label") && !i.getAttribute("aria-label") && !document.querySelector('label[for="' + i.id + '"]')).map(i => i.id || i.name);
  return {over: document.documentElement.scrollWidth > innerWidth, small, unlabelled: labels, h1: document.querySelectorAll("h1").length};
})()`

// TestVisualSharePages captures /share and a book's share page in the visualMatrix themes and widths:
// empty, with people and a waiting invite, and with a created link shown. The five Share boards are
// copied beside the captures for a side-by-side look.
func TestVisualSharePages(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	e := inviteNewWeb(t)
	e.exec(`UPDATE users SET display_name = 'Daniel Reyes' WHERE id = ?`, e.ownerID)
	for _, n := range []string{"SharePage", "ShareTablet", "SharePhone", "ShareForm", "SharePeople"} {
		src, err := os.Open(filepath.Join("..", "..", "docs", "design", "tendshelf-handoff", "screens", "people", n+".png"))
		if err != nil {
			continue
		}
		if dst, err := os.Create(filepath.Join(out, "share-board-"+n+".png")); err == nil {
			_, _ = io.Copy(dst, src)
			_ = dst.Close()
		}
		_ = src.Close()
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if theme := r.URL.Query().Get("theme"); theme != "" {
			r.AddCookie(&http.Cookie{Name: themeCookie, Value: theme})
		}
		r.AddCookie(&http.Cookie{Name: e.owner.Name, Value: e.owner.Value})
		e.a.h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	book := "/books/" + e.books[0].ID + "/share"

	run := func(state, path, fill string) {
		for _, sh := range visualMatrix(true) {
			theme, w := sh.Theme, sh.Width
			slug := "share"
			if strings.HasPrefix(path, "/books/") {
				slug = "bookshare"
			}
			t.Run(fmt.Sprintf("%s-%s-%s-%d", slug, state, theme, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w == 390, false)
				tab.Navigate(srv.URL + path + "?theme=" + theme)
				tab.WaitFor(`document.querySelector(".ts-share") !== null`, 10*time.Second)
				if fill != "" {
					inviteFill(tab, "#who", fill)
					if slug == "share" {
						playerClickSel(t, tab, `label[for="scope-all"]`)
					}
					playerClickSel(t, tab, `.ts-share__create button`)
					tab.WaitFor(`document.querySelector("#invite-link") !== null`, 10*time.Second)
					time.Sleep(time.Second) // the page swap cross-fades; capture once it has settled
				}
				var m struct {
					Over       bool     `json:"over"`
					Small      []string `json:"small"`
					Unlabelled []string `json:"unlabelled"`
					H1         int      `json:"h1"`
				}
				tab.Eval(shareProbeJS, &m)
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
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("%s-%s-%s-%d.png", slug, state, theme, w)))
			})
		}
	}

	run("empty", "/share", "")
	run("empty", book, "")

	// People: Rosa on one book with a shelf-less grant, Maya on everything, Lena on the library, and
	// one invite waiting for the first book.
	for name, spec := range map[string]struct {
		role  users.Role
		scope string
	}{"Rosa Reyes": {users.RoleGuest, "book"}, "Maya Chen": {users.RoleMember, "all"}, "Lena Reyes": {users.RoleGuest, "library"}} {
		id, _ := e.person(spec.role)
		e.exec(`UPDATE users SET display_name = ? WHERE id = ?`, name, id)
		switch spec.scope {
		case "all":
			e.exec(`INSERT INTO grants(id, user_id, scope, created_at, updated_at) VALUES ('g-'||?, ?, 'all', 1, 1)`, id, id)
		case "library":
			e.exec(`INSERT INTO grants(id, user_id, scope, library_id, can_download, created_at, updated_at) VALUES ('g-'||?, ?, 'library', ?, 1, 1, 1)`, id, id, e.lib.ID)
		default:
			e.exec(`INSERT INTO grants(id, user_id, scope, book_id, can_download, created_at, updated_at) VALUES ('g-'||?, ?, 'book', ?, 1, 1, 1)`, id, id, e.books[0].ID)
		}
	}
	e.create(e.ownerID, share.InviteSpec{Name: "Inés", Note: "Start here.", Role: users.RoleGuest, ExpiresAt: time.Now().AddDate(0, 0, 5).UnixMilli(),
		Grants: []share.InviteGrant{{Scope: share.GrantBook, Target: e.books[0].ID, Download: true}}})
	run("people", "/share", "")
	run("people", book, "")
	run("created", book, "Mamá")
	run("created", "/share", "Tío")
	t.Logf("screenshots in %s", out)
}
