//go:build dev

package web

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// ltProbeJS runs the three large-text probes on the page as it stands (P3-11):
//
//	(a) over: the page is wider than the window.
//	(b) small: a visible control under 44 by 44 CSS px.
//	(c) clip: a time-left element that is cut (its text is wider than its box) or whose box sticks out of
//	    an ancestor that clips. The time left is never truncated, at any text size.
const ltProbeJS = `(() => {
  const shown = el => { const r = el.getBoundingClientRect(); return r.width > 0 && r.height > 0 && getComputedStyle(el).visibility !== "hidden"; };
  const name = el => (typeof el.className === "string" && el.className ? "." + el.className.split(" ")[0] : el.tagName) + (el.id ? "#" + el.id : "");
  const small = [];
  for (const el of document.querySelectorAll("button, select, summary, .ts-btn, .ts-iconbtn, .ts-seg__item, .ts-card, .ts-tab")) {
    if (!shown(el) || el.closest("[hidden], [inert]")) continue;
    const r = el.getBoundingClientRect();
    if (r.width < 43.5 || r.height < 43.5) small.push(name(el) + ":" + Math.round(r.width) + "x" + Math.round(r.height));
  }
  const clip = [];
  for (const el of document.querySelectorAll("[data-ts-full-left], [data-ts-full-book-left], [data-ts-bar-left], [data-ts-meta], .ts-hero__where")) {
    if (!shown(el)) continue;
    if (el.scrollWidth > el.clientWidth + 1) clip.push(name(el) + " cut " + el.scrollWidth + ">" + el.clientWidth);
    const r = el.getBoundingClientRect();
    for (let p = el.parentElement; p && p !== document.documentElement; p = p.parentElement) {
      const o = getComputedStyle(p);
      if (o.overflowX === "visible" && o.overflowY === "visible") continue;
      const q = p.getBoundingClientRect();
      if (q.width > 0 && (r.left < q.left - 1 || r.right > q.right + 1)) { clip.push(name(el) + " outside " + name(p)); break; }
    }
  }
  const wide = [];
  for (const el of document.body.querySelectorAll("*")) {
    const r = el.getBoundingClientRect();
    if (r.width > 0 && r.right > window.__ltW + 1 && wide.length < 6) wide.push(name(el) + ":" + Math.round(r.right));
  }
  return {over: document.documentElement.scrollWidth > window.__ltW + 1, wide, small, clip, size: parseFloat(getComputedStyle(document.documentElement).fontSize),
    attr: document.documentElement.dataset.textSize || ""};
})()`

type ltProbe struct {
	Over  bool     `json:"over"`
	Wide  []string `json:"wide"`
	Small []string `json:"small"`
	Clip  []string `json:"clip"`
	Size  float64  `json:"size"`
	Attr  string   `json:"attr"`
}

// ltPage is one page to capture. browserText marks a signed-out page: it never takes the setting (the
// browser's own text size applies), so the test sets the root to 125 % the way a browser would.
type ltPage struct {
	name, base, path, ready string
	browserText             bool
}

func ltSetSize(t *testing.T, a *testApp, uid string, large bool) {
	t.Helper()
	if !large {
		streamExec(t, a, `DELETE FROM user_settings WHERE user_id = ? AND key = 'ui.text_size'`, uid)
		return
	}
	streamExec(t, a, `INSERT OR REPLACE INTO user_settings(user_id, key, value_json, updated_at) VALUES (?, 'ui.text_size', '"large"', 1)`, uid)
}

func ltShots() []visualShot {
	if !visualFull() {
		return []visualShot{{"dark", 390}, {"light", 768}, {"dark", 1024}, {"light", 1440}, {"dark", 1440}, {"light", 390}}
	}
	var out []visualShot
	for _, th := range themes {
		for _, w := range []int{390, 768, 1024, 1440} {
			out = append(out, visualShot{th, w})
		}
	}
	return out
}

// ltCheck opens the page, settles it and runs the probes. probes holds "b" to check target sizes and "s"
// to skip the large-attribute check (the zoom runs switch the setting on and off).
func ltCheck(t *testing.T, b *cdp.Browser, pg ltPage, theme string, w int, probes string, shot string) {
	t.Helper()
	tab := b.NewTab(w, 900, w == 390, false)
	sep := "?"
	if strings.Contains(pg.path, "?") {
		sep = "&"
	}
	tab.Navigate(pg.base + pg.path + sep + "theme=" + theme)
	ready := pg.ready
	if ready == "" {
		ready = `document.readyState === "complete"`
	}
	tab.WaitFor(ready, 10*time.Second)
	time.Sleep(600 * time.Millisecond)
	if pg.browserText {
		tab.Eval(`document.documentElement.style.fontSize = "125%"`, nil)
		time.Sleep(150 * time.Millisecond)
	}
	var m ltProbe
	ltProbeAt(tab, w, &m)
	if m.Over {
		t.Errorf("horizontal overflow, past the window: %v", m.Wide)
	}
	if strings.Contains(probes, "b") && len(m.Small) > 0 {
		t.Errorf("targets under 44 px: %v", m.Small)
	}
	if len(m.Clip) > 0 {
		t.Errorf("time left cut or outside a clipping parent: %v", m.Clip)
	}
	if !pg.browserText && !strings.Contains(probes, "s") && m.Attr != "large" {
		t.Errorf("data-text-size = %q, want large", m.Attr)
	}
	if shot != "" {
		tab.ScreenshotFull(shot)
	}
}

