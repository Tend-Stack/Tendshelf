package static

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Guards for the native-feel rules (decision 0011): selection is scoped, every control shows a
// pressed state, focus rings are keyboard-only, fields never zoom iOS, fixed bars clear the safe
// areas, inner scrollers contain their overscroll and themes set color-scheme.

// allCSS returns every source stylesheet as name to text.
func allCSS(t *testing.T) map[string]string {
	t.Helper()
	files, err := filepath.Glob("css/*.css")
	if err != nil || len(files) == 0 {
		t.Fatalf("no stylesheets found: %v", err)
	}
	out := map[string]string{}
	for _, f := range files {
		out[filepath.Base(f)] = readCSS(t, filepath.Base(f))
	}
	return out
}

// parts splits a selector list on top-level commas.
func parts(prelude string) []string {
	var out []string
	depth, start := 0, 0
	for i, c := range prelude {
		switch c {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ',':
			if depth == 0 {
				out = append(out, strings.TrimSpace(prelude[start:i]))
				start = i + 1
			}
		}
	}
	return append(out, strings.TrimSpace(prelude[start:]))
}

func TestNativeSelectScoped(t *testing.T) {
	broad := map[string]bool{"html": true, "body": true, "*": true, "input": true, "textarea": true, "select": true, "[contenteditable]": true}
	for name, css := range allCSS(t) {
		for _, r := range rules(css) {
			for _, p := range parts(r[0]) {
				if broad[p] && regexp.MustCompile(`(?:^|[\s;])user-select:\s*none`).MatchString(r[1]) {
					t.Errorf("%s: %q sets user-select: none; fields and content must stay selectable", name, p)
				}
				if (p == "html" || p == "body") && regexp.MustCompile(`overflow(-[xy])?:\s*hidden`).MatchString(r[1]) {
					t.Errorf("%s: %q sets overflow: hidden; the document scrolls", name, p)
				}
			}
		}
	}
	util := readCSS(t, "utilities.css")
	var textRule, htmlRule bool
	for _, r := range rules(util) {
		ps := parts(r[0])
		for _, p := range ps {
			if p == "input" || p == "textarea" || p == "[contenteditable]" {
				textRule = strings.Contains(r[1], "user-select: text") && strings.Contains(r[1], "-webkit-user-select: text")
			}
			if p == "html" && strings.Contains(r[1], "overscroll-behavior-y: none") {
				htmlRule = true
			}
		}
	}
	if !textRule {
		t.Error("utilities.css: input, textarea and [contenteditable] must restore user-select: text (with the -webkit- prefix)")
	}
	if !htmlRule {
		t.Error("utilities.css: html needs overscroll-behavior-y: none")
	}
	// The one allowed overflow lock is a modal dialog, because Safari honours overscroll-behavior only on boxes that scroll.
	if !strings.Contains(stripComments(util), "html:has(dialog:modal) { overflow: hidden; }") {
		t.Error("utilities.css: the page must lock while a modal dialog is open")
	}
}

func TestNativePressStates(t *testing.T) {
	bundle := readCSS(t, "bundle.css")
	classes := map[string]bool{}
	for _, r := range rules(bundle) {
		if !strings.HasPrefix(r[0], ".ts-btn:focus-visible") {
			continue
		}
		for _, m := range regexp.MustCompile(`\.(ts-[a-z0-9_-]+):focus-visible`).FindAllStringSubmatch(r[0], -1) {
			classes[m[1]] = true
		}
	}
	for _, c := range strings.Fields("ts-option ts-chapter ts-selmenu__item ts-player-bar__item ts-mode__item") {
		classes[c] = true
	}
	for _, c := range strings.Fields("ts-switch ts-input ts-select ts-check") { // their state is the feedback
		delete(classes, c)
	}
	if len(classes) < 8 {
		t.Fatalf("found only %d interactive classes in the focus group; the parser is off", len(classes))
	}
	var all []string
	for _, n := range []string{"bundle.css", "utilities.css", "motion.css"} {
		for _, r := range rules(readCSS(t, n)) {
			all = append(all, r[0])
		}
	}
	joined := strings.Join(all, "\n")
	for c := range classes {
		if !strings.Contains(joined, "."+c+":active") {
			t.Errorf(".%s has no :active rule; every control shows a pressed state", c)
		}
	}
	for _, r := range rules(readCSS(t, "motion.css")) {
		if strings.Contains(r[0], ":active") && !strings.Contains(r[0], ":not(") && !strings.Contains(r[1], "transition-duration: 0s") && !strings.Contains(r[1], "transform: none") {
			t.Errorf("motion.css %q: a press state must start instantly (transition-duration: 0s)", r[0])
		}
	}
}

