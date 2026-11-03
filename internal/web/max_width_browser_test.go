//go:build dev

package web

import (
	"testing"
)

// TestMaxWidthBrowser: on a monitor wider than the app's 1920 px limit the shell, the player bar and the
// Now playing panel stay in one 1920 px column at the left edge (operator, 2026-11-03), the page fills that
// column, it never scrolls sideways, and the sidebar's toggle sits above the bar. At exactly 1920 px nothing
// is capped yet.
func TestMaxWidthBrowser(t *testing.T) {
	e := nowPanelEnv(t)
	for _, s := range []struct{ w, h int }{{1920, 1080}, {2560, 1080}, {3840, 1080}, {5120, 1440}} {
		w := s.w
		tab := nowPanelOpen(t, e, w, s.h)
		var r struct {
			ShellL, ShellR, BarL, BarR, PanelR, MainL, MainR, ScrollW, InnerW, ToggleB, BarT float64
		}
		tab.Eval(`(() => {
		  const q = (s) => document.querySelector(s).getBoundingClientRect();
		  const shell = q(".ts-shell"), bar = q("#player"), panel = q("#player-full"), main = q("#main"), toggle = q("#nav-toggle");
		  return {ShellL: shell.left, ShellR: shell.right, BarL: bar.left, BarR: bar.right, PanelR: panel.right,
		    MainL: main.left, MainR: main.right, ScrollW: document.documentElement.scrollWidth, InnerW: innerWidth,
		    ToggleB: toggle.bottom, BarT: bar.top};
		})()`, &r)
		near := func(a, b float64) bool { return a-b < 1.5 && b-a < 1.5 }
		// The page reserves a scrollbar gutter, so the usable width is the document's own width (ScrollW), not innerWidth.
		want := 1920.0
		if r.ScrollW < want {
			want = r.ScrollW
		}
		if !near(r.ShellR-r.ShellL, want) {
			t.Errorf("%d: shell is %.0f px wide, want %.0f", w, r.ShellR-r.ShellL, want)
		}
		if !near(r.ShellL, 0) {
			t.Errorf("%d: shell starts at %.1f, want the left edge", w, r.ShellL)
		}
		if w == 1920 && (!near(r.BarL, 0) || !near(r.BarR, r.ScrollW)) {
			t.Errorf("1920: bar %.1f..%.1f, want 0..%.0f (nothing capped yet)", r.BarL, r.BarR, r.ScrollW)
		}
		if !near(r.MainR, r.ShellR) {
			t.Errorf("%d: #main ends at %.1f, short of the shell's %.1f", w, r.MainR, r.ShellR)
		}
		if !near(r.BarL, r.ShellL) || !near(r.BarR, r.ShellR) {
			t.Errorf("%d: bar %.1f..%.1f, shell %.1f..%.1f", w, r.BarL, r.BarR, r.ShellL, r.ShellR)
		}
		if !near(r.PanelR, r.ShellR) {
			t.Errorf("%d: panel right %.1f, shell right %.1f", w, r.PanelR, r.ShellR)
		}
		if r.MainL < r.ShellL-0.5 || r.MainR > r.ShellR+0.5 {
			t.Errorf("%d: #main %.1f..%.1f outside shell %.1f..%.1f", w, r.MainL, r.MainR, r.ShellL, r.ShellR)
		}
		if r.ScrollW > r.InnerW {
			t.Errorf("%d: horizontal scroll, scrollWidth %.0f > innerWidth %.0f", w, r.ScrollW, r.InnerW)
		}
		if r.ToggleB >= r.BarT {
			t.Errorf("%d: #nav-toggle bottom %.0f is not above the bar top %.0f", w, r.ToggleB, r.BarT)
		}
	}
}
