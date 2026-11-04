package web

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestXNoAlignLeft: read-along is gone (decision 0018). No Go, templ or script file under internal/ or cmd/ names the
// removed package, the reader's old name or the runtimes page. Test files are skipped: some name the old words on purpose (an old link).
func TestXNoAlignLeft(t *testing.T) {
	gone := []string{"internal/" + "align", "read" + "along", "/admin/" + "runtimes"}
	for _, root := range []string{"../../internal", "../../cmd"} {
		err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			switch filepath.Ext(p) {
			case ".go", ".templ", ".js":
			default:
				return nil
			}
			if strings.HasSuffix(p, "_test.go") {
				return nil
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			for _, w := range gone {
				if strings.Contains(string(raw), w) {
					t.Errorf("%s still names %q", p, w)
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
}
