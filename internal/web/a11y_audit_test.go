//go:build dev

package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// The accessibility audit (M3c, docs/plans/m3c-audit.md): every page of a11yPages, for each role that
// reaches it, at four widths and in four themes. The checks are our own JavaScript
// (internal/web/testdata/a11y/*.js) and Chromium's own accessibility tree. `make a11y` runs it; it
// needs TENDSHELF_TEST_BROWSER and writes .local/tests/a11y/<run>/findings.json.

// a11yWidths are the audit's viewports. "Mobile" is a width under 1280.
var a11yWidths = []struct{ W, H int }{{390, 844}, {768, 1024}, {1024, 768}, {1440, 900}}

// a11yChecks are the check ids a finding or an allowlist line may name.
var a11yChecks = []string{
	"lang", "title", "heading-skip", "nav-missing", "img-alt", "label-missing", "duplicate-id", "aria-ref-broken",
	"aria-hidden-focusable", "viewport-zoom", "autocomplete-missing", "live-region-missing",
	"h1-count", "main-count", "name-missing", "contrast-text", "load-error",
	// A2: targets, keyboard focus, non-text contrast, reflow.
	"target-size", "focus-not-visible", "focus-indicator-contrast", "focus-obscured", "focus-trap", "focus-unreachable",
	"contrast-control", "contrast-icon", "scroll-x", "reflow-320", "text-200-scroll-x", "text-200-clip",
}

func a11yFast() bool { return os.Getenv("TENDSHELF_A11Y_FAST") == "1" }

// a11yMatrix is the widths and themes of this run: the whole matrix, or the fast one (dark and
// contrast at 390 and 1440).
func a11yMatrix() (widths []struct{ W, H int }, themes []string) {
	if !a11yFast() {
		return a11yWidths, themes4()
	}
	return []struct{ W, H int }{a11yWidths[0], a11yWidths[3]}, []string{"dark", "contrast"}
}

func themes4() []string { return append([]string(nil), themes...) }

// a11yFinding is one violation. Mark is the planted fixture case the element belongs to (empty on real pages).
type a11yFinding struct {
	Check    string  `json:"check"`
	Page     string  `json:"page"`
	Role     string  `json:"role"`
	Theme    string  `json:"theme"`
	Width    int     `json:"width"`
	Selector string  `json:"selector"`
	Text     string  `json:"text"`
	Detail   string  `json:"detail"`
	Measured float64 `json:"measured,omitempty"`
	Required float64 `json:"required,omitempty"`
	// Wcag258 is set on target-size findings: whether the target still meets the WCAG 2.5.8 floor of 24 px.
	Wcag258 *bool  `json:"wcag258,omitempty"`
	Mark    string `json:"mark,omitempty"`
}

var (
	a11yFiles   = map[string]string{}
	a11yFilesMu sync.Mutex
)

// a11yRead returns a file of testdata/a11y.
func a11yRead(name string) string {
	a11yFilesMu.Lock()
	defer a11yFilesMu.Unlock()
	if s, ok := a11yFiles[name]; ok {
		return s
	}
	b, err := os.ReadFile(filepath.Join("testdata", "a11y", name))
	if err != nil {
		panic(err)
	}
	a11yFiles[name] = string(b)
	return a11yFiles[name]
}

// a11yScript runs a script of testdata/a11y (a function expression) with common.js in front and the
// options as its argument, and decodes its JSON result into out.
func a11yScript(tab *cdp.Tab, name string, options, out any) error {
	opts, err := json.Marshal(options)
	if err != nil {
		return err
	}
	expr := "(() => {\n" + a11yRead("common.js") + "\n" + a11yRead("colour.js") + "\nreturn (" + a11yRead(name) + "\n)(" + string(opts) + ");\n})()"
	return tab.TryEval(expr, out)
}

// a11yTheme sets the theme on <html> and settles the styles. It does not wait for animation frames
// (a tab behind the front one gets none): it forces a style and layout pass, finishes every running
// transition so a colour is read at its end value, and forces another pass.
func a11yTheme(tab *cdp.Tab, theme string) error {
	return tab.TryEval(`(() => {
  document.documentElement.setAttribute("data-theme", `+cdp.Quote(theme)+`);
  void document.body.offsetHeight;
  for (const animation of document.getAnimations()) {
    try { animation.finish(); } catch (error) { /* an infinite animation has no end */ }
  }
  void document.body.offsetHeight;
  return true;
})()`, nil)
}

