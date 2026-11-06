package progress_test

import (
	"errors"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/progress"
)

func clipI64(v int64) *int64   { return &v }
func clipStr(v string) *string { return &v }

func TestClipUpdate(t *testing.T) {
	svc, _, u, books, clk := progEnv(t)
	b := books[0].ID
	bm, err := svc.AddBookmark(ctx, u[0], b, 1000, "keep me")
	if err != nil || bm.Kind != "bookmark" || bm.EndMs != 0 {
		t.Fatalf("add = %+v, %v", bm, err)
	}
	clk.Advance(time.Second)
	got, err := svc.UpdateBookmark(ctx, u[0], bm.ID, progress.BookmarkPatch{EndMs: clipI64(5000), Kind: clipStr("quote")})
	if err != nil || got.EndMs != 5000 || got.Kind != "quote" || got.Note != "keep me" || got.UpdatedAt != clk.Ms() {
		t.Fatalf("clip = %+v, %v", got, err)
	}
	// The note alone leaves the end and kind as they are.
	clk.Advance(time.Second)
	got, err = svc.SetBookmarkNote(ctx, u[0], bm.ID, "new")
	if err != nil || got.EndMs != 5000 || got.Kind != "quote" || got.Note != "new" || got.UpdatedAt != clk.Ms() {
		t.Fatalf("note = %+v, %v", got, err)
	}
	// 0 clears the end.
	clk.Advance(time.Second)
	got, err = svc.UpdateBookmark(ctx, u[0], bm.ID, progress.BookmarkPatch{EndMs: clipI64(0)})
	if err != nil || got.EndMs != 0 || got.UpdatedAt != clk.Ms() {
		t.Fatalf("clear = %+v, %v", got, err)
	}
	// The sync feed carries the change.
	feed, _, err := svc.BookmarksChanged(ctx, u[0], b, clk.Ms()-1, "", 0)
	if err != nil || len(feed) != 1 || feed[0].ID != bm.ID {
		t.Fatalf("feed = %+v, %v", feed, err)
	}
}

func TestClipInvalid(t *testing.T) {
	svc, _, u, books, _ := progEnv(t)
	b := books[0].ID
	bm, _ := svc.AddBookmark(ctx, u[0], b, 1000, "")
	for _, c := range []struct {
		name  string
		patch progress.BookmarkPatch
		field string
	}{
		{"end at start", progress.BookmarkPatch{EndMs: clipI64(1000)}, "end_ms"},
		{"end before start", progress.BookmarkPatch{EndMs: clipI64(10)}, "end_ms"},
		{"negative end", progress.BookmarkPatch{EndMs: clipI64(-1)}, "end_ms"},
		{"unknown kind", progress.BookmarkPatch{Kind: clipStr("loud")}, "kind"},
		{"empty kind", progress.BookmarkPatch{Kind: clipStr("")}, "kind"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, err := svc.UpdateBookmark(ctx, u[0], bm.ID, c.patch)
			var inv *progress.InvalidError
			if !errors.As(err, &inv) || inv.Field != c.field {
				t.Fatalf("err = %v, want invalid %s", err, c.field)
			}
		})
	}
	if got, _ := svc.Bookmark(ctx, u[0], bm.ID); got.EndMs != 0 || got.Kind != "bookmark" {
		t.Errorf("a refused change was stored: %+v", got)
	}
}

func TestClipTextBookmarkRefusesEnd(t *testing.T) {
	svc, st, u, books, _ := progEnv(t)
	hlEdition(t, st, books[0].ID, "ed_1")
	tb, _, err := svc.AddTextBookmark(ctx, u[0], books[0].ID, "ed_1", "c1.p1.s0", 4200, "", "")
	if err != nil {
		t.Fatal(err)
	}
	_, err = svc.UpdateBookmark(ctx, u[0], tb.ID, progress.BookmarkPatch{EndMs: clipI64(9000)})
	var inv *progress.InvalidError
	if !errors.As(err, &inv) || inv.Field != "end_ms" {
		t.Fatalf("err = %v", err)
	}
	// A kind alone is fine on a text bookmark.
	if got, err := svc.UpdateBookmark(ctx, u[0], tb.ID, progress.BookmarkPatch{Kind: clipStr("favourite")}); err != nil || got.Kind != "favourite" {
		t.Fatalf("kind = %+v, %v", got, err)
	}
}

func TestClipOtherPersonAndTombstone(t *testing.T) {
	svc, _, u, books, clk := progEnv(t)
	b := books[0].ID
	bm, _ := svc.AddBookmark(ctx, u[0], b, 1000, "")
	if _, err := svc.UpdateBookmark(ctx, u[1], bm.ID, progress.BookmarkPatch{Kind: clipStr("quote")}); !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("B updates A's bookmark: %v", err)
	}
	if _, err := svc.UpdateBookmark(ctx, u[0], bm.ID, progress.BookmarkPatch{EndMs: clipI64(2000), Kind: clipStr("revisit")}); err != nil {
		t.Fatal(err)
	}
	clk.Advance(time.Second)
	if err := svc.DeleteBookmark(ctx, u[0], bm.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UpdateBookmark(ctx, u[0], bm.ID, progress.BookmarkPatch{Kind: clipStr("quote")}); !errors.Is(err, progress.ErrNotFound) {
		t.Fatalf("update of a tombstone: %v", err)
	}
	feed, _, err := svc.BookmarksChanged(ctx, u[0], b, 0, "", 0)
	if err != nil || len(feed) != 1 || feed[0].DeletedAt == 0 || feed[0].EndMs != 2000 || feed[0].Kind != "revisit" {
		t.Fatalf("tombstone = %+v, %v", feed, err)
	}
}
