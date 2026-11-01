package progress_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/progress"
)

func TestBookmarkLifecycle(t *testing.T) {
	svc, st, u, books, clk := progEnv(t)
	b := books[0].ID
	a1, err := svc.AddBookmark(ctx, u[0], b, 1000, "")
	if err != nil || !strings.HasPrefix(a1.ID, "bm_") || a1.CreatedAt != clk.Ms() {
		t.Fatalf("add = %+v, %v", a1, err)
	}
	clk.Advance(time.Second)
	a2, err := svc.AddBookmark(ctx, u[0], b, 2000, "  a thought  ")
	if err != nil || a2.Note != "a thought" {
		t.Fatalf("add with note = %+v, %v", a2, err)
	}
	clk.Advance(time.Second)
	a3, _ := svc.AddBookmark(ctx, u[0], b, 3000, "")

	list, err := svc.Bookmarks(ctx, u[0], b, false)
	if err != nil || len(list) != 3 || list[0].ID != a3.ID || list[2].ID != a1.ID {
		t.Fatalf("list = %+v, %v", list, err)
	}
	notes, _ := svc.Bookmarks(ctx, u[0], b, true)
	if len(notes) != 1 || notes[0].ID != a2.ID {
		t.Fatalf("with notes = %+v", notes)
	}
	if all, withNotes, err := svc.BookmarkCounts(ctx, u[0], b); err != nil || all != 3 || withNotes != 1 {
		t.Fatalf("counts = %d, %d, %v", all, withNotes, err)
	}
	if all, withNotes, err := svc.BookmarkCounts(ctx, u[0], books[1].ID); err != nil || all != 0 || withNotes != 0 {
		t.Fatalf("empty counts = %d, %d, %v", all, withNotes, err)
	}

	// Another person sees nothing and changes nothing.
	if other, _ := svc.Bookmarks(ctx, u[1], b, false); len(other) != 0 {
		t.Fatalf("B lists A's bookmarks: %+v", other)
	}
	if _, err := svc.Bookmark(ctx, u[1], a2.ID); !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("B reads A's bookmark: %v", err)
	}
	if _, err := svc.SetBookmarkNote(ctx, u[1], a2.ID, "mine now"); !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("B notes A's bookmark: %v", err)
	}
	if err := svc.DeleteBookmark(ctx, u[1], a2.ID); !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("B deletes A's bookmark: %v", err)
	}
	if got, _ := svc.Bookmark(ctx, u[0], a2.ID); got.Note != "a thought" {
		t.Fatalf("A's bookmark changed: %+v", got)
	}

	clk.Advance(time.Second)
	set, err := svc.SetBookmarkNote(ctx, u[0], a1.ID, "now with words")
	if err != nil || set.Note != "now with words" || set.UpdatedAt != clk.Ms() || set.CreatedAt != a1.CreatedAt {
		t.Fatalf("set = %+v, %v", set, err)
	}
	if set, err = svc.SetBookmarkNote(ctx, u[0], a1.ID, ""); err != nil || set.Note != "" {
		t.Fatalf("clear = %+v, %v", set, err)
	}

	clk.Advance(time.Second)
	if err := svc.DeleteBookmark(ctx, u[0], a2.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Bookmark(ctx, u[0], a2.ID); !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("deleted bookmark readable: %v", err)
	}
	if list, _ := svc.Bookmarks(ctx, u[0], b, false); len(list) != 2 {
		t.Fatalf("list after delete = %+v", list)
	}
	if all, withNotes, _ := svc.BookmarkCounts(ctx, u[0], b); all != 2 || withNotes != 0 {
		t.Fatalf("counts after delete = %d, %d", all, withNotes)
	}
	var note string
	var del, upd, pos int64
	if err := st.Reader().QueryRow(`SELECT note, deleted_at, updated_at, position_ms FROM bookmarks WHERE id = ?`, a2.ID).Scan(&note, &del, &upd, &pos); err != nil {
		t.Fatalf("tombstone gone: %v", err)
	}
	if note != "" || del != clk.Ms() || upd != clk.Ms() || pos != 2000 {
		t.Fatalf("tombstone = %q %d %d %d", note, del, upd, pos)
	}
	if err := svc.DeleteBookmark(ctx, u[0], a2.ID); !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := svc.SetBookmarkNote(ctx, u[0], a2.ID, "x"); !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("note on a tombstone: %v", err)
	}

	// Validation and unknown parents.
	if _, err := svc.AddBookmark(ctx, u[0], b, -1, ""); !errors.Is(err, progress.ErrInvalid) {
		t.Fatalf("negative position: %v", err)
	}
	if _, err := svc.AddBookmark(ctx, u[0], "bk_nope", 1, ""); !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("unknown book: %v", err)
	}
}

func TestBookmarkLimit(t *testing.T) {
	svc, _, u, books, _ := progEnv(t)
	old := progress.MaxBookmarksPerBook
	progress.MaxBookmarksPerBook = 2
	t.Cleanup(func() { progress.MaxBookmarksPerBook = old })
	b := books[0].ID
	var first progress.Bookmark
	for i := 0; i < 2; i++ {
		bm, err := svc.AddBookmark(ctx, u[0], b, int64(i), "")
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			first = bm
		}
	}
	if _, err := svc.AddBookmark(ctx, u[0], b, 5, ""); !errors.Is(err, progress.ErrLimit) {
		t.Fatalf("third: %v", err)
	}
	// The limit is per person and per book, and deleting frees a place.
	if _, err := svc.AddBookmark(ctx, u[1], b, 5, ""); err != nil {
		t.Fatalf("other person: %v", err)
	}
	if _, err := svc.AddBookmark(ctx, u[0], books[1].ID, 5, ""); err != nil {
		t.Fatalf("other book: %v", err)
	}
	if err := svc.DeleteBookmark(ctx, u[0], first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.AddBookmark(ctx, u[0], b, 6, ""); err != nil {
		t.Fatalf("after delete: %v", err)
	}
}

func TestBookmarkNoteRules(t *testing.T) {
	svc, _, u, books, _ := progEnv(t)
	cases := []struct {
		name, in, want string
		invalid        bool
	}{
		{"plain", "hello", "hello", false},
		{"crlf kept as lf", "a\r\nb", "a\nb", false},
		{"tab and newline fine", "a\tb\nc", "a\tb\nc", false},
		{"exactly 2000 runes", strings.Repeat("é", 2000), strings.Repeat("é", 2000), false},
		{"2001 runes", strings.Repeat("é", 2001), "", true},
		{"control char", "a\x01b", "", true},
		{"escape", "a\x1bb", "", true},
		{"invalid utf8", "a\xffb", "", true},
	}
	for _, c := range cases {
		bm, err := svc.AddBookmark(ctx, u[0], books[2].ID, 1, c.in)
		if c.invalid {
			var ie *progress.InvalidError
			if !errors.As(err, &ie) || ie.Field != "note" {
				t.Errorf("%s: err = %v", c.name, err)
			}
			continue
		}
		if err != nil || bm.Note != c.want {
			t.Errorf("%s: %q, %v", c.name, bm.Note, err)
		}
	}
}