// a11yLoad opens url and waits for it: the shell's "ts-ready" mark on a shell page, the load event
// and the fonts on any other. It returns the HTTP status of the document.
func a11yLoad(tab *cdp.Tab, url string, shell bool) (int, error) {
	if err := tab.TryEval(`window.__tsNav = 1`, nil); err != nil {
		return 0, err
	}
	var nav struct {
		ErrorText string `json:"errorText"`
	}
	if err := tab.Call("Page.navigate", map[string]any{"url": url}, &nav); err != nil {
		return 0, err
	}
	if nav.ErrorText != "" {
		return 0, fmt.Errorf("navigate %s: %s", url, nav.ErrorText)
	}
	ready := `window.__tsNav === undefined && document.readyState === "complete" && document.fonts.status === "loaded"`
	if shell {
		ready += ` && performance.getEntriesByName("ts-ready").length > 0`
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		var ok bool
		if err := tab.TryEval(`Boolean(`+ready+`)`, &ok); err == nil && ok {
			break
		}
		if time.Now().After(deadline) {
			return 0, fmt.Errorf("timed out waiting for %s", url)
		}
		time.Sleep(50 * time.Millisecond)
	}
	var status int
	if err := tab.TryEval(`performance.getEntriesByType("navigation")[0].responseStatus`, &status); err != nil {
		return 0, err
	}
	return status, nil
}

// a11yResult is what the checks found on one loaded page.
type a11yResult struct {
	Findings, Unmeasured []a11yFinding
	Measured             int
}

// a11yCheck runs every check on the page that is loaded: structure and the accessibility tree in dark,
// then text contrast in each of the themes. Page, Role and Width are left for the caller.
func a11yCheck(tab *cdp.Tab, shell bool, checkThemes []string) (a11yResult, error) {
	var res a11yResult
	if err := a11yTheme(tab, "dark"); err != nil {
		return res, err
	}
	var structure []a11yFinding
	if err := a11yScript(tab, "structure.js", map[string]any{"shell": shell}, &structure); err != nil {
		return res, fmt.Errorf("structure.js: %w", err)
	}
	ax, err := a11yAX(tab)
	if err != nil {
		return res, fmt.Errorf("accessibility tree: %w", err)
	}
	for _, f := range append(structure, ax...) {
		f.Theme = "dark"
		res.Findings = append(res.Findings, f)
	}
	for _, theme := range checkThemes {
		if err := a11yTheme(tab, theme); err != nil {
			return res, err
		}
		var c struct {
			Findings, Unmeasured []a11yFinding
			Measured             int
		}
		if err := a11yScript(tab, "contrast.js", map[string]any{"theme": theme}, &c); err != nil {
			return res, fmt.Errorf("contrast.js (%s): %w", theme, err)
		}
		for _, f := range c.Findings {
			f.Theme = theme
			res.Findings = append(res.Findings, f)
		}
		for _, f := range c.Unmeasured {
			f.Theme = theme
			res.Unmeasured = append(res.Unmeasured, f)
		}
		res.Measured += c.Measured
	}
	return res, nil
}

// a11yAllowEntry is one line of testdata/a11y/allow.txt: `<check> <pageKey|*> <selector> -- <reason>`.
type a11yAllowEntry struct {
	Check, Page, Selector, Reason string
	Line, Used                    int
}

// a11yParseAllow reads the allowlist text. It returns the entries and one error per bad line.
func a11yParseAllow(text string) (entries []*a11yAllowEntry, bad []string) {
	known := map[string]bool{}
	for _, c := range a11yChecks {
		known[c] = true
	}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		head, reason, ok := strings.Cut(line, " -- ")
		fields := strings.Fields(head)
		switch {
		case !ok || len(fields) != 3:
			bad = append(bad, fmt.Sprintf("line %d: want `<check> <pageKey|*> <selector> -- <reason>`", i+1))
		case len(strings.TrimSpace(reason)) < 10:
			bad = append(bad, fmt.Sprintf("line %d: the reason needs at least 10 characters", i+1))
		case !known[fields[0]]:
			bad = append(bad, fmt.Sprintf("line %d: unknown check %q", i+1, fields[0]))
		default:
			entries = append(entries, &a11yAllowEntry{Check: fields[0], Page: fields[1], Selector: fields[2], Reason: strings.TrimSpace(reason), Line: i + 1})
		}
	}
	return entries, bad
}

