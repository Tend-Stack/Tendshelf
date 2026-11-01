//go:build dev

package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

// browsePalette are the cover colours of the boards.
var browsePalette = []string{"#1f4a52", "#8c3b24", "#2e3a2a", "#3b2f4a", "#c7894e", "#5a6b73", "#d9d4c7", "#163a40"}

var browseTitlesSeed = []string{
	"The Lighthouse Keeper's Daughter", "Salt and Ember", "A Field Guide to Small Winters", "The Cartographer's Silence",
	"Nine Bridges Home", "The Orchard at Low Tide", "The Quiet Mechanic", "Saltmarsh", "Weather for Beginners", "Lantern Street",
	"The Long Tide", "A Quiet Orchard", "Harbour Lights", "Ninety Winters",
}

// browseWriteCover writes a small solid-colour PNG under the data directory and returns its address.
func browseWriteCover(t *testing.T, dir, key, tint string) (sha, rel string) {
	t.Helper()
	h := sha256.Sum256([]byte(key))
	sum := hex.EncodeToString(h[:])
	r, _ := strconv.ParseUint(tint[1:3], 16, 8)
	g, _ := strconv.ParseUint(tint[3:5], 16, 8)
	b, _ := strconv.ParseUint(tint[5:7], 16, 8)
	img := image.NewRGBA(image.Rect(0, 0, 96, 96))
	for y := 0; y < 96; y++ {
		for x := 0; x < 96; x++ {
			c := color.RGBA{uint8(r), uint8(g), uint8(b), 255}
			if x+y > 100 && x+y < 118 { // a lighter band so the image reads as a cover
				c = color.RGBA{uint8(min(int(r)+60, 255)), uint8(min(int(g)+60, 255)), uint8(min(int(b)+60, 255)), 255}
			}
			img.Set(x, y, c)
		}
	}
	rel = filepath.Join("covers", sum+".png")
	if err := os.MkdirAll(filepath.Join(dir, "covers"), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, rel))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	return sum, rel
}

