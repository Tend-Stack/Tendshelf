package static

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func readCSS(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("css", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var cssComment = regexp.MustCompile(`(?s)/\*.*?\*/`)

func stripComments(s string) string { return cssComment.ReplaceAllString(s, "") }

// blocks returns the body of every block whose prelude starts with start (for example
// "@media (hover: none)"), found by brace matching.
func blocks(css, start string) []string {
	var out []string
	for i := 0; ; {
		j := strings.Index(css[i:], start)
		if j < 0 {
			return out
		}
		j += i
		open := strings.Index(css[j:], "{")
		if open < 0 {
			return out
		}
		depth, k := 0, j+open
		for ; k < len(css); k++ {
			if css[k] == '{' {
				depth++
			} else if css[k] == '}' {
				depth--
				if depth == 0 {
					break
				}
			}
		}
		out = append(out, css[j+open+1:k])
		i = k
	}
}

// rules returns (prelude, body) for each rule that has declarations. Rules nested in @media,
// @starting-style or @keyframes appear with their own prelude.
func rules(css string) [][2]string {
	var out [][2]string
	var stack []string
	var buf strings.Builder
	for _, c := range stripComments(css) {
		switch c {
		case '{':
			stack = append(stack, strings.TrimSpace(buf.String()))
			buf.Reset()
		case '}':
			if len(stack) > 0 {
				if body := buf.String(); strings.TrimSpace(body) != "" {
					out = append(out, [2]string{stack[len(stack)-1], body})
				}
				stack = stack[:len(stack)-1]
			}
			buf.Reset()
		default:
			buf.WriteRune(c)
		}
	}
	return out
}

// TestCSSBudget bounds what a page costs. A page loads the core (tokens, fonts, bundle, utilities,
// motion), the player's file (10 KB) plus its own page files (8 KB each), so a core limit and a per-file limit on each pages-*.css bound
// every page without a total that grows with each new page.
func TestCSSBudget(t *testing.T) {
	entries, err := os.ReadDir("css")
	if err != nil {
		t.Fatal(err)
	}
	core := map[string]bool{"tokens.css": true, "fonts.css": true, "bundle.css": true, "utilities.css": true, "motion.css": true}
	var coreTotal int
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".css") {
			continue
		}
		n := len(readCSS(t, name))
		switch {
		case core[name]:
			coreTotal += n
		case name == "player.css":
			// The player's own file, loaded on every page that has the shell.
			if n > 10*1024 {
				t.Errorf("%s %d bytes, budget 10 KB", name, n)
			}
		case strings.HasPrefix(name, "pages-"):
			if n > 8*1024 {
				t.Errorf("%s %d bytes, budget 8 KB", name, n)
			}
		default:
			t.Errorf("%s is neither core nor a pages-*.css file; add it to the budget", name)
		}
	}
	if n := len(readCSS(t, "player.css")); n == 0 {
		t.Error("player.css is missing")
	}
	if coreTotal > 72*1024 {
		t.Errorf("core CSS %d bytes, budget 72 KB", coreTotal)
	}
	if n := len(readCSS(t, "utilities.css")); n > 6*1024 {
		t.Errorf("utilities.css %d bytes, budget 6 KB", n)
	}
	if n := len(readCSS(t, "motion.css")); n > 8*1024 {
		t.Errorf("motion.css %d bytes, budget 8 KB", n)
	}
}

func TestOurCSSUsesTokensOnly(t *testing.T) {
	colour := regexp.MustCompile(`#[0-9a-fA-F]{3,8}\b|\brgba?\(|\bhsla?\(`)
	for _, n := range []string{"utilities.css", "motion.css"} {
		css := stripComments(readCSS(t, n))
		if m := colour.FindString(css); m != "" {
			t.Errorf("%s: literal colour %q (use a token)", n, m)
		}
		if strings.Contains(css, "!important") {
			t.Errorf("%s: !important (the layers decide the cascade)", n)
		}
		if strings.Contains(css, "style=") {
			t.Errorf("%s: style=", n)
		}
	}
}

func TestUtilityPrefix(t *testing.T) {
	class := regexp.MustCompile(`\.([A-Za-z_][\w-]*)`)
	for _, n := range []string{"utilities.css", "motion.css"} {
		for _, r := range rules(readCSS(t, n)) {
			if strings.HasPrefix(r[0], "@") {
				continue
			}
			for _, m := range class.FindAllStringSubmatch(r[0], -1) {
				if !strings.HasPrefix(m[1], "ts-") && !strings.HasPrefix(m[1], "is-") {
					t.Errorf("%s: class .%s in %q does not start with ts- or is-", n, m[1], r[0])
				}
			}
		}
	}
}

