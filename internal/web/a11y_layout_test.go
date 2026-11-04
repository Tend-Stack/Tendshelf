//go:build dev

package web

import (
	"fmt"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// a11yTargetMin is the smallest touch target in CSS px at a viewport width: 44 on the touch layouts
// (390, 768, 1024; AGENTS.md), the WCAG 2.5.8 minimum of 24 at desktop width. Operator decision Q1 in
// docs/plans/m3c-audit.md; this is the only place the numbers live.
func a11yTargetMin(width int) float64 {
	if width >= 1280 {
		return 24
	}
	return 44
}

// a11yLayout runs the checks that read geometry and the non-text colours: target size (in dark, theme
// does not change geometry), non-text contrast in each of the themes, and sideways scrolling.
func a11yLayout(tab *cdp.Tab, width int, checkThemes []string) (a11yResult, error) {
	var res a11yResult
	if err := a11yTheme(tab, "dark"); err != nil {
		return res, err
	}
	var targets struct {
		Findings []a11yFinding
		Measured int
	}
	if err := a11yScript(tab, "targets.js", map[string]any{"min": a11yTargetMin(width)}, &targets); err != nil {
		return res, fmt.Errorf("targets.js: %w", err)
	}
	for _, f := range targets.Findings {
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
		if err := a11yScript(tab, "noncontrast.js", map[string]any{"theme": theme}, &c); err != nil {
			return res, fmt.Errorf("noncontrast.js (%s): %w", theme, err)
		}
		for _, f := range c.Findings {
			f.Theme = theme
			res.Findings = append(res.Findings, f)
		}
		for _, f := range c.Unmeasured {
			f.Theme = theme
			res.Unmeasured = append(res.Unmeasured, f)
		}
	}
	if err := a11yTheme(tab, "dark"); err != nil {
		return res, err
	}
	reflow, err := a11yReflow(tab, width, "scroll-x", "")
	if err != nil {
		return res, err
	}
	res.Findings = append(res.Findings, reflow.Findings...)
	return res, nil
}

// a11yReflow runs reflow.js on the loaded page, in a window of the given width, and names what it finds: a page wider than the window is a
// finding of check scrollCheck; clipped text is a finding of check clipCheck (left out when clipCheck is
// empty), and text cut off on purpose is unmeasured.
func a11yReflow(tab *cdp.Tab, width int, scrollCheck, clipCheck string) (a11yResult, error) {
	var res a11yResult
	var r struct {
		ScrollX *a11yFinding
		Clips   []a11yFinding
		Trunc   []a11yFinding `json:"truncated"`
	}
	if err := a11yScript(tab, "reflow.js", map[string]any{"width": width}, &r); err != nil {
		return res, fmt.Errorf("reflow.js: %w", err)
	}
	if r.ScrollX != nil {
		f := *r.ScrollX
		f.Check, f.Theme = scrollCheck, "dark"
		res.Findings = append(res.Findings, f)
	}
	if clipCheck != "" {
		for _, f := range r.Clips {
			f.Check, f.Theme = clipCheck, "dark"
			res.Findings = append(res.Findings, f)
		}
		for _, f := range r.Trunc {
			f.Check, f.Theme = clipCheck, "dark"
			res.Unmeasured = append(res.Unmeasured, f)
		}
	}
	return res, nil
}

// a11yTextZoom sets the root font size to 200 % (through the CSSOM, which the CSP allows) so text sits at
// twice its size; the next navigation puts it back.
func a11yTextZoom(tab *cdp.Tab) error {
	return tab.TryEval(`(document.documentElement.style.fontSize = "200%", void document.body.offsetHeight, true)`, nil)
}
