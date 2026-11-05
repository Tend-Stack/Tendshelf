package progress_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/progress"
)

const hlHexA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const hlHexB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// hlEdition gives a book an ebook file and an edition so marks can point at it.
func hlEdition(t *testing.T, st *db.Store, bookID, id string) {
	t.Helper()
	for _, q := range []string{
		`INSERT INTO ebook_files(id, origin, library_id, folder_id, path, book_id, ext, size, quick_hash, sha256, state, created_at, updated_at)
SELECT 'ef_` + id + `', 'library', library_id, folder_id, root_path || '/` + id + `.epub', id, 'epub', 10, '` + hlHexA + `', NULL, 'ready', 1, 1 FROM books WHERE id = '` + bookID + `'`,
		`INSERT INTO editions(id, ebook_file_id, source_sha256, recipe, doc_sha256, doc_bytes, format, chapter_count, word_count, char_count, created_at)
VALUES ('` + id + `', 'ef_` + id + `', '` + hlHexA + `', 'tendshelf-ebook-v1', '` + hlHexB + `', 5, 'epub', 1, 2, 3, 1)`,
	} {
		if _, err := st.Writer().Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
}

func hlBase(user, book string) progress.Highlight {
	return progress.Highlight{UserID: user, BookID: book, Chapter: 1, StartSID: "c1.p1.s0", StartOff: 0, EndSID: "c1.p1.s1", EndOff: 4, Quote: "Call me Ishmael."}
}

func TestHighlightAddAndList(t *testing.T) {
	svc, st, u, books, clk := progEnv(t)
	b := books[0].ID
	hlEdition(t, st, b, "ed_1")
	h := hlBase(u[0], b)
	h.EditionID, h.Note, h.Prefix, h.Suffix = "ed_1", "  good  ", "before ", " after"
	a, created, err := svc.AddHighlight(ctx, h)
	if err != nil || !created || !strings.HasPrefix(a.ID, "hl_") || a.Colour != "yellow" || a.Note != "good" || a.CreatedAt != clk.Ms() {
		t.Fatalf("add = %+v, %v, %v", a, created, err)
	}
	h2 := hlBase(u[0], b)
	h2.EditionID, h2.Chapter, h2.StartSID, h2.EndSID = "ed_1", 2, "c2.p1.s0", "c2.p1.s0"
	h2.Colour = "green"
	clk.Advance(time.Second)
	a2, _, err := svc.AddHighlight(ctx, h2)
	if err != nil {
		t.Fatal(err)
	}
	all, err := svc.Highlights(ctx, u[0], b)
	if err != nil || len(all) != 2 || all[0].ID != a.ID || all[1].ID != a2.ID {
		t.Fatalf("list = %+v, %v", all, err)
	}
	if all[1].Colour != "green" {
		t.Errorf("colour lost: %+v", all)
	}
	ch, err := svc.ChapterHighlights(ctx, u[0], "ed_1", 2)
	if err != nil || len(ch) != 1 || ch[0].ID != a2.ID {
		t.Fatalf("chapter 2 = %+v, %v", ch, err)
	}
	if ch, _ := svc.ChapterHighlights(ctx, u[0], "ed_1", 3); len(ch) != 0 {
		t.Errorf("chapter 3 = %+v", ch)
	}
	got, err := svc.Highlight(ctx, u[0], a.ID)
	if err != nil || got.Quote != h.Quote || got.Prefix != "before " || got.EditionID != "ed_1" || got.EndOff != 4 {
		t.Fatalf("get = %+v, %v", got, err)
	}
	note, colour := "", "pink"
	set, err := svc.SetHighlight(ctx, u[0], a.ID, &note, &colour)
	if err != nil || set.Note != "" || set.Colour != "pink" {
		t.Fatalf("set = %+v, %v", set, err)
	}
	set, err = svc.SetHighlight(ctx, u[0], a.ID, nil, nil)
	if err != nil || set.Colour != "pink" {
		t.Fatalf("set nothing = %+v, %v", set, err)
	}
	if _, err := svc.SetHighlight(ctx, u[0], a.ID, nil, new(string)); !errors.Is(err, progress.ErrInvalid) {
		t.Errorf("set empty colour = %v", err)
	}
	// An unknown edition is a missing thing, not a server error.
	bad := hlBase(u[0], b)
	bad.EditionID = "ed_nope"
	if _, _, err := svc.AddHighlight(ctx, bad); !errors.Is(err, progress.ErrNotFound) {
		t.Errorf("unknown edition = %v", err)
	}
}

func TestHighlightInvalid(t *testing.T) {
	svc, _, u, books, _ := progEnv(t)
	b := books[0].ID
	cases := []struct {
		name string
		edit func(h *progress.Highlight)
	}{
		{"bad start sid", func(h *progress.Highlight) { h.StartSID = "nope" }},
		{"bad end sid", func(h *progress.Highlight) { h.EndSID = "c1.p0.s0" }},
		{"cross chapter", func(h *progress.Highlight) { h.EndSID = "c2.p1.s0" }},
		{"chapter mismatch", func(h *progress.Highlight) { h.Chapter = 2 }},
		{"chapter zero", func(h *progress.Highlight) { h.Chapter = 0 }},
		{"end before start", func(h *progress.Highlight) { h.StartSID, h.EndSID = "c1.p2.s0", "c1.p1.s9" }},
		{"equal sid, start_off >= end_off", func(h *progress.Highlight) { h.EndSID, h.StartOff, h.EndOff = h.StartSID, 5, 5 }},
		{"empty quote", func(h *progress.Highlight) { h.Quote = "" }},
		{"4001 runes", func(h *progress.Highlight) { h.Quote = strings.Repeat("é", 4001) }},
		{"long prefix", func(h *progress.Highlight) { h.Prefix = strings.Repeat("p", 33) }},
		{"long suffix", func(h *progress.Highlight) { h.Suffix = strings.Repeat("p", 33) }},
		{"bad colour", func(h *progress.Highlight) { h.Colour = "red" }},
		{"long note", func(h *progress.Highlight) { h.Note = strings.Repeat("n", 2001) }},
		{"control char in note", func(h *progress.Highlight) { h.Note = "a\x07b" }},
		{"negative offset", func(h *progress.Highlight) { h.StartOff = -1 }},
		{"bad client id", func(h *progress.Highlight) { h.ClientID = "a b" }},
		{"no user", func(h *progress.Highlight) { h.UserID = "" }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := hlBase(u[0], b)
			c.edit(&h)
			if _, _, err := svc.AddHighlight(ctx, h); !errors.Is(err, progress.ErrInvalid) {
				t.Errorf("err = %v, want ErrInvalid", err)
			}
		})
	}
	if _, _, err := svc.AddHighlight(ctx, hlBase(u[0], b)); err != nil {
		t.Errorf("the base highlight must be valid: %v", err)
	}
	// 4000 runes of two bytes each is the longest allowed quote.
	long := hlBase(u[0], b)
	long.Quote = strings.Repeat("é", 4000)
	if _, _, err := svc.AddHighlight(ctx, long); err != nil {
		t.Errorf("4000 runes: %v", err)
	}
}

