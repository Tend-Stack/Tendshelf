package static

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestFlipFrontHasATransform: WebKit (the desktop app, the Android WebView is Blink) hides the back of a
// face only when the face has a transform of its own. Without one, the front of the leaf stays visible
// past the half turn, mirrored, over the back (seen in the desktop window, 2026-11-09). The rule that gives
// every side a transform must stay, and the back side's own rule must come after it.
func TestFlipFrontHasATransform(t *testing.T) {
	css := stripComments(readCSS(t, "pages-reader.css"))
	front := strings.Index(css, ".ts-flip__side { transform: rotateY(0.001deg); }")
	back := strings.Index(css, ".ts-flip__side--back { transform: rotateY(180deg); }")
	if front < 0 {
		t.Fatal("the front side of the flip leaf has no transform of its own")
	}
	if back < front {
		t.Error("the back side's rotateY(180deg) must come after the rule for every side")
	}
}

// TestReaderNeedsNoSecureContext: crypto.randomUUID is missing on a plain http page that is not localhost
// (the Android app over a home network, seen 2026-11-09: the page ribbon did nothing). The reader's scripts
// must not call it bare.
func TestReaderNeedsNoSecureContext(t *testing.T) {
	for _, name := range []string{"reader-pages.js", "reader-marks.js"} {
		data, err := os.ReadFile(filepath.Join("js", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(data), "\n") {
			if strings.Contains(line, "crypto.randomUUID()") && !strings.Contains(line, "crypto.randomUUID ?") {
				t.Errorf("%s calls crypto.randomUUID() without a fallback: %s", name, strings.TrimSpace(line))
			}
		}
	}
}
