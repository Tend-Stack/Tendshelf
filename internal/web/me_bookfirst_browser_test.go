//go:build dev

package web

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
	"github.com/Tend-Stack/tendshelf/internal/testutil/wav"
)

// Profile in Chromium (decision 0021, PR2). The fixture is the player's book (the hero, with sound), eight
// more short books under way, seven finished and three shelves; every title and name is invented.

// bookfirstWorld is playerApp plus the books of Profile. tiles are the ids of the books under way after the
// hero, newest first.
func bookfirstWorld(t *testing.T) (e *playerEnv, tiles []string) {
	t.Helper()
	e = playerAppWith(t, func(s *librarytest.BookSpec) {
		s.Description = "A calm harbour at dusk, and a lamp that will not go out."
	})
	ctx := context.Background()
	now := time.Now().UnixMilli()
	e.store(t, 60_000, "bookfirstdev01", "Phone", now, false)
	titles := []string{"The Orchard at Low Tide", "A Field Guide to Small Winters", "The Last Ferry North", "The Glass Orchard",
		"Nine Bridges Home", "The Fog Bell", "The Quiet Mechanic", "Winter Signal"}
	for i, title := range titles {
		b := librarytest.AddBook(t, e.a.lib, e.folder, librarytest.BookSpec{Title: title, Authors: []string{"Imogen Hale"}, DurationMs: 90_000, Files: 1})
		bookVisualCover(t, e.a, b, i+1)
		files, err := e.a.lib.AudioFiles(ctx, b.ID)
		if err != nil || len(files) != 1 {
			t.Fatalf("audio files = %d, %v", len(files), err)
		}
		if err := wav.Write(files[0].Path, 90_000, 300+i*40); err != nil {
			t.Fatal(err)
		}
		fi, err := os.Stat(files[0].Path)
		if err != nil {
			t.Fatal(err)
		}
		streamExec(t, e.a, `UPDATE audio_files SET container='wav', codec='pcm_s16le', duration_ms=90000, offset_ms=0, size=?, mtime_ns=? WHERE id=?`,
			fi.Size(), fi.ModTime().UnixNano(), files[0].ID)
		if _, err := e.a.srv.progress.Save(ctx, progress.Update{UserID: e.ownerID, BookID: b.ID, PositionMs: 30_000, DurationMs: 90_000,
			ClientUpdatedAt: now - int64(i+2)*60_000, DeviceID: "bookfirstdev01"}); err != nil {
			t.Fatal(err)
		}
		tiles = append(tiles, b.ID)
	}
	var shelf library.Shelf
	for i, title := range []string{"Salt and Ember", "A Map of Small Hours", "Notes from the Lower Field", "Of Bread and Weather", "Saltmarsh", "Winter Lantern", "Harbour Post"} {
		b := librarytest.AddBook(t, e.a.lib, e.folder, librarytest.BookSpec{Title: title, Authors: []string{"Tomás Reyes"}, DurationMs: 90_000, Files: 1})
		bookVisualCover(t, e.a, b, i+3)
		if _, err := e.a.srv.progress.Save(ctx, progress.Update{UserID: e.ownerID, BookID: b.ID, PositionMs: 90_000, DurationMs: 90_000, Finished: true,
			ClientUpdatedAt: now - int64(i+1)*86_400_000, DeviceID: "bookfirstdev01"}); err != nil {
			t.Fatal(err)
		}
		if i%3 == 0 {
			var err error
			if shelf, err = e.a.lib.CreateShelf(ctx, e.ownerID, []string{"Sunday mysteries", "For Mamá", "Road trips"}[i/3]); err != nil {
				t.Fatal(err)
			}
		}
		if err := e.a.lib.AddToShelf(ctx, e.ownerID, shelf.ID, b.ID); err != nil {
			t.Fatal(err)
		}
	}
	return e, tiles
}

