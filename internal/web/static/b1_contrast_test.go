package static

import (
	"math"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// b1Theme is one theme block of tokens.css with its custom properties.
type b1Theme struct {
	name    string
	prelude string
	vars    map[string]string
	base    map[string]string // the dark block, which is also :root: what a theme does not set
	min     float64           // the ratio text needs on the grounds: 4.5 (AA) or 7 (the contrast theme)
}

var b1VarLine = regexp.MustCompile(`(?m)^\s*(--[a-z0-9-]+):\s*([^;]+);`)

// b1Themes reads the four theme blocks of tokens.css.
func b1Themes(t *testing.T) []b1Theme {
	t.Helper()
	css := stripComments(readCSS(t, "tokens.css"))
	themes := []b1Theme{
		{name: "dark", prelude: `:root, [data-theme="dark"] {`, min: 4.5},
		{name: "light", prelude: `[data-theme="light"] {`, min: 4.5},
		{name: "sepia", prelude: `[data-theme="sepia"] {`, min: 4.5},
		{name: "contrast", prelude: `[data-theme="contrast"] {`, min: 7},
	}
	for i := range themes {
		body := blocks(css, themes[i].prelude)
		if len(body) != 1 {
			t.Fatalf("theme %s: want one block, found %d", themes[i].name, len(body))
		}
		themes[i].vars = map[string]string{}
		for _, m := range b1VarLine.FindAllStringSubmatch(body[0], -1) {
			themes[i].vars[m[1]] = strings.TrimSpace(m[2])
		}
	}
	for i := range themes {
		themes[i].base = themes[0].vars
	}
	return themes
}

// get resolves a token, following var() references and falling back to the dark block.
func (th b1Theme) get(t *testing.T, name string) string {
	t.Helper()
	v, ok := th.vars[name]
	if !ok {
		v, ok = th.base[name]
	}
	if !ok {
		t.Fatalf("theme %s: %s is not defined", th.name, name)
	}
	if strings.HasPrefix(v, "var(") {
		return th.get(t, strings.TrimSuffix(strings.TrimPrefix(v, "var("), ")"))
	}
	return v
}

type b1RGBA struct{ r, g, b, a float64 }

// b1Colour parses #rgb, #rrggbb and #rrggbbaa.
func b1Colour(t *testing.T, s string) b1RGBA {
	t.Helper()
	s = strings.TrimPrefix(strings.TrimSpace(s), "#")
	if len(s) == 3 {
		s = string([]byte{s[0], s[0], s[1], s[1], s[2], s[2]})
	}
	if len(s) != 6 && len(s) != 8 {
		t.Fatalf("not a hex colour: %q", s)
	}
	n := func(i int) float64 {
		v, err := strconv.ParseUint(s[i:i+2], 16, 8)
		if err != nil {
			t.Fatal(err)
		}
		return float64(v)
	}
	c := b1RGBA{n(0), n(2), n(4), 1}
	if len(s) == 8 {
		c.a = n(6) / 255
	}
	return c
}

// over composites c on an opaque background.
func (c b1RGBA) over(bg b1RGBA) b1RGBA {
	return b1RGBA{c.r*c.a + bg.r*(1-c.a), c.g*c.a + bg.g*(1-c.a), c.b*c.a + bg.b*(1-c.a), 1}
}

func (c b1RGBA) luminance() float64 {
	f := func(v float64) float64 {
		v /= 255
		if v <= 0.04045 {
			return v / 12.92
		}
		return math.Pow((v+0.055)/1.055, 2.4)
	}
	return 0.2126*f(c.r) + 0.7152*f(c.g) + 0.0722*f(c.b)
}

func b1Ratio(a, b b1RGBA) float64 {
	la, lb := a.luminance(), b.luminance()
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

// TestB1TokenContrast holds the colour tokens to the audit's thresholds in every theme: ink on the three
// backgrounds needs 4.5:1 (7:1 in the contrast theme); the strong line and the focus ring need 3:1. A value
// that is not a plain hex colour (a color-mix, a gradient) is skipped: it cannot be resolved without a browser.
func TestB1TokenContrast(t *testing.T) {
	for _, th := range b1Themes(t) {
		for _, bgName := range []string{"--ground", "--surface", "--raised"} {
			bg := b1Colour(t, th.get(t, bgName))
			for _, ink := range []string{"--ink", "--ink-muted", "--ink-subtle", "--accent", "--success", "--warning", "--danger"} {
				v := th.get(t, ink)
				if !strings.HasPrefix(v, "#") {
					continue
				}
				if got := b1Ratio(b1Colour(t, v).over(bg), bg); got < th.min {
					t.Errorf("%s: %s %s on %s %s is %.2f:1, want %.1f:1", th.name, ink, v, bgName, th.get(t, bgName), got, th.min)
				}
			}
			if got := b1Ratio(b1Colour(t, th.get(t, "--line-strong")).over(bg), bg); got < 3 {
				t.Errorf("%s: --line-strong on %s is %.2f:1, want 3:1", th.name, bgName, got)
			}
			// The focus ring is two box-shadow rings; the outer one is what shows against the page.
			parts := strings.Split(th.get(t, "--focus-ring"), ",")
			outer := strings.Fields(parts[len(parts)-1])
			colour := outer[len(outer)-1]
			if got := b1Ratio(b1Colour(t, colour).over(bg), bg); got < 3 {
				t.Errorf("%s: the focus ring %s on %s is %.2f:1, want 3:1", th.name, colour, bgName, got)
			}
		}
	}
}

// TestB1AvatarInitials holds the slate avatar in the contrast theme to 7:1 under its cream initials.
func TestB1AvatarInitials(t *testing.T) {
	for _, th := range b1Themes(t) {
		if th.name != "contrast" {
			continue
		}
		bg := b1Colour(t, th.get(t, "--avatar-c5"))
		if got := b1Ratio(b1Colour(t, "#f1e8da"), bg); got < 7 {
			t.Errorf("avatar initials on --avatar-c5 are %.2f:1, want 7:1", got)
		}
	}
	if !strings.Contains(readCSS(t, "bundle.css"), "var(--avatar-c5, #5a6b73)") {
		t.Error("bundle.css: .ts-avatar--c5 does not read --avatar-c5")
	}
}

// TestB1FocusRingKeptOnCurrentItems holds the rule that a marker drawn as a box-shadow must not replace the
// ring: each current or selected item has a :focus-visible rule that lists both.
func TestB1FocusRingKeptOnCurrentItems(t *testing.T) {
	for file, selectors := range map[string][]string{
		"bundle.css":         {`.ts-side__item[aria-current="page"]:focus-visible`, `.ts-tabs__item[aria-selected="true"]:focus-visible`, `.ts-mode__item[aria-checked="true"]:focus-visible`},
		"pages-book.css":     {`.ts-tabs__item.is-home:focus-visible`},
		"pages-settings.css": {`.ts-prefs__item[aria-current="true"]:focus-visible`},
		"player-bubble.css":  {`.ts-player-slot [data-ts-toggle]:focus-visible`},
	} {
		css := stripComments(readCSS(t, file))
		for _, sel := range selectors {
			i := strings.Index(css, sel)
			if i < 0 {
				t.Errorf("%s: no rule for %s", file, sel)
				continue
			}
			if rule := css[i : i+strings.Index(css[i:], "}")]; !strings.Contains(rule, "var(--focus-ring)") {
				t.Errorf("%s: %s does not keep --focus-ring", file, sel)
			}
		}
	}
}