// TestVisualLargeText captures the pages in large text (Home, Library, a book, Settings, the people
// pages, the share and link pages, the invite pages, the bar and the full player) and fails on
// overflow, a target under 44 px, or a cut time left. It also runs Home and a book at 640 and 720 px
// in both text sizes (a 1280 or 1440 px window at 200 % browser zoom). The Home and Settings boards are
// copied beside the captures.
func TestVisualLargeText(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	for _, bd := range []struct{ name, dir string }{{"RosaHome", "people"}, {"SettingsPhone", "people"}, {"HomePhoneLight", "core"}, {"HomeDesktop", "core"}} {
		if src, err := os.Open(filepath.Join("..", "..", "docs", "design", "tendshelf-handoff", "screens", bd.dir, bd.name+".png")); err == nil {
			if dst, err := os.Create(filepath.Join(out, "largetext-board-"+bd.name+".png")); err == nil {
				_, _ = io.Copy(dst, src)
				_ = dst.Close()
			}
			_ = src.Close()
		}
	}
	file := func(name, theme string, w int) string {
		return filepath.Join(out, fmt.Sprintf("largetext-%s-%s-%d.png", name, theme, w))
	}
	run := func(t *testing.T, pages []ltPage, shots []visualShot, probes string) {
		for _, pg := range pages {
			for _, sh := range shots {
				theme, w := sh.Theme, sh.Width
				t.Run(fmt.Sprintf("%s-%s-%d", pg.name, theme, w), func(t *testing.T) {
					ltCheck(t, b, pg, theme, w, probes, file(pg.name, theme, w))
				})
			}
		}
	}

	// The signed-in pages: the owner's Home with Shared with you, the Library, a book, Settings, Your
	// devices, the Users pages.
	member, _, danielCk := sharedHomeSeed(t)
	ownerID := sweepUserID(t, member, member.owner(t))
	danielID := sweepUserID(t, member, danielCk)
	ltSetSize(t, member, ownerID, true)
	base := themedServer(t, member.signedIn(t))
	var bookID, heroID string
	if err := member.st.Reader().QueryRow(`SELECT id FROM books WHERE title = 'The Fog Bell'`).Scan(&bookID); err != nil {
		t.Fatal(err)
	}
	if err := member.st.Reader().QueryRow(`SELECT id FROM books WHERE title LIKE 'The Lighthouse%'`).Scan(&heroID); err != nil {
		t.Fatal(err)
	}
	home := `window.tsPlayer !== undefined && document.getElementById("player").classList.contains("is-suppressed")`
	pages := []ltPage{
		{"home", base, "/", home, false},
		{"library", base, "/libraries", "", false},
		{"book", base, "/books/" + heroID, "", false},
		{"book-chapters", base, "/books/" + heroID + "/chapters", "", false},
		{"book-share", base, "/books/" + bookID + "/share", "", false},
		{"settings", base, "/me/settings", "", false},
		{"profile", base, "/me", "", false},
		{"users", base, "/admin/users", "", false},
		{"user", base, "/admin/users/" + danielID, "", false},
	}
	t.Run("signed-in", func(t *testing.T) { run(t, pages, ltShots(), "b") })

	// Rosa: a guest with one shelf.
	guest := newTestApp(t)
	guest.owner(t)
	browseSeed(t, guest, 3)
	daniel := guest.as(t, users.RoleAdmin)
	rosa := guest.as(t, users.RoleGuest)
	streamExec(t, guest, `UPDATE users SET display_name = 'Daniel' WHERE id = ?`, sweepUserID(t, guest, daniel))
	var gb string
	if err := guest.st.Reader().QueryRow(`SELECT id FROM books ORDER BY title LIMIT 1`).Scan(&gb); err != nil {
		t.Fatal(err)
	}
	shelf := shelfMake(t, guest, daniel, "For Rosa")
	shelfAdd(t, guest, daniel, shelf, gb)
	streamExec(t, guest, `INSERT INTO grants(id, user_id, scope, shelf_id, created_by, created_at, updated_at) VALUES ('gs1', ?, 'shelf', ?, ?, 1, 1)`,
		sweepUserID(t, guest, rosa), shelf, sweepUserID(t, guest, daniel))
	ltSetSize(t, guest, sweepUserID(t, guest, rosa), true)
	guestBase := asServer(t, guest, func() *http.Cookie { return rosa })
	t.Run("rosa", func(t *testing.T) { run(t, []ltPage{{"rosa-home", guestBase, "/", "", false}}, ltShots(), "b") })

	// Two text sizes at 640 and 720 px: a 1280 or 1440 px window at 200 % browser zoom.
	t.Run("zoom", func(t *testing.T) {
		for _, large := range []bool{false, true} {
			ltSetSize(t, member, ownerID, large)
			mode := map[bool]string{false: "standard", true: "large"}[large]
			for _, pg := range []ltPage{{"zoom-home-" + mode, base, "/", home, false}, {"zoom-book-" + mode, base, "/books/" + heroID, "", false}} {
				for _, w := range []int{640, 720} {
					t.Run(fmt.Sprintf("%s-%d", pg.name, w), func(t *testing.T) {
						ltCheck(t, b, pg, "dark", w, "s", "")
					})
				}
			}
		}
		ltSetSize(t, member, ownerID, true)
	})
}
