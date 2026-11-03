package components

import (
	"bytes"
	"context"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/web/static"
)

func renderHead(t *testing.T, theme string) string {
	t.Helper()
	assets, err := static.Load()
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	if err := Head(Page{Title: "T", Theme: theme, Assets: assets}).Render(context.Background(), &buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestHeadNativeMeta(t *testing.T) {
	tests := []struct {
		theme, scheme string
		themeColors   []string
	}{
		{"", "dark light", []string{`media="(prefers-color-scheme: light)" content="#f4eee3"`, `media="(prefers-color-scheme: dark)" content="#14110e"`}},
		{"dark", "dark", []string{`<meta name="theme-color" content="#14110e"`}},
		{"light", "light", []string{`<meta name="theme-color" content="#f4eee3"`}},
		{"sepia", "light", []string{`<meta name="theme-color" content="#e6d7bc"`}},
		{"contrast", "dark", []string{`<meta name="theme-color" content="#000000"`}},
	}
	for _, tc := range tests {
		t.Run("theme="+tc.theme, func(t *testing.T) {
			html := renderHead(t, tc.theme)
			// Zoom must stay available (WCAG 1.4.4): the viewport is exactly this.
			if !strings.Contains(html, `<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover"`) {
				t.Error("viewport meta changed")
			}
			if strings.Contains(html, "user-scalable") || strings.Contains(html, "maximum-scale") {
				t.Error("zoom is disabled")
			}
			if !strings.Contains(html, `<meta name="color-scheme" content="`+tc.scheme+`"`) {
				t.Errorf("want color-scheme %q in %s", tc.scheme, html)
			}
			for _, w := range tc.themeColors {
				if !strings.Contains(html, w) {
					t.Errorf("missing %q", w)
				}
			}
			if got := strings.Count(html, `name="theme-color"`); (tc.theme == "") != (got == 2) || got == 0 {
				t.Errorf("theme-color metas: %d", got)
			}
			if !strings.Contains(html, `<meta name="format-detection" content="telephone=no, date=no, email=no, address=no"`) {
				t.Error("format-detection meta missing")
			}
		})
	}
}

// themeGround must equal --ground in each theme block of tokens.css (dark is the :root block).
func TestThemeGroundMatchesTokens(t *testing.T) {
	css, err := os.ReadFile("../static/css/tokens.css")
	if err != nil {
		t.Fatal(err)
	}
	for theme, want := range themeGround {
		start := `[data-theme="` + theme + `"] {`
		if theme == "dark" {
			start = `:root, [data-theme="dark"] {`
		}
		i := strings.Index(string(css), start)
		if i < 0 {
			t.Fatalf("no %s block in tokens.css", theme)
		}
		m := regexp.MustCompile(`--ground:\s*(#[0-9a-fA-F]{6})`).FindSubmatch(css[i:])
		if m == nil || strings.ToLower(string(m[1])) != want {
			t.Errorf("--ground of %s is %q, themeGround has %q", theme, m, want)
		}
	}
	if colorSchemeMeta("nope") != "dark" || themeColor("nope") != themeGround["dark"] {
		t.Error("unknown theme should fall back to dark")
	}
}
