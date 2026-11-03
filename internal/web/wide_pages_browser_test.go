//go:build dev

package web

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// wideSeed gives the owner four shelves and a queue so the wide pages have content to lay out.
func wideSeed(t *testing.T, e *playerEnv) (shelfID string) {
	t.Helper()
	ctx := context.Background()
	page, err := e.a.lib.ListBooks(ctx, bookAllScope, library.ListQuery{Sort: library.SortTitle, Limit: 20})
	if err != nil || len(page.Books) < 4 {
		t.Fatalf("seed books = %d, %v", len(page.Books), err)
	}
	for i, name := range []string{"Sunday mysteries", "For Mamá", "Sea stories", "Sleep"} {
		sh, err := e.a.lib.CreateShelf(ctx, e.ownerID, name)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			shelfID = sh.ID
		}
		for _, b := range page.Books[:min(i+1, len(page.Books))] {
			if err := e.a.lib.AddToShelf(ctx, e.ownerID, sh.ID, b.ID); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, b := range page.Books[1:] {
		if _, err := e.a.lib.QueueAdd(ctx, e.ownerID, b.ID, false); err != nil {
			t.Fatal(err)
		}
	}
	return shelfID
}

// wideNoOverflow fails when #main scrolls sideways.
func wideNoOverflow(t *testing.T, tab *cdp.Tab, what string) {
	t.Helper()
	var over bool
	tab.Eval(`(() => { const m = document.getElementById("main"); return m.scrollWidth > m.clientWidth; })()`, &over)
	if over {
		t.Errorf("%s: #main overflows horizontally", what)
	}
}

// TestVisualWidePages captures Shelves, one shelf, Share, Queue, Search (three groups and one group)
// and Downloads at 390, 1024, 1440 and 1920 px, and fails when #main scrolls sideways. Output goes under
// the visual directory (git-ignored) for a human to read.
func TestVisualWidePages(t *testing.T) {
	e := nowPanelEnv(t)
	shelf := wideSeed(t, e)
	b := chromium(t)
	out := visualDir(t, "chromium")
	pages := []struct{ name, path string }{
		{"shelves", "/shelves"}, {"shelf", "/shelves/" + shelf}, {"share", "/share"}, {"queue", "/queue"},
		{"search", "/search?q=Harbour"}, {"search-one", "/search?q=Salt"}, {"downloads", "/me/downloads"},
	}
	for _, w := range []int{390, 1024, 1440, 1920} {
		for _, p := range pages {
			t.Run(fmt.Sprintf("%s-%d", p.name, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w < 768, true)
				tab.Navigate(e.URL + p.path)
				tab.WaitFor(`document.readyState === "complete" && document.getElementById("main") !== null`, 10*time.Second)
				time.Sleep(250 * time.Millisecond)
				wideNoOverflow(t, tab, p.name)
				tab.Screenshot(filepath.Join(out, fmt.Sprintf("wide-%s-%d.png", p.name, w)))
			})
		}
	}
}

// TestVisualWidePanel captures Shelves and Search at 1440 px with the Now playing panel open, where the
// page gives up its right edge to the panel.
func TestVisualWidePanel(t *testing.T) {
	e := nowPanelEnv(t)
	wideSeed(t, e)
	out := visualDir(t, "chromium")
	for _, p := range []struct{ name, path string }{{"shelves", "/shelves"}, {"search", "/search?q=Harbour"}} {
		t.Run(p.name, func(t *testing.T) {
			tab := fullpOpen(t, e, 1440, 900, true)
			tab.Navigate(e.URL + p.path)
			tab.WaitFor(`window.tsPlayer && !document.getElementById("player").hidden`, 10*time.Second)
			bmkeyPlay(t, tab, "[data-ts-toggle]")
			fullpOpenPlayer(t, tab)
			tab.WaitFor(`document.querySelector("#now-panel[data-book-id] .ts-np__desc") !== null`, 5*time.Second)
			time.Sleep(400 * time.Millisecond)
			wideNoOverflow(t, tab, p.name+" with the panel open")
			tab.Screenshot(filepath.Join(out, fmt.Sprintf("wide-%s-1440-panel.png", p.name)))
		})
	}
}
