//go:build linux && !gtk3

package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestWindowUniquePath checks the rule for a download name: a bare file name, and " (n)" before
// the extension when the name is taken.
func TestWindowUniquePath(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"notes.md", "notes (1).md", "plain", ".hidden"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	tests := []struct {
		name      string
		suggested string
		want      string
	}{
		{"free name", "book.md", "book.md"},
		{"taken twice", "notes.md", "notes (2).md"},
		{"no extension", "plain", "plain (1)"},
		{"dot file keeps its name", ".hidden", ".hidden (1)"},
		{"never leaves the folder", "../../etc/passwd", "passwd"},
		{"empty", "", "download"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := uniquePath(dir, tt.suggested), filepath.Join(dir, tt.want); got != want {
				t.Errorf("uniquePath(%q) = %q, want %q", tt.suggested, got, want)
			}
		})
	}
}