// browseSeed fills the app with n books in one library: covers in the board colours (every seventh
// book has none), authors, genres and lengths from a few minutes to a day.
func browseSeed(t *testing.T, a *testApp, n int) library.Folder {
	t.Helper()
	f := browseLibrary(t, a, "Audiobooks")
	genres := [][]string{{"Mystery"}, {"Literary"}, {"Mystery", "Thriller"}, {"Fantasy"}, {"Literary", "Romance"}}
	for i := 0; i < n; i++ {
		title := browseTitlesSeed[i%len(browseTitlesSeed)]
		if i >= len(browseTitlesSeed) {
			title += " " + strconv.Itoa(i/len(browseTitlesSeed)+1)
		}
		b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{
			Title: title, Authors: []string{[]string{"Imogen Hale", "Ruth Okafor", "Elias Varn", "Mara Lindqvist"}[i%4]},
			Genres: genres[i%len(genres)], DurationMs: int64(1+(i*7)%30) * 3_000_000, AddedAt: int64(i+1) * 1000,
		})
		if i%7 == 6 {
			continue
		}
		tint := browsePalette[i%len(browsePalette)]
		sha, rel := browseWriteCover(t, a.dir, fmt.Sprintf("cover-%d", i), tint)
		cover := library.Cover{BookID: b.ID, Source: library.CoverFolder, Mime: "image/png", Width: 96, Height: 96, SHA256: sha, Path: rel, Tint: tint}
		if err := a.lib.Update(context.Background(), func(tx *library.Tx) error {
			if err := tx.PutCover(&cover); err != nil {
				return err
			}
			return tx.SetBookCover(b.ID, cover.ID)
		}); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// browseProbeJS checks targets against 43.5 px, not 44: a sheet still sliding in (translate) gives
// its boxes sub-pixel heights such as 43.9999 although min-height is 44 px.
const browseProbeJS = `(() => {
  const small = [];
  for (const el of document.querySelectorAll("main a, main button, main select, #sheet a, #sheet label, #sheet button")) {
    const r = el.getBoundingClientRect();
    if (r.width > 0 && r.height > 0 && Math.min(r.width, r.height) < 43.5) small.push((el.className || el.tagName) + ":" + Math.round(r.width) + "x" + Math.round(r.height));
  }
  const labels = [...document.querySelectorAll("input:not([type=hidden]), select")].filter(i => !document.querySelector('label[for="' + i.id + '"]')).map(i => i.id || i.name);
  let tint = null;
  const sp = document.querySelector(".ts-spine[data-ts-tint]");
  if (sp) {
    const h = sp.dataset.tsTint;
    const rgb = "rgb(" + parseInt(h.slice(1, 3), 16) + ", " + parseInt(h.slice(3, 5), 16) + ", " + parseInt(h.slice(5, 7), 16) + ")";
    tint = {applied: getComputedStyle(sp).backgroundColor === rgb, tinted: sp.classList.contains("is-tinted")};
  }
  const spines = [...document.querySelectorAll(".ts-spine")].map(s => { const r = s.getBoundingClientRect(); return Math.round(r.width) + "x" + Math.round(r.height); });
  return {over: document.documentElement.scrollWidth > innerWidth, theme: document.documentElement.dataset.theme || "",
    small, unlabelled: labels, h1: document.querySelectorAll("h1").length, tint, spines: spines.slice(0, 3)};
})()`

type browseProbe struct {
	Over       bool     `json:"over"`
	Theme      string   `json:"theme"`
	Small      []string `json:"small"`
	Unlabelled []string `json:"unlabelled"`
	H1         int      `json:"h1"`
	Tint       *struct {
		Applied bool `json:"applied"`
		Tinted  bool `json:"tinted"`
	} `json:"tint"`
}

// TestVisualBrowsePages captures Home and the Library in every theme at four widths and fails on
// horizontal overflow, small touch targets, an unlabelled input, a page without exactly one h1, or a
// tint that is missing outside the contrast theme or present inside it. The board PNGs are copied
// beside the captures.
func TestVisualBrowsePages(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")

	seeded := newTestApp(t)
	seeded.owner(t)
	browseSeed(t, seeded, 60)
	seededBase := themedServer(t, seeded.signedIn(t))

	empty := newTestApp(t)
	emptyBase := themedServer(t, empty.signedIn(t))

	scanning := newTestApp(t)
	scanning.owner(t)
	sf := browseSeed(t, scanning, 9)
	scanning.adminEnqueueScan(t, sf.LibraryID)
	scanningBase := themedServer(t, scanning.signedIn(t))

	boards := map[string]string{
		"HomePhone": "core", "HomePhoneLight": "core", "HomeTablet": "core", "HomeTabletLight": "core", "HomeDesktop": "core", "HomeDesktopLight": "core",
		"LibraryPhone": "browse", "LibraryListPhone": "browse", "LibraryFiltersPhone": "browse", "LibraryEmptyPhone": "browse",
		"LibraryScanningPhone": "browse", "LibraryTablet": "browse", "LibraryPage": "browse",
		"StateLoading": "states", "StateNoResults": "states",
	}
	for name, dir := range boards {
		src, err := os.Open(filepath.Join("..", "..", "docs", "design", "tendshelf-handoff", "screens", dir, name+".png"))
		if err != nil {
			continue
		}
		if dst, err := os.Create(filepath.Join(out, "browse-board-"+name+".png")); err == nil {
			_, _ = io.Copy(dst, src)
			_ = dst.Close()
		}
		_ = src.Close()
	}

	pgs := []struct{ name, base, path string }{
		{"home", seededBase, "/"},
		{"library-grid", seededBase, "/libraries"},
		{"library-list", seededBase, "/libraries?view=list"},
		{"library-filters", seededBase, "/libraries?sheet=filters"},
		{"library-nomatch", seededBase, "/libraries?genre=Mystery&genre=Romance"},
		{"home-empty", emptyBase, "/"},
		{"library-empty", emptyBase, "/libraries"},
		{"library-scanning", scanningBase, "/libraries"},
		{"home-scanning", scanningBase, "/"},
	}
	for _, pg := range pgs {
		for _, theme := range themes {
			for _, w := range []int{390, 768, 1024, 1440} {
				t.Run(fmt.Sprintf("%s-%s-%d", pg.name, theme, w), func(t *testing.T) {
					tab := b.NewTab(w, 900, w == 390, false)
					sep := "?"
					if strings.Contains(pg.path, "?") {
						sep = "&"
					}
					tab.Navigate(pg.base + pg.path + sep + "theme=" + theme)
					var m browseProbe
					tab.Eval(browseProbeJS, &m)
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
					if m.Tint != nil {
						if theme == "contrast" && (m.Tint.Applied || !m.Tint.Tinted) {
							t.Errorf("contrast theme: tint applied=%v tinted=%v, want no tint on a tinted element", m.Tint.Applied, m.Tint.Tinted)
						}
						if theme != "contrast" && !m.Tint.Applied {
							t.Errorf("%s theme: the spine tint was not applied", theme)
						}
					}
					tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("browse-%s-%s-%d.png", pg.name, theme, w)))
				})
			}
		}
	}
	t.Logf("screenshots in %s", out)
}

