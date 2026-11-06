package static

import (
	"regexp"
	"strings"
	"testing"
)

// fullScreenLayers are the classes whose rules say "position: fixed; inset: 0" in static/css, each named in
// pages-desktop.css under html[data-shell="desktop"] so it starts below the bar (decision 0023).
var fullScreenLayers = []string{".ts-player", ".ts-readerpage", ".ts-eyesfree", ".ts-sheet-layer"}

// stickyLayers are rules that hug the top edge (top: 0) and are moved below the bar as well.
var stickyLayers = []string{".ts-nav-side", ".ts-nav-rail", ".ts-offline", ".ts-prefs__frame > .ts-prefs__nav"}

var ruleRe = regexp.MustCompile(`([^{}]+)\{([^{}]*)\}`)

// TestDeskBarOffsets: every rule that pins a layer over the whole window is on the list, and each one on the
// list is moved below the bar for the desktop shell. A new full-screen layer fails here until it is added.
func TestDeskBarOffsets(t *testing.T) {
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range s.Names() {
		if !strings.HasPrefix(name, "css/") || !strings.HasSuffix(name, ".css") || name == "css/pages-desktop.css" {
			continue
		}
		a, _ := s.Asset(name)
		css := regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(string(a.Body), "")
		for _, m := range ruleRe.FindAllStringSubmatch(css, -1) {
			if !strings.Contains(m[2], "position: fixed") || !regexp.MustCompile(`inset: 0[;\s]|inset: 0$`).MatchString(m[2]) {
				continue
			}
			selector := strings.TrimSpace(m[1])
			if i := strings.LastIndex(selector, "@media"); i >= 0 {
				selector = strings.TrimSpace(selector[i:])
			}
			listed := false
			for _, class := range fullScreenLayers {
				if strings.HasPrefix(selector, class) {
					listed = true
				}
			}
			if !listed {
				t.Errorf("%s: %q is fixed over the whole window; add it to fullScreenLayers and to pages-desktop.css", name, selector)
			}
		}
	}
	desktop, _ := s.Asset("css/pages-desktop.css")
	css := string(desktop.Body)
	for _, class := range append(append([]string{}, fullScreenLayers...), stickyLayers...) {
		if !strings.Contains(css, `html[data-shell="desktop"] `+class) {
			t.Errorf("pages-desktop.css does not move %s below the bar", class)
		}
	}
	if !strings.Contains(css, ".ts-side__brand { display: none; }") {
		t.Error("the sidebar brand must go: the logo is in the bar")
	}
}

// TestDeskBudgetSplit: the two desktop-only scripts are counted apart from the browser total, and nothing
// else is in the split.
func TestDeskBudgetSplit(t *testing.T) {
	if jsDesktopTotalGzip != 4096 {
		t.Errorf("jsDesktopTotalGzip is %d, the approved ceiling is 4096", jsDesktopTotalGzip)
	}
	if len(jsDesktopFiles) != 2 || !jsDesktopFiles["desktop.js"] || !jsDesktopFiles["desktop-shell.js"] {
		t.Errorf("jsDesktopFiles = %v", jsDesktopFiles)
	}
	if got := jsBudgets["desktop-shell.js"].Gzip; got != 2816 {
		t.Errorf("desktop-shell.js row is %d, approved 2816", got)
	}
	s, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	for name := range jsDesktopFiles {
		a, ok := s.Asset("js/" + name)
		if !ok {
			t.Fatalf("js/%s missing", name)
		}
		if strings.Contains(string(a.Body), "export ") && name == "desktop.js" {
			t.Errorf("js/%s should stay a plain module with no exports", name)
		}
	}
}
