//go:build dev

package web

import (
	"fmt"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// a11yWalkCap is the most Tab presses one page gets. A page with more tabbable elements than this has
// its unreachable check left out (reported as unmeasured), not guessed.
const a11yWalkCap = 200

// a11yTab presses the Tab key once with real key events.
func a11yTab(tab *cdp.Tab) error {
	for _, typ := range []string{"keyDown", "keyUp"} {
		if err := tab.Call("Input.dispatchKeyEvent", map[string]any{"type": typ, "key": "Tab", "code": "Tab", "windowsVirtualKeyCode": 9}, nil); err != nil {
			return err
		}
	}
	return nil
}

// a11yStop is what focus.js reports about the element that has focus.
type a11yStop struct {
	None     bool   `json:"none"`
	ID       int    `json:"id"`
	Selector string `json:"selector"`
	Text     string `json:"text"`
	FocusVis bool   `json:"focusVisible"`
	Ring     bool   `json:"ring"`
	Obscured bool   `json:"obscured"`
	Mark     string `json:"mark"`
	Ratios   map[string]struct {
		Ratio  float64 `json:"ratio"`
		Detail string  `json:"detail"`
	} `json:"perTheme"`
}

// a11yStopFindings turns one stop into findings: whether the focus is shown, how well in each theme, and
// whether something covers it.
func a11yStopFindings(s a11yStop, checkThemes []string) []a11yFinding {
	base := a11yFinding{Selector: s.Selector, Text: s.Text, Mark: s.Mark}
	var out []a11yFinding
	add := func(f a11yFinding) {
		f.Selector, f.Text, f.Mark = base.Selector, base.Text, base.Mark
		out = append(out, f)
	}
	switch {
	case !s.FocusVis:
		add(a11yFinding{Check: "focus-not-visible", Detail: "the focused element does not match :focus-visible after a Tab"})
	case !s.Ring:
		add(a11yFinding{Check: "focus-not-visible", Detail: "no outline of 2 px or more and no box-shadow ring with a spread of 2 px or more"})
	default:
		for _, theme := range checkThemes {
			r, ok := s.Ratios[theme]
			if ok && r.Ratio < 3 {
				add(a11yFinding{Check: "focus-indicator-contrast", Theme: theme, Detail: r.Detail, Measured: r.Ratio, Required: 3})
			}
		}
	}
	if s.Obscured {
		add(a11yFinding{Check: "focus-obscured", Detail: "no sample point of the focused element can be hit: something covers it"})
	}
	return out
}

// a11yFocusWalk presses Tab through the loaded page: min(tabbables+5, 200) times, starting from the
// document. It returns the focus findings (focus-not-visible, focus-indicator-contrast, focus-obscured,
// focus-trap, focus-unreachable) and the notes for what it could not decide.
func a11yFocusWalk(t *testing.T, tab *cdp.Tab, checkThemes []string) (findings, unmeasured []a11yFinding, err error) {
	t.Helper()
	trapped := false
	var tabbables []struct {
		ID       int    `json:"id"`
		Selector string `json:"selector"`
	}
	if err = a11yScript(tab, "focus.js", map[string]any{"op": "tabbables"}, &tabbables); err != nil {
		return nil, nil, fmt.Errorf("focus.js tabbables: %w", err)
	}
	// Focus starts at the document: click the corner and drop what the click focused.
	var before string
	_ = tab.TryEval(`location.href`, &before)
	for _, typ := range []string{"mousePressed", "mouseReleased"} {
		if err = tab.Call("Input.dispatchMouseEvent", map[string]any{"type": typ, "x": 1, "y": 1, "button": "left", "clickCount": 1}, nil); err != nil {
			return nil, nil, err
		}
	}
	var after string
	_ = tab.TryEval(`location.href`, &after)
	if before != after {
		return nil, nil, fmt.Errorf("the click at the corner left %s", before)
	}
	_ = tab.TryEval(`(document.activeElement && document.activeElement.blur(), true)`, nil)

	presses := len(tabbables) + 5
	capped := presses > a11yWalkCap
	if capped {
		presses = a11yWalkCap
	}
	reached := map[int]bool{}
	var run []int // ids of the last stops
	for i := 0; i < presses; i++ {
		if err = a11yTab(tab); err != nil {
			return nil, nil, err
		}
		var s a11yStop
		if err = a11yScript(tab, "focus.js", map[string]any{"op": "stop", "themes": checkThemes}, &s); err != nil {
			return nil, nil, fmt.Errorf("focus.js stop: %w", err)
		}
		if s.None {
			run = append(run, 0)
			continue
		}
		first := !reached[s.ID]
		reached[s.ID] = true
		if first {
			findings = append(findings, a11yStopFindings(s, checkThemes)...)
		}
		run = append(run, s.ID)
		if n := len(run); n >= 4 && len(tabbables) > 1 && run[n-1] == run[n-2] && run[n-2] == run[n-3] && run[n-3] == run[n-4] {
			findings = append(findings, a11yFinding{Check: "focus-trap", Selector: s.Selector, Text: s.Text, Mark: s.Mark, Detail: "three Tab presses left focus on the same element"})
			trapped = true
			break
		}
	}
	if capped {
		unmeasured = append(unmeasured, a11yFinding{Check: "focus-unreachable", Selector: "document", Detail: fmt.Sprintf("%d tabbable elements, the walk stops at %d presses", len(tabbables), a11yWalkCap)})
		return findings, unmeasured, nil
	}
	for _, tb := range tabbables {
		if trapped {
			break
		}
		if !reached[tb.ID] {
			findings = append(findings, a11yFinding{Check: "focus-unreachable", Selector: tb.Selector, Detail: "a visible tabbable element that Tab never focused"})
		}
	}
	return findings, unmeasured, nil
}
