package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/progress"
	"github.com/Tend-Stack/tendshelf/internal/users"
)

// bookmarkClock is the "now" of the bookmark tests: Sunday 1 November 2026, noon, in the server's zone.
var bookmarkClock = time.Date(2026, 11, 1, 12, 0, 0, 0, time.Local)

// bookmarkFx is a book with four chapters, three people and a place in the book for the owner.
type bookmarkFx struct {
	a                    *testApp
	owner, admin, member *http.Cookie
	ownerID, adminID     string
	memberID             string
	book                 library.Book
	svc                  *progress.Service
}

// bookmarkChapters are the chapters of the fixture: 0:00, 10:00, 30:00 (untitled) and 60:00.
func bookmarkChapters() []library.Chapter {
	return []library.Chapter{
		{StartMs: 0, EndMs: 600_000, Title: "Arrival", Source: library.ChapterFile},
		{StartMs: 600_000, EndMs: 1_800_000, Title: "The Harbour", Source: library.ChapterFile},
		{StartMs: 1_800_000, EndMs: 3_600_000, Source: library.ChapterFile},
		{StartMs: 3_600_000, EndMs: 7_200_000, Title: "Home", Source: library.ChapterFile},
	}
}

// bookmarkBook adds a two-hour book with the fixture chapters to the app.
func bookmarkBook(t *testing.T, a *testApp, title string) library.Book {
	t.Helper()
	f := browseLibrary(t, a, "Library of "+title)
	b := librarytest.AddBook(t, a.lib, f, librarytest.BookSpec{Title: title, Authors: []string{"Imogen Hale"}, DurationMs: 7_200_000, AddedAt: 5, Files: 1})
	err := a.lib.Update(context.Background(), func(tx *library.Tx) error { return tx.ReplaceChapters(b.ID, bookmarkChapters()) })
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// bookmarkEnv builds the fixture and fixes the day-word clock. The owner is 14:06 into chapter 2.
func bookmarkEnv(t *testing.T) *bookmarkFx {
	t.Helper()
	old := bookmarkNow
	bookmarkNow = func() time.Time { return bookmarkClock }
	t.Cleanup(func() { bookmarkNow = old })
	a := newTestApp(t)
	f := &bookmarkFx{a: a, svc: progress.New(a.st, nil)}
	f.owner, f.admin, f.member = a.as(t, users.RoleOwner), a.as(t, users.RoleAdmin), a.as(t, users.RoleMember)
	f.ownerID, f.adminID, f.memberID = sweepUserID(t, a, f.owner), sweepUserID(t, a, f.admin), sweepUserID(t, a, f.member)
	f.book = bookmarkBook(t, a, "Lantern Tide")
	_, err := f.svc.Save(context.Background(), progress.Update{
		UserID: f.ownerID, BookID: f.book.ID, PositionMs: 1_446_000, DurationMs: f.book.DurationMs,
		ClientUpdatedAt: time.Now().UnixMilli(), DeviceID: "device-0001",
	})
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// bookmarkAdd stores a bookmark made at the given time, straight through the service.
func bookmarkAdd(t *testing.T, f *bookmarkFx, userID, bookID string, pos int64, note string, at time.Time) progress.Bookmark {
	t.Helper()
	b, err := progress.New(f.a.st, func() time.Time { return at }).AddBookmark(context.Background(), userID, bookID, pos, note)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func bookmarkGet(f *bookmarkFx, ck *http.Cookie, path string) *httptest.ResponseRecorder {
	return browseGet(f.a, path, false, ck)
}

func bookmarkPath(f *bookmarkFx, suffix string) string {
	return "/books/" + f.book.ID + "/bookmarks" + suffix
}

// bookmarkStored reads the live bookmarks of a person on a book.
func bookmarkStored(t *testing.T, f *bookmarkFx, userID, bookID string) []progress.Bookmark {
	t.Helper()
	list, err := f.svc.Bookmarks(context.Background(), userID, bookID, false)
	if err != nil {
		t.Fatal(err)
	}
	return list
}

func bookmarkWant(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(body, w) {
			t.Errorf("the page lacks %q", w)
		}
	}
}

func bookmarkNotWant(t *testing.T, body string, not ...string) {
	t.Helper()
	for _, w := range not {
		if strings.Contains(body, w) {
			t.Errorf("the page has %q", w)
		}
	}
}

func TestBookmarkTab(t *testing.T) {
	f := bookmarkEnv(t)
	at := func(m time.Month, d, h int) time.Time { return time.Date(2026, m, d, h, 0, 0, 0, time.Local) }
	empty := bookmarkGet(f, f.owner, bookmarkPath(f, "")).Body.String()
	bookmarkWant(t, empty, "No bookmarks yet", "Tap the bookmark button while listening to save a moment.", "All · 0", "With notes · 0", "Bookmark this moment · 14:06")

	third := at(time.October, 29, 10)
	notes := []struct {
		pos  int64
		note string
		when time.Time
		text string
		day  string
	}{
		{660_000, "Harbour at dawn", at(time.November, 1, 9), "Chapter 2 · 1:00", "Today"},
		{100_000, "", at(time.October, 31, 20), "Chapter 1 · 1:40", "Yesterday"},
		{1_900_000, "Second lamp", third, "Chapter 3 · 1:40", third.Weekday().String()},
		{3_700_000, "Home at last", at(time.October, 24, 10), "Chapter 4 · 1:40", "24 Oct"},
		{1_000, "", at(time.October, 19, 10), "Chapter 1 · 0:01", "19 Oct"},
		{2_000, "Ink", at(time.October, 18, 10), "Chapter 1 · 0:02", "18 Oct"},
	}
	var ids []string
	for _, n := range notes {
		ids = append(ids, bookmarkAdd(t, f, f.ownerID, f.book.ID, n.pos, n.note, n.when).ID)
	}
	bookmarkAdd(t, f, f.adminID, f.book.ID, 5_000, "Sweepnote private", at(time.November, 1, 9))

	body := bookmarkGet(f, f.owner, bookmarkPath(f, "")).Body.String()
	bookmarkWant(t, body, "Bookmarks and notes", "All · 6", "With notes · 4", `aria-label="Show"`, `aria-current="page"`, "Export")
	last := -1
	for _, n := range notes {
		i := strings.Index(body, ">"+n.text+"<")
		if i < 0 || i < last {
			t.Errorf("%q is missing or out of order (newest first)", n.text)
		}
		last = i
		bookmarkWant(t, body, ">"+n.day+"<")
	}
	bookmarkWant(t, body, `data-ts-play-at="660000"`, `data-ts-book="`+f.book.ID+`"`, `class="ts-mark__jump"`, "Edit note · Chapter 2 · 1:00", "Add a note · Chapter 1 · 1:40")
	if n := strings.Count(body, "ts-mark__note is-empty"); n != 2 {
		t.Errorf("empty notes = %d, want 2", n)
	}
	if n := strings.Count(body, `class="ts-mark__jump" data-ts-play-at`); n != 6 || strings.Count(body, " hidden>Play from here<") != 6 {
		t.Errorf("hidden jump buttons = %d, want 6", n)
	}
	bookmarkNotWant(t, body, "Sweepnote")
	bookmarkWant(t, body, "Bookmark this moment · 14:06", `name="position_ms" value="1446000"`, `name="_csrf"`)
	if strings.Contains(body, "Highlights") {
		t.Error("the Highlights chip waits for R4")
	}

	only := bookmarkGet(f, f.owner, bookmarkPath(f, "?only=notes")).Body.String()
	if n := strings.Count(only, `class="ts-mark"`); n != 4 {
		t.Errorf("rows with notes = %d, want 4", n)
	}
	bookmarkNotWant(t, only, "No note")

	// Tabs: About, Chapters, Bookmarks, Details; a person without progress has no add button.
	page := bookmarkGet(f, f.admin, bookmarkPath(f, "")).Body.String()
	a, c, b, d := strings.Index(page, `id="book-tab-about"`), strings.Index(page, `id="book-tab-chapters"`), strings.Index(page, `id="book-tab-bookmarks"`), strings.Index(page, `id="book-tab-details"`)
	if !(0 < a && a < c && c < b && b < d) {
		t.Errorf("tab order = %d %d %d %d, want About, Chapters, Bookmarks, Details", a, c, b, d)
	}
	// The form is there for the player to show, but hidden and without a stored place or a label.
	bookmarkWant(t, page, `data-ts-bookmark-add="`+f.book.ID+`" hidden`, `<span data-ts-bookmark-add-label></span>`)
	bookmarkWant(t, page, "All · 1")

	// The toast follows an added bookmark of the person's own, and only that.
	toast := bookmarkGet(f, f.owner, bookmarkPath(f, "?added="+ids[0])).Body.String()
	bookmarkWant(t, toast, "Bookmarked at 1:00 · ", "Add a note", `role="status"`, "sheet=bookmark&amp;bookmark="+ids[0])
	if other := bookmarkGet(f, f.admin, bookmarkPath(f, "?added="+ids[0])).Body.String(); strings.Contains(other, "Bookmarked at 1:00") {
		t.Error("the toast names somebody else's bookmark")
	}
	if junk := bookmarkGet(f, f.owner, bookmarkPath(f, "?added=bm_nothing")).Body.String(); strings.Contains(junk, "Bookmarked at 1:00") {
		t.Error("the toast names an unknown bookmark")
	}

	// A tab swap by htmx still carries the panel it selects.
	rec := browseGet(f.a, bookmarkPath(f, ""), true, f.owner)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `id="book-panel"`) {
		t.Errorf("htmx tab = %d without #book-panel", rec.Code)
	}
	// The person who cannot listen has no tab and no panel.
	if rec := bookmarkGet(f, f.member, bookmarkPath(f, "")); rec.Code != http.StatusNotFound {
		t.Errorf("member tab = %d, want 404", rec.Code)
	}
}

func TestBookmarkAdd(t *testing.T) {
	f := bookmarkEnv(t)
	post := func(ck *http.Cookie, form url.Values, mod func(*http.Request)) *httptest.ResponseRecorder {
		form.Set("_csrf", f.a.tokenFor(t, ck))
		return f.a.do(http.MethodPost, "/books/"+f.book.ID+"/bookmarks", form, mod, ck)
	}
	rec := post(f.owner, url.Values{"position_ms": {"5000"}, "note": {" first "}}, nil)
	if rec.Code != http.StatusSeeOther || !strings.HasPrefix(location(rec), bookmarkPath(f, "?added=bm_")) {
		t.Fatalf("add = %d %q", rec.Code, location(rec))
	}
	got := bookmarkStored(t, f, f.ownerID, f.book.ID)
	if len(got) != 1 || got[0].PositionMs != 5000 || got[0].Note != "first" {
		t.Fatalf("stored = %+v", got)
	}
	if rec := post(f.owner, url.Values{"position_ms": {"7000"}, "next": {"/libraries"}}, nil); location(rec) != "/libraries" {
		t.Errorf("next = %q", location(rec))
	}
	if rec := post(f.owner, url.Values{"position_ms": {"7000"}, "next": {"//evil.example"}}, nil); !strings.HasPrefix(location(rec), bookmarkPath(f, "?added=")) {
		t.Errorf("unsafe next = %q", location(rec))
	}

	rec = post(f.owner, url.Values{"position_ms": {"3700000"}}, func(r *http.Request) { r.Header.Set("Accept", "application/json") })
	var out struct {
		ID         string `json:"id"`
		PositionMs int64  `json:"positionMs"`
		Label      string `json:"label"`
		NoteHref   string `json:"noteHref"`
	}
	if rec.Code != http.StatusCreated || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.PositionMs != 3_700_000 || out.Label != "1:40" || !strings.HasPrefix(out.ID, "bm_") {
		t.Errorf("json add = %d %s", rec.Code, rec.Body)
	}
	if want := "/books/" + f.book.ID + "/bookmarks?sheet=bookmark&bookmark=" + out.ID; out.NoteHref != want {
		t.Errorf("noteHref = %q, want %q", out.NoteHref, want)
	}

	post(f.owner, url.Values{"position_ms": {"99999999999"}}, nil)
	var top int64
	for _, b := range bookmarkStored(t, f, f.ownerID, f.book.ID) {
		top = max(top, b.PositionMs)
	}
	if top != f.book.DurationMs {
		t.Errorf("position not clamped: %d, want %d", top, f.book.DurationMs)
	}

	before := len(bookmarkStored(t, f, f.ownerID, f.book.ID))
	for _, bad := range []string{"", "abc", "-5", "1.5"} {
		if rec := post(f.owner, url.Values{"position_ms": {bad}}, nil); rec.Code != http.StatusBadRequest {
			t.Errorf("position %q = %d, want 400", bad, rec.Code)
		}
	}
	rec = post(f.owner, url.Values{"position_ms": {"1"}, "note": {strings.Repeat("x", 2001)}}, nil)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "Notes can be up to 2,000 characters.") {
		t.Errorf("long note = %d %q", rec.Code, rec.Body)
	}
	rec = post(f.owner, url.Values{"position_ms": {"-1"}}, hx)
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `role="alert"`) {
		t.Errorf("htmx refusal = %d %q, want a toast", rec.Code, rec.Body)
	}
	if n := len(bookmarkStored(t, f, f.ownerID, f.book.ID)); n != before {
		t.Errorf("a refused form stored %d bookmarks", n-before)
	}

	// The limit.
	old := progress.MaxBookmarksPerBook
	progress.MaxBookmarksPerBook = 1
	t.Cleanup(func() { progress.MaxBookmarksPerBook = old })
	if rec := post(f.admin, url.Values{"position_ms": {"1"}}, nil); rec.Code != http.StatusSeeOther {
		t.Fatalf("first admin add = %d", rec.Code)
	}
	rec = post(f.admin, url.Values{"position_ms": {"2"}}, nil)
	if rec.Code != http.StatusConflict || !strings.Contains(rec.Body.String(), "This book has 1,000 bookmarks, the most it can hold.") {
		t.Errorf("limit = %d %q", rec.Code, rec.Body)
	}

	// No CSRF token: refused, nothing stored.
	n := f.a.count(t, `SELECT count(*) FROM bookmarks`)
	if rec := f.a.do(http.MethodPost, "/books/"+f.book.ID+"/bookmarks", url.Values{"position_ms": {"1"}}, nil, f.owner); rec.Code != http.StatusForbidden {
		t.Errorf("no token = %d, want 403", rec.Code)
	}
	// A member cannot listen: 404 and nothing stored.
	if rec := post(f.member, url.Values{"position_ms": {"1"}}, nil); rec.Code != http.StatusNotFound {
		t.Errorf("member add = %d, want 404", rec.Code)
	}
	if got := f.a.count(t, `SELECT count(*) FROM bookmarks`); got != n {
		t.Errorf("bookmarks %d -> %d after a refused add", n, got)
	}
}