// a11yAllowed returns the entry that allows the finding, or nil. Role, theme and width do not matter.
func a11yAllowed(entries []*a11yAllowEntry, f a11yFinding) *a11yAllowEntry {
	for _, e := range entries {
		if e.Check == f.Check && (e.Page == "*" || e.Page == f.Page) && e.Selector == f.Selector {
			return e
		}
	}
	return nil
}

func TestA11yAllowlistFormat(t *testing.T) {
	_, bad := a11yParseAllow(a11yRead("allow.txt"))
	for _, b := range bad {
		t.Error(b)
	}
	// The parser itself: a good line, and one of each kind of bad line.
	good, bad := a11yParseAllow("# comment\n\ncontrast-text login .ts-x>a -- the logo is a trademark and exempt\n")
	if len(good) != 1 || len(bad) != 0 {
		t.Errorf("good line: %d entries, bad %v", len(good), bad)
	}
	for _, line := range []string{"contrast-text login .a", "contrast-text login .a -- short", "no-such-check login .a -- a long enough reason", "contrast-text login -- a long enough reason"} {
		if _, bad := a11yParseAllow(line + "\n"); len(bad) != 1 {
			t.Errorf("%q: bad lines %v, want 1", line, bad)
		}
	}
}

// a11yRun collects the findings of one audit run and writes them out.
type a11yRun struct {
	ID, Mode             string
	Pages, States        []string
	Findings, Unmeasured []a11yFinding
	Allowed              []a11yFinding
	Measured             int
	allow                []*a11yAllowEntry
}

func (r *a11yRun) add(base a11yFinding, fs []a11yFinding) {
	for _, f := range fs {
		f.Page, f.Role, f.Width = base.Page, base.Role, base.Width
		if f.Theme == "" {
			f.Theme = base.Theme
		}
		if e := a11yAllowed(r.allow, f); e != nil {
			e.Used++
			r.Allowed = append(r.Allowed, f)
			continue
		}
		r.Findings = append(r.Findings, f)
	}
}

func (r *a11yRun) addUnmeasured(base a11yFinding, fs []a11yFinding) {
	for _, f := range fs {
		f.Page, f.Role, f.Width = base.Page, base.Role, base.Width
		r.Unmeasured = append(r.Unmeasured, f)
	}
}

// counts returns the findings per check, sorted by name.
func a11yCounts(fs []a11yFinding) (names []string, counts map[string]int) {
	counts = map[string]int{}
	for _, f := range fs {
		counts[f.Check]++
	}
	for n := range counts {
		names = append(names, n)
	}
	sort.Strings(names)
	return names, counts
}

func a11yLine(f a11yFinding) string {
	return fmt.Sprintf("%s  %s/%s %s %dpx  %s  %q  %s", f.Check, f.Role, f.Page, f.Theme, f.Width, f.Selector, f.Text, f.Detail)
}

