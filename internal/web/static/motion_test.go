package static

import (
	"os"
	"path"
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

// sentBytes is what the server sends to a client that accepts gzip: the gzip body, or the plain
// body when the asset is not compressed.
func sentBytes(a *Asset) int {
	if a.Gzip != nil {
		return len(a.Gzip)
	}
	return len(a.Body)
}

// cssBudget is one CSS file's limit in gzip bytes plus a raw-byte sanity ceiling, so the source can
// stay written for reading.
type cssBudget struct{ gzip, raw int }

// TestCSSBudget bounds what a page costs, in the gzip bytes of the built (served) asset. A page loads
// the core (tokens, fonts, bundle, utilities, motion), the player's file and its own page files, so a
// core limit and a per-file limit on each pages-*.css bound every page without a total that grows with
// each new page.
func TestCSSBudget(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	core := map[string]bool{"css/tokens.css": true, "css/fonts.css": true, "css/bundle.css": true, "css/utilities.css": true, "css/motion.css": true}
	single := map[string]cssBudget{
		"css/utilities.css": {2 * 1024, 24 * 1024},
		"css/motion.css":    {2 * 1024, 24 * 1024},
		// The player's own file, loaded on every page that has the shell; P1c-5 adds the full player.
		"css/player.css": {4 * 1024, 24 * 1024},
	}
	var coreGzip, coreRaw int
	for _, name := range s.Names() {
		if !strings.HasPrefix(name, "css/") {
			continue
		}
		a, _ := s.Asset(name)
		g, raw := sentBytes(a), len(a.Body)
		if core[name] {
			coreGzip += g
			coreRaw += raw
		}
		limit, ok := single[name]
		if !ok && strings.HasPrefix(path.Base(name), "pages-") {
			limit, ok = cssBudget{3 * 1024, 24 * 1024}, true
		}
		switch {
		case ok:
			if g > limit.gzip {
				t.Errorf("%s %d gzip bytes, budget %d (raw %d)", name, g, limit.gzip, raw)
			}
			if raw > limit.raw {
				t.Errorf("%s %d raw bytes, sanity ceiling %d", name, raw, limit.raw)
			}
		case !core[name]:
			t.Errorf("%s is neither core nor a pages-*.css file; add it to the budget", name)
		}
	}
	if coreGzip > 18*1024 {
		t.Errorf("core CSS %d gzip bytes, budget %d (raw %d)", coreGzip, 18*1024, coreRaw)
	}
	if coreRaw > 128*1024 {
		t.Errorf("core CSS %d raw bytes, sanity ceiling %d", coreRaw, 128*1024)
	}
	if a, ok := s.Asset("css/player.css"); !ok || len(a.Body) == 0 {
		t.Error("player.css is missing")
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

// jsBudgets lists every script with its limits: gzip bytes of the served file, and a raw-byte sanity
// ceiling. A script that is not listed fails, so a new file needs a deliberate budget; a listed
// script that does not exist yet is skipped.
var jsBudgets = map[string]struct{ Gzip, Raw int }{
	"shell.js": {4 * 1024, 64 * 1024},
	// The plan said 9 KB, sized from the squeezed file (7.2 KB). The version written for reading
	// (one statement per line, comments for the why) is 10.2 KB, so the limit is 11 KB; see the
	// AGENTS.md rule: report an overrun, never squeeze.
	// P1c-5b adds volume, fade and the toast host to player.js (12 KB), and the speed and sleep
	// sheets, the sleep timer, the bookmark toast and the B key to player-full.js (12 KB).
	"player.js":      {12 * 1024, 64 * 1024},
	"player-full.js": {12 * 1024, 64 * 1024},
	// W2: the waveform drawing. The plan said 2 KB; the version written for reading (comments, one
	// statement per line) is 3.0 KB, so the limit is 3.5 KB; see the AGENTS.md rule: report, never squeeze.
	"waveform.js": {3584, 16 * 1024},
}

// jsTotalGzip bounds all app scripts (js/*.js, not vendor/) together.
const jsTotalGzip = 30 * 1024 // 12 (player.js) + 4 (shell.js) + 12 (player-full.js) + 3 (waveform.js)

func TestJSBudget(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	total := 0
	for _, source := range s.Names() {
		if !strings.HasPrefix(source, "js/") || !strings.HasSuffix(source, ".js") {
			continue
		}
		name := strings.TrimPrefix(source, "js/")
		a, _ := s.Asset(source)
		seen[name] = true
		g := sentBytes(a)
		total += g
		limit, ok := jsBudgets[name]
		if !ok {
			t.Errorf("js/%s has no budget; add it to jsBudgets", name)
			continue
		}
		if g > limit.Gzip {
			t.Errorf("js/%s %d gzip bytes, budget %d (raw %d)", name, g, limit.Gzip, len(a.Body))
		}
		if len(a.Body) > limit.Raw {
			t.Errorf("js/%s %d raw bytes, sanity ceiling %d", name, len(a.Body), limit.Raw)
		}
	}
	for name := range jsBudgets {
		if !seen[name] {
			t.Logf("js/%s is budgeted but not present yet; skipped", name)
		}
	}
	if total > jsTotalGzip {
		t.Errorf("app JS %d gzip bytes, budget %d", total, jsTotalGzip)
	}
}
