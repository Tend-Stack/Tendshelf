//go:build dev

package web

import (
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/metadata"
)

// bookMoreReachJS says whether every item of the open menu can be reached on a phone: the panel sits between the
// top of the screen and the bottom navigation, and each item, scrolled into view inside the panel, is the element
// under its own middle (so nothing covers it and it has not run off the screen).
const bookMoreReachJS = `(() => {
  const panel = document.querySelector(".ts-moremenu__body");
  const bar = document.querySelector(".ts-nav-tabbar");
  const p = panel.getBoundingClientRect();
  const hidden = [];
  for (const a of panel.querySelectorAll("a")) {
    a.scrollIntoView({block: "nearest"});
    const r = a.getBoundingClientRect();
    const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
    if (hit !== a) hidden.push(a.textContent.trim());
  }
  return {top: p.top, bottom: p.bottom, left: p.left, right: p.right, barTop: bar.getBoundingClientRect().top,
    vw: innerWidth, scrolls: panel.scrollHeight > panel.clientHeight + 1, overflowY: getComputedStyle(panel).overflowY,
    items: panel.querySelectorAll("a").length, hidden, pageMoved: scrollY};
})()`

type bookMoreReach struct {
	Top, Bottom, Left, Right, BarTop, VW, PageMoved float64
	Scrolls                                         bool
	OverflowY                                       string
	Items                                           int
	Hidden                                          []string
}

// TestBookMoreMenuReachBrowser is the operator's report from a phone (2026-11-14): the three-dot menu on a book's
// page hung under its button, ran off the screen behind the bottom navigation, and its last items could not be
// reached. On a phone the panel is anchored above the navigation and scrolls inside itself. The menu is made long
// (twelve items) on a short screen so the test does not depend on how many actions a seeded book has.
func TestBookMoreMenuReachBrowser(t *testing.T) {
	b := chromium(t)
	for _, h := range []int{560, 844} {
		t.Run(fmt.Sprint(h), func(t *testing.T) {
			l := newCoverLab(t)
			l.start(t)
			l.a.srv.details = metadata.New(l.a.lib, l.a.runner, slog.New(slog.NewTextHandler(io.Discard, nil)))
			folder := browseLibrary(t, l.a, "Reach")
			book := librarytest.AddBook(t, l.a.lib, folder, librarytest.BookSpec{Title: "A Book With Many Actions", Authors: []string{"Ann Author"},
				DurationMs: 3_600_000, Files: 1})
			base := themedServer(t, l.a.signedIn(t))
			tab := b.NewTab(390, h, true, true)
			tab.Navigate(base + "/books/" + book.ID + "?theme=dark")
			tab.WaitFor(`window.htmx !== undefined && document.querySelector(".ts-moremenu > summary") !== null`, 10*time.Second)
			// The row of actions sits low on the screen, as it does after scrolling down a little.
			tab.Eval(`document.querySelector(".ts-book__acts").scrollIntoView({block: "end"})`, nil)
			tab.Eval(`(() => {
  const group = document.querySelector(".ts-moremenu__body [role=group]");
  const first = group.querySelector("a");
  for (let i = group.querySelectorAll("a").length; i < 12; i++) {
    const a = first.cloneNode(true);
    a.textContent = "Another action " + i;
    a.removeAttribute("hx-get");
    group.append(a);
  }
  document.querySelector(".ts-moremenu > summary").click();
})()`, nil)
			tab.WaitFor(`document.querySelector(".ts-moremenu").open`, 2*time.Second)
			var r bookMoreReach
			tab.Eval(bookMoreReachJS, &r)
			if r.Items != 12 {
				t.Fatalf("menu items = %d, want 12", r.Items)
			}
			if r.Top < 0 || r.Bottom > r.BarTop+0.5 || r.Left < 0 || r.Right > r.VW {
				t.Errorf("panel %.0f..%.0f (x %.0f..%.0f) leaves the room above the navigation at %.0f on a %.0f px wide screen", r.Top, r.Bottom, r.Left, r.Right, r.BarTop, r.VW)
			}
			if len(r.Hidden) > 0 {
				t.Errorf("items that cannot be reached: %v", r.Hidden)
			}
			if h == 560 && (!r.Scrolls || r.OverflowY != "auto") {
				t.Errorf("a menu taller than the screen does not scroll inside itself (scrolls %v, overflow-y %q)", r.Scrolls, r.OverflowY)
			}
		})
	}
}
