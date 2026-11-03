package static

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// A width media condition written as (min-width: 1280px) next to (max-width: 1279px) leaves a gap for
// fractional viewport widths (Firefox with display scaling reports 1279.5 px): neither side matches and
// the navigation vanishes. Range syntax with complementary bounds, (width < 1280px) and (width >= 1280px),
// leaves no gap. This guard fails on the old syntax in CSS, JS (matchMedia) and templ (media, sizes).
var legacyWidthQuery = regexp.MustCompile(`\((min|max)-width\s*:`)

func TestMediaQueriesUseRangeSyntax(t *testing.T) {
	var files []string
	for _, glob := range []string{"css/*.css", "js/*.js", "../components/*.templ", "../pages/*.templ"} {
		m, err := filepath.Glob(glob)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, m...)
	}
	if len(files) < 20 {
		t.Fatalf("only %d files scanned, the globs are wrong", len(files))
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if loc := legacyWidthQuery.FindIndex(b); loc != nil {
			t.Errorf("%s uses %q in a width query; write (width >= Npx) and (width < Npx)", f, b[loc[0]:loc[1]])
		}
	}
}
