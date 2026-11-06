//go:build dev

package web

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/media"
	"github.com/Tend-Stack/tendshelf/internal/upload"
)

// uploadProbeJS finds what a page of this package can get wrong: sideways scrolling, a wrong theme,
// a control under 44 px, an input with no label around it or aria-label, and the heading count.
const uploadProbeJS = `(() => {
  const small = [];
  for (const el of document.querySelectorAll("main a.ts-btn, main button, main label.ts-btn, main .ts-upset__choice, main summary")) {
    const r = el.getBoundingClientRect();
    if (r.width > 0 && r.height < 43.5) small.push((el.className || el.tagName) + ":" + Math.round(r.height));
  }
  const unlabelled = [...document.querySelectorAll("main input:not([type=hidden])")].filter(i => !(i.labels && i.labels.length) && !i.getAttribute("aria-label")).map(i => i.name);
  return {over: document.documentElement.scrollWidth > innerWidth, theme: document.documentElement.dataset.theme || "", small, unlabelled, h1: document.querySelectorAll("main h1").length};
})()`

// uploadLiveRowsJS draws the two rows only the script makes (sending and paused) from the server's
// row template, with the board's words, so the capture shows every state of the queue.
const uploadLiveRowsJS = `(() => {
  const root = document.querySelector("[data-ts-module]");
  const make = (state, title, meta, pct, line, icon) => {
    const li = root.querySelector("template[data-ts-row]").content.firstElementChild.cloneNode(true);
    li.className = "ts-upload__row ts-upload__row--" + state;
    li.querySelector("[data-ts-icons]").className = "ts-upload__icon ts-upload__icon--" + state;
    li.querySelector("[data-ts-title]").textContent = title;
    li.querySelector("[data-ts-meta]").textContent = meta;
    li.querySelector("[data-ts-bar]").value = pct;
    li.querySelector("[data-ts-line]").textContent = line;
    for (const s of li.querySelectorAll("[data-ts-icon]")) s.hidden = s.dataset.tsIcon !== icon;
    for (const b of li.querySelectorAll("[data-ts-for]")) b.hidden = !b.dataset.tsFor.split(" ").includes(state) || b.dataset.tsAct === "cancel";
    return li;
  };
  const list = root.querySelector("[data-ts-list]");
  list.prepend(make("paused", "The Hollow Orchard", "14 files · mp3", 41, "Paused. 460 MB of 1.1 GB sent.", "paused"));
  list.prepend(make("sending", "All Systems Red", "1 file · m4b", 62, "2.1 GB of 3.4 GB · about 6 min left", "sending"));
  return true;
})()`

// uploadVisualApp is the board's owner with one library whose folder takes uploads, and the queue the
// board shows: stopped after a reload, adding, added, refused as copy-protected and out of space.
func uploadVisualApp(t *testing.T) (*testApp, string) {
	t.Helper()
	a := newTestApp(t, func(o *Options) {
		svc := upload.New(upload.Config{DataDir: o.DataDir}, o.Store, o.Library, media.Tools{}, o.Jobs, nil)
		if err := svc.Register(o.Jobs); err != nil {
			t.Fatal(err)
		}
		o.Uploads = svc
	})
	a.owner(t)
	dir := visualTempDir(t)
	l, f := librarytest.NewLibrary(t, a.lib, "Audiobooks", dir)
	if err := a.lib.SetUploadFolder(context.Background(), l.ID, f.ID); err != nil {
		t.Fatal(err)
	}
	owner := sweepUserID(t, a, a.owner(t))
	add := func(id, label, state, errCode, refused, bookPath string, skipped int, files ...[4]any) {
		streamExec(t, a, `INSERT INTO uploads(id, library_id, folder_id, created_by, label, state, bytes_total, bytes_received, skipped, book_path, refused_file, error_code, created_at, updated_at)
  VALUES (?,?,?,?,?,?,0,0,?,NULLIF(?,''),NULLIF(?,''),NULLIF(?,''),?,?)`, id, l.ID, f.ID, owner, label, state, skipped, bookPath, refused, errCode, time.Now().UnixMilli(), time.Now().UnixMilli())
		for i, fl := range files {
			streamExec(t, a, `INSERT INTO upload_files(id, upload_id, idx, rel_path, size, modified_ms, bytes_received) VALUES (?,?,?,?,?,0,?)`,
				fmt.Sprintf("%s_f%d", id, i), id, i, fl[0], fl[1], fl[2])
		}
	}
	// Newest first: the page lists by creation time, so give each row its own later stamp.
	add("upl_a1", "A Quiet Harbour", "failed", "no_space", "", "", 0, [4]any{"Harbour/01.m4a", 4_000_000_000, 4_000_000_000})
	add("upl_a2", "The Glass Orchard", "refused", "drm", "Glass/The Glass Orchard.aax", "", 0, [4]any{"Glass/The Glass Orchard.aax", 90_000_000, 90_000_000})
	book := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: "The Lighthouse Keeper's Daughter", Authors: []string{"Ann Marlowe"}, Files: 1})
	streamExec(t, a, `UPDATE books SET root_path = ? WHERE id = ?`, filepath.Join(dir, "Ann Marlowe", "Tidewater", "02 - The Lighthouse Keeper's Daughter"), book.ID)
	add("upl_a3", "The Lighthouse Keeper's Daughter", "placed", "", "", filepath.Join(dir, "Ann Marlowe", "Tidewater", "02 - The Lighthouse Keeper's Daughter"), 2, [4]any{"Lighthouse/01.mp3", 700_000_000, 700_000_000})
	add("upl_a4", "Weather for Beginners", "placed", "", "", filepath.Join(dir, "Rosa Lindqvist", "Weather for Beginners"), 0, [4]any{"Weather/01.mp3", 300_000_000, 300_000_000})
	add("upl_a5", "Salt Road Letters", "receiving", "", "", "", 0,
		[4]any{"Salt/01.flac", 100_000_000, 46_000_000}, [4]any{"Salt/02.flac", 100_000_000, 46_000_000})
	// created_at orders the rows: oldest last.
	for i, id := range []string{"upl_a1", "upl_a2", "upl_a3", "upl_a4", "upl_a5"} {
		streamExec(t, a, `UPDATE uploads SET created_at = ?, updated_at = ? WHERE id = ?`, time.Now().UnixMilli()-int64(i)*1000, time.Now().UnixMilli(), id)
	}
	return a, l.ID
}

