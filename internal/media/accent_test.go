package media

import (
	"image"
	"image/color"
	"math"
	"os"
	"regexp"
	"strings"
	"testing"
)

// The handoff's example pairs (dark / light). Its dark examples are the tints fed to Accents. The
// pairs were tuned by hand outside the handoff's own rules (the light values sit above L 0.46 and
// rust above chroma 0.10), so Tendshelf follows the rules: the goldens below are what they give,
// and the handoff values are held to a ΔE_OK tolerance instead.
var handoffPairs = []struct {
	name                      string
	tint                      string
	handoffDark, handoffLight string
	goldenDark, goldenLight   string
}{
	{"teal", "#9ccbc4", "#9ccbc4", "#2c6660", "#9ac9c2", "#35615c"},
	{"moss", "#b8cb9a", "#b8cb9a", "#4b6338", "#b3c695", "#4f5f32"},
	{"rust", "#f0a27e", "#f0a27e", "#9a3d1d", "#f4a987", "#854423"},
	{"plum", "#c7b3e0", "#c7b3e0", "#5b4677", "#c8b3e1", "#614e76"},
}

// deltaEOK is the distance between two colours in OKLab.
func deltaEOK(t *testing.T, a, b string) float64 {
	t.Helper()
	ar, ag, ab, ok1 := parseHex(a)
	br, bg, bb, ok2 := parseHex(b)
	if !ok1 || !ok2 {
		t.Fatalf("bad colours %q %q", a, b)
	}
	l1, a1, b1 := oklab(ar, ag, ab)
	l2, a2, b2 := oklab(br, bg, bb)
	return math.Sqrt((l1-l2)*(l1-l2) + (a1-a2)*(a1-a2) + (b1-b2)*(b1-b2))
}

func channelsWithin(t *testing.T, got, want string, tol int) bool {
	t.Helper()
	gr, gg, gb, _ := parseHex(got)
	wr, wg, wb, _ := parseHex(want)
	d := func(a, b int) bool { return a-b <= tol && b-a <= tol }
	return d(gr, wr) && d(gg, wg) && d(gb, wb)
}

func TestHandoffPairs(t *testing.T) {
	for _, p := range handoffPairs {
		t.Run(p.name, func(t *testing.T) {
			acc, ok := Accents(p.tint)
			if !ok {
				t.Fatalf("no accent for %s", p.tint)
			}
			if len(acc) != 3 {
				t.Fatalf("accents = %v, want all three themes", acc)
			}
			if !channelsWithin(t, acc["dark"], p.goldenDark, 1) {
				t.Errorf("dark = %s, golden %s", acc["dark"], p.goldenDark)
			}
			if !channelsWithin(t, acc["light"], p.goldenLight, 1) {
				t.Errorf("light = %s, golden %s", acc["light"], p.goldenLight)
			}
			if acc["sepia"] != acc["light"] {
				t.Errorf("sepia = %s, light = %s", acc["sepia"], acc["light"])
			}
			dd, dl := deltaEOK(t, acc["dark"], p.handoffDark), deltaEOK(t, acc["light"], p.handoffLight)
			t.Logf("%s: dark %s (ΔE_OK %.3f from %s), light %s (ΔE_OK %.3f from %s)",
				p.name, acc["dark"], dd, p.handoffDark, acc["light"], dl, p.handoffLight)
			if dd > 0.02 {
				t.Errorf("dark ΔE_OK = %.3f, want <= 0.02", dd)
			}
			if dl > 0.045 {
				t.Errorf("light ΔE_OK = %.3f, want <= 0.045", dl)
			}
			for _, th := range Themes {
				a := acc[th.Name]
				if c := Contrast(th.OnAccent, a); c < 4.5 {
					t.Errorf("%s: label on %s = %.2f, want >= 4.5", th.Name, a, c)
				}
				if c := Contrast(a, th.Ground); c < 3 {
					t.Errorf("%s: %s on ground = %.2f, want >= 3", th.Name, a, c)
				}
				if c := Contrast(a, th.Surface); c < 3 {
					t.Errorf("%s: %s on surface = %.2f, want >= 3", th.Name, a, c)
				}
				_, c, _ := toOKLCH(mustHex(t, a))
				if c > 0.10+0.004 {
					t.Errorf("%s: chroma %.3f over the cap", th.Name, c)
				}
			}
		})
	}
}

func mustHex(t *testing.T, s string) (int, int, int) {
	t.Helper()
	r, g, b, ok := parseHex(s)
	if !ok {
		t.Fatalf("bad hex %q", s)
	}
	return r, g, b
}