// Every :hover rule of the handoff stylesheet needs its resting look put back on touch screens.
func TestHoverNoneCoversBundle(t *testing.T) {
	hover := regexp.MustCompile(`(\.ts-[\w-]+):hover`)
	want := map[string]bool{}
	for _, r := range rules(readCSS(t, "bundle.css")) {
		for _, m := range hover.FindAllStringSubmatch(r[0], -1) {
			want[m[1]] = true
		}
	}
	if len(want) < 17 {
		t.Fatalf("found only %d :hover selectors in bundle.css", len(want))
	}
	none := strings.Join(blocks(stripComments(readCSS(t, "motion.css")), "@media (hover: none)"), "\n")
	if none == "" {
		t.Fatal("no @media (hover: none) block in motion.css")
	}
	for c := range want {
		if !strings.Contains(none, c+":hover") {
			t.Errorf("motion.css @media (hover: none) does not reset %s:hover", c)
		}
	}
}

// Every animation outside the plain fades must stop or become a fade under prefers-reduced-motion.
func TestReducedMotionCoversKeyframes(t *testing.T) {
	bundle, motion := stripComments(readCSS(t, "bundle.css")), stripComments(readCSS(t, "motion.css"))
	reduce := strings.Join(append(blocks(bundle, "@media (prefers-reduced-motion: reduce)"),
		blocks(motion, "@media (prefers-reduced-motion: reduce)")...), "\n")
	if reduce == "" {
		t.Fatal("no reduced-motion block")
	}
	var reduceSelectors []string
	for _, r := range rules(reduce) {
		for _, s := range strings.Split(r[0], ",") {
			reduceSelectors = append(reduceSelectors, strings.Join(strings.Fields(s), " "))
		}
	}
	has := func(sel string) bool {
		for _, s := range reduceSelectors {
			if s == sel {
				return true
			}
		}
		return false
	}
	frames := regexp.MustCompile(`@keyframes (ts-[\w-]+)`)
	for _, css := range []string{bundle, motion} {
		for _, m := range frames.FindAllStringSubmatch(css, -1) {
			name := m[1]
			if strings.HasPrefix(name, "ts-fade-") {
				continue
			}
			use := regexp.MustCompile(`animation\s*:[^;}]*\b` + regexp.QuoteMeta(name) + `\b`)
			users := 0
			for _, r := range rules(css) {
				if !use.MatchString(r[1]) {
					continue
				}
				users++
				for _, s := range strings.Split(r[0], ",") {
					if sel := strings.Join(strings.Fields(s), " "); !has(sel) {
						t.Errorf("keyframes %s used by %q, not neutralised under prefers-reduced-motion", name, sel)
					}
				}
			}
			if users == 0 {
				t.Errorf("keyframes %s is not used by any animation", name)
			}
		}
	}
}

// P0b's skip link once shared .ts-skip with the handoff's skip-back and skip-forward button.
func TestNoSkipCollision(t *testing.T) {
	skip := regexp.MustCompile(`\.ts-skip[\w-]*`)
	got := map[string]bool{}
	for _, r := range rules(readCSS(t, "bundle.css")) {
		for _, m := range skip.FindAllString(r[0], -1) {
			got[m] = true
		}
	}
	if len(got) != 2 || !got[".ts-skip"] || !got[".ts-skip__num"] {
		t.Errorf("bundle.css .ts-skip selectors = %v, want only the handoff's .ts-skip and .ts-skip__num", got)
	}
	for _, r := range rules(readCSS(t, "bundle.css")) {
		if strings.Contains(r[0], ".ts-skip") && strings.Contains(r[0], ":focus-visible") {
			t.Errorf("a skip-link rule is back in bundle.css: %q", r[0])
		}
	}
}

// jsBudgets lists every script with its limit in raw bytes. A script that is not listed fails, so a
// new file needs a deliberate budget. player.js may grow to 28 KB in P1c-5.
var jsBudgets = map[string]int{"shell.js": 8 * 1024, "player.js": 20 * 1024}

func TestJSBudget(t *testing.T) {
	entries, err := os.ReadDir("js")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".js") {
			continue
		}
		seen[name] = true
		limit, ok := jsBudgets[name]
		if !ok {
			t.Errorf("js/%s has no budget; add it to jsBudgets", name)
			continue
		}
		b, err := os.ReadFile("js/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if len(b) > limit {
			t.Errorf("js/%s %d bytes, budget %d", name, len(b), limit)
		}
	}
	for name := range jsBudgets {
		if !seen[name] {
			t.Errorf("js/%s is budgeted but missing", name)
		}
	}
}
