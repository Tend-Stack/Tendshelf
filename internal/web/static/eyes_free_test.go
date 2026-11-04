package static

import (
	"slices"
	"testing"
)

// TestEyesFreePrecache: Eyes-free loads its script and stylesheet on first use, so the worker must
// already hold both for the car, where the signal comes and goes (decision 0013).
func TestEyesFreePrecache(t *testing.T) {
	s := mustLoad(t)
	pre := s.Precache()
	for _, name := range []string{"js/eyes-free.js", "css/pages-eyes-free.css"} {
		a, ok := s.Asset(name)
		if !ok {
			t.Fatalf("%s is not an asset", name)
		}
		if !slices.Contains(pre, "/static/"+a.Hashed) {
			t.Errorf("%s (/static/%s) is not in Precache()", name, a.Hashed)
		}
	}
}
