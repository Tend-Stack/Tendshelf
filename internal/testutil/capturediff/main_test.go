package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCompare(t *testing.T) {
	a, b := t.TempDir(), t.TempDir()
	w := func(dir, name, body string) {
		p := filepath.Join(dir, name)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	w(a, "x/one.png", "1")
	w(b, "x/one.png", "1")
	w(a, "two.png", "2")
	w(b, "two.png", "3")
	w(a, "only.png", "4")
	ident, total, bad, err := compare(a, b)
	if err != nil || ident != 1 || total != 3 || len(bad) != 2 {
		t.Fatalf("got %d of %d, %v, %v", ident, total, bad, err)
	}
}
