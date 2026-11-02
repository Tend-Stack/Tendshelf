package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/web/components"
	"github.com/Tend-Stack/tendshelf/internal/web/pages"
)

// Views and the export text of the Bookmarks tab (P1c-7a). Every read is the signed-in person's own:
// the service takes the user id, so another person's bookmark is never loaded.

// errBookmarkNotFound makes the page answer 404: the sheet names a bookmark that is not the
// person's own live bookmark on this book.
var errBookmarkNotFound = errors.New("web: bookmark not found")

func bookmarkTabHref(bookID string) string { return bookTabHref(bookID, "bookmarks") }

func bookmarkSheetHref(bookID, id string) string {
	return bookmarkTabHref(bookID) + "?sheet=bookmark&bookmark=" + url.QueryEscape(id)
}

// bookmarkWhere places a position in the book: the number of the chapter that holds it (the last
// one that starts at or before it) and the time into that chapter. Without a chapter before the
// position it is 0 and the time into the book.
func bookmarkWhere(chapters []library.Chapter, posMs int64) (chapterNo int, clock string) {
	start := int64(0)
	for i, c := range chapters {
		if c.StartMs <= posMs && (chapterNo == 0 || c.StartMs >= start) {
			chapterNo, start = i+1, c.StartMs
		}
	}
	return chapterNo, clockText(posMs - start)
}

// bookmarkWhenText is "Chapter 2 · 1:00", or just the clock without a chapter.
func bookmarkWhenText(chapterNo int, clock string) string {
	if chapterNo <= 0 {
		return clock
	}
	return fmt.Sprintf(bookmarkChapterN, chapterNo) + " · " + clock
}

// bookmarkDayText says when a bookmark was made in day words: "Today" before 18:00 and "Tonight"
// from then, "Yesterday", the weekday within the last week, then "24 Oct" and "24 Oct 2025". The
// days are those of at's own time zone.
func bookmarkDayText(at, now time.Time) string {
	now = now.In(at.Location())
	day := func(t time.Time) time.Time { return time.Date(t.Year(), t.Month(), t.Day(), 12, 0, 0, 0, time.UTC) }
	days := int(day(now).Sub(day(at)).Hours() / 24)
	switch {
	case days <= 0:
		if at.Hour() >= 18 {
			return bookmarkTonight
		}
		return bookmarkToday
	case days == 1:
		return bookmarkYesterday
	case days < 7:
		return at.Weekday().String()
	case at.Year() == now.Year():
		return at.Format("2 Jan")
	}
	return at.Format("2 Jan 2006")
}

// bookmarkRow turns a stored bookmark into the row the tab draws.
func bookmarkRow(b progress.Bookmark, chapters []library.Chapter, now time.Time) components.BookmarkRowView {
	n, clock := bookmarkWhere(chapters, b.PositionMs)
	when := bookmarkWhenText(n, clock)
	v := components.BookmarkRowView{
		ID: b.ID, BookID: b.BookID, WhenText: when, PlayAtMs: b.PositionMs, Note: b.Note,
		DayText: bookmarkDayText(time.UnixMilli(b.CreatedAt).In(time.Local), now), NoNoteText: bookmarkNoNote,
		EditHref: bookmarkSheetHref(b.BookID, b.ID), PlayLabel: bookmarkPlayFrom,
	}
	if b.Note == "" {
		v.EditLabel = bookmarkAddNote + " · " + when
	} else {
		v.EditLabel = bookmarkEditNote + " · " + when
	}
	return v
}