func TestAccentsNone(t *testing.T) {
	for _, tint := range []string{"#808080", "#ffffff", "#000000", "#7a7f7b", "bogus", ""} {
		if acc, ok := Accents(tint); ok || acc != nil {
			t.Errorf("Accents(%q) = %v, %v; want none", tint, acc, ok)
		}
	}
}

func TestContrast(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want float64
	}{
		{"#000000", "#ffffff", 21},
		{"#ffffff", "#ffffff", 1},
		{"#777777", "#ffffff", 4.48},
	} {
		if got := Contrast(c.a, c.b); math.Abs(got-c.want) > 0.01 {
			t.Errorf("Contrast(%s, %s) = %.3f, want %.2f", c.a, c.b, got, c.want)
		}
	}
	if Contrast("nope", "#fff") != 0 {
		t.Error("bad input should give 0")
	}
}

func solid(w, h int, c color.RGBA) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetRGBA(x, y, c)
		}
	}
	return img
}

func fill(img *image.RGBA, r image.Rectangle, c color.RGBA) {
	for y := r.Min.Y; y < r.Max.Y; y++ {
		for x := r.Min.X; x < r.Max.X; x++ {
			img.SetRGBA(x, y, c)
		}
	}
}

func TestTint(t *testing.T) {
	teal := color.RGBA{0x2c, 0x66, 0x60, 255}
	red := color.RGBA{200, 30, 30, 255}
	white := color.RGBA{255, 255, 255, 255}
	black := color.RGBA{0, 0, 0, 255}

	t.Run("solid", func(t *testing.T) {
		hex, ok := Tint(solid(20, 20, teal))
		if !ok || hex != "#2c6660" {
			t.Errorf("Tint = %q, %v", hex, ok)
		}
	})
	t.Run("black and white only", func(t *testing.T) {
		img := solid(20, 20, white)
		fill(img, image.Rect(0, 0, 20, 10), black)
		if hex, ok := Tint(img); ok {
			t.Errorf("Tint = %q, want none", hex)
		}
	})
	t.Run("small logo on white", func(t *testing.T) {
		img := solid(100, 100, white)
		fill(img, image.Rect(0, 0, 30, 10), red) // 3 %
		if hex, ok := Tint(img); ok {
			t.Errorf("Tint = %q, want none", hex)
		}
	})
	t.Run("majority wins", func(t *testing.T) {
		img := solid(100, 100, teal)
		fill(img, image.Rect(0, 0, 100, 40), red)
		hex, ok := Tint(img)
		if !ok || hex != "#2c6660" {
			t.Errorf("Tint = %q, %v; want the 60 %% teal", hex, ok)
		}
	})
	t.Run("tie goes to the smaller bucket", func(t *testing.T) {
		img := solid(100, 100, teal)
		fill(img, image.Rect(0, 0, 100, 50), red)
		hex, ok := Tint(img)
		if !ok || hex != "#2c6660" { // teal's bucket (2,6,6) sorts below red's (12,1,1)
			t.Errorf("Tint = %q, %v", hex, ok)
		}
	})
	t.Run("mean of the bucket", func(t *testing.T) {
		img := solid(10, 10, color.RGBA{0x40, 0x80, 0xc0, 255})
		fill(img, image.Rect(0, 0, 10, 5), color.RGBA{0x4f, 0x8f, 0xcf, 255})
		hex, ok := Tint(img)
		if !ok || hex != "#4888c8" {
			t.Errorf("Tint = %q, %v; want #4888c8", hex, ok)
		}
	})
}

// TestThemeTokens keeps Themes equal to the CSS design tokens.
func TestThemeTokens(t *testing.T) {
	css, err := os.ReadFile("../web/static/css/tokens.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, th := range Themes {
		sel := `[data-theme="` + th.Name + `"]`
		i := strings.Index(string(css), sel)
		if th.Name == "dark" {
			i = strings.Index(string(css), `:root, `+sel)
		}
		if i < 0 {
			t.Fatalf("no block for %s", sel)
		}
		rest := string(css)[i:]
		end := strings.Index(rest, "}")
		block := rest[:end]
		for prop, want := range map[string]string{"--ground": th.Ground, "--surface": th.Surface, "--on-cover-accent": th.OnAccent} {
			m := regexp.MustCompile(`(?m)^\s*` + regexp.QuoteMeta(prop) + `:\s*(#[0-9a-fA-F]{6})\s*;`).FindStringSubmatch(block)
			if m == nil {
				t.Errorf("%s: no %s in tokens.css", th.Name, prop)
				continue
			}
			if !strings.EqualFold(m[1], want) {
				t.Errorf("%s: %s = %s in tokens.css, Themes has %s", th.Name, prop, m[1], want)
			}
		}
	}
}
