//go:build dev

package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/db"
)

// seedVisualIDs gives a visual test (its name has "Visual", as `make visual` selects them) and all its
// subtests a repeatable id sequence, so spine heights, avatar colours and anything else derived from an
// id come out the same on every run.
func seedVisualIDs(t *testing.T) {
	top := strings.SplitN(t.Name(), "/", 2)[0]
	if !strings.Contains(top, "Visual") {
		db.SeedIDs("") // other tests keep random ids
		return
	}
	db.SeedIDs(top)
}

// visualTempDir is a temp folder with a fixed path (os.TempDir()/tendshelf-visual/<test>), made empty
// and removed afterwards. t.TempDir() puts a random number in the path, and the folder browser shows
// that path on screen, so its captures would change on every run.
func visualTempDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "tendshelf-visual", strings.SplitN(t.Name(), "/", 2)[0])
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}
