package library_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
	"github.com/Tend-Stack/tendshelf/internal/library/librarytest"
	"github.com/Tend-Stack/tendshelf/internal/share"
)

var (
	adminAllScope  = library.Scope{Books: share.Filter{SQL: "1=1"}, Libraries: share.Filter{SQL: "1=1"}}
	adminNoneScope = library.Scope{Books: share.Filter{SQL: "0=1"}, Libraries: share.Filter{SQL: "0=1"}}
)

func TestFolderSummaries(t *testing.T) {
	r, _ := newRepo(t)
	l1, f1 := librarytest.NewLibrary(t, r, "Zeta", "/z")
	l2, f2 := librarytest.NewLibrary(t, r, "alpha", "/a")
	_, f3 := librarytest.NewLibrary(t, r, "Empty", "/e")
	librarytest.AddBook(t, r, f1, librarytest.BookSpec{Title: "One", Files: 3}) // sizes 0, 1000, 2000
	librarytest.AddBook(t, r, f1, librarytest.BookSpec{Title: "Gone", Files: 2, MissingSince: 5})
	librarytest.AddBook(t, r, f1, librarytest.BookSpec{Title: "Drm", Refused: library.RefusedDRM})
	librarytest.AddBook(t, r, f2, librarytest.BookSpec{Title: "Two", Files: 2}) // 0, 1000
	got, err := r.FolderSummaries(ctx, adminAllScope)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d folders", len(got))
	}
	// library name NOCASE: alpha, Empty, Zeta
	want := []struct {
		id, lib string
		books   int
		bytes   int64
	}{{f2.ID, "alpha", 1, 1000}, {f3.ID, "Empty", 0, 0}, {f1.ID, "Zeta", 1, 3000}}
	for i, w := range want {
		g := got[i]
		if g.ID != w.id || g.LibraryName != w.lib || g.Books != w.books || g.Bytes != w.bytes {
			t.Errorf("row %d = %+v, want %+v", i, g, w)
		}
	}
	_, _ = l1, l2
	if none, err := r.FolderSummaries(ctx, adminNoneScope); err != nil || len(none) != 0 {
		t.Fatalf("none scope = %v, %v", none, err)
	}
}

func TestProblemsGroupsAndCaps(t *testing.T) {
	r, _ := newRepo(t)
	_, f := librarytest.NewLibrary(t, r, "Books", "/b")
	for i := 0; i < 25; i++ {
		librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: fmt.Sprintf("Drm %02d", i), Refused: library.RefusedDRM})
	}
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Bad", Refused: library.RefusedDamaged})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Lost", MissingSince: 9})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Lost drm", MissingSince: 9, Refused: library.RefusedDRM})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Fine"})
	p, err := r.Problems(ctx, adminAllScope, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.RefusedCount[library.RefusedDRM] != 25 || len(p.Refused[library.RefusedDRM]) != 20 {
		t.Errorf("drm count %d list %d", p.RefusedCount[library.RefusedDRM], len(p.Refused[library.RefusedDRM]))
	}
	if p.Refused[library.RefusedDRM][0].Title != "Drm 00" {
		t.Errorf("first drm = %q", p.Refused[library.RefusedDRM][0].Title)
	}
	if p.RefusedCount[library.RefusedDamaged] != 1 || len(p.Refused[library.RefusedDamaged]) != 1 {
		t.Errorf("damaged = %v", p.Refused)
	}
	if p.MissingCount != 2 || len(p.Missing) != 2 {
		t.Errorf("missing %d %d", p.MissingCount, len(p.Missing))
	}
	if p.NoCoverCount != 1 || len(p.NoCover) != 1 || p.NoCover[0].Title != "Fine" {
		t.Errorf("no cover = %d %+v", p.NoCoverCount, p.NoCover)
	}
	small, err := r.Problems(ctx, adminAllScope, 3)
	if err != nil || len(small.Refused[library.RefusedDRM]) != 3 || small.RefusedCount[library.RefusedDRM] != 25 {
		t.Errorf("cap 3 = %v, %v", small.RefusedCount, err)
	}
	none, err := r.Problems(ctx, adminNoneScope, 0)
	if err != nil || len(none.RefusedCount) != 0 || none.MissingCount != 0 || none.NoCoverCount != 0 {
		t.Errorf("none scope = %+v, %v", none, err)
	}
}

func TestProblemTitleFallsBackToFolderName(t *testing.T) {
	r, _ := newRepo(t)
	_, f := librarytest.NewLibrary(t, r, "Books", "/b")
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "", Refused: library.RefusedDRM})
	p, err := r.Problems(ctx, adminAllScope, 0)
	if err != nil || len(p.Refused[library.RefusedDRM]) != 1 {
		t.Fatalf("%+v %v", p, err)
	}
	if got := p.Refused[library.RefusedDRM][0].Title; got == "" || got[0] == '/' {
		t.Errorf("title = %q", got)
	}
}

func TestNoCoverSkipsUnsettled(t *testing.T) {
	r, _ := newRepo(t)
	_, f := librarytest.NewLibrary(t, r, "Books", "/b")
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Settled"})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Waiting", Unsettled: true})
	p, err := r.Problems(ctx, adminAllScope, 0)
	if err != nil {
		t.Fatal(err)
	}
	if p.NoCoverCount != 1 || p.NoCover[0].Title != "Settled" {
		t.Fatalf("no cover = %d %+v", p.NoCoverCount, p.NoCover)
	}
}

