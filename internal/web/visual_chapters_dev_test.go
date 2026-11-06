//go:build dev

package web

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/metadata/online"
)

// TestChapterEditVisual captures the Edit chapters page (nineteen edited chapters, so the reset link
// shows) and the reset confirmation at 390, 768, 1024 and 1440 px, as chapters-edit-*. It fails on
// horizontal overflow, a target under 44 px, an unlabelled field, a page without one h1, a skipped
// heading level or an inline style. No board exists for this page.
func TestChapterEditVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	app := newTestApp(t)
	app.owner(t)
	bookID := bookmarkVisualSeedChapters(t, app)
	base := themedServer(t, app.signedIn(t))
	shots := visualMatrix(true)
	pages := []struct{ name, path string }{
		{"page", "/books/" + bookID + "/chapters/edit"},
		{"reset", "/books/" + bookID + "/chapters/reset"},
	}
	for _, p := range pages {
		for _, sh := range shots {
			p, sh := p, sh
			t.Run(fmt.Sprintf("%s-%s-%d", p.name, sh.Theme, sh.Width), func(t *testing.T) {
				tab := b.NewTab(sh.Width, 900, sh.Width == 390, false)
				tab.Navigate(base + p.path + "?theme=" + sh.Theme)
				time.Sleep(200 * time.Millisecond)
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
				if m.H1 != 1 || m.Skip || m.Styled > 0 {
					t.Errorf("h1 = %d, skipped level = %v, styled = %d", m.H1, m.Skip, m.Styled)
				}
				tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("chapters-edit-%s-%s-%d.png", p.name, sh.Theme, sh.Width)))
			})
		}
	}
}

// bookmarkVisualSeedChapters seeds the Harbour Lights book with nineteen chapters saved as the owner's own.
func bookmarkVisualSeedChapters(t *testing.T, a *testApp) string {
	t.Helper()
	bookID, _, _, _ := bookVisualSeed(t, a)
	const chapterMs = 3_300_000
	var chs []library.Chapter
	for i := 0; i < 19; i++ {
		chs = append(chs, library.Chapter{StartMs: int64(i) * chapterMs, EndMs: int64(i+1) * chapterMs, Title: fmt.Sprintf("Chapter %d", i+1), Source: "user"})
	}
	if err := a.lib.Update(context.Background(), func(tx *library.Tx) error { return tx.ReplaceChapters(bookID, chs) }); err != nil {
		t.Fatal(err)
	}
	return bookID
}

// TestChapterLookupVisual captures the Edit chapters page with the lookup form open and a finished
// lookup in its box (Audible's list is 12 s longer than the book, so the warning shows) at 390, 768,
// 1024 and 1440 px, as chapters-lookup-*, with the same checks as TestChapterEditVisual.
func TestChapterLookupVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	app := newTestApp(t, compareMod(t, &compareNet{}))
	app.owner(t)
	bookID := bookmarkVisualSeedChapters(t, app)
	job, err := app.srv.details.StartChapterLookup(context.Background(), bookID, "B0TESTBOOK", "")
	if err != nil {
		t.Fatal(err)
	}
	var item []online.ChapterItem
	for i := 0; i < 19; i++ {
		item = append(item, online.ChapterItem{StartMs: int64(i) * 3_300_000, LengthMs: 3_300_000, Title: fmt.Sprintf("Part %d", i+1)})
	}
	book, err := app.lib.Book(context.Background(), bookID)
	if err != nil {
		t.Fatal(err)
	}
	res, err := json.Marshal(online.ChapterList{ASIN: "B0TESTBOOK", RuntimeMs: book.DurationMs + 12_000, Chapters: item})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := app.st.Writer().Exec(`UPDATE jobs SET state = 'done', result_json = ? WHERE id = ?`, string(res), job); err != nil {
		t.Fatal(err)
	}
	base := themedServer(t, app.signedIn(t))
	for _, sh := range visualMatrix(true) {
		sh := sh
		t.Run(fmt.Sprintf("%s-%d", sh.Theme, sh.Width), func(t *testing.T) {
			tab := b.NewTab(sh.Width, 900, sh.Width == 390, false)
			tab.Navigate(base + "/books/" + bookID + "/chapters/edit?lookup=" + job + "&theme=" + sh.Theme)
			time.Sleep(200 * time.Millisecond)
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
			if m.H1 != 1 || m.Skip || m.Styled > 0 {
				t.Errorf("h1 = %d, skipped level = %v, styled = %d", m.H1, m.Skip, m.Styled)
			}
			tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("chapters-lookup-%s-%d.png", sh.Theme, sh.Width)))
		})
	}
}
