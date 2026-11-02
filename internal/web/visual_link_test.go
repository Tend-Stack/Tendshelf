//go:build dev

package web

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// linkProbeJS checks one page of a share link or the Share page: no sideways overflow, every visible
// control at least 44 px, every visible input labelled, one h1.
const linkProbeJS = `(() => {
  const small = [];
  for (const el of document.querySelectorAll("main input:not([type=hidden]), main button, main a.ts-btn, main summary")) {
    const box = el.closest("label, .ts-share__choice, .ts-switch-row") || el;
    const r = box.getBoundingClientRect();
    if (r.width > 0 && r.height < 43.5) small.push((el.id || el.name || el.className || el.tagName) + ":" + Math.round(r.height));
  }
  const labels = [...document.querySelectorAll("main input:not([type=hidden])")].filter(i =>
    !i.closest("label") && !i.getAttribute("aria-label") && !document.querySelector('label[for="' + i.id + '"]')).map(i => i.id || i.name);
  return {over: document.documentElement.scrollWidth > innerWidth, small, unlabelled: labels, h1: document.querySelectorAll("h1").length};
})()`

// TestVisualLinkPages captures the three pages of a share link (open, locked, broken) and the link
// section of a book's Share page (empty, with links listed, with a new link shown) in the
// visualMatrix themes and widths. There is no board: the pages are built from the invite page's parts.
func TestVisualLinkPages(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	e := linkNewEnv(t)
	_, open := e.make(e.book.ID, 7, "")
	_, locked := e.make(e.book.ID, 30, "a long quiet password")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if theme := r.URL.Query().Get("theme"); theme != "" {
			r.AddCookie(&http.Cookie{Name: themeCookie, Value: theme})
		}
		if !strings.HasPrefix(r.URL.Path, "/s/") {
			r.AddCookie(&http.Cookie{Name: e.owner.Name, Value: e.owner.Value})
		}
		e.a.h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	shoot := func(name, path, ready, fill string) {
		for _, sh := range visualMatrix(true) {
			theme, w := sh.Theme, sh.Width
			t.Run(fmt.Sprintf("%s-%s-%d", name, theme, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w == 390, false)
				tab.Navigate(srv.URL + path + "?theme=" + theme)
				tab.WaitFor(ready, 10*time.Second)
				if fill != "" {
					inviteFill(tab, "#link-label", fill)
					playerClickSel(t, tab, `form.ts-share__linkform button[type=submit]`)
					tab.WaitFor(`document.querySelector("#share-link") !== null`, 10*time.Second)
					time.Sleep(time.Second) // the page swap cross-fades; capture once it has settled
				}
				var m struct {
					Over       bool     `json:"over"`
					Small      []string `json:"small"`
					Unlabelled []string `json:"unlabelled"`
					H1         int      `json:"h1"`
				}
				tab.Eval(linkProbeJS, &m)
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
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("link-%s-%s-%d.png", name, theme, w)))
			})
		}
	}

	shoot("open", "/s/"+open, `window.tsPlayer !== undefined && document.querySelector("#link-play [data-ts-play]") !== null`, "")
	shoot("locked", "/s/"+locked, `document.querySelector("#link-password") !== null`, "")
	shoot("broken", "/s/"+strings.Repeat("x", 43), `document.querySelector("h1") !== null`, "")
	book := "/books/" + e.book.ID + "/share"
	shoot("share-empty", book, `document.querySelector(".ts-share__links") !== null`, "")
	e.make(e.book.ID, 7, "")
	e.make(e.book.ID, 30, "a long quiet password")
	shoot("share-listed", book, `document.querySelector(".ts-share__linklist") !== null`, "")
	shoot("share-created", book, `document.querySelector("#link-label") !== null`, "Tia Marta")
	t.Logf("screenshots in %s", out)
}
