//go:build dev

package web

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// pageFill measures what the page-width rule promises (operator, 2026-11-08): lists, grids, dashboards and the
// book page fill .ts-main instead of keeping an old fixed width, and running text keeps a readable measure.
type pageFill struct {
	MainRight, ContentRight, ScrollW, InnerW, LongestPara float64
	Who                                                   string
}

// pageFillJS reads the right edge of .ts-main's content box, the right edge of the rightmost thing drawn inside it
// (the Book page's full-bleed band is not content), and the widest paragraph of running text.
const pageFillJS = `(() => {
  const main = document.querySelector(".ts-main");
  const mr = main.getBoundingClientRect();
  const cs = getComputedStyle(main);
  let right = 0, para = 0;
  for (const el of main.querySelectorAll("*")) {
    if (el.closest(".ts-book__band, [hidden]")) continue;
    const r = el.getBoundingClientRect();
    if (r.width === 0 || r.height === 0) continue;
    const st = getComputedStyle(el);
    if (st.position === "fixed") continue;
    // Only what is drawn counts: text and controls (leaves), boxes with a fill or a border. A bare layout wrapper
    // (a grid frame beside the settings menu) reaches the edge whatever its content does.
    const painted = el.children.length === 0 || st.backgroundColor !== "rgba(0, 0, 0, 0)" || parseFloat(st.borderTopWidth) > 0;
    if (!painted) continue;
    if (r.right > right) { right = r.right; window.__who = el.tagName + "." + el.className; }
    if (el.tagName === "P" && el.textContent.trim().length > 90) para = Math.max(para, r.width);
  }
  return {MainRight: mr.right - parseFloat(cs.paddingRight), ContentRight: right,
    ScrollW: document.documentElement.scrollWidth, InnerW: innerWidth, LongestPara: para, Who: window.__who};
})()`

// downloadsRowsJS stands in for js/downloads.js, which fills the page from the browser's own storage: it shows the
// ready list and clones the row template into it, so the check sees the page as a person with downloads does.
const downloadsRowsJS = `(() => {
  const sec = document.querySelector("[data-ts-ready]");
  if (!sec) return;
  sec.hidden = false;
  document.querySelector("[data-ts-empty]").hidden = true;
  const list = sec.querySelector("[data-ts-ready-list]");
  const tpl = document.querySelector("template[data-ts-row-ready]");
  for (let i = 1; i <= 7; i++) {
    const row = tpl.content.cloneNode(true);
    row.querySelector("[data-ts-title]").textContent = "Harbour book number " + i;
    row.querySelector("[data-ts-meta]").textContent = "Downloaded, " + i + " MB";
    list.append(row);
  }
})()`

func pageSetup(tab *cdp.Tab, name string) {
	if name == "downloads" {
		tab.Eval(downloadsRowsJS, nil)
	}
}

func pageWidthPages(e *playerEnv) []struct{ name, path string } {
	return []struct{ name, path string }{
		{"book", "/books/" + e.book.ID},
		{"search", "/search?q=Harbour"},
		{"downloads", "/me/downloads"},
		{"admin-libraries", "/admin/libraries"},
		{"stats", "/me/listening"},
	}
}

func TestPageWidthFillBrowser(t *testing.T) {
	e := nowPanelEnv(t)
	b := chromium(t)
	out := os.Getenv("TENDSHELF_WIDTH_OUT") // optional: write captures here
	for _, p := range pageWidthPages(e) {
		for _, w := range []int{1024, 1920} {
			t.Run(fmt.Sprintf("%s-%d", p.name, w), func(t *testing.T) {
				tab := b.NewTab(w, 1000, false, true)
				defer tab.Close()
				tab.Navigate(e.URL + p.path)
				time.Sleep(600 * time.Millisecond)
				pageSetup(tab, p.name)
				var r pageFill
				tab.Eval(pageFillJS, &r)
				if out != "" {
					_ = os.MkdirAll(out, 0o755)
					tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("%s-%d.png", p.name, w)))
				}
				if gap := r.MainRight - r.ContentRight; gap > 48 {
					t.Errorf("content ends %.0f px short of .ts-main right edge (%.0f), last thing drawn: %s", gap, r.MainRight, r.Who)
				}
				if r.ScrollW > r.InnerW {
					t.Errorf("sideways scroll: scrollWidth %.0f > innerWidth %.0f", r.ScrollW, r.InnerW)
				}
				if w == 1920 && r.LongestPara > 46*16+1 {
					t.Errorf("running text is %.0f px wide, want at most 46rem (736)", r.LongestPara)
				}
			})
		}
	}
}

