package static

import (
	"strings"
	"testing"
)

// TestB3HitArea: the hit-area rule from the M3c plan is in utilities.css, and the small controls carry it.
func TestB3HitArea(t *testing.T) {
	css := stripComments(readCSS(t, "utilities.css"))
	flat := strings.Join(strings.Fields(css), " ")
	for _, want := range []string{
		".ts-hit, .ts-check, .ts-switch",
		"content: \"\"; position: absolute; inset: min(0px, calc((100% - 2.75rem) / 2));",
	} {
		if !strings.Contains(flat, want) {
			t.Errorf("utilities.css lacks %q", want)
		}
	}
	for _, sel := range []string{".ts-hit::after", ".ts-check::before", ".ts-switch::before", ".ts-chip::after"} {
		if !strings.Contains(flat, sel) {
			t.Errorf("utilities.css gives %s no hit area", sel)
		}
	}
}