// TestVisualLibraryFilterSheet is the check behind the plan's guess that a boosted GET form with
// hx-select-oob="#sheet" closes the sheet with the swap: open the sheet, tick a genre, submit; the
// address gains genre=, #sheet is empty and #main shows the filtered grid. Back returns to the
// unfiltered list.
func TestVisualLibraryFilterSheet(t *testing.T) {
	b := chromium(t)
	app := newTestApp(t)
	app.owner(t)
	browseSeed(t, app, 60)
	srv := httptest.NewServer(app.signedIn(t))
	t.Cleanup(srv.Close)

	tab := b.NewTab(390, 844, true, false)
	tab.Navigate(srv.URL + "/libraries")
	tab.WaitFor(`window.htmx !== undefined`, 5*time.Second)
	tab.Eval(`document.querySelector(".ts-lib-sort").click()`, nil)
	tab.WaitFor(`Boolean(document.querySelector("#sheet .ts-sheet"))`, 5*time.Second)
	var before int
	tab.Eval(`document.querySelectorAll("#library-grid .ts-card").length`, &before)
	tab.Screenshot(filepath.Join(visualDir(t, "chromium"), "browse-sheet-open-390.png"))
	tab.Eval(`(() => { const c = [...document.querySelectorAll("#sheet input[type=checkbox]")].find(i => i.value === "Literary"); c.click(); })()`, nil)
	tab.Eval(`document.querySelector("#sheet .ts-filter-submit").click()`, nil)
	tab.WaitFor(`location.search.includes("genre=Literary")`, 5*time.Second)
	tab.WaitFor(`document.querySelector("#sheet").children.length === 0`, 5*time.Second)
	var got struct {
		Cards, Chips int
		Pressed      bool
		SheetOpen    bool
	}
	tab.Eval(`({cards: document.querySelectorAll("#library-grid .ts-card").length, chips: document.querySelectorAll(".ts-lib-filters .ts-chip").length,
		pressed: Boolean(document.querySelector('.ts-lib-filters .ts-chip[aria-current="true"]')), sheetOpen: Boolean(document.querySelector("#sheet .ts-sheet"))})`, &got)
	if got.Cards == 0 || got.Cards >= before || !got.Pressed || got.SheetOpen {
		t.Errorf("after the filter: %+v (cards before %d)", got, before)
	}
	tab.Eval(`history.back()`, nil)
	tab.WaitFor(`!location.search.includes("genre=")`, 5*time.Second)
	tab.WaitFor(`document.querySelectorAll("#library-grid .ts-card").length === `+strconv.Itoa(before), 5*time.Second)
}

// TestVisualLibraryInfiniteScroll loads the next page when the sentinel scrolls into view, keeps the
// tints on the new items, and shows the skeletons (StateLoading) while the request is pending.
func TestVisualLibraryInfiniteScroll(t *testing.T) {
	b := chromium(t)
	app := newTestApp(t)
	app.owner(t)
	browseSeed(t, app, 60)
	var slow atomic.Bool
	signed := app.signedIn(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if slow.Load() && strings.HasPrefix(r.URL.Path, "/hx/libraries/") {
			time.Sleep(2500 * time.Millisecond)
		}
		signed.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)

	tab := b.NewTab(390, 844, true, false)
	slow.Store(true)
	tab.Navigate(srv.URL + "/libraries")
	tab.WaitFor(`window.htmx !== undefined`, 5*time.Second)
	tab.Eval(`document.querySelector(".ts-more").scrollIntoView({block: "center"})`, nil)
	tab.WaitFor(`Boolean(document.querySelector(".ts-more.htmx-request"))`, 5*time.Second)
	tab.Screenshot(filepath.Join(visualDir(t, "chromium"), "browse-loading-390.png"))
	tab.WaitFor(`document.querySelectorAll("#library-grid .ts-card").length === 60 && !document.querySelector(".ts-more")`, 10*time.Second)
	var blanks, tinted int
	tab.Eval(`document.querySelectorAll("#library-grid .ts-cover--blank").length`, &blanks)
	tab.Eval(`document.querySelectorAll("#library-grid .ts-cover--blank.is-tinted").length`, &tinted)
	if blanks == 0 {
		t.Error("no blank covers in the seed")
	}
	if tinted != 0 {
		t.Errorf("%d blank covers tinted, the seed gave them no tint", tinted)
	}
}