func TestHighlightLimit(t *testing.T) {
	svc, _, u, books, _ := progEnv(t)
	old := progress.MaxHighlightsPerBook
	progress.MaxHighlightsPerBook = 2
	t.Cleanup(func() { progress.MaxHighlightsPerBook = old })
	b := books[0].ID
	var first progress.Highlight
	for i := 0; i < 2; i++ {
		h, _, err := svc.AddHighlight(ctx, hlBase(u[0], b))
		if err != nil {
			t.Fatal(err)
		}
		first = h
	}
	if _, _, err := svc.AddHighlight(ctx, hlBase(u[0], b)); !errors.Is(err, progress.ErrLimit) {
		t.Fatalf("third = %v, want ErrLimit", err)
	}
	if _, _, err := svc.AddHighlight(ctx, hlBase(u[0], books[1].ID)); err != nil {
		t.Errorf("another book: %v", err)
	}
	if _, _, err := svc.AddHighlight(ctx, hlBase(u[1], b)); err != nil {
		t.Errorf("another person: %v", err)
	}
	if err := svc.DeleteHighlight(ctx, u[0], first.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := svc.AddHighlight(ctx, hlBase(u[0], b)); err != nil {
		t.Errorf("after a delete: %v", err)
	}
}

func TestHighlightPrivate(t *testing.T) {
	svc, _, u, books, _ := progEnv(t)
	b := books[0].ID
	a, _, err := svc.AddHighlight(ctx, hlBase(u[0], b))
	if err != nil {
		t.Fatal(err)
	}
	note := "x"
	if _, err := svc.Highlight(ctx, u[1], a.ID); !errors.Is(err, progress.ErrNotFound) {
		t.Errorf("Highlight = %v", err)
	}
	if l, err := svc.Highlights(ctx, u[1], b); err != nil || len(l) != 0 {
		t.Errorf("Highlights = %v, %v", l, err)
	}
	if _, err := svc.SetHighlight(ctx, u[1], a.ID, &note, nil); !errors.Is(err, progress.ErrNotFound) {
		t.Errorf("SetHighlight = %v", err)
	}
	if err := svc.DeleteHighlight(ctx, u[1], a.ID); !errors.Is(err, progress.ErrNotFound) {
		t.Errorf("DeleteHighlight = %v", err)
	}
	if got, err := svc.Highlight(ctx, u[0], a.ID); err != nil || got.Note != "" {
		t.Errorf("owner's highlight changed: %+v, %v", got, err)
	}
}

func TestHighlightDeleteClearsText(t *testing.T) {
	svc, st, u, books, clk := progEnv(t)
	h := hlBase(u[0], books[0].ID)
	h.Note, h.Prefix, h.Suffix = "private", "pre", "suf"
	a, _, err := svc.AddHighlight(ctx, h)
	if err != nil {
		t.Fatal(err)
	}
	clk.Advance(5000)
	if err := svc.DeleteHighlight(ctx, u[0], a.ID); err != nil {
		t.Fatal(err)
	}
	if err := svc.DeleteHighlight(ctx, u[0], a.ID); !errors.Is(err, progress.ErrNotFound) {
		t.Errorf("second delete = %v", err)
	}
	if _, err := svc.Highlight(ctx, u[0], a.ID); !errors.Is(err, progress.ErrNotFound) {
		t.Errorf("get after delete = %v", err)
	}
	if l, _ := svc.Highlights(ctx, u[0], books[0].ID); len(l) != 0 {
		t.Errorf("list after delete = %+v", l)
	}
	var quote, pre, suf, note string
	var del, upd int64
	if err := st.Reader().QueryRow(`SELECT quote, quote_prefix, quote_suffix, note, deleted_at, updated_at FROM highlights WHERE id = ?`, a.ID).
		Scan(&quote, &pre, &suf, &note, &del, &upd); err != nil {
		t.Fatal(err)
	}
	if quote+pre+suf+note != "" || del != clk.Ms() || upd != clk.Ms() {
		t.Errorf("tombstone = %q %q %q %q del %d upd %d", quote, pre, suf, note, del, upd)
	}
}

func TestHighlightClientID(t *testing.T) {
	svc, _, u, books, _ := progEnv(t)
	h := hlBase(u[0], books[0].ID)
	h.ClientID = "c-1"
	a, created, err := svc.AddHighlight(ctx, h)
	if err != nil || !created {
		t.Fatalf("first = %v, %v", created, err)
	}
	again, created, err := svc.AddHighlight(ctx, h)
	if err != nil || created || again.ID != a.ID {
		t.Fatalf("retry = %+v, %v, %v", again, created, err)
	}
	if l, _ := svc.Highlights(ctx, u[0], books[0].ID); len(l) != 1 {
		t.Errorf("retry wrote a row: %d", len(l))
	}
	other := hlBase(u[0], books[1].ID)
	other.ClientID = "c-1"
	if _, _, err := svc.AddHighlight(ctx, other); !errors.Is(err, progress.ErrClientIDTaken) {
		t.Errorf("other book = %v", err)
	}
	// Like bookmarks, an id already used by another person is refused.
	mine := hlBase(u[1], books[0].ID)
	mine.ClientID = "c-1"
	if _, _, err := svc.AddHighlight(ctx, mine); !errors.Is(err, progress.ErrClientIDTaken) {
		t.Errorf("other person = %v", err)
	}
}

func TestBookmarkTextPlace(t *testing.T) {
	svc, st, u, books, _ := progEnv(t)
	b := books[0].ID
	hlEdition(t, st, b, "ed_1")
	cases := []struct {
		name         string
		edition, sid string
		wantInvalid  bool
		clientID     string
	}{
		{name: "place", edition: "ed_1", sid: "c2.p3.s4"},
		{name: "with client id", edition: "ed_1", sid: "c1.p1.s0", clientID: "t-1"},
		{name: "sid without edition", sid: "c1.p1.s0", wantInvalid: true},
		{name: "edition without sid", edition: "ed_1", wantInvalid: true},
		{name: "bad sid", edition: "ed_1", sid: "c1.p1", wantInvalid: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bm, created, err := svc.AddTextBookmark(ctx, u[0], b, c.edition, c.sid, 4200, " n ", c.clientID)
			if c.wantInvalid {
				if !errors.Is(err, progress.ErrInvalid) {
					t.Fatalf("err = %v, want ErrInvalid", err)
				}
				return
			}
			if err != nil || !created || bm.EditionID != c.edition || bm.SID != c.sid || bm.PositionMs != 4200 || bm.Note != "n" {
				t.Fatalf("add = %+v, %v, %v", bm, created, err)
			}
			got, err := svc.Bookmark(ctx, u[0], bm.ID)
			if err != nil || got.EditionID != c.edition || got.SID != c.sid || got.ClientID != c.clientID {
				t.Fatalf("scan = %+v, %v", got, err)
			}
		})
	}
	// A retry with the client id returns the first one; an audio bookmark still has no edition or sid.
	a, created, err := svc.AddTextBookmark(ctx, u[0], b, "ed_1", "c1.p1.s0", 4200, "", "t-1")
	if err != nil || created || a.SID != "c1.p1.s0" {
		t.Errorf("retry = %+v, %v, %v", a, created, err)
	}
	au, err := svc.AddBookmark(ctx, u[0], b, 10, "")
	if err != nil || au.EditionID != "" || au.SID != "" {
		t.Errorf("audio bookmark = %+v, %v", au, err)
	}
	if _, _, err := svc.AddTextBookmark(ctx, u[0], b, "ed_nope", "c1.p1.s0", 0, "", ""); !errors.Is(err, progress.ErrNotFound) {
		t.Errorf("unknown edition = %v", err)
	}
	list, err := svc.Bookmarks(ctx, u[0], b, false)
	if err != nil || len(list) != 1 || list[0].SID != "" {
		t.Errorf("audio list = %d, %v", len(list), err) // the Bookmarks tab holds audio moments only
	}
	text, err := svc.TextBookmarks(ctx, u[0], b)
	if err != nil || len(text) != 2 {
		t.Errorf("text list = %d, %v", len(text), err)
	}
}