// bookfirstProbe is what one load of Profile looks like to the person.
const bookfirstProbe = `(() => {
  const vis = (el) => { const r = el.getBoundingClientRect(); return r.width > 0 && r.height > 0; };
  const rows = [...document.querySelectorAll(".ts-pbook")].filter(vis);
  const li = rows[0];
  const ctl = li && li.querySelector(":scope > .ts-iconbtn");
  const cover = li && li.querySelector(".ts-card__cover");
  const inside = ctl && cover ? (() => { const a = ctl.getBoundingClientRect(), b = cover.getBoundingClientRect();
    return a.left >= b.left - 0.5 && a.right <= b.right + 0.5 && a.top >= b.top - 0.5 && a.bottom <= b.bottom + 0.5; })() : false;
  const cut = [...document.querySelectorAll(".ts-pbook .ts-card__author.ts-num")].filter(vis).filter(e => e.scrollWidth > e.clientWidth + 1).map(e => e.textContent);
  const cols = getComputedStyle(document.querySelector(".ts-profile__cols")).gridTemplateColumns;
  return { over: document.documentElement.scrollWidth > innerWidth, rows: rows.length, tiles: li ? getComputedStyle(li).display === "block" : false,
    inside, cut, columns: cols.split(" ").length, finished: [...document.querySelectorAll(".ts-profile__done > *")].filter(vis).length };
})()`

type bookfirstShape struct {
	Over     bool     `json:"over"`
	Rows     int      `json:"rows"`
	Tiles    bool     `json:"tiles"`
	Inside   bool     `json:"inside"`
	Cut      []string `json:"cut"`
	Columns  int      `json:"columns"`
	Finished int      `json:"finished"`
}

func TestBookFirstBrowser(t *testing.T) {
	b := chromium(t)
	for _, w := range []int{390, 768, 1024, 1440} {
		t.Run(fmt.Sprint(w), func(t *testing.T) {
			e, tiles := bookfirstWorld(t)
			tab := b.NewTab(w, 900, w < 768, false)
			tab.Navigate(e.URL + "/me")
			tab.WaitFor(`window.tsPlayer !== undefined && document.querySelector(".ts-pbook") !== null`, 10*time.Second)
			var m browseProbe
			tab.Eval(browseProbeJS, &m)
			if m.Over {
				t.Error("horizontal overflow")
			}
			if len(m.Small) > 0 {
				t.Errorf("targets under 44 px: %v", m.Small)
			}
			if m.H1 != 1 {
				t.Errorf("%d h1", m.H1)
			}
			var s bookfirstShape
			tab.Eval(bookfirstProbe, &s)
			switch {
			case w == 390 && (s.Rows != 5 || s.Tiles || s.Finished != 3):
				t.Errorf("phone: %d rows (want 5), tiles %v, %d finished (want 3)", s.Rows, s.Tiles, s.Finished)
			case w >= 768 && (!s.Tiles || !s.Inside || s.Rows != 8 || s.Finished != 6):
				t.Errorf("%d: tiles %v, control inside the cover %v, %d tiles (want 8), %d finished (want 6)", w, s.Tiles, s.Inside, s.Rows, s.Finished)
			}
			if w == 1440 && s.Columns != 2 {
				t.Errorf("1440 with the panel closed: %d columns, want 2", s.Columns)
			}
			if len(s.Cut) > 0 {
				t.Errorf("the time left is cut: %v", s.Cut)
			}

			// Tab reaches every tile control, and each shows a focus ring.
			seen := map[string]bool{}
			for i := 0; i < 90 && len(seen) < min(s.Rows, 5); i++ {
				for _, typ := range []string{"keyDown", "keyUp"} {
					if err := tab.Call("Input.dispatchKeyEvent", map[string]any{"type": typ, "key": "Tab", "code": "Tab", "windowsVirtualKeyCode": 9}, nil); err != nil {
						t.Fatal(err)
					}
				}
				var f struct {
					ID   string `json:"id"`
					Ring bool   `json:"ring"`
				}
				tab.Eval(`({id: (document.activeElement.closest(".ts-pbook") && document.activeElement.matches(".ts-iconbtn")) ? document.activeElement.dataset.tsPlay || document.activeElement.getAttribute("href") : "",
				  ring: getComputedStyle(document.activeElement).boxShadow !== "none" || getComputedStyle(document.activeElement).outlineStyle !== "none"})`, &f)
				if f.ID != "" {
					seen[f.ID] = true
					if !f.Ring {
						t.Errorf("no focus ring on %s", f.ID)
					}
				}
			}
			if len(seen) < min(s.Rows, 5) {
				t.Errorf("Tab reached %d tile controls, want %d", len(seen), min(s.Rows, 5))
			}

			// A tile's control plays that book; paused, with the control on screen, the mini-player hides.
			sel := `.ts-pbook [data-ts-play="` + tiles[0] + `"]`
			playerClickSel(t, tab, sel)
			tab.WaitFor(`window.tsPlayer.state().playing && window.tsPlayer.state().bookId === `+cdp.Quote(tiles[0]), 10*time.Second)
			tab.Eval(`window.tsPlayer.pause()`, nil)
			tab.WaitFor(`document.getElementById("player").classList.contains("is-suppressed")`, 5*time.Second)

			// 200 % text wraps: no overflow, the time left whole. At 768 px the hero's card (shared with Home) is
			// narrower than a Resume button at 200 % text alone (browser zoom would give it the phone layout), so
			// that width is checked at 150 %.
			size := "200%"
			if w == 768 {
				size = "150%"
			}
			tab.Eval(`document.documentElement.style.fontSize = `+cdp.Quote(size), nil)
			time.Sleep(300 * time.Millisecond)
			tab.Eval(bookfirstProbe, &s)
			if s.Over || len(s.Cut) > 0 {
				var wide []string
				tab.Eval(`[...document.querySelectorAll("body *")].filter(e => e.getBoundingClientRect().right > innerWidth + 1).slice(0, 6).map(e => e.tagName + "." + e.className + ":" + Math.round(e.getBoundingClientRect().left) + "-" + Math.round(e.getBoundingClientRect().right)).concat([...document.querySelectorAll(".ts-hero__top, .ts-hero__text, .ts-hero__actions, .ts-profile, main")].map(e => e.className + "=" + Math.round(e.getBoundingClientRect().left) + "-" + Math.round(e.getBoundingClientRect().right)))`, &wide)
				t.Errorf("200 %% text: overflow %v, cut %v, wide %v", s.Over, s.Cut, wide)
			}
		})
	}
}