func TestBookmarkNoteAndDelete(t *testing.T) {
	f := bookmarkEnv(t)
	postTo := func(ck *http.Cookie, path string, form url.Values) *httptest.ResponseRecorder {
		if form == nil {
			form = url.Values{}
		}
		form.Set("_csrf", f.a.tokenFor(t, ck))
		return f.a.do(http.MethodPost, path, form, nil, ck)
	}
	note := func(id string) string {
		b, err := f.svc.Bookmark(context.Background(), f.ownerID, id)
		if err != nil {
			t.Fatal(err)
		}
		return b.Note
	}
	bm := bookmarkAdd(t, f, f.ownerID, f.book.ID, 660_000, "Draft <b>", bookmarkClock)
	sheet := bookmarkGet(f, f.owner, bookmarkPath(f, "?sheet=bookmark&bookmark="+bm.ID)).Body.String()
	bookmarkWant(t, sheet, `role="dialog"`, "Draft &lt;b&gt;", `maxlength="2000"`, "/bookmarks/"+bm.ID+"/note", "/bookmarks/"+bm.ID+"/delete", "Save", "Delete bookmark", "Chapter 2 · 1:00")

	rec := postTo(f.owner, "/bookmarks/"+bm.ID+"/note", url.Values{"note": {"  Better  "}})
	if rec.Code != http.StatusSeeOther || location(rec) != bookmarkPath(f, "") || note(bm.ID) != "Better" {
		t.Errorf("save = %d %q note %q", rec.Code, location(rec), note(bm.ID))
	}
	postTo(f.owner, "/bookmarks/"+bm.ID+"/note", url.Values{"note": {""}})
	if note(bm.ID) != "" {
		t.Error("an empty note does not clear")
	}
	postTo(f.owner, "/bookmarks/"+bm.ID+"/note", url.Values{"note": {"Keep"}})

	rec = postTo(f.owner, "/bookmarks/"+bm.ID+"/note", url.Values{"note": {strings.Repeat("é", 2001)}})
	loc := location(rec)
	if rec.Code != http.StatusSeeOther || !strings.Contains(loc, "error=note_too_long") || !strings.Contains(loc, "sheet=bookmark&bookmark="+bm.ID) || note(bm.ID) != "Keep" {
		t.Fatalf("long note = %d %q note %q", rec.Code, loc, note(bm.ID))
	}
	bookmarkWant(t, bookmarkGet(f, f.owner, loc).Body.String(), "Notes can be up to 2,000 characters.", `aria-invalid="true"`)
	if n := len([]rune(strings.Repeat("é", 2000))); n != progress.MaxNoteRunes {
		t.Fatalf("limit %d", n)
	}
	if rec := postTo(f.owner, "/bookmarks/"+bm.ID+"/note", url.Values{"note": {strings.Repeat("é", 2000)}}); location(rec) != bookmarkPath(f, "") {
		t.Errorf("2000 runes = %q, want the tab", location(rec))
	}

	// Somebody else's bookmark and one on a book the person cannot listen to: 404, nothing changes.
	for _, ck := range []*http.Cookie{f.admin, f.member} {
		for _, p := range []string{"/note", "/delete"} {
			if rec := postTo(ck, "/bookmarks/"+bm.ID+p, url.Values{"note": {"Stolen"}}); rec.Code != http.StatusNotFound {
				t.Errorf("other person %s = %d, want 404", p, rec.Code)
			}
		}
		if rec := bookmarkGet(f, ck, bookmarkPath(f, "?sheet=bookmark&bookmark="+bm.ID)); rec.Code != http.StatusNotFound {
			t.Errorf("other person sheet = %d, want 404", rec.Code)
		}
	}
	mine := bookmarkAdd(t, f, f.memberID, f.book.ID, 1000, "Member note", bookmarkClock)
	for _, p := range []string{"/note", "/delete"} {
		if rec := postTo(f.member, "/bookmarks/"+mine.ID+p, url.Values{"note": {"x"}}); rec.Code != http.StatusNotFound {
			t.Errorf("member own bookmark on a refused book %s = %d, want 404", p, rec.Code)
		}
	}
	if got := bookmarkStored(t, f, f.memberID, f.book.ID); len(got) != 1 || got[0].Note != "Member note" {
		t.Errorf("a refused write changed the member's bookmark: %+v", got)
	}
	if note(bm.ID) != strings.Repeat("é", 2000) {
		t.Error("another person's write changed the note")
	}

	// A bookmark of another book is not on this book's tab.
	other := bookmarkBook(t, f.a, "Second Tide")
	ob := bookmarkAdd(t, f, f.ownerID, other.ID, 1000, "", bookmarkClock)
	if rec := bookmarkGet(f, f.owner, bookmarkPath(f, "?sheet=bookmark&bookmark="+ob.ID)); rec.Code != http.StatusNotFound {
		t.Errorf("sheet of another book's bookmark = %d, want 404", rec.Code)
	}
	if rec := bookmarkGet(f, f.owner, bookmarkPath(f, "?sheet=bookmark&bookmark=bm_unknown")); rec.Code != http.StatusNotFound {
		t.Errorf("sheet of an unknown bookmark = %d, want 404", rec.Code)
	}

	// Delete.
	if rec := postTo(f.owner, "/bookmarks/"+bm.ID+"/delete", nil); rec.Code != http.StatusSeeOther || location(rec) != bookmarkPath(f, "") {
		t.Errorf("delete = %d %q", rec.Code, location(rec))
	}
	if body := bookmarkGet(f, f.owner, bookmarkPath(f, "")).Body.String(); strings.Contains(body, "Chapter 2 · 1:00") {
		t.Error("a deleted bookmark is still listed")
	}
	if rec := postTo(f.owner, "/bookmarks/"+bm.ID+"/delete", nil); rec.Code != http.StatusNotFound {
		t.Errorf("second delete = %d, want 404", rec.Code)
	}
	if rec := bookmarkGet(f, f.owner, bookmarkPath(f, "?sheet=bookmark&bookmark="+bm.ID)); rec.Code != http.StatusNotFound {
		t.Errorf("sheet of a deleted bookmark = %d, want 404", rec.Code)
	}
	// No CSRF: refused.
	if rec := f.a.do(http.MethodPost, "/bookmarks/"+ob.ID+"/delete", url.Values{}, nil, f.owner); rec.Code != http.StatusForbidden {
		t.Errorf("delete without a token = %d, want 403", rec.Code)
	}
}

