package components

import (
	"strings"
	"testing"
)

// TestCoverSizes: both forms start with "auto", so a browser that knows it uses the real layout width
// of the lazy image, and the rest keep the old list (P3-11).
func TestCoverSizes(t *testing.T) {
	for _, size := range []string{"fill", "96", "160"} {
		got := coverSizes(size)
		if !strings.HasPrefix(got, "auto, ") {
			t.Errorf("coverSizes(%q) = %q, want an auto prefix", size, got)
		}
	}
	if got := coverSizes("96"); got != "auto, 96px" {
		t.Errorf("coverSizes(96) = %q", got)
	}
}
