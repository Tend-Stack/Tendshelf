package library_test

import (
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/library"
)

func TestListable(t *testing.T) {
	if got, want := library.Listable("b"), "b.refused_reason IS NULL AND b.missing_since IS NULL"; got != want {
		t.Fatalf("Listable(b) = %q, want %q", got, want)
	}
	for _, bad := range []string{"", "B", "b;", "b b", "1", "abcdefghijklmnopq", "b--"} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("Listable(%q) did not panic", bad)
				}
			}()
			library.Listable(bad)
		}()
	}
}