// TestBookFirstBrowserPanel: at 1440 with the Now playing panel open the page is one column.
func TestBookFirstBrowserPanel(t *testing.T) {
	e, _ := bookfirstWorld(t)
	tab := nowPanelOpen(t, e, 1440, 900)
	persistClickNav(t, tab, `a[data-nav="/me"]`, "/me")
	tab.WaitFor(`location.pathname === "/me" && document.querySelector(".ts-profile__cols") !== null`, 8*time.Second)
	time.Sleep(300 * time.Millisecond)
	var s bookfirstShape
	tab.Eval(bookfirstProbe, &s)
	if s.Columns != 1 || s.Over {
		t.Errorf("panel open: %d columns, overflow %v; want one column", s.Columns, s.Over)
	}
	tab.ScreenshotFull(filepath.Join(visualDir(t, "chromium"), "bookfirst-panel-dark-1440.png"))
}

// TestBookFirstVisual captures Profile in the four themes at 390, 768, 1024 and 1440 (files bookfirst-{theme}-{width}.png)
// and the board in each theme, for the side-by-side look.
func TestBookFirstVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	e, _ := bookfirstWorld(t)
	base := themedServer(t, e.spy)
	for _, th := range []string{"dark", "light", "sepia", "contrast"} {
		for _, w := range []int{390, 768, 1024, 1440} {
			t.Run(fmt.Sprintf("%s-%d", th, w), func(t *testing.T) {
				tab := b.NewTab(w, 900, w == 390, false)
				tab.Navigate(base + "/me?theme=" + th)
				tab.WaitFor(`document.querySelector(".ts-pbook") !== null`, 10*time.Second)
				var m browseProbe
				tab.Eval(browseProbeJS, &m)
				if m.Over || m.Theme != th || len(m.Small) > 0 || m.H1 != 1 {
					t.Errorf("overflow %v, theme %q, small %v, h1 %d", m.Over, m.Theme, m.Small, m.H1)
				}
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("bookfirst-%s-%d.png", th, w)))
			})
		}
	}
	for _, th := range []string{"dark", "light", "sepia", "contrast"} {
		tab := b.NewTab(1500, 900, false, false)
		tab.Navigate("file://" + mustAbs(t, filepath.Join("..", "..", "docs", "design", "boards", "Profile.html")))
		tab.Eval(`document.getElementById("bd-`+th+`").checked = true`, nil)
		time.Sleep(400 * time.Millisecond)
		tab.ScreenshotFull(filepath.Join(out, "bookfirst-board-"+th+".png"))
	}
}

func mustAbs(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	if err != nil {
		t.Fatal(err)
	}
	return a
}
