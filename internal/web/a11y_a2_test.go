//go:build dev

package web

import (
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/Tend-Stack/tendshelf/internal/testutil/cdp"
)

// TestA11yA2TargetMin needs no browser: the size asked of a target at each width.
func TestA11yA2TargetMin(t *testing.T) {
	for width, want := range map[int]float64{320: 44, 390: 44, 768: 44, 1024: 44, 1279: 44, 1280: 24, 1440: 24} {
		if got := a11yTargetMin(width); got != want {
			t.Errorf("a11yTargetMin(%d) = %v, want %v", width, got, want)
		}
	}
}

// TestA11yA2HarnessCatches proves the A2 checks see what they claim to see. fixture-a2.html plants one
// element for each check, marked data-case, with the checks that must fire on it in data-expect ("none"
// for the passing twins). The small fixtures a2b, a2c and a2d plant the Tab-walk cases that need a page of
// their own: a page of tabindex -1 only, a skipped stop and a trap.
func TestA11yA2HarnessCatches(t *testing.T) {
	b := chromium(t)
	srv := httptest.NewServer(http.FileServer(http.Dir("testdata/a11y")))
	t.Cleanup(srv.Close)
	tab := b.NewTab(1024, 800, false, true)
	tab320 := b.NewTab(320, 640, true, true)
	allThemes := themes4()

	checksByCase := func(fs []a11yFinding) map[string]string {
		sets := map[string]map[string]bool{}
		for _, f := range fs {
			if sets[f.Mark] == nil {
				sets[f.Mark] = map[string]bool{}
			}
			sets[f.Mark][f.Check] = true
		}
		out := map[string]string{}
		for mark, set := range sets {
			var names []string
			for n := range set {
				names = append(names, n)
			}
			sort.Strings(names)
			out[mark] = strings.Join(names, " ")
		}
		return out
	}
	load := func(t *testing.T, on *cdp.Tab, page string) {
		t.Helper()
		if status, err := a11yLoad(on, srv.URL+"/"+page, false); err != nil || status != 200 {
			t.Fatalf("load %s: %d, %v", page, status, err)
		}
	}

	t.Run("fixture-a2", func(t *testing.T) {
		load(t, tab, "fixture-a2.html")
		var cases []struct{ Case, Expect, Unmeasured string }
		tab.Eval(`[...document.querySelectorAll("[data-case]")].map((el) => ({Case: el.dataset.case, Expect: el.dataset.expect, Unmeasured: el.dataset.expectUnmeasured || ""}))`, &cases)
		if len(cases) < 20 {
			t.Fatalf("the fixture lists %d cases", len(cases))
		}

		// Geometry and non-text colour at 1024 (44 px), the Tab walk, the clip check at 200 % text.
		layout, err := a11yLayout(tab, 1024, allThemes)
		if err != nil {
			t.Fatal(err)
		}
		found := append([]a11yFinding(nil), layout.Findings...)
		unmeasured := append([]a11yFinding(nil), layout.Unmeasured...)
		walked, notes, err := a11yFocusWalk(t, tab, allThemes)
		if err != nil {
			t.Fatal(err)
		}
		found = append(found, walked...)
		unmeasured = append(unmeasured, notes...)
		zoomed, err := a11yReflow(tab, 1024, "text-200-scroll-x", "text-200-clip")
		if err != nil {
			t.Fatal(err)
		}
		found = append(found, zoomed.Findings...)
		unmeasured = append(unmeasured, zoomed.Unmeasured...)

		// The 320 px window: only the 400 px box sticks out.
		load(t, tab320, "fixture-a2.html")
		narrow, err := a11yReflow(tab320, 320, "reflow-320", "")
		if err != nil {
			t.Fatal(err)
		}
		found = append(found, narrow.Findings...)

		got := checksByCase(found)
		for _, c := range cases {
			want := c.Expect
			if want == "none" {
				want = ""
			}
			if got[c.Case] != want {
				t.Errorf("case %s: checks %q, want %q", c.Case, got[c.Case], want)
			}
		}
		if got[""] != "" {
			t.Errorf("findings with no case: %q", got[""])
		}
		for _, c := range cases {
			seen := false
			for _, u := range unmeasured {
				seen = seen || u.Mark == c.Case
			}
			if c.Unmeasured != "" && !seen {
				t.Errorf("case %s: not listed as unmeasured", c.Case)
			}
		}
		// The weak ring fails in every theme (one finding each); the target finding carries the 2.5.8 flag.
		weak := 0
		for _, f := range found {
			if f.Check == "focus-indicator-contrast" && f.Mark == "f-weak" {
				weak++
			}
			if f.Check == "target-size" && f.Mark == "t-bare" && (f.Wcag258 == nil || !*f.Wcag258 || f.Detail != "32x32") {
				t.Errorf("t-bare: detail %q, wcag258 %v", f.Detail, f.Wcag258)
			}
		}
		if weak != len(allThemes) {
			t.Errorf("f-weak: %d contrast findings, want one per theme (%d)", weak, len(allThemes))
		}
	})

	t.Run("target-min-at-desktop", func(t *testing.T) {
		// At 24 px the bare 32x32 button passes: the same page, the desktop minimum.
		load(t, tab, "fixture-a2.html")
		layout, err := a11yLayout(tab, 1440, []string{"dark"})
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range layout.Findings {
			if f.Check == "target-size" {
				t.Errorf("target-size at 1440: %s %s", f.Mark, f.Detail)
			}
		}
	})

	for _, c := range []struct{ page, want string }{
		{"fixture-a2b.html", ""},
		{"fixture-a2c.html", "focus-unreachable"},
		{"fixture-a2d.html", "focus-trap"},
	} {
		c := c
		t.Run(c.page, func(t *testing.T) {
			load(t, tab, c.page)
			found, _, err := a11yFocusWalk(t, tab, allThemes)
			if err != nil {
				t.Fatal(err)
			}
			var checks []string
			for _, f := range found {
				checks = append(checks, f.Check)
			}
			sort.Strings(checks)
			if got := strings.Join(checks, " "); got != c.want {
				t.Errorf("checks %q, want %q (%v)", got, c.want, found)
			}
			if c.want == "focus-unreachable" && found[0].Selector != "#second" {
				t.Errorf("unreachable %q, want #second", found[0].Selector)
			}
		})
	}
}
