//go:build dev

package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/db"
	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// seedVisualIDs gives a visual test (its name has "Visual", as `make visual` selects them, or starts with TestA11y, the audit) and all its
// subtests a repeatable id sequence, so spine heights, avatar colours and anything else derived from an
// id come out the same on every run.
func seedVisualIDs(t *testing.T) {
	top := strings.SplitN(t.Name(), "/", 2)[0]
	if !strings.Contains(top, "Visual") && !strings.HasPrefix(top, "TestA11y") {
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

// fixedStorageEstimate makes navigator.storage.estimate give the same answer on every run in tab, from
// its next page load on. The Downloads page's storage block shows the estimate, which follows the
// machine's free disk space, so a capture of it changed from one day to the next. Only captures use
// it: the download tests check room against the browser's real answer.
func fixedStorageEstimate(t *testing.T, tab *cdp.Tab) {
	t.Helper()
	fixed := `navigator.storage.estimate = async () => ({usage: 3000000, quota: 11500000000});`
	if err := tab.Call("Page.addScriptToEvaluateOnNewDocument", map[string]any{"source": fixed}, nil); err != nil {
		t.Fatal(err)
	}
}
