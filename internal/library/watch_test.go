package library_test

import (
	"errors"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

// TestWatchSetting: new libraries watch and check every six hours; the setting saves and is validated.
func TestWatchSetting(t *testing.T) {
	r, _ := newRepo(t)
	l, _, err := r.CreateLibraryWithFolder(ctx, "Books", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Library(ctx, l.ID)
	if err != nil || !got.Watch || got.ScanIntervalMin != library.DefaultScanIntervalMin {
		t.Fatalf("new library = %+v, %v; want watch on and %d minutes", got, err, library.DefaultScanIntervalMin)
	}
	if err := r.SetWatch(ctx, l.ID, false, 60); err != nil {
		t.Fatal(err)
	}
	if got, _ = r.Library(ctx, l.ID); got.Watch || got.ScanIntervalMin != 60 {
		t.Errorf("after save = %+v", got)
	}
	if err := r.SetWatch(ctx, l.ID, true, -1); !errors.Is(err, library.ErrInvalid) {
		t.Errorf("negative interval: %v", err)
	}
	if err := r.SetWatch(ctx, "lib_nope", true, 0); !errors.Is(err, library.ErrNotFound) {
		t.Errorf("unknown library: %v", err)
	}
}
