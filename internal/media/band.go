package media

import (
	"fmt"
	"math"
)

// BandMax is the strongest share of the cover tint the book page's band ever draws, per theme: the
// phone board's top alpha. The stylesheet (pages-book.css) uses the same numbers.
var BandMax = map[string]float64{"dark": 0.85, "light": 0.28, "sepia": 0.28}

// BandSteps are the fractions of BandMax a band can be drawn at, strongest first. A level is the
// 1-based index into this list.
var BandSteps = []float64{1, 0.66, 0.33}

// minBandText is the contrast the page's text keeps against the band.
const minBandText = 4.5

// BandLevels picks, per theme, the strongest band level at which the tint, laid over the theme's
// ground, still gives the ink and the muted ink 4.5:1 and leaves the band on the same side of the
// ink's luminance as the ground. The fade only weakens the tint going down, so every point of it
// passes as well. A theme with no passing level is absent: that theme gets a plain band. The map is
// nil for a tint that is not "#rrggbb".
func BandLevels(tint string) map[string]int {
	if _, _, _, ok := parseHex(tint); !ok {
		return nil
	}
	out := map[string]int{}
	for _, th := range Themes {
		for i, k := range BandSteps {
			if bandPasses(th, mixHex(th.Ground, tint, BandMax[th.Name]*k)) {
				out[th.Name] = i + 1
				break
			}
		}
	}
	return out
}

// bandPasses reports whether both inks read on the band colour and the band is still on the
// ground's side of the ink.
func bandPasses(th Theme, band string) bool {
	if Contrast(th.Ink, band) < minBandText || Contrast(th.InkMuted, band) < minBandText {
		return false
	}
	return sideOfInk(th, band) == sideOfInk(th, th.Ground)
}

// sideOfInk is +1 when the colour is darker than the theme's ink and -1 when it is lighter.
func sideOfInk(th Theme, hex string) int {
	ir, ig, ib, _ := parseHex(th.Ink)
	r, g, b, _ := parseHex(hex)
	if luminance(r, g, b) <= luminance(ir, ig, ib) {
		return 1
	}
	return -1
}

// mixHex lays tint over ground at alpha a, in sRGB space like CSS color-mix: per channel
// round(g + (t-g)*a).
func mixHex(ground, tint string, a float64) string {
	gr, gg, gb, _ := parseHex(ground)
	tr, tg, tb, _ := parseHex(tint)
	mix := func(g, t int) int {
		return int(math.Round(float64(g) + float64(t-g)*a))
	}
	return fmt.Sprintf("#%02x%02x%02x", mix(gr, tr), mix(gg, tg), mix(gb, tb))
}
