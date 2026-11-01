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
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

// bookmarkVisualSeed gives the Harbour Lights book of the boards nineteen chapters of 55 minutes and
// the six bookmarks of BookmarksPhone (four with notes), made on days that read "Tonight",
// "Yesterday", "Tuesday", "24 Oct" and "19 Oct" on Thursday 5 November 2026. The owner is 14:06 into
// chapter 2. It returns the book id and the id of the bookmark whose note sheet the test opens.
func bookmarkVisualSeed(t *testing.T, a *testApp) (bookID, openID string) {
	t.Helper()
	ctx := context.Background()
	bookID, _, _, _ = bookVisualSeed(t, a)
	const chapterMs = 3_300_000
	var chs []library.Chapter
	for i := 0; i < 19; i++ {
		chs = append(chs, library.Chapter{StartMs: int64(i) * chapterMs, EndMs: int64(i+1) * chapterMs, Title: fmt.Sprintf("Chapter %d", i+1), Source: library.ChapterFile})
	}
	if err := a.lib.Update(ctx, func(tx *library.Tx) error { return tx.ReplaceChapters(bookID, chs) }); err != nil {
		t.Fatal(err)
	}
	var owner string
	if err := a.st.Reader().QueryRow(`SELECT id FROM users WHERE role = 'owner'`).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	at := func(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, time.Local) }
	for _, b := range []struct {
		chapter    int
		inMs       int64
		note       string
		created    time.Time
		openedHere bool
	}{
		{12, 842_000, "The second lamp is her father's. Check this against chapter 3.", at(time.November, 5, 19), true},
		{11, 1_367_000, "The oilcloth smelled of tar. Ask Nell about it.", at(time.November, 4, 9), false},
		{9, 161_000, "Lovely description of the harbour at dawn.", at(time.November, 3, 9), false},
		{6, 2_415_000, "Who is rowing out from the mainland? First hint.", at(time.October, 24, 9), false},
		{3, 690_000, "", at(time.October, 19, 9), false},
		{1, 300_000, "", at(time.October, 18, 9), false},
	} {
		pos := int64(b.chapter-1)*chapterMs + b.inMs
		bm, err := progress.New(a.st, func() time.Time { return b.created }).AddBookmark(ctx, owner, bookID, pos, b.note)
		if err != nil {
			t.Fatal(err)
		}
		if b.openedHere {
			openID = bm.ID
		}
	}
	_, err := progress.New(a.st, nil).Save(ctx, progress.Update{
		UserID: owner, BookID: bookID, PositionMs: chapterMs + 846_000, DurationMs: 63_120_000,
		ClientUpdatedAt: time.Now().UnixMilli(), DeviceID: "device-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	return bookID, openID
}

// TestBookmarkVisual captures the Bookmarks tab (six bookmarks, four with notes), the note sheet and
// the export sheet at the widths and themes of visualMatrix. It fails on horizontal overflow, a
// target under 44 px (the hidden "Play from here" is not measured), an unlabelled field, a page
// without one h1, a skipped heading level or an inline style. The BookmarksPhone board is copied
// beside the captures for the side-by-side at 390 px.
func TestBookmarkVisual(t *testing.T) {
	b := chromium(t)
	out := visualDir(t, "chromium")
	old := bookmarkNow
	bookmarkNow = func() time.Time { return time.Date(2026, time.November, 5, 12, 0, 0, 0, time.Local) }
	t.Cleanup(func() { bookmarkNow = old })
	app := newTestApp(t)
	app.owner(t)
	bookID, openID := bookmarkVisualSeed(t, app)
	base := themedServer(t, app.signedIn(t))

	if src, err := os.Open(filepath.Join("..", "..", "docs", "design", "tendshelf-handoff", "screens", "browse", "BookmarksPhone.png")); err == nil {
		if dst, err := os.Create(filepath.Join(out, "bookmarks-board-BookmarksPhone.png")); err == nil {
			_, _ = io.Copy(dst, src)
			_ = dst.Close()
		}
		_ = src.Close()
	}

	type page struct{ name, path, wait string }
	tabPath := "/books/" + bookID + "/bookmarks"
	main := []page{{"tab", tabPath, ""}}
	rest := []page{
		{"notes", tabPath + "?only=notes", ""},
		{"sheet", tabPath + "?sheet=bookmark&bookmark=" + openID, ".ts-bookmark-sheet"},
		{"export", tabPath + "?sheet=export", ".ts-bookmark-sheet"},
	}
	for i, group := range [][]page{main, rest} {
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
					if p.wait != "" {
						tab.WaitFor(`Boolean(document.querySelector("`+p.wait+`"))`, 5*time.Second)
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
					if p.name == "tab" {
						var rows struct{ Marks, Edits, Labelled int }
						tab.Eval(`({marks: document.querySelectorAll("#book-panel .ts-mark").length,
							edits: document.querySelectorAll("#book-panel .ts-mark .ts-iconbtn").length,
							labelled: document.querySelectorAll('#book-panel .ts-mark .ts-iconbtn[aria-label]').length})`, &rows)
						if rows.Marks != 6 || rows.Edits != 6 || rows.Labelled != 6 {
							t.Errorf("rows = %+v, want six with a labelled edit control", rows)
						}
					}
					tab.ScreenshotFull(filepath.Join(out, fmt.Sprintf("bookmarks-%s-%s-%d.png", p.name, sh.Theme, sh.Width)))
					if p.name == "tab" && sh.Width == 390 {
						// What a phone shows with the list scrolled: the add button sits above the tab bar.
						tab.Eval(`window.scrollTo(0, 640)`, nil)
						time.Sleep(150 * time.Millisecond)
						tab.Screenshot(filepath.Join(out, fmt.Sprintf("bookmarks-tab-view-%s-%d.png", sh.Theme, sh.Width)))
					}
				})
			}
		}
	}
}