// TestPageWidthCapturesBrowser writes captures of every wide page at four widths into TENDSHELF_WIDTH_OUT, for a
// look by eye (stretched rows, orphan cards, a head that does not line up). It asserts only that nothing scrolls
// sideways, and is skipped without the variable.
func TestPageWidthCapturesBrowser(t *testing.T) {
	out := os.Getenv("TENDSHELF_WIDTH_OUT")
	if out == "" {
		t.Skip("TENDSHELF_WIDTH_OUT not set")
	}
	e := nowPanelEnv(t)
	b := chromium(t)
	links := b.NewTab(1440, 900, false, true)
	links.Navigate(e.URL + "/books/" + e.book.ID)
	time.Sleep(500 * time.Millisecond)
	var hrefs struct{ Series, Author, Narrator string }
	links.Eval(`({Series: document.querySelector('a[href^="/series/"]')?.getAttribute("href") || "/",
	  Author: document.querySelector('a[href^="/authors/"]')?.getAttribute("href") || "/",
	  Narrator: document.querySelector('a[href^="/narrators/"]')?.getAttribute("href") || "/"})`, &hrefs)
	links.Close()
	lib := e.folder.LibraryID
	pages := append(pageWidthPages(e), []struct{ name, path string }{
		{"series", hrefs.Series}, {"author", hrefs.Author}, {"narrator", hrefs.Narrator},
		{"queue", "/queue"}, {"shelves", "/shelves"}, {"words", "/me/words"}, {"history", "/me/listening/history"},
		{"highlights", "/me/highlights"}, {"devices", "/me/devices"}, {"profile", "/me"}, {"reading", "/me/reading"},
		{"upload", "/libraries/" + lib + "/add"}, {"users", "/admin/users"}, {"duplicates", "/admin/duplicates"},
		{"health", "/admin/health"}, {"ebooks", "/admin/ebooks"}, {"backups", "/admin/backups"},
		{"lookups", "/admin/libraries/" + lib + "/lookups"}, {"book-bookmarks", "/books/" + e.book.ID + "/bookmarks"},
		{"book-details", "/books/" + e.book.ID + "/details"}, {"library", "/libraries"}, {"home", "/"},
	}...)
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range pages {
		for _, w := range []int{1024, 1440, 1920, 2560} {
			t.Run(fmt.Sprintf("%s-%d", p.name, w), func(t *testing.T) {
				tab := b.NewTab(w, 1000, false, true)
				defer tab.Close()
				tab.Navigate(e.URL + p.path)
				time.Sleep(500 * time.Millisecond)
				pageSetup(tab, p.name)
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("%s-%d.png", p.name, w)))
				var r pageFill
				tab.Eval(pageFillJS, &r)
				if r.ScrollW > r.InnerW {
					t.Errorf("sideways scroll: scrollWidth %.0f > innerWidth %.0f", r.ScrollW, r.InnerW)
				}
			})
		}
	}
}

// TestPageWidthStatsCapturesBrowser is the same look for the pages that need a person with a listening history.
func TestPageWidthStatsCapturesBrowser(t *testing.T) {
	out := os.Getenv("TENDSHELF_WIDTH_OUT")
	if out == "" {
		t.Skip("TENDSHELF_WIDTH_OUT not set")
	}
	b := chromium(t)
	w := listeningVisualApp(t)
	base := themedServer(t, w.a.signedIn(t))
	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, p := range []struct{ name, path string }{
		{"stats-data", "/me/listening"}, {"history-data", "/me/listening/history"}, {"reading-data", "/me/reading"},
	} {
		for _, width := range []int{1024, 1440, 1920, 2560} {
			t.Run(fmt.Sprintf("%s-%d", p.name, width), func(t *testing.T) {
				tab := b.NewTab(width, 1000, false, true)
				defer tab.Close()
				tab.Navigate(base + p.path)
				time.Sleep(700 * time.Millisecond)
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("%s-%d.png", p.name, width)))
				var r pageFill
				tab.Eval(pageFillJS, &r)
				if r.ScrollW > r.InnerW {
					t.Errorf("sideways scroll: scrollWidth %.0f > innerWidth %.0f", r.ScrollW, r.InnerW)
				}
			})
		}
	}
}

