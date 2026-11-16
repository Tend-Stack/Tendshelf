package library_test

import (
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

func pairOf(size int64, letter byte) library.SizeQuick {
	return library.SizeQuick{Size: size, Quick: dupHash(letter)}
}

func TestSameDetailsRule(t *testing.T) {
	base := library.DupKey{Title: "The Long Road", Author: "Ann Writer", Narrator: "Nat Voice", DurationMs: 10 * dupHour}
	for _, c := range []struct {
		name string
		o    library.DupKey
		want bool
	}{
		{"same", base, true},
		{"case and punctuation", library.DupKey{Title: "the long road!", Author: "ANN writer", Narrator: "nat voice", DurationMs: 10*dupHour + 30_000}, true},
		{"two percent", library.DupKey{Title: "The Long Road", Author: "Ann Writer", Narrator: "Nat Voice", DurationMs: 10*dupHour + 9*60_000}, true},
		{"another edition", library.DupKey{Title: "The Long Road", Author: "Ann Writer", Narrator: "Nat Voice", DurationMs: 11 * dupHour}, false},
		{"another narrator", library.DupKey{Title: "The Long Road", Author: "Ann Writer", Narrator: "Other", DurationMs: 10 * dupHour}, false},
		{"no length", library.DupKey{Title: "The Long Road", Author: "Ann Writer", Narrator: "Nat Voice"}, false},
	} {
		if got := library.SameDetails(base, c.o); got != c.want {
			t.Errorf("%s: SameDetails = %v, want %v", c.name, got, c.want)
		}
	}
	if library.SameDetails(library.DupKey{Title: "T", Author: "A", DurationMs: 5}, library.DupKey{Title: "T", Author: "A", DurationMs: 5}) {
		t.Error("a missing narrator must never match")
	}
}

func TestFileMatches(t *testing.T) {
	r, st, f := dupRepo(t)
	whole := dupAdd(t, r, f, "Whole", dupHour)
	dupFiles(t, st, whole.ID, "100:a", "200:b", "300:c")
	other := dupAdd(t, r, f, "Other", dupHour)
	dupFiles(t, st, other.ID, "100:a", "999:z")

	// The whole sorted signature of the upload is held by one book: same_files, whatever the order.
	got, err := r.FileMatches(ctx, allScope, f.LibraryID, []library.SizeQuick{pairOf(300, 'c'), pairOf(100, 'a'), pairOf(200, 'b')})
	if err != nil || len(got) != 1 || got[0].BookID != whole.ID || got[0].Tier != library.MatchSameFiles {
		t.Fatalf("whole upload = %+v, %v", got, err)
	}
	// One file of a two-file upload matches: some_files only.
	got, _ = r.FileMatches(ctx, allScope, f.LibraryID, []library.SizeQuick{pairOf(200, 'b'), pairOf(50, 'q')})
	if len(got) != 1 || got[0].Tier != library.MatchSomeFiles || got[0].Matched != 1 || got[0].Total != 2 {
		t.Errorf("partial upload = %+v", got)
	}
	// A different size with the same hash is a different file.
	if got, _ = r.FileMatches(ctx, allScope, f.LibraryID, []library.SizeQuick{pairOf(101, 'a')}); len(got) != 0 {
		t.Errorf("size mismatch matched: %+v", got)
	}
}

func TestFileTierNeedsHalfTheBytes(t *testing.T) {
	for _, c := range []struct {
		matched, total      int
		matchedBytes, bytes int64
		want                string
	}{
		{3, 3, 10, 10, library.MatchSameFiles},
		{1, 3, 49, 100, ""}, // a shared intro file
		{1, 3, 50, 100, library.MatchSomeFiles},
		{0, 3, 0, 100, ""},
	} {
		if got := library.FileTier(c.matched, c.total, c.matchedBytes, c.bytes); got != c.want {
			t.Errorf("%+v = %q", c, got)
		}
	}
}

func TestFileMatchesManyPairs(t *testing.T) {
	r, st, f := dupRepo(t)
	b := dupAdd(t, r, f, "Small", dupHour)
	dupFiles(t, st, b.ID, "100:a")
	// 1000 pairs would blow SQLite's expression depth in one OR chain; the query runs in chunks.
	pairs := []library.SizeQuick{pairOf(100, 'a')}
	for i := 0; i < 999; i++ {
		pairs = append(pairs, library.SizeQuick{Size: int64(1000 + i), Quick: dupHash(byte('A' + i%26))})
	}
	got, err := r.FileMatches(ctx, allScope, f.LibraryID, pairs)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 { // one file of 1000 is far below half the bytes
		t.Errorf("got %+v", got)
	}
	if got, err = r.FileMatches(ctx, allScope, f.LibraryID, []library.SizeQuick{pairOf(100, 'a')}); err != nil || len(got) != 1 || got[0].Tier != library.MatchSameFiles {
		t.Errorf("single = %+v, %v", got, err)
	}
}

func TestFileMatchesScopeAndLibrary(t *testing.T) {
	r, st, f := dupRepo(t)
	secret := dupAdd(t, r, f, "Secret Title", dupHour)
	dupFiles(t, st, secret.ID, "100:a")
	pairs := []library.SizeQuick{pairOf(100, 'a')}

	hidden := library.Scope{Books: share.Filter{SQL: "b.id <> ?", Args: []any{secret.ID}}, Libraries: share.Filter{SQL: "1=1"}}
	for name, sc := range map[string]library.Scope{"book not listable": hidden, "nothing listable": noneScope} {
		got, err := r.FileMatches(ctx, sc, f.LibraryID, pairs)
		if err != nil || len(got) != 0 {
			t.Errorf("%s: %+v, %v (a title must not leak)", name, got, err)
		}
		det, _ := r.DetailMatches(ctx, sc, f.LibraryID, "Secret Title", "A", "N", dupHour)
		if len(det) != 0 {
			t.Errorf("%s: details leaked %+v", name, det)
		}
	}
	// Another library does not match.
	lib2, _ := librarytest.NewLibrary(t, r, "Second", "/audio2")
	if got, err := r.FileMatches(ctx, allScope, lib2.ID, pairs); err != nil || len(got) != 0 {
		t.Errorf("other library: %+v, %v", got, err)
	}
}

func TestDetailMatchesAndSameAudioCopies(t *testing.T) {
	r, st, f := dupRepo(t)
	a := dupDetailed(t, r, f, "Twin", "Ann", "Nat", 10*dupHour)
	b := dupDetailed(t, r, f, "twin", "ANN", "Nat", 10*dupHour+1000)
	far := dupDetailed(t, r, f, "Twin", "Ann", "Nat", 12*dupHour)
	dupFiles(t, st, a.ID, "100:a", "200:b")
	dupFiles(t, st, b.ID, "200:b", "100:a")
	dupFiles(t, st, far.ID, "100:a")

	got, err := r.DetailMatches(ctx, allScope, f.LibraryID, "Twin", "Ann", "Nat", 10*dupHour)
	if err != nil || len(got) != 2 || got[0].BookID != a.ID || got[1].BookID != b.ID || got[0].Tier != library.MatchSameDetails {
		t.Fatalf("details = %+v, %v", got, err)
	}
	if got, _ := r.DetailMatches(ctx, allScope, f.LibraryID, "Twin", "Ann", "", 10*dupHour); len(got) != 0 {
		t.Errorf("no narrator matched: %+v", got)
	}
	ids, err := r.SameAudioCopies(ctx, allScope, a.ID)
	if err != nil || len(ids) != 1 || ids[0] != b.ID {
		t.Fatalf("copies of a = %v, %v", ids, err)
	}
	if err := r.NotDuplicates(ctx, a.ID, b.ID, dupUser); err != nil {
		t.Fatal(err)
	}
	if ids, _ = r.SameAudioCopies(ctx, allScope, a.ID); len(ids) != 0 {
		t.Errorf("a pair marked not the same still listed: %v", ids)
	}
	if ids, _ = r.SameAudioCopies(ctx, noneScope, a.ID); len(ids) != 0 {
		t.Errorf("scope ignored: %v", ids)
	}
}
