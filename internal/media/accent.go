// OKLab and OKLCH conversions follow Björn Ottosson's reference, https://bottosson.github.io/posts/oklab/ (public domain / MIT); see NOTICES.md.

package media

import (
	"fmt"
	"image"
	"math"
	"strconv"
)

// Theme is a colour theme's grounds, as the player draws the cover accent on them. The values are
// the tokens in internal/web/static/css/tokens.css (a test keeps them equal). The contrast theme is
// left out: it never uses the cover accent.
type Theme struct {
	Name     string
	Ground   string // page background
	Surface  string // card and player background
	OnAccent string // text on the accent (the play button's label)
	Ink      string // body text (--ink)
	InkMuted string // quiet text (--ink-muted)
}

// Themes are the themes that take a cover accent.
var Themes = []Theme{
	{"dark", "#14110e", "#1e1a15", "#14110e", "#f1e8da", "#b5a894"},
	{"light", "#f4eee3", "#fbf7f0", "#ffffff", "#1f1a14", "#5e5448"},
	{"sepia", "#e6d7bc", "#eee2cb", "#ffffff", "#33281b", "#55452f"},
}

// Accent colour rules (docs/design/tendshelf-handoff/docs/HANDOFF.md, "Cover accent and theme rules").
const (
	maxChroma   = 0.10
	minChroma   = 0.03 // below this the cover is grey: no accent
	minOnAccent = 4.5  // text on the accent
	minGraphic  = 3.0  // the accent against its ground and surface
)

// tintBucket is the pixels of one colour bucket: their count and channel sums.
type tintBucket struct{ n, r, g, b int }

// tintBuckets counts kept pixels per bucket, the bucket key being the top bits of each channel.
type tintBuckets map[int]*tintBucket

// add counts one pixel in the bucket of its top bits per channel.
func (tb tintBuckets) add(r, g, b, bits int) {
	shift := 8 - bits
	key := r>>shift<<(2*bits) | g>>shift<<bits | b>>shift
	bk := tb[key]
	if bk == nil {
		bk = &tintBucket{}
		tb[key] = bk
	}
	bk.n++
	bk.r += r
	bk.g += g
	bk.b += b
}

// winner returns the mean colour of the biggest bucket (a tie goes to the smaller key) when it holds
// at least 5 % of the cover's pixels.
func (tb tintBuckets) winner(total int) (hex string, ok bool) {
	best, bestKey := (*tintBucket)(nil), 0
	for key, bk := range tb {
		if best == nil || bk.n > best.n || (bk.n == best.n && key < bestKey) {
			best, bestKey = bk, key
		}
	}
	if best == nil || best.n*20 < total {
		return "", false
	}
	mean := func(sum int) int { return (sum + best.n/2) / best.n }
	return fmt.Sprintf("#%02x%02x%02x", mean(best.r), mean(best.g), mean(best.b)), true
}

// Tint returns the dominant colour of a cover as "#rrggbb". Near-black and near-white pixels are
// ignored. Colours are counted in 4-bit-per-channel buckets; the biggest bucket wins (a tie goes to
// the smaller bucket key) and its pixels' mean is the tint. A cover of busy artwork, such as a
// gradient, spreads over too many fine buckets for any to reach 5 %; it gets a second count in
// coarser 3-bit buckets under the same rules. A cover with no usable colour (under 10 % of its
// pixels kept, or no winner of 5 % in either count) has none.
func Tint(img image.Image) (hex string, ok bool) {
	b := img.Bounds()
	total := b.Dx() * b.Dy()
	if total <= 0 {
		return "", false
	}
	fine, coarse := tintBuckets{}, tintBuckets{}
	kept := 0
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r16, g16, b16, _ := img.At(x, y).RGBA()
			r, g, bl := int(r16>>8), int(g16>>8), int(b16>>8)
			hi, lo := max(r, g, bl), min(r, g, bl)
			if hi < 40 || lo > 225 {
				continue
			}
			kept++
			fine.add(r, g, bl, 4)
			coarse.add(r, g, bl, 3)
		}
	}
	if kept*10 < total {
		return "", false
	}
	if hex, ok := fine.winner(total); ok {
		return hex, true
	}
	return coarse.winner(total)
}