func TestOverview(t *testing.T) {
	r, _ := newRepo(t)
	_, f := librarytest.NewLibrary(t, r, "Books", "/b")
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "A", DurationMs: 1000, Series: "S1", Narrators: []string{"N1", "N2"}})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "B", DurationMs: 2000, Series: "S1", Narrators: []string{"N1"}})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "C", DurationMs: 4000, Series: "S2", Narrators: []string{"N3"}, Authors: []string{"Au"}})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Gone", DurationMs: 9000, Series: "S9", Narrators: []string{"N9"}, MissingSince: 3})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Drm", DurationMs: 9000, Refused: library.RefusedDRM})
	o, err := r.Overview(ctx, adminAllScope)
	if err != nil {
		t.Fatal(err)
	}
	want := library.Overview{Books: 3, DurationMs: 7000, Series: 2, Narrators: 3, Problems: 2}
	if o != want {
		t.Fatalf("Overview = %+v, want %+v", o, want)
	}
	if z, err := r.Overview(ctx, adminNoneScope); err != nil || z != (library.Overview{}) {
		t.Fatalf("none scope = %+v, %v", z, err)
	}
}

func TestScanProgress(t *testing.T) {
	r, _ := newRepo(t)
	l, f := librarytest.NewLibrary(t, r, "Books", "/b")
	_, f2 := librarytest.NewLibrary(t, r, "Other", "/o")
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "A", Files: 2})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "B", Files: 3, Unsettled: true})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Gone", Files: 2, MissingSince: 3, Unsettled: true})
	librarytest.AddBook(t, r, f2, librarytest.BookSpec{Title: "Elsewhere", Files: 5, Unsettled: true})
	p, err := r.ScanProgress(ctx, adminAllScope, l.ID)
	if err != nil {
		t.Fatal(err)
	}
	// Books: A and B listable. Files: A 2 + B 3; the missing book's files and the other library's do not count.
	if p.Books != 2 {
		t.Errorf("Books = %d", p.Books)
	}
	if p.Files != 5 || p.FilesWaiting != 3 {
		t.Errorf("Files %d waiting %d", p.Files, p.FilesWaiting)
	}
	if z, err := r.ScanProgress(ctx, adminNoneScope, l.ID); err != nil || z != (library.ScanProgress{}) {
		t.Fatalf("none scope = %+v, %v", z, err)
	}
}

func TestCreateLibraryWithFolderAtomic(t *testing.T) {
	r, _ := newRepo(t)
	l, f, err := r.CreateLibraryWithFolder(ctx, " Fresh ", "/srv/a/")
	if err != nil || l.Name != "Fresh" || f.LibraryID != l.ID || f.Path != "/srv/a" {
		t.Fatalf("create = %+v %+v %v", l, f, err)
	}
	if _, _, err := r.CreateLibraryWithFolder(ctx, "Second", "/srv/a/sub"); !errors.Is(err, library.ErrFolderOverlap) {
		t.Fatalf("overlap err = %v", err)
	}
	libs, err := r.Libraries(ctx)
	if err != nil || len(libs) != 1 {
		t.Fatalf("libraries after refused create = %v, %v", libs, err)
	}
	if _, _, err := r.CreateLibraryWithFolder(ctx, "  ", "/x"); !errors.Is(err, library.ErrInvalid) {
		t.Fatalf("blank name err = %v", err)
	}
	if _, _, err := r.CreateLibraryWithFolder(ctx, "Rel", "relative"); !errors.Is(err, library.ErrInvalid) {
		t.Fatalf("relative path err = %v", err)
	}
	if libs, _ := r.Libraries(ctx); len(libs) != 1 {
		t.Fatalf("libraries = %v", libs)
	}
}

func TestMarkFolderBooksMissing(t *testing.T) {
	r, _ := newRepo(t)
	l, f := librarytest.NewLibrary(t, r, "Books", "/b")
	f2, err := r.AddFolder(ctx, l.ID, "/c")
	if err != nil {
		t.Fatal(err)
	}
	a := librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "A", Files: 2})
	librarytest.AddBook(t, r, f, librarytest.BookSpec{Title: "Already", MissingSince: 5})
	b := librarytest.AddBook(t, r, f2, librarytest.BookSpec{Title: "Other folder"})
	if n, err := r.PresentBooksInFolder(ctx, f.ID); err != nil || n != 1 {
		t.Fatalf("present = %d, %v", n, err)
	}
	var n int
	err = r.Update(ctx, func(tx *library.Tx) error {
		var err error
		n, err = tx.MarkFolderBooksMissing(f.ID, 777)
		return err
	})
	if err != nil || n != 1 {
		t.Fatalf("marked %d, %v", n, err)
	}
	got, _ := r.Book(ctx, a.ID)
	if got.MissingSince != 777 {
		t.Errorf("book missing_since = %d", got.MissingSince)
	}
	files, _ := r.AudioFiles(ctx, a.ID)
	for _, af := range files {
		if af.MissingSince != 777 {
			t.Errorf("file %s missing_since = %d", af.Path, af.MissingSince)
		}
	}
	if other, _ := r.Book(ctx, b.ID); other.MissingSince != 0 {
		t.Errorf("other folder marked: %d", other.MissingSince)
	}
	if of, _ := r.AudioFiles(ctx, b.ID); of[0].MissingSince != 0 {
		t.Errorf("other folder file marked")
	}
	err = r.Update(ctx, func(tx *library.Tx) error {
		var err error
		n, err = tx.MarkFolderBooksMissing(f.ID, 888)
		return err
	})
	if err != nil || n != 0 {
		t.Fatalf("second run marked %d, %v", n, err)
	}
	if got, _ := r.Book(ctx, a.ID); got.MissingSince != 777 {
		t.Errorf("second run changed missing_since to %d", got.MissingSince)
	}
}
