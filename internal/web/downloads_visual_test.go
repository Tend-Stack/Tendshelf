//go:build dev

package web

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

// offlineCFillJS stands in for js/downloads.js (package B2): it shows the hidden nodes and fills the
// two row templates with the board's rows, so the captures show the page the script will draw.
const offlineCFillJS = `(() => {
  const root = document.querySelector("[data-ts-downloads]");
  const show = (sel) => { const el = root.querySelector(sel); el.hidden = false; return el; };
  show("[data-ts-storage]");
  root.querySelector("[data-ts-storage-free]").textContent = "7.4 GB free";
  root.querySelector("[data-ts-storage-used]").textContent = "Tendshelf · 6.2 GB";
  root.querySelector("[data-ts-storage-bar] .ts-bar__fill").style.setProperty("--ts-value", "19");
  show("[data-ts-persist-note]");
  show("[data-ts-keep-open]");
  show("[data-ts-install]");
  show("[data-ts-settings]");
  const addRows = (tpl, list, rows) => {
    for (const r of rows) {
      const li = root.querySelector(tpl).content.firstElementChild.cloneNode(true);
      li.querySelector("[data-ts-title]").textContent = r.title;
      li.querySelector("[data-ts-meta]").textContent = r.meta;
      const bar = li.querySelector("[data-ts-row-bar]");
      if (bar) {
        bar.setAttribute("aria-valuenow", String(r.pct));
        bar.setAttribute("aria-label", "Downloading " + r.title);
        bar.querySelector(".ts-bar__fill").style.setProperty("--ts-value", String(r.pct));
        if (r.pct < 0) bar.hidden = true;
      }
      for (const a of li.querySelectorAll("[data-ts-act]")) {
        a.hidden = !(r.acts || ["remove"]).includes(a.dataset.tsAct);
        a.setAttribute("aria-label", a.dataset.tsAct + " " + r.title);
      }
      root.querySelector(list).append(li);
    }
  };
  addRows("[data-ts-row-active]", "[data-ts-active-list]", [
    {title: "The Fog Bell", meta: "214 of 468 MB · about 2 min", pct: 46, acts: ["pause"]},
    {title: "A Field Guide to Small Winters", meta: "Waiting · 291 MB", pct: -1, acts: ["cancel"]},
  ]);
  addRows("[data-ts-row-ready]", "[data-ts-ready-list]", [
    {title: "The Lighthouse Keeper's Daughter", meta: "Ready offline · 1.6 GB"},
    {title: "Salt and Ember", meta: "Ready offline · 1.1 GB"},
    {title: "The Last Ferry North", meta: "Ready offline · 0.8 GB · finished 3 days ago"},
  ]);
  show("[data-ts-active]").querySelector("[data-ts-active-count]").textContent = "2";
  show("[data-ts-ready]").querySelector("[data-ts-ready-count]").textContent = "3";
  root.querySelector("[data-ts-ready-size]").textContent = "3.5 GB";
  root.querySelector("[data-ts-empty]").hidden = true;
  return true;
})()`

// TestOfflineCDownloadsVisual captures the Downloads page empty (what the server sends) and filled
// (the board's states, drawn by offlineCFillJS), plus the book page's Download control, at 390, 1024
// and 1440 px, and fails on horizontal overflow or a target under 44 px.
func TestOfflineCDownloadsVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	a := newTestApp(t)
	a.owner(t)
	f := browseLibrary(t, a, "Audiobooks")
	book := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "The Fog Bell", Authors: []string{"Imogen Hale"},
		Narrators: []string{"Theo Marsh"}, DurationMs: 9 * 3_600_000, Description: "A keeper, a bell and a fog that will not lift."})
	offlineCStyleBook(t, a, f, book)
	streamExec(t, a, `UPDATE audio_files SET container='mp3', codec='mp3', duration_ms=(SELECT duration_ms FROM books b WHERE b.id = audio_files.book_id)`) // a playable file, so the book has a Download control
	base := themedServer(t, a.signedIn(t))
	for _, sh := range []visualShot{{"dark", 390}, {"dark", 1024}, {"dark", 1440}, {"light", 390}} {
		theme, w := sh.Theme, sh.Width
		for _, state := range []string{"empty", "filled"} {
			t.Run(fmt.Sprintf("downloads-%s-%s-%d", state, theme, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w == 390, false)
				tab.Navigate(base + "/me/downloads?theme=" + theme)
				tab.WaitFor(`document.querySelector("[data-ts-downloads]") !== null`, 5*time.Second)
				if state == "filled" {
					var ok bool
					tab.Eval(offlineCFillJS, &ok)
				}
				time.Sleep(300 * time.Millisecond)
				var m browseProbe
				tab.Eval(browseProbeJS, &m)
				if m.Over {
					t.Errorf("horizontal overflow")
				}
				if small := offlineCSmall(m.Small); len(small) > 0 {
					t.Errorf("targets under 44 px: %v", small)
				}
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("downloads-%s-%s-%d.png", state, theme, w)))
			})
		}
		t.Run(fmt.Sprintf("book-%s-%d", theme, w), func(t *testing.T) {
			tab := b.NewTab(w, 900, w == 390, false)
			tab.Navigate(base + "/books/" + book.ID + "?theme=" + theme)
			tab.WaitFor(`document.querySelector("[data-ts-download]") !== null`, 5*time.Second)
			// What js/downloads.js does for a book that may be downloaded: show the button.
			var ok bool
			tab.Eval(`(() => { document.querySelector("[data-ts-download]").hidden = false; return true; })()`, &ok)
			time.Sleep(300 * time.Millisecond)
			var m browseProbe
			tab.Eval(browseProbeJS, &m)
			if m.Over {
				t.Errorf("horizontal overflow")
			}
			if len(m.Small) > 0 {
				t.Errorf("targets under 44 px: %v", m.Small)
			}
			tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("book-download-%s-%d.png", theme, w)))
		})
	}
	t.Logf("screenshots in %s", out)
}

// offlineCStyleBook gives the book a cover so the page looks like the boards.
func offlineCStyleBook(t *testing.T, a *testApp, f library.Folder, b library.Book) {
	t.Helper()
	sha, rel := browseWriteCover(t, a.dir, "offc-"+b.Title, "#3d4f5c")
	cover := library.Cover{BookID: b.ID, Source: library.CoverFolder, Mime: "image/png", Width: 96, Height: 96, SHA256: sha, Path: rel, Tint: "#3d4f5c"}
	if err := a.lib.Update(t.Context(), func(tx *library.Tx) error {
		if err := tx.PutCover(&cover); err != nil {
			return err
		}
		return tx.SetBookCover(b.ID, cover.ID)
	}); err != nil {
		t.Fatal(err)
	}
}

// offlineCSmall drops the switch itself: like every settings row, its row is the 56 px target.
func offlineCSmall(in []string) []string {
	var out []string
	for _, s := range in {
		if !strings.HasPrefix(s, "ts-switch:") {
			out = append(out, s)
		}
	}
	return out
}