func TestBookmarkExport(t *testing.T) {
	f := bookmarkEnv(t)
	bookmarkAdd(t, f, f.ownerID, f.book.ID, 660_000, "Lovely\nharbour", bookmarkClock.Add(-4*time.Hour))
	bookmarkAdd(t, f, f.ownerID, f.book.ID, 100_000, "", bookmarkClock.Add(-3*time.Hour))
	bookmarkAdd(t, f, f.ownerID, f.book.ID, 2_000_000, "Second lamp", bookmarkClock.Add(-2*time.Hour))
	bookmarkAdd(t, f, f.ownerID, f.book.ID, 700_000, "", bookmarkClock.Add(-1*time.Hour))
	bookmarkAdd(t, f, f.adminID, f.book.ID, 50_000, "Sweepnote private", bookmarkClock)

	txt := "Lantern Tide\nBookmarks and notes\n\nChapter 1: Arrival\n  1:40  No note\n\nChapter 2: The Harbour\n  1:00  Lovely\n        harbour\n  1:40  No note\n\nChapter 3\n  3:20  Second lamp\n"
	md := "# Lantern Tide\n\n## Chapter 1: Arrival\n\n- **1:40** _No note_\n\n## Chapter 2: The Harbour\n\n- **1:00** Lovely\n  harbour\n- **1:40** _No note_\n\n## Chapter 3\n\n- **3:20** Second lamp\n"
	for _, c := range []struct{ format, ctype, want string }{
		{"txt", "text/plain; charset=utf-8", txt},
		{"md", "text/markdown; charset=utf-8", md},
	} {
		rec := bookmarkGet(f, f.owner, bookmarkPath(f, "/export?format="+c.format))
		if rec.Code != http.StatusOK || rec.Body.String() != c.want {
			t.Errorf("%s export = %d\n%q\nwant\n%q", c.format, rec.Code, rec.Body, c.want)
		}
		h := rec.Header()
		if h.Get("Content-Type") != c.ctype || h.Get("Cache-Control") != "no-store" ||
			h.Get("Content-Disposition") != `attachment; filename="lantern-tide-bookmarks.`+c.format+`"` {
			t.Errorf("%s export headers = %v", c.format, h)
		}
	}
	// Only the person's own bookmarks.
	if body := bookmarkGet(f, f.admin, bookmarkPath(f, "/export?format=txt")).Body.String(); !strings.Contains(body, "Sweepnote private") || strings.Contains(body, "Second lamp") {
		t.Errorf("admin export = %q", body)
	}
	for _, bad := range []string{"", "?format=pdf", "?format=TXT"} {
		if rec := bookmarkGet(f, f.owner, bookmarkPath(f, "/export"+bad)); rec.Code != http.StatusBadRequest {
			t.Errorf("export%s = %d, want 400", bad, rec.Code)
		}
	}
	if rec := bookmarkGet(f, f.member, bookmarkPath(f, "/export?format=txt")); rec.Code != http.StatusNotFound {
		t.Errorf("member export = %d, want 404", rec.Code)
	}
	// A title with no ASCII letters in places, and no bookmarks: the heading lines only.
	b2 := bookmarkBook(t, f.a, "Élan: Vital — Café")
	rec := bookmarkGet(f, f.owner, "/books/"+b2.ID+"/bookmarks/export?format=md")
	if rec.Body.String() != "# Élan: Vital — Café\n" || rec.Header().Get("Content-Disposition") != `attachment; filename="lan-vital-caf-bookmarks.md"` {
		t.Errorf("empty export = %q %q", rec.Body, rec.Header().Get("Content-Disposition"))
	}
	if body := bookmarkGet(f, f.owner, bookmarkPath(f, "?sheet=export")).Body.String(); !strings.Contains(body, "format=txt") || !strings.Contains(body, "format=md") || !strings.Contains(body, "Plain text") || !strings.Contains(body, "Markdown") {
		t.Error("the export sheet lacks the two downloads")
	}
}

