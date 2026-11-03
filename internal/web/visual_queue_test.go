//go:build dev

package web

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

// queueVisualSeed builds the Harbour Lights library of the book boards and queues four of its books
// for the owner when the returned function runs (the empty page is captured before it). It returns
// the id of the book page to capture.
func queueVisualSeed(t *testing.T, a *testApp) (bookID string, queue func()) {
	t.Helper()
	bookID, _, _, _ = bookVisualSeed(t, a)
	ctx := context.Background()
	var owner string
	if err := a.st.Reader().QueryRow(`SELECT id FROM users WHERE role = 'owner'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	page, err := a.lib.ListBooks(ctx, bookAllScope, library.ListQuery{Sort: library.SortTitle, Limit: 20})
	if err != nil || len(page.Books) < 5 {
		t.Fatalf("seed books = %d, %v", len(page.Books), err)
	}
	return bookID, func() {
		for _, b := range page.Books[1:5] {
			if _, err := a.lib.QueueAdd(ctx, owner, b.ID, false); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// TestVisualQueue captures the split play button (closed and open) and the /queue page (empty and
// filled) at the widths and themes of visualMatrix (make visual-full: 390, 768, 1024 and 1440 px), and fails on horizontal overflow, a target
// under 44 px, a page without one h1 or an inline style. The open menu is checked to stay inside the
// viewport.
func TestVisualQueue(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	app := newTestApp(t)
	app.owner(t)
	bookID, queue := queueVisualSeed(t, app)
	base := themedServer(t, app.signedIn(t))

	shoot := func(t *testing.T, name, path, theme string, w int, open bool) {
		tab := b.NewTab(w, 900, w == 390, false)
		tab.Navigate(base + path + "?theme=" + theme)
		if open {
			tab.WaitFor(`document.querySelector(".ts-split__more > summary") !== null`, 5*time.Second)
			tab.Eval(`document.querySelector(".ts-split__more").open = true`, nil)
			time.Sleep(150 * time.Millisecond)
			var in bool
			tab.Eval(`(() => { const r = document.querySelector(".ts-split__more .ts-player__menubody").getBoundingClientRect(); return r.left >= 0 && r.right <= innerWidth; })()`, &in)
			if !in {
				t.Errorf("the open menu leaves the viewport")
			}
		}
		var m bookProbeResult
		tab.Eval(bookProbeJS, &m)
		switch {
		case m.Over:
			t.Errorf("horizontal overflow")
		case m.Theme != theme:
			t.Errorf("data-theme = %q, want %q", m.Theme, theme)
		}
		if len(m.Small) > 0 {
			t.Errorf("targets under 44 px: %v", m.Small)
		}
		if m.H1 != 1 {
			t.Errorf("%d h1 elements", m.H1)
		}
		if m.Styled > 0 {
			t.Errorf("%d elements with a style attribute", m.Styled)
		}
		tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("queue-%s-%s-%d.png", name, theme, w)))
	}
	each := func(name, path string, open bool) {
		for _, sh := range visualMatrix(true) {
			t.Run(fmt.Sprintf("%s-%s-%d", name, sh.Theme, sh.Width), func(t *testing.T) { shoot(t, name, path, sh.Theme, sh.Width, open) })
		}
	}
	each("page-empty", "/queue", false)
	queue()
	each("split-closed", "/books/"+bookID, false)
	each("split-open", "/books/"+bookID, true)
	each("page-filled", "/queue", false)
}
