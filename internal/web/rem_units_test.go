package web

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Sizes in the stylesheets are rem, so the "Larger text" setting (a bigger root font size) scales type,
// spacing, controls and icons together. This test keeps px values from creeping back. A px value is
// allowed only as: a 1px or 2px hairline or border; anything in outline, box-shadow, --shadow-* or
// --focus-ring (rings and drop shadows); the pill radius token (9999px, "fully round"); or a media-query
// breakpoint (CSS pixels by design). Everything else must be written in rem.

var remunitsPx = regexp.MustCompile(`[0-9]*\.?[0-9]+px\b`)
var remunitsComment = regexp.MustCompile(`(?s)/\*.*?\*/`)

// remunitsStrip blanks comments out but keeps their length, so offsets stay valid.
func remunitsStrip(css string) string {
	return remunitsComment.ReplaceAllStringFunc(css, func(c string) string {
		return strings.Repeat(" ", len(c))
	})
}

// remunitsDisallowed returns the px values in css that are not on the allowed list, each with its
// declaration so a failure says where to look.
func remunitsDisallowed(css string) []string {
	css = remunitsStrip(css)
	var bad []string
	for _, loc := range remunitsPx.FindAllStringIndex(css, -1) {
		start := 0
		for _, sep := range []string{";", "{", "}"} {
			if i := strings.LastIndex(css[:loc[0]], sep); i+1 > start {
				start = i + 1
			}
		}
		decl := strings.TrimSpace(css[start:loc[0]])
		prop, _, _ := strings.Cut(decl, ":")
		prop = strings.TrimSpace(prop)
		value := css[loc[0] : loc[1]-2]
		switch {
		case strings.HasPrefix(prop, "@media"), strings.HasPrefix(prop, "@container"), strings.HasPrefix(prop, "@supports"):
			continue
		case strings.HasPrefix(prop, "outline"), strings.HasPrefix(prop, "box-shadow"),
			strings.HasPrefix(prop, "--shadow"), strings.HasPrefix(prop, "--focus-ring"):
			continue
		case prop == "--radius-pill" && value == "9999":
			continue
		}
		if n, err := strconv.ParseFloat(value, 64); err == nil && (n == 0 || n == 1 || n == 2) {
			continue
		}
		bad = append(bad, strings.TrimSpace(css[start:min(len(css), loc[1])])+" (px value "+value+"px)")
	}
	return bad
}

func TestRemUnitsStylesheetsHaveNoStrayPx(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("static", "css", "*.css"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no stylesheets found: %v", err)
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range remunitsDisallowed(string(b)) {
			t.Errorf("%s: %s; write sizes in rem (1rem = 16px)", filepath.Base(f), bad)
		}
	}
}

// The checker itself: what it lets through and what it rejects.
func TestRemUnitsCheckerRules(t *testing.T) {
	cases := []struct {
		name, css string
		bad       int
	}{
		{"font size in px", `.a { font-size: 15px; }`, 1},
		{"font shorthand", `.a { font: 600 15px/20px var(--f); }`, 2},
		{"gap in px", `.a { gap: 12px; }`, 1},
		{"fractional px", `.a { margin-left: -1.5px; }`, 1},
		{"leading dot", `.a { margin-left: .5px; }`, 1},
		{"rem is fine", `.a { font-size: 0.9375rem; padding: 1rem; }`, 0},
		{"hairline border", `.a { border: 1px solid var(--line); border-radius: 2px; }`, 0},
		{"focus ring", `.a { outline: 2px solid transparent; outline-offset: 3px; box-shadow: 0 0 0 4px red; }`, 0},
		{"shadow token", `:root { --shadow-float: 0 8px 24px #000; --focus-ring: 0 0 0 4px #fff; }`, 0},
		{"pill radius", `:root { --radius-pill: 9999px; }`, 0},
		{"media query", `@media (min-width: 768px) and (max-width: 1279px) { .a { gap: 1rem; } }`, 0},
		{"px inside a media block", `@media (min-width: 768px) { .a { width: 320px; } }`, 1},
		{"comment ignored", `/* was 15px */ .a { gap: 1rem; }`, 0},
	}
	for _, c := range cases {
		if got := remunitsDisallowed(c.css); len(got) != c.bad {
			t.Errorf("%s: %d stray values %v, want %d", c.name, len(got), got, c.bad)
		}
	}
}