func TestNativeHoverKeepsActive(t *testing.T) {
	stateLayer := regexp.MustCompile(`\.ts-(option|player-bar__item|selmenu__item|chapter|rail__item|side__item):hover`)
	hn := blocks(readCSS(t, "motion.css"), "@media (hover: none)")
	if len(hn) != 1 {
		t.Fatalf("want one (hover: none) block, got %d", len(hn))
	}
	n := 0
	for _, r := range rules(hn[0]) {
		if stateLayer.MatchString(r[0]) && strings.Contains(r[1], "background") {
			n++
			if !strings.Contains(r[0], ":active") {
				t.Errorf("(hover: none) %q resets the background without :active: the pressed state would vanish", r[0])
			}
		}
	}
	if n < 4 {
		t.Errorf("checked %d state-layer resets, want 4", n)
	}
}

func TestNativeFocusVisible(t *testing.T) {
	bareFocus := regexp.MustCompile(`:focus(?:[^-\w]|$)`)
	noOutline := regexp.MustCompile(`outline:\s*(none|0)\b`)
	for name, css := range allCSS(t) {
		for _, r := range rules(css) {
			for _, p := range parts(r[0]) {
				if bareFocus.MatchString(p) && p != ".ts-frame__main:focus" {
					t.Errorf("%s: %q uses :focus; keyboard rings use :focus-visible", name, p)
				}
				if noOutline.MatchString(r[1]) && !strings.Contains(p, ":focus-visible") && p != ".ts-main" && p != ".ts-frame__main:focus" && !strings.HasPrefix(r[0], "@") {
					t.Errorf("%s: %q removes the outline outside :focus-visible", name, p)
				}
			}
		}
	}
}

func TestNativeFieldSize(t *testing.T) {
	field := regexp.MustCompile(`textarea|\.ts-input|\.ts-select|input\[type=`)
	size := regexp.MustCompile(`font(?:-size)?:\s*(?:[0-9]{3}\s+)?([0-9.]+)(rem|px)`)
	for name, css := range allCSS(t) {
		for _, r := range rules(css) {
			if !field.MatchString(r[0]) || strings.Contains(r[0], "::placeholder") {
				continue
			}
			for _, m := range size.FindAllStringSubmatch(r[1], -1) {
				v, _ := strconv.ParseFloat(m[1], 64)
				if m[2] == "rem" {
					v *= 16
				}
				if v < 16 {
					t.Errorf("%s: %q sets %s%s; fields below 16 px make iOS zoom on focus", name, r[0], m[1], m[2])
				}
			}
		}
	}
}

func TestNativeSafeAreas(t *testing.T) {
	want := map[string][]string{
		".ts-nav-tabbar":   {"top-none", "bottom", "left", "right"},
		".ts-player-slot":  {"bottom", "left", "right"},
		".ts-toasts":       {"bottom", "left", "right"},
		".ts-sheet":        {"bottom", "left", "right"},
		".ts-main":         {"top"},
		".ts-shell":        {"left", "right"},
		".ts-nav-rail":     {"top", "bottom"},
		".ts-nav-side":     {"top"},
		".ts-player__body": {"top", "bottom", "left", "right"},
		".ts-frame__main":  {"top", "bottom", "left", "right"},
	}
	css := allCSS(t)
	for sel, edges := range want {
		for _, edge := range edges {
			if edge == "top-none" {
				continue
			}
			found := false
			for _, text := range css {
				for _, r := range rules(text) {
					for _, p := range parts(r[0]) {
						if (p == sel || strings.HasSuffix(p, " "+sel)) && strings.Contains(r[1], "env(safe-area-inset-"+edge+")") {
							found = true
						}
					}
				}
			}
			if !found {
				t.Errorf("%s has no rule using env(safe-area-inset-%s)", sel, edge)
			}
		}
	}
}

