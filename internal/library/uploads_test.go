package library_test

import (
	"errors"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

func TestSetUploadFolder(t *testing.T) {
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
	if got, _ := r.Library(ctx, lib.ID); got.UploadFolderID != "" {
		t.Fatalf("uploads are on by default: %q", got.UploadFolderID)
	}
	if err := r.SetUploadFolder(ctx, lib.ID, f.ID); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.Library(ctx, lib.ID); got.UploadFolderID != f.ID {
		t.Errorf("folder not stored: %q", got.UploadFolderID)
	}
	// A folder of another library, an unknown folder and an unknown library are refused.
	for name, c := range map[string][2]string{
		"other library's folder": {lib.ID, of.ID},
		"unknown folder":         {lib.ID, "lf_nope"},
		"unknown library":        {"lib_nope", f.ID},
		"unknown library, off":   {"lib_nope", ""},
	} {
		if err := r.SetUploadFolder(ctx, c[0], c[1]); !errors.Is(err, library.ErrNotFound) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	if got, _ := r.Library(ctx, lib.ID); got.UploadFolderID != f.ID {
		t.Errorf("a refused change altered the setting: %q", got.UploadFolderID)
	}
	if err := r.SetUploadFolder(ctx, lib.ID, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.Library(ctx, lib.ID); got.UploadFolderID != "" {
		t.Errorf("not turned off: %q", got.UploadFolderID)
	}
}

func TestBookRootsIn(t *testing.T) {
	r, _ := newRepo(t)
	lib, f := fixture(t, r)
	addBook(t, r, lib, f, "/audio/B")
	addBook(t, r, lib, f, "/audio/A")
	got, err := r.BookRootsIn(ctx, f.ID)
	if err != nil || len(got) != 2 || got[0] != "/audio/A" || got[1] != "/audio/B" {
		t.Errorf("roots = %v, %v", got, err)
	}
	if got, _ := r.BookRootsIn(ctx, "lf_nope"); len(got) != 0 {
		t.Errorf("unknown folder: %v", got)
	}
}