// bookmarksPanel builds the Bookmarks tab for the signed-in person (the caller has checked they may
// listen to the book) and, when the address asks for one, the sheet: the note of one of their
// bookmarks, or the export choices. A bookmark that is not theirs on this book is
// errBookmarkNotFound.
func (s *server) bookmarksPanel(r *http.Request, d library.BookDetail) (pages.BookmarksView, templ.Component, error) {
	ctx := r.Context()
	q := r.URL.Query()
	u := currentUser(r)
	id := d.Book.ID
	csrf := s.csrf.TokenFrom(ctx)
	base := bookmarkTabHref(id)
	only := q.Get("only") == "notes"

	list, err := s.progress.Bookmarks(ctx, u.ID, id, only)
	if err != nil {
		return pages.BookmarksView{}, nil, err
	}
	all, notes, err := s.progress.BookmarkCounts(ctx, u.ID, id)
	if err != nil {
		return pages.BookmarksView{}, nil, err
	}
	now := bookmarkNow()
	v := pages.BookmarksView{
		Heading: bookmarkHeading, ExportLabel: bookmarkExport, ExportHref: base + "?sheet=export",
		ChipsLabel: bookmarkChipsLabel,
		Chips: []pages.BookmarkChip{
			{Label: fmt.Sprintf(bookmarkChipAll, all), Href: base, Current: !only},
			{Label: fmt.Sprintf(bookmarkChipNotes, notes), Href: base + "?only=notes", Current: only},
		},
		EmptyTitle: bookmarkEmptyTitle, EmptyBody: bookmarkEmptyBody,
	}
	if only {
		v.EmptyTitle, v.EmptyBody = bookmarkEmptyNotes, ""
	}
	for _, b := range list {
		v.Rows = append(v.Rows, bookmarkRow(b, d.Chapters, now))
	}
	// Without the player the button saves the person's stored place; no place, a hidden form the
	// player shows once it has a live position.
	if p, err := s.progress.Get(ctx, u.ID, id); err == nil {
		_, clock := bookmarkWhere(d.Chapters, p.PositionMs)
		v.Add = &pages.BookmarkAdd{
			Action: "/books/" + id + "/bookmarks", CSRF: csrf, PositionMs: strconv.FormatInt(p.PositionMs, 10),
			Label: fmt.Sprintf(bookmarkAddButton, clock), BookID: id,
		}
	} else if errors.Is(err, progress.ErrNotFound) {
		v.Add = &pages.BookmarkAdd{Action: "/books/" + id + "/bookmarks", CSRF: csrf, PositionMs: "0", BookID: id, Hidden: true}
	} else {
		return v, nil, err
	}
	if added := q.Get("added"); added != "" {
		switch b, err := s.progress.Bookmark(ctx, u.ID, added); {
		case err == nil && b.BookID == id:
			_, clock := bookmarkWhere(d.Chapters, b.PositionMs)
			v.Toast = &pages.BookmarkToast{Text: fmt.Sprintf(bookmarkToast, clock), LinkHref: bookmarkSheetHref(id, b.ID), LinkLabel: bookmarkAddNote}
		case err != nil && !errors.Is(err, progress.ErrNotFound):
			return v, nil, err
		}
	}

	switch q.Get("sheet") {
	case "bookmark":
		b, err := s.progress.Bookmark(ctx, u.ID, q.Get("bookmark"))
		if errors.Is(err, progress.ErrNotFound) || (err == nil && b.BookID != id) {
			return v, nil, errBookmarkNotFound
		}
		if err != nil {
			return v, nil, err
		}
		n, clock := bookmarkWhere(d.Chapters, b.PositionMs)
		sv := pages.BookmarkSheetView{
			CSRF: csrf, Title: bookmarkSheetTitle, When: bookmarkWhenText(n, clock), Note: b.Note,
			NoteLabel: bookmarkNoteLabel, SaveLabel: bookmarkSave, DeleteLabel: bookmarkDelete,
			NoteAction: "/bookmarks/" + b.ID + "/note", DeleteAction: "/bookmarks/" + b.ID + "/delete",
			CloseHref: base, CloseLabel: bookmarkClose, MaxRunes: progress.MaxNoteRunes,
			Styles: s.sheetStyles(pages.BookmarkStyles...),
		}
		if q.Get("error") == "note_too_long" {
			sv.Error = bookmarkErrTooLong
		}
		return v, pages.BookmarkSheet(sv), nil
	case "export":
		ev := pages.BookmarkExportView{
			Title: bookmarkExportTitle, CloseHref: base, CloseLabel: bookmarkClose,
			Styles: s.sheetStyles(pages.BookmarkStyles...),
			Links: []pages.BookmarkChip{
				{Label: bookmarkExportText, Href: base + "/export?format=txt"},
				{Label: bookmarkExportMD, Href: base + "/export?format=md"},
			},
		}
		return v, pages.BookmarkExportSheet(ev), nil
	}
	return v, nil, nil
}

// bookmarkSlug is the file name stem of an export: lower-case ASCII letters and digits, other runs
// become one "-", at most 60 characters, "book" when nothing is left.
func bookmarkSlug(title string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(title) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			if dash && b.Len() > 0 {
				b.WriteByte('-')
			}
			dash = false
			b.WriteRune(r)
		} else {
			dash = true
		}
	}
	out := b.String()
	if len(out) > 60 {
		out = strings.TrimRight(out[:60], "-")
	}
	if out == "" {
		return "book"
	}
	return out
}

// bookmarkGroupTitle is the heading of a run of bookmarks: "Chapter 2: The harbour", "Chapter 2"
// for a chapter without a title, "Book" before the first chapter or when the book has none.
func bookmarkGroupTitle(chapters []library.Chapter, n int) string {
	if n <= 0 {
		return bookmarkExportGroup
	}
	label := fmt.Sprintf(bookmarkChapterN, n)
	if t := strings.TrimSpace(chapters[n-1].Title); t != "" {
		return label + ": " + t
	}
	return label
}

// bookmarkExportBody writes the bookmarks (list is in position order) as plain text or Markdown,
// grouped by chapter in book order.
func bookmarkExportBody(title string, chapters []library.Chapter, list []progress.Bookmark, markdown bool) string {
	var b strings.Builder
	if markdown {
		b.WriteString("# " + title + "\n")
	} else {
		b.WriteString(title + "\n" + bookmarkExportHeader + "\n")
	}
	last := -1
	for _, bm := range list {
		n, clock := bookmarkWhere(chapters, bm.PositionMs)
		if n != last {
			last = n
			b.WriteString("\n")
			if markdown {
				b.WriteString("## " + bookmarkGroupTitle(chapters, n) + "\n\n")
			} else {
				b.WriteString(bookmarkGroupTitle(chapters, n) + "\n")
			}
		}
		lines := strings.Split(bm.Note, "\n")
		if bm.Note == "" {
			lines = []string{bookmarkNoNote}
			if markdown {
				lines = []string{"_" + bookmarkNoNote + "_"}
			}
		}
		if markdown {
			b.WriteString("- **" + clock + "** " + lines[0] + "\n")
			for _, l := range lines[1:] {
				b.WriteString("  " + l + "\n")
			}
			continue
		}
		b.WriteString("  " + clock + "  " + lines[0] + "\n")
		for _, l := range lines[1:] {
			b.WriteString("        " + l + "\n")
		}
	}
	return b.String()
}