// write saves findings.json and summary.txt, and returns the folder.
func (r *a11yRun) write(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("TENDSHELF_A11Y_OUT")
	if dir == "" {
		dir = filepath.Join("..", "..", ".local", "tests", "a11y", r.ID)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	doc := map[string]any{
		"run": r.ID, "mode": r.Mode, "pages": r.Pages, "states": r.States,
		"findings": nilEmpty(r.Findings), "unmeasured": nilEmpty(r.Unmeasured), "allowed": nilEmpty(r.Allowed), "measuredTextNodes": r.Measured,
	}
	b, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "findings.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	var sum strings.Builder
	fmt.Fprintf(&sum, "run %s (%s): %d pages, %d states, %d text nodes measured\n", r.ID, r.Mode, len(r.Pages), len(r.States), r.Measured)
	names, counts := a11yCounts(r.Findings)
	for _, n := range names {
		fmt.Fprintf(&sum, "  %-24s %d\n", n, counts[n])
	}
	fmt.Fprintf(&sum, "findings %d, allowed %d, unmeasured %d\n", len(r.Findings), len(r.Allowed), len(r.Unmeasured))
	perPage := map[string]int{}
	for _, f := range r.Findings {
		perPage[f.Page]++
	}
	var keys []string
	for k := range perPage {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	sum.WriteString("per page:\n")
	for _, k := range keys {
		fmt.Fprintf(&sum, "  %-28s %d\n", k, perPage[k])
	}
	if err := os.WriteFile(filepath.Join(dir, "summary.txt"), []byte(sum.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func nilEmpty(fs []a11yFinding) []a11yFinding {
	if fs == nil {
		return []a11yFinding{}
	}
	return fs
}

// TestA11yAudit opens every page and state in the matrix, writes the findings file (also when it
// fails), and fails with the count of each check and the first 20 findings.
func TestA11yAudit(t *testing.T) {
	b := chromium(t)
	e := a11ySeed(t)
	widths, checkThemes := a11yMatrix()
	run := &a11yRun{ID: time.Now().UTC().Format("20060102T150405Z"), Mode: "full"}
	if a11yFast() {
		run.Mode = "fast"
	}
	var bad []string
	run.allow, bad = a11yParseAllow(a11yRead("allow.txt"))
	for _, m := range bad {
		t.Error(m)
	}
	// TENDSHELF_A11Y_ONLY=home,book narrows the run to those page and state keys (for fixing; the
	// allowlist check is then off). TENDSHELF_A11Y_TRACE=1 logs the seconds each visit took.
	var only map[string]bool
	if v := os.Getenv("TENDSHELF_A11Y_ONLY"); v != "" {
		only = map[string]bool{}
		for _, k := range strings.Split(v, ",") {
			only[strings.TrimSpace(k)] = true
		}
	}
	tabs := map[int]*cdp.Tab{}
	for _, w := range widths {
		tabs[w.W] = b.NewTab(w.W, w.H, w.W < 1280, true)
	}
	tab320 := b.NewTab(320, 640, true, true)
	defer func() {
		dir := run.write(t)
		t.Logf("findings: %s", filepath.Join(dir, "findings.json"))
		if run.Mode == "full" && only == nil {
			for _, en := range run.allow {
				if en.Used == 0 {
					t.Errorf("allow.txt line %d (%s %s %s) matches nothing in a full run", en.Line, en.Check, en.Page, en.Selector)
				}
			}
		}
		names, counts := a11yCounts(run.Findings)
		if len(names) == 0 {
			return
		}
		var lines []string
		for _, n := range names {
			lines = append(lines, fmt.Sprintf("%s=%d", n, counts[n]))
		}
		t.Errorf("%d findings (%s), %d allowed, %d text nodes over an image not measured; see %s", len(run.Findings), strings.Join(lines, ", "), len(run.Allowed), len(run.Unmeasured), dir)
		for i, f := range run.Findings {
			if i == 20 {
				break
			}
			t.Log(a11yLine(f))
		}
	}()

	// visit loads one address at every width and runs the checks (after prepare, if there is one).
	visit := func(key, role, url string, shell bool, minWidth int, prepare func(*cdp.Tab)) {
		for _, w := range widths {
			if w.W < minWidth {
				continue
			}
			tab := tabs[w.W]
			base := a11yFinding{Page: key, Role: role, Width: w.W}
			started := time.Now()
			trace := func(phase string) {
				if os.Getenv("TENDSHELF_A11Y_TRACE") == "1" {
					t.Logf("trace %s/%s %dpx %s: %.1fs", role, key, w.W, phase, time.Since(started).Seconds())
				}
			}
			status, err := a11yLoad(tab, url, shell)
			trace("loaded")
			switch {
			case err != nil:
				run.add(base, []a11yFinding{{Check: "load-error", Selector: "document", Detail: err.Error()}})
				continue
			case status != 200 && role == "member":
				run.addUnmeasured(base, []a11yFinding{{Check: "load-error", Selector: "document", Detail: fmt.Sprintf("status %d for the member, page skipped", status)}})
				continue
			case status != 200:
				run.add(base, []a11yFinding{{Check: "load-error", Selector: "document", Detail: fmt.Sprintf("status %d, want 200", status)}})
				continue
			}
			if prepare != nil {
				prepare(tab)
			}
			res, err := a11yCheck(tab, shell, checkThemes)
			trace("checked")
			if err != nil {
				run.add(base, []a11yFinding{{Check: "load-error", Selector: "document", Detail: err.Error()}})
				continue
			}
			if prepare != nil {
				// A state is a modal dialog over the page: the page behind it is inert, so the page-wide counts
				// (one h1, one main) belong to the page and are not asked of the dialog.
				kept := res.Findings[:0:0]
				for _, f := range res.Findings {
					if f.Check == "h1-count" || f.Check == "main-count" {
						f.Detail += " (modal state: the page behind is inert)"
						run.addUnmeasured(base, []a11yFinding{f})
						continue
					}
					kept = append(kept, f)
				}
				res.Findings = kept
			}
			// A2: target size, non-text contrast and sideways scroll; then the Tab walk (pages, at the phone
			// and desktop widths); then the same page with text at 200 %.
			layout, err := a11yLayout(tab, w.W, checkThemes)
			trace("layout")
			if err != nil {
				run.add(base, []a11yFinding{{Check: "load-error", Selector: "document", Detail: err.Error()}})
				continue
			}
			res.Findings = append(res.Findings, layout.Findings...)
			res.Unmeasured = append(res.Unmeasured, layout.Unmeasured...)
			touchOrDesktop := w.W == 390 || w.W == 1440
			if prepare == nil && touchOrDesktop {
				found, notes, err := a11yFocusWalk(t, tab, checkThemes)
				trace("walked")
				if err != nil {
					found = []a11yFinding{{Check: "load-error", Selector: "document", Detail: "focus walk: " + err.Error()}}
				}
				res.Findings = append(res.Findings, found...)
				res.Unmeasured = append(res.Unmeasured, notes...)
			}
			if touchOrDesktop {
				zoom := a11yTextZoom(tab)
				zoomed, zerr := a11yReflow(tab, w.W, "text-200-scroll-x", "text-200-clip")
				if zoom != nil || zerr != nil {
					zoomed.Findings = []a11yFinding{{Check: "load-error", Selector: "document", Detail: fmt.Sprintf("text at 200 %%: %v %v", zoom, zerr)}}
				}
				trace("zoomed")
				res.Findings = append(res.Findings, zoomed.Findings...)
				res.Unmeasured = append(res.Unmeasured, zoomed.Unmeasured...)
			}
			run.add(base, res.Findings)
			run.addUnmeasured(base, res.Unmeasured)
			run.Measured += res.Measured
		}
		// The narrowest window WCAG 1.4.10 names: 320 CSS px, a phone. A state that only exists on wider
		// windows is not opened here.
		if minWidth > 0 {
			return
		}
		base := a11yFinding{Page: key, Role: role, Width: 320}
		status, err := a11yLoad(tab320, url, shell)
		if err != nil || status != 200 {
			return // the wider visits above already report a page that does not load
		}
		if prepare != nil {
			prepare(tab320)
		}
		narrow, err := a11yReflow(tab320, 320, "reflow-320", "")
		if err != nil {
			run.add(base, []a11yFinding{{Check: "load-error", Selector: "document", Detail: err.Error()}})
			return
		}
		run.add(base, narrow.Findings)
	}

	for _, p := range a11yPages() {
		if only != nil && !only[p.Key] {
			continue
		}
		for _, role := range p.Roles {
			base := e.url(p.Fixture, role)
			if base == "" {
				t.Fatalf("page %s: no server for %s/%s", p.Key, p.Fixture, role)
			}
			run.Pages = append(run.Pages, role+"/"+p.Key)
			visit(p.Key, role, base+p.Path(e), p.Shell, 0, nil)
		}
	}
	for _, s := range a11yStates() {
		s := s
		if only != nil && !only[s.Key] {
			continue
		}
		run.States = append(run.States, s.Role+"/"+s.Key)
		fixture := s.Fixture
		if fixture == "" {
			fixture = "player"
		}
		visit(s.Key, s.Role, e.url(fixture, s.Role)+s.Path(e), fixture == "player", s.MinWidth, func(tab *cdp.Tab) { s.Prepare(t, tab, e) })
	}
}
