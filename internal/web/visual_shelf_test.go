//go:build dev

package web

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

// shelfVisualSeed builds the Harbour Lights library of the book boards, then three shelves of the
// owner: "Sunday mysteries" (3 books), "For Mamá" (5) and an empty "Sleep". It returns the ids the
// pages need.
func shelfVisualSeed(t *testing.T, a *testApp) (bookID, full, empty string) {
	t.Helper()
	bookID, _, _, _ = bookVisualSeed(t, a)
	ctx := context.Background()
	var owner string
	if err := a.st.Reader().QueryRow(`SELECT id FROM users WHERE role = 'owner'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	page, err := a.lib.ListBooks(ctx, bookAllScope, library.ListQuery{Sort: library.SortTitle, Limit: 20})
	if err != nil || len(page.Books) < 8 {
		t.Fatalf("seed books = %d, %v", len(page.Books), err)
	}
	mk := func(name string, from, to int) string {
		sh, err := a.lib.CreateShelf(ctx, owner, name)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range page.Books[from:to] {
			if err := a.lib.AddToShelf(ctx, owner, sh.ID, b.ID); err != nil {
				t.Fatal(err)
			}
		}
		return sh.ID
	}
	mk("Sunday mysteries", 0, 3)
	full = mk("For Mamá", 2, 7)
	empty = mk("Sleep", 0, 0)
	return bookID, full, empty
}

// TestVisualShelfPages captures the shelves list, one shelf, an empty shelf, the delete page and the
// book page with the "Add to shelf" sheet open, at the widths and themes of visualMatrix. It fails
// on horizontal overflow, a target under 44 px, an unlabelled field, a page without one h1, a skipped
// heading level or an inline style. The board PNGs the pages follow (the desktop sidebar of
// LibraryPage, the book boards) are copied beside the captures.
func TestVisualShelfPages(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	app := newTestApp(t)
	app.owner(t)
	bookID, full, empty := shelfVisualSeed(t, app)
	base := themedServer(t, app.signedIn(t))

	for name, dir := range map[string]string{"LibraryPage": "browse", "BookPhone": "core", "BookDesktop": "core", "BookTablet": "core"} {
		src, err := os.Open(filepath.Join("..", "..", "docs", "design", "tendshelf-handoff", "screens", dir, name+".png"))
		if err != nil {
			continue
		}
		if dst, err := os.Create(filepath.Join(out, "shelf-board-"+name+".png")); err == nil {
			_, _ = io.Copy(dst, src)
			_ = dst.Close()
		}
		_ = src.Close()
	}

	type page struct{ name, path string }
	main3 := []page{{"list", "/shelves"}, {"shelf", "/shelves/" + full}, {"book-sheet", "/books/" + bookID + "?sheet=shelves"}}
	rest := []page{{"book", "/books/" + bookID}, {"shelf-empty", "/shelves/" + empty}, {"delete", "/shelves/" + empty + "/delete"}}
	for i, group := range [][]page{main3, rest} {
		for _, p := range group {
			for _, sh := range visualMatrix(i == 0) {
				p, sh := p, sh
				t.Run(fmt.Sprintf("%s-%s-%d", p.name, sh.Theme, sh.Width), func(t *testing.T) {
					tab := b.NewTab(sh.Width, 900, sh.Width == 390, false)
					sep := "?"
					if strings.Contains(p.path, "?") {
						sep = "&"
					}
					tab.Navigate(base + p.path + sep + "theme=" + sh.Theme)
					if strings.Contains(p.path, "sheet=") {
						tab.WaitFor(`Boolean(document.querySelector(".ts-shelf-sheet"))`, 5*time.Second)
						time.Sleep(450 * time.Millisecond) // the sheet slides in; measure it at rest
					}
					var m bookProbeResult
					tab.Eval(bookProbeJS, &m)
					switch {
					case m.Over:
						t.Errorf("horizontal overflow")
					case m.Theme != sh.Theme:
						t.Errorf("data-theme = %q, want %q", m.Theme, sh.Theme)
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
					if m.Skip {
						t.Errorf("a heading level is skipped")
					}
					if m.Styled > 0 {
						t.Errorf("%d elements with a style attribute", m.Styled)
					}
					tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("shelf-%s-%s-%d.png", p.name, sh.Theme, sh.Width)))
				})
			}
		}
	}
}

// TestVisualShelfSheetLive checks in a real browser what the server tests cannot: the Add to shelf
// sheet opens from the button without leaving the page, a shelf toggles in place (aria-pressed
// flips, the sheet stays open), and the sidebar of the shelf page lists the shelves at 1440 px.
func TestVisualShelfSheetLive(t *testing.T) {
	b := chromium(t)
	app := newTestApp(t)
	app.owner(t)
	bookID, full, _ := shelfVisualSeed(t, app)
	srv := themedServer(t, app.signedIn(t))

	tab := b.NewTab(390, 844, true, false)
	tab.Navigate(srv + "/books/" + bookID)
	tab.WaitFor(`window.htmx !== undefined`, 5*time.Second)
	tab.Eval(`Array.from(document.querySelectorAll("main a")).find(a => a.textContent.trim() === "Add to shelf").click()`, nil)
	tab.WaitFor(`document.querySelectorAll("#sheet .ts-shelf-sheet__toggle").length === 4`, 5*time.Second)
	var got struct {
		Path    string
		Pressed []string
	}
	tab.Eval(`({path: location.pathname, pressed: Array.from(document.querySelectorAll("#sheet .ts-shelf-sheet__toggle")).map(b => b.textContent.trim() + "=" + b.getAttribute("aria-pressed"))})`, &got)
	if got.Path != "/books/"+bookID || len(got.Pressed) != 4 {
		t.Fatalf("after opening the sheet: %+v", got)
	}
	tab.Eval(`Array.from(document.querySelectorAll("#sheet .ts-shelf-sheet__toggle")).find(b => b.textContent.includes("Sleep")).click()`, nil)
	tab.WaitFor(`Array.from(document.querySelectorAll("#sheet .ts-shelf-sheet__toggle")).some(b => b.textContent.includes("Sleep") && b.getAttribute("aria-pressed") === "true")`, 5*time.Second)
	tab.Eval(`({path: location.pathname, pressed: Array.from(document.querySelectorAll("#sheet .ts-shelf-sheet__toggle")).map(b => b.textContent.trim() + "=" + b.getAttribute("aria-pressed"))})`, &got)
	if got.Path != "/books/"+bookID {
		t.Errorf("toggling left the page: %s", got.Path)
	}

	wide := b.NewTab(1440, 900, false, false)
	wide.Navigate(srv + "/shelves/" + full)
	var side struct {
		Items   []string
		Current string
	}
	wide.Eval(`({items: Array.from(document.querySelectorAll("#nav-side .ts-side__item")).map(a => a.textContent.trim().replace(/\s+/g, " ")),
		current: (document.querySelector('#nav-side [aria-current="page"]') || {textContent: ""}).textContent.trim().replace(/\s+/g, " ")})`, &side)
	if !strings.Contains(strings.Join(side.Items, "|"), "Sunday mysteries") || !strings.HasPrefix(side.Current, "For Mamá") {
		t.Errorf("sidebar at 1440: %+v", side)
	}
}
