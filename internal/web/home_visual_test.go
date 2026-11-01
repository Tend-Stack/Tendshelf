//go:build dev

package web

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// homeVisualSeed gives the owner a hero book at 62 %, two series (one with a started next book, one
// with a fresh next book) and a finished book that seeds "Because you listened to".
func homeVisualSeed(t *testing.T, a *testApp, f library.Folder) library.Book {
	t.Helper()
	ctx := context.Background()
	uid := sweepUserID(t, a, a.owner(t))
	add := func(title, author, narrator, series, seq string, hours int64, tint string) library.Book {
		b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: title, Authors: []string{author}, Narrators: []string{narrator},
			Series: series, Sequence: seq, DurationMs: hours * 3_600_000, AddedAt: time.Now().UnixMilli()})
		sha, rel := browseWriteCover(t, a.dir, "home-"+title, tint)
		cover := library.Cover{BookID: b.ID, Source: library.CoverFolder, Mime: "image/png", Width: 96, Height: 96, SHA256: sha, Path: rel, Tint: tint}
		if err := a.lib.Update(ctx, func(tx *library.Tx) error {
			if err := tx.PutCover(&cover); err != nil {
				return err
			}
			return tx.SetBookCover(b.ID, cover.ID)
		}); err != nil {
			t.Fatal(err)
		}
		return b
	}
	hero := add("The Lighthouse Keeper's Daughter", "Imogen Hale", "Theo Marsh", "Harbour Lights", "2", 17, "#1f4a52")
	h1 := add("The Fog Bell", "Imogen Hale", "Theo Marsh", "Harbour Lights", "1", 9, "#3d4f5c")
	h3 := add("Low Water", "Imogen Hale", "Theo Marsh", "Harbour Lights", "3", 9, "#2e3a2a")
	t1 := add("Salt and Ember", "Ruth Okafor", "Noor Haddad", "The Tidewater Books", "1", 11, "#8c3b24")
	t2 := add("The Orchard at Low Tide", "Ruth Okafor", "Noor Haddad", "The Tidewater Books", "2", 12, "#c7894e")
	add("Winter Signal", "Imogen Hale", "Noor Haddad", "", "", 8, "#6b2f3a")
	add("The Quiet Mechanic", "Jonah Feld", "Theo Marsh", "", "", 10, "#5a6b73")
	_, _ = h3, t2
	streamExec(t, a, `UPDATE audio_files SET container='mp3', codec='mp3', duration_ms=(SELECT duration_ms FROM books b WHERE b.id = audio_files.book_id)`)
	var chs []library.Chapter
	for i := int64(0); i < 19; i++ {
		chs = append(chs, library.Chapter{StartMs: i * 3_200_000, EndMs: (i + 1) * 3_200_000, Title: fmt.Sprintf("Chapter %d", i+1), Source: library.ChapterFile})
	}
	if err := a.lib.Update(ctx, func(tx *library.Tx) error { return tx.ReplaceChapters(hero.ID, chs) }); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UnixMilli()
	save := func(id string, pos, dur, ago int64, fin bool) {
		if _, err := a.srv.progress.Save(ctx, progress.Update{UserID: uid, BookID: id, PositionMs: pos, DurationMs: dur, ClientUpdatedAt: now - ago,
			DeviceID: "homevisual0001", DeviceLabel: "Test", Finished: fin}); err != nil {
			t.Fatal(err)
		}
	}
	save(hero.ID, 17*3_600_000*62/100, 17*3_600_000, 1000, false)
	save(h1.ID, 9*3_600_000, 9*3_600_000, 90_000, true)
	save(t1.ID, 11*3_600_000*35/100, 11*3_600_000, 60_000, false)
	return hero
}

