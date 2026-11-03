package library_test

import (
	"errors"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

const day = int64(24 * 60 * 60 * 1000)

// lookupBook adds a book with its details read (a sidecar signature) added at the given time.
func lookupBook(t *testing.T, r *library.Repo, lib library.Library, f library.Folder, root string, addedAt int64, read bool) string {
	t.Helper()
	b := library.Book{LibraryID: lib.ID, FolderID: f.ID, RootPath: root, Title: root, AddedAt: addedAt}
	err := r.Update(ctx, func(tx *library.Tx) error {
		if err := tx.InsertBook(&b); err != nil {
			return err
		}
		if read {
			return tx.PutSidecarSignature(b.ID, "sig", 1)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return b.ID
}

func putLookup(t *testing.T, r *library.Repo, id, outcome, job string, at int64) {
	t.Helper()
	if err := r.Update(ctx, func(tx *library.Tx) error { return tx.PutBookLookup(id, outcome, "audnexus", job, at) }); err != nil {
		t.Fatal(err)
	}
}

func TestLookupsNextBook(t *testing.T) {
	r, _ := newRepo(t)
	lib, f := fixture(t, r)
	other, err := r.CreateLibrary(ctx, "Other", 0)
	if err != nil {
		t.Fatal(err)
	}
	of, err := r.AddFolder(ctx, other.ID, "/other")
	if err != nil {
		t.Fatal(err)
	}
	const now = 100 * day
	unread := lookupBook(t, r, lib, f, "/audio/unread", 1, false)
	b1 := lookupBook(t, r, lib, f, "/audio/b1", 10, true)
	b2 := lookupBook(t, r, lib, f, "/audio/b2", 20, true)
	b3 := lookupBook(t, r, lib, f, "/audio/b3", 30, true)
	elsewhere := lookupBook(t, r, other, of, "/other/x", 5, true)
	_ = unread
	_ = elsewhere

	next := func(mode string, runStart, skipBefore int64) (string, int) {
		t.Helper()
		id, n, err := r.NextLookupBook(ctx, lib.ID, mode, runStart, skipBefore)
		if err != nil {
			t.Fatal(err)
		}
		return id, n
	}
	check := func(name, mode string, runStart, skipBefore int64, wantID string, wantN int) {
		t.Helper()
		if id, n := next(mode, runStart, skipBefore); id != wantID || n != wantN {
			t.Errorf("%s: got %q/%d, want %q/%d", name, id, n, wantID, wantN)
		}
	}
	skip := now - 30*day

	check("all, oldest first, unread and other library left out", "all", now, skip, b1, 3)
	putLookup(t, r, b1, library.LookupMatched, "j1", now+1)
	check("looked up in this run", "all", now, skip, b2, 2)
	check("a later run redoes it only outside the window", "all", now+2, skip, b2, 2)
	putLookup(t, r, b2, library.LookupNoMatch, "j1", now-10*day)
	check("10 days old is skipped", "all", now, skip, b3, 1)
	putLookup(t, r, b2, library.LookupNoMatch, "j1", now-31*day)
	check("31 days old is redone", "all", now, skip, b2, 2)
	putLookup(t, r, b2, library.LookupFailed, "j1", now-1*day)
	check("failed is retried", "all", now, skip, b2, 2)
	putLookup(t, r, b2, library.LookupFailed, "j1", now+5)
	check("failed in this run is not retried at once", "all", now, skip, b3, 1)
	putLookup(t, r, b3, library.LookupChosen, "", now-60*day)
	check("chosen is never redone", "all", now, skip, "", 0)
	putLookup(t, r, b3, library.LookupDismissed, "", now-60*day)
	check("dismissed is never redone", "all", now, skip, "", 0)
	putLookup(t, r, b2, library.LookupKept, "j1", now-60*day)

	// New mode: only books added since the switch, and only without an outcome other than failed.
	check("off means nothing is new", "new", now, skip, "", 0)
	if err := r.SetAutoLookup(ctx, lib.ID, 15); err != nil {
		t.Fatal(err)
	}
	if got, err := r.Library(ctx, lib.ID); err != nil || got.AutoLookupAt != 15 {
		t.Fatalf("library = %+v, %v", got, err)
	}
	b4 := lookupBook(t, r, lib, f, "/audio/b4", 40, true)
	check("new takes books added since, b3 is dismissed", "new", now+50*day, skip, b4, 1)
	putLookup(t, r, b4, library.LookupFailed, "j2", 1)
	check("new retries failed", "new", now+50*day, skip, b4, 1)
	putLookup(t, r, b4, library.LookupNeedsLook, "j2", 1)
	check("new skips any other outcome", "new", now+50*day, skip, "", 0)

	if _, _, err := r.NextLookupBook(ctx, lib.ID, "bogus", 0, 0); !errors.Is(err, library.ErrInvalid) {
		t.Errorf("bad mode: %v", err)
	}

	// A missing or refused book is not eligible.
	for _, b := range []library.Book{
		{RootPath: "/audio/gone", MissingSince: 3},
		{RootPath: "/audio/drm", RefusedReason: library.RefusedDRM},
	} {
		b.LibraryID, b.FolderID, b.Title, b.AddedAt = lib.ID, f.ID, "x", 50
		if err := r.Update(ctx, func(tx *library.Tx) error {
			if err := tx.InsertBook(&b); err != nil {
				return err
			}
			return tx.PutSidecarSignature(b.ID, "sig", 1)
		}); err != nil {
			t.Fatal(err)
		}
	}
	check("missing and refused books are left out", "new", now+50*day, skip, "", 0)
}

func TestLookupsCountsNeedsAndDue(t *testing.T) {
	r, _ := newRepo(t)
	lib, f := fixture(t, r)
	a := lookupBook(t, r, lib, f, "/audio/a", 100, true)
	b := lookupBook(t, r, lib, f, "/audio/b", 200, true)
	c := lookupBook(t, r, lib, f, "/audio/c", 300, true)
	putLookup(t, r, a, library.LookupNeedsLook, "j1", 1)
	putLookup(t, r, b, library.LookupNoMatch, "j1", 1)
	putLookup(t, r, c, library.LookupMatched, "j2", 1)
	counts, err := r.LookupCounts(ctx, "j1")
	if err != nil || counts[library.LookupNeedsLook] != 1 || counts[library.LookupNoMatch] != 1 || len(counts) != 2 {
		t.Errorf("counts = %v, %v", counts, err)
	}
	rows, total, err := r.NeedsLook(ctx, lib.ID, 1)
	if err != nil || total != 2 || len(rows) != 1 || rows[0].BookID != a || rows[0].Outcome != library.LookupNeedsLook {
		t.Errorf("needs look = %+v %d %v", rows, total, err)
	}

	d := lookupBook(t, r, lib, f, "/audio/d", 400, true)
	if id, due, err := r.AutoLookupDue(ctx, d); err != nil || id != lib.ID || due {
		t.Errorf("off: %v %v %v", id, due, err)
	}
	if err := r.SetAutoLookup(ctx, lib.ID, 350); err != nil {
		t.Fatal(err)
	}
	if _, due, _ := r.AutoLookupDue(ctx, d); !due {
		t.Error("new book is due")
	}
	if _, due, _ := r.AutoLookupDue(ctx, c); due {
		t.Error("a book added before the switch is not due")
	}
	putLookup(t, r, d, library.LookupMatched, "j3", 1)
	if _, due, _ := r.AutoLookupDue(ctx, d); due {
		t.Error("a settled book is not due")
	}
	if err := r.SetAutoLookup(ctx, lib.ID, 0); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.Library(ctx, lib.ID); got.AutoLookupAt != 0 {
		t.Error("off did not clear")
	}
	if _, _, err := r.AutoLookupDue(ctx, "bk_none"); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("unknown book: %v", err)
	}
	if err := r.SetAutoLookup(ctx, "lib_none", 5); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("unknown library: %v", err)
	}
}
