package static

import (
	"strings"
	"testing"
)

// comfortShare is the 60 % of --mark that pages-reader.css mixes into the warm wash.
const comfortShare = 0.6

// cfMultiply is what mix-blend-mode: multiply does to a backdrop colour under a translucent wash.
func cfMultiply(backdrop, wash b1RGBA) b1RGBA {
	a := wash.a * comfortShare
	f := func(b, w float64) float64 { return b * (1 - a + a*w/255) }
	return b1RGBA{f(backdrop.r, wash.r), f(backdrop.g, wash.g), f(backdrop.b, wash.b), 1}
}

// TestComfortContrast: the warm light (G5) darkens text and ground together, so reading text must still meet 4.5:1
// in the three themes that get the wash. The contrast theme never gets it (a rule in pages-reader.css, checked below).
func TestComfortContrast(t *testing.T) {
	for _, th := range b1Themes(t) {
		if th.name == "contrast" {
			continue
		}
		ground := b1Colour(t, th.get(t, "--ground"))
		ink := b1Colour(t, th.get(t, "--read-ink"))
		wash := b1Colour(t, th.get(t, "--mark"))
		if got := b1Ratio(cfMultiply(ink, wash), cfMultiply(ground, wash)); got < 4.5 {
			t.Errorf("%s: read ink under the warm wash is %.2f:1, want 4.5:1; lower the share in pages-reader.css and here", th.name, got)
		}
	}
	css := readCSS(t, "pages-reader.css")
	if !strings.Contains(css, `[data-theme="contrast"] .ts-readerpage[data-warm="on"]::after { content: none; }`) {
		t.Error("the contrast theme must switch the warm wash off")
	}
	if !strings.Contains(css, "color-mix(in srgb, var(--mark) 60%, transparent)") {
		t.Error("the wash is no longer 60 % of --mark: update comfortShare")
	}
}
