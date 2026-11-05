package static

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// brMixRe matches the one computed shape pages-reader.css uses for a highlight wash:
// `color-mix(in srgb, var(--x) N%, transparent)`, which is the colour of --x at N percent opacity.
var brMixRe = regexp.MustCompile(`^color-mix\(in srgb, var\((--[a-z-]+)\) (\d+)%, transparent\)$`)

// brWash reads a highlight wash: a plain hex, a var() of one, or the color-mix above. Any other shape
// fails the test, so a new shape is noticed instead of skipped.
func brWash(t *testing.T, th b1Theme, value string) b1RGBA {
	t.Helper()
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "var(") {
		return brWash(t, th, th.get(t, strings.TrimSuffix(strings.TrimPrefix(value, "var("), ")")))
	}
	if m := brMixRe.FindStringSubmatch(value); m != nil {
		c := b1Colour(t, th.get(t, m[1]))
		pct, _ := strconv.Atoi(m[2])
		c.a = float64(pct) / 100
		return c
	}
	return b1Colour(t, value)
}

// brRule returns the declarations of the [data-colour="x"] rule in pages-reader.css.
func brRule(t *testing.T, css, colour string) string {
	t.Helper()
	sel := `[data-colour="` + colour + `"] {`
	i := strings.Index(css, sel)
	if i < 0 {
		t.Fatalf("pages-reader.css: no rule for %s", sel)
	}
	rest := css[i+len(sel):]
	return rest[:strings.Index(rest, "}")]
}

// TestBRContrast holds the reader's ink to the contrast text needs on every highlight colour in every theme
// (4.5:1, or 7:1 in the contrast theme), on each ground the reader text can sit on, and on the selection
// wash. Yellow is the base --mark; green, blue and pink are the rules of pages-reader.css.
func TestBRContrast(t *testing.T) {
	css := stripComments(readCSS(t, "pages-reader.css"))
	decl := regexp.MustCompile(`--mark:\s*([^;]+);`)
	for _, th := range b1Themes(t) {
		washes := map[string]b1RGBA{
			"yellow":    brWash(t, th, th.get(t, "--mark")),
			"selection": brWash(t, th, th.get(t, "--selection")),
		}
		for _, colour := range []string{"green", "blue", "pink"} {
			m := decl.FindStringSubmatch(brRule(t, css, colour))
			if m == nil {
				t.Fatalf("%s: no --mark in the %s rule", th.name, colour)
			}
			washes[colour] = brWash(t, th, m[1])
		}
		for name, wash := range washes {
			for _, bgName := range []string{"--ground", "--surface", "--raised"} {
				bg := b1Colour(t, th.get(t, bgName))
				under := wash.over(bg)
				ink := b1Colour(t, th.get(t, "--read-ink")).over(under)
				if got := b1Ratio(ink, under); got < th.min {
					t.Errorf("%s: --read-ink on the %s highlight over %s is %.2f:1, want %.1f:1", th.name, name, bgName, got, th.min)
				}
			}
		}
	}
}

// TestBRAvatarColours holds every avatar colour to 7:1 under its initials in the contrast theme (4.5:1 in
// the others), so the id a seeded person gets cannot move the audit.
func TestBRAvatarColours(t *testing.T) {
	css := stripComments(readCSS(t, "bundle.css"))
	plain := regexp.MustCompile(`(?m)^\.ts-avatar--(c\d)\s*\{\s*background:\s*([^;]+);`)
	over := regexp.MustCompile(`(?m)^\[data-theme="contrast"\] \.ts-avatar--(c\d)\s*\{\s*background:\s*([^;]+);`)
	initials := regexp.MustCompile(`\.ts-avatar \{[^}]*color:\s*(#[0-9a-f]{6})`).FindStringSubmatch(css)
	if initials == nil {
		t.Fatal("bundle.css: no initials colour on .ts-avatar")
	}
	fg := b1Colour(t, initials[1])
	n := 0
	for _, th := range b1Themes(t) {
		colours := map[string]string{}
		for _, m := range plain.FindAllStringSubmatch(css, -1) {
			colours[m[1]] = strings.TrimSpace(m[2])
		}
		if th.name == "contrast" {
			for _, m := range over.FindAllStringSubmatch(css, -1) {
				colours[m[1]] = strings.TrimSpace(m[2])
			}
		}
		for id, v := range colours {
			if strings.HasPrefix(v, "var(") { // var(--avatar-c5, #5a6b73): the token, else its fallback
				name := strings.TrimSpace(strings.SplitN(strings.TrimPrefix(v, "var("), ",", 2)[0])
				fallback := strings.TrimSuffix(strings.TrimSpace(strings.SplitN(v, ",", 2)[1]), ")")
				if tv, ok := th.vars[name]; ok {
					v = tv
				} else {
					v = fallback
				}
			}
			n++
			if got := b1Ratio(fg, b1Colour(t, v)); got < th.min {
				t.Errorf("%s: initials on .ts-avatar--%s %s are %.2f:1, want %.1f:1", th.name, id, v, got, th.min)
			}
		}
	}
	if n < 24 {
		t.Errorf("checked %d avatar colours, want 6 in each of 4 themes", n)
	}
}