// Accents derives the accent colour of each theme from a tint: the tint's hue, chroma capped at
// 0.10, and the first lightness in the theme's range (dark 0.75 to 0.85 from the middle outwards,
// light and sepia 0.46 down to 0.38) whose colour, in the sRGB gamut, gives the label 4.5:1 and
// reaches 3:1 against the theme's ground and surface. The map is keyed by theme name; a theme with
// no passing lightness is left out, and a grey tint (chroma under 0.03) gives no accent at all.
func Accents(tint string) (map[string]string, bool) {
	r, g, b, ok := parseHex(tint)
	if !ok {
		return nil, false
	}
	_, c, h := toOKLCH(r, g, b)
	if c < minChroma {
		return nil, false
	}
	c = math.Min(c, maxChroma)
	out := map[string]string{}
	for _, th := range Themes {
		for _, l := range lightnessCandidates(th.Name) {
			hex := inGamutHex(l, c, h)
			if Contrast(th.OnAccent, hex) >= minOnAccent && Contrast(hex, th.Ground) >= minGraphic && Contrast(hex, th.Surface) >= minGraphic {
				out[th.Name] = hex
				break
			}
		}
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

// lightnessCandidates lists the lightness values to try, best first.
func lightnessCandidates(theme string) []float64 {
	if theme == "dark" {
		return []float64{0.80, 0.81, 0.79, 0.82, 0.78, 0.83, 0.77, 0.84, 0.76, 0.85, 0.75}
	}
	return []float64{0.46, 0.45, 0.44, 0.43, 0.42, 0.41, 0.40, 0.39, 0.38}
}

// Contrast is the WCAG 2.x contrast ratio of two "#rrggbb" colours (1 to 21); 0 for bad input.
func Contrast(a, b string) float64 {
	ar, ag, ab, ok1 := parseHex(a)
	br, bg, bb, ok2 := parseHex(b)
	if !ok1 || !ok2 {
		return 0
	}
	la, lb := luminance(ar, ag, ab), luminance(br, bg, bb)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func parseHex(s string) (r, g, b int, ok bool) {
	if len(s) != 7 || s[0] != '#' {
		return 0, 0, 0, false
	}
	v, err := strconv.ParseUint(s[1:], 16, 32)
	if err != nil {
		return 0, 0, 0, false
	}
	return int(v >> 16), int(v >> 8 & 0xff), int(v & 0xff), true
}

func luminance(r, g, b int) float64 {
	return 0.2126*srgbToLinear(float64(r)/255) + 0.7152*srgbToLinear(float64(g)/255) + 0.0722*srgbToLinear(float64(b)/255)
}

func srgbToLinear(c float64) float64 {
	if c <= 0.04045 {
		return c / 12.92
	}
	return math.Pow((c+0.055)/1.055, 2.4)
}

func linearToSRGB(c float64) float64 {
	if c <= 0.0031308 {
		return 12.92 * c
	}
	return 1.055*math.Pow(c, 1/2.4) - 0.055
}

// oklab converts 8-bit sRGB to OKLab.
func oklab(r, g, b int) (L, a, bb float64) {
	lr, lg, lb := srgbToLinear(float64(r)/255), srgbToLinear(float64(g)/255), srgbToLinear(float64(b)/255)
	l := math.Cbrt(0.4122214708*lr + 0.5363325363*lg + 0.0514459929*lb)
	m := math.Cbrt(0.2119034982*lr + 0.6806995451*lg + 0.1073969566*lb)
	s := math.Cbrt(0.0883024619*lr + 0.2817188376*lg + 0.6299787005*lb)
	return 0.2104542553*l + 0.7936177850*m - 0.0040720468*s,
		1.9779984951*l - 2.4285922050*m + 0.4505937099*s,
		0.0259040371*l + 0.7827717662*m - 0.8086757660*s
}

// toOKLCH converts 8-bit sRGB to lightness, chroma and hue (radians).
func toOKLCH(r, g, b int) (L, c, h float64) {
	L, a, bb := oklab(r, g, b)
	return L, math.Hypot(a, bb), math.Atan2(bb, a)
}

// linearFromOKLCH returns the linear sRGB of an OKLCH colour, possibly outside [0, 1].
func linearFromOKLCH(L, c, h float64) (r, g, b float64) {
	a, bb := c*math.Cos(h), c*math.Sin(h)
	l := math.Pow(L+0.3963377774*a+0.2158037573*bb, 3)
	m := math.Pow(L-0.1055613458*a-0.0638541728*bb, 3)
	s := math.Pow(L-0.0894841775*a-1.2914855480*bb, 3)
	return 4.0767416621*l - 3.3077115913*m + 0.2309699292*s,
		-1.2684380046*l + 2.6097574011*m - 0.3413193965*s,
		-0.0041960863*l - 0.7034186147*m + 1.7076147010*s
}

// inGamutHex lowers the chroma by 0.001 at a time until the colour fits sRGB, then rounds it to hex.
func inGamutHex(L, c, h float64) string {
	const eps = 1e-4
	for ; c > 0; c -= 0.001 {
		r, g, b := linearFromOKLCH(L, c, h)
		if r >= -eps && r <= 1+eps && g >= -eps && g <= 1+eps && b >= -eps && b <= 1+eps {
			break
		}
	}
	c = math.Max(c, 0)
	r, g, b := linearFromOKLCH(L, c, h)
	to8 := func(v float64) int {
		return int(math.Round(math.Min(1, math.Max(0, linearToSRGB(math.Min(1, math.Max(0, v))))) * 255))
	}
	return fmt.Sprintf("#%02x%02x%02x", to8(r), to8(g), to8(b))
}
