//go:build dev

package web

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
)

// TestA11yHarnessCatches proves the audit sees what it claims to see. fixture-a1.html plants one
// element for each check, marked data-case, with the checks that must fire on it in data-expect (and
// "none" for the ones that must stay quiet: a pair that passes, screen-reader-only text, a disabled
// button). The page-level checks are compared on the whole page. fixture-a1b.html carries the page
// defects (no lang, no title, zoom off, no landmarks) and is read as a shell page.
func TestA11yHarnessCatches(t *testing.T) {
	b := chromium(t)
	srv := httptest.NewServer(http.FileServer(http.Dir("testdata/a11y")))
	t.Cleanup(srv.Close)
	tab := b.NewTab(1024, 800, false, true)
	checkThemes := []string{"dark", "contrast"}

	setOf := func(checks []string) string {
		sort.Strings(checks)
		return strings.Join(checks, " ")
	}

	t.Run("fixture-a1", func(t *testing.T) {
		if status, err := a11yLoad(tab, srv.URL+"/fixture-a1.html", false); err != nil || status != 200 {
			t.Fatalf("load: %d, %v", status, err)
		}
		res, err := a11yCheck(tab, false, checkThemes)
		if err != nil {
			t.Fatal(err)
		}
		if res.Measured == 0 {
			t.Error("no text was measured")
		}
		var cases []struct{ Case, Expect, Contrast, Unmeasured string }
		tab.Eval(`[...document.querySelectorAll("[data-case]")].map((el) => ({Case: el.dataset.case, Expect: el.dataset.expect,
		  Contrast: el.dataset.expectContrast || "", Unmeasured: el.dataset.expectUnmeasured || ""}))`, &cases)
		if len(cases) < 20 {
			t.Fatalf("the fixture lists %d cases", len(cases))
		}
		for _, theme := range checkThemes {
			// Structure and tree checks run once (in dark); contrast runs in each theme.
			got := map[string][]string{}
			for _, f := range res.Findings {
				if f.Check == "contrast-text" && f.Theme != theme {
					continue
				}
				if f.Check != "contrast-text" && f.Theme != "dark" {
					continue
				}
				got[f.Mark] = append(got[f.Mark], f.Check)
			}
			unmeasured := map[string]bool{}
			for _, f := range res.Unmeasured {
				if f.Theme == theme {
					unmeasured[f.Mark] = true
				}
			}
			for _, c := range cases {
				expect := c.Expect
				if theme == "contrast" && c.Contrast != "" {
					expect = c.Contrast
				}
				want := []string{}
				if expect != "none" {
					want = strings.Fields(expect)
				}
				if w, g := setOf(want), setOf(got[c.Case]); w != g {
					t.Errorf("%s, case %s: checks %q, want %q", theme, c.Case, g, w)
				}
				if (c.Unmeasured != "") != unmeasured[c.Case] {
					t.Errorf("%s, case %s: unmeasured %v, want %v", theme, c.Case, unmeasured[c.Case], c.Unmeasured != "")
				}
			}
			// Page level (no case): only the two h1.
			if g := setOf(got[""]); g != "h1-count" {
				t.Errorf("%s, page level: %q, want %q", theme, g, "h1-count")
			}
			for _, f := range res.Findings {
				if f.Mark != "" && !strings.Contains(strings.Join(caseNames(cases), " "), f.Mark) {
					t.Errorf("finding for an unknown case %q", f.Mark)
				}
			}
		}
	})

	t.Run("fixture-a1b", func(t *testing.T) {
		if status, err := a11yLoad(tab, srv.URL+"/fixture-a1b.html", false); err != nil || status != 200 {
			t.Fatalf("load: %d, %v", status, err)
		}
		res, err := a11yCheck(tab, true, []string{"dark"})
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, f := range res.Findings {
			got = append(got, f.Check)
		}
		want := "h1-count lang live-region-missing main-count nav-missing title viewport-zoom"
		if g := setOf(got); g != want {
			t.Errorf("page defects: %q, want %q", g, want)
		}
	})
}

func caseNames(cases []struct{ Case, Expect, Contrast, Unmeasured string }) []string {
	var out []string
	for _, c := range cases {
		out = append(out, c.Case)
	}
	return out
}
