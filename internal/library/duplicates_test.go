package library_test

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
)

// dupGroupTitles names each group as "tier:title+title" with the oldest copy first.
func dupGroupTitles(gs []library.DuplicateGroup) []string {
	var out []string
	for _, g := range gs {
		var ts []string
		for _, b := range g.Books {
			ts = append(ts, b.Title)
		}
		out = append(out, g.Tier+":"+strings.Join(ts, "+"))
	}
	return out
}

func dupDetailed(t *testing.T, r *library.Repo, f library.Folder, title, author, narrator string, dur int64) library.Book {
	t.Helper()
	b := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: title, Authors: []string{author}, Narrators: []string{narrator}, DurationMs: dur})
	return b
}

func TestDupTiersSameAudio(t *testing.T) {
	r, st, f := dupRepo(t)
	one := dupAdd(t, r, f, "One", dupHour)
	two := dupAdd(t, r, f, "One (copy)", dupHour)
	three := dupAdd(t, r, f, "Different", dupHour)
	// The same files in another order and under other names still match; a different size does not.
	dupFiles(t, st, one.ID, "100:a", "200:b", "300:c")
	dupFiles(t, st, two.ID, "300:c", "100:a", "200:b")
	dupFiles(t, st, three.ID, "100:a", "200:b", "301:c")
	gs, err := r.Duplicates(ctx, allScope)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := dupGroupTitles(gs), []string{"same_audio:One+One (copy)"}; !reflect.DeepEqual(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
	}
	if g := gs[0]; g.Books[0].Files != 3 || g.Books[0].Bytes != 600 {
		t.Errorf("first copy has %d files, %d bytes", g.Books[0].Files, g.Books[0].Bytes)
	}
}

func TestDupTiersSameDetails(t *testing.T) {
	r, _, f := dupRepo(t)
	dupDetailed(t, r, f, "The Long Road", "Ann Writer", "Nat Voice", 10*dupHour)
	dupDetailed(t, r, f, "the long road!", "ANN writer", "nat voice", 10*dupHour+30_000)  // within 60 s
	dupDetailed(t, r, f, "The Long Road", "Ann Writer", "Nat Voice", 10*dupHour+9*60_000) // 9 min on 10 h: within 2 %
	dupDetailed(t, r, f, "The Long Road", "Ann Writer", "Nat Voice", 11*dupHour)          // another edition: 10 %
	dupDetailed(t, r, f, "The Long Road", "Ann Writer", "Other Voice", 10*dupHour)        // another narrator
	gs, err := r.Duplicates(ctx, allScope)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"same_details:The Long Road+the long road!+The Long Road"}
	if got := dupGroupTitles(gs); !reflect.DeepEqual(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
	}
}

func TestDupMissingDetailsAreNotMatched(t *testing.T) {
	r, _, f := dupRepo(t)
	// No narrator, no author, no length: nothing is guessed.
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Same", Authors: []string{"A"}, DurationMs: dupHour})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Same", Authors: []string{"A"}, DurationMs: dupHour})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Zero", Authors: []string{"A"}, Narrators: []string{"N"}})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Zero", Authors: []string{"A"}, Narrators: []string{"N"}})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Anon", Narrators: []string{"N"}, DurationMs: dupHour})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Anon", Narrators: []string{"N"}, DurationMs: dupHour})
	gs, err := r.Duplicates(ctx, allScope)
	if err != nil || len(gs) != 0 {
		t.Errorf("groups = %v, %v; want none", dupGroupTitles(gs), err)
	}
}

func TestDupSameAudioPairIsNotAlsoSameDetails(t *testing.T) {
	r, st, f := dupRepo(t)
	a := dupDetailed(t, r, f, "Twin", "A", "N", dupHour)
	b := dupDetailed(t, r, f, "Twin", "A", "N", dupHour)
	dupFiles(t, st, a.ID, "100:a")
	dupFiles(t, st, b.ID, "100:a")
	gs, _ := r.Duplicates(ctx, allScope)
	if got, want := dupGroupTitles(gs), []string{"same_audio:Twin+Twin"}; !reflect.DeepEqual(got, want) {
		t.Errorf("groups = %v, want %v", got, want)
	}
}

