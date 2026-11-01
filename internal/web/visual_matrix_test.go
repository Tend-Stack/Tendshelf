package web

import (
	"os"
	"testing"
)

// visualShot is one theme at one window width.
type visualShot struct {
	Theme string
	Width int
}

// visualFull reports whether the full matrix is wanted (make visual-full sets TENDSHELF_VISUAL_FULL=1).
func visualFull() bool { return os.Getenv("TENDSHELF_VISUAL_FULL") == "1" }

// visualMatrix is the theme and width combinations a page test captures. The fast default (make
// visual) covers every theme and every width at least once on a page's main shots and the two
// extremes on the rest; the full run (make visual-full) is the four-theme, four-width parity rule:
// all four themes at four widths for a main page, dark and light at four widths for the rest.
func visualMatrix(main bool) []visualShot {
	if !visualFull() {
		if main {
			return []visualShot{{"dark", 390}, {"dark", 1440}, {"light", 768}, {"sepia", 1024}, {"contrast", 390}}
		}
		return []visualShot{{"dark", 390}, {"light", 1440}}
	}
	themes := []string{"dark", "light", "sepia", "contrast"}
	if !main {
		themes = themes[:2]
	}
	var out []visualShot
	for _, th := range themes {
		for _, w := range []int{390, 768, 1024, 1440} {
			out = append(out, visualShot{th, w})
		}
	}
	return out
}

func TestVisualMatrixShapes(t *testing.T) {
	t.Setenv("TENDSHELF_VISUAL_FULL", "")
	if visualFull() {
		t.Fatal("visualFull true without the variable")
	}
	fastMain, fastSec := visualMatrix(true), visualMatrix(false)
	if len(fastMain) != 5 || len(fastSec) != 2 {
		t.Fatalf("fast shapes: main %d, secondary %d", len(fastMain), len(fastSec))
	}
	themes, widths := map[string]bool{}, map[int]bool{}
	for _, s := range fastMain {
		themes[s.Theme], widths[s.Width] = true, true
	}
	if len(themes) != 4 || len(widths) != 4 {
		t.Errorf("fast main covers %d themes and %d widths, want 4 and 4", len(themes), len(widths))
	}

	t.Setenv("TENDSHELF_VISUAL_FULL", "1")
	if !visualFull() {
		t.Fatal("visualFull false with the variable set")
	}
	fullMain, fullSec := visualMatrix(true), visualMatrix(false)
	if len(fullMain) != 16 || len(fullSec) != 8 {
		t.Fatalf("full shapes: main %d, secondary %d", len(fullMain), len(fullSec))
	}
	for _, s := range fullSec {
		if s.Theme != "dark" && s.Theme != "light" {
			t.Errorf("full secondary has theme %q", s.Theme)
		}
	}
	seen := map[visualShot]bool{}
	for _, s := range fullMain {
		if seen[s] {
			t.Errorf("duplicate %v", s)
		}
		seen[s] = true
	}
}