// TestHomeVisualBookPlay captures the hero book's page (About and Chapters) with the play region and
// the chapter links, and fails on overflow, small targets or a second control for the book.
func TestHomeVisualBookPlay(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	a := newTestApp(t)
	a.owner(t)
	f := browseSeed(t, a, 6)
	hero := homeVisualSeed(t, a, f)
	base := themedServer(t, a.signedIn(t))
	for _, sh := range visualMatrix(true) {
		for _, tab := range []string{"", "/chapters"} {
			theme, w := sh.Theme, sh.Width
			name := "about"
			if tab != "" {
				name = "chapters"
			}
			t.Run(fmt.Sprintf("%s-%s-%d", name, theme, w), func(t *testing.T) {
				tb := b.NewTab(w, 900, w == 390, false)
				tb.Navigate(base + "/books/" + hero.ID + tab + "?theme=" + theme)
				if tab == "" { // the page's Resume is on screen, so the mini-player must end up hidden
					tb.WaitFor(`window.tsPlayer !== undefined && document.getElementById("player").classList.contains("is-suppressed")`, 8*time.Second)
					time.Sleep(450 * time.Millisecond)
				}
				var m browseProbe
				tb.Eval(browseProbeJS, &m)
				if m.Over {
					t.Errorf("horizontal overflow")
				}
				if len(m.Small) > 0 {
					t.Errorf("targets under 44 px: %v", m.Small)
				}
				var n int
				tb.Eval(`document.querySelectorAll("[data-ts-play='`+hero.ID+`']").length`, &n)
				if n != 1 {
					t.Errorf("%d play controls for the book", n)
				}
				tb.ScreenshotFull(filepath.Join(out, fmt.Sprintf("book-play-%s-%s-%d.png", name, theme, w)))
			})
		}
	}
}

// TestHomeVisualListening captures Home with a hero, series rows and a because row in the matrix and
// fails on horizontal overflow, small targets, or a second resume control for one book. The boards
// HomePhone and HomeDesktop are copied beside the captures.
func TestHomeVisualListening(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	a := newTestApp(t)
	a.owner(t)
	f := browseSeed(t, a, 12)
	homeVisualSeed(t, a, f)
	base := themedServer(t, a.signedIn(t))
	for _, name := range []string{"HomePhone", "HomePhoneLight", "HomeTablet", "HomeDesktop", "HomeDesktopLight"} {
		if src, err := os.Open(filepath.Join("..", "..", "docs", "design", "tendshelf-handoff", "screens", "core", name+".png")); err == nil {
			if dst, err := os.Create(filepath.Join(out, "home-board-"+name+".png")); err == nil {
				_, _ = io.Copy(dst, src)
				_ = dst.Close()
			}
			_ = src.Close()
		}
	}
	for _, sh := range visualMatrix(true) {
		theme, w := sh.Theme, sh.Width
		t.Run(fmt.Sprintf("%s-%d", theme, w), func(t *testing.T) {
			tab := b.NewTab(w, 900, w == 390, false)
			tab.Navigate(base + "/?theme=" + theme)
			// The hero's Resume is on screen, so the mini-player must end up hidden (one resume per book).
			tab.WaitFor(`window.tsPlayer !== undefined && document.getElementById("player").classList.contains("is-suppressed")`, 8*time.Second)
			time.Sleep(450 * time.Millisecond) // the bar slides out over 250 ms
			var m browseProbe
			tab.Eval(browseProbeJS, &m)
			if m.Over {
				t.Errorf("horizontal overflow")
			}
			if len(m.Small) > 0 {
				t.Errorf("targets under 44 px: %v", m.Small)
			}
			var counts map[string]int
			tab.Eval(`(() => { const c = {}; for (const el of document.querySelectorAll("[data-ts-play]")) c[el.dataset.tsPlay] = (c[el.dataset.tsPlay] || 0) + 1; return c; })()`, &counts)
			for id, n := range counts {
				if n != 1 {
					t.Errorf("%d play controls for %s", n, id)
				}
			}
			if len(counts) != 2 {
				t.Errorf("%d books have a play control, want 2 (hero and one series row)", len(counts))
			}
			tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("home-listening-%s-%d.png", theme, w)))
		})
	}
	t.Logf("screenshots in %s", out)
}