// With the Now playing panel open at 1440 the pages narrow to what is left and still do not scroll sideways.
func TestPageWidthPanelOpenBrowser(t *testing.T) {
	e := nowPanelEnv(t)
	for _, p := range pageWidthPages(e) {
		t.Run(p.name, func(t *testing.T) {
			tab := nowPanelOpen(t, e, 1440, 900)
			// A boosted link keeps the player (and its open panel) alive, as a click in the sidebar does.
			tab.Eval(`(() => {
			  const href = `+fmt.Sprintf("%q", p.path)+`;
			  let a = document.querySelector('.ts-shell a[href="' + href + '"]');
			  if (!a) { a = document.createElement("a"); a.href = href; document.querySelector(".ts-shell").append(a); htmx.process(a); }
			  a.click();
			})()`, nil)
			time.Sleep(900 * time.Millisecond)
			pageSetup(tab, p.name)
			var r pageFill
			tab.Eval(pageFillJS, &r)
			var open bool
			tab.Eval(`document.querySelector("dialog.ts-player").open`, &open)
			if !open {
				t.Fatal("the panel closed; the check did not run with it open")
			}
			if r.ScrollW > r.InnerW {
				t.Errorf("sideways scroll with the panel open: scrollWidth %.0f > innerWidth %.0f", r.ScrollW, r.InnerW)
			}
			if gap := r.MainRight - r.ContentRight; gap > 48 {
				t.Errorf("content ends %.0f px short of .ts-main right edge (%.0f), last thing drawn: %s", gap, r.MainRight, r.Who)
			}
		})
	}
}

// TestPageSwapNoSqueeze: the page area is never its own View Transition layer, so a tall page is not drawn from a squeezed
// snapshot during a page change (operator, Firefox, 2026-11-09); the real page rises in instead.
func TestPageSwapNoSqueeze(t *testing.T) {
	b := chromium(t)
	base := appServer(t)
	tab := b.NewTab(1440, 900, false, false)
	tab.Navigate(base + "/")
	tab.WaitFor(`window.htmx !== undefined`, 10*time.Second)
	tab.Eval(`document.querySelector('a[href^="/libraries"]').click()`, nil)
	tab.WaitFor(`location.pathname.startsWith("/libraries") && document.getElementById("main").classList.contains("ts-entering")`, 10*time.Second)
	var name string
	tab.Eval(`getComputedStyle(document.getElementById("main")).viewTransitionName`, &name)
	if name != "none" {
		t.Errorf("#main has its own layer %q", name)
	}
}

// TestPageSwapEntersBeforeFirstPaint: the new page carries ts-entering from the frame it is swapped in, so it never
// shows once in place and then restarts from opacity 0 and 4 px low (operator, Firefox and the Android app, 2026-11-10:
// "the whole page blinks and jumps after it loads"). htmx:after:swap comes only when the page-change transition ends,
// so the class used to arrive about 270 ms after the first frame of the new page.
func TestPageSwapEntersBeforeFirstPaint(t *testing.T) {
	b := chromium(t)
	base := appServer(t)
	tab := b.NewTab(390, 800, true, false)
	tab.Navigate(base + "/")
	tab.WaitFor(`window.htmx !== undefined`, 10*time.Second)
	// Every frame records whether #main still holds the old page and whether it is entering.
	tab.Eval(`(() => {
		window.__frames = [];
		const oldMain = document.getElementById("main").innerHTML;
		const tick = () => {
			const main = document.getElementById("main");
			window.__frames.push([main.innerHTML !== oldMain, main.classList.contains("ts-entering")]);
			if (window.__frames.length < 120) requestAnimationFrame(tick);
		};
		requestAnimationFrame(tick);
		document.querySelector('a[href^="/libraries"]').click();
	})()`, nil)
	tab.WaitFor(`window.__frames.length >= 120`, 15*time.Second)
	var first []bool
	tab.Eval(`window.__frames.find((f) => f[0]) || []`, &first)
	if len(first) != 2 {
		t.Fatal("the page never changed")
	}
	if !first[1] {
		t.Error("the first frame of the new page is not entering: it is drawn in place, then restarted by ts-entering")
	}
}
