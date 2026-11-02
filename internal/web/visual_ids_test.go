//go:build !dev

package web

import "testing"

// seedVisualIDs is a no-op without -tags dev; the visual captures are made with that tag.
func seedVisualIDs(t *testing.T) {}