// TestUploadVisual captures the Add books page (empty, with the queue of every state), the owner's
// setting in Libraries & scanning and the board itself at 390, 768, 1024 and 1440 px, dark and
// light, and fails on sideways scrolling, a wrong data-theme, a target under 44 px or an unlabelled
// input. Run with TENDSHELF_VISUAL_FULL=1 for the whole matrix.
func TestUploadVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	a, libID := uploadVisualApp(t)
	base := themedServer(t, a.signedIn(t))
	board, err := filepath.Abs(filepath.Join("..", "..", "docs", "design", "boards", "UploadBooks.html"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(board); err != nil {
		t.Fatal(err)
	}
	shots := []visualShot{{"dark", 390}, {"light", 1440}}
	if visualFull() {
		shots = nil
		for _, th := range []string{"dark", "light"} {
			for _, w := range []int{390, 768, 1024, 1440} {
				shots = append(shots, visualShot{th, w})
			}
		}
	}
	for _, sh := range shots {
		theme, w := sh.Theme, sh.Width
		for _, pg := range []struct{ name, path string }{
			{"empty", "/libraries/" + libID + "/add"},
			{"queue", "/libraries/" + libID + "/add"},
			{"admin", "/admin/libraries"},
		} {
			t.Run(fmt.Sprintf("%s-%s-%d", pg.name, theme, w), func(t *testing.T) {
				if pg.name == "empty" {
					// The first shot has no queue: the same page for a person with no uploads of the day.
					streamExec(t, a, `UPDATE uploads SET updated_at = 1, created_at = 1`)
				} else {
					streamExec(t, a, `UPDATE uploads SET updated_at = ?, created_at = ? - CAST(substr(id, 6) AS INTEGER) * 1000`, time.Now().UnixMilli(), time.Now().UnixMilli())
				}
				tab := b.NewTab(w, 900, w == 390, false)
				tab.Navigate(base + pg.path + "?theme=" + theme)
				if pg.name == "queue" {
					var ok bool
					tab.Eval(uploadLiveRowsJS, &ok)
				}
				time.Sleep(300 * time.Millisecond)
				var m struct {
					Over       bool     `json:"over"`
					Theme      string   `json:"theme"`
					Small      []string `json:"small"`
					Unlabelled []string `json:"unlabelled"`
					H1         int      `json:"h1"`
				}
				tab.Eval(uploadProbeJS, &m)
				if m.Over {
					t.Errorf("horizontal overflow")
				}
				if m.Theme != theme {
					t.Errorf("data-theme = %q, want %q", m.Theme, theme)
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
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("upload-%s-%s-%d.png", pg.name, theme, w)))
			})
		}
	}
	// The board beside them, in the same two themes.
	for _, theme := range []string{"dark", "light"} {
		t.Run("board-"+theme, func(t *testing.T) {
			tab := b.NewTab(1600, 900, false, false)
			tab.Navigate("file://" + board)
			tab.Eval(`document.getElementById("bd-`+theme+`").click()`, nil)
			time.Sleep(300 * time.Millisecond)
			tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("board-upload-%s.png", theme)))
		})
	}
	t.Logf("screenshots in %s", out)
}