func TestBookmarkDayText(t *testing.T) {
	at := func(y int, m time.Month, d, h, min int) time.Time {
		return time.Date(y, m, d, h, min, 0, 0, time.Local)
	}
	now := bookmarkClock // Sunday 1 November 2026, noon
	for _, c := range []struct {
		name string
		at   time.Time
		want string
	}{
		{"before six", at(2026, 11, 1, 17, 59), "Today"},
		{"from six", at(2026, 11, 1, 18, 0), "Tonight"},
		{"morning", at(2026, 11, 1, 6, 0), "Today"},
		{"yesterday", at(2026, 10, 31, 23, 0), "Yesterday"},
		{"two days", at(2026, 10, 30, 8, 0), "Friday"},
		{"three days", at(2026, 10, 29, 8, 0), "Thursday"},
		{"six days", at(2026, 10, 26, 8, 0), "Monday"},
		{"a week", at(2026, 10, 25, 8, 0), "25 Oct"},
		{"ten days", at(2026, 10, 22, 8, 0), "22 Oct"},
		{"this year", at(2026, 1, 3, 8, 0), "3 Jan"},
		{"last year", at(2025, 12, 24, 8, 0), "24 Dec 2025"},
	} {
		if got := bookmarkDayText(c.at, now); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestBookmarkWhere(t *testing.T) {
	late := []library.Chapter{{StartMs: 5_000}, {StartMs: 65_000}}
	for _, c := range []struct {
		name     string
		chapters []library.Chapter
		pos      int64
		n        int
		clock    string
	}{
		{"first chapter start", bookmarkChapters(), 0, 1, "0:00"},
		{"inside", bookmarkChapters(), 1_446_000, 2, "14:06"},
		{"on a boundary", bookmarkChapters(), 600_000, 2, "0:00"},
		{"past an hour into the book", bookmarkChapters(), 7_000_000, 4, "56:40"},
		{"before the first chapter", late, 1_000, 0, "0:01"},
		{"in the second", late, 66_000, 2, "0:01"},
		{"no chapters", nil, 3_725_000, 0, "1:02:05"},
	} {
		n, clock := bookmarkWhere(c.chapters, c.pos)
		if n != c.n || clock != c.clock {
			t.Errorf("%s: %d %q, want %d %q", c.name, n, clock, c.n, c.clock)
		}
	}
	if got := bookmarkWhenText(2, "1:00"); got != "Chapter 2 · 1:00" {
		t.Errorf("when = %q", got)
	}
	if got := bookmarkWhenText(0, "1:00"); got != "1:00" {
		t.Errorf("when = %q", got)
	}
	for in, want := range map[string]string{"Lantern Tide": "lantern-tide", "  ": "book", "Élan — Vital!": "lan-vital", strings.Repeat("ab ", 40): strings.TrimRight(strings.Repeat("ab-", 40)[:60], "-")} {
		if got := bookmarkSlug(in); got != want {
			t.Errorf("slug(%q) = %q, want %q", in, got, want)
		}
	}
}
