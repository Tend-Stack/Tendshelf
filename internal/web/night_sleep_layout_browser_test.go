//go:build dev

package web

import (
	"testing"
	"time"
)

// TestNightSleepSettingsLayout: the night fields on the Settings page do not overflow the page sideways
// and every one is at least 44 px tall, at 390 and 1440 px.
func TestNightSleepSettingsLayout(t *testing.T) {
	for _, size := range []struct {
		name string
		w, h int
	}{{"390", 390, 844}, {"1440", 1440, 900}} {
		t.Run(size.name, func(t *testing.T) {
			e := playerApp(t)
			tab := fullpOpen(t, e, size.w, size.h, true)
			tab.Eval(`window.location.assign("/me/settings/playback")`, nil)
			tab.WaitFor(`!!document.getElementById("st-night-from")`, 10*time.Second)
			var got struct {
				Overflow bool
				Heights  []float64
				Inside   bool
			}
			tab.Eval(`(() => {
			  const ids = ["st-night-from", "st-night-to", "st-night-min"].map((id) => document.getElementById(id));
			  const row = ids[0].closest(".ts-night").getBoundingClientRect();
			  return { Overflow: document.documentElement.scrollWidth > document.documentElement.clientWidth,
			    Heights: ids.map((el) => el.getBoundingClientRect().height),
			    Inside: ids.every((el) => { const r = el.getBoundingClientRect(); return r.left >= row.left - 1 && r.right <= row.right + 1; }) };
			})()`, &got)
			if got.Overflow {
				t.Error("the page scrolls sideways")
			}
			if !got.Inside {
				t.Error("a night field sticks out of its row")
			}
			for i, h := range got.Heights {
				if h < 44 {
					t.Errorf("field %d is %.1f px tall, want at least 44", i, h)
				}
			}
		})
	}
}
