//go:build dev

package web

import (
	"testing"
)

// The page keeps room at its foot for the player bar only while the bar shows: a suppressed bar
// (Resume on screen, the Now playing panel) or no book at all leaves no empty strip (operator, 2026-11-04).
// Run by: TENDSHELF_TEST_BROWSER=<chromium> go test -tags dev -run TestBarRoom ./internal/web/
func TestBarRoomBrowser(t *testing.T) {
	e := nowPanelEnv(t)
	for _, w := range []int{1280, 1440, 1920} { // the Now playing panel opens from 1280
		tab := nowPanelOpen(t, e, w, 900)
		var r struct{ Shown, Suppressed, Hidden, PanelGapShown, PanelGapSuppressed float64 }
		tab.Eval(`(() => {
		  const slot = document.getElementById("player"), main = document.getElementById("main");
		  const pad = () => parseFloat(getComputedStyle(main).paddingBottom);
		  slot.classList.remove("is-suppressed");
		  slot.hidden = false;
		  const gap = () => innerHeight - document.getElementById("player-full").getBoundingClientRect().bottom;
		  const shown = pad(), panelShown = gap();
		  slot.classList.add("is-suppressed");
		  const suppressed = pad(), panelSuppressed = gap();
		  slot.classList.remove("is-suppressed");
		  slot.hidden = true;
		  const hidden = pad();
		  slot.hidden = false;
		  return {Shown: shown, Suppressed: suppressed, Hidden: hidden, PanelGapShown: panelShown, PanelGapSuppressed: panelSuppressed};
		})()`, &r)
		if r.Suppressed >= r.Shown || r.Hidden >= r.Shown {
			t.Errorf("%d: foot room shown %.0f px, suppressed %.0f px, hidden %.0f px; want less without the bar", w, r.Shown, r.Suppressed, r.Hidden)
		}
		// The Now playing panel stops above a showing bar and reaches the foot when the bar is suppressed.
		if r.PanelGapShown < 40 || r.PanelGapSuppressed > 1 {
			t.Errorf("%d: panel gap with the bar %.0f px, without it %.0f px; want room above the bar and none without", w, r.PanelGapShown, r.PanelGapSuppressed)
		}
		if r.Suppressed > 40 {
			t.Errorf("%d: a suppressed bar still leaves %.0f px at the foot", w, r.Suppressed)
		}
	}
}
