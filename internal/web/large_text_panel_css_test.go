package web

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLargeTextPanelGuard: the Now playing panel's 1280 px block (it hides the full player's transport and
// settings bar) must open with the standard-text guard. Large text uses the tablet layout at every width,
// so there the transport and the bar stay in the full player (P3-11).
func TestLargeTextPanelGuard(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("static", "css", "now-panel.css"))
	if err != nil {
		t.Fatal(err)
	}
	headers, bodies := textsizeBlocks(string(raw), "width >= 1280px")
	if len(headers) == 0 {
		t.Fatal("now-panel.css has no 1280 px block")
	}
	for i, body := range bodies {
		// A leading comment is allowed; the guard must come before any rule.
		rest := strings.TrimSpace(body)
		for strings.HasPrefix(rest, "/*") {
			end := strings.Index(rest, "*/")
			if end < 0 {
				break
			}
			rest = strings.TrimSpace(rest[end+2:])
		}
		if !strings.HasPrefix(rest, textsizeStdGuard) {
			t.Errorf("now-panel.css: 1280 px block %d does not open with the standard-text guard", i)
		}
	}
}
