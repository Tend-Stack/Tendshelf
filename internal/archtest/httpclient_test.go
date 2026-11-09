// Package archtest holds tests of the code base's shape rather than its behaviour.
package archtest

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHTTPClientOnlyWhereAllowed: AGENTS.md says the server makes outbound requests from one place
// (internal/metadata/online), plus the desktop shell's health check and the sign-in provider client
// (decision 0037). A new http.Client anywhere else is a new outbound connection and needs a decision.
// internal/testutil is test tooling that never ships (the browser driver talks to a local Chromium).
func TestHTTPClientOnlyWhereAllowed(t *testing.T) {
	allowed := []string{
		"internal/metadata/online/",
		"internal/desktopshell/",
		"internal/auth/oidc/", // oidctest, the fake provider, lives below it
		"internal/testutil/",
	}
	root := filepath.Join("..", "..")
	var found int
	for _, top := range []string{"internal", "cmd"} {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			src := string(raw)
			if !strings.Contains(src, "http.Client{") && !strings.Contains(src, "new(http.Client)") {
				return nil
			}
			found++
			rel, _ := filepath.Rel(root, path)
			rel = filepath.ToSlash(rel)
			for _, a := range allowed {
				if strings.HasPrefix(rel, a) {
					return nil
				}
			}
			t.Errorf("%s constructs an http.Client; only %s may (decision 0037)", rel, strings.Join(allowed, ", "))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if found < 3 {
		t.Fatalf("found %d http.Client constructions, expected at least the three allowed ones: the walk is broken", found)
	}
}