func TestNativeOverscroll(t *testing.T) {
	scrolls := regexp.MustCompile(`overflow(-x|-y)?:\s*(auto|scroll)`)
	css := allCSS(t)
	var contained []string // preludes of rules that set overscroll-behavior
	for _, text := range css {
		for _, r := range rules(text) {
			if strings.Contains(r[1], "overscroll-behavior") {
				contained = append(contained, r[0])
			}
		}
	}
	elsewhere := func(sel string) bool {
		for _, p := range contained {
			if strings.Contains(p, sel) {
				return true
			}
		}
		return false
	}
	exempt := []string{".ts-player__body", ".ts-player__chapters", ".ts-sheet-layer--", ".ts-details-sheet"}
	for name, text := range css {
		for _, r := range rules(text) {
			if !scrolls.MatchString(r[1]) || strings.Contains(r[1], "overscroll-behavior") {
				continue
			}
			ex := ""
			for _, e := range exempt {
				if strings.Contains(r[0], e) {
					ex = e
				}
			}
			if ex == "" {
				t.Errorf("%s: %q scrolls but does not set overscroll-behavior", name, r[0])
			} else if ex == ".ts-sheet-layer--" || ex == ".ts-details-sheet" {
				if !elsewhere(".ts-sheet,") {
					t.Errorf("%s: sheets need overscroll-behavior: contain", name)
				}
			} else if !elsewhere(ex) {
				t.Errorf("%s: %s needs overscroll-behavior: contain in another rule", name, ex)
			}
		}
	}
}

func TestNativeColorScheme(t *testing.T) {
	tokens := readCSS(t, "tokens.css")
	want := map[string]string{`:root, [data-theme="dark"]`: "dark", `[data-theme="light"]`: "light", `[data-theme="sepia"]`: "light", `[data-theme="contrast"]`: "dark"}
	seen := 0
	for _, r := range rules(tokens) {
		if w, ok := want[r[0]]; ok {
			seen++
			if !strings.Contains(r[1], "color-scheme: "+w+";") {
				t.Errorf("tokens.css %s: want color-scheme: %s", r[0], w)
			}
		}
	}
	if seen != 4 {
		t.Errorf("found %d of 4 theme blocks", seen)
	}
	s := mustLoad(t)
	served := bodyOf(t, s, "css/tokens.css")
	for _, w := range []string{"color-scheme: light", "color-scheme: dark"} {
		if !strings.Contains(served, w) {
			t.Errorf("served tokens.css lacks %q", w)
		}
	}
	// The system blocks copy light (inside prefers-color-scheme: light) and contrast (inside prefers-contrast: more).
	for _, media := range []string{"@media (prefers-color-scheme: light)", "@media (prefers-contrast: more)"} {
		bs := blocks(served, media)
		if len(bs) == 0 {
			t.Errorf("served tokens.css has no %s block", media)
			continue
		}
		if !strings.Contains(bs[0], "color-scheme:") {
			t.Errorf("served system block %s lacks color-scheme", media)
		}
	}
}

// iOS applies :active only when a touch listener exists on the element or <body>; document counts in
// current Safari, and the listener that closes sheets by dragging is that one.
func TestNativeTouchListener(t *testing.T) {
	b, err := os.ReadFile("js/shell.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `document.addEventListener("touchstart"`) {
		t.Error("shell.js must keep a touchstart listener, or iOS never shows :active")
	}
}
