package static

import (
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"
)

// TestBuildAndPrecache: the build id follows the version and the asset bytes, and the precache list is
// every hashed asset but the notices and the worker script.
func TestBuildAndPrecache(t *testing.T) {
	s := mustLoad(t)
	b := s.Build("v1")
	if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(b) {
		t.Fatalf("build %q is not 12 hex", b)
	}
	if b != s.Build("v1") || b == s.Build("v2") {
		t.Errorf("build must be stable and follow the version")
	}
	other := &Set{byName: map[string]*Asset{}, byHashed: map[string]*Asset{}}
	if err := other.add("css/x.css", []byte("a{}"), "text/css"); err != nil {
		t.Fatal(err)
	}
	if other.Build("v1") == b {
		t.Errorf("a different asset set gave the same build")
	}
	pre := s.Precache()
	if !sort.StringsAreSorted(pre) || len(pre) == 0 {
		t.Fatalf("precache not sorted or empty: %d", len(pre))
	}
	for _, u := range pre {
		if strings.HasSuffix(u, ".txt") || strings.Contains(u, "/js/sw.") {
			t.Errorf("precache lists %s", u)
		}
	}
	if a, ok := s.Asset("css/bundle.css"); !ok || !slices.Contains(pre, "/static/"+a.Hashed) {
		t.Errorf("precache lacks bundle.css")
	}
	// Only the reader page uses its stylesheet, so it is not kept for offline use.
	if a, ok := s.Asset("css/pages-reader.css"); !ok || slices.Contains(pre, "/static/"+a.Hashed) {
		t.Errorf("pages-reader.css must be an asset and stay out of the precache")
	}
}
