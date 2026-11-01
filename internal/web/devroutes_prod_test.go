//go:build !dev

package web

import (
	"net/http"
	"testing"
)

// Release builds have no developer routes (decision in docs/roadmap.md, P0c).
func TestDevRoutesAbsent(t *testing.T) {
	h := NewHandler(Options{})
	for _, p := range []string{"/dev/kitchen-sink", "/dev/kitchen-sink/b", "/hx/dev/progress?v=1"} {
		if rec := send(h, http.MethodGet, p, nil, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", p, rec.Code)
		}
	}
}