func TestDupNotSameHidesThePair(t *testing.T) {
	r, _, f := dupRepo(t)
	a := dupDetailed(t, r, f, "Twin", "A", "N", dupHour)
	b := dupDetailed(t, r, f, "Twin", "A", "N", dupHour)
	c := dupDetailed(t, r, f, "Twin", "A", "N", dupHour)
	if err := r.NotDuplicates(ctx, b.ID, a.ID, dupUser); err != nil { // given the other way round: stored in order
		t.Fatal(err)
	}
	gs, _ := r.Duplicates(ctx, allScope)
	if got, want := dupGroupTitles(gs), []string{"same_details:Twin+Twin"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
	if ids := []string{gs[0].Books[0].ID, gs[0].Books[1].ID}; ids[0] != a.ID || ids[1] != c.ID {
		t.Errorf("group = %v, want the oldest and the third copy", ids)
	}
	if err := r.NotDuplicates(ctx, a.ID, "bk_nope", dupUser); err == nil {
		t.Error("a pair with an unknown book was accepted")
	}
	if err := r.NotDuplicates(ctx, a.ID, a.ID, dupUser); err == nil {
		t.Error("a book against itself was accepted")
	}
}

func TestDupScopeAndLibraries(t *testing.T) {
	r, st, f := dupRepo(t)
	_, other := librarytest.NewLibrary(t, r, "Other", "/other")
	a := dupAdd(t, r, f, "Here", dupHour)
	b := dupAdd(t, r, f, "Here too", dupHour)
	far := dupAdd(t, r, other, "Elsewhere", dupHour)
	refused := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Locked", Refused: "drm", DurationMs: dupHour})
	for _, id := range []string{a.ID, b.ID, far.ID, refused.ID} {
		dupFiles(t, st, id, "100:a")
	}
	gs, err := r.Duplicates(ctx, allScope)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := dupGroupTitles(gs), []string{"same_audio:Here+Here too"}; !reflect.DeepEqual(got, want) {
		t.Errorf("groups = %v, want %v (never across libraries, never refused)", got, want)
	}
	// A scope that hides one copy leaves nothing to group.
	gs, _ = r.Duplicates(ctx, listenScope(b.ID))
	if len(gs) != 0 {
		t.Errorf("groups with one copy hidden = %v", dupGroupTitles(gs))
	}
}

func TestDupPreviewCounts(t *testing.T) {
	r, st, f := dupRepo(t)
	keep, drop := dupAdd(t, r, f, "Keep", dupHour), dupAdd(t, r, f, "Drop", dupHour)
	dupFiles(t, st, keep.ID, "100:a")
	dupFiles(t, st, drop.ID, "100:a")
	dupProgress(t, st, dupUser, keep.ID, 1, 10)
	dupProgress(t, st, dupUser, drop.ID, 2, 20)
	dupProgress(t, st, dupMem, drop.ID, 3, 30)
	dupExec(t, st, `INSERT INTO grants(id, user_id, scope, book_id, created_at, updated_at) VALUES ('gr_d', 'us_b', 'book', ?, 1, 1)`, drop.ID)
	dupExec(t, st, `INSERT INTO bookmarks(id, user_id, book_id, position_ms, created_at, updated_at) VALUES ('bm_d', 'us_a', ?, 9, 1, 1)`, drop.ID)
	p, err := r.MergePreview(ctx, keep.ID, drop.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := fmt.Sprint(p.SameAudio, p.Listeners, p.ListenersBoth, p.Bookmarks, p.Grants)
	if got != "true 2 1 1 1" {
		t.Errorf("preview = %s, want true 2 1 1 1", got)
	}
	if _, err := r.MergePreview(ctx, keep.ID, keep.ID); err == nil {
		t.Error("preview of a book with itself")
	}
}
